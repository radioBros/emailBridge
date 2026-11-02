package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/sinks"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestSinkConfigViewRedactsSecrets(t *testing.T) {
	raw := map[string]any{
		"url": "nats://127.0.0.1:4222", "subject": "eb.{type}",
		"creds": crypto.EncryptedString{DEK: "dk", CT: []byte("x")},
	}
	view := sinkConfigView(sinks.KindNATS, raw)
	if view["creds"] != sinkSecretMarker || view["url"] != "nats://127.0.0.1:4222" {
		t.Fatalf("view = %#v", view)
	}
	if sinkConfigView(sinks.KindNATS, nil) != nil {
		t.Fatal("nil")
	}
}

func TestValidateSinkKinds(t *testing.T) {
	svc := &Service{Egress: egress.NewDialer(egress.Policy{})}
	base := Endpoint{
		Name: "s", Kind: sinks.KindNATS, Events: []string{events.MessageReceived},
		Security: SecurityOptions{SignatureMode: SignatureStandard},
		Retry:    RetryOptions{MaxAttempts: 1, BackoffBase: "1s", Timeout: "1s"},
		Payload:  PayloadOptions{Text: "none"},
		Delivery: DeliveryOptions{
			Ordering: OrderingStrict, ContentType: "json",
			MaxConcurrency: 8, AutoDisableAfter: "120h",
		},
		SinkConfig: map[string]any{"url": "nats://127.0.0.1:4222", "subject": "eb.{type}"},
	}
	if err := svc.validateEndpoint(context.Background(), &base); err != nil {
		t.Fatal(err)
	}
	withURL := base
	withURL.URL = "http://example.test"
	if errs.CodeOf(svc.validateEndpoint(context.Background(), &withURL)) != errs.CodeValidationFailed {
		t.Fatal("sink with url")
	}
	httpWithSink := base
	httpWithSink.Kind = KindHTTP
	httpWithSink.URL = "http://127.0.0.1:9/hook"
	if errs.CodeOf(svc.validateEndpoint(context.Background(), &httpWithSink)) != errs.CodeValidationFailed {
		t.Fatal("http with sinkConfig")
	}
	kafka := base
	kafka.Kind = sinks.KindKafka
	kafka.URL = ""
	kafka.SinkConfig = map[string]any{"brokers": []any{"127.0.0.1:9092"}, "topic": "t"}
	if err := svc.validateEndpoint(context.Background(), &kafka); err != nil {
		t.Fatal(err)
	}
	sqs := base
	sqs.Kind = sinks.KindSQS
	sqs.URL = ""
	sqs.SinkConfig = map[string]any{
		"queueUrl": "https://sqs.us-east-1.amazonaws.com/1/q", "region": "us-east-1",
	}
	if errs.CodeOf(svc.validateEndpoint(context.Background(), &sqs)) != errs.CodeValidationFailed {
		t.Fatal("sqs without keys and ambient off")
	}
	sqs.SinkConfig["accessKeyId"] = "a"
	sqs.SinkConfig["secretAccessKey"] = "b"
	if err := svc.validateEndpoint(context.Background(), &sqs); err != nil {
		t.Fatal(err)
	}
	sns := base
	sns.Kind = sinks.KindSNS
	sns.URL = ""
	sns.SinkConfig = map[string]any{
		"topicArn": "arn:aws:sns:us-east-1:123456789012:t", "region": "us-east-1",
		"accessKeyId": "a", "secretAccessKey": "b", "endpoint": "https://127.0.0.1:4566",
	}
	if err := svc.validateEndpoint(context.Background(), &sns); err != nil {
		t.Fatal(err)
	}
	ps := base
	ps.Kind = sinks.KindPubSub
	ps.URL = ""
	ps.SinkConfig = map[string]any{"project": "p", "topic": "t", "credentialsJSON": "{}"}
	if err := svc.validateEndpoint(context.Background(), &ps); err != nil {
		t.Fatal(err)
	}
	ps.SinkConfig = map[string]any{"project": "p", "topic": "t"}
	if errs.CodeOf(svc.validateEndpoint(context.Background(), &ps)) != errs.CodeValidationFailed {
		t.Fatal("pubsub ambient off")
	}
	missing := base
	missing.Kind = sinks.KindNATS
	missing.SinkConfig = nil
	if errs.CodeOf(svc.validateEndpoint(context.Background(), &missing)) != errs.CodeValidationFailed {
		t.Fatal("missing sinkConfig")
	}
	encDoc := base
	encDoc.SinkConfig = map[string]any{
		"url": "nats://127.0.0.1:4222", "subject": "eb.{type}",
		"creds": map[string]any{"dek": "d", "kid": "k", "nonce": []byte{1}, "ct": []byte{2}},
	}
	if err := svc.validateEndpoint(context.Background(), &encDoc); err != nil {
		t.Fatal(err)
	}
}

func TestSinkHelpersOddsAndEnds(t *testing.T) {
	if !errorsIsBlocked(egress.ErrBlocked) || errorsIsBlocked(errors.New("x")) {
		t.Fatal("blocked")
	}
	if got := bytesFromAny("hi"); string(got) != "hi" {
		t.Fatal(got)
	}
	if bytesFromAny(nil) != nil || bytesFromAny(123) != nil {
		t.Fatal("bytes")
	}
	s, ok := stringField(map[string]any{"a": "b", "n": 1}, "a")
	if !ok || s != "b" {
		t.Fatal(s, ok)
	}
	if _, ok := stringField(map[string]any{"n": 1}, "n"); ok {
		t.Fatal("non-string")
	}
	_ = cloneMap(nil)
	_ = sinkConfigView(sinks.KindSQS, map[string]any{"secretAccessKey": "plain"})
	_ = allowAmbientDummy()
}

