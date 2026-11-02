package exports

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fakeSettings struct {
	on bool
}

func (f fakeSettings) Effective(context.Context, string) (settings.SettingsBody, error) {
	return settings.SettingsBody{
		Switches: map[string]settings.SettingValue{
			SwitchEnabled: {Value: f.on},
		},
	}, nil
}

func TestRequireFeatureWithSettings(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, OrganizationID: org, Role: tenancy.RoleOwner, UserID: "u",
	})
	s := &Service{Registry: features.DefaultRegistry(), Env: "production", Settings: fakeSettings{on: false}}
	if err := s.requireFeature(ctx); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("%v", err)
	}
	s.Settings = fakeSettings{on: true}
	if err := s.requireFeature(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStreamExportWrites(t *testing.T) {
	rc := io.NopCloser(bytes.NewReader([]byte("gzip-bytes")))
	if sr := streamExport("exp_x", io.NopCloser(bytes.NewReader(nil))); sr == nil || sr.Body == nil {
		t.Fatal("stream")
	}
	w := &streamWriter{h: map[string]string{}, buf: &bytes.Buffer{}}
	writeExportStream(w, "exp_x", rc)
	if w.buf.String() != "gzip-bytes" || w.h["Content-Type"] == "" {
		t.Fatalf("%q %+v", w.buf.String(), w.h)
	}
}

type streamWriter struct {
	h   map[string]string
	buf *bytes.Buffer
}

func (s *streamWriter) SetHeader(k, v string) { s.h[k] = v }
func (s *streamWriter) BodyWriter() io.Writer { return s.buf }

func TestClampAndSignEdges(t *testing.T) {
	if clampTTL(time.Minute) != time.Minute {
		t.Fatal("clamp mid")
	}
	if _, _, err := VerifyToken([]byte("k"), "", time.Now()); !errors.Is(err, errBadToken) {
		t.Fatal(err)
	}
	if _, _, err := VerifyToken(nil, "a.b", time.Now()); !errors.Is(err, errBadToken) {
		t.Fatal(err)
	}
	key := []byte("k")
	tok, err := Sign(key, "org", "exp", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	bad := tok[:len(tok)-2] + "xx"
	if _, _, err := VerifyToken(key, bad, time.Now()); !errors.Is(err, errBadToken) {
		t.Fatalf("%v", err)
	}
	if _, _, err := VerifyToken(key, "!!!.abc", time.Now()); !errors.Is(err, errBadToken) {
		t.Fatalf("%v", err)
	}
}
