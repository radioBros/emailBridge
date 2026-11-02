package pec

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	intpec "github.com/radiobros/emailbridge/internal/pec"
)

// InjectFunc appends raw RFC 822 into a user's mailbox (typically "INBOX").
type InjectFunc func(user, mailbox string, raw []byte) error

// ReceiptInfo is one generated PEC system message recorded for the control API.
type ReceiptInfo struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	RefMessageID string    `json:"refMessageId"`
	TransportID  string    `json:"identificativo"` // PEC daticert field (S17)
	At           time.Time `json:"at"`
	User         string    `json:"user"`
}

type domainRT struct {
	Dom    Domain
	Signer *intpec.Signer
}

// Engine runs PEC receipt / envelope generation for the simulator.
type Engine struct {
	cfg    *Config
	ca     *intpec.CA
	doms   map[string]*domainRT // lowercased domain → runtime
	inject InjectFunc

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	seq     atomic.Int64
	recSeq  atomic.Int64
	recs    []ReceiptInfo
	delays  map[string]time.Duration // sender mailbox → extra delay before delivery
	force   map[string]string        // sender mailbox → next forced receipt type
	sleepFn func(d time.Duration)
}

// New builds a PEC engine. inject must be non-nil. Domains must be non-empty.
func New(cfg *Config, inject InjectFunc) (*Engine, error) {
	if cfg == nil || len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("pec: config needs at least one domain")
	}
	if inject == nil {
		return nil, fmt.Errorf("pec: inject required")
	}
	ca, err := intpec.NewCA("EmailBridge PEC simulator CA")
	if err != nil {
		return nil, err
	}
	doms := make(map[string]*domainRT, len(cfg.Domains))
	for _, d := range cfg.Domains {
		name := strings.ToLower(strings.TrimSpace(d.Name))
		if name == "" {
			return nil, fmt.Errorf("pec: empty domain name")
		}
		if d.Provider == "" {
			d.Provider = "Gestore Simulato S.p.A." // IT provider name (S17)
		}
		if d.SystemAddress == "" {
			d.SystemAddress = "posta-certificata@" + name
		}
		d.Name = name
		signer, err := ca.Issue(d.Provider, d.SystemAddress, time.Time{}, time.Time{})
		if err != nil {
			return nil, err
		}
		doms[name] = &domainRT{Dom: d, Signer: signer}
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		cfg: cfg, ca: ca, doms: doms, inject: inject,
		ctx: ctx, cancel: cancel,
		delays: make(map[string]time.Duration), force: make(map[string]string),
	}
	e.sleepFn = cfg.Sleep
	if e.sleepFn == nil {
		e.sleepFn = func(d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
			case <-e.ctx.Done():
			}
		}
	}
	return e, nil
}

// Stop cancels in-flight PEC work and waits for goroutines.
func (e *Engine) Stop() {
	if e == nil || e.cancel == nil {
		return
	}
	e.cancel()
	e.wg.Wait()
}

// Reset clears receipt log and per-mailbox controls (CA/signers kept).
func (e *Engine) Reset() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recs = nil
	e.delays = make(map[string]time.Duration)
	e.force = make(map[string]string)
}

// CAPEM returns the test CA certificate as PEM.
func (e *Engine) CAPEM() []byte {
	if e == nil || e.ca == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: e.ca.Cert.Raw})
}

// WriteCAPEM writes the CA PEM to path (for EB_PEC_TRUST_ANCHORS).
func (e *Engine) WriteCAPEM(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, e.CAPEM(), 0o600)
}

// Anchors returns a cert pool with the simulator CA (tests / Parse).
func (e *Engine) Anchors() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(e.ca.Cert)
	return p
}

// IsPECAddress reports whether addr's domain is a configured PEC domain.
func (e *Engine) IsPECAddress(addr string) bool {
	if e == nil {
		return false
	}
	_, dom := splitAddr(addr)
	_, ok := e.doms[dom]
	return ok
}