func allowAmbientDummy() bool {
	return (&Service{}).allowAmbientCredentials(context.Background())
}

func TestAsEncryptedBSONShapes(t *testing.T) {
	enc, ok := asEncrypted(bson.D{
		{Key: "kid", Value: "local:t"}, {Key: "dek", Value: "dek1"},
		{Key: "nonce", Value: bson.Binary{Data: []byte{1, 2}}},
		{Key: "ct", Value: bson.Binary{Data: []byte{3, 4}}},
	})
	if !ok || enc.DEK != "dek1" || len(enc.Nonce) != 2 || len(enc.CT) != 2 {
		t.Fatalf("%+v %v", enc, ok)
	}
	enc2, ok := asEncrypted(bson.M{"dek": "d", "kid": "k", "ct": []byte{9}})
	if !ok || enc2.DEK != "d" {
		t.Fatal(enc2, ok)
	}
	if _, ok := asEncrypted(42); ok {
		t.Fatal("unexpected")
	}
}

func TestParseTypedSinkAndHosts(t *testing.T) {
	svc := &Service{Egress: egress.NewDialer(egress.Policy{BlockPrivate: true})}
	if _, err := parseTypedSink("nope", map[string]any{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	for _, kind := range []string{sinks.KindNATS, sinks.KindKafka, sinks.KindSQS, sinks.KindSNS, sinks.KindPubSub} {
		cfg, err := parseTypedSink(kind, map[string]any{})
		if err != nil {
			t.Fatal(kind, err)
		}
		_ = cfg
	}
	if err := svc.checkSinkHost(context.Background(), "http://10.0.0.1/"); errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("blocked: %v", err)
	}
	if err := svc.checkSinkHost(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if brokerHost("127.0.0.1:9092") != "127.0.0.1" || brokerHost("kafka://x:1") != "x" {
		t.Fatal("brokerHost")
	}
}

func TestEncryptSinkConfigKeepsPrevious(t *testing.T) {
	local, err := crypto.NewLocal([]crypto.LocalKey{{ID: "t", Key: bytes.Repeat([]byte{7}, crypto.KeySize)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	storeMem := &testKeyStore{keys: map[string]crypto.DataKey{}}
	var seq int
	svc := &Service{Cipher: &crypto.Cipher{
		KEK: local, Store: storeMem,
		NewID: func() string { seq++; return fmt.Sprintf("dek_%026d", seq) },
	}}
	ep := &Endpoint{ID: store.NewID(store.PrefixEndpoint), OrganizationID: store.NewID(store.PrefixOrganization), Kind: sinks.KindKafka}
	prev, err := svc.encryptSinkConfig(context.Background(), ep, map[string]any{
		"brokers": []any{"127.0.0.1:9092"}, "topic": "t", "saslPassword": "secret-one",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := svc.encryptSinkConfig(context.Background(), ep, map[string]any{
		"brokers": []any{"127.0.0.1:9092"}, "topic": "t", "saslPassword": sinkSecretMarker,
	}, prev)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := asEncrypted(merged["saslPassword"]); !ok {
		t.Fatalf("expected encrypted keep: %#v", merged["saslPassword"])
	}
	view := sinkConfigView(sinks.KindKafka, merged)
	if view["saslPassword"] != sinkSecretMarker {
		t.Fatalf("view %#v", view)
	}
	plain, err := svc.decryptSinkConfig(context.Background(), &Endpoint{
		ID: ep.ID, OrganizationID: ep.OrganizationID, Kind: sinks.KindKafka, SinkConfig: merged,
	})
	if err != nil || plain["saslPassword"] != "secret-one" {
		t.Fatalf("decrypt %#v %v", plain, err)
	}
}

type testKeyStore struct {
	mu   sync.Mutex
	keys map[string]crypto.DataKey
}

func (m *testKeyStore) Active(_ context.Context, org string) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.keys {
		ko := ""
		if k.OrganizationID != nil {
			ko = *k.OrganizationID
		}
		if ko == org && k.Status == crypto.DEKActive {
			kk := k
			return &kk, nil
		}
	}
	return nil, nil
}

func (m *testKeyStore) Get(_ context.Context, id string) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[id]
	if !ok {
		return nil, crypto.ErrNotFound
	}
	return &k, nil
}

func (m *testKeyStore) Insert(_ context.Context, k crypto.DataKey) (*crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[k.ID] = k
	return &k, nil
}

func (m *testKeyStore) All(context.Context) ([]crypto.DataKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]crypto.DataKey, 0, len(m.keys))
	for _, k := range m.keys {
		out = append(out, k)
	}
	return out, nil
}

func (m *testKeyStore) UpdateWrap(_ context.Context, id, kek string, w []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.keys[id]
	k.KEKID, k.Wrapped = kek, w
	m.keys[id] = k
	return nil
}

func (m *testKeyStore) Retire(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.keys[id]
	k.Status = crypto.DEKRetired
	m.keys[id] = k
	return nil
}
