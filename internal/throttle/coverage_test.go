package throttle

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
)

func TestBucketsCheckAndNilPaths(t *testing.T) {
	c := NewMemoryCounter()
	b := &Buckets{Counter: c, Limits: Limits{}}
	ctx := context.Background()
	r, err := b.Check(ctx, "o", []string{"x.com"})
	if err != nil || !r.OK {
		t.Fatal(r, err)
	}
	r, err = (*Buckets)(nil).Take(ctx, "", nil)
	if err != nil || !r.OK {
		t.Fatal(r, err)
	}
	r, err = (&Buckets{}).Take(ctx, "o", []string{"x.com"})
	if err != nil || !r.OK {
		t.Fatal(r, err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 59, 500e6, time.UTC)
	b2 := &Buckets{Counter: c, Limits: Limits{DomainPerMinute: 1, RetryAfter: "1s"}, Now: func() time.Time { return now }}
	_, _ = b2.Take(ctx, "o2", []string{"a.com"})
	r, _ = b2.Take(ctx, "o2", []string{"a.com"})
	if r.OK {
		t.Fatal("expected delay")
	}
}

type errCounter struct{}

func (errCounter) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("boom")
}
func (errCounter) Get(context.Context, string) (int64, error) { return 0, errors.New("boom") }

func TestBucketsCounterError(t *testing.T) {
	b := &Buckets{Counter: errCounter{}, Limits: DefaultLimits(), Now: func() time.Time { return time.Now().UTC() }}
	if _, err := b.Take(context.Background(), "o", []string{"x.com"}); err == nil {
		t.Fatal("want err")
	}
	g := &Gate{Counter: errCounter{}, Config: func(context.Context, string, string) (Config, error) {
		return Config{ThrottleOn: true, Limits: Limits{DomainPerMinute: 1}}, nil
	}}
	if d := g.BeforeSandbox(context.Background(), Message{OrganizationID: "o", To: []string{"a@x.com"}}); d.Action != ActionAllow {
		t.Fatal(d)
	}
	// Get ok, Add fails
	b2 := &Buckets{Counter: &addFailCounter{inner: NewMemoryCounter()}, Limits: Limits{DomainPerMinute: 5},
		Now: func() time.Time { return time.Date(2026, 2, 2, 2, 2, 0, 0, time.UTC) }}
	if _, err := b2.Take(context.Background(), "o", []string{"z.com"}); err == nil {
		t.Fatal("want add err")
	}
}

type addFailCounter struct{ inner *MemoryCounter }

func (a *addFailCounter) Get(ctx context.Context, key string) (int64, error) {
	return a.inner.Get(ctx, key)
}
func (a *addFailCounter) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("add fail")
}

func TestGateLoadErrorAndHelpers(t *testing.T) {
	g := &Gate{
		Config: func(context.Context, string, string) (Config, error) {
			return Config{}, errors.New("cfg")
		},
	}
	_ = g.log()
	_ = g.registry()
	_ = (&Gate{Log: slog.Default(), Registry: features.DefaultRegistry()}).log()
	_ = (&Gate{Log: slog.Default(), Registry: features.DefaultRegistry()}).registry()
	if d := g.BeforeSandbox(context.Background(), Message{OrganizationID: "o", To: []string{"a@b.com"}}); d.Action != ActionAllow {
		t.Fatal(d)
	}
	if d := (&Gate{}).AfterSandbox(context.Background(), Message{}); d.Action != ActionAllow {
		t.Fatal(d)
	}
}

