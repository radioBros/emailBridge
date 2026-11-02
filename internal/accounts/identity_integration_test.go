//go:build integration

package accounts

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestIntegrationIdentityPinOnCredentialChange(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("ext-pin-1"))
	if err != nil {
		t.Fatal(err)
	}
	// First credential change pins identity.
	pass := "secret-pass"
	user := "u"
	patched, err := svc.Patch(ctx, a.ID, PatchInput{
		IMAP: &IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: user, Pass: pass}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if patched.Identity == nil || patched.Identity.IMAPUser != user || patched.Identity.Email != a.Email {
		t.Fatalf("pin %+v", patched.Identity)
	}

	// Different IMAP user without rePin → 409 identity_mismatch.
	other := "other-user"
	_, err = svc.Patch(ctx, a.ID, PatchInput{
		IMAP: &IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: other, Pass: pass}},
	}, "")
	if errs.CodeOf(err) != errs.CodeIdentityMismatch {
		t.Fatalf("want identity_mismatch, got %v", err)
	}

	// rePin accepts the new identity.
	patched, err = svc.Patch(ctx, a.ID, PatchInput{
		IMAP:  &IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: other, Pass: pass}},
		RePin: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if patched.Identity == nil || patched.Identity.IMAPUser != other {
		t.Fatalf("repin %+v", patched.Identity)
	}
	if patched.Identity.PinnedAt.IsZero() || time.Since(patched.Identity.PinnedAt) > time.Minute {
		t.Fatalf("pinnedAt %+v", patched.Identity.PinnedAt)
	}
}
