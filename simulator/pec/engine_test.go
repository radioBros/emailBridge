package pec

import (
	"bytes"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	intpec "github.com/radiobros/emailbridge/internal/pec"
)

type memStore struct {
	mu   sync.Mutex
	mail map[string][][]byte // user → messages
}

func (m *memStore) inject(user, _ string, raw []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mail == nil {
		m.mail = map[string][][]byte{}
	}
	m.mail[user] = append(m.mail[user], append([]byte(nil), raw...))
	return nil
}

func (m *memStore) of(user string) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]byte(nil), m.mail[user]...)
}

func testCfg(sleep instantSleep) *Config {
	return &Config{
		Domains: []Domain{{
			Name: "pec.sim.test", Provider: "Gestore Simulato S.p.A.", // IT
			SystemAddress: "posta-certificata@pec.sim.test",
		}},
		SlowDelay: 5 * time.Millisecond,
		Now:       func() time.Time { return time.Date(2026, 10, 26, 10, 15, 30, 0, time.UTC) },
		Sleep:     sleep,
	}
}

type instantSleep func(d time.Duration)

func newEng(t *testing.T, store *memStore, sleep instantSleep) *Engine {
	t.Helper()
	if sleep == nil {
		sleep = func(time.Duration) {}
	}
	cfg := testCfg(sleep)
	e, err := New(cfg, store.inject)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return e
}

func waitN(t *testing.T, store *memStore, user string, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := store.of(user)
		if len(msgs) >= n {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d msgs in %s (have %d)", n, user, len(store.of(user)))
	return nil
}

func parseAll(t *testing.T, raws [][]byte, anchors *x509.CertPool) []*intpec.Message {
	t.Helper()
	out := make([]*intpec.Message, 0, len(raws))
	for _, r := range raws {
		m, err := intpec.Parse(r, intpec.Options{Anchors: anchors})
		if err != nil {
			t.Fatalf("parse: %v\n%s", err, r[:min(200, len(r))])
		}
		out = append(out, m)
	}
	return out
}

func findType(msgs []*intpec.Message, typ string) *intpec.Message {
	for _, m := range msgs {
		if m.Type == typ {
			return m
		}
	}
	return nil
}

const origBody = "From: sender@pec.sim.test\r\nTo: rcpt@pec.sim.test\r\nSubject: Verbale 42\r\n" +
	"Message-ID: <orig-42@legacyapp.test>\r\n\r\nBody.\r\n"

func TestHappyPathAcceptanceAndDelivery(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"rcpt@pec.sim.test"}, []byte(origBody))
	senderMsgs := waitN(t, store, "sender@pec.sim.test", 2) // accettazione + consegna
	rcptMsgs := waitN(t, store, "rcpt@pec.sim.test", 1)     // envelope
	anchors := e.Anchors()
	sm := parseAll(t, senderMsgs, anchors)
	acc := findType(sm, intpec.ReceiptAcceptance)
	del := findType(sm, intpec.ReceiptDelivery)
	if acc == nil || del == nil {
		t.Fatalf("receipts: %+v %+v", typesOf(sm), sm)
	}
	if acc.RefMessageID != "<orig-42@legacyapp.test>" || !acc.Signature.Valid ||
		acc.Signature.Trusted == nil || !*acc.Signature.Trusted {
		t.Fatalf("acc: %+v sig=%+v", acc, acc.Signature)
	}
	if del.ReceiptKind != "completa" || del.Cert == nil || del.Cert.DeliveredTo != "rcpt@pec.sim.test" ||
		!del.Signature.Valid || !*del.Signature.Trusted {
		t.Fatalf("del: %+v", del)
	}
	em := parseAll(t, rcptMsgs, anchors)
	if em[0].Kind != intpec.KindEnvelope || !em[0].Signature.Valid || !*em[0].Signature.Trusted {
		t.Fatalf("env: %+v", em[0])
	}
	recs := e.ListReceipts("sender@pec.sim.test")
	if len(recs) < 2 {
		t.Fatalf("list: %+v", recs)
	}
}

func TestEsternoNoDelivery(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"someone@example.com"}, []byte(origBody))
	msgs := waitN(t, store, "sender@pec.sim.test", 1)
	sm := parseAll(t, msgs, e.Anchors())
	if findType(sm, intpec.ReceiptAcceptance) == nil || findType(sm, intpec.ReceiptDelivery) != nil {
		t.Fatalf("want only acceptance: %v", typesOf(sm))
	}
	if len(store.of("someone@example.com")) != 0 {
		t.Fatal("no envelope for esterno")
	}
}

