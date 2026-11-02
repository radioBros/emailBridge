package integrations

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
)

func TestMaterializeAndP82InRegistry(t *testing.T) {
	r := DefaultRegistry()
	for _, id := range []string{
		TypeSlack, TypeTeams, TypeDiscord, TypeTelegram, TypeMattermost,
		TypeFeatherDesk, TypeHelpdesk, TypeZapier, TypeRspamd,
	} {
		d, ok := r.Get(id)
		if !ok || d.Test == nil {
			t.Fatalf("%s missing or no test", id)
		}
		if id == TypeSlack && (d.Version != "1.1.0" || len(d.Resources.Routes) != 4) {
			t.Fatalf("slack %+v", d)
		}
	}
}

func TestMaterializeTeamsTestHook(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	def := Materialize(connectors.Teams())
	tr, err := def.Test(context.Background(), TestInput{
		Config: map[string]any{"webhookUrl": srv.URL},
		Client: srv.Client(),
	})
	if err != nil || !tr.OK || body == "" {
		t.Fatalf("%+v %v body=%q", tr, err, body)
	}
}
