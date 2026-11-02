package accounts

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ProtocolLogAccount returns the account's organisation after enforcing accounts:read (and
// account-bound API key checks). Used by the protolog API via the AccountGate interface.
func (s *Service) ProtocolLogAccount(ctx context.Context, accountID string) (organizationID string, err error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", accountID); err != nil {
		return "", err
	}
	a, err := s.load(ctx, accountID)
	if err != nil {
		return "", err
	}
	return a.OrganizationID, nil
}

// ProtocolLogDownload audits a protocol-logs download (protolog.DownloadAuditor).
func (s *Service) ProtocolLogDownload(ctx context.Context, accountID string, from, to time.Time) error {
	s.audit(ctx, "account.protocolLogs.download", accountID, audit.Changes{
		"from": {After: from.UTC().Format(time.RFC3339)},
		"to":   {After: to.UTC().Format(time.RFC3339)},
	})
	return nil
}

func boolSwitch(sw map[string]any, name string) bool {
	if sw == nil {
		return false
	}
	v, _ := sw[name].(bool)
	return v
}

// applyProtocolSwitches enforces owner/admin for protocol-log switches, sets RawDebugUntil when raw
// debug is enabled (now + TTL), clears it when off, and reports whether the sync owner must reconnect.
func (s *Service) applyProtocolSwitches(ctx context.Context, a *Account, next map[string]any, now time.Time) (configChanged bool, err error) {
	prevEnabled := boolSwitch(a.Switches, "protocolLogs.enabled")
	prevRaw := a.rawDebugActive()
	nextEnabled := boolSwitch(next, "protocolLogs.enabled")
	nextRaw := boolSwitch(next, "protocolLogs.rawDebug")

	if !nextRaw {
		a.RawDebugUntil = nil
	}
	needsPerm := prevEnabled != nextEnabled || nextRaw || (prevRaw && !nextRaw)
	if !needsPerm {
		return false, nil
	}
	if err := tenancy.Require(ctx, tenancy.PermProtocolLogs); err != nil {
		return false, err
	}
	if nextRaw {
		until := now.UTC().Add(s.rawDebugTTL())
		a.RawDebugUntil = &until
		s.audit(ctx, "account.protocolLogs.rawDebug.enable", a.ID, audit.Changes{
			"rawDebugUntil": {After: until.Format(time.RFC3339)},
			"ttl":           {After: s.rawDebugTTL().String()},
		})
	}
	return prevEnabled != nextEnabled || prevRaw != nextRaw || nextRaw, nil
}
