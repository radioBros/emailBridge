//go:build integration

package messages

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
)

// The q search text is a literal, case-insensitive substring: regex syntax never applies and pathological
// patterns are inert; other organisations stay invisible.
func TestIntegrationListSearchIsLiteral(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	_, dotted := seedIndex(t, h, "INBOX", 1, 7, "<a.b+c(1)@x>", nil, 10)
	_, plain := seedIndex(t, h, "INBOX", 2, 7, "<aXb+c(1)@x>", nil, 10)
	// Same Message-ID in another organisation.
	now := time.Now().UTC()
	if _, err := h.db.Collection(index.Messages).InsertOne(ctx, &index.Message{
		ID: store.NewID(store.PrefixMessage), OrganizationID: store.NewID(store.PrefixOrganization),
		AccountID: h.acc.ID, MailboxID: store.NewID(store.PrefixMailbox), UIDValidity: 7, UID: 3,
		MessageIDHeader: "<a.b+c(1)@x>", InternalDate: now, IndexedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: "a.b+c(1)"})
	if err != nil || len(got.Data) != 1 || got.Data[0].ID != dotted {
		t.Fatalf("literal special chars: %+v %v", got, err)
	}
	got, err = h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: "AXB+C(1)"})
	if err != nil || len(got.Data) != 1 || got.Data[0].ID != plain {
		t.Fatalf("case-insensitive: %+v %v", got, err)
	}
	for _, q := range []string{".*", "(", "[", "^<", "a|b"} {
		got, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: q})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		for _, m := range got.Data {
			if !strings.Contains(strings.ToLower(m.MessageIDHeader), strings.ToLower(q)) {
				t.Fatalf("%q matched as a regex: %+v", q, m)
			}
		}
	}
	start := time.Now()
	evil := "(a+)+$" + strings.Repeat("a", 200)
	if _, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: evil}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("pathological pattern was evaluated as a regex")
	}
	if _, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: strings.Repeat("x", maxSearchLen+1)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("too long: %v", err)
	}
}
