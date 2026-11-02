package tracking

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
)

// Service prepares tracked MIME and handles public hits.
type Service struct {
	Repo      *Repo
	SignKey   []byte
	PublicURL string
	TTL       time.Duration
	Events    *events.Writer
	Settings  *settings.Service
	Registry  *features.Registry
	Env       string
	// Unsub adds the address to the org unsubscribe list (wired to suppression.Service).
	// mailingListID scopes the entry when the outbound carried a List-Id (W53).
	Unsub func(ctx context.Context, org, address, reason, mailingListID string) error
	// AccountOf resolves accountId for an outbound message (optional; empty ok).
	AccountOf func(ctx context.Context, org, outboundID string) (accountID string, externalID *string)
	Log       *slog.Logger
	Now       func() time.Time
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

func (s *Service) ttl() time.Duration { return clampTTL(s.TTL) }

// TrackState is the resolved tracking switches plus org lock flags (W53).
type TrackState struct {
	Opens, Clicks, Unsub      bool
	OpensLocked, ClicksLocked bool
}

// SwitchState resolves opens/clicks/listUnsub for org+account.
func (s *Service) SwitchState(ctx context.Context, org, accountID string) (opens, clicks, unsub bool) {
	st := s.TrackState(ctx, org, accountID)
	return st.Opens, st.Clicks, st.Unsub
}

// TrackState resolves switches and whether the org locked opens/clicks.
func (s *Service) TrackState(ctx context.Context, org, accountID string) TrackState {
	opens, opensLocked := s.switchDetail(ctx, org, accountID, "tracking.opens")
	clicks, clicksLocked := s.switchDetail(ctx, org, accountID, "tracking.clicks")
	unsub, _ := s.switchDetail(ctx, org, accountID, "sending.listUnsubscribe")
	return TrackState{
		Opens: opens, Clicks: clicks, Unsub: unsub,
		OpensLocked: opensLocked, ClicksLocked: clicksLocked,
	}
}

func (s *Service) switchDetail(ctx context.Context, org, accountID, name string) (on, locked bool) {
	sw, ok := s.registry().Get(name)
	if !ok {
		return false, false
	}
	vals := features.Values{}
	if s.Repo != nil && s.Repo.DB != nil {
		var gdoc settings.Doc
		_ = s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.DocIDGlobal}).Decode(&gdoc)
		if gdoc.Switches != nil {
			if sv, ok := gdoc.Switches[name]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Global = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
					if sv.Locked {
						locked = true
					}
				}
			}
		}
		var doc settings.Doc
		_ = s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(org)}).Decode(&doc)
		if doc.Switches != nil {
			if sv, ok := doc.Switches[name]; ok {
				if b, ok := sv.Value.(bool); ok {
					vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
					if sv.Locked {
						locked = true
					}
				}
			}
		}
		if accountID != "" {
			var acc struct {
				Switches map[string]any `bson:"switches"`
			}
			_ = s.Repo.DB.Collection("accounts").FindOne(ctx, bson.M{"_id": accountID, "organizationId": org}).Decode(&acc)
			if acc.Switches != nil {
				if b, ok := acc.Switches[name].(bool); ok {
					vals.Lower = &features.Setting{Value: features.BoolValue(b)}
				}
			}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, src := features.Resolve(sw, env, vals)
	if src == features.SourceLockedGlobal {
		locked = true
	}
	return v.Bool != nil && *v.Bool, locked
}

// EffectiveTracking applies per-message overrides (may only turn off; locked ignores override).
func EffectiveTracking(st TrackState, trackOpens, trackClicks *bool) (opens, clicks bool) {
	opens, clicks = st.Opens, st.Clicks
	if trackOpens != nil && !st.OpensLocked && !*trackOpens {
		opens = false
	}
	if trackClicks != nil && !st.ClicksLocked && !*trackClicks {
		clicks = false
	}
	return opens, clicks
}

// Apply rewrites HTML/headers and persists click links. No-op when all switches off and no List-Id.
func (s *Service) Apply(ctx context.Context, in PrepareInput) (*PrepareResult, error) {
	if !in.TrackOpens && !in.TrackClicks && !in.ListUnsub && strings.TrimSpace(in.ListID) == "" {
		return &PrepareResult{HTML: in.HTML, Headers: copyHeaders(in.Headers)}, nil
	}
	if (in.TrackOpens || in.TrackClicks || in.ListUnsub) && strings.TrimRight(s.PublicURL, "/") == "" && in.PublicURL == "" {
		return nil, errs.New(errs.CodeValidationFailed, "EB_PUBLIC_URL is required when tracking or List-Unsubscribe is enabled.")
	}
	if in.PublicURL == "" {
		in.PublicURL = s.PublicURL
	}
	if in.ExpireAt.IsZero() {
		in.ExpireAt = s.now().Add(s.ttl())
	}
	res, err := Prepare(s.SignKey, in)
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, err.Error())
	}
	if len(res.Links) > 0 && s.Repo != nil {
		docs := make([]LinkDoc, len(res.Links))
		for i, l := range res.Links {
			docs[i] = LinkDoc{
				ID:             store.NewID(store.PrefixOutbound) + ":lnk:" + itoa(l.Idx),
				OrganizationID: l.OrganizationID, OutboundID: l.OutboundID, AccountID: in.AccountID,
				Idx: l.Idx, URL: l.URL, Hash: l.Hash, ExpireAt: l.ExpireAt,
			}
		}
		if err := s.Repo.insertLinks(ctx, docs); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// HandleOpen serves the pixel and emits tracking.opened.
func (s *Service) HandleOpen(w http.ResponseWriter, r *http.Request, token string) {
	c, err := Verify(s.SignKey, token, s.now())
	if err != nil {
		httpapi.WriteProblem(w, r, mapTokenErr(err))
		return
	}
	if c.Kind != KindOpen {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeSignedURLInvalid, "The tracking token is invalid."))
		return
	}
	s.record(r, c, "", KindOpen)
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pixelGIF)
}

