package pec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
)

// Signer signs PEC system messages: the provider's certificate and key, plus intermediates.
type Signer struct {
	Cert  *x509.Certificate
	Key   crypto.Signer
	Chain []*x509.Certificate
}

// CA is a test certification authority for simulated PEC providers (S17 §11). Never used in production.
type CA struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// NewCA generates a self-signed test CA.
func NewCA(name string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: name, Organization: []string{"EmailBridge PEC simulator"}},
		// Wide window: VerifyWithChainAtTime uses the message Date, which fixtures set in the past.
		NotBefore: time.Now().AddDate(-10, 0, 0), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key}, nil
}

// Issue creates a provider signing certificate valid from notBefore to notAfter (zero = a wide default).
func (ca *CA) Issue(provider, email string, notBefore, notAfter time.Time) (*Signer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if notBefore.IsZero() {
		notBefore = time.Now().AddDate(-1, 0, 0)
	}
	if notAfter.IsZero() {
		notAfter = time.Now().AddDate(5, 0, 0)
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: provider, Organization: []string{provider}},
		EmailAddresses: []string{email}, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Signer{Cert: cert, Key: key, Chain: []*x509.Certificate{ca.Cert}}, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	return n
}

// Build describes a PEC system message to generate (simulator, tests).
type Build struct {
	Kind        Kind
	Type        string // receipt type, "posta-certificata" for an envelope, "errore" for an anomaly
	ReceiptKind string // completa | breve | sintetica (delivery receipts)
	From        string // the provider's system address, e.g. posta-certificata@pec.example.it
	To          string // the mailbox that receives this message
	Sender      string
	Recipients  []Recipient
	Subject     string // original subject
	Provider    string
	At          time.Time
	TransportID string
	MessageID   string // Message-ID of this system message
	OriginalID  string // Message-ID of the original message
	DeliveredTo string
	Error       string // daticert errore, "nessuno" when empty
	ErrorDetail string
	Original    []byte // postacert.eml (envelopes, complete delivery receipts)
	Text        string // human-readable part
}

var subjectPrefix = map[string]string{
	ReceiptAcceptance: "ACCETTAZIONE: ", ReceiptNonAcceptance: "AVVISO DI NON ACCETTAZIONE: ",
	ReceiptTakingCharge: "PRESA IN CARICO: ", ReceiptDelivery: "AVVENUTA CONSEGNA: ",
	ReceiptDeliveryError: "AVVISO DI MANCATA CONSEGNA: ", ReceiptDeliveryNotice: "AVVISO DI MANCATA CONSEGNA PER SUP. TEMPO MASSIMO: ",
	ReceiptVirus: "PROBLEMA DI SICUREZZA: ", "posta-certificata": "POSTA CERTIFICATA: ", "errore": "ANOMALIA MESSAGGIO: ",
}

