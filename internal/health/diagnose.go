package health

import (
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/accounts"
)

// DiagnoseInput is everything needed to build an AccountHealth without I/O.
type DiagnoseInput struct {
	Account     *accounts.Account
	FolderCount int
	Now         time.Time
	Live        *LiveCheck
}

// Diagnose builds AccountHealth from stored account data (+ optional live check).
func Diagnose(in DiagnoseInput) AccountHealth {
	a := in.Account
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	liveCode, liveMsg := "", ""
	if in.Live != nil && in.Live.Error != nil {
		liveCode = in.Live.Error.Code
		liveMsg = in.Live.Error.Message
	}
	hints := collectHints(a, liveCode, liveMsg)
	sev := MaxSeverity(severityFromState(a.State, a.StateReason), severityFromHints(hints))
	stale := staleSync(a, now)
	if stale && sev.Rank() < SeverityInfo.Rank() {
		sev = SeverityInfo
	}
	// Missing IDLE on a connected IMAP account is informational.
	facts := buildFacts(a, in.FolderCount, in.Live)
	if a.State == accounts.StateConnected && !facts.Idle && !providerAPI(a.Provider) {
		sev = MaxSeverity(sev, SeverityInfo)
	}
	if in.Live != nil && !in.Live.OK {
		sev = MaxSeverity(sev, SeverityWarning)
		if in.Live.Error != nil && (in.Live.Error.Kind == "auth" || in.Live.Error.Code == "mail_auth_failed" ||
			in.Live.Error.Code == "oauth_revoked") {
			sev = MaxSeverity(sev, SeverityCritical)
		}
	}
	h := AccountHealth{
		AccountID:    a.ID,
		Name:         a.Name,
		Email:        a.Email,
		State:        a.State,
		StateReason:  stateReasonView(a.StateReason),
		Severity:     sev,
		Summary:      summarize(sev, a.State, hints, stale),
		Hints:        hints,
		Facts:        facts,
		RecentErrors: recentViews(a.RecentErrors),
		LiveCheck:    in.Live,
		CheckedAt:    now,
	}
	if h.Hints == nil {
		h.Hints = []Hint{}
	}
	return h
}

func providerAPI(provider string) bool {
	p := strings.ToLower(provider)
	return p == "gmail" || p == "graph"
}

func buildFacts(a *accounts.Account, folders int, live *LiveCheck) ProtocolFacts {
	f := ProtocolFacts{
		TLSMode:     a.IMAP.TLS,
		AuthUser:    a.IMAP.Auth.User,
		HasPassword: !a.IMAP.Auth.Pass.IsZero(),
		OAuthLinked: a.OAuth != nil,
		Provider:    providerFamily(a.Email, a.IMAP.Host, a.Provider),
		FolderCount: folders,
		LastSyncAt:  a.LastSyncAt,
		LastErrorAt: a.LastErrorAt,
	}
	f.Idle = capBool(a.Capabilities, "IDLE")
	f.Condstore = capBool(a.Capabilities, "CONDSTORE")
	f.QResync = capBool(a.Capabilities, "QRESYNC")
	if mechs, ok := capStrings(a.Capabilities, "auth"); ok {
		f.AuthMechanisms = mechs
	}
	if q, ok := a.Capabilities["quota"].(map[string]any); ok {
		qf := &QuotaFact{}
		if u, ok := asInt64(q["used"]); ok {
			qf.Used = &u
		}
		if l, ok := asInt64(q["limit"]); ok {
			qf.Limit = &l
		}
		if qf.Used != nil || qf.Limit != nil {
			f.Quota = qf
		}
	}
	if live != nil && live.OK && live.LatencyMs > 0 {
		ms := live.LatencyMs
		f.ConnectLatencyMs = &ms
		for _, c := range live.Capabilities {
			u := strings.ToUpper(c)
			switch u {
			case "IDLE":
				f.Idle = true
			case "CONDSTORE":
				f.Condstore = true
			case "QRESYNC":
				f.QResync = true
			}
		}
	}
	return f
}

func capBool(caps map[string]any, key string) bool {
	if caps == nil {
		return false
	}
	if v, ok := caps[key]; ok {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			return t != "" && !strings.EqualFold(t, "false")
		}
		return true
	}
	// Some stores use lowercase keys.
	if v, ok := caps[strings.ToLower(key)]; ok {
		b, _ := v.(bool)
		return b
	}
	return false
}

func capStrings(caps map[string]any, key string) ([]string, bool) {
	if caps == nil {
		return nil, false
	}
	v, ok := caps[key]
	if !ok {
		v, ok = caps[strings.ToLower(key)]
	}
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case float64:
		return int64(t), true
	default:
		return 0, false
	}
}

// ToRow projects a diagnosis to an org-list row.
func ToRow(h AccountHealth) Row {
	codes := make([]string, 0, len(h.Hints))
	for _, hint := range h.Hints {
		codes = append(codes, hint.Code)
	}
	return Row{
		AccountID: h.AccountID, Name: h.Name, Email: h.Email, State: h.State,
		Severity: h.Severity, Summary: h.Summary, HintCodes: codes,
		LastSyncAt: h.Facts.LastSyncAt, LastErrorAt: h.Facts.LastErrorAt,
	}
}
