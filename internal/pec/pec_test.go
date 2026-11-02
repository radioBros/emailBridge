package pec

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

type fixture struct {
	ca      *CA
	signer  *Signer
	anchors *x509.CertPool
	at      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ca, err := NewCA("Sim PEC CA")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ca.Issue("Gestore Simulato S.p.A.", "posta-certificata@pec.sim.test", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return &fixture{ca: ca, signer: s, anchors: pool, at: time.Date(2026, 10, 26, 10, 15, 30, 0, time.UTC)}
}

const original = "From: sender@pec.sim.test\r\nTo: rcpt@pec.sim.test\r\nSubject: Verbale 42\r\nMessage-ID: <orig-42@legacyapp.test>\r\n\r\nBody of the original.\r\n"

func (f *fixture) delivery(t *testing.T, mut func(*Build)) []byte {
	t.Helper()
	b := Build{
		Kind: KindReceipt, Type: ReceiptDelivery, ReceiptKind: "completa",
		From: "posta-certificata@pec.sim.test", To: "sender@pec.sim.test", Sender: "sender@pec.sim.test",
		Recipients: []Recipient{{Address: "rcpt@pec.sim.test", Kind: "certificato"}, {Address: "x@example.com", Kind: "esterno"}},
		Subject:    "Verbale 42", Provider: "Gestore Simulato S.p.A.", At: f.at,
		TransportID: "opec.20261026101530.1.sim@pec.sim.test", MessageID: "<rcpt-1@pec.sim.test>",
		OriginalID: "<orig-42@legacyapp.test>", DeliveredTo: "rcpt@pec.sim.test", Original: []byte(original),
	}
	if mut != nil {
		mut(&b)
	}
	raw, err := b.Generate(f.signer)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseSignedDeliveryReceipt(t *testing.T) {
	f := newFixture(t)
	raw := f.delivery(t, nil)
	m, err := Parse(raw, Options{Anchors: f.anchors})
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindReceipt || m.Type != ReceiptDelivery || m.ReceiptKind != "completa" || m.ParseError != "" {
		t.Fatalf("message: %+v", m)
	}
	if !m.Signature.Valid || m.Signature.Trusted == nil || !*m.Signature.Trusted || m.Signature.Error != "" {
		t.Fatalf("signature: %+v", m.Signature)
	}
	if !strings.Contains(m.Signature.Signer, "Gestore Simulato") {
		t.Fatalf("signer: %q", m.Signature.Signer)
	}
	c := m.Cert
	if c == nil || c.TransportID != "opec.20261026101530.1.sim@pec.sim.test" || c.DeliveredTo != "rcpt@pec.sim.test" ||
		c.Error != "nessuno" || !c.At.Equal(f.at) || len(c.Recipients) != 2 || c.Recipients[1].Kind != "esterno" {
		t.Fatalf("certification: %+v", c)
	}
	if m.RefMessageID != "<orig-42@legacyapp.test>" || c.OriginalMessageID != "<orig-42@legacyapp.test>" {
		t.Fatalf("ref: %q / %q", m.RefMessageID, c.OriginalMessageID)
	}
	if !bytes.Contains(m.Original, []byte("Body of the original.")) {
		t.Fatalf("original: %q", m.Original)
	}

	// Without anchors trust is unknown, never true.
	m, _ = Parse(raw, Options{})
	if !m.Signature.Valid || m.Signature.Trusted != nil {
		t.Fatalf("no anchors: %+v", m.Signature)
	}
	// Other anchors: valid but not trusted.
	other, _ := NewCA("Other CA")
	pool := x509.NewCertPool()
	pool.AddCert(other.Cert)
	m, _ = Parse(raw, Options{Anchors: pool})
	if !m.Signature.Valid || m.Signature.Trusted == nil || *m.Signature.Trusted {
		t.Fatalf("foreign anchors: %+v", m.Signature)
	}
}

func TestParseDetectsTampering(t *testing.T) {
	f := newFixture(t)
	raw := f.delivery(t, nil)
	// Change the delivered-to address inside the base64 daticert: re-encode a modified daticert.
	tampered := bytes.Replace(raw, []byte(base64.StdEncoding.EncodeToString([]byte("x"))[:0]+"Body of the original."), []byte("Body of the forged one."), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("fixture did not change")
	}
	m, err := Parse(tampered, Options{Anchors: f.anchors})
	if err != nil {
		t.Fatal(err)
	}
	if m.Signature.Valid || m.Signature.Error == "" {
		t.Fatalf("tampered message verified: %+v", m.Signature)
	}
	if m.Cert == nil || m.Type != ReceiptDelivery {
		t.Fatal("a damaged receipt is still parsed and reported")
	}
}

func TestParseLineEndingsRewritten(t *testing.T) {
	f := newFixture(t)
	lf := bytes.ReplaceAll(f.delivery(t, nil), []byte("\r\n"), []byte("\n"))
	m, err := Parse(lf, Options{Anchors: f.anchors})
	if err != nil || !m.Signature.Valid || m.Cert == nil {
		t.Fatalf("LF copy: %+v %v", m, err)
	}
}

func TestParseExpiredSigner(t *testing.T) {
	f := newFixture(t)
	old, err := f.ca.Issue("Gestore Scaduto", "pc@pec.sim.test", time.Now().AddDate(-3, 0, 0), time.Now().AddDate(-2, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Build{Kind: KindReceipt, Type: ReceiptAcceptance, From: "pc@pec.sim.test", To: "s@pec.sim.test",
		Sender: "s@pec.sim.test", Subject: "x", TransportID: "t1", At: f.at, OriginalID: "<o@x>"}.Generate(old)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := Parse(raw, Options{Anchors: f.anchors})
	if m.Signature.Valid || m.Signature.Trusted == nil || *m.Signature.Trusted {
		t.Fatalf("expired signer accepted: %+v", m.Signature)
	}
}

func TestParseKindsAndUnsigned(t *testing.T) {
	f := newFixture(t)
	env, err := Build{Kind: KindEnvelope, Type: "posta-certificata", From: "posta-certificata@pec.sim.test", To: "rcpt@pec.sim.test",
		Sender: "sender@pec.sim.test", Subject: "Hello", TransportID: "t2", At: f.at, OriginalID: "<o2@x>", Original: []byte(original)}.Generate(f.signer)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := Parse(env, Options{Anchors: f.anchors})
	if m.Kind != KindEnvelope || m.Cert == nil || m.Cert.Type != "posta-certificata" || len(m.Original) == 0 || !m.Signature.Valid {
		t.Fatalf("envelope: %+v", m)
	}
	anom, _ := Build{Kind: KindAnomaly, Type: "errore", From: "posta-certificata@pec.sim.test", To: "rcpt@pec.sim.test",
		Sender: "someone@example.com", Subject: "Not PEC", TransportID: "t3", At: f.at}.Generate(f.signer)
	if m, _ := Parse(anom, Options{}); m.Kind != KindAnomaly {
		t.Fatalf("anomaly: %+v", m)
	}
	unsigned, _ := Build{Kind: KindReceipt, Type: ReceiptAcceptance, From: "a@pec.sim.test", To: "s@pec.sim.test",
		Sender: "s@pec.sim.test", Subject: "x", TransportID: "t4", At: f.at}.Generate(nil)
	m, _ = Parse(unsigned, Options{})
	if m.Signature.Valid || m.ParseError == "" || m.Cert == nil {
		t.Fatalf("unsigned: %+v", m)
	}
	if _, err := Parse([]byte("From: a@b\r\nSubject: hi\r\n\r\nplain"), Options{}); err == nil {
		t.Fatal("ordinary mail is not a PEC system message")
	}
	if _, err := (Build{}).Generate(f.signer); err == nil {
		t.Fatal("empty build")
	}
}

func TestParseOpaqueSignature(t *testing.T) {
	f := newFixture(t)
	inner := "Content-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n--b1\r\nContent-Type: application/xml; name=\"daticert.xml\"\r\n\r\n" +
		"<postacert tipo=\"accettazione\" errore=\"nessuno\"><dati><identificativo>op-1</identificativo><msgid>&lt;o@x&gt;</msgid>" +
		"<data zona=\"+0200\"><giorno>26/10/2026</giorno><ora>12:15:30</ora></data></dati></postacert>\r\n--b1--\r\n"
	sd, err := pkcs7.NewSignedData([]byte(inner))
	if err != nil {
		t.Fatal(err)
	}
	if err := sd.AddSignerChain(f.signer.Cert, f.signer.Key, f.signer.Chain, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	raw := "From: a@pec.sim.test\r\nTo: s@pec.sim.test\r\nX-Ricevuta: accettazione\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		wrap64(der) + "\r\n"
	m, err := Parse([]byte(raw), Options{Anchors: f.anchors})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Signature.Valid || m.Cert == nil || m.Cert.TransportID != "op-1" || m.RefMessageID != "<o@x>" || !m.Cert.At.Equal(f.at) {
		t.Fatalf("opaque: %+v %+v", m, m.Cert)
	}
}

func TestParseDaticertEdgeCases(t *testing.T) {
	ns := `<?xml version="1.0" encoding="ISO-8859-1"?><p:postacert xmlns:p="urn:x" tipo=" Avvenuta-Consegna " errore="no-dest">` +
		`<p:dati><p:data><p:giorno>01/02/2026</p:giorno><p:ora>08:00:00</p:ora></p:data><p:errore-esteso>5.1.1 ` + "caf\xe9" + `</p:errore-esteso></p:dati></p:postacert>`
	c, err := ParseDaticert([]byte(ns))
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "avvenuta-consegna" || c.Error != "no-dest" || c.ErrorDetail != "5.1.1 café" {
		t.Fatalf("namespaced: %+v", c)
	}
	if want := time.Date(2026, 2, 1, 7, 0, 0, 0, time.UTC); !c.At.Equal(want) { // no zone → Europe/Rome (CET)
		t.Fatalf("zone default: %v", c.At)
	}
	for name, doc := range map[string]string{
		"entity":   `<!DOCTYPE p [<!ENTITY x SYSTEM "file:///etc/passwd">]><postacert tipo="a">&x;</postacert>`,
		"no type":  `<postacert errore="nessuno"></postacert>`,
		"oversize": "<postacert tipo=\"a\">" + strings.Repeat("x", MaxDaticert) + "</postacert>",
		"charset":  `<?xml version="1.0" encoding="EBCDIC"?><postacert tipo="a"/>`,
		"broken":   `<postacert tipo="a"><dati>`,
	} {
		if _, err := ParseDaticert([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if parseDate("", "", "") != (time.Time{}) || !parseDate("26/10/2026", "", "+0000").Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("parseDate edge cases")
	}
	if capText("àèì", 3) != "à" || capText("ab", 5) != "ab" {
		t.Fatal("capText")
	}
}

func TestIsCandidate(t *testing.T) {
	h := map[string]string{"x-ricevuta": "accettazione"}
	if !IsCandidate(func(n string) string { return h[n] }) {
		t.Fatal("receipt")
	}
	if IsCandidate(func(string) string { return "" }) {
		t.Fatal("ordinary")
	}
}
