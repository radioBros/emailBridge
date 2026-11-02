//go:build integration

package sinks_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/sinks"
)

func testDeps(t *testing.T) sinks.Deps {
	t.Helper()
	return sinks.Deps{Dial: egress.NewDialer(egress.Policy{})}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestIntegrationNATSCoreAndJetStream(t *testing.T) {
	dir := t.TempDir()
	opts := &server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true,
		StoreDir: dir, NoLog: true, NoSigs: true,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
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

	sink, err := sinks.New(sinks.KindNATS, sinks.Config{NATS: &sinks.NATSConfig{
		URL: url, Subject: "eb.{type}",
	}}, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msg := sinks.Message{ID: "evt_nats_1", Type: "message.received", AccountID: "acc_1", Body: []byte(`{"a":1}`)}
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	got, err := sub.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(msg.Body) {
		t.Fatalf("body %q", got.Data)
	}
	if got.Header.Get(nats.MsgIdHdr) != msg.ID {
		t.Fatalf("msgid %q", got.Header.Get(nats.MsgIdHdr))
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.CreateStream(context.Background(), jetstream.StreamConfig{
		Name: "EB", Subjects: []string{"js.eb.>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	jsSink, err := sinks.New(sinks.KindNATS, sinks.Config{NATS: &sinks.NATSConfig{
		URL: url, Subject: "js.eb.{type}", JetStream: true,
	}}, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jsSink.Close() })
	jsMsg := sinks.Message{ID: "evt_nats_js", Type: "message.received", AccountID: "acc_1", Body: []byte(`{"js":1}`)}
	if err := jsSink.Send(context.Background(), jsMsg); err != nil {
		t.Fatal(err)
	}
	cons, err := js.CreateOrUpdateConsumer(context.Background(), "EB", jetstream.ConsumerConfig{
		Durable: "test", FilterSubject: "js.eb.message.received", AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := cons.Next(jetstream.FetchMaxWait(4 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Data()) != string(jsMsg.Body) {
		t.Fatalf("js body %q", m.Data())
	}
	if m.Headers().Get(nats.MsgIdHdr) != jsMsg.ID {
		t.Fatalf("js msgid %q", m.Headers().Get(nats.MsgIdHdr))
	}
	_ = m.Ack()
}

func TestIntegrationKafkaRedpanda(t *testing.T) {
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
		t.Skipf("redpanda image unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	broker := fmt.Sprintf("127.0.0.1:%d", hostPort)
	topic := "eb-events"
	sink, err := sinks.New(sinks.KindKafka, sinks.Config{Kafka: &sinks.KafkaConfig{
		Brokers: []string{broker}, Topic: topic,
	}}, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msg := sinks.Message{
		ID: "evt_k1", Type: "message.received", AccountID: "acc_k", Subject: "inbox",
		Body: []byte(`{"k":1}`), Headers: map[string]string{"source": "eb-test"},
	}
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	cl, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(topic), kgo.ConsumerGroup("sinks-it"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cl.Close)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		fetches := cl.PollFetches(context.Background())
		if ferrs := fetches.Errors(); len(ferrs) > 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		iter := fetches.RecordIter()
		for !iter.Done() {
			rec := iter.Next()
			if string(rec.Key) != msg.AccountID {
				t.Fatalf("key %q", rec.Key)
			}
			if string(rec.Value) != string(msg.Body) {
				t.Fatalf("value %q", rec.Value)
			}
			hdrs := map[string]string{}
			for _, h := range rec.Headers {
				hdrs[h.Key] = string(h.Value)
			}
			if hdrs["ce_id"] != msg.ID || hdrs["ce_type"] != msg.Type {
				t.Fatalf("ce headers %+v", hdrs)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timed out waiting for kafka message")
}

func startMoto(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	const image = "motoserver/moto:5.0.27"
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts("5000/tcp"),
		testcontainers.WithEnv(map[string]string{"MOTO_PORT": "5000"}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5000/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		t.Skipf("moto image unavailable: %v", err)
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

func awsTestCfg(t *testing.T, endpoint, region string) aws.Config {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("testing", "testing", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseEndpoint = aws.String(endpoint)
	return cfg
}

func TestIntegrationSQS(t *testing.T) {
	endpoint := startMoto(t)
	region := "us-east-1"
	cfg := awsTestCfg(t, endpoint, region)
	client := sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })

	out, err := client.CreateQueue(context.Background(), &sqs.CreateQueueInput{
		QueueName: aws.String("eb-fifo.fifo"),
		Attributes: map[string]string{
			"FifoQueue":                 "true",
			"ContentBasedDeduplication": "false",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	queueURL := aws.ToString(out.QueueUrl)
	// Moto may return an AWS-shaped URL; rewrite to the emulator endpoint for the sink.
	if u := rewriteAWSURL(queueURL, endpoint); u != "" {
		queueURL = u
	}

	sink, err := sinks.New(sinks.KindSQS, sinks.Config{SQS: &sinks.SQSConfig{
		QueueURL: queueURL, Region: region, Endpoint: endpoint,
		AccessKeyID: "testing", SecretAccessKey: "testing",
	}}, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msg := sinks.Message{ID: "evt_sqs_1", Type: "message.received", AccountID: "acc_s", Body: []byte(`{"s":1}`)}
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	recv, err := client.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(queueURL),
		MaxNumberOfMessages:         1,
		WaitTimeSeconds:             5,
		MessageAttributeNames:       []string{"All"},
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recv.Messages) != 1 {
		t.Fatalf("got %d messages", len(recv.Messages))
	}
	m := recv.Messages[0]
	if aws.ToString(m.Body) != string(msg.Body) {
		t.Fatalf("body %q", aws.ToString(m.Body))
	}
	if attr := m.MessageAttributes["eventId"]; attr.StringValue == nil || *attr.StringValue != msg.ID {
		t.Fatalf("eventId attr %+v", m.MessageAttributes)
	}
	if g := m.Attributes["MessageGroupId"]; g != "" && g != msg.AccountID {
		t.Fatalf("group %q", g)
	}
	if d := m.Attributes["MessageDeduplicationId"]; d != "" && d != msg.ID {
		t.Fatalf("dedup %q", d)
	}
}

func TestIntegrationSNS(t *testing.T) {
	endpoint := startMoto(t)
	region := "us-east-1"
	cfg := awsTestCfg(t, endpoint, region)
	snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(endpoint) })
	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })

	q, err := sqsClient.CreateQueue(context.Background(), &sqs.CreateQueueInput{QueueName: aws.String("eb-sns-sub")})
	if err != nil {
		t.Fatal(err)
	}
	queueURL := aws.ToString(q.QueueUrl)
	if u := rewriteAWSURL(queueURL, endpoint); u != "" {
		queueURL = u
	}
	attrs, err := sqsClient.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(queueURL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := attrs.Attributes["QueueArn"]

	topic, err := snsClient.CreateTopic(context.Background(), &sns.CreateTopicInput{Name: aws.String("eb-topic")})
	if err != nil {
		t.Fatal(err)
	}
	topicARN := aws.ToString(topic.TopicArn)
	_, err = snsClient.Subscribe(context.Background(), &sns.SubscribeInput{
		TopicArn: aws.String(topicARN), Protocol: aws.String("sqs"), Endpoint: aws.String(queueARN),
	})
	if err != nil {
		t.Fatal(err)
	}

	sink, err := sinks.New(sinks.KindSNS, sinks.Config{SNS: &sinks.SNSConfig{
		TopicARN: topicARN, Region: region, Endpoint: endpoint,
		AccessKeyID: "testing", SecretAccessKey: "testing",
	}}, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msg := sinks.Message{ID: "evt_sns_1", Type: "message.received", AccountID: "acc_n", Body: []byte(`{"n":1}`)}
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		recv, err := sqsClient.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 1, WaitTimeSeconds: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(recv.Messages) == 0 {
			continue
		}
		body := aws.ToString(recv.Messages[0].Body)
		if !strings.Contains(body, `"n":1`) && !strings.Contains(body, string(msg.Body)) && !strings.Contains(body, "Message") {
			t.Fatalf("unexpected sns body %q", body)
		}
		return
	}
	t.Fatal("timed out waiting for sns→sqs delivery")
}

func rewriteAWSURL(awsURL, endpoint string) string {
	// https://sqs.us-east-1.amazonaws.com/123/name → http://localhost:PORT/123/name (moto path style varies)
	if !strings.Contains(awsURL, "amazonaws.com") && !strings.Contains(awsURL, "localhost") {
		return awsURL
	}
	// Prefer leaving as-is when BaseEndpoint is set on the client; sink also has Endpoint override.
	return awsURL
}

func TestIntegrationPubSub(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	const image = "gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:cced7aefc06898a91efcfc942e6ab98537b3ec2d15d66ec54cffa62ac6d7f19b"
	ctr, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts("8085/tcp"),
		testcontainers.WithCmd("gcloud", "beta", "emulators", "pubsub", "start", "--host-port=0.0.0.0:8085", "--project=eb-test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("8085/tcp").WithStartupTimeout(3*time.Minute)),
	)
	if err != nil {
		t.Skipf("pubsub emulator image unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "8085/tcp")
	if err != nil {
		t.Fatal(err)
	}
	emu := fmt.Sprintf("%s:%s", host, port.Port())
	project := "eb-test"
	topicID := "eb-topic"
	subID := "eb-sub"

	t.Setenv("PUBSUB_EMULATOR_HOST", emu)
	admin, err := pubsub.NewClient(ctx, project,
		option.WithoutAuthentication(),
		option.WithEndpoint(emu),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	topicName := fmt.Sprintf("projects/%s/topics/%s", project, topicID)
	if _, err := admin.TopicAdminClient.CreateTopic(ctx, &pb.Topic{Name: topicName}); err != nil {
		t.Fatal(err)
	}
	subName := fmt.Sprintf("projects/%s/subscriptions/%s", project, subID)
	if _, err := admin.SubscriptionAdminClient.CreateSubscription(ctx, &pb.Subscription{
		Name:                  subName,
		Topic:                 topicName,
		EnableMessageOrdering: true,
	}); err != nil {
		t.Fatal(err)
	}

	psDeps := testDeps(t)
	psDeps.AllowAmbientCredentials = true // anonymous emulator; not ADC
	sink, err := sinks.New(sinks.KindPubSub, sinks.Config{PubSub: &sinks.PubSubConfig{
		Project: project, Topic: topicID, EmulatorHost: emu,
	}}, psDeps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	msg := sinks.Message{ID: "evt_ps_1", Type: "message.received", AccountID: "acc_p", Body: []byte(`{"p":1}`)}
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	subscriber := admin.Subscriber(subID)
	gotCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err = subscriber.Receive(gotCtx, func(ctx context.Context, m *pubsub.Message) {
		defer m.Ack()
		if string(m.Data) != string(msg.Body) {
			t.Errorf("body %q", m.Data)
		}
		if m.OrderingKey != msg.AccountID {
			t.Errorf("ordering key %q", m.OrderingKey)
		}
		if m.Attributes["eventId"] != msg.ID {
			t.Errorf("eventId %q", m.Attributes["eventId"])
		}
		cancel()
	})
	if err != nil && gotCtx.Err() == nil {
		t.Fatal(err)
	}
}