func TestFaults(t *testing.T) {
	cases := []struct {
		local string
		want  string
		err   string
		valid *bool // nil = don't care; false = must be invalid
	}{
		{"nouser", intpec.ReceiptDeliveryError, "no-dest", boolPtr(true)},
		{"nodomain", intpec.ReceiptDeliveryError, "no-dominio", boolPtr(true)},
		{"virus", intpec.ReceiptNonAcceptance, "virus", boolPtr(true)},
		{"badsig", intpec.ReceiptDelivery, "", boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.local, func(t *testing.T) {
			store := &memStore{}
			e := newEng(t, store, nil)
			rcpt := tc.local + "@pec.sim.test"
			e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test", []string{rcpt}, []byte(origBody))
			n := 2
			if tc.local == "virus" {
				n = 1
			}
			if tc.local == "badsig" {
				n = 2 // acc + bad delivery
			}
			msgs := waitN(t, store, "sender@pec.sim.test", n)
			sm := parseAll(t, msgs, e.Anchors())
			m := findType(sm, tc.want)
			if m == nil {
				t.Fatalf("missing %s in %v", tc.want, typesOf(sm))
			}
			if tc.err != "" && (m.Cert == nil || m.Cert.Error != tc.err) {
				t.Fatalf("error: %+v", m.Cert)
			}
			if tc.valid != nil && m.Signature.Valid != *tc.valid {
				t.Fatalf("valid=%v want %v (%+v)", m.Signature.Valid, *tc.valid, m.Signature)
			}
			if tc.local == "badsig" && m.Signature.Trusted != nil && *m.Signature.Trusted {
				t.Fatal("badsig must not be trusted")
			}
		})
	}
}

func TestDupe(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"dupe@pec.sim.test"}, []byte(origBody))
	msgs := waitN(t, store, "sender@pec.sim.test", 3) // acc + 2× delivery
	sm := parseAll(t, msgs, e.Anchors())
	var dels []*intpec.Message
	for _, m := range sm {
		if m.Type == intpec.ReceiptDelivery {
			dels = append(dels, m)
		}
	}
	if len(dels) != 2 || dels[0].Cert.TransportID != dels[1].Cert.TransportID {
		t.Fatalf("dupe: %d ids %q %q", len(dels),
			safeID(dels, 0), safeID(dels, 1))
	}
}

func TestSlow(t *testing.T) {
	store := &memStore{}
	var slept []time.Duration
	var mu sync.Mutex
	e := newEng(t, store, func(d time.Duration) {
		mu.Lock()
		slept = append(slept, d)
		mu.Unlock()
	})
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"slow@pec.sim.test"}, []byte(origBody))
	msgs := waitN(t, store, "sender@pec.sim.test", 3) // acc + preavviso + delivery
	sm := parseAll(t, msgs, e.Anchors())
	if findType(sm, intpec.ReceiptDeliveryNotice) == nil || findType(sm, intpec.ReceiptDelivery) == nil {
		t.Fatalf("slow: %v", typesOf(sm))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(slept) == 0 || slept[0] < 5*time.Millisecond {
		t.Fatalf("sleep: %v", slept)
	}
}

func TestBreveReceiptKind(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	body := []byte("From: sender@pec.sim.test\r\nTo: rcpt@pec.sim.test\r\nSubject: x\r\n" +
		"Message-ID: <b1@t>\r\nX-TipoRicevuta: breve\r\n\r\nhi\r\n")
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"rcpt@pec.sim.test"}, body)
	msgs := waitN(t, store, "sender@pec.sim.test", 2)
	sm := parseAll(t, msgs, e.Anchors())
	del := findType(sm, intpec.ReceiptDelivery)
	if del == nil || del.ReceiptKind != "breve" || len(del.Original) != 0 {
		t.Fatalf("breve: %+v orig=%d", del, len(del.Original))
	}
}

func TestAnomalyOrdinaryToPEC(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	body := []byte("From: a@example.com\r\nTo: rcpt@pec.sim.test\r\nSubject: hi\r\nMessage-ID: <o@x>\r\n\r\nx\r\n")
	e.OnOrdinaryToPEC("a@example.com", []string{"rcpt@pec.sim.test"}, body)
	msgs := waitN(t, store, "rcpt@pec.sim.test", 1)
	m := parseAll(t, msgs, e.Anchors())[0]
	if m.Kind != intpec.KindAnomaly || !m.Signature.Valid {
		t.Fatalf("anomaly: %+v", m)
	}
}

