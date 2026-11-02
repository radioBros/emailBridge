package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/sinks"
)

const sinkSecretMarker = "[set]"

// encryptSinkConfig encrypts credential fields; empty/marker values keep previous secrets.
func (s *Service) encryptSinkConfig(ctx context.Context, endpoint *Endpoint, incoming, previous map[string]any) (map[string]any, error) {
	if incoming == nil {
		incoming = map[string]any{}
	}
	out := cloneMap(incoming)
	for _, field := range sinks.SecretFields(endpoint.Kind) {
		plain, has := stringField(out, field)
		if !has || plain == "" || plain == sinkSecretMarker {
			if previous != nil {
				if prev, ok := previous[field]; ok {
					if enc, ok := asEncrypted(prev); ok {
						out[field] = enc
					} else {
						out[field] = prev
					}
					continue
				}
			}
			delete(out, field)
			continue
		}
		enc, err := s.Cipher.EncryptString(ctx, endpoint.OrganizationID, plain, crypto.AAD{
			Collection: EndpointsCollection, Field: "sinkConfig." + field, DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out[field] = enc
	}
	return out, nil
}

// decryptSinkConfig returns a plain map suitable for sinks.New (secrets decrypted).
func (s *Service) decryptSinkConfig(ctx context.Context, endpoint *Endpoint) (map[string]any, error) {
	raw := endpoint.SinkConfig
	if raw == nil {
		return map[string]any{}, nil
	}
	out := cloneMap(raw)
	for _, field := range sinks.SecretFields(endpoint.Kind) {
		v, ok := out[field]
		if !ok {
			continue
		}
		enc, ok := asEncrypted(v)
		if !ok || enc.IsZero() {
			continue
		}
		plain, err := s.Cipher.DecryptString(ctx, enc, crypto.AAD{
			Collection: EndpointsCollection, Field: "sinkConfig." + field, DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out[field] = plain
	}
	return out, nil
}

func sinkConfigView(kind string, raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := cloneMap(raw)
	for _, field := range sinks.SecretFields(kind) {
		if v, ok := out[field]; ok {
			if enc, ok := asEncrypted(v); ok && !enc.IsZero() {
				out[field] = sinkSecretMarker
			} else if s, ok := v.(string); ok && s != "" {
				out[field] = sinkSecretMarker
			}
		}
	}
	return out
}

func parseTypedSink(kind string, plain map[string]any) (sinks.Config, error) {
	cfg := sinks.Config{}
	data, err := json.Marshal(plain)
	if err != nil {
		return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid")
	}
	switch kind {
	case sinks.KindNATS:
		var c sinks.NATSConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid for nats")
		}
		cfg.NATS = &c
	case sinks.KindKafka:
		var c sinks.KafkaConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid for kafka")
		}
		cfg.Kafka = &c
	case sinks.KindSQS:
		var c sinks.SQSConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid for sqs")
		}
		cfg.SQS = &c
	case sinks.KindSNS:
		var c sinks.SNSConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid for sns")
		}
		cfg.SNS = &c
	case sinks.KindPubSub:
		var c sinks.PubSubConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return cfg, errs.New(errs.CodeValidationFailed, "sinkConfig is invalid for pubsub")
		}
		cfg.PubSub = &c
	default:
		return cfg, errs.New(errs.CodeValidationFailed, fmt.Sprintf("unknown sink kind %q", kind))
	}
	return cfg, nil
}

