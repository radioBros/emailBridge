package pec

import (
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func FuzzParse(f *testing.F) {
	fx := struct{ raw []byte }{}
	ca, _ := NewCA("fuzz")
	s, _ := ca.Issue("p", "p@pec.sim.test", time.Time{}, time.Time{})
	fx.raw, _ = Build{Kind: KindReceipt, Type: ReceiptAcceptance, From: "p@pec.sim.test", To: "s@pec.sim.test",
		Sender: "s@pec.sim.test", Subject: "x", TransportID: "t", OriginalID: "<o@x>"}.Generate(s)
	f.Add(fx.raw)
	f.Add([]byte("X-Ricevuta: accettazione\r\nContent-Type: multipart/signed; boundary=x\r\n\r\n--x\r\n\r\n--x--"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = Parse(raw, Options{})
	})
}

func FuzzParseDaticert(f *testing.F) {
	f.Add([]byte(`<postacert tipo="accettazione"><dati><data zona="+0100"><giorno>1/2/2026</giorno><ora>1:02:03</ora></data></dati></postacert>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = ParseDaticert(b)
	})
}

func TestMIMEBranches(t *testing.T) {
	e, _ := parseEntity([]byte("Content-Transfer-Encoding: quoted-printable\r\n\r\ncaf=C3=A9=\r\n!"))
	if b, err := e.decoded(); err != nil || string(b) != "café!" {
		t.Fatalf("qp: %q %v", b, err)
	}
	e, _ = parseEntity([]byte("Content-Transfer-Encoding: base64\r\n\r\naGVsbG8")) // no padding
	if b, err := e.decoded(); err != nil || string(b) != "hello" {
		t.Fatalf("unpadded base64: %q %v", b, err)
	}
	e, _ = parseEntity([]byte("Content-Transfer-Encoding: base64\r\n\r\n!!!"))
	if _, err := e.decoded(); err == nil {
		t.Fatal("bad base64")
	}
	e, _ = parseEntity([]byte("Content-Type: ;;bad\r\n\r\nx"))
	if mt, _ := e.mediaType(); mt != "" {
		t.Fatalf("bad content type: %q", mt)
	}
	e, _ = parseEntity([]byte("Content-Type: application/xml; name=\"DatiCert.XML\"\r\n\r\nx"))
	if e.filename() != "daticert.xml" {
		t.Fatalf("name param: %q", e.filename())
	}
	if parts([]byte("x"), "") != nil || len(parts([]byte("--b\r\nA\r\n--b"), "b")) != 1 {
		t.Fatal("parts edge cases")
	}
	if got := string(toCRLF([]byte("a\nb\r\nc"))); got != "a\r\nb\r\nc" || string(toCRLF([]byte("x"))) != "x" {
		t.Fatalf("toCRLF: %q", got)
	}
	head, body := splitHeaderBody([]byte("A: b\nC: d\n\nbody"))
	if string(head) != "A: b\nC: d\n" || string(body) != "body" {
		t.Fatalf("LF split: %q %q", head, body)
	}
	if h, b := splitHeaderBody([]byte("only headers")); string(h) != "only headers" || b != nil {
		t.Fatal("no body")
	}
	g := HeaderGetter(textproto.MIMEHeader{"X-Ricevuta": {"accettazione"}})
	if g("x-ricevuta") != "accettazione" {
		t.Fatal("HeaderGetter")
	}
	for _, cs := range []string{"latin9", "windows-1252", "us-ascii"} {
		if _, err := charsetReader(cs, strings.NewReader("x")); err != nil {
			t.Fatalf("%s: %v", cs, err)
		}
	}
}

func TestSignedWithoutSignaturePart(t *testing.T) {
	raw := "X-Ricevuta: accettazione\r\nContent-Type: multipart/signed; boundary=\"s\"\r\n\r\n--s\r\nContent-Type: text/plain\r\n\r\nx\r\n--s--\r\n"
	m, err := Parse([]byte(raw), Options{})
	if err != nil || m.ParseError == "" || m.Signature.Valid {
		t.Fatalf("missing signature part: %+v %v", m, err)
	}
	opaque := "X-Ricevuta: accettazione\r\nContent-Type: application/pkcs7-mime\r\nContent-Transfer-Encoding: base64\r\n\r\nAAAA\r\n"
	m, err = Parse([]byte(opaque), Options{Now: time.Now})
	if err != nil || m.Signature.Valid || m.Signature.Error == "" {
		t.Fatalf("garbage opaque: %+v %v", m, err)
	}
}
