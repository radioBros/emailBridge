//go:build integration

package webhook

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/sinks"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func ownerCaller(h *webhookHarness) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: h.orgID, Role: tenancy.RoleOwner, InstanceOwner: true,
	})
}

func TestIntegrationNATSSinkDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	dir := t.TempDir()
	opts := &server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, StoreDir: dir}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(srv.Shutdown)
	url := srv.ClientURL()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	sub, err := nc.SubscribeSync("eb.message.received")
	if err != nil {
		t.Fatal(err)
	}

	accountID := store.NewID(store.PrefixAccount)
	secretEP, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "nats-secret", Kind: sinks.KindNATS, Events: []string{events.MessageReceived},
		SinkConfig: map[string]any{"url": url, "subject": "eb.secret.{type}", "creds": "CREDENTIALS-FILE"},
		Retry:      RetryOptions{MaxAttempts: 1, BackoffBase: "1s", Timeout: "1s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := endpointView(secretEP)
	if view.SinkConfig["creds"] != sinkSecretMarker {
		t.Fatalf("creds leaked: %#v", view.SinkConfig)
	}
	var raw bson.M
	if err := h.db.Collection(EndpointsCollection).FindOne(h.ctx, bson.M{"_id": secretEP.ID}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	sinkRaw, _ := raw["sinkConfig"].(bson.M)
	if s, ok := sinkRaw["creds"].(string); ok && s == "CREDENTIALS-FILE" {
		t.Fatal("plain creds in mongo")
	}
	kept, err := h.service.UpdateEndpoint(h.ctx, secretEP.ID, EndpointUpdate{
		SinkConfig: &map[string]any{"url": url, "subject": "eb.secret.{type}", "creds": sinkSecretMarker},
	}, formatETag(secretEP.Version))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := h.service.decryptSinkConfig(h.ctx, kept)
	if err != nil || plain["creds"] != "CREDENTIALS-FILE" {
		t.Fatalf("patch keep: %#v %v", plain, err)
	}

	ep, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "nats-sink", Kind: sinks.KindNATS, Events: []string{events.MessageReceived},
		Filter:     EndpointFilter{AccountIDs: []string{accountID}},
		SinkConfig: map[string]any{"url": url, "subject": "eb.{type}"},
		Retry:      RetryOptions{MaxAttempts: 3, BackoffBase: "1s", BackoffJitter: .2, Timeout: "3s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := h.event(t, accountID, "nats-one")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: ep.ID, EventID: event.ID})
	if err != nil || len(rows.Data) != 1 {
		t.Fatalf("deliveries %#v %v", rows, err)
	}
	if err := h.service.HandleDelivery(h.ctx, rows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.NextMsg(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	done, _ := h.service.GetDelivery(h.ctx, rows.Data[0].ID)
	if done.State != StateSucceeded || done.Attempts[0].StatusCode != 0 {
		t.Fatalf("delivery %+v", done)
	}

	badPort := freeTCPPort(t)
	bad, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "nats-retry", Kind: sinks.KindNATS, Events: []string{events.MessageReceived},
		Filter:     EndpointFilter{AccountIDs: []string{accountID}},
		SinkConfig: map[string]any{"url": fmt.Sprintf("nats://127.0.0.1:%d", badPort), "subject": "eb.{type}"},
		Retry:      RetryOptions{MaxAttempts: 2, BackoffBase: "10ms", BackoffJitter: 0, Timeout: "500ms"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev2 := h.event(t, accountID, "retry")
	if err := h.service.RouteEvent(h.ctx, ev2); err != nil {
		t.Fatal(err)
	}
	badRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: bad.ID, EventID: ev2.ID})
	_ = h.service.HandleDelivery(h.ctx, badRows.Data[0].ID, false)
	d1, _ := h.service.GetDelivery(h.ctx, badRows.Data[0].ID)
	if d1.State != StateRetrying {
		t.Fatalf("want retrying got %s %#v", d1.State, d1.Attempts)
	}
	h.now = h.now.Add(time.Second)
	_ = h.service.HandleDelivery(h.ctx, badRows.Data[0].ID, true)
	d2, _ := h.service.GetDelivery(h.ctx, badRows.Data[0].ID)
	if d2.State != StateDeadLettered {
		t.Fatalf("want dead letter got %s", d2.State)
	}

	h.service.Egress = egress.NewDialer(egress.Policy{BlockPrivate: true})
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "nats-perm", Kind: sinks.KindNATS, Events: []string{events.MessageReceived},
		SinkConfig: map[string]any{"url": "nats://10.255.255.1:4222", "subject": "eb.{type}"},
	}); errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
		t.Fatalf("expected blocked create: %v", err)
	}
}

func TestIntegrationKafkaSinkDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := context.Background()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	hostPort := freeTCPPort(t)
	const image = "docker.redpanda.com/redpandadata/redpanda:v24.2.4"
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts("9092/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("9092/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(hostPort)}},
			}
		}),
		testcontainers.WithCmd(
			"redpanda", "start", "--overprovisioned", "--smp", "1", "--memory", "512M",
			"--reserve-memory", "0M", "--node-id", "0", "--check=false",
			"--kafka-addr", "0.0.0.0:9092",
			"--advertise-kafka-addr", fmt.Sprintf("127.0.0.1:%d", hostPort),
			"--set", "redpanda.auto_create_topics_enabled=true",
		),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForListeningPort("9092/tcp"),
				wait.ForLog("Successfully started Redpanda").WithOccurrence(1),
			).WithDeadline(2*time.Minute),
		),
	)
	if err != nil {
		t.Skipf("redpanda unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	broker := fmt.Sprintf("127.0.0.1:%d", hostPort)
	topic := "eb-webhook"
	accountID := store.NewID(store.PrefixAccount)
	ep, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "kafka-sink", Kind: sinks.KindKafka, Events: []string{events.MessageReceived},
		Filter:     EndpointFilter{AccountIDs: []string{accountID}},
		SinkConfig: map[string]any{"brokers": []any{broker}, "topic": topic},
		Retry:      RetryOptions{MaxAttempts: 2, BackoffBase: "1s", Timeout: "10s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := h.event(t, accountID, "kafka-one")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: ep.ID, EventID: event.ID})
	if err := h.service.HandleDelivery(h.ctx, rows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(topic), kgo.ConsumerGroup("wh-it"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		fetches := cl.PollFetches(context.Background())
		iter := fetches.RecordIter()
		for !iter.Done() {
			_ = iter.Next()
			done, _ := h.service.GetDelivery(h.ctx, rows.Data[0].ID)
			if done.State != StateSucceeded {
				t.Fatalf("state %s", done.State)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("kafka message timeout")
}

func startMotoWebhook(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	ctr, err := testcontainers.Run(ctx, "motoserver/moto:5.0.27",
		testcontainers.WithExposedPorts("5000/tcp"),
		testcontainers.WithEnv(map[string]string{"MOTO_PORT": "5000"}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5000/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		t.Skipf("moto unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "5000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

func TestIntegrationSQSSinkDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	endpoint := startMotoWebhook(t)
	region := "us-east-1"
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("testing", "testing", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseEndpoint = aws.String(endpoint)
	client := sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
	out, err := client.CreateQueue(context.Background(), &sqs.CreateQueueInput{QueueName: aws.String("eb-wh")})
	if err != nil {
		t.Fatal(err)
	}
	queueURL := aws.ToString(out.QueueUrl)
	if u, err := rewriteMotoURL(queueURL, endpoint); err == nil {
		queueURL = u
	}

	accountID := store.NewID(store.PrefixAccount)
	ep, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "sqs-sink", Kind: sinks.KindSQS, Events: []string{events.MessageReceived},
		Filter: EndpointFilter{AccountIDs: []string{accountID}},
		SinkConfig: map[string]any{
			"queueUrl": queueURL, "region": region, "endpoint": endpoint,
			"accessKeyId": "testing", "secretAccessKey": "testing",
		},
		Retry: RetryOptions{MaxAttempts: 2, BackoffBase: "1s", Timeout: "10s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if endpointView(ep).SinkConfig["secretAccessKey"] != sinkSecretMarker {
		t.Fatal("sqs secret leaked")
	}

	settingsSvc := settings.NewService(h.db, h.service.Cipher, features.DefaultRegistry(), nil)
	h.service.Settings = settingsSvc
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "sqs-ambient-off", Kind: sinks.KindSQS, Events: []string{events.MessageReceived},
		SinkConfig: map[string]any{"queueUrl": queueURL, "region": region, "endpoint": endpoint},
	}); err == nil {
		t.Fatal("ambient off should reject")
	}
	if _, err := settingsSvc.Patch(ownerCaller(h), "global", map[string]any{"sinks.ambientCredentials": true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "sqs-ambient-on", Kind: sinks.KindSQS, Events: []string{events.MessageReceived},
		SinkConfig: map[string]any{"queueUrl": queueURL, "region": region, "endpoint": endpoint},
	}); err != nil {
		t.Fatal(err)
	}

	event := h.event(t, accountID, "sqs-one")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: ep.ID, EventID: event.ID})
	if err := h.service.HandleDelivery(h.ctx, rows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	recv, err := client.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 1, WaitTimeSeconds: 5,
		MessageAttributeNames: []string{"All"},
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameAll,
		},
	})
	if err != nil || len(recv.Messages) != 1 {
		t.Fatalf("sqs recv %#v %v", recv, err)
	}
	done, _ := h.service.GetDelivery(h.ctx, rows.Data[0].ID)
	if done.State != StateSucceeded {
		t.Fatalf("state %s", done.State)
	}
}

func rewriteMotoURL(queueURL, endpoint string) (string, error) {
	if strings.HasPrefix(queueURL, "http") && strings.Contains(queueURL, "amazonaws.com") {
		u := strings.TrimRight(endpoint, "/")
		path := queueURL[strings.Index(queueURL, ".com")+4:]
		return u + path, nil
	}
	return queueURL, nil
}

func TestIntegrationMTLSDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	certPEM, keyPEM := testPEMPair(t)
	settingsSvc := settings.NewService(h.db, h.service.Cipher, features.DefaultRegistry(), nil)
	h.service.Settings = settingsSvc

	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "mtls-off", URL: "https://127.0.0.1:1/hook", Events: []string{events.MessageReceived},
		Security: SecurityWrite{TLS: &TLSInput{ClientCert: certPEM, ClientKey: keyPEM}},
	}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("expected feature_disabled: %v", err)
	}
	if _, err := settingsSvc.Patch(ownerCaller(h), "organization", map[string]any{"webhook.mtls": true}, ""); err != nil {
		t.Fatal(err)
	}

	srvCertPEM, srvKeyPEM := testServerPEMPair(t)
	srvPair, err := tls.X509KeyPair([]byte(srvCertPEM), []byte(srvKeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	var sawClient atomic.Bool
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{srvPair},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			sawClient.Store(true)
		}
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(tlsLn) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })
	hookURL := "https://" + ln.Addr().String() + "/hook"

	accountID := store.NewID(store.PrefixAccount)
	ep, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "mtls-on", URL: hookURL, Events: []string{events.MessageReceived},
		Filter: EndpointFilter{AccountIDs: []string{accountID}},
		Security: SecurityWrite{
			SignatureMode: SignatureStandard,
			TLS:           &TLSInput{ClientCert: certPEM, ClientKey: keyPEM, CABundle: srvCertPEM},
		},
		Retry: RetryOptions{MaxAttempts: 2, BackoffBase: "1s", Timeout: "5s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := endpointView(ep)
	if view.Security.TLS == nil || view.Security.TLS.ClientKey != sinkSecretMarker {
		t.Fatalf("tls view %+v", view.Security.TLS)
	}

	event := h.event(t, accountID, "mtls")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: ep.ID, EventID: event.ID})
	if err := h.service.HandleDelivery(h.ctx, rows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	done, _ := h.service.GetDelivery(h.ctx, rows.Data[0].ID)
	if done.State != StateSucceeded || !sawClient.Load() {
		t.Fatalf("mtls delivery state=%s sawClient=%v attempts=%#v", done.State, sawClient.Load(), done.Attempts)
	}

	noTLS, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "plain-http-to-mtls", URL: hookURL, Events: []string{events.MessageReceived},
		Filter: EndpointFilter{AccountIDs: []string{accountID}},
		Retry:  RetryOptions{MaxAttempts: 1, BackoffBase: "1s", Timeout: "3s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev2 := h.event(t, accountID, "no-cert")
	_ = h.service.RouteEvent(h.ctx, ev2)
	r2, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: noTLS.ID, EventID: ev2.ID})
	_ = h.service.HandleDelivery(h.ctx, r2.Data[0].ID, false)
	fail, _ := h.service.GetDelivery(h.ctx, r2.Data[0].ID)
	if fail.State == StateSucceeded {
		t.Fatal("expected failure without client cert")
	}
}
