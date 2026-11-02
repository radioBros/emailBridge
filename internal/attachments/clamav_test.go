package attachments

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseClamAddr(t *testing.T) {
	n, a, err := parseClamAddr("tcp://127.0.0.1:3310")
	if err != nil || n != "tcp" || a != "127.0.0.1:3310" {
		t.Fatalf("%s %s %v", n, a, err)
	}
	n, a, err = parseClamAddr("unix:///var/run/clamd.sock")
	if err != nil || n != "unix" || a != "/var/run/clamd.sock" {
		t.Fatalf("%s %s %v", n, a, err)
	}
	n, a, err = parseClamAddr("/tmp/clamd.sock")
	if err != nil || n != "unix" {
		t.Fatalf("%s %s %v", n, a, err)
	}
	n, a, err = parseClamAddr("127.0.0.1:3310")
	if err != nil || n != "tcp" {
		t.Fatalf("%s %s %v", n, a, err)
	}
	if _, _, err := parseClamAddr(""); err == nil {
		t.Fatal("empty")
	}
}

func TestScannerSkippedAndFakeClamd(t *testing.T) {
	if r := (*Scanner)(nil).Scan(context.Background(), bytes.NewReader(nil), 0); r.Status != ScanSkipped {
		t.Fatal(r)
	}
	if r := (&Scanner{}).Scan(context.Background(), bytes.NewReader(nil), 0); r.Status != ScanSkipped {
		t.Fatal(r)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		buf := make([]byte, 16)
		_, _ = io.ReadFull(c, buf[:10]) // zINSTREAM\0
		for {
			var hdr [4]byte
			if _, err := io.ReadFull(c, hdr[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(hdr[:])
			if n == 0 {
				_, _ = io.WriteString(c, "stream: OK\x00")
				return
			}
			_, _ = io.CopyN(io.Discard, c, int64(n))
		}
	}()
	s := &Scanner{Addr: ln.Addr().String(), Timeout: 2 * time.Second, MaxSize: 1 << 20}
	r := s.Scan(context.Background(), bytes.NewReader([]byte("clean-bytes")), 11)
	if r.Status != ScanClean {
		t.Fatalf("%+v", r)
	}

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln2.Close() }()
	go func() {
		c, err := ln2.Accept()
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
				_, _ = io.WriteString(c, "stream: Eicar-Test-Signature FOUND\x00")
				return
			}
			_, _ = io.CopyN(io.Discard, c, int64(n))
		}
	}()
	s2 := &Scanner{Addr: ln2.Addr().String(), Timeout: 2 * time.Second}
	r2 := s2.Scan(context.Background(), bytes.NewReader([]byte("X")), 1)
	if r2.Status != ScanInfected || !strings.Contains(r2.Name, "Eicar") {
		t.Fatalf("%+v", r2)
	}

	s3 := &Scanner{Addr: "127.0.0.1:1", Timeout: 50 * time.Millisecond}
	if r := s3.Scan(context.Background(), bytes.NewReader([]byte("x")), 1); r.Status != ScanError {
		t.Fatalf("%+v", r)
	}
	s4 := &Scanner{Addr: "tcp://127.0.0.1:9", MaxSize: 1}
	if r := s4.Scan(context.Background(), bytes.NewReader([]byte("ab")), 2); r.Status != ScanSkipped {
		t.Fatalf("oversize %+v", r)
	}
}
