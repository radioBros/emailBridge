//go:build e2e

package p7

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

const e2eMJML = `<mjml>
  <mj-body>
    <mj-section>
      <mj-column>
        <mj-text>Hello {{name}}, your invoice is ready.</mj-text>
        <mj-button href="https://billing.example.com/i/{{url invoice}}">View invoice</mj-button>
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>`

// G2: an MJML template is compiled at save (invalid MJML → 422 with errors at body.html), previews and is
// sent through submit with the compiled + rendered HTML, Handlebars in attributes included.
func TestP7_TemplatesMJML_CompilePreviewSend(t *testing.T) {
	f := startFixture(t, nil, []string{"message.sent"}, sink.Options{})
	owner := f.Owner

	bad := owner.Do(http.MethodPost, "/v1/templates", map[string]any{
		"name": "p7-mjml-bad", "htmlFormat": "mjml",
		"html": "<mjml><mj-body><mj-section><mj-column><mj-bogus></mj-bogus></mj-column></mj-section></mj-body></mjml>",
	})
	if bad.Status != http.StatusUnprocessableEntity || problemCode(bad) != "validation_failed" {
		t.Fatalf("invalid MJML: want 422 validation_failed, got %d %s", bad.Status, bad.Body)
	}
	items, _ := bad.JSON["errors"].([]any)
	var first map[string]any
	if len(items) > 0 {
		first, _ = items[0].(map[string]any)
	}
	if first == nil || first["path"] != "body.html" || !strings.Contains(fmt.Sprint(first["message"]), "mj-bogus") {
		t.Fatalf("field errors: %s", bad.Body)
	}

	run := strconv.FormatInt(time.Now().UnixNano(), 10)
	created := owner.Do(http.MethodPost, "/v1/templates", map[string]any{
		"name": "p7-mjml-" + run, "subject": "MJML {{run}}", "text": "Hello {{name}}",
		"htmlFormat": "mjml", "html": e2eMJML,
	})
	harness.ExpectStatus(t, created, http.StatusCreated)
	tplID := created.Str("id")
	if created.Str("htmlFormat") != "mjml" || !strings.Contains(created.Str("htmlCompiled"), "Hello {{name}}") {
		t.Fatalf("create: %s", created.Body)
	}

	vars := map[string]any{"name": "Ada", "invoice": "2026/7", "run": run}
	prev := owner.Do(http.MethodPost, "/v1/templates/"+tplID+"/preview", map[string]any{"variables": vars})
	harness.ExpectStatus(t, prev, http.StatusOK)
	if !strings.Contains(prev.Str("html"), "Hello Ada") ||
		!strings.Contains(prev.Str("html"), `href="https://billing.example.com/i/2026%2F7"`) {
		t.Fatalf("preview html: %s", prev.Body)
	}

	sub := owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/outbound-messages", map[string]any{
		"to":       []map[string]any{{"address": f.Email}},
		"template": map[string]any{"id": tplID, "variables": vars},
	})
	if sub.Status != http.StatusAccepted {
		t.Fatalf("submit: %d %s", sub.Status, sub.Body)
	}
	out := owner.WaitJSON(t, "/v1/outbound-messages/"+sub.Str("id"), 90*time.Second, "sent", func(r harness.Resp) bool {
		st := r.Str("state")
		return r.Status == http.StatusOK && (st == "sent" || st == "failed" || st == "outcomeUnknown")
	})
	if out.Str("state") != "sent" {
		t.Fatalf("outbound state=%s body=%s", out.Str("state"), out.Body)
	}

	msgID := waitMessageID(t, owner, f.AccID, "MJML "+run, 90*time.Second)
	got := owner.Do(http.MethodGet, "/v1/accounts/"+f.AccID+"/messages/"+msgID+"?text=html", nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	html := got.Str("html")
	if !strings.Contains(html, "Hello Ada") || !strings.Contains(html, "https://billing.example.com/i/2026%2F7") ||
		strings.Contains(html, "{{") || strings.Contains(html, "<mj-") {
		t.Fatalf("received html is not the compiled + rendered MJML: %s", html)
	}
}
