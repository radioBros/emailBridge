//go:build integration

package templates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/templates/mjml"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// countingMJML counts real compiles, to prove compilation happens at save and never at preview / send.
type countingMJML struct {
	inner MJMLCompiler
	calls int
}

func (c *countingMJML) Compile(ctx context.Context, src string) (string, error) {
	c.calls++
	return c.inner.Compile(ctx, src)
}

const helloMJML = `<mjml><mj-body><mj-section><mj-column>
<mj-text>Hello {{name}}</mj-text>
<mj-button href="https://example.com/u/{{url id}}">Open</mj-button>
</mj-column></mj-section></mj-body></mjml>`

func TestIntegrationMJMLCreatePreviewUpdateVersions(t *testing.T) {
	db := testDB(t)
	cc := &countingMJML{inner: mjml.Default()}
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "production", MJML: cc,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
	}
	org := store.NewID(store.PrefixOrganization)
	ctx := asOrg(org, tenancy.RoleOwner)

	tpl, err := svc.Create(ctx, CreateInput{Name: "mjml", Subject: "Hi {{name}}", HTML: helloMJML, HTMLFormat: "mjml"})
	if err != nil {
		t.Fatal(err)
	}
	if tpl.HTMLFormat != HTMLFormatMJML || !strings.Contains(tpl.HTMLCompiled, "Hello {{name}}") || tpl.HTML != helloMJML {
		t.Fatalf("create: %+v", tpl)
	}
	if cc.calls != 1 {
		t.Fatalf("compiles after create: %d", cc.calls)
	}
	got, err := svc.Get(ctx, tpl.ID)
	if err != nil || got.HTMLCompiled != tpl.HTMLCompiled || got.HTMLFormat != HTMLFormatMJML {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := svc.List(ctx, ListFilter{Limit: 10})
	if err != nil || len(list.Data) != 1 || list.Data[0].HTMLCompiled != "" || list.Data[0].HTMLFormat != HTMLFormatMJML ||
		list.Data[0].HTML != helloMJML {
		t.Fatalf("list must carry format + source but not compiled HTML: %v %+v", err, list)
	}

	prev, err := svc.Preview(ctx, tpl.ID, PreviewInput{Variables: map[string]any{"name": "Ada", "id": "a/b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prev.HTML, "Hello Ada") || !strings.Contains(prev.HTML, `href="https://example.com/u/a%2Fb"`) ||
		!strings.Contains(strings.ToLower(prev.HTML), "<!doctype html>") || prev.Subject != "Hi Ada" {
		t.Fatalf("preview: %+v", prev)
	}
	if _, _, html, err := svc.RenderParts(ctx, tpl.ID, nil, map[string]any{"name": "Bob"}); err != nil ||
		!strings.Contains(html, "Hello Bob") {
		t.Fatalf("render for submit: %v", err)
	}
	if cc.calls != 1 {
		t.Fatalf("preview / send must not compile: %d", cc.calls)
	}

	// Subject-only change: new content version, no recompile, compiled HTML kept.
	subj := "Hey {{name}}"
	upd, err := svc.Update(ctx, tpl.ID, UpdateInput{Subject: &subj}, "")
	if err != nil || cc.calls != 1 || upd.HTMLCompiled != tpl.HTMLCompiled || upd.CurrentVersion != 2 {
		t.Fatalf("subject update: %v calls=%d %+v", err, cc.calls, upd)
	}

	// New MJML: recompiled, v3; v1 still renders its own compiled HTML.
	v3src := strings.Replace(helloMJML, "Hello", "Ciao", 1)
	upd, err = svc.Update(ctx, tpl.ID, UpdateInput{HTML: &v3src}, formatETag(upd.Version))
	if err != nil || cc.calls != 2 || !strings.Contains(upd.HTMLCompiled, "Ciao {{name}}") || upd.CurrentVersion != 3 {
		t.Fatalf("html update: %v calls=%d", err, cc.calls)
	}
	v1 := 1
	old, err := svc.Preview(ctx, tpl.ID, PreviewInput{Version: &v1, Variables: map[string]any{"name": "Ada"}})
	if err != nil || !strings.Contains(old.HTML, "Hello Ada") || strings.Contains(old.HTML, "Ciao") {
		t.Fatalf("v1 preview: %v", err)
	}
	var ver Version
	if err := db.Collection(VersionsCollection).FindOne(ctx, bson.M{"_id": versionID(tpl.ID, 1)}).Decode(&ver); err != nil ||
		ver.HTMLFormat != HTMLFormatMJML || ver.HTMLCompiled == "" {
		t.Fatalf("version snapshot keeps format + compiled HTML: %v %+v", err, ver)
	}

	// Invalid MJML: rejected with field errors, stored template untouched.
	bad := "<mjml><mj-body><mj-bogus/></mj-body></mjml>"
	_, err = svc.Update(ctx, tpl.ID, UpdateInput{HTML: &bad}, "")
	var fe *fieldError
	if errs.CodeOf(err) != errs.CodeValidationFailed || !errors.As(err, &fe) || fe.items[0].Path != "body.html" {
		t.Fatalf("invalid MJML update: %v", err)
	}
	cur, _ := svc.Get(ctx, tpl.ID)
	if cur.CurrentVersion != 3 || cur.HTML != v3src {
		t.Fatalf("failed update changed the template: %+v", cur)
	}

	// Switch to html: compiled HTML dropped, the html renders as written.
	htmlFmt := "html"
	plain := "<p>Plain {{name}}</p>"
	upd, err = svc.Update(ctx, tpl.ID, UpdateInput{HTMLFormat: &htmlFmt, HTML: &plain}, "")
	if err != nil || upd.HTMLFormat != HTMLFormatHTML || upd.HTMLCompiled != "" {
		t.Fatalf("switch to html: %v %+v", err, upd)
	}
	prev, err = svc.Preview(ctx, tpl.ID, PreviewInput{Variables: map[string]any{"name": "Ada"}})
	if err != nil || prev.HTML != "<p>Plain Ada</p>" {
		t.Fatalf("html preview: %v %+v", err, prev)
	}
	// Switch back to mjml without sending html: the current html is compiled as MJML (and is not MJML).
	mjmlFmt := "mjml"
	if _, err := svc.Update(ctx, tpl.ID, UpdateInput{HTMLFormat: &mjmlFmt}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("plain HTML is not MJML: %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "bad-format", HTML: "<p/>", HTMLFormat: "markdown"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unknown format: %v", err)
	}
}

func TestIntegrationMJMLLegacyDocumentIsHTML(t *testing.T) {
	db := testDB(t)
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	org := store.NewID(store.PrefixOrganization)
	ctx := asOrg(org, tenancy.RoleOwner)
	id := store.NewID(store.PrefixTemplate)
	// A document written before G2 has no htmlFormat.
	if _, err := db.Collection(Collection).InsertOne(ctx, bson.M{
		"_id": id, "organizationId": org, "name": "legacy", "engine": "handlebars", "html": "<p>{{x}}</p>",
		"currentVersion": 1, "version": 1, "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, id)
	if err != nil || got.HTMLFormat != HTMLFormatHTML {
		t.Fatalf("legacy get: %v %+v", err, got)
	}
	list, err := svc.List(ctx, ListFilter{})
	if err != nil || list.Data[0].HTMLFormat != HTMLFormatHTML {
		t.Fatalf("legacy list: %v", err)
	}
	prev, err := svc.Preview(ctx, id, PreviewInput{Variables: map[string]any{"x": "y"}})
	if err != nil || prev.HTML != "<p>y</p>" {
		t.Fatalf("legacy preview: %v %+v", err, prev)
	}
}

func TestIntegrationMJMLAPIProblemFieldErrors(t *testing.T) {
	db := testDB(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{Registry: features.DefaultRegistry(), Env: "production", Log: quiet}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	caller := tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: store.NewID(store.PrefixOrganization), Role: tenancy.RoleOwner,
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(tenancy.WithCaller(r.Context(), caller))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	bad, _ := json.Marshal(map[string]any{
		"name": "bad", "htmlFormat": "mjml",
		"html": "<mjml><mj-body><mj-section><mj-column><mj-bogus/><mj-image width=\"abc\" src=\"a.png\"/></mj-column></mj-section></mj-body></mjml>",
	})
	w := do(http.MethodPost, "/v1/templates", string(bad))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
		t.Fatalf("invalid MJML: %d %s", w.Code, w.Body.String())
	}
	var p errs.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != errs.CodeValidationFailed || len(p.Errors) != 2 || p.Errors[0].Path != "body.html" ||
		!strings.Contains(p.Errors[0].Message, "mj-bogus") || !strings.Contains(p.Errors[1].Message, "mj-image") {
		t.Fatalf("problem: %+v", p)
	}

	good, _ := json.Marshal(map[string]any{"name": "good", "htmlFormat": "mjml", "html": helloMJML})
	w = do(http.MethodPost, "/v1/templates", string(good))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if created["htmlFormat"] != "mjml" || !strings.Contains(created["htmlCompiled"].(string), "Hello {{name}}") {
		t.Fatalf("create body: %s", w.Body.String())
	}
	w = do(http.MethodGet, "/v1/templates/"+id, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"htmlCompiled"`) {
		t.Fatalf("get: %d", w.Code)
	}
	w = do(http.MethodGet, "/v1/templates", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"htmlCompiled"`) || !strings.Contains(w.Body.String(), `"htmlFormat":"mjml"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	w = do(http.MethodPost, "/v1/templates/"+id+"/preview", `{"variables":{"name":"Ada","id":"7"}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Hello Ada") || !strings.Contains(w.Body.String(), "https://example.com/u/7") {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	w = do(http.MethodPatch, "/v1/templates/"+id, `{"html":"<mjml><mj-body><mj-bogus/></mj-body></mjml>"}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"path":"body.html"`) {
		t.Fatalf("patch invalid: %d %s", w.Code, w.Body.String())
	}
	w = do(http.MethodPost, "/v1/templates", `{"name":"x","html":"<p/>","htmlFormat":"markdown"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("enum: %d %s", w.Code, w.Body.String())
	}
}
