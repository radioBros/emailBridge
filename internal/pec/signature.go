package pec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/smallstep/pkcs7"
)

// Signature is the result of the S/MIME check of a PEC message (S17 §7).
type Signature struct {
	// Valid: the CMS signature matches the content and the signer certificate was valid at the message time.
	Valid bool `json:"valid" doc:"The CMS signature matches the content and the signer certificate was valid at the message time" example:"true"`
	// Trusted: the signer chains to a configured trust anchor; nil when no anchors are configured (unknown).
	Trusted  *bool     `json:"trusted" doc:"The signer chains to a configured trust anchor; null when no anchors are configured" example:"true"`
	Signer   string    `json:"signer,omitempty" doc:"Subject of the signing certificate" example:"CN=posta-certificata@pec.example.it, O=Example PEC S.p.A., C=IT"`
	Issuer   string    `json:"issuer,omitempty" doc:"Issuer of the signing certificate" example:"CN=Example PEC CA, O=Example PEC S.p.A., C=IT"`
	NotAfter time.Time `json:"notAfter,omitempty" doc:"Expiry of the signing certificate" example:"2027-06-30T23:59:59Z"`
	Error    string    `json:"error,omitempty" doc:"Why the signature check failed, when it failed" example:"x509: certificate has expired"`
}

// verifyCMS checks a CMS SignedData (der). content is the signed content for a detached signature, nil for an
// opaque one (the content is inside der). It returns the result and, for an opaque signature, the content.
func verifyCMS(der, content []byte, at time.Time, anchors *x509.CertPool) (Signature, []byte) {
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return Signature{Error: "unreadable signature: " + err.Error()}, nil
	}
	if content != nil {
		p7.Content = content
	}
	var sig Signature
	signer := p7.GetOnlySigner()
	if signer == nil && len(p7.Certificates) > 0 {
		signer = p7.Certificates[0]
	}
	if signer != nil {
		sig.Signer, sig.Issuer, sig.NotAfter = signer.Subject.String(), signer.Issuer.String(), signer.NotAfter.UTC()
	}
	if at.IsZero() {
		at = time.Now()
	}
	if err := p7.VerifyWithChainAtTime(nil, at); err != nil {
		sig.Error = err.Error()
	} else if signer == nil {
		sig.Error = "no signer certificate"
	} else if at.Before(signer.NotBefore) || at.After(signer.NotAfter) {
		sig.Error = fmt.Sprintf("signer certificate not valid at %s", at.UTC().Format(time.RFC3339))
	} else {
		sig.Valid = true
	}
	if anchors != nil {
		trusted := sig.Valid && p7.VerifyWithChainAtTime(anchors, at) == nil
		sig.Trusted = &trusted
		if sig.Valid && !trusted && sig.Error == "" {
			sig.Error = "signer does not chain to a trust anchor"
		}
	}
	return sig, p7.Content
}

// errNotSigned is returned when a candidate carries no S/MIME signature.
var errNotSigned = errors.New("pec: message is not S/MIME signed")
