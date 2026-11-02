package protolog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/query"
	logstore "github.com/radiobros/emailbridge/internal/logging/store"
)

type gateFunc func(ctx context.Context, accountID string) (string, error)

func (f gateFunc) ProtocolLogAccount(ctx context.Context, accountID string) (string, error) {
	return f(ctx, accountID)
}

type dlAudit struct {
	n int
}

func (d *dlAudit) ProtocolLogDownload(context.Context, string, time.Time, time.Time) error {
	d.n++
	return nil
}

func TestAPIListDownloadUnit(t *testing.T) {
	ctx := context.Background()
	sys := logging.New(logging.Options{Node: "n1", Stdout: io.Discard, Fallback: io.Discard, FlushEvery: 5 * time.Millisecond})
	t.Cleanup(func() { _ = sys.Close(ctx) })
	ls, err := logstore.Open(ctx, logstore.Options{Dir: t.TempDir(), Node: "n1", SweepEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ls.Close() })
	sys.AddSink(ls)
	hub := logstore.NewHub("n1", 0)
	t.Cleanup(hub.Close)
	qsvc, err := query.New(query.Options{Local: &logstore.Node{Store: ls, Hub: hub}})
	if err != nil {
		t.Fatal(err)
	}

	org, acc := "org_aaaaaaaaaaaaaaaaaaaaaaaaaa", "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	rec := &Recorder{Log: sys.For(logging.CatProtocol, "imap")}
	sink := rec.Sink(org, acc, false)
	sink(Line{Dir: Client, Proto: "imap", ConnID: "c1", Text: "a1 NOOP", Bytes: 7})
	sink(Line{Dir: Server, Proto: "imap", ConnID: "c1", Text: "a1 OK", Bytes: 5})
	if err := sys.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	aud := &dlAudit{}
	api := &API{
		Accounts: gateFunc(func(_ context.Context, id string) (string, error) {
			if id != acc {
				return "", errs.New(errs.CodeNotFound, "missing")
			}
			return org, nil
		}),
		Logs: qsvc, Audit: aud, Log: slog.New(slog.DiscardHandler),
	}
	hs, err := httpapi.New(httpapi.Options{AccessLog: slog.New(slog.DiscardHandler), SecurityLog: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	api.Register(hs)
	srv := httptest.NewServer(hs)
	t.Cleanup(srv.Close)

	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	resp, err := http.Get(srv.URL + "/v1/accounts/" + acc + "/protocol-logs?from=" + from + "&to=" + to)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var list ProtocolLogRecordList
	if err := json.Unmarshal(body, &list); err != nil || len(list.Data) < 2 {
		t.Fatalf("%v %+v", err, list)
	}

	resp, err = http.Get(srv.URL + "/v1/accounts/" + acc + "/protocol-logs/download?from=" + from + "&to=" + to + "&proto=imap")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || aud.n != 1 || !strings.Contains(string(body), "a1 NOOP") {
		t.Fatalf("download %d audit=%d body=%s", resp.StatusCode, aud.n, body)
	}

	resp, err = http.Get(srv.URL + "/v1/accounts/acc_missingmissingmissingmiss/protocol-logs")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing %d", resp.StatusCode)
	}

	// validation: partial range via handler
	resp, err = http.Get(srv.URL + "/v1/accounts/" + acc + "/protocol-logs?from=" + from)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("partial range %d", resp.StatusCode)
	}

	if _, err := api.list(ctx, &listIn{AccountID: acc, Limit: 999}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	out, err := api.list(ctx, &listIn{AccountID: acc, Limit: 1, Proto: "imap"})
	if err != nil || len(out.Body.Data) != 1 || out.Body.NextCursor == nil {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestModuleStartRequiresGate(t *testing.T) {
	m := &Module{}
	if err := m.Start(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestAttrHelpers(t *testing.T) {
	if attrInt(int(3)) != 3 || attrInt(int64(4)) != 4 || attrInt(float64(5)) != 5 || attrInt("x") != 0 {
		t.Fatal("attrInt")
	}
	if s, ok := attrString(stringer("y")); !ok || s != "y" {
		t.Fatal("stringer")
	}
}

type stringer string

func (s stringer) String() string { return string(s) }
