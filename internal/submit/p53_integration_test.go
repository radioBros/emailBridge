//go:build integration

package submit

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

type stubGateway struct {
	acc *SMTPAccount
	err error
}

func (s stubGateway) ResolveSMTP(context.Context, string, string) (*SMTPAccount, error) {
	if s.err != nil {
		return nil, s.err
	}
	cp := *s.acc
	return &cp, nil
}

func TestIntegrationDSNParamsSeenBySimulator(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{
		DSN: &DSNRequest{Notify: []string{"success", "failure"}, Ret: "hdrs", EnvID: "env-p53"},
	})
	h.work()
	if m := h.get(id); m.State != StateSent {
		t.Fatalf("state %s", m.State)
	}
	msgs, err := h.sim.Client.ListSMTPMessages(h.ctx)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("smtp msgs: %v %d", err, len(msgs))
	}
	dsn := msgs[len(msgs)-1].DSN
	if dsn == nil || dsn.EnvID != "env-p53" || dsn.Ret == "" || len(dsn.Notify) == 0 {
		t.Fatalf("dsn not observed: %+v", dsn)
	}
}

func TestIntegrationGatewayUsed(t *testing.T) {
	h := newHarness(t)
	gwyHost, gwyPort := h.acc.Host, h.acc.Port
	gwyID := "gwy_01HTESTGATEWAY000000000000"
	// Force account SMTP to a dead port so only the gateway can succeed.
	h.acc.Host, h.acc.Port = "127.0.0.1", 1
	h.accounts.put(h.acc)
	h.svc.Gateways = stubGateway{acc: &SMTPAccount{
		GatewayID: gwyID, Host: gwyHost, Port: gwyPort, TLS: string(smtpc.TLSNone),
		User: testUser, Password: testPass, EHLO: "via-gateway.test",
	}}
	id := h.submit(SubmitRequest{GatewayID: gwyID})
	h.work()
	m := h.get(id)
	if m.State != StateSent || m.GatewayID != gwyID {
		t.Fatalf("gateway send: %+v", m)
	}
}

func TestIntegrationRetryAndSendNow(t *testing.T) {
	h := newHarness(t)
	id := h.submit(SubmitRequest{})
	_, err := h.svc.Repo.coll().UpdateOne(h.ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{"state": StateFailed, "finalError": `{"code":"smtp_permanent","title":"x"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := h.svc.Retry(h.ctx, id, "")
	if err != nil || m.State != StateQueued {
		t.Fatalf("retry: %v %+v", err, m)
	}
	h.work()
	if h.get(id).State != StateSent {
		t.Fatalf("after retry: %s", h.get(id).State)
	}

	future := h.svc.now().Add(time.Hour)
	sid := h.submit(SubmitRequest{SendAt: &future})
	if h.get(sid).State != StateScheduled {
		t.Fatalf("scheduled: %s", h.get(sid).State)
	}
	m, err = h.svc.SendNow(h.ctx, sid, "")
	if err != nil || m.State != StateQueued {
		t.Fatalf("send-now: %v %+v", err, m)
	}
	h.work()
	if h.get(sid).State != StateSent {
		t.Fatalf("after send-now: %s", h.get(sid).State)
	}

	if _, err := h.svc.Retry(h.ctx, sid, ""); errs.CodeOf(err) != errs.CodeOutboundNotRetryable {
		t.Fatalf("sent not retryable: %v", err)
	}
	if _, err := h.svc.SendNow(h.ctx, sid, ""); errs.CodeOf(err) != errs.CodeOutboundNotSendable {
		t.Fatalf("sent not sendable: %v", err)
	}
}

func TestIntegrationProxySOCKS5AndEHLO(t *testing.T) {
	h := newHarness(t)
	proxyAddr := startSOCKS5(t)
	h.acc.Proxy = "socks5://" + proxyAddr
	h.acc.EHLO = "proxy-ehlo.test"
	h.accounts.put(h.acc)
	id := h.submit(SubmitRequest{})
	h.work()
	if m := h.get(id); m.State != StateSent {
		t.Fatalf("proxy send: %s err=%v", m.State, m.FinalError)
	}
}

// startSOCKS5 is a minimal no-auth SOCKS5 CONNECT proxy (RFC 1928) for tests.
func startSOCKS5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSOCKS5(c)
		}
	}()
	return ln.Addr().String()
}

func serveSOCKS5(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := make([]byte, 512)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return
	}
	_, _ = c.Write([]byte{0x05, 0x00})
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // domain
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(c, buf[:l]); err != nil {
			return
		}
		host = string(buf[:l])
	default:
		return
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(buf[:2]))
	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, itoa(port)), 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = target.Close() }()
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, c); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(c, target); errc <- struct{}{} }()
	<-errc
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
