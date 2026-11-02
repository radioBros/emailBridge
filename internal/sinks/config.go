package sinks

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Config holds the already-decrypted per-kind sinkConfig plus cache identity.
type Config struct {
	EndpointID string
	Version    int64
	NATS       *NATSConfig
	Kafka      *KafkaConfig
	SQS        *SQSConfig
	SNS        *SNSConfig
	PubSub     *PubSubConfig
}

// NATSConfig is sinkConfig for kind=nats (S7 §6).
type NATSConfig struct {
	URL       string `json:"url"`       // nats://… or tls://…; no userinfo
	Creds     string `json:"creds"`     // decrypted .creds file contents (optional)
	Subject   string `json:"subject"`   // template, e.g. eb.{type}
	JetStream bool   `json:"jetstream"` // JetStream publish with Nats-Msg-Id
}

// KafkaConfig is sinkConfig for kind=kafka.
type KafkaConfig struct {
	Brokers       []string `json:"brokers"`
	Topic         string   `json:"topic"`
	TLS           bool     `json:"tls"`
	SASLMechanism string   `json:"saslMechanism"` // "", PLAIN, SCRAM-SHA-256, SCRAM-SHA-512
	SASLUsername  string   `json:"saslUsername"`
	SASLPassword  string   `json:"saslPassword"`
}

