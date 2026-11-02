package archive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

func TestModuleNameRoles(t *testing.T) {
	t.Parallel()
	m := &Module{}
	if m.Name() != "archive" || len(m.Roles()) != 0 {
		t.Fatalf("%s %v", m.Name(), m.Roles())
	}
}

func TestPoolIdleDefaultAndCloseNil(t *testing.T) {
	t.Parallel()
	p := &Pool{}
	if p.idleFor() != defaultIdle {
		t.Fatal(p.idleFor())
	}
	p.Close() // empty
	p.Sweep() // empty
}

func TestLeaseNilSafe(t *testing.T) {
	t.Parallel()
	var l *Lease
	l.Release()
	l.Discard()
}

func TestServiceLogNowDefaults(t *testing.T) {
	t.Parallel()
	s := &Service{}
	if s.log() == nil {
		t.Fatal("log")
	}
	if s.now().IsZero() {
		t.Fatal("now")
	}
	s.Log = slog.New(slog.DiscardHandler)
	_ = s.log()
	s.Now = func() time.Time { return time.Unix(1, 0).UTC() }
	if !s.now().Equal(time.Unix(1, 0).UTC()) {
		t.Fatal(s.now())
	}
}

func TestFromMailboxNoDeps(t *testing.T) {
	t.Parallel()
	s := &Service{}
	_, ok := s.fromMailbox(context.Background(), "acc", &msgDoc{MailboxID: "mb"})
	if ok {
		t.Fatal("expected false")
	}
}

func TestPutRawBadSegment(t *testing.T) {
	t.Parallel()
	s := &Store{Objects: &objstore.Store{}}
	_, err := s.PutRaw(context.Background(), "org", "acc/bad", "msg", time.Now(), []byte("x"))
	if err == nil {
		t.Fatal("want bad key")
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()
	a := &ebsync.Account{IMAPAddr: "h:1", IMAPUser: "u"}
	if fingerprint(a) != "h:1|u" {
		t.Fatal(fingerprint(a))
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (errReader) Close() error             { return nil }

func TestStreamEMLArchiveBranch(t *testing.T) {
	t.Parallel()
	res := &rawResult{Source: SourceArchive, Reader: io.NopCloser(errReader{}), Size: 1}
	sr := streamEML("msg_01habcdefghijklmnopqrstuvw", res, false)
	if sr == nil || sr.Body == nil {
		t.Fatal("stream")
	}
}

func TestErrsCodes(t *testing.T) {
	t.Parallel()
	if errs.CodeArchiveNotFound == "" {
		t.Fatal()
	}
}
