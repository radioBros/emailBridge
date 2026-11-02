package webhook

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestAnyIntFloat(t *testing.T) {
	for _, v := range []any{int(1), int32(2), int64(3), float64(4), float32(5)} {
		if _, ok := anyInt(v); !ok {
			if _, okf := anyFloat(v); !okf {
				t.Fatalf("%T", v)
			}
		}
	}
	if _, ok := anyInt("x"); ok {
		t.Fatal("string")
	}
	if _, ok := anyFloat(struct{}{}); ok {
		t.Fatal("struct")
	}
	_, _ = anyFloat(float32(1.5))
	_, _ = anyInt(int64(9))
	_, _ = anyInt(int32(8))
	_, _ = anyFloat(float64(2.5))
}

func TestListHelpers(t *testing.T) {
	if listLimit(0) != defaultListLimit || listLimit(500) != maxListLimit || listLimit(12) != 12 {
		t.Fatal("list limits")
	}
	for _, tc := range []struct {
		input string
		field string
		dir   int
	}{
		{"", "_id", -1}, {"name", "name", 1}, {"-createdAt", "createdAt", -1},
	} {
		field, dir, err := endpointSort(tc.input)
		if err != nil || field != tc.field || dir != tc.dir {
			t.Errorf("%q = %s/%d/%v", tc.input, field, dir, err)
		}
	}
	for _, input := range []string{"bad", "-bad"} {
		if _, _, err := endpointSort(input); errs.CodeOf(err) != errs.CodeSortNotAllowed {
			t.Errorf("endpoint sort %q: %v", input, err)
		}
		if _, _, err := deliverySort(input); errs.CodeOf(err) != errs.CodeSortNotAllowed {
			t.Errorf("delivery sort %q: %v", input, err)
		}
	}
	for _, input := range []string{"", "id", "-createdAt", "state", "lastAttemptAt", "-lastAttemptAt"} {
		if _, _, err := deliverySort(input); err != nil {
			t.Errorf("delivery sort %q: %v", input, err)
		}
	}
	now := time.Now().UTC()
	token := encodeListCursor(listCursor{ID: "x", Key: now.Format(time.RFC3339Nano), Field: "createdAt", Desc: true})
	decoded, err := decodeListCursor(token)
	if err != nil || decoded.ID != "x" {
		t.Fatalf("cursor = %+v %v", decoded, err)
	}
	base := bson.M{"organizationId": "org"}
	if _, _, err := listQuery(base, "createdAt", -1, 10, token, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listQuery(base, "name", -1, 10, token, 0); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("accepted cursor for wrong sort")
	}
	for _, invalid := range []string{"%%%", encodeListCursor(listCursor{ID: "x", Key: "bad", Field: "createdAt", Desc: true})} {
		if _, _, err := listQuery(base, "createdAt", -1, 10, invalid, 0); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("cursor %q: %v", invalid, err)
		}
	}
	if _, _, err := listQuery(base, "_id", 1, 10, encodeListCursor(listCursor{ID: "x", Field: "_id"}), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listQuery(base, "_id", 1, 10, "", 2); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryMatchSortKeysAndViews(t *testing.T) {
	from, to := time.Now().Add(-time.Hour), time.Now()
	filter := deliveryMatch(DeliveryFilter{
		EndpointID: "end", RouteID: "route", EventID: "event", EventType: "type",
		AccountID: "account", State: StateFailed, From: from, To: to,
	})
	if len(filter) != 7 {
		t.Fatalf("filter = %v", filter)
	}
	endpoint := Endpoint{
		ID: "end", Name: "name", CreatedAt: from,
		Headers: []StoredHeader{{Name: "Authorization", Secret: true}},
		Secrets: []StoredSecret{{Preview: "preview"}},
	}
	view := endpointView(&endpoint)
	if view.SecretPreview != "preview" || !view.Headers[0].Secret || view.Headers[0].Value != "" {
		t.Fatalf("view = %+v", view)
	}
	for _, field := range []string{"name", "createdAt", "_id"} {
		if endpointSortKey(endpoint, field) == nil {
			t.Fatal(field)
		}
	}
	at := to
	delivery := Delivery{ID: "delivery", State: StateFailed, CreatedAt: to, LastAttemptAt: &at}
	for _, field := range []string{"state", "createdAt", "lastAttemptAt", "_id"} {
		if deliverySortKey(delivery, field) == nil {
			t.Fatal(field)
		}
	}
	if !reflect.DeepEqual(deliveryMatch(DeliveryFilter{}), bson.M{}) {
		t.Fatal("empty match")
	}
	multi := deliveryMatch(DeliveryFilter{State: "failed,deadLettered"})
	in, ok := multi["state"].(bson.M)["$in"].([]string)
	if !ok || len(in) != 2 {
		t.Fatalf("multi state = %v", multi["state"])
	}
}

func TestDefaultsValidationETagsAndSwitches(t *testing.T) {
	service := &Service{Egress: egress.NewDialer(egress.Policy{})}
	endpoint := &Endpoint{
		Name: "valid", URL: "http://127.0.0.1:8080/hook", Events: []string{events.MessageReceived},
	}
	service.applyDefaults(context.Background(), endpoint)
	if endpoint.Kind != KindHTTP || endpoint.Retry.MaxAttempts != 10 ||
		endpoint.Delivery.Ordering != OrderingStrict || endpoint.Security.SignatureMode != SignatureStandard {
		t.Fatalf("defaults = %+v", endpoint)
	}
	configured := &Endpoint{}
	(&Service{
		DefaultMaxAttempts: 3, DefaultBackoffBase: time.Second, DefaultJitter: .1,
		DefaultTimeout: 2 * time.Second, DefaultAutoDisable: 24 * time.Hour,
	}).applyDefaults(context.Background(), configured)
	if configured.Retry.MaxAttempts != 3 || configured.Retry.BackoffBase != "1s" ||
		configured.Retry.BackoffJitter != .1 || configured.Retry.Timeout != "2s" ||
		configured.Delivery.AutoDisableAfter != "24h0m0s" {
		t.Fatalf("configured defaults = %+v", configured)
	}
	if err := service.validateEndpoint(context.Background(), endpoint); err != nil {
		t.Fatal(err)
	}
	if checkETag("", 1) != nil || checkETag(`"v2"`, 2) != nil ||
		errs.CodeOf(checkETag(`"v3"`, 2)) != errs.CodeVersionMismatch || formatETag(2) != `"v2"` {
		t.Fatal("etag helpers")
	}
	if !isFinal(StateSucceeded) || isFinal(StateRetrying) {
		t.Fatal("final states")
	}
	endpoint.Switches = map[string]any{"webhook.signature.mode": SignatureBodyOnly}
	if service.signatureMode(context.Background(), endpoint) != SignatureBodyOnly {
		t.Fatal("signature switch")
	}
	endpoint.Switches = map[string]any{"webhook.secret.rotationOverlap": false}
	if service.rotationOverlap(endpoint) != 0 {
		t.Fatal("disabled overlap")
	}
	endpoint.Switches = map[string]any{"webhook.secret.rotationOverlap": "2h"}
	if service.rotationOverlap(endpoint) != 2*time.Hour {
		t.Fatal("overlap duration")
	}
}

func TestEndpointValidationFailures(t *testing.T) {
	valid := Endpoint{
		Name: "valid", Kind: KindHTTP, URL: "http://127.0.0.1:8080",
		Events: []string{events.MessageReceived}, Security: SecurityOptions{SignatureMode: SignatureStandard},
		Retry:   RetryOptions{MaxAttempts: 1, BackoffBase: "1s", Timeout: "1s"},
		Payload: PayloadOptions{Text: "none"},
		Delivery: DeliveryOptions{
			Ordering: OrderingStrict, ContentType: "json",
			MaxConcurrency: 8, AutoDisableAfter: "120h",
		},
	}
	tests := []func(*Endpoint){
		func(e *Endpoint) { e.Name = "" },
		func(e *Endpoint) { e.Kind = "kafka" },
		func(e *Endpoint) { e.URL = "ftp://host/path" },
		func(e *Endpoint) { e.Security.SignatureMode = "bad" },
		func(e *Endpoint) { e.Retry.MaxAttempts = 101 },
		func(e *Endpoint) { e.Retry.BackoffJitter = 2 },
		func(e *Endpoint) { e.Retry.Timeout = "bad" },
		func(e *Endpoint) { e.Payload.Text = "bad" },
		func(e *Endpoint) { e.Payload.Headers = []string{"*", "subject"} },
		func(e *Endpoint) { e.Delivery.Ordering = "bad" },
		func(e *Endpoint) { e.Delivery.ContentType = "bad" },
		func(e *Endpoint) { e.Delivery.MaxConcurrency = 0 },
		func(e *Endpoint) { e.Delivery.MaxConcurrency = 65 },
		func(e *Endpoint) { e.Delivery.AutoDisableAfter = "30m" },
		func(e *Endpoint) { e.Delivery.AutoDisableAfter = "744h" },
		func(e *Endpoint) { e.Events = []string{events.EndpointTest} },
		func(e *Endpoint) { e.Filter.AccountIDs = []string{"bad"} },
	}
	service := &Service{Egress: egress.NewDialer(egress.Policy{})}
	for i, mutate := range tests {
		endpoint := valid
		mutate(&endpoint)
		if err := service.validateEndpoint(context.Background(), &endpoint); err == nil {
			t.Errorf("case %d accepted: %+v", i, endpoint)
		}
	}
	capped := valid
	capped.Delivery.MaxConcurrency = 8
	if err := (&Service{Egress: egress.NewDialer(egress.Policy{}), Concurrency: 4}).validateEndpoint(context.Background(), &capped); err == nil {
		t.Fatal("maxConcurrency above node concurrency accepted")
	}
	if err := (&Service{}).validateEndpoint(context.Background(), &valid); err == nil {
		t.Fatal("missing dialer accepted")
	}
	blocked := &Service{Egress: egress.NewDialer(egress.Policy{BlockPrivate: true})}
	if errs.CodeOf(blocked.validateEndpoint(context.Background(), &valid)) != errs.CodeEndpointURLBlocked {
		t.Fatal("blocked URL")
	}
}

func TestDeliveryRequestHelpersAndJobs(t *testing.T) {
	headers := http.Header{"Retry-After": {"2"}, "Content-Type": {"text/plain"}, "X-Request-Id": {"r"}, "Other": {"x"}}
	selected := selectedResponseHeaders(headers)
	if len(selected) != 3 || selected["Other"] != "" || selectedResponseHeaders(http.Header{}) != nil {
		t.Fatalf("selected = %v", selected)
	}
	got := routeHeaderValues(map[string]any{
		"X-A": "a", "X-B": map[string]any{"value": "b"}, "X-C": 1,
	})
	if len(got) != 2 || got["X-A"] != "a" || got["X-B"] != "b" {
		t.Fatalf("route headers = %v", got)
	}
	job, err := decodeJob([]byte(`{"deliveryId":"d"}`))
	if err != nil || job.DeliveryID != "d" {
		t.Fatalf("job = %+v %v", job, err)
	}
	for _, raw := range [][]byte{[]byte(`{`), []byte(`{}`)} {
		if _, err := decodeJob(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if (&Service{Concurrency: 2}).workerAckPending() != 2 || (&Service{}).workerAckPending() != 64 {
		t.Fatal("ack pending")
	}
	if (&Service{DeliveryPartitions: 3}).partitions() != 3 || (&Service{}).partitions() != 256 {
		t.Fatal("partitions")
	}
	if (&Service{Retention: time.Hour}).retention() != time.Hour || (&Service{}).retention() != 30*24*time.Hour {
		t.Fatal("retention")
	}
	if !store.IsID(store.PrefixDelivery, storeDeliveryID()) {
		t.Fatal("delivery ID")
	}
	service := &Service{}
	if service.log() == nil || service.securityLog() == nil || service.random() < 0 || service.random() >= 1 {
		t.Fatal("service defaults")
	}
	service.audit(context.Background(), "action", "id", nil)
	service.auditDelivery(context.Background(), "action", "id", nil)
	if err := service.RouteEvent(context.Background(), events.Event{Type: events.EndpointTest}); err != nil {
		t.Fatal(err)
	}
	if account, env, err := service.ruleInputs(context.Background(), events.Event{}); err != nil ||
		len(account) != 0 || len(env) != 0 {
		t.Fatalf("empty rule inputs: %v %v %v", account, env, err)
	}
	if errorsNewPublisher() == nil {
		t.Fatal("publisher error")
	}
}
