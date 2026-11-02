package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/integrations/connectors"
	"github.com/radiobros/emailbridge/internal/sinks"
	"github.com/radiobros/emailbridge/internal/store"
)

// AttachmentReader loads attachment bytes for storage actions (read-only).
type AttachmentReader interface {
	GetAttachment(ctx context.Context, accountID, messageID, attachmentID string, maxBytes int64) ([]byte, string, error)
}

// ActionResult is recorded on the instance after a delivery attempt.
type ActionResult struct {
	OK     bool
	Detail string
	Error  string
}

// RunAction executes the connector action for an integration sink delivery (W52).
// orgID is the organisation of the delivering endpoint: an endpoint may only drive its own organisation's
// instances (an endpoint naming another organisation's instance must never use that instance's secrets).
func (s *Service) RunAction(ctx context.Context, orgID, instanceID string, msg sinks.Message) error {
	if !store.IsID(store.PrefixIntegration, instanceID) {
		return errs.New(errs.CodeValidationFailed, "invalid integration instance id")
	}
	inst, err := s.Repo.getByID(ctx, instanceID)
	if err != nil {
		return err
	}
	if orgID == "" || inst.OrganizationID != orgID {
		// Same answer as a missing instance: never confirm that another organisation's instance exists.
		return errs.New(errs.CodeNotFound, "The integration instance does not exist.")
	}
	if !inst.Enabled {
		return errs.New(errs.CodeFeatureDisabled, "integration instance is disabled")
	}
	// Deliveries run outside a request: give the action its organisation (tenant-scoped reads such as the
	// attachment store need it).
	ctx = store.WithOrganization(ctx, orgID)
	def, ok := s.reg().Get(inst.Type)
	if !ok {
		return errs.New(errs.CodeNotFound, "Unknown integration type.").WithSafe("type", inst.Type)
	}
	secrets, err := s.decryptSecrets(ctx, inst, def)
	if err != nil {
		return err
	}
	client := s.client()
	if client == nil {
		// Never fall back to http.DefaultClient: connector calls must go through the SSRF-safe egress dialer.
		return errs.New(errs.CodeInternalError, "integrations: egress HTTP client is not configured")
	}
	payload, err := parseActionPayload(msg)
	if err != nil {
		_ = s.recordAction(ctx, inst, ActionResult{OK: false, Error: err.Error()})
		return errs.Wrap(err, errs.CodeValidationFailed, "integrations.action.payload")
	}
	detail, runErr := s.dispatchAction(ctx, inst, secrets, client, payload)
	res := ActionResult{OK: runErr == nil, Detail: detail}
	if runErr != nil {
		res.Error = truncateErr(runErr.Error(), 240)
	}
	_ = s.recordAction(ctx, inst, res)
	return runErr
}

type actionPayload struct {
	Address, Subject, Body, Direction string
	AccountID, MessageID              string
	Attachments                       []actionAtt
}

type actionAtt struct {
	ID, Name, ContentType string
}

func parseActionPayload(msg sinks.Message) (actionPayload, error) {
	var envelope map[string]any
	if err := json.Unmarshal(msg.Body, &envelope); err != nil {
		return actionPayload{}, fmt.Errorf("invalid event body")
	}
	data, _ := envelope["data"].(map[string]any)
	if data == nil {
		data = envelope
	}
	typ := msg.Type
	if typ == "" {
		typ, _ = envelope["type"].(string)
	}
	out := actionPayload{
		AccountID: firstString(data, "accountId", msg.AccountID),
		MessageID: firstString(data, "messageId"),
		Subject:   firstString(data, "subject"),
		Direction: "in",
	}
	if strings.Contains(typ, "sent") || strings.Contains(typ, "delivered") {
		out.Direction = "out"
	}
	if from, ok := data["from"].(map[string]any); ok {
		out.Address = firstString(from, "address")
	}
	if out.Address == "" {
		if to, ok := data["to"].([]any); ok && len(to) > 0 {
			if m, ok := to[0].(map[string]any); ok {
				out.Address = firstString(m, "address")
			}
		}
	}
	if text, ok := data["text"].(map[string]any); ok {
		out.Body = firstString(text, "plain")
		if out.Body == "" {
			out.Body = firstString(text, "html")
		}
	}
	if atts, ok := data["attachments"].([]any); ok {
		for _, a := range atts {
			m, ok := a.(map[string]any)
			if !ok {
				continue
			}
			out.Attachments = append(out.Attachments, actionAtt{
				ID: firstString(m, "id"), Name: firstString(m, "filename", firstString(m, "name")),
				ContentType: firstString(m, "contentType"),
			})
		}
	}
	return out, nil
}

