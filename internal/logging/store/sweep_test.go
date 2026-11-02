package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// writeDays writes n records in each of the given days back from base for cat, then closes the writers so
// file sizes are stable.
func writeDays(t *testing.T, s *Store, cat logging.Category, days int) {
	t.Helper()
	for d := 0; d < days; d++ {
		var recs []logging.Record
		for i := 0; i < 50; i++ {
			recs = append(recs, rec(base.Add(-time.Duration(d)*24*time.Hour+time.Duration(i)*time.Second), d*100+i, cat, logging.LevelInfo,
				"payload record for size", func(r *logging.Record) { r.Attrs = map[string]any{"pad": "0123456789abcdef0123456789abcdef"} }))
		}
		write(t, s, recs...)
	}
	s.mu.Lock()
	for k, w := range s.writers {
		_ = w.close()
		delete(s.writers, k)
	}
	s.mu.Unlock()
}

func dayNames(t *testing.T, s *Store, cat logging.Category) []string {
	t.Helper()
	files, err := s.days(cat)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		out = append(out, f.day)
	}
	return out
}

func settingsWith(mut func(*logging.Settings)) func() logging.Settings {
	return func() logging.Settings {
		st := logging.DefaultSettings()
		mut(&st)
		return st
	}
}

func setCat(st *logging.Settings, c logging.Category, f func(*logging.CategorySettings)) {
	cs := st.Categories[c]
	f(&cs)
	st.Categories[c] = cs
}

func TestSweepRetention(t *testing.T) {
	c := &capture{}
	s := openTest(t, func(o *Options) {
		o.Log = c.logger()
		o.Settings = settingsWith(func(st *logging.Settings) {
			setCat(st, logging.CatAPI, func(cs *logging.CategorySettings) { cs.Retention = 2 * 24 * time.Hour })
		})
	})
	writeDays(t, s, logging.CatAPI, 5)  // 10-16 .. 10-20
	writeDays(t, s, logging.CatSync, 5) // default 14 d retention: nothing deleted
	del, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// now = 10-20 12:00, retention 2 d → cut 10-18 12:00: days ending at or before it go (10-16, 10-17)
	if got := dayNames(t, s, logging.CatAPI); len(got) != 3 || got[0] != "2026-10-18" {
		t.Fatalf("api days left %v", got)
	}
	if len(dayNames(t, s, logging.CatSync)) != 5 {
		t.Fatal("sync must keep its days")
	}
	if len(del) != 2 || del[0].Reason != ReasonRetention || del[0].Bytes <= 0 {
		t.Fatalf("deletions %+v", del)
	}
	if c.count("log day file deleted") != 2 || c.count(`"reason":"retention"`) != 2 || c.count(`"logCategory":"api"`) != 2 {
		t.Fatalf("deletion records: %v", c.msgs)
	}
	if del, err := s.Sweep(context.Background()); err != nil || len(del) != 0 {
		t.Fatalf("second sweep %v %v", del, err)
	}
}

func TestSweepCategoryCapKeepsToday(t *testing.T) {
	s := openTest(t, nil)
	writeDays(t, s, logging.CatSend, 4)
	files, _ := s.days(logging.CatSend)
	one := files[0].bytes
	s.o.Settings = settingsWith(func(st *logging.Settings) {
		setCat(st, logging.CatSend, func(cs *logging.CategorySettings) { cs.Cap = 2*one + one/2 })
	})
	del, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := dayNames(t, s, logging.CatSend); len(got) != 2 || got[1] != "2026-10-20" {
		t.Fatalf("send days left %v (deleted %+v)", got, del)
	}
	for _, d := range del {
		if d.Reason != ReasonCategoryCap {
			t.Fatalf("reason %s", d.Reason)
		}
	}
	// a cap smaller than today's file never deletes today
	s.o.Settings = settingsWith(func(st *logging.Settings) {
		setCat(st, logging.CatSend, func(cs *logging.CategorySettings) { cs.Cap = 1 })
	})
	if _, err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayNames(t, s, logging.CatSend); len(got) != 1 || got[0] != "2026-10-20" {
		t.Fatalf("today must survive: %v", got)
	}
}

func TestSweepDiskCapEvictsByPriority(t *testing.T) {
	s := openTest(t, nil)
	writeDays(t, s, logging.CatProtocol, 3) // lowest priority
	writeDays(t, s, logging.CatAPI, 3)
	writeDays(t, s, logging.CatAudit, 3) // highest priority
	usage, _ := s.DiskUsage()
	total := usage[logging.CatProtocol] + usage[logging.CatAPI] + usage[logging.CatAudit]
	files, _ := s.days(logging.CatAPI)
	// room for everything but three old days: protocol loses its two old days first, then api one
	s.o.Settings = settingsWith(func(st *logging.Settings) { st.DiskCap = total - 2*files[0].bytes - files[0].bytes/2 })
	del, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := dayNames(t, s, logging.CatProtocol); len(got) != 1 {
		t.Fatalf("protocol days %v", got)
	}
	if got := dayNames(t, s, logging.CatAPI); len(got) != 2 || got[0] != "2026-10-19" {
		t.Fatalf("api days %v", got)
	}
	if got := dayNames(t, s, logging.CatAudit); len(got) != 3 {
		t.Fatalf("audit must be untouched: %v", got)
	}
	if len(del) != 3 || del[0].Reason != ReasonDiskCap || del[0].Category != logging.CatProtocol || del[2].Category != logging.CatAPI {
		t.Fatalf("deletions %+v", del)
	}
}

func TestSweepSkipsFilesInUse(t *testing.T) {
	s := openTest(t, func(o *Options) {
		o.Settings = settingsWith(func(st *logging.Settings) {
			setCat(st, logging.CatJob, func(cs *logging.CategorySettings) { cs.Retention = time.Hour })
		})
	})
	writeDays(t, s, logging.CatJob, 2)
	_, release, ok, err := s.openReader(logging.CatJob, "2026-10-19")
	if err != nil || !ok {
		t.Fatal(err)
	}
	del, err := s.Sweep(context.Background())
	if !errors.Is(err, ErrFileBusy) || len(del) != 0 {
		t.Fatalf("busy file: %v %v", del, err)
	}
	release()
	if del, err := s.Sweep(context.Background()); err != nil || len(del) != 1 {
		t.Fatalf("retry: %v %v", del, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writeDays(t, s, logging.CatJob, 3)
	if _, err := s.Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sweep: %v", err)
	}
}
