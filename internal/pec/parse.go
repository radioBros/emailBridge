package pec

import (
	"crypto/x509"
	"errors"
	"net/textproto"
	"strings"
	"time"
)

// Kind is what a PEC system message is (S17 §2).
type Kind string

// Kinds.
const (
	KindEnvelope Kind = "envelope" // transport envelope: a certified message to this mailbox
	KindAnomaly  Kind = "anomaly"  // anomaly envelope: arrived without being valid PEC
	KindReceipt  Kind = "receipt"  // a receipt about a message this mailbox sent
)

// Receipt types (X-Ricevuta / daticert tipo).
const (
	ReceiptAcceptance     = "accettazione"
	ReceiptNonAcceptance  = "non-accettazione"
	ReceiptTakingCharge   = "presa-in-carico"
	ReceiptDelivery       = "avvenuta-consegna"
	ReceiptDeliveryError  = "errore-consegna"
	ReceiptDeliveryNotice = "preavviso-errore-consegna"
	ReceiptVirus          = "rilevazione-virus"
)

// CandidateHeaders are the headers sync fetches with every message of a PEC account (S17 §4).
var CandidateHeaders = []string{"X-Trasporto", "X-Ricevuta", "X-TipoRicevuta", "X-Riferimento-Message-ID", "X-VerificaSicurezza"}

// IsCandidate reports whether headers (lower-cased names → values, as sync stores them) mark a PEC system message.
func IsCandidate(get func(name string) string) bool {
	return strings.TrimSpace(get("x-trasporto")) != "" || strings.TrimSpace(get("x-ricevuta")) != ""
}

// Message is a parsed PEC system message.
type Message struct {
	Kind         Kind           `json:"kind"`
	Type         string         `json:"type"`
	ReceiptKind  string         `json:"receiptKind,omitempty"`
	RefMessageID string         `json:"refMessageId,omitempty"` // X-Riferimento-Message-ID (else daticert msgid)
	SecurityErr  bool           `json:"securityError,omitempty"`
	Cert         *Certification `json:"certification,omitempty"`
	Signature    Signature      `json:"signature"`
	// Original is postacert.eml (the original message) for envelopes and complete delivery receipts.
	Original   []byte `json:"-"`
	ParseError string `json:"parseError,omitempty"`
}

// Options configure Parse.
type Options struct {
	// Anchors are the trust anchors (EB_PEC_TRUST_ANCHORS); nil = trust unknown.
	Anchors *x509.CertPool
	// Now is the fallback check time when the daticert has no date.
	Now func() time.Time
}

// Parse parses a PEC system message from its raw source. It returns an error only when raw is not a PEC
// system message at all; a damaged one is returned with ParseError set, so the caller still records it.
func Parse(raw []byte, opt Options) (*Message, error) {
	top, err := parseEntity(raw)
	if err != nil {
		return nil, err
	}
	h := top.header
	m := &Message{
		ReceiptKind:  lower(h.Get("X-TipoRicevuta")),
		RefMessageID: strings.TrimSpace(h.Get("X-Riferimento-Message-ID")),
		SecurityErr:  strings.EqualFold(strings.TrimSpace(h.Get("X-VerificaSicurezza")), "errore"),
	}
	switch tr, rc := lower(h.Get("X-Trasporto")), lower(h.Get("X-Ricevuta")); {
	case rc != "":
		m.Kind, m.Type = KindReceipt, rc
	case tr == "errore":
		m.Kind, m.Type = KindAnomaly, "errore"
	case tr != "":
		m.Kind, m.Type = KindEnvelope, tr
	default:
		return nil, errors.New("pec: not a PEC system message")
	}

	signed, err := m.unwrap(top, opt)
	if err != nil {
		m.ParseError = err.Error()
		if signed == nil {
			signed = top // look for the daticert anyway
		}
	}
	m.collect(signed)
	if m.Cert != nil {
		if m.RefMessageID == "" {
			m.RefMessageID = m.Cert.OriginalMessageID
		}
		if m.ReceiptKind == "" {
			m.ReceiptKind = m.Cert.ReceiptKind
		}
	} else if m.ParseError == "" && m.Kind != KindAnomaly {
		m.ParseError = "daticert.xml not found"
	}
	return m, nil
}

// unwrap verifies the S/MIME layer and returns the signed content entity.
func (m *Message) unwrap(top *entity, opt Options) (*entity, error) {
	mt, params := top.mediaType()
	at := time.Time{}
	if opt.Now != nil {
		at = opt.Now()
	}
	switch mt {
	case "multipart/signed":
		ps := parts(top.body, params["boundary"])
		if len(ps) < 2 {
			return nil, errors.New("pec: multipart/signed without signature part")
		}
		content, err := parseEntity(ps[0])
		if err != nil {
			return nil, err
		}
		sigEnt, err := parseEntity(ps[1])
		if err != nil {
			return content, err
		}
		der, err := sigEnt.decoded()
		if err != nil {
			return content, err
		}
		// The time of the check is the certification time when known (parsed before the signature check).
		probe := &Message{}
		probe.collect(content)
		if probe.Cert != nil && !probe.Cert.At.IsZero() {
			at = probe.Cert.At
		}
		sig, _ := verifyCMS(der, ps[0], at, opt.Anchors)
		if !sig.Valid && strings.Contains(sig.Error, "digest") {
			// Some stores rewrite line endings: retry on the canonical CRLF form.
			if alt, _ := verifyCMS(der, toCRLF(ps[0]), at, opt.Anchors); alt.Valid {
				sig = alt
			}
		}
		m.Signature = sig
		return content, nil
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		der, err := top.decoded()
		if err != nil {
			return nil, err
		}
		sig, inner := verifyCMS(der, nil, at, opt.Anchors)
		m.Signature = sig
		if inner == nil {
			return nil, errors.New("pec: opaque signature without content")
		}
		e, err := parseEntity(inner)
		return e, err
	}
	m.Signature = Signature{Error: errNotSigned.Error()}
	return top, errNotSigned
}

// collect finds daticert.xml and postacert.eml in the signed content.
func (m *Message) collect(e *entity) {
	walk(e, 0, func(p *entity) bool {
		mt, _ := p.mediaType()
		name := p.filename()
		switch {
		case name == "daticert.xml" || (m.Cert == nil && (mt == "application/xml" || mt == "text/xml") && strings.Contains(name, "daticert")):
			if b, err := p.decoded(); err == nil {
				if c, err := ParseDaticert(b); err == nil {
					m.Cert = c
				} else if m.ParseError == "" {
					m.ParseError = err.Error()
				}
			}
			return false
		case name == "postacert.eml" || (mt == "message/rfc822" && m.Original == nil):
			if b, err := p.decoded(); err == nil {
				m.Original = b
			}
			return false // never descend into the original: its own attachments are not ours
		}
		return true
	})
}

// HeaderGetter adapts a textproto header for IsCandidate.
func HeaderGetter(h textproto.MIMEHeader) func(string) string {
	return func(name string) string { return h.Get(name) }
}