// SQSConfig is sinkConfig for kind=sqs.
type SQSConfig struct {
	QueueURL        string `json:"queueUrl"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"` // optional override (moto / LocalStack-style)
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// SNSConfig is sinkConfig for kind=sns.
type SNSConfig struct {
	TopicARN        string `json:"topicArn"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// PubSubConfig is sinkConfig for kind=pubsub.
type PubSubConfig struct {
	Project         string `json:"project"`
	Topic           string `json:"topic"`
	CredentialsJSON string `json:"credentialsJSON"` // decrypted service-account JSON (optional with ADC / emulator)
	EmulatorHost    string `json:"emulatorHost"`    // host:port for the Pub/Sub emulator
}

// SecretFields lists sinkConfig keys that are encrypted at rest (never returned).
func SecretFields(kind string) []string {
	switch kind {
	case KindNATS:
		return []string{"creds"}
	case KindKafka:
		return []string{"saslPassword"}
	case KindSQS, KindSNS:
		return []string{"secretAccessKey"}
	case KindPubSub:
		return []string{"credentialsJSON"}
	default:
		return nil
	}
}

// Validate checks NATS config for create/update (422).
func (c NATSConfig) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return validation("nats.url is required")
	}
	if err := checkNoUserinfoURL(c.URL, "nats.url"); err != nil {
		return err
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return validation("nats.url must be a valid nats/tls/ws URL with a host")
	}
	switch strings.ToLower(u.Scheme) {
	case "nats", "tls", "ws", "wss":
	default:
		return validation("nats.url scheme must be nats, tls, ws or wss")
	}
	if strings.TrimSpace(c.Subject) == "" {
		return validation("nats.subject is required")
	}
	return checkTemplate(c.Subject, "nats.subject")
}

// Validate checks Kafka config.
func (c KafkaConfig) Validate() error {
	if len(c.Brokers) == 0 {
		return validation("kafka.brokers is required")
	}
	for i, b := range c.Brokers {
		b = strings.TrimSpace(b)
		if b == "" {
			return validation(fmt.Sprintf("kafka.brokers[%d] is empty", i))
		}
		if strings.Contains(b, "://") {
			if err := checkNoUserinfoURL(b, "kafka.brokers"); err != nil {
				return err
			}
		}
		if strings.Contains(b, "@") {
			return validation("kafka.brokers must not contain credentials")
		}
	}
	if strings.TrimSpace(c.Topic) == "" {
		return validation("kafka.topic is required")
	}
	if err := checkTemplate(c.Topic, "kafka.topic"); err != nil {
		return err
	}
	switch strings.ToUpper(strings.TrimSpace(c.SASLMechanism)) {
	case "", "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
	default:
		return validation("kafka.saslMechanism must be PLAIN, SCRAM-SHA-256, SCRAM-SHA-512 or empty")
	}
	if c.SASLMechanism != "" && strings.TrimSpace(c.SASLUsername) == "" {
		return validation("kafka.saslUsername is required when saslMechanism is set")
	}
	return nil
}

// Validate checks SQS config. When allowAmbient is false, accessKeyId and secretAccessKey are required.
func (c SQSConfig) Validate(allowAmbient bool) error {
	if strings.TrimSpace(c.QueueURL) == "" {
		return validation("sqs.queueUrl is required")
	}
	if err := checkNoUserinfoURL(c.QueueURL, "sqs.queueUrl"); err != nil {
		return err
	}
	u, err := url.Parse(c.QueueURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return validation("sqs.queueUrl must be an http(s) URL")
	}
	if strings.TrimSpace(c.Region) == "" {
		return validation("sqs.region is required")
	}
	if c.Endpoint != "" {
		if err := checkNoUserinfoURL(c.Endpoint, "sqs.endpoint"); err != nil {
			return err
		}
	}
	if (c.AccessKeyID == "") != (c.SecretAccessKey == "") {
		return validation("sqs access key and secret must both be set or both empty")
	}
	if !allowAmbient && c.AccessKeyID == "" {
		return validation(errAmbientDisabledMsg)
	}
	return nil
}

// Validate checks SNS config. When allowAmbient is false, accessKeyId and secretAccessKey are required.
func (c SNSConfig) Validate(allowAmbient bool) error {
	if strings.TrimSpace(c.TopicARN) == "" {
		return validation("sns.topicArn is required")
	}
	if !isSNSTopicARN(c.TopicARN) {
		return validation("sns.topicArn must look like arn:aws:sns:region:account:name")
	}
	if strings.TrimSpace(c.Region) == "" {
		return validation("sns.region is required")
	}
	if c.Endpoint != "" {
		if err := checkNoUserinfoURL(c.Endpoint, "sns.endpoint"); err != nil {
			return err
		}
	}
	if (c.AccessKeyID == "") != (c.SecretAccessKey == "") {
		return validation("sns access key and secret must both be set or both empty")
	}
	if !allowAmbient && c.AccessKeyID == "" {
		return validation(errAmbientDisabledMsg)
	}
	return nil
}

// Validate checks Pub/Sub config. When allowAmbient is false, credentialsJSON is required.
func (c PubSubConfig) Validate(allowAmbient bool) error {
	if strings.TrimSpace(c.Project) == "" {
		return validation("pubsub.project is required")
	}
	if strings.TrimSpace(c.Topic) == "" {
		return validation("pubsub.topic is required")
	}
	if err := checkTemplate(c.Topic, "pubsub.topic"); err != nil {
		return err
	}
	if c.EmulatorHost != "" {
		if strings.Contains(c.EmulatorHost, "://") {
			return validation("pubsub.emulatorHost must be host:port without a scheme")
		}
		if strings.Contains(c.EmulatorHost, "@") {
			return validation("pubsub.emulatorHost must not contain credentials")
		}
	}
	if !allowAmbient && c.CredentialsJSON == "" {
		return validation(errAmbientDisabledMsg)
	}
	return nil
}

func validation(msg string) error {
	return errs.New(errs.CodeValidationFailed, msg)
}

func checkNoUserinfoURL(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return validation(field + " is not a valid URL")
	}
	if u.User != nil {
		return validation(field + " must not contain credentials")
	}
	return nil
}

func isSNSTopicARN(arn string) bool {
	// arn:aws:sns:region:account:name[.fifo]
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return false
	}
	if parts[0] != "arn" || parts[2] != "sns" {
		return false
	}
	return parts[3] != "" && parts[4] != "" && parts[5] != ""
}

// IsFIFOQueue reports whether an SQS queue URL is FIFO.
func IsFIFOQueue(queueURL string) bool {
	u, err := url.Parse(queueURL)
	if err != nil {
		return strings.Contains(queueURL, ".fifo")
	}
	return strings.HasSuffix(u.Path, ".fifo")
}

// IsFIFOTopic reports whether an SNS topic ARN is FIFO.
func IsFIFOTopic(topicARN string) bool {
	return strings.HasSuffix(topicARN, ".fifo")
}
