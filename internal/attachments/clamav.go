package attachments

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// Scanner talks to clamd over TCP or Unix (INSTREAM).
type Scanner struct {
	Addr    string // tcp://host:3310 or unix:///path or host:port
	Timeout time.Duration
	MaxSize int64
	Dial    func(ctx context.Context, network, address string) (net.Conn, error) // tests
}

// ScanResult is the outcome of one INSTREAM scan.
type ScanResult struct {
	Status string // clean | infected | error | skipped
	Name   string // virus name when infected
	Err    error
}

// Scan streams r to clamd. Failures never panic; Status is error/skipped.
func (s *Scanner) Scan(ctx context.Context, r io.Reader, size int64) ScanResult {
	if s == nil || strings.TrimSpace(s.Addr) == "" {
		return ScanResult{Status: ScanSkipped}
	}
	max := s.MaxSize
	if max <= 0 {
		max = 25 << 20
	}
	if size > max {
		return ScanResult{Status: ScanSkipped}
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	network, address, err := parseClamAddr(s.Addr)
	if err != nil {
		return ScanResult{Status: ScanError, Err: err}
	}
	dial := s.Dial
	if dial == nil {
		dial = defaultDial
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dial(dctx, network, address)
	if err != nil {
		return ScanResult{Status: ScanError, Err: err}
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := io.WriteString(conn, "zINSTREAM\x00"); err != nil {
		return ScanResult{Status: ScanError, Err: err}
	}
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if written+int64(n) > max {
				n = int(max - written)
			}
			if n > 0 {
				var hdr [4]byte
				binary.BigEndian.PutUint32(hdr[:], uint32(n)) // chunk ≤ 32KiB
				if _, err := conn.Write(hdr[:]); err != nil {
					return ScanResult{Status: ScanError, Err: err}
				}
				if _, err := conn.Write(buf[:n]); err != nil {
					return ScanResult{Status: ScanError, Err: err}
				}
				written += int64(n)
			}
			if written >= max {
				break
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return ScanResult{Status: ScanError, Err: rerr}
		}
	}
	var zero [4]byte
	if _, err := conn.Write(zero[:]); err != nil {
		return ScanResult{Status: ScanError, Err: err}
	}
	line, err := bufio.NewReader(conn).ReadString('\x00')
	if err != nil && !errors.Is(err, io.EOF) {
		return ScanResult{Status: ScanError, Err: err}
	}
	line = strings.TrimRight(line, "\x00\n\r")
	switch {
	case strings.HasSuffix(line, " OK"):
		return ScanResult{Status: ScanClean}
	case strings.Contains(line, " FOUND"):
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "stream: "), " FOUND"))
		return ScanResult{Status: ScanInfected, Name: name}
	default:
		return ScanResult{Status: ScanError, Err: fmt.Errorf("clamav: unexpected response %q", line)}
	}
}

func defaultDial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func parseClamAddr(raw string) (network, address string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("clamav: empty address")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", err
		}
		switch u.Scheme {
		case "tcp", "tcp4", "tcp6":
			return "tcp", u.Host, nil
		case "unix":
			return "unix", u.Path, nil
		default:
			return "", "", fmt.Errorf("clamav: unsupported scheme %q", u.Scheme)
		}
	}
	if strings.HasPrefix(raw, "/") {
		return "unix", raw, nil
	}
	return "tcp", raw, nil
}
