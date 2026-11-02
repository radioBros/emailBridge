package integrations

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
)

// rspamdTargetTTL bounds how long a node keeps an organisation's Rspamd target: sync asks for every new
// message, so the lookup must not hit MongoDB each time; an edit applies within this delay.
const rspamdTargetTTL = time.Minute

type rspamdEntry struct {
	opts  *connectors.RspamdOptions
	until time.Time
}

// rspamdCache is the per-node cache of RspamdTarget results (nil options = no enabled instance).
type rspamdCache struct {
	mu   sync.Mutex
	rows map[string]rspamdEntry
}

// RspamdTarget returns the Rspamd endpoint of the organisation's enabled Rspamd instance (P8.3), or nil when
// it has none. The controller password is decrypted here and never leaves the node.
func (s *Service) RspamdTarget(ctx context.Context, orgID string) (*connectors.RspamdOptions, error) {
	now := time.Now()
	s.rspamd.mu.Lock()
	if e, ok := s.rspamd.rows[orgID]; ok && now.Before(e.until) {
		s.rspamd.mu.Unlock()
		return e.opts, nil
	}
	s.rspamd.mu.Unlock()

	opts, err := s.loadRspamd(store.WithOrganization(ctx, orgID))
	if err != nil {
		return nil, err
	}
	s.rspamd.mu.Lock()
	if s.rspamd.rows == nil {
		s.rspamd.rows = map[string]rspamdEntry{}
	}
	s.rspamd.rows[orgID] = rspamdEntry{opts: opts, until: now.Add(rspamdTargetTTL)}
	s.rspamd.mu.Unlock()
	return opts, nil
}

func (s *Service) loadRspamd(ctx context.Context) (*connectors.RspamdOptions, error) {
	rows, err := s.Repo.listByType(ctx, TypeRspamd)
	if err != nil {
		return nil, err
	}
	def, ok := s.reg().Get(TypeRspamd)
	if !ok {
		return nil, nil
	}
	for i := range rows {
		inst := &rows[i]
		if !inst.Enabled {
			continue
		}
		base, _ := inst.Config["baseUrl"].(string)
		if strings.TrimSpace(base) == "" {
			continue
		}
		secrets, err := s.decryptSecrets(ctx, inst, def)
		if err != nil {
			return nil, err
		}
		opts := &connectors.RspamdOptions{BaseURL: base, Password: secrets["password"]}
		if t, _ := inst.Config["timeout"].(string); t != "" {
			if d, err := time.ParseDuration(t); err == nil && d > 0 {
				opts.Timeout = d
			}
		}
		return opts, nil
	}
	return nil, nil
}

// HTTPClient is the client connector calls use (the egress dialer's client when wired).
func (s *Service) HTTPClient() *http.Client { return s.client() }
