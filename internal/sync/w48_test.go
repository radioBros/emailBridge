package sync

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestApplyAttachmentResults_byPartID(t *testing.T) {
	atts := []types.AttachmentInfo{
		{PartID: "1", Filename: "a.txt"},
		{PartID: "1.2", Filename: "b.pdf"},
	}
	applyAttachmentResults(atts, []AttachmentResult{
		{ID: "att_a", PartID: "1", URL: "https://x/a", Scan: "clean"},
		{ID: "att_b", PartID: "1.2", Scan: "skipped"},
	})
	if atts[0].ID != "att_a" || atts[0].URL == "" || atts[1].ID != "att_b" {
		t.Fatalf("%+v", atts)
	}
}

func TestIndexMetasFromBody_includesInlineCID(t *testing.T) {
	root := &imapc.Part{
		Children: []*imapc.Part{
			{Path: []int{1}, MediaType: "text/plain", Size: 1},
			{Path: []int{2}, MediaType: "image/png", Disposition: "inline", ContentID: "logo", Size: 2},
			{Path: []int{3}, MediaType: "application/pdf", Disposition: "attachment", Filename: "a.pdf", Size: 3},
		},
	}
	metas := indexMetasFromBody(root)
	if len(metas) != 2 {
		t.Fatalf("%+v", metas)
	}
	if metas[0].PartID != "2" || metas[1].PartID != "3" {
		t.Fatalf("%+v", metas)
	}
}

func TestApplySpecialUse(t *testing.T) {
	if got := applySpecialUse("Sent", "", map[string]string{"sent": "Sent"}); got != `\Sent` {
		t.Fatal(got)
	}
	if got := applySpecialUse("INBOX", `\Inbox`, map[string]string{"sent": "Sent"}); got != `\Inbox` {
		t.Fatal(got)
	}
}

func TestApplyAccountNeeds_allowSwitches(t *testing.T) {
	r := &runner{acc: &Account{
		DenyCalendarEvents: true,
		AuthResults:        "header", TrustedAuth: []string{"mx.test"},
	}}
	n := r.applyAccountNeeds(Needs{WebSafeHTML: true, CalendarEvents: true, ReplyText: true, Text: true})
	if !n.WebSafeHTML || n.CalendarEvents || !n.ReplyText || n.AuthResults != "header" {
		t.Fatalf("%+v", n)
	}
}

type memAtt struct {
	registered []AttachmentMeta
	erased     []string
}

func (m *memAtt) Register(_ context.Context, _, _, _ string, metas []AttachmentMeta) ([]AttachmentResult, error) {
	m.registered = append(m.registered, metas...)
	out := make([]AttachmentResult, len(metas))
	for i, meta := range metas {
		out[i] = AttachmentResult{ID: "att_" + meta.PartID, PartID: meta.PartID, Scan: "skipped"}
	}
	return out, nil
}
func (m *memAtt) Ingest(context.Context, string, string, string, []byte, bool, bool) ([]AttachmentResult, error) {
	return nil, nil
}
func (m *memAtt) EraseMessage(_ context.Context, _, messageID string) error {
	m.erased = append(m.erased, messageID)
	return nil
}

func TestIngestAttachments_registerPath(t *testing.T) {
	mem := &memAtt{}
	r := &runner{acc: &Account{ID: "acc", OrganizationID: "org"}, attachments: mem, log: discardLog()}
	body := &imapc.Part{Path: []int{1}, Disposition: "attachment", Filename: "a.pdf", MediaType: "application/pdf", Size: 9}
	data := &types.MessageReceived{Attachments: []types.AttachmentInfo{{PartID: "1", Filename: "a.pdf"}}}
	r.ingestAttachments(context.Background(), "msg_1", body, data, nil, 0)
	if len(mem.registered) != 1 || data.Attachments[0].ID != "att_1" {
		t.Fatalf("reg=%+v data=%+v", mem.registered, data.Attachments)
	}
}
