package suppression

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchSuppression = "sending.suppression"

const (
	maxNameLen = 128
	maxDescLen = 512
	maxReason  = 256
	maxImport  = 10_000
)

// Service is suppression CRUD + submit check + P5.4 hooks.
type Service struct {
	Repo     *Repo
	Registry *features.Registry
	Env      string
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) registry() *features.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return features.DefaultRegistry()
}

// SuppressionEnabled reports whether submit should enforce lists for the org.
func (s *Service) SuppressionEnabled(ctx context.Context, org string) bool {
	sw, ok := s.registry().Get(switchSuppression)
	if !ok {
		return true
	}
	vals := features.Values{}
	if s.Repo != nil && s.Repo.DB != nil {
		var doc settings.Doc
		_ = s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc)
		if doc.Switches != nil {
			if sv, ok := doc.Switches[switchSuppression]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
				}
			}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return true
}

// CheckAddresses refuses when any address is on an org list and the switch is on.
// mailingListID is the optional RFC List-Id for this send (W53); empty = transactional.
func (s *Service) CheckAddresses(ctx context.Context, org string, addresses []string, mailingListID string) error {
	if !s.SuppressionEnabled(ctx, org) {
		return nil
	}
	norm := uniqueNorm(addresses)
	if len(norm) == 0 {
		return nil
	}
	hits, err := s.Repo.findHits(ctx, org, norm)
	if err != nil {
		return err
	}
	hits = filterHitsForList(hits, mailingListID)
	if len(hits) == 0 {
		return nil
	}
	return errs.New(errs.CodeRecipientSuppressed,
		fmt.Sprintf("%d recipient(s) are on a suppression list.", len(hits))).
		WithSafe("recipients", hits)
}

// filterHitsForList keeps bounce/complaint/manual always; unsubscribe only when global
// or matching mailingListID. List-scoped unsubs do not block transactional (no list) mail.
func filterHitsForList(hits []Hit, mailingListID string) []Hit {
	if len(hits) == 0 {
		return hits
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		switch h.Kind {
		case KindUnsubscribe:
			if h.MailingListID == "" {
				out = append(out, h) // global unsub
				continue
			}
			if mailingListID != "" && h.MailingListID == mailingListID {
				out = append(out, h)
			}
		default:
			out = append(out, h)
		}
	}
	return out
}

// CreateList inserts a list.
func (s *Service) CreateList(ctx context.Context, in CreateListInput) (*List, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	name, err := validateName(in.Name)
	if err != nil {
		return nil, err
	}
	kind := in.Kind
	if kind == "" {
		kind = KindManual
	}
	if !validKind(kind) {
		return nil, errs.New(errs.CodeValidationFailed, "kind must be manual, unsubscribe, bounce or complaint.")
	}
	desc, err := validateDesc(in.Description)
	if err != nil {
		return nil, err
	}
	now := s.now()
	l := &List{
		ID: store.NewID(store.PrefixSuppression), OrganizationID: org, Name: name, Kind: kind,
		Description: desc, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		l.CreatedBy = c.Actor()
	}
	if err := s.Repo.insertList(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// GetList returns one list.
func (s *Service) GetList(ctx context.Context, id string) (*List, error) {
	if err := tenancy.RequireRead(ctx, "suppression"); err != nil {
		return nil, err
	}
	if !store.IsID(store.PrefixSuppression, id) {
		return nil, errs.New(errs.CodeNotFound, "The suppression list does not exist.")
	}
	return s.Repo.getList(ctx, id)
}

// ListLists pages lists.
func (s *Service) ListLists(ctx context.Context, f ListFilter) (*ListPage, error) {
	if err := tenancy.RequireRead(ctx, "suppression"); err != nil {
		return nil, err
	}
	return s.Repo.listLists(ctx, f)
}

// PatchList updates name/description.
func (s *Service) PatchList(ctx context.Context, id, ifMatch string, in PatchListInput) (*List, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	l, err := s.Repo.getList(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkIfMatch(ifMatch, l.Version); err != nil {
		return nil, err
	}
	old := l.Version
	if in.Name != nil {
		name, err := validateName(*in.Name)
		if err != nil {
			return nil, err
		}
		l.Name = name
	}
	if in.Description != nil {
		desc, err := validateDesc(*in.Description)
		if err != nil {
			return nil, err
		}
		l.Description = desc
	}
	l.UpdatedAt = s.now()
	l.Version++
	if err := s.Repo.replaceList(ctx, l, old); err != nil {
		return nil, err
	}
	return l, nil
}

// DeleteList removes a list and its entries.
func (s *Service) DeleteList(ctx context.Context, id, ifMatch string) error {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return err
	}
	l, err := s.Repo.getList(ctx, id)
	if err != nil {
		return err
	}
	if err := checkIfMatch(ifMatch, l.Version); err != nil {
		return err
	}
	return s.Repo.deleteList(ctx, l)
}

// AddEntry upserts an address on a list.
func (s *Service) AddEntry(ctx context.Context, listID string, in CreateEntryInput) (*Entry, bool, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, false, err
	}
	l, err := s.Repo.getList(ctx, listID)
	if err != nil {
		return nil, false, err
	}
	addr, err := normalizeAddress(in.Address)
	if err != nil {
		return nil, false, err
	}
	reason := strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(reason) > maxReason {
		return nil, false, errs.New(errs.CodeValidationFailed, "reason must be at most 256 characters.")
	}
	e := &Entry{
		ID: entryID(l.ID, addr), ListID: l.ID, OrganizationID: l.OrganizationID,
		Address: addr, Reason: reason, Source: SourceManual, At: s.now(),
	}
	created, err := s.Repo.upsertEntry(ctx, e)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, err := s.Repo.getEntry(ctx, l.ID, e.ID)
		if err != nil {
			return e, false, nil
		}
		return existing, false, nil
	}
	return e, true, nil
}