// DomainOf returns the domain config for an address, or nil.
func (e *Engine) DomainOf(addr string) *Domain {
	if e == nil {
		return nil
	}
	_, dom := splitAddr(addr)
	rt, ok := e.doms[dom]
	if !ok {
		return nil
	}
	return &rt.Dom
}

// SetDeliveryDelay sets an extra wait before delivery receipts for sends from user.
func (e *Engine) SetDeliveryDelay(user string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	user = strings.ToLower(user)
	if d <= 0 {
		delete(e.delays, user)
		return
	}
	e.delays[user] = d
}

// ForceNextReceipt forces the next delivery-side receipt type for sends from user.
func (e *Engine) ForceNextReceipt(user, typ string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	user = strings.ToLower(user)
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		delete(e.force, user)
		return
	}
	e.force[user] = typ
}

// ClearForceReceipt clears a forced receipt type.
func (e *Engine) ClearForceReceipt(user string) { e.ForceNextReceipt(user, "") }

// ListReceipts returns generated receipts, optionally filtered by user (case-insensitive).
func (e *Engine) ListReceipts(user string) []ReceiptInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	user = strings.ToLower(strings.TrimSpace(user))
	out := make([]ReceiptInfo, 0, len(e.recs))
	for _, r := range e.recs {
		if user == "" || strings.ToLower(r.User) == user {
			out = append(out, r)
		}
	}
	return out
}

// OnPECSend schedules PEC processing for a message submitted by a PEC mailbox.
func (e *Engine) OnPECSend(sender, from string, to []string, body []byte) {
	if e == nil {
		return
	}
	body = append([]byte(nil), body...)
	to = append([]string(nil), to...)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.processSend(sender, from, to, body)
	}()
}

// OnOrdinaryToPEC injects anomaly envelopes for PEC recipients of ordinary mail.
func (e *Engine) OnOrdinaryToPEC(from string, to []string, body []byte) {
	if e == nil {
		return
	}
	body = append([]byte(nil), body...)
	for _, rcpt := range to {
		if !e.IsPECAddress(rcpt) {
			continue
		}
		rt := e.doms[domainOf(rcpt)]
		if rt == nil {
			continue
		}
		user := normalizeUser(rcpt)
		subj := headerGet(body, "Subject")
		tid := e.nextID(rt.Dom.Name)
		at := e.cfg.now()
		raw, err := intpec.Build{
			Kind: intpec.KindAnomaly, Type: "errore",
			From: rt.Dom.SystemAddress, To: user, Sender: from,
			Recipients: []intpec.Recipient{{Address: rcpt, Kind: "esterno"}},
			Subject:    subj, Provider: rt.Dom.Provider, At: at,
			TransportID: tid, MessageID: "<anom-" + tid + ">",
			Original: body,
		}.Generate(rt.Signer)
		if err != nil {
			continue
		}
		_ = e.inject(user, "INBOX", raw)
		e.record(user, "errore", headerGet(body, "Message-ID"), tid, at)
	}
}

func (e *Engine) processSend(sender, from string, to []string, body []byte) {
	select {
	case <-e.ctx.Done():
		return
	default:
	}
	sender = normalizeUser(sender)
	if from == "" {
		from = sender
	}
	srt := e.doms[domainOf(sender)]
	if srt == nil {
		return
	}
	origID := headerGet(body, "Message-ID")
	if origID == "" {
		origID = fmt.Sprintf("<sim-%d@%s>", e.seq.Add(1), srt.Dom.Name)
	}
	subj := headerGet(body, "Subject")
	receiptKind := strings.ToLower(headerGet(body, "X-TipoRicevuta"))
	if receiptKind != "breve" && receiptKind != "sintetica" {
		receiptKind = "completa"
	}
	recipients := e.buildRecipients(to)
	at := e.cfg.now()

	// Virus on any recipient → non-accettazione only.
	for _, rcpt := range to {
		if localOf(rcpt) == "virus" {
			tid := e.nextID(srt.Dom.Name)
			e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
				intpec.ReceiptNonAcceptance, "", "", "virus", "virus detected by simulator", nil, false, false)
			return
		}
	}

	tid := e.nextID(srt.Dom.Name)
	e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
		intpec.ReceiptAcceptance, "", "", "", "", nil, false, false)

	extra := e.deliveryDelay(sender)
	if extra > 0 {
		e.sleep(extra)
		if e.stopped() {
			return
		}
	}

	for _, rcpt := range to {
		if e.stopped() {
			return
		}
		e.processRecipient(srt, sender, from, rcpt, recipients, subj, origID, receiptKind, body)
	}
}