// Generate builds the signed RFC 822 message. A nil signer produces an unsigned message (tests of damaged input).
func (b Build) Generate(s *Signer) ([]byte, error) {
	if b.Type == "" || b.To == "" {
		return nil, errors.New("pec: build needs a type and a recipient mailbox")
	}
	at := b.At
	if at.IsZero() {
		at = time.Now()
	}
	dati, err := b.daticert(at)
	if err != nil {
		return nil, err
	}
	inner := newBoundary("mixed")
	var content bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&content, format, a...) }
	w("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", inner)
	text := b.Text
	if text == "" {
		text = fmt.Sprintf("Messaggio di sistema PEC (%s) per %q.", b.Type, b.Subject)
	}
	w("--%s\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", inner, wrap64([]byte(text)))
	w("--%s\r\nContent-Type: application/xml; name=\"daticert.xml\"\r\nContent-Disposition: attachment; filename=\"daticert.xml\"\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", inner, wrap64(dati))
	if len(b.Original) > 0 {
		w("--%s\r\nContent-Type: message/rfc822; name=\"postacert.eml\"\r\nContent-Disposition: attachment; filename=\"postacert.eml\"\r\n\r\n", inner)
		content.Write(toCRLF(b.Original))
		w("\r\n")
	}
	w("--%s--\r\n", inner)

	var out bytes.Buffer
	o := func(format string, a ...any) { fmt.Fprintf(&out, format, a...) }
	o("Return-Path: <%s>\r\nFrom: \"Per conto di: %s\" <%s>\r\nTo: %s\r\n", b.From, b.Sender, b.From, b.To)
	o("Subject: %s%s\r\nDate: %s\r\n", subjectPrefix[b.Type], b.Subject, at.Format(time.RFC1123Z))
	if b.MessageID != "" {
		o("Message-ID: %s\r\n", b.MessageID)
	}
	switch b.Kind {
	case KindReceipt:
		o("X-Ricevuta: %s\r\n", b.Type)
		if b.ReceiptKind != "" {
			o("X-TipoRicevuta: %s\r\n", b.ReceiptKind)
		}
		if b.OriginalID != "" {
			o("X-Riferimento-Message-ID: %s\r\n", b.OriginalID)
		}
	case KindAnomaly:
		o("X-Trasporto: errore\r\n")
	default:
		o("X-Trasporto: posta-certificata\r\n")
		if b.OriginalID != "" {
			o("X-Riferimento-Message-ID: %s\r\n", b.OriginalID)
		}
	}
	o("MIME-Version: 1.0\r\n")
	if s == nil {
		out.Write(content.Bytes())
		return out.Bytes(), nil
	}
	sd, err := pkcs7.NewSignedData(content.Bytes())
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSignerChain(s.Cert, s.Key, s.Chain, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	sd.Detach()
	der, err := sd.Finish()
	if err != nil {
		return nil, err
	}
	outer := newBoundary("signed")
	o("Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"%s\"\r\n\r\n", outer)
	o("This is an S/MIME signed message\r\n\r\n--%s\r\n", outer)
	out.Write(content.Bytes())
	o("\r\n--%s\r\nContent-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\n%s\r\n--%s--\r\n", outer, wrap64(der), outer)
	return out.Bytes(), nil
}

func (b Build) daticert(at time.Time) ([]byte, error) {
	errore := b.Error
	if errore == "" {
		errore = "nessuno"
	}
	var x bytes.Buffer
	x.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&x, "<postacert tipo=%q errore=%q>\n  <intestazione>\n", b.Type, errore)
	fmt.Fprintf(&x, "    <mittente>%s</mittente>\n", esc(b.Sender))
	for _, r := range b.Recipients {
		kind := r.Kind
		if kind == "" {
			kind = "certificato"
		}
		fmt.Fprintf(&x, "    <destinatari tipo=%q>%s</destinatari>\n", kind, esc(r.Address))
	}
	fmt.Fprintf(&x, "    <risposte>%s</risposte>\n    <oggetto>%s</oggetto>\n  </intestazione>\n  <dati>\n", esc(b.Sender), esc(b.Subject))
	loc := at.In(rome)
	fmt.Fprintf(&x, "    <gestore-emittente>%s</gestore-emittente>\n", esc(b.Provider))
	fmt.Fprintf(&x, "    <data zona=%q><giorno>%s</giorno><ora>%s</ora></data>\n", loc.Format("-0700"), loc.Format("02/01/2006"), loc.Format("15:04:05"))
	fmt.Fprintf(&x, "    <identificativo>%s</identificativo>\n", esc(b.TransportID))
	if b.OriginalID != "" {
		fmt.Fprintf(&x, "    <msgid>%s</msgid>\n", esc(b.OriginalID))
	}
	if b.ReceiptKind != "" {
		fmt.Fprintf(&x, "    <ricevuta tipo=%q/>\n", b.ReceiptKind)
	}
	if b.DeliveredTo != "" {
		fmt.Fprintf(&x, "    <consegna>%s</consegna>\n", esc(b.DeliveredTo))
	}
	if b.ErrorDetail != "" {
		fmt.Fprintf(&x, "    <errore-esteso>%s</errore-esteso>\n", esc(b.ErrorDetail))
	}
	x.WriteString("  </dati>\n</postacert>\n")
	return x.Bytes(), nil
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func wrap64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 76 {
		out.WriteString(s[:76])
		out.WriteString("\r\n")
		s = s[76:]
	}
	out.WriteString(s)
	return out.String()
}

func newBoundary(tag string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("----=_pec_%s_%x", tag, b)
}
