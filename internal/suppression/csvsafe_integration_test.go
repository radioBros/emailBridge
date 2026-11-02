//go:build integration

package suppression

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationCSVInjectionAndLimits(t *testing.T) {
	db := testDB(t)
	_ = EnsureIndexes(context.Background(), db)
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}
	cctx := orgCtx(org)
	l, err := svc.CreateList(cctx, CreateListInput{Name: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: "evil@z.test", Reason: `=HYPERLINK("https://x.test","y")`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddEntry(cctx, l.ID, CreateEntryInput{Address: "plus@z.test", Reason: "+cmd|' /C calc'!A0"}); err != nil {
		t.Fatal(err)
	}
	m := &Module{svc: svc}
	export := func(listID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(withChiList(cctx, listID))
		w := httptest.NewRecorder()
		m.serveExport(w, req)
		return w
	}
	w := export(l.ID)
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") == "" {
		t.Fatalf("export %d", w.Code)
	}
	if !strings.Contains(body, `'=HYPERLINK`) || !strings.Contains(body, `'+cmd`) {
		t.Fatalf("formula cells not neutralised: %s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		for _, cell := range strings.Split(line, ",") {
			c := strings.Trim(cell, `"`)
			if c != "" && strings.ContainsAny(c[:1], "=+-@") {
				t.Fatalf("cell starts with a formula trigger: %q", cell)
			}
		}
	}
	// A failing export answers a problem without the attachment header.
	miss := export(store.NewID(store.PrefixSuppression))
	if miss.Code == http.StatusOK || miss.Header().Get("Content-Disposition") != "" {
		t.Fatalf("failed export: %d %q", miss.Code, miss.Header().Get("Content-Disposition"))
	}
	// Re-importing the export restores the original values.
	l2, err := svc.CreateList(cctx, CreateListInput{Name: "csv-copy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportCSV(cctx, l2.ID, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.Repo.allEntries(cctx, org, l2.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %d", err, len(rows))
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Reason, "'") {
			t.Fatalf("escape kept on import: %q", r.Reason)
		}
	}
	// An oversized import is 413, never a silently truncated import.
	big := strings.Repeat("a@z.test\n", (maxImportBytes/9)+10)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big)).WithContext(withChiList(cctx, l2.ID))
	w = httptest.NewRecorder()
	m.serveImport(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), string(errs.CodePayloadTooLarge)) {
		t.Fatalf("oversized import: %d %s", w.Code, w.Body.String())
	}
}
