package sandbox

import (
	"context"
	"io"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

// Objects is the S3 surface the gate and API need (capture copy + raw download + retention delete).
type Objects interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, meta objstore.ObjectMeta) error
	Get(ctx context.Context, key string) (io.ReadCloser, objstore.ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}

// Gate implements submit.SandboxGate (S6 §7).
type Gate struct {
	DB       *store.DB
	Objects  Objects
	Events   *events.Writer
	Settings *settings.Service // optional; without it switches are read from settings docs directly
	Registry *features.Registry
	// Env is EB_ENV (sandbox defaults on when not "production").
	Env string
	// Retention is EB_RETENTION_SANDBOX (default 14d); sets expireAt on captures.
	Retention time.Duration
	Log       *slog.Logger
	Now       func() time.Time
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *Gate) log() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

func (g *Gate) registry() *features.Registry {
	if g.Registry != nil {
		return g.Registry
	}
	return features.DefaultRegistry()
}

func (g *Gate) env() string {
	if g.Env != "" {
		return g.Env
	}
	return "production"
}

func (g *Gate) retention() time.Duration {
	if g.Retention > 0 {
		return g.Retention
	}
	return 14 * 24 * time.Hour
}

// Gate runs before the SMTP session (S6 §7).
func (g *Gate) Gate(ctx context.Context, m *submit.Message, raw func() (io.ReadCloser, error)) (handled bool, rewriteTo []string, err error) {
	d, err := g.resolve(ctx, m.OrganizationID, m.AccountID)
	if err != nil {
		return false, nil, err
	}
	if !d.Enabled || d.Policy == PolicyAllow || d.Policy == "" {
		return false, nil, nil
	}
	switch d.Policy {
	case PolicyRewrite:
		addr, err := validateRewriteTo(d.RewriteTo)
		if err != nil {
			return false, nil, err
		}
		g.log().InfoContext(ctx, "sandbox rewrite", "outboundId", m.ID, "rewriteTo", addr)
		return false, []string{addr}, nil
	case PolicyDrop:
		return true, nil, g.finalize(ctx, m, PolicyDrop, "", nil)
	case PolicyCapture:
		return true, nil, g.capture(ctx, m, raw)
	default:
		return false, nil, errs.New(errs.CodeValidationFailed, "Unknown sandbox.recipientPolicy.").WithSafe("policy", d.Policy)
	}
}

func (g *Gate) capture(ctx context.Context, m *submit.Message, raw func() (io.ReadCloser, error)) error {
	capID := store.NewID(store.PrefixCapture)
	if m.System {
		// System mail holds login codes and reset links, and every member can download captures: keep the
		// envelope and subject only (S10 §7).
		g.log().InfoContext(ctx, "sandbox captured system mail without its body", "outboundId", m.ID)
		return g.finalize(ctx, m, PolicyCapture, capID, &Captured{
			ID: capID, OrganizationID: m.OrganizationID, AccountID: m.AccountID, OutboundID: m.ID,
			Envelope: m.Envelope, Subject: m.Subject,
			CapturedAt: g.now(), ExpireAt: g.now().Add(g.retention()),
		})
	}
	body, err := raw()
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	key, err := objstore.SandboxKey(m.OrganizationID, capID)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "sandbox.key")
	}
	if err := g.Objects.Put(ctx, key, body, m.Size, objstore.ObjectMeta{
		Organization: m.OrganizationID, Account: m.AccountID, ContentType: "message/rfc822",
	}); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "sandbox.put")
	}
	if err := g.finalize(ctx, m, PolicyCapture, capID, &Captured{
		ID: capID, OrganizationID: m.OrganizationID, AccountID: m.AccountID, OutboundID: m.ID,
		Envelope: m.Envelope, Subject: m.Subject, RawRef: key,
		CapturedAt: g.now(), ExpireAt: g.now().Add(g.retention()),
	}); err != nil {
		_ = g.Objects.Delete(ctx, key)
		return err
	}
	return nil
}

// finalize writes outbound state=captured + optional capturedMessages row + message.captured in one tx.
func (g *Gate) finalize(ctx context.Context, m *submit.Message, policy, captureID string, row *Captured) error {
	now := g.now()
	ref := eventtypes.OutboundRef{
		AccountRef:      eventtypes.AccountRef{AccountID: m.AccountID},
		OutboundID:      m.ID,
		MessageIDHeader: m.MessageIDHeader,
		Envelope:        eventtypes.Envelope{From: m.Envelope.From, To: append([]string(nil), m.Envelope.To...)},
		Subject:         m.Subject,
	}
	if ref.Envelope.To == nil {
		ref.Envelope.To = []string{}
	}
	return g.DB.WithTx(ctx, func(txCtx context.Context) error {
		res, err := g.DB.Collection(submit.Collection).UpdateOne(txCtx,
			bson.M{"_id": m.ID, "organizationId": m.OrganizationID, "state": submit.StateSending},
			bson.M{
				"$set":   bson.M{"state": submit.StateCaptured},
				"$inc":   bson.M{"version": 1},
				"$unset": bson.M{"worker": "", "heartbeatAt": "", "nextAttemptAt": ""},
			})
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "sandbox.outbound")
		}
		if res.MatchedCount == 0 {
			return errs.New(errs.CodeConflict, "The outbound message is no longer sending.")
		}
		if row != nil {
			if _, err := g.DB.Collection(Collection).InsertOne(txCtx, row); err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "sandbox.insert")
			}
		}
		_, err = g.Events.Emit(txCtx, events.Draft{
			Type: events.MessageCaptured, OrganizationID: m.OrganizationID, AccountID: m.AccountID,
			Subject: m.ID, Time: now,
			Data: eventtypes.MessageCaptured{OutboundRef: ref, CaptureID: captureID, Policy: policy},
		})
		return err
	})
}

var _ submit.SandboxGate = (*Gate)(nil)
