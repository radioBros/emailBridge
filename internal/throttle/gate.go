package throttle

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/rules"
)

const (
	switchThrottle = "sending.throttle"
	switchWindows  = "sending.sendWindows"
	switchPreSend  = "sending.preSendHook"

	ReasonSendWindow  = "send_window"
	ReasonPresendHold = "presend_hold"

	maxDetail = 256
)

// Action is a gate decision for the submit worker.
type Action string

// Gate actions.
const (
	ActionAllow    Action = "allow"
	ActionDelay    Action = "delay"    // throttle → requeue queued
	ActionSchedule Action = "schedule" // send window → scheduled
	ActionHold     Action = "hold"     // pre-send hold → scheduled, no sendAt
	ActionReject   Action = "reject"   // pre-send reject → fail
)

// Decision is returned by Gate methods.
type Decision struct {
	Action  Action
	Delay   time.Duration
	Until   time.Time // for schedule
	Reason  string
	Detail  string
	Problem error // for reject
}

// Message is the minimal outbound view the gate needs (avoids importing submit).
type Message struct {
	OrganizationID string
	AccountID      string
	From           string
	To             []string
	Cc             []string
	Bcc            []string
	BccCount       int
	Size           int64
	Headers        map[string]string
	ExternalID     string
}

// Config holds resolved org settings for one evaluation.
type Config struct {
	ThrottleOn bool
	WindowsOn  bool
	PreSendOn  bool
	Limits     Limits
	Window     *Window
	CEL        string
}

// ConfigFunc loads Config for an organisation (and optional account).
type ConfigFunc func(ctx context.Context, orgID, accountID string) (Config, error)

// Gate evaluates window → throttle → (later) pre-send. PreSend is separate so the worker can
// run sandbox between throttle and policy.
type Gate struct {
	Counter  Counter
	PreSend  *rules.PreSendEngine
	Config   ConfigFunc
	Registry *features.Registry
	Env      string // EB_ENV for feature defaults
	Log      *slog.Logger
	Now      func() time.Time
}

func (g *Gate) now() time.Time {
	if g != nil && g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *Gate) log() *slog.Logger {
	if g != nil && g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

func (g *Gate) registry() *features.Registry {
	if g != nil && g.Registry != nil {
		return g.Registry
	}
	return features.DefaultRegistry()
}

func (g *Gate) load(ctx context.Context, org, account string) Config {
	if g != nil && g.Config != nil {
		c, err := g.Config(ctx, org, account)
		if err == nil {
			return c
		}
		g.log().WarnContext(ctx, "throttle config load failed; using defaults", "error", err)
	}
	return Config{
		ThrottleOn: true,
		Limits:     DefaultLimits(),
	}
}

// BeforeSandbox runs send-window then throttle (S6 §3.2).
func (g *Gate) BeforeSandbox(ctx context.Context, m Message) Decision {
	if g == nil {
		return Decision{Action: ActionAllow}
	}
	cfg := g.load(ctx, m.OrganizationID, m.AccountID)
	now := g.now()

	if cfg.WindowsOn && cfg.Window != nil && !cfg.Window.Contains(now) {
		until := cfg.Window.NextOpen(now)
		return Decision{Action: ActionSchedule, Until: until, Reason: ReasonSendWindow, Detail: "outside send window"}
	}

	if !cfg.ThrottleOn || g.Counter == nil {
		return Decision{Action: ActionAllow}
	}
	addrs := append(append([]string{}, m.To...), m.Cc...)
	addrs = append(addrs, m.Bcc...)
	domains := DomainsFrom(addrs)
	if len(domains) == 0 {
		return Decision{Action: ActionAllow}
	}
	b := &Buckets{Counter: g.Counter, Limits: cfg.Limits, Now: g.Now}
	res, err := b.Take(ctx, m.OrganizationID, domains)
	if err != nil {
		g.log().WarnContext(ctx, "throttle counter error; allowing send", "error", err)
		return Decision{Action: ActionAllow}
	}
	if !res.OK {
		return Decision{Action: ActionDelay, Delay: res.Delay, Reason: "throttled", Detail: "per-domain or provider rate limit"}
	}
	return Decision{Action: ActionAllow}
}

// AfterSandbox runs the pre-send CEL hook.
func (g *Gate) AfterSandbox(ctx context.Context, m Message) Decision {
	if g == nil {
		return Decision{Action: ActionAllow}
	}
	cfg := g.load(ctx, m.OrganizationID, m.AccountID)
	if !cfg.PreSendOn || strings.TrimSpace(cfg.CEL) == "" || g.PreSend == nil {
		return Decision{Action: ActionAllow}
	}
	in := rules.PreSendInput{
		Envelope: map[string]any{
			"from": m.From, "to": m.To, "cc": m.Cc, "bccCount": m.BccCount,
		},
		Headers: m.Headers,
		Size:    m.Size,
		Account: map[string]any{
			"id": m.AccountID, "organizationId": m.OrganizationID, "externalId": m.ExternalID,
		},
		Now: g.now(),
	}
	res, err := g.PreSend.Eval(ctx, cfg.CEL, in)
	if err != nil {
		g.log().WarnContext(ctx, "pre-send CEL failed open", "error", err)
		return Decision{Action: ActionAllow}
	}
	switch res.Action {
	case rules.PreSendHold:
		return Decision{Action: ActionHold, Reason: ReasonPresendHold, Detail: clip(res.Reason)}
	case rules.PreSendReject:
		detail := clip(res.Reason)
		if detail == "" {
			detail = "The pre-send policy rejected this message."
		}
		return Decision{
			Action:  ActionReject,
			Reason:  "presend_rejected",
			Detail:  detail,
			Problem: errs.New(errs.CodePresendRejected, detail),
		}
	default:
		return Decision{Action: ActionAllow}
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxDetail {
		return s
	}
	r := []rune(s)
	return string(r[:maxDetail])
}

// SwitchOn resolves a feature switch with optional org override from cfg flags already decided
// by ConfigFunc. Helper for settings adapters.
func SwitchOn(reg *features.Registry, name, env string, org *bool) bool {
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	sw, ok := reg.Get(name)
	if !ok {
		return true
	}
	vals := features.Values{}
	if org != nil {
		vals.Organization = &features.Setting{Value: features.BoolValue(*org)}
	}
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	if v.Bool != nil {
		return *v.Bool
	}
	return true
}
