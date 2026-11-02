package attachments

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// fakeClamd listens once and replies with reply after the INSTREAM body ends.
func fakeClamd(t *testing.T, reply string) string {
	t.Helper()
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
		buf := make([]byte, 16)
		_, _ = io.ReadFull(c, buf[:10])
		for {
			var hdr [4]byte
			if _, err := io.ReadFull(c, hdr[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(hdr[:])
			if n == 0 {
				_, _ = io.WriteString(c, reply)
				return
			}
			_, _ = io.CopyN(io.Discard, c, int64(n))
		}
	}()
	return ln.Addr().String()
}

func TestOpenBodyFetcherMissFallsToRaw(t *testing.T) {
	s := testSvc(t)
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	ctx := context.Background()
	res, err := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME()})
	if err != nil {
		t.Fatal(err)
	}
	s.Fetcher = fakeFetcher{err: errors.New("imap down")}
	d, rc, err := s.Open(ctx, acc, msg, res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "pdf" || d.Filename != "a.pdf" {
		t.Fatalf("%q %+v", body, d)
	}
}

func TestOpenNoSource(t *testing.T) {
	s := testSvc(t)
	s.Raw = nil
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	ctx := context.Background()
	res, err := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, acc, msg, res[0].ID); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		t.Fatal(err)
	}
}

func TestIngestInfectedSkipsS3(t *testing.T) {
	s := testSvc(t)
	ln := fakeClamd(t, "stream: Win.Test.EICAR FOUND\x00")
	s.Scanner = &Scanner{Addr: ln, Timeout: 2 * time.Second}
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	ctx := context.Background()
	res, err := s.Ingest(ctx, IngestInput{
		OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME(), Scan: true, StoreS3: true,
	})
	if err != nil || res[0].Scan != ScanInfected || res[0].URL != "" {
		t.Fatalf("%+v %v", res, err)
	}
	docs, _ := s.store.listByMessage(ctx, acc, msg)
	if !docs[0].Infected || docs[0].ObjectKey != "" {
		t.Fatalf("%+v", docs[0])
	}
}

func TestListInlineAndEmptyFilename(t *testing.T) {
	raw := []byte("MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<html></html>\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-Disposition: inline\r\n" +
		"Content-Id: <cid1>\r\n\r\nx\r\n" +
		"--b--\r\n")
	parts, err := listParts(raw)
	if err != nil || len(parts) != 1 || parts[0].Disposition != "inline" || parts[0].ContentID != "cid1" {
		t.Fatalf("%+v %v", parts, err)
	}
}

func TestListNestedMultipart(t *testing.T) {
	raw := []byte("MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain\r\n\r\np\r\n" +
		"--inner\r\nContent-Type: text/html\r\n\r\n<h>\r\n" +
		"--inner--\r\n" +
		"--outer\r\nContent-Type: application/zip\r\n" +
		"Content-Disposition: attachment; filename=\"z.zip\"\r\n\r\nZZ\r\n" +
		"--outer--\r\n")
	parts, err := listParts(raw)
	if err != nil || len(parts) != 1 || parts[0].Filename != "z.zip" || parts[0].PartID == "" {
		t.Fatalf("%+v %v", parts, err)
	}
}

func TestNowUTC(t *testing.T) {
	if nowUTC().IsZero() {
		t.Fatal("zero")
	}
}

func TestContentDispositionEdge(t *testing.T) {
	if contentDisposition("INLINE", "") != "inline" {
		t.Fatal(contentDisposition("INLINE", ""))
	}
	d := contentDisposition("attachment", "a\"b\\c.txt")
	if !strings.Contains(d, "filename*") {
		t.Fatal(d)
	}
	if got := asciiFilename("\x01"); got != "_" {
		t.Fatal(got)
	}
	if asciiFilename("") != "attachment" {
		t.Fatal(asciiFilename(""))
	}
}

func TestParseClamAddrErrors(t *testing.T) {
	if _, _, err := parseClamAddr("ftp://x"); err == nil {
		t.Fatal("scheme")
	}
	if _, _, err := parseClamAddr("://bad"); err == nil {
		t.Fatal("url")
	}
}

func TestScannerUnexpectedResponse(t *testing.T) {
	ln := fakeClamd(t, "stream: WHAT\x00")
	s := &Scanner{Addr: ln, Timeout: 2 * time.Second}
	if r := s.Scan(context.Background(), bytes.NewReader([]byte("x")), 1); r.Status != ScanError {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyTokenEdgeCases(t *testing.T) {
	key := []byte("k")
	if _, _, err := VerifyToken(key, "", time.Now()); err == nil {
		t.Fatal("empty")
	}
	if _, _, err := VerifyToken(nil, "a.b", time.Now()); err == nil {
		t.Fatal("nil key")
	}
	if _, _, err := VerifyToken(key, "abc", time.Now()); err == nil {
		t.Fatal("no dot")
	}
	if _, _, err := VerifyToken(key, "bad!.sig", time.Now()); err == nil {
		t.Fatal("bad b64")
	}
	tok, _ := Sign(key, "o", "a", time.Now().Add(time.Hour))
	parts := strings.SplitN(tok, ".", 2)
	// corrupt payload that still verifies? change payload without sig → fail
	if _, _, err := VerifyToken(key, "YQ.+"+parts[1], time.Now()); err == nil {
		t.Fatal("bad payload")
	}
	if errs.CodeOf(mapTokenErr(errors.New("other"))) != errs.CodeSignedURLInvalid {
		t.Fatal("wrap")
	}
}

func TestSignedURLSignError(t *testing.T) {
	s := testSvc(t)
	s.SignKey = nil
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	ctx := context.Background()
	res, _ := s.Ingest(ctx, IngestInput{OrganizationID: org, AccountID: acc, MessageID: msg, Raw: sampleMIME()})
	docs, _ := s.store.listByMessage(ctx, acc, msg)
	docs[0].ObjectKey = "k"
	_ = s.store.upsert(ctx, docs[0])
	if _, err := s.SignedURL(ctx, acc, msg, res[0].ID); err == nil {
		t.Fatal("expected sign error")
	}
}

func TestModuleDefaultsAndLog(t *testing.T) {
	m := &Module{Log: slog.Default()}
	if m.Service() != nil {
		t.Fatal("before start")
	}
	s := &Service{}
	s.init()
	if s.log() == nil {
		t.Fatal("log")
	}
	s.Log = slog.Default()
	if s.log() == nil {
		t.Fatal("log set")
	}
	if clampTTL(time.Minute) != time.Minute {
		t.Fatal("ttl")
	}
	_ = bytesReader([]byte("y"))
	if !needsStar(`a"b`) || needsStar("plain.txt") {
		t.Fatal("needsStar")
	}
}

func TestGetAttachmentRequiresTenancy(t *testing.T) {
	s := testSvc(t)
	_, err := s.getAttachment(context.Background(), &getAttIn{
		AccountID:    "acc_01habcdefghijklmnopqrstuvw",
		MessageID:    "msg_01habcdefghijklmnopqrstuvw",
		AttachmentID: "att_01habcdefghijklmnopqrstuvw",
	})
	if err == nil {
		t.Fatal("expected tenancy error")
	}
}

func TestMigrationNumber(t *testing.T) {
	mig := Migration(27)
	if mig.Number != 27 || mig.Name == "" || mig.Up == nil {
		t.Fatal(mig)
	}
}
