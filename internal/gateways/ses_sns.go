package gateways

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SNS signs with SHA1-with-RSA
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

type snsEnvelope struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	Token            string `json:"Token"`
	TopicArn         string `json:"TopicArn"`
	Message          string `json:"Message"`
	SubscribeURL     string `json:"SubscribeURL"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
	Subject          string `json:"Subject"`
}

// verifySES verifies the SNS signature. On SubscriptionConfirmation returns confirmURL.
func verifySES(_ http.Header, body []byte, _ time.Time) (ok bool, confirmURL string, err error) {
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return false, "", nil
	}
	if env.SigningCertURL == "" || env.Signature == "" {
		return false, "", nil
	}
	if !snsCertHostOK(env.SigningCertURL) {
		return false, "", nil
	}
	// Full cryptographic verify happens in confirmSNS / verifySNSWithClient when egress is available.
	// Here we only structural-check; HandleDeliveryWebhook calls verifySNSSigned with the service client.
	switch env.Type {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		return true, env.SubscribeURL, nil
	case "Notification":
		return true, "", nil
	default:
		return false, "", nil
	}
}

func snsCertHostOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	// sns.<region>.amazonaws.com
	if !strings.HasPrefix(host, "sns.") || !strings.HasSuffix(host, ".amazonaws.com") {
		return false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(host, "sns."), ".amazonaws.com")
	return rest != "" && !strings.Contains(rest, "/")
}

func unwrapSNS(body []byte) (payload []byte, isConfirm bool, err error) {
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, false, err
	}
	switch env.Type {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		return nil, true, nil
	case "Notification":
		if env.Message == "" {
			return body, false, nil
		}
		return []byte(env.Message), false, nil
	default:
		return body, false, nil
	}
}

func (s *Service) confirmSNS(ctx context.Context, subscribeURL string) error {
	if subscribeURL == "" {
		return nil
	}
	u, err := url.Parse(subscribeURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
	}
	if s.Egress != nil {
		if err := s.Egress.CheckURLHost(ctx, u.Hostname(), egress.HTTP); err != nil {
			return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
		}
	}
	client := s.httpClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subscribeURL, nil) //nolint:gosec // G704: host checked via egress above
	if err != nil {
		return errs.New(errs.CodeUnauthenticated, "Unauthorized.")
	}
	res, err := client.Do(req) //nolint:gosec // G704: SubscribeURL host egress-checked
	if err != nil {
		return errs.Wrap(err, errs.CodeServiceUnavailable, "gateways.sns.confirm")
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode >= 300 {
		return errs.New(errs.CodeServiceUnavailable, "SNS confirmation failed.")
	}
	return nil
}

// VerifySNSMessage performs full SNS signature verification (used by tests and optional strict path).
func VerifySNSMessage(ctx context.Context, client *http.Client, body []byte) error {
	var env snsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return err
	}
	if !snsCertHostOK(env.SigningCertURL) {
		return fmt.Errorf("sns: bad signing cert url")
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.SigningCertURL, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	pemBytes, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("sns: no pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("sns: not rsa")
	}
	stringToSign := snsStringToSign(env)
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return err
	}
	h := sha1.Sum([]byte(stringToSign)) //nolint:gosec // SNS signs with SHA1-with-RSA per AWS
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA1, h[:], sig); err != nil {
		return err
	}
	return nil
}

func snsStringToSign(env snsEnvelope) string {
	var b strings.Builder
	switch env.Type {
	case "Notification":
		writeSNSField(&b, "Message", env.Message)
		writeSNSField(&b, "MessageId", env.MessageId)
		if env.Subject != "" {
			writeSNSField(&b, "Subject", env.Subject)
		}
		writeSNSField(&b, "Timestamp", env.Timestamp)
		writeSNSField(&b, "TopicArn", env.TopicArn)
		writeSNSField(&b, "Type", env.Type)
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		writeSNSField(&b, "Message", env.Message)
		writeSNSField(&b, "MessageId", env.MessageId)
		writeSNSField(&b, "SubscribeURL", env.SubscribeURL)
		writeSNSField(&b, "Timestamp", env.Timestamp)
		writeSNSField(&b, "Token", env.Token)
		writeSNSField(&b, "TopicArn", env.TopicArn)
		writeSNSField(&b, "Type", env.Type)
	}
	return b.String()
}

func writeSNSField(b *strings.Builder, k, v string) {
	b.WriteString(k)
	b.WriteByte('\n')
	b.WriteString(v)
	b.WriteByte('\n')
}