func (s *Service) validateSinkConfig(ctx context.Context, endpoint *Endpoint) error {
	if endpoint.SinkConfig == nil {
		return errs.New(errs.CodeValidationFailed, "sinkConfig is required for non-HTTP endpoints.")
	}
	allowAmbient := s.allowAmbientCredentials(ctx)
	// Validate with placeholders for encrypted fields already stored as documents.
	plain := cloneMap(endpoint.SinkConfig)
	for _, field := range sinks.SecretFields(endpoint.Kind) {
		if v, ok := plain[field]; ok {
			if _, isEnc := asEncrypted(v); isEnc {
				plain[field] = "x" // non-empty so Validate does not require ambient
			} else if _, isMap := v.(map[string]any); isMap {
				plain[field] = "x" // Mongo-decoded encrypted document
			} else if _, isD := v.(bson.D); isD {
				plain[field] = "x"
			} else if _, isM := v.(bson.M); isM {
				plain[field] = "x"
			}
		}
	}
	typed, err := parseTypedSink(endpoint.Kind, plain)
	if err != nil {
		return err
	}
	switch endpoint.Kind {
	case sinks.KindNATS:
		if err := typed.NATS.Validate(); err != nil {
			return err
		}
		return s.checkSinkHost(ctx, typed.NATS.URL)
	case sinks.KindKafka:
		if err := typed.Kafka.Validate(); err != nil {
			return err
		}
		for _, b := range typed.Kafka.Brokers {
			if err := s.checkSinkHost(ctx, brokerHost(b)); err != nil {
				return err
			}
		}
		return nil
	case sinks.KindSQS:
		if err := typed.SQS.Validate(allowAmbient); err != nil {
			return err
		}
		host := typed.SQS.Endpoint
		if host == "" {
			host = typed.SQS.QueueURL
		}
		return s.checkSinkHost(ctx, host)
	case sinks.KindSNS:
		if err := typed.SNS.Validate(allowAmbient); err != nil {
			return err
		}
		if typed.SNS.Endpoint != "" {
			return s.checkSinkHost(ctx, typed.SNS.Endpoint)
		}
		return nil
	case sinks.KindPubSub:
		if err := typed.PubSub.Validate(allowAmbient); err != nil {
			return err
		}
		if typed.PubSub.EmulatorHost != "" {
			return s.checkSinkHost(ctx, typed.PubSub.EmulatorHost)
		}
		return nil
	default:
		return errs.New(errs.CodeValidationFailed, "Unsupported webhook endpoint kind.")
	}
}

func (s *Service) allowAmbientCredentials(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}
	body, err := s.Settings.Effective(ctx, "")
	if err != nil {
		return false
	}
	sv, ok := body.Values["sinks.ambientCredentials"]
	if !ok {
		return false
	}
	b, ok := sv.Value.(bool)
	return ok && b
}

func (s *Service) checkSinkHost(ctx context.Context, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || s.Egress == nil {
		return nil
	}
	host := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return nil
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	} else if i := strings.IndexByte(raw, ':'); i > 0 && !strings.Contains(raw, "/") {
		host = raw[:i]
	}
	if host == "" {
		return nil
	}
	if err := s.Egress.CheckURLHost(ctx, host, egress.HTTP); err != nil {
		if errorsIsBlocked(err) {
			s.securityLog().WarnContext(ctx, "sink host blocked",
				"host", host, "code", errs.CodeEndpointURLBlocked)
			return errs.New(errs.CodeEndpointURLBlocked, "The sink host is blocked by the egress policy.")
		}
		return errs.Wrap(err, errs.CodeValidationFailed, "webhook.validate_sink_host")
	}
	return nil
}

func errorsIsBlocked(err error) bool {
	return errors.Is(err, egress.ErrBlocked)
}

func brokerHost(b string) string {
	b = strings.TrimSpace(b)
	if strings.Contains(b, "://") {
		if u, err := url.Parse(b); err == nil {
			return u.Hostname()
		}
	}
	if h, _, err := net.SplitHostPort(b); err == nil {
		return h
	}
	return b
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func asEncrypted(v any) (crypto.EncryptedString, bool) {
	switch t := v.(type) {
	case crypto.EncryptedString:
		return t, true
	case *crypto.EncryptedString:
		if t == nil {
			return crypto.EncryptedString{}, false
		}
		return *t, true
	case map[string]any:
		return encryptedFromMap(t)
	case bson.M:
		return encryptedFromMap(t)
	case bson.D:
		m := make(map[string]any, len(t))
		for _, e := range t {
			m[e.Key] = e.Value
		}
		return encryptedFromMap(m)
	default:
		return crypto.EncryptedString{}, false
	}
}

func encryptedFromMap(t map[string]any) (crypto.EncryptedString, bool) {
	enc := crypto.EncryptedString{}
	if dek, ok := t["dek"].(string); ok {
		enc.DEK = dek
	}
	if kid, ok := t["kid"].(string); ok {
		enc.KID = kid
	}
	enc.Nonce = bytesFromAny(t["nonce"])
	enc.CT = bytesFromAny(t["ct"])
	return enc, !enc.IsZero()
}

func bytesFromAny(v any) []byte {
	switch b := v.(type) {
	case []byte:
		return b
	case string:
		return []byte(b)
	case bson.Binary:
		return b.Data
	default:
		return nil
	}
}