func TestForceAndDelayAndCA(t *testing.T) {
	store := &memStore{}
	var slept []time.Duration
	e := newEng(t, store, func(d time.Duration) { slept = append(slept, d) })
	e.SetDeliveryDelay("sender@pec.sim.test", 50*time.Millisecond)
	e.ForceNextReceipt("sender@pec.sim.test", intpec.ReceiptDeliveryError)
	e.OnPECSend("sender@pec.sim.test", "sender@pec.sim.test",
		[]string{"rcpt@pec.sim.test"}, []byte(origBody))
	msgs := waitN(t, store, "sender@pec.sim.test", 2)
	sm := parseAll(t, msgs, e.Anchors())
	if findType(sm, intpec.ReceiptDeliveryError) == nil {
		t.Fatalf("forced: %v", typesOf(sm))
	}
	if len(slept) == 0 || slept[0] != 50*time.Millisecond {
		t.Fatalf("delay sleep: %v", slept)
	}
	e.ClearForceReceipt("sender@pec.sim.test")
	e.SetDeliveryDelay("sender@pec.sim.test", 0)

	pem := e.CAPEM()
	if !bytes.Contains(pem, []byte("BEGIN CERTIFICATE")) {
		t.Fatalf("pem: %s", pem)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	if err := e.WriteCAPEM(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, pem) {
		t.Fatal("file mismatch")
	}
	e.Reset()
	if len(e.ListReceipts("")) != 0 {
		t.Fatal("reset cleared receipts")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil, func(string, string, []byte) error { return nil }); err == nil {
		t.Fatal("nil cfg")
	}
	if _, err := New(&Config{Domains: []Domain{{Name: "x"}}}, nil); err == nil {
		t.Fatal("nil inject")
	}
	if _, err := New(&Config{Domains: []Domain{{}}}, func(string, string, []byte) error { return nil }); err == nil {
		t.Fatal("empty name")
	}
}

func TestHelpers(t *testing.T) {
	if localOf("Alice <A@Pec.Sim.Test>") != "a" || domainOf("Alice <A@Pec.Sim.Test>") != "pec.sim.test" {
		t.Fatal(splitAddr("Alice <A@Pec.Sim.Test>"))
	}
	if normalizeUser("Bob <B@X.Y>") != "b@x.y" {
		t.Fatal(normalizeUser("Bob <B@X.Y>"))
	}
	raw := []byte("Subject: Hello\r\nMessage-ID: <m@x>\r\n\r\nbody")
	if headerGet(raw, "Subject") != "Hello" || headerGet(raw, "Message-ID") != "<m@x>" {
		t.Fatal(headerGet(raw, "Subject"), headerGet(raw, "Message-ID"))
	}
	broken := breakSignature([]byte("Messaggio di sistema PEC here"))
	if bytes.Contains(broken, []byte("Messaggio di sistema PEC")) {
		t.Fatal("breakSignature")
	}
	(*Engine)(nil).Stop()
	(&Engine{}).Stop()
}

func typesOf(msgs []*intpec.Message) []string {
	var s []string
	for _, m := range msgs {
		s = append(s, m.Type)
	}
	return s
}

func boolPtr(v bool) *bool { return &v }

func safeID(msgs []*intpec.Message, i int) string {
	if i >= len(msgs) || msgs[i].Cert == nil {
		return ""
	}
	return msgs[i].Cert.TransportID
}

func TestIsPECAndDomainOf(t *testing.T) {
	store := &memStore{}
	e := newEng(t, store, nil)
	if !e.IsPECAddress("x@pec.sim.test") || e.IsPECAddress("x@example.com") {
		t.Fatal("IsPECAddress")
	}
	d := e.DomainOf("x@pec.sim.test")
	if d == nil || !strings.Contains(d.Provider, "Gestore") {
		t.Fatalf("%+v", d)
	}
	if (*Engine)(nil).IsPECAddress("x@pec.sim.test") {
		t.Fatal("nil engine")
	}
	if !strings.Contains(string(e.CAPEM()), "CERTIFICATE") {
		t.Fatal("capem")
	}
}