func (e *Engine) processRecipient(srt *domainRT, sender, from, rcpt string, recipients []intpec.Recipient,
	subj, origID, receiptKind string, body []byte) {
	local := localOf(rcpt)
	force := e.takeForce(sender)
	at := e.cfg.now()

	if force != "" {
		e.emitForced(srt, sender, from, rcpt, recipients, subj, origID, receiptKind, force, at, body)
		return
	}

	switch local {
	case "nouser":
		tid := e.nextID(srt.Dom.Name)
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptDeliveryError, "", rcpt, "no-dest", "5.1.1 - recipient unknown", nil, false, false)
		return
	case "nodomain":
		tid := e.nextID(srt.Dom.Name)
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptDeliveryError, "", rcpt, "no-dominio", "5.1.2 - domain does not exist", nil, false, false)
		return
	case "slow":
		tid := e.nextID(srt.Dom.Name)
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptDeliveryNotice, "", rcpt, "", "", nil, false, false)
		e.sleep(e.cfg.slowDelay())
		if e.stopped() {
			return
		}
		at = e.cfg.now()
		e.deliverOK(srt, sender, from, rcpt, recipients, subj, origID, receiptKind, body, at, false, false)
		return
	}

	if !e.IsPECAddress(rcpt) {
		return // esterno: no envelope, no delivery receipt
	}
	badsig := local == "badsig"
	dupe := local == "dupe"
	e.deliverOK(srt, sender, from, rcpt, recipients, subj, origID, receiptKind, body, at, badsig, dupe)
}

func (e *Engine) deliverOK(srt *domainRT, sender, from, rcpt string, recipients []intpec.Recipient,
	subj, origID, receiptKind string, body []byte, at time.Time, badsig, dupe bool) {
	rrt := e.doms[domainOf(rcpt)]
	if rrt == nil {
		rrt = srt
	}
	user := normalizeUser(rcpt)
	tid := e.nextID(rrt.Dom.Name)

	// Transport envelope into recipient INBOX when the user exists.
	envRaw, err := intpec.Build{
		Kind: intpec.KindEnvelope, Type: "posta-certificata",
		From: rrt.Dom.SystemAddress, To: user, Sender: from,
		Recipients: recipients, Subject: subj, Provider: rrt.Dom.Provider, At: at,
		TransportID: tid, MessageID: "<env-" + tid + ">", OriginalID: origID, Original: body,
	}.Generate(rrt.Signer)
	if err == nil {
		if err := e.inject(user, "INBOX", envRaw); err == nil {
			e.record(user, "posta-certificata", origID, tid, at)
		}
	}

	var origAttach []byte
	if receiptKind == "completa" {
		origAttach = body
	}
	e.injectReceipt(rrt, sender, from, recipients, subj, origID, tid, at,
		intpec.ReceiptDelivery, receiptKind, rcpt, "", "", origAttach, badsig, dupe)
}

func (e *Engine) emitForced(srt *domainRT, sender, from, rcpt string, recipients []intpec.Recipient,
	subj, origID, receiptKind, force string, at time.Time, body []byte) {
	tid := e.nextID(srt.Dom.Name)
	switch force {
	case intpec.ReceiptDeliveryError:
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptDeliveryError, "", rcpt, "no-dest", "5.1.1 - forced", nil, false, false)
	case intpec.ReceiptDeliveryNotice:
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptDeliveryNotice, "", rcpt, "", "", nil, false, false)
	case intpec.ReceiptNonAcceptance:
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptNonAcceptance, "", "", "altro", "forced", nil, false, false)
	case intpec.ReceiptVirus:
		e.injectReceipt(srt, sender, from, recipients, subj, origID, tid, at,
			intpec.ReceiptVirus, "", "", "virus", "forced", nil, false, false)
	default: // avvenuta-consegna or unknown → normal delivery
		e.deliverOK(srt, sender, from, rcpt, recipients, subj, origID, receiptKind, body, at, false, false)
	}
}

