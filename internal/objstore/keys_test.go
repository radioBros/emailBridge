package objstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/objstore"
)

func TestKeyBuilders(t *testing.T) {
	ts := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"outbound", func() (string, error) {
			return objstore.OutboundKey("org_a", "acc_b", "out_c")
		}, "outbound/org_a/acc_b/out_c.eml"},
		{"archive", func() (string, error) {
			return objstore.ArchiveKey("org_a", "acc_b", "msg_c", ts)
		}, "archive/org_a/acc_b/2026/03/msg_c.eml"},
		{"sandbox", func() (string, error) {
			return objstore.SandboxKey("org_a", "cap_b")
		}, "sandbox/org_a/cap_b.eml"},
		{"attachment", func() (string, error) {
			return objstore.AttachmentKey("org_a", "acc_b", "att_c")
		}, "attachments/org_a/acc_b/att_c"},
		{"export", func() (string, error) {
			return objstore.ExportPrefix("org_a", "exp_b")
		}, "exports/org_a/exp_b/"},
		{"branding", func() (string, error) {
			return objstore.BrandingKey("org_a", "bas_b")
		}, "branding/org_a/bas_b"},
		{"brandingPrefix", func() (string, error) {
			return objstore.BrandingPrefix("org_a")
		}, "branding/org_a/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestKeyValidation(t *testing.T) {
	bad := []struct {
		name string
		fn   func() error
	}{
		{"empty", func() error { _, err := objstore.OutboundKey("", "a", "b"); return err }},
		{"slash", func() error { _, err := objstore.OutboundKey("org/x", "a", "b"); return err }},
		{"dotdot", func() error { _, err := objstore.SandboxKey("org", ".."); return err }},
		{"dotdot-embed", func() error { _, err := objstore.AttachmentKey("org", "acc", "x..y"); return err }},
		{"backslash", func() error { _, err := objstore.ExportPrefix(`org\x`, "e"); return err }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, objstore.ErrBadKey) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
