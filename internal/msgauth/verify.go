package msgauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
	"golang.org/x/net/publicsuffix"
)

// MaxRawBytes is the verify-mode raw message cap (S5 §4.1).
const MaxRawBytes = 10 << 20

// ErrTooLarge is returned when the raw message exceeds MaxRawBytes.
var ErrTooLarge = errors.New("msgauth: raw message exceeds 10 MiB")

// LookupTXT looks up DNS TXT records (injected for tests).
type LookupTXT func(domain string) ([]string, error)

// PolicyLookup returns the DMARC record for a domain (injected for tests).
// Return (nil, dmarc.ErrNoPolicy) when no policy exists.
type PolicyLookup func(domain string) (*dmarc.Record, error)

// Resolver bundles DNS lookups used by Verify.
type Resolver struct {
	LookupTXT LookupTXT
	DMARC     PolicyLookup
}

// Verify runs DKIM verification of all signatures and DMARC evaluation against the RFC5322.From
// domain, using headerSPF for the SPF input. Source is "verify". ARC is not evaluated here (none).
func Verify(ctx context.Context, raw []byte, headerSPF Item, res Resolver) (*Results, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxRawBytes {
		return nil, ErrTooLarge
	}
	out := emptyResults(SourceVerify)
	out.SPF = Item{
		Result: normalizeResult(headerSPF.Result),
		Domain: strings.ToLower(headerSPF.Domain),
	}

	lookup := res.LookupTXT
	verifs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: lookup})
	if err != nil && !errors.Is(err, dkim.ErrTooManySignatures) {
		// Unexpected I/O errors — surface them. Signature-level failures are on each Verification.
		if !isDKIMSigErr(err) {
			return nil, fmt.Errorf("msgauth: dkim verify: %w", err)
		}
	}
	selectors := dkimSelectorsFromMessage(raw)
	for i, v := range verifs {
		item := Item{Domain: strings.ToLower(v.Domain)}
		if i < len(selectors) {
			item.Selector = selectors[i]
		}
		item.Result = dkimResultOf(v)
		out.DKIM = append(out.DKIM, item)
	}

	fromDomain, err := fromDomainOf(raw)
	if err != nil || fromDomain == "" {
		out.DMARC = Item{Result: "permerror"}
		return out, nil
	}
	out.DMARC = evaluateDMARC(fromDomain, out.SPF, out.DKIM, res.DMARC)
	return out, nil
}

func isDKIMSigErr(err error) bool {
	return dkim.IsPermFail(err) || dkim.IsTempFail(err)
}

func dkimResultOf(v *dkim.Verification) string {
	if v == nil {
		return "none"
	}
	if v.Err == nil {
		return "pass"
	}
	if dkim.IsTempFail(v.Err) {
		return "temperror"
	}
	if dkim.IsPermFail(v.Err) {
		return "permerror"
	}
	return "fail"
}

func fromDomainOf(raw []byte) (string, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	addr, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(addr.Address, "@")
	if i < 0 || i == len(addr.Address)-1 {
		return "", errors.New("msgauth: From has no domain")
	}
	return strings.ToLower(addr.Address[i+1:]), nil
}

func dkimSelectorsFromMessage(raw []byte) []string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	vals := msg.Header[textproto.CanonicalMIMEHeaderKey("DKIM-Signature")]
	var out []string
	for _, v := range vals {
		out = append(out, selectorFromDKIMSig(v))
	}
	return out
}

func selectorFromDKIMSig(v string) string {
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "s") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func evaluateDMARC(fromDomain string, spf Item, dkimItems []Item, lookup PolicyLookup) Item {
	item := Item{Domain: fromDomain, Result: "none"}
	if lookup == nil {
		lookup = func(domain string) (*dmarc.Record, error) {
			return dmarc.Lookup(domain)
		}
	}
	rec, err := lookup(fromDomain)
	if err != nil {
		if errors.Is(err, dmarc.ErrNoPolicy) {
			return item
		}
		if dmarc.IsTempFail(err) {
			item.Result = "temperror"
			return item
		}
		item.Result = "permerror"
		return item
	}
	if rec == nil {
		return item
	}

	dkimAligned := false
	for _, d := range dkimItems {
		if d.Result != "pass" {
			continue
		}
		if aligned(fromDomain, d.Domain, rec.DKIMAlignment) {
			dkimAligned = true
			break
		}
	}
	spfAligned := spf.Result == "pass" && aligned(fromDomain, spf.Domain, rec.SPFAlignment)
	if dkimAligned || spfAligned {
		item.Result = "pass"
	} else {
		item.Result = "fail"
	}
	return item
}

func aligned(fromDomain, authDomain string, mode dmarc.AlignmentMode) bool {
	fromDomain = strings.ToLower(strings.TrimSpace(fromDomain))
	authDomain = strings.ToLower(strings.TrimSpace(authDomain))
	if fromDomain == "" || authDomain == "" {
		return false
	}
	if fromDomain == authDomain {
		return true
	}
	if mode == dmarc.AlignmentStrict {
		return false
	}
	// Relaxed: same organisational domain (eTLD+1).
	fo, err1 := publicsuffix.EffectiveTLDPlusOne(fromDomain)
	ao, err2 := publicsuffix.EffectiveTLDPlusOne(authDomain)
	if err1 != nil || err2 != nil {
		// Fallback: subdomain relationship.
		return strings.HasSuffix(fromDomain, "."+authDomain) || strings.HasSuffix(authDomain, "."+fromDomain)
	}
	return fo == ao
}
