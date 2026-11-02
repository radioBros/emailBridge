package smtpc

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSendSimpleImplicitTLSAndAuth(t *testing.T) {
	t.Parallel()
	cert := testCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serveSMTPSession(conn, true)
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	err = SendSimple(context.Background(), Options{
		Host: "127.0.0.1", Port: port, TLS: TLSImplicit,
		User: "u", Pass: "p", EHLO: "eb",
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}, "a@b", []string{"c@d"}, []byte("From: a@b\r\nTo: c@d\r\n\r\nhi\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestSendSimpleStartTLS(t *testing.T) {
	t.Parallel()
	cert := testCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serveStartTLS(conn, cert)
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	err = SendSimple(context.Background(), Options{
		Host: "127.0.0.1", Port: port, TLS: TLSStartTLS, EHLO: "eb",
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}, "a@b", []string{"c@d"}, []byte("From: a@b\r\nTo: c@d\r\n\r\nhi\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestSendSimpleNoRecipients(t *testing.T) {
	err := SendSimple(context.Background(), Options{Host: "127.0.0.1"}, "a@b", nil, nil)
	if errs.CodeOf(err) != errs.CodeSMTPPermanent {
		t.Fatal(err)
	}
}

func TestSendSimpleBadTLSMode(t *testing.T) {
	err := SendSimple(context.Background(), Options{Host: "127.0.0.1", Port: 1, TLS: "weird"}, "a@b", []string{"c@d"}, nil)
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}
}

func TestClassifyConnLost(t *testing.T) {
	err := Classify(string(StageDataEnd), errors.New("write: broken pipe"))
	if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
		t.Fatal(err)
	}
	err = Classify(string(StageData), errors.New("connection reset by peer"))
	if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
		t.Fatal(err)
	}
	err = Classify(string(StageDataEnd), net.ErrClosed)
	if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
		t.Fatal(err)
	}
	err = Classify(string(StageAuth), errors.New("connection reset during AUTH"))
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}
}

func TestSendSimpleDefaultsAndDialFail(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // refuse connections

	err = SendSimple(context.Background(), Options{
		Host: "127.0.0.1", Port: port, TLS: TLSNone,
	}, "a@b", []string{"c@d"}, []byte("x"))
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}

	// default port/TLS with unreachable host
	err = SendSimple(context.Background(), Options{Host: "127.0.0.1", Port: 1}, "a@b", []string{"c@d"}, nil)
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}
}

func TestDialTLSHandshakeFail(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = c.Write([]byte("not tls"))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	err = SendSimple(context.Background(), Options{
		Host: "127.0.0.1", Port: port, TLS: TLSImplicit,
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}, "a@b", []string{"c@d"}, nil)
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}
}

func serveStartTLS(conn net.Conn, cert tls.Certificate) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) { _, _ = w.WriteString(s); _ = w.Flush() }
	write("220 plain\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimRight(line, "\r\n"))
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-localhost\r\n250 STARTTLS\r\n")
		case upper == "STARTTLS":
			write("220 ready\r\n")
			_ = w.Flush()
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			serveSMTPSession(tlsConn, false)
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func serveSMTPSession(conn net.Conn, greet bool) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) { _, _ = w.WriteString(s); _ = w.Flush() }
	if greet {
		write("220 tls\r\n")
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimRight(line, "\r\n"))
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-localhost\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			write("235 ok\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
			write("250 OK\r\n")
		case upper == "DATA":
			write("354 Go\r\n")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
			}
			write("250 OK\r\n")
		case upper == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