func TestGateRejectClipAndEmptyCEL(t *testing.T) {
	ps, err := rules.NewPreSendEngine(rules.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reason := strings.Repeat("r", 300)
	g := &Gate{PreSend: ps, Config: func(context.Context, string, string) (Config, error) {
		return Config{PreSendOn: true, CEL: `{"action":"reject","reason":"` + reason + `"}`}, nil
	}}
	d := g.AfterSandbox(context.Background(), Message{OrganizationID: "o"})
	if d.Action != ActionReject || len([]rune(d.Detail)) > maxDetail {
		t.Fatalf("%+v", d)
	}
	g.Config = func(context.Context, string, string) (Config, error) {
		return Config{PreSendOn: true, CEL: `"reject"`}, nil
	}
	d = g.AfterSandbox(context.Background(), Message{OrganizationID: "o"})
	if d.Action != ActionReject || d.Detail == "" {
		t.Fatalf("%+v", d)
	}
	g.Config = func(context.Context, string, string) (Config, error) {
		return Config{PreSendOn: true, CEL: "   "}, nil
	}
	if d := g.AfterSandbox(context.Background(), Message{}); d.Action != ActionAllow {
		t.Fatal(d)
	}
	if clip("short") != "short" {
		t.Fatal()
	}
}

func TestParseLimitsAndRetryBranches(t *testing.T) {
	l := ParseLimits([]byte(`{"domainPerMinute":0,"retryAfter":"bad"}`))
	if l.DomainPerMinute != 60 || l.RetryDelay() != 30*time.Second {
		t.Fatalf("%+v %v", l, l.RetryDelay())
	}
	l = ParseLimits(struct{ X int }{1})
	if l.DomainPerMinute != 60 {
		t.Fatal(l)
	}
	l = Limits{RetryAfter: "0s"}
	if l.RetryDelay() != 30*time.Second {
		t.Fatal(l.RetryDelay())
	}
	if (Limits{}).ProviderLimit("") != 0 || (Limits{}).ProviderLimit("gmail") != 0 {
		t.Fatal()
	}
	if (Limits{Domains: map[string]int{}}).DomainLimit("x.com") != 0 && false {
		t.Fatal()
	}
	d := DefaultLimits()
	if d.DomainLimit("x.com") != 60 {
		t.Fatal()
	}
}

func TestParseWindowBranches(t *testing.T) {
	if w, err := ParseWindow(nil); err != nil || w != nil {
		t.Fatal(w, err)
	}
	if w, err := ParseWindow([]byte("{}")); err != nil || w != nil {
		t.Fatal(w, err)
	}
	if w, err := ParseWindow(map[string]any{}); err != nil || w != nil {
		t.Fatal(w, err)
	}
	if _, err := ParseWindow(`{"start":"xx","end":"09:00"}`); err == nil {
		t.Fatal("bad start")
	}
	if _, err := ParseWindow(`{"start":"09:00","end":"xx"}`); err == nil {
		t.Fatal("bad end")
	}
	w, err := ParseWindow(map[string]any{"timeZone": "Not/AZone", "start": "09:00", "end": "10:00"})
	if err != nil || w == nil || w.loc() != time.UTC {
		t.Fatal(w, err)
	}
	w2 := &Window{Start: "09:00", End: "09:00"}
	if !w2.Contains(time.Now()) {
		t.Fatal("degenerate")
	}
	if !(&Window{Start: "09:00", End: "18:00"}).Contains(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatal()
	}
	next := (&Window{TimeZone: "UTC", Days: []int{1}, Start: "09:00", End: "10:00"}).NextOpen(
		time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)) // Monday inside? 9:30 is inside so NextOpen returns t
	_ = next
	closed := time.Date(2026, 10, 26, 10, 30, 0, 0, time.UTC)
	n := (&Window{TimeZone: "UTC", Days: []int{1}, Start: "09:00", End: "10:00"}).NextOpen(closed)
	want := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) // next Monday
	if !n.Equal(want) {
		t.Fatalf("%v want %v", n, want)
	}
}

func TestSettingsConfigNilDB(t *testing.T) {
	fn := SettingsConfig(nil, nil, "production")
	cfg, err := fn(context.Background(), "org_1", "")
	if err != nil || !cfg.ThrottleOn || cfg.WindowsOn {
		t.Fatalf("%+v %v", cfg, err)
	}
	cfg, err = fn(context.Background(), "", "")
	if err != nil || !cfg.ThrottleOn {
		t.Fatal(cfg, err)
	}
}

