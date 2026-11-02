package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const maxCallbackBody = 1 << 20 // 1 MiB

// HandleCallback processes POST /integrations/{instanceId}/callback (FeatherDesk inbound).
func (s *Service) HandleCallback(ctx context.Context, instanceID string, r *http.Request) error {
	if s.limiter == nil {
		s.limiter = newCallbackLimiter(60)
	}
	now := time.Now().UTC()
	if err := s.limiter.take(instanceID, now); err != nil {
		return err
	}
	if !store.IsID(store.PrefixIntegration, instanceID) {
		return errs.New(errs.CodeNotFound, "The integration instance does not exist.")
	}
	inst, err := s.Repo.getByID(ctx, instanceID)
	if err != nil {
		return err
	}
	if !inst.Enabled || inst.Type != TypeFeatherDesk {
		return errs.New(errs.CodeNotFound, "The integration instance does not exist.")
	}
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return errs.New(errs.CodeNotFound, "Unknown integration type.")
	}
	secrets, err := s.decryptSecrets(ctx, inst, def)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(secrets["callbackToken"])
	if token == "" {
		return errs.New(errs.CodeUnauthenticated, "Callback is not configured.")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCallbackBody+1))
	if err != nil {
		return errs.Wrap(err, errs.CodeValidationFailed, "integrations.callback.read")
	}
	if len(body) > maxCallbackBody {
		return errs.New(errs.CodeValidationFailed, "Request body too large.")
	}
	ts := r.Header.Get("X-FeatherDesk-Timestamp")
	sig := r.Header.Get("X-FeatherDesk-Signature")
	if !connectors.VerifyFeatherDeskCallback(token, body, ts, sig, now) {
		return errs.New(errs.CodeUnauthenticated, "Invalid callback signature or timestamp.")
	}
	reply, err := connectors.ParseFeatherDeskReply(body)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "Invalid callback body.")
	}
	send, err := connectors.MapReplyToSend(reply)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, err.Error())
	}
	accountID, _ := inst.Config["accountId"].(string)
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = strings.TrimSpace(send.AccountID)
	}
	if accountID == "" || !store.IsID(store.PrefixAccount, accountID) {
		return errs.New(errs.CodeValidationFailed, "accountId is required on the FeatherDesk instance.")
	}
	if s.Submit == nil {
		return errs.New(errs.CodeInternalError, "Outbound submit is not configured.")
	}
	sys := tenancy.WithCaller(store.WithOrganization(ctx, inst.OrganizationID), tenancy.Caller{
		Kind: tenancy.CallerSystem, OrganizationID: inst.OrganizationID,
	})
	return s.Submit.SubmitOutbound(sys, accountID, send.To, send.Subject, send.Text, send.Headers, callbackKey(instanceID, reply.MessageID, body))
}

// callbackKey is the submit idempotency key of a callback: a signed callback stays valid for minutes and
// FeatherDesk retries on timeouts, so the same reply must not send twice. The reply's message ID identifies
// it; without one, the body hash does.
func callbackKey(instanceID, messageID string, body []byte) string {
	id := strings.TrimSpace(messageID)
	if id == "" {
		sum := sha256.Sum256(body)
		id = hex.EncodeToString(sum[:16])
	}
	return "featherdesk:" + instanceID + ":" + id
}
