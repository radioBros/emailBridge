package evidence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
)

// RegisterHandlers registers evidence.tsa-retry on the jobs service (Claude wires this at start).
func (s *Store) RegisterHandlers(js *jobs.Service) {
	if js == nil || s == nil {
		return
	}
	js.Handle(JobKindTSARetry, s.HandleTSARetry)
}

// HandleTSARetry stamps a previously archived object that lacked a TSA token.
func (s *Store) HandleTSARetry(ctx context.Context, j *jobs.Job, _ jobs.Reporter) (any, error) {
	var p TSARetryPayload
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid evidence.tsa-retry payload.")
		}
	}
	if p.Key == "" || p.SHA256 == "" || p.TSAURL == "" {
		return nil, errs.New(errs.CodeValidationFailed, "evidence.tsa-retry requires key, sha256 and tsaUrl.")
	}
	first := p.FirstAttemptAt.UTC()
	if first.IsZero() {
		first = j.CreatedAt.UTC()
	}
	if time.Now().UTC().After(first.Add(TSARetryMaxAge)) {
		return map[string]string{"skipped": "max_age"}, nil
	}
	// Already stamped?
	tsrKey := TSRKeyFor(p.Key)
	if ok, _ := s.backend.Exists(ctx, tsrKey); ok {
		return map[string]string{"skipped": "already_stamped"}, nil
	}
	token, at, err := s.Stamp(ctx, p.SHA256, p.TSAURL)
	if err != nil {
		return nil, err
	}
	manKey := ManifestKeyFor(p.Key)
	man, err := s.readManifest(ctx, manKey)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "evidence.tsa-retry.manifest")
	}
	until := time.Now().UTC().Add(s.retention)
	if locked, u, _ := s.backend.GetRetention(ctx, p.Key); locked && u != nil {
		until = *u
	}
	if err := s.storeToken(ctx, p.Key, manKey, *man, token, at, until); err != nil {
		return nil, errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.tsa-retry.store")
	}
	return map[string]any{"tsaAt": at.UTC(), "tsrKey": tsrKey}, nil
}

// NewTSARetryPayload builds a payload the caller passes to jobs.Create after a stamp failure.
func NewTSARetryPayload(ref Ref, item Item, firstAttemptAt time.Time) TSARetryPayload {
	if firstAttemptAt.IsZero() {
		firstAttemptAt = time.Now().UTC()
	}
	return TSARetryPayload{
		OrganizationID: item.Organization,
		AccountID:      item.Account,
		ID:             item.ID,
		Key:            ref.Key,
		SHA256:         ref.SHA256,
		TSAURL:         item.TSAURL,
		FirstAttemptAt: firstAttemptAt.UTC(),
	}
}