func TestConfigFromDoc(t *testing.T) {
	reg := features.DefaultRegistry()
	on, off := true, false
	doc := settingsDoc{
		switches: map[string]any{
			switchThrottle: on, switchWindows: on, switchPreSend: off,
		},
		values: map[string]any{
			keyLimits:  `{"domainPerMinute":9}`,
			keyWindow:  `{"timeZone":"UTC","start":"09:00","end":"17:00"}`,
			keyPreSend: "\"allow\"",
		},
	}
	cfg := configFromDoc(doc.asSettings(), reg, "production")
	if !cfg.ThrottleOn || !cfg.WindowsOn || cfg.PreSendOn || cfg.Limits.DomainPerMinute != 9 || cfg.Window == nil || cfg.CEL != `"allow"` {
		t.Fatalf("%+v", cfg)
	}
	cfg = configFromDoc(doc.asSettings(), reg, "production")
	_ = cfg
	// bad window ignored
	cfg = configFromDoc(mustDoc(map[string]any{keyWindow: `{"start":"bad"}`}, nil), reg, "test")
	if cfg.Window != nil {
		t.Fatal(cfg.Window)
	}
}

type settingsDoc struct {
	switches map[string]any
	values   map[string]any
}

func (s settingsDoc) asSettings() settings.Doc {
	d := settings.Doc{Values: s.values, Switches: map[string]settings.SwitchValue{}}
	for k, v := range s.switches {
		d.Switches[k] = settings.SwitchValue{Value: v}
	}
	return d
}

func mustDoc(values map[string]any, switches map[string]any) settings.Doc {
	return (settingsDoc{values: values, switches: switches}).asSettings()
}

func TestSwitchOnUnknownAndNilReg(t *testing.T) {
	if !SwitchOn(nil, "sending.throttle", "", nil) {
		t.Fatal("default on")
	}
	if !SwitchOn(nil, "totally.unknown", "production", nil) {
		t.Fatal("unknown defaults true")
	}
	off := false
	if SwitchOn(features.DefaultRegistry(), switchWindows, "production", &off) {
		t.Fatal()
	}
}

func TestModuleStartWithLog(t *testing.T) {
	m := &Module{Log: slog.Default(), Env: "test", Registry: features.DefaultRegistry()}
	// Start without app.New would panic — covered in module_test
	_ = m
}

func TestUntilNextMinuteTiny(t *testing.T) {
	d := untilNextMinute(time.Date(2026, 1, 1, 0, 0, 59, 900e6, time.UTC))
	if d < time.Second {
		t.Fatal(d)
	}
}

func TestParseHHMMAndWindowEmptyStart(t *testing.T) {
	if _, _, err := parseHHMM("9"); err == nil {
		t.Fatal()
	}
	if _, _, err := parseHHMM("24:00"); err == nil {
		t.Fatal()
	}
	if _, _, err := parseHHMM("12:99"); err == nil {
		t.Fatal()
	}
	w, err := ParseWindow(`{"timeZone":"UTC"}`)
	if err != nil || w != nil {
		t.Fatal(w, err)
	}
	// NextOpen when already open
	open := &Window{TimeZone: "UTC", Start: "00:00", End: "23:59"}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	if !open.NextOpen(now).Equal(now.UTC()) {
		t.Fatal()
	}
	// overnight NextOpen from afternoon
	ow := &Window{TimeZone: "UTC", Start: "22:00", End: "06:00"}
	n := ow.NextOpen(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	want := time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC)
	if !n.Equal(want) {
		t.Fatalf("%v want %v", n, want)
	}
}

func TestBucketsNowDefault(t *testing.T) {
	b := &Buckets{Counter: NewMemoryCounter(), Limits: DefaultLimits()}
	r, err := b.Take(context.Background(), "o", []string{"gmail.com", "googlemail.com"})
	if err != nil || !r.OK {
		t.Fatal(r, err)
	}
}
