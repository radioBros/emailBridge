package webhook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

// EndpointTLS is optional mTLS material for HTTP endpoints (S7 §2.3).
type EndpointTLS struct {
	ClientCert string                 `bson:"clientCert,omitempty" json:"clientCert,omitempty" doc:"PEM client certificate presented to the endpoint" example:"-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2…\n-----END CERTIFICATE-----"`
	ClientKey  crypto.EncryptedString `bson:"clientKey,omitempty" json:"-"`
	CABundle   string                 `bson:"caBundle,omitempty" json:"caBundle,omitempty" doc:"PEM CA bundle that must have issued the endpoint certificate" example:"-----BEGIN CERTIFICATE-----\nMIIBdTCCARugAwIBAgIUY7…\n-----END CERTIFICATE-----"`
	HasKey     bool                   `bson:"-" json:"hasKey,omitempty" doc:"A private key is stored" example:"true"`
}

// TLSInput is the write shape for security.tls.
type TLSInput struct {
	ClientCert string `json:"clientCert,omitempty" doc:"PEM client certificate presented to the endpoint (mTLS; needs the webhook.mtls switch)" example:"-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2…\n-----END CERTIFICATE-----"`
	ClientKey  string `json:"clientKey,omitempty" doc:"PEM private key of the client certificate; write only, stored encrypted" example:"-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49…\n-----END PRIVATE KEY-----"`
	CABundle   string `json:"caBundle,omitempty" doc:"PEM CA bundle that must have issued the endpoint certificate" example:"-----BEGIN CERTIFICATE-----\nMIIBdTCCARugAwIBAgIUY7…\n-----END CERTIFICATE-----"`
}

type mtlsCacheKey struct {
	endpointID string
	version    int64
	timeout    time.Duration
}

func (s *Service) mtlsEnabled(ctx context.Context, orgID string) bool {
	if s.Settings == nil {
		return false
	}
	body, err := s.Settings.Effective(ctx, orgID)
	if err != nil {
		return false
	}
	sv, ok := body.Switches["webhook.mtls"]
	if !ok {
		return false
	}
	b, ok := sv.Value.(bool)
	return ok && b
}

func (s *Service) encryptTLS(ctx context.Context, endpoint *Endpoint, in *TLSInput, previous *EndpointTLS) (*EndpointTLS, error) {
	if in == nil {
		return previous, nil
	}
	if in.ClientCert == "" && in.ClientKey == "" && in.CABundle == "" {
		return nil, nil
	}
	out := &EndpointTLS{ClientCert: strings.TrimSpace(in.ClientCert), CABundle: strings.TrimSpace(in.CABundle)}
	if previous != nil && out.ClientCert == "" {
		out.ClientCert = previous.ClientCert
	}
	if previous != nil && out.CABundle == "" {
		out.CABundle = previous.CABundle
	}
	keyPlain := in.ClientKey
	if keyPlain == "" || keyPlain == sinkSecretMarker {
		if previous == nil || previous.ClientKey.IsZero() {
			return nil, errs.New(errs.CodeValidationFailed, "tls.clientKey is required when configuring mTLS.")
		}
		out.ClientKey = previous.ClientKey
	} else {
		enc, err := s.Cipher.EncryptString(ctx, endpoint.OrganizationID, keyPlain, crypto.AAD{
			Collection: EndpointsCollection, Field: "security.tls.clientKey", DocID: endpoint.ID,
		})
		if err != nil {
			return nil, err
		}
		out.ClientKey = enc
	}
	if out.ClientCert == "" {
		return nil, errs.New(errs.CodeValidationFailed, "tls.clientCert is required when configuring mTLS.")
	}
	plainKey, err := s.Cipher.DecryptString(ctx, out.ClientKey, crypto.AAD{
		Collection: EndpointsCollection, Field: "security.tls.clientKey", DocID: endpoint.ID,
	})
	if err != nil {
		return nil, err
	}
	if err := validatePEMTLS(out.ClientCert, plainKey, out.CABundle); err != nil {
		return nil, err
	}
	return out, nil
}

func validatePEMTLS(certPEM, keyPEM, caPEM string) error {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "tls client certificate and key do not match or are invalid PEM.")
	}
	if len(cert.Certificate) == 0 {
		return errs.New(errs.CodeValidationFailed, "tls.clientCert is empty.")
	}
	if caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return errs.New(errs.CodeValidationFailed, "tls.caBundle must be PEM-encoded certificates.")
		}
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return errs.New(errs.CodeValidationFailed, "tls.clientCert must be PEM-encoded.")
	}
	return nil
}

func (s *Service) httpClientFor(endpoint *Endpoint, timeout time.Duration) (*http.Client, error) {
	if endpoint.Security.TLS == nil || endpoint.Security.TLS.ClientCert == "" {
		return s.httpClient(timeout), nil
	}
	key := mtlsCacheKey{endpointID: endpoint.ID, version: endpoint.Version, timeout: timeout}
	if s.mtlsClients == nil {
		s.mtlsClients = &sync.Map{}
	}
	if c, ok := s.mtlsClients.Load(key); ok {
		return c.(*http.Client), nil
	}
	plainKey, err := s.Cipher.DecryptString(context.Background(), endpoint.Security.TLS.ClientKey, crypto.AAD{
		Collection: EndpointsCollection, Field: "security.tls.clientKey", DocID: endpoint.ID,
	})
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair([]byte(endpoint.Security.TLS.ClientCert), []byte(plainKey))
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if endpoint.Security.TLS.CABundle != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(endpoint.Security.TLS.CABundle)) {
			// Fail closed: a configured but unreadable CA bundle must not fall back to the system roots.
			return nil, errs.New(errs.CodeValidationFailed, "The endpoint's tls.caBundle cannot be read.")
		}
		tlsCfg.RootCAs = pool
	}
	base := s.Egress.HTTPClient(timeout)
	transport, ok := base.Transport.(*http.Transport)
	if !ok { // never send without the client certificate the endpoint requires
		return nil, errs.New(errs.CodeInternalError, "The egress HTTP transport cannot carry a client certificate.")
	}
	tr := transport.Clone()
	tr.TLSClientConfig = tlsCfg
	tr.ForceAttemptHTTP2 = false // custom client certs: stick to HTTP/1.1
	client := &http.Client{
		Transport:     tr,
		Timeout:       base.Timeout,
		CheckRedirect: base.CheckRedirect,
	}
	actual, _ := s.mtlsClients.LoadOrStore(key, client)
	return actual.(*http.Client), nil
}

func tlsView(t *EndpointTLS) *TLSInput {
	if t == nil || t.ClientCert == "" {
		return nil
	}
	return &TLSInput{
		ClientCert: t.ClientCert,
		ClientKey:  sinkSecretMarker,
		CABundle:   t.CABundle,
	}
}
