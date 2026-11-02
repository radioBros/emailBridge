package autoconfig

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type mozillaConfig struct {
	XMLName       xml.Name `xml:"clientConfig"`
	EmailProvider struct {
		Incoming []mozillaServer `xml:"incomingServer"`
		Outgoing []mozillaServer `xml:"outgoingServer"`
	} `xml:"emailProvider"`
}

type mozillaServer struct {
	Type     string `xml:"type,attr"`
	Hostname string `xml:"hostname"`
	Port     int    `xml:"port"`
	Socket   string `xml:"socketType"`
	Username string `xml:"username"`
}

func (s *Service) tryISPDB(ctx context.Context, domain string) *Result {
	urls := []string{
		fmt.Sprintf("https://autoconfig.%s/mail/config-v1.1.xml", domain),
		fmt.Sprintf("https://%s/.well-known/autoconfig/mail/config-v1.1.xml", domain),
		fmt.Sprintf("https://autoconfig.thunderbird.net/v1.1/%s", domain),
	}
	for _, u := range urls {
		r, err := s.fetchMozilla(ctx, u)
		if err == nil && r != nil {
			return r
		}
	}
	return nil
}

func (s *Service) fetchMozilla(ctx context.Context, url string) (*Result, error) {
	if s.HTTP == nil {
		return nil, fmt.Errorf("no http")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml, text/xml, */*")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseMozillaXML(body)
}

func parseMozillaXML(body []byte) (*Result, error) {
	var cfg mozillaConfig
	if err := xml.Unmarshal(body, &cfg); err != nil {
		return nil, err
	}
	out := &Result{Source: SourceISPDB}
	for _, in := range cfg.EmailProvider.Incoming {
		if !strings.EqualFold(in.Type, "imap") || in.Hostname == "" {
			continue
		}
		out.IMAP = &Server{
			Host: in.Hostname, Port: in.Port, TLS: socketTLS(in.Socket), User: mozillaUser(in.Username),
		}
		break
	}
	for _, outg := range cfg.EmailProvider.Outgoing {
		if !strings.EqualFold(outg.Type, "smtp") || outg.Hostname == "" {
			continue
		}
		out.SMTP = &Server{
			Host: outg.Hostname, Port: outg.Port, TLS: socketTLS(outg.Socket), User: mozillaUser(outg.Username),
		}
		break
	}
	if out.IMAP == nil && out.SMTP == nil {
		return nil, fmt.Errorf("empty config")
	}
	return out, nil
}

func socketTLS(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SSL", "SSL/TLS":
		return tlsImplicit
	case "STARTTLS":
		return tlsStartTLS
	default:
		if s == "" {
			return tlsImplicit
		}
		return tlsNone
	}
}

func mozillaUser(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case u == "" || strings.EqualFold(u, "%EMAILADDRESS%"):
		return userEmail
	case strings.EqualFold(u, "%EMAILLOCALPART%"):
		return userLocal
	default:
		return u
	}
}