func (e *Engine) injectReceipt(rt *domainRT, toUser, from string, recipients []intpec.Recipient,
	subj, origID, tid string, at time.Time, typ, receiptKind, deliveredTo, errCode, errDetail string,
	original []byte, badsig, dupe bool) {
	raw, err := intpec.Build{
		Kind: intpec.KindReceipt, Type: typ, ReceiptKind: receiptKind,
		From: rt.Dom.SystemAddress, To: toUser, Sender: from,
		Recipients: recipients, Subject: subj, Provider: rt.Dom.Provider, At: at,
		TransportID: tid, MessageID: "<rcp-" + tid + ">", OriginalID: origID,
		DeliveredTo: deliveredTo, Error: errCode, ErrorDetail: errDetail, Original: original,
	}.Generate(rt.Signer)
	if err != nil {
		return
	}
	if badsig {
		raw = breakSignature(raw)
	}
	_ = e.inject(toUser, "INBOX", raw)
	e.record(toUser, typ, origID, tid, at)
	if dupe {
		_ = e.inject(toUser, "INBOX", raw)
		e.record(toUser, typ, origID, tid, at)
	}
}

func (e *Engine) buildRecipients(to []string) []intpec.Recipient {
	out := make([]intpec.Recipient, 0, len(to))
	for _, a := range to {
		kind := "esterno"
		if e.IsPECAddress(a) {
			kind = "certificato" // PEC daticert recipient kind (S17)
		}
		out = append(out, intpec.Recipient{Address: a, Kind: kind})
	}
	return out
}

func (e *Engine) nextID(domain string) string {
	n := e.seq.Add(1)
	at := e.cfg.now().UTC()
	return fmt.Sprintf("opec.%s.%d.sim@%s", at.Format("20060102150405"), n, domain)
}

func (e *Engine) record(user, typ, ref, tid string, at time.Time) {
	id := fmt.Sprintf("simrcp_%d", e.recSeq.Add(1))
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recs = append(e.recs, ReceiptInfo{
		ID: id, Type: typ, RefMessageID: ref, TransportID: tid, At: at, User: user,
	})
}

func (e *Engine) deliveryDelay(user string) time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delays[strings.ToLower(user)]
}

func (e *Engine) takeForce(user string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	user = strings.ToLower(user)
	t := e.force[user]
	delete(e.force, user)
	return t
}

func (e *Engine) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	e.sleepFn(d)
}

func (e *Engine) stopped() bool {
	select {
	case <-e.ctx.Done():
		return true
	default:
		return false
	}
}

func breakSignature(raw []byte) []byte {
	// Alter signed content after CMS so verification fails.
	out := bytes.Replace(raw, []byte("Messaggio di sistema PEC"), []byte("Messaggio di sistema XXX"), 1)
	if bytes.Equal(out, raw) {
		// Fallback: flip a byte in the body.
		out = append([]byte(nil), raw...)
		if i := bytes.Index(out, []byte("daticert")); i >= 0 && i+1 < len(out) {
			out[i] ^= 0x01
		}
	}
	return out
}

func splitAddr(addr string) (local, domain string) {
	addr = strings.TrimSpace(addr)
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	addr = strings.ToLower(addr)
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return addr, ""
	}
	return addr[:i], addr[i+1:]
}

func localOf(addr string) string  { l, _ := splitAddr(addr); return l }
func domainOf(addr string) string { _, d := splitAddr(addr); return d }

func normalizeUser(addr string) string {
	addr = strings.TrimSpace(addr)
	if a, err := mail.ParseAddress(addr); err == nil {
		return strings.ToLower(a.Address)
	}
	return strings.ToLower(addr)
}

func headerGet(raw []byte, name string) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(msg.Header.Get(name))
}