// HandleClick redirects to the stored URL and emits tracking.clicked.
func (s *Service) HandleClick(w http.ResponseWriter, r *http.Request, token string) {
	c, err := Verify(s.SignKey, token, s.now())
	if err != nil {
		httpapi.WriteProblem(w, r, mapTokenErr(err))
		return
	}
	if c.Kind != KindClick {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeSignedURLInvalid, "The tracking token is invalid."))
		return
	}
	if s.Repo == nil {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeNotFound, "The tracked link does not exist."))
		return
	}
	link, err := s.Repo.getLink(r.Context(), c.OrgID, c.OutboundID, c.Index)
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	s.record(r, c, link.URL, KindClick)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, link.URL, http.StatusFound)
}

// HandleUnsubGET serves a minimal confirmation page.
func (s *Service) HandleUnsubGET(w http.ResponseWriter, r *http.Request, token string) {
	c, err := Verify(s.SignKey, token, s.now())
	if err != nil {
		httpapi.WriteProblem(w, r, mapTokenErr(err))
		return
	}
	if c.Kind != KindUnsub {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeSignedURLInvalid, "The tracking token is invalid."))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: Accept-Language mapped to a fixed code; recipient HTML-escaped in renderUnsubConfirm
	_, _ = w.Write([]byte(renderUnsubConfirm(r.Header.Get("Accept-Language"), c.Recipient)))
}

// HandleUnsubPOST performs RFC 8058 one-click unsubscribe.
func (s *Service) HandleUnsubPOST(w http.ResponseWriter, r *http.Request, token string) {
	c, err := Verify(s.SignKey, token, s.now())
	if err != nil {
		httpapi.WriteProblem(w, r, mapTokenErr(err))
		return
	}
	if c.Kind != KindUnsub {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeSignedURLInvalid, "The tracking token is invalid."))
		return
	}
	_ = r.ParseForm()
	mailingListID := ""
	if s.Repo != nil {
		mailingListID = s.Repo.outboundListID(r.Context(), c.OrgID, c.OutboundID)
	}
	if s.Unsub != nil {
		if err := s.Unsub(r.Context(), c.OrgID, c.Recipient, "list-unsubscribe", mailingListID); err != nil {
			httpapi.WriteProblem(w, r, err)
			return
		}
	}
	s.record(r, c, "", KindUnsub)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: Accept-Language mapped to a fixed code; recipient HTML-escaped in renderUnsubDone
	_, _ = w.Write([]byte(renderUnsubDone(r.Header.Get("Accept-Language"), c.Recipient)))
}

func (s *Service) record(r *http.Request, c Claims, destURL, kind string) {
	bot := LikelyBot(r)
	ip := TruncateIP(httpapi.ClientIP(r.Context()))
	ua := r.Header.Get("User-Agent")
	if len(ua) > 512 {
		ua = ua[:512]
	}
	accID := ""
	var ext *string
	if s.AccountOf != nil {
		accID, ext = s.AccountOf(r.Context(), c.OrgID, c.OutboundID)
	}
	now := s.now()
	if s.Repo != nil {
		_ = s.Repo.insertEvent(r.Context(), &EventDoc{
			ID: store.NewID(store.PrefixEvent), OrganizationID: c.OrgID, AccountID: accID,
			OutboundID: c.OutboundID, Kind: kind, Recipient: c.Recipient, URL: destURL,
			IP: ip, UserAgent: ua, Bot: bot, At: now, ExpireAt: now.Add(s.ttl()),
		})
	}
	if s.Events == nil {
		return
	}
	var draft events.Draft
	switch kind {
	case KindOpen:
		draft = events.Draft{
			Type: events.TrackingOpened, OrganizationID: c.OrgID, AccountID: accID,
			Subject: c.OutboundID,
			Data: types.TrackingOpened{
				AccountRef: types.AccountRef{AccountID: accID, ExternalID: ext},
				OutboundID: c.OutboundID, Recipient: c.Recipient, IP: ip, UserAgent: ua, Bot: bot,
			},
		}
	case KindClick:
		draft = events.Draft{
			Type: events.TrackingClicked, OrganizationID: c.OrgID, AccountID: accID,
			Subject: c.OutboundID,
			Data: types.TrackingClicked{
				AccountRef: types.AccountRef{AccountID: accID, ExternalID: ext},
				OutboundID: c.OutboundID, Recipient: c.Recipient, URL: destURL,
				IP: ip, UserAgent: ua, Bot: bot,
			},
		}
	case KindUnsub:
		draft = events.Draft{
			Type: events.ListUnsubscribed, OrganizationID: c.OrgID, AccountID: accID,
			Subject: c.OutboundID,
			Data: types.ListUnsubscribed{
				AccountRef: types.AccountRef{AccountID: accID, ExternalID: ext},
				OutboundID: c.OutboundID, Recipient: c.Recipient, IP: ip, UserAgent: ua, Bot: bot,
			},
		}
	default:
		return
	}
	if _, err := s.Events.Emit(r.Context(), draft); err != nil && s.Log != nil {
		s.Log.WarnContext(r.Context(), "tracking event emit failed", "error", err, "kind", kind)
	}
}

func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// 1x1 transparent GIF
var pixelGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0xff, 0xff, 0xff,
	0x00, 0x00, 0x00, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}