func (s *Service) dispatchAction(
	ctx context.Context,
	inst *Instance,
	secrets map[string]string,
	client *http.Client,
	p actionPayload,
) (string, error) {
	cfg := inst.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	eng := connectors.MailEngagement{
		Address: p.Address, Subject: p.Subject, Body: truncateErr(p.Body, 4000), Direction: p.Direction,
	}
	switch inst.Type {
	case connectors.TypeHubSpot:
		return connectors.HubSpotLogMail(ctx, client, strCfg(cfg, "baseUrl"), secrets["accessToken"], eng)
	case connectors.TypePipedrive:
		return connectors.PipedriveLogMail(ctx, client, strCfg(cfg, "baseUrl"), secrets["apiToken"], strCfg(cfg, "companyDomain"), eng)
	case connectors.TypeGDrive, connectors.TypeOneDrive:
		return s.runStorageUpload(ctx, inst, secrets, client, p)
	default:
		return "", errs.New(errs.CodeValidationFailed, "connector has no runtime action").
			WithSafe("type", inst.Type)
	}
}

func (s *Service) runStorageUpload(
	ctx context.Context,
	inst *Instance,
	secrets map[string]string,
	client *http.Client,
	p actionPayload,
) (string, error) {
	if len(p.Attachments) == 0 {
		return "", nil // nothing to upload
	}
	if s.Attachments == nil {
		return "", errs.New(errs.CodeInternalError, "attachment reader not configured")
	}
	token, err := connectors.RefreshOAuthToken(ctx, client, connectors.OAuthTokenInput{
		TokenURL:     strCfg(inst.Config, "tokenUrl"),
		ClientID:     strCfg(inst.Config, "clientId"),
		ClientSecret: secrets["clientSecret"],
		RefreshToken: secrets["refreshToken"],
		AccessToken:  secrets["accessToken"],
		Provider:     inst.Type,
	})
	if err != nil {
		return "", err
	}
	var lastID string
	for _, att := range p.Attachments {
		if att.ID == "" || p.AccountID == "" || p.MessageID == "" {
			continue
		}
		data, ct, err := s.Attachments.GetAttachment(ctx, p.AccountID, p.MessageID, att.ID, 25<<20)
		if err != nil {
			return lastID, err
		}
		if ct == "" {
			ct = att.ContentType
		}
		name := att.Name
		if name == "" {
			name = att.ID
		}
		meta := connectors.UploadMeta{
			Name: name, ContentType: ct,
			FolderID: strCfg(inst.Config, "folderId"), DriveID: strCfg(inst.Config, "driveId"),
			FolderPath: strCfg(inst.Config, "folderPath"),
		}
		switch inst.Type {
		case connectors.TypeGDrive:
			lastID, err = connectors.GDriveUpload(ctx, client, strCfg(inst.Config, "baseUrl"), token, meta, data)
		case connectors.TypeOneDrive:
			lastID, err = connectors.OneDriveUpload(ctx, client, strCfg(inst.Config, "baseUrl"), token, meta, data)
		}
		if err != nil {
			return lastID, err
		}
	}
	return lastID, nil
}

func (s *Service) recordAction(ctx context.Context, inst *Instance, res ActionResult) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	inst.LastActionAt = &now
	inst.LastActionOK = res.OK
	inst.LastActionDetail = res.Detail
	inst.LastActionError = res.Error
	inst.UpdatedAt = now
	return s.Repo.patchActionStatus(ctx, inst)
}

func strCfg(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg[key].(string)
	return strings.TrimSpace(v)
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncateErr(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}