// DeleteEntry removes one entry.
func (s *Service) DeleteEntry(ctx context.Context, listID, entryID string) error {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return err
	}
	if _, err := s.Repo.getList(ctx, listID); err != nil {
		return err
	}
	return s.Repo.deleteEntry(ctx, listID, entryID)
}

// ListEntries pages entries.
func (s *Service) ListEntries(ctx context.Context, f EntryFilter) (*EntryPage, error) {
	if err := tenancy.RequireRead(ctx, "suppression"); err != nil {
		return nil, err
	}
	if _, err := s.Repo.getList(ctx, f.ListID); err != nil {
		return nil, err
	}
	return s.Repo.listEntries(ctx, f)
}

// EnsureSystemList returns (creating if needed) the org's system list of kind.
func (s *Service) EnsureSystemList(ctx context.Context, org, kind string) (*List, error) {
	if kind != KindUnsubscribe && kind != KindBounce && kind != KindComplaint {
		return nil, errs.New(errs.CodeValidationFailed, "not a system list kind")
	}
	if existing, err := s.Repo.getListByKind(ctx, org, kind); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	now := s.now()
	l := &List{
		ID: store.NewID(store.PrefixSuppression), OrganizationID: org,
		Name: systemName(kind), Kind: kind, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := s.Repo.insertList(ctx, l); err != nil {
		if errs.CodeOf(err) == errs.CodeSuppressionListKindTaken || errs.CodeOf(err) == errs.CodeSuppressionListNameTaken {
			return s.Repo.getListByKind(ctx, org, kind)
		}
		return nil, err
	}
	return l, nil
}

// AddUnsubscribe records an unsubscribe (public /u handler; no tenancy).
// mailingListID scopes the entry to one List-Id (W53); empty = global.
func (s *Service) AddUnsubscribe(ctx context.Context, org, address, reason, mailingListID string) error {
	addr, err := normalizeAddress(address)
	if err != nil {
		return err
	}
	mailingListID = normalizeMailingListID(mailingListID)
	l, err := s.EnsureSystemList(ctx, org, KindUnsubscribe)
	if err != nil {
		return err
	}
	e := &Entry{
		ID: entryIDScoped(l.ID, addr, mailingListID), ListID: l.ID, OrganizationID: org,
		Address: addr, Reason: reason, Source: SourceUnsubscribe, At: s.now(),
		MailingListID: mailingListID,
	}
	_, err = s.Repo.upsertEntry(ctx, e)
	return err
}

// AddBounce records a hard bounce (P5.4 hook).
func (s *Service) AddBounce(ctx context.Context, org, address, reason string) error {
	return s.addSystem(ctx, org, KindBounce, SourceBounce, address, reason)
}

// AddComplaint records a complaint (P5.4 hook).
func (s *Service) AddComplaint(ctx context.Context, org, address, reason string) error {
	return s.addSystem(ctx, org, KindComplaint, SourceComplaint, address, reason)
}

func (s *Service) addSystem(ctx context.Context, org, kind, source, address, reason string) error {
	addr, err := normalizeAddress(address)
	if err != nil {
		return err
	}
	l, err := s.EnsureSystemList(ctx, org, kind)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(reason) > maxReason {
		reason = string([]rune(reason)[:maxReason])
	}
	e := &Entry{
		ID: entryID(l.ID, addr), ListID: l.ID, OrganizationID: org,
		Address: addr, Reason: reason, Source: source, At: s.now(),
	}
	_, err = s.Repo.upsertEntry(ctx, e)
	return err
}

func entryID(listID, address string) string {
	return listID + ":" + address
}

func entryIDScoped(listID, address, mailingListID string) string {
	if mailingListID == "" {
		return entryID(listID, address)
	}
	return listID + ":" + address + ":" + mailingListID
}

func normalizeMailingListID(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "<>")
	if len(raw) > 200 {
		raw = raw[:200]
	}
	return raw
}

func systemName(kind string) string {
	switch kind {
	case KindUnsubscribe:
		return "Unsubscribes"
	case KindBounce:
		return "Hard bounces"
	case KindComplaint:
		return "Complaints"
	default:
		return kind
	}
}

func validKind(k string) bool {
	switch k {
	case KindManual, KindUnsubscribe, KindBounce, KindComplaint:
		return true
	}
	return false
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", errs.New(errs.CodeValidationFailed, "name must be 1 to 128 characters.")
	}
	return name, nil
}

func validateDesc(d string) (string, error) {
	d = strings.TrimSpace(d)
	if utf8.RuneCountInString(d) > maxDescLen {
		return "", errs.New(errs.CodeValidationFailed, "description must be at most 512 characters.")
	}
	return d, nil
}

func normalizeAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errs.New(errs.CodeValidationFailed, "address is required.")
	}
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address == "" {
		return "", errs.New(errs.CodeValidationFailed, "address is not a valid e-mail address.")
	}
	return strings.ToLower(a.Address), nil
}

func uniqueNorm(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		a, err := normalizeAddress(raw)
		if err != nil {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

func checkIfMatch(ifMatch string, version int64) error {
	if ifMatch == "" {
		return nil
	}
	want := fmt.Sprintf(`"v%d"`, version)
	if strings.TrimSpace(ifMatch) != want {
		return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
	}
	return nil
}

func formatETag(v int64) string { return fmt.Sprintf(`"v%d"`, v) }
