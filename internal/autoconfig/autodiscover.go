package autoconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type adJSON struct {
	Protocol string `json:"Protocol"`
	URL      string `json:"Url"`
}

type poxRequest struct {
	XMLName xml.Name `xml:"Autodiscover"`
	XMLNS   string   `xml:"xmlns,attr"`
	Request struct {
		EMailAddress string `xml:"EMailAddress"`
		Acceptable   string `xml:"AcceptableResponseSchema"`
	} `xml:"Request"`
}

type poxResponse struct {
	XMLName  xml.Name `xml:"Autodiscover"`
	Response struct {
		Account struct {
			Protocol []poxProtocol `xml:"Protocol"`
		} `xml:"Account"`
	} `xml:"Response"`
}

type poxProtocol struct {
	Type   string `xml:"Type"`
	Server string `xml:"Server"`
	Port   int    `xml:"Port"`
	SSL    string `xml:"SSL"`
	Login  string `xml:"LoginName"`
}

func (s *Service) tryAutodiscover(ctx context.Context, email, domain string) *Result {
	urls := []string{}
	if u := s.autodiscoverJSONURL(ctx, email); u != "" {
		urls = append(urls, u)
	}
	urls = append(urls,
		fmt.Sprintf("https://autodiscover.%s/autodiscover/autodiscover.xml", domain),
		fmt.Sprintf("https://%s/autodiscover/autodiscover.xml", domain),
	)
	for _, u := range urls {
		if r := s.fetchPOX(ctx, u, email); r != nil {
			return r
		}
	}
	return nil
}

func (s *Service) autodiscoverJSONURL(ctx context.Context, email string) string {
	if s.HTTP == nil {
		return ""
	}
	u := "https://autodiscover-s.outlook.com/autodiscover/autodiscover.json/v2.0/" + email + "?Protocol=Autodiscoverv1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return ""
	}
	var body adJSON
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.URL)
}

func (s *Service) fetchPOX(ctx context.Context, url, email string) *Result {
	if s.HTTP == nil {
		return nil
	}
	var reqBody poxRequest
	reqBody.XMLNS = "http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006"
	reqBody.Request.EMailAddress = email
	reqBody.Request.Acceptable = "http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a"
	raw, err := xml.Marshal(reqBody)
	if err != nil {
		return nil
	}
	payload := append([]byte(xml.Header), raw...)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil
	}
	return parsePOX(body)
}

func parsePOX(body []byte) *Result {
	var resp poxResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil
	}
	out := &Result{Source: SourceAutodiscover, OAuthHint: HintOutlook}
	for _, p := range resp.Response.Account.Protocol {
		tlsMode := tlsStartTLS
		if strings.EqualFold(p.SSL, "on") || p.Port == 993 || p.Port == 465 {
			tlsMode = tlsImplicit
		}
		user := userEmail
		if p.Login != "" {
			user = p.Login
		}
		switch strings.ToUpper(p.Type) {
		case "IMAP":
			if out.IMAP == nil && p.Server != "" {
				port := p.Port
				if port == 0 {
					port = 993
				}
				out.IMAP = &Server{Host: p.Server, Port: port, TLS: tlsMode, User: user}
			}
		case "SMTP":
			if out.SMTP == nil && p.Server != "" {
				port := p.Port
				if port == 0 {
					port = 587
				}
				out.SMTP = &Server{Host: p.Server, Port: port, TLS: tlsMode, User: user}
			}
		}
	}
	if out.IMAP == nil && out.SMTP == nil {
		return nil
	}
	return out
}
