package integrations

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
)

func (s *Service) notifySentry(ctx context.Context, typ string, cfg map[string]any, secrets map[string]string, enabled bool) {
	if s == nil || s.OnSentryChange == nil || typ != connectors.TypeSentry {
		return
	}
	rt := connectors.SentryRuntimeFrom(cfg, secrets, enabled)
	s.OnSentryChange(ctx, rt.Enabled, rt.DSN, rt.Environment, rt.SampleRate)
}

// ActiveSentry returns the runtime of the enabled Sentry connector (W37), installed only by instance owners,
// so at most one is expected; with several, the oldest wins. Every node calls it at start and periodically,
// so an install on one node reaches the reporters of all nodes.
func (s *Service) ActiveSentry(ctx context.Context) (connectors.SentryRuntime, error) {
	var inst Instance
	err := s.Repo.coll().FindOne(ctx, bson.M{"type": connectors.TypeSentry, "enabled": true},
		options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: 1}})).Decode(&inst)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return connectors.SentryRuntime{}, nil
	}
	if err != nil {
		return connectors.SentryRuntime{}, errs.Wrap(err, errs.CodeInternalError, "integrations.activeSentry")
	}
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return connectors.SentryRuntime{}, nil
	}
	secrets, err := s.decryptSecrets(store.WithOrganization(ctx, inst.OrganizationID), &inst, def)
	if err != nil {
		return connectors.SentryRuntime{}, err
	}
	return connectors.SentryRuntimeFrom(inst.Config, secrets, true), nil
}
