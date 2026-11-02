package pecreceipts

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const switchPEC = "pec.enabled"

// RawSource streams a receipt's signed RFC 822. Implementations prefer evidence when the receipt
// has evidence.key (W41); otherwise they fall back to the message raw (IMAP / archive).
type RawSource interface {
	Open(ctx context.Context, rc *Receipt) (io.ReadCloser, error)
}

// MessageOpener opens the indexed message raw (archive / on-demand IMAP).
type MessageOpener interface {
	Open(ctx context.Context, accountID, messageID string) (io.ReadCloser, error)
}

// EvidenceOpener opens an evidence object by key (W41). Optional until evidence lands.
type EvidenceOpener interface {
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// CompositeRaw is the default RawSource: evidence first when present, else message raw.
type CompositeRaw struct {
	Message  MessageOpener
	Evidence EvidenceOpener // nil until W41 wiring
}

// Open implements RawSource.
func (c CompositeRaw) Open(ctx context.Context, rc *Receipt) (io.ReadCloser, error) {
	if rc.Evidence != nil && rc.Evidence.Key != "" && c.Evidence != nil {
		return c.Evidence.Open(ctx, rc.Evidence.Key)
	}
	if c.Message == nil {
		return nil, errs.New(errs.CodeInternalError, "Raw receipt source is not configured.")
	}
	return c.Message.Open(ctx, rc.AccountID, rc.MessageID)
}

// EnabledFn reports whether pec.enabled is on for the account.
type EnabledFn func(ctx context.Context, accountID string) (bool, error)

// Service is the PEC receipts HTTP API.
type Service struct {
	Repo     *Repo
	Raw      RawSource
	Enabled  EnabledFn
	Registry *features.Registry
	Env      string
	Evidence EvidenceVerifier // P9.2; nil = no evidence archive on this node
}

func (s *Service) register(srv *httpapi.Server) {
	s.registerEvidence(srv)
	ext := httpapi.Phase("P9")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listAccountPECReceipts", Method: http.MethodGet,
		Path:    "/v1/accounts/{accountId}/pec/receipts",
		Summary: "List PEC receipts for an account",
		Tags:    []string{"pec"},
		Description: "Lists the PEC receipts (ricevute) the account received: acceptance, delivery, error and virus " +
			"receipts parsed from daticert.xml, with the S/MIME signature check and, when `pec.evidenceArchive` was on, " +
			"the evidence archive reference. Receipts about a message EmailBridge sent carry its `outboundId`; filter " +
			"with `outboundId` to see one message's trail, with `type`, or with `q` (prefix of the transport ID or of " +
			"`deliveredTo`). Needs the `pec.enabled` switch on the account (`403 feature_disabled`). Requires the " +
			"`read` permission (API keys `messages:read` or `messages:read:<accountId>`). Pagination: `limit` " +
			"(default 50, max 200) with `cursor`, or `page` with `includeTotal`; sort `at` or `type` (default `-at`). " +
			"Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "PEC is disabled for this account."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The account does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "outboundId is not a valid outbound id."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleReceiptList())}},
		Extensions: httpapi.WithScope(ext, "messages:read"),
	}, s.list)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getPECReceipt", Method: http.MethodGet,
		Path:    "/v1/pec/receipts/{receiptId}",
		Summary: "Get one PEC receipt",
		Tags:    []string{"pec"},
		Description: "Returns one PEC receipt with its parsed daticert.xml fields, the S/MIME signature check, the " +
			"SHA-256 of the signed original and the evidence archive reference. A receipt of another organisation " +
			"is `404 not_found`. Needs `pec.enabled` on the receipt's account. Requires the `read` permission (API " +
			"keys `messages:read` or `messages:read:<accountId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "PEC is disabled for this account."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The PEC receipt does not exist."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleDeliveryReceipt())}},
		Extensions: httpapi.WithScope(ext, "messages:read"),
	}, s.get)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getPECReceiptRaw", Method: http.MethodGet,
		Path:    "/v1/pec/receipts/{receiptId}/raw",
		Summary: "Download the signed raw PEC receipt",
		Tags:    []string{"pec"},
		Description: "Streams the receipt exactly as the PEC provider signed it (RFC 822, `message/rfc822`, " +
			"`<receiptId>.eml`): from the evidence archive when the receipt was archived, otherwise from the message " +
			"archive or IMAP. The `Digest` header carries the SHA-256 stored at arrival, so the file can be checked " +
			"against `sha256`. Needs `pec.enabled` on the receipt's account. Requires the `read` permission (API keys " +
			"`messages:read` or `messages:read:<accountId>`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "PEC is disabled for this account."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The PEC receipt does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The message archive is temporarily unavailable; retry later."}),
		Extensions: httpapi.WithScope(ext, "messages:read"),
		Responses: map[string]*huma.Response{"200": {Description: "Raw receipt",
			Headers: map[string]*huma.Param{
				"Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString},
					Description: "Download file name: `<receiptId>.eml`.", Example: `attachment; filename="` + exampleReceiptID + `.eml"`},
				"Digest": {Schema: &huma.Schema{Type: huma.TypeString},
					Description: "RFC 3230 digest of the body: base64 SHA-256 stored when the receipt arrived.",
					Example:     "sha-256=n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg="},
			},
			Content: map[string]*huma.MediaType{"message/rfc822": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"},
				Example: exampleRawReceipt}}}},
	}, s.getRaw)
}

type listIn struct {
	AccountID    string `path:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Account ID (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6InJjcF8wMW01ZDIweTgwbjRwczBhcHozZTZiYXFxMyIsImZpZWxkIjoiYXQiLCJkZXNjIjp0cnVlfQ"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"at or type; prefix - for descending. Default -at." example:"-at"`
	Type         string `query:"type" doc:"Only receipts of this type, e.g. avvenuta-consegna." example:"avvenuta-consegna"`
	OutboundID   string `query:"outboundId" doc:"Only receipts about this outbound message (out_…)." example:"out_01m5cqe6d02twj9xw9b9vyh5b4"`
	Q            string `query:"q" doc:"Case-insensitive prefix of the transport ID or of deliveredTo (at most 200 characters)." example:"studio.rossi"`
}

type listOut struct {
	Body List
}

type receiptIDIn struct {
	ReceiptID string `path:"receiptId" pattern:"^rcp_[0-9a-z]{26}$" doc:"PEC receipt ID (rcp_…)." example:"rcp_01m5d20y80n4ps0apz3e6baqq3"`
}

type getOut struct {
	Body Receipt
}

func (s *Service) list(ctx context.Context, in *listIn) (*listOut, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", in.AccountID); err != nil {
		return nil, err
	}
	if err := s.requirePEC(ctx, in.AccountID); err != nil {
		return nil, err
	}
	res, err := s.Repo.List(ctx, in.AccountID, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Type: in.Type, OutboundID: in.OutboundID, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (s *Service) get(ctx context.Context, in *receiptIDIn) (*getOut, error) {
	rc, err := s.loadForRead(ctx, in.ReceiptID)
	if err != nil {
		return nil, err
	}
	return &getOut{Body: *rc}, nil
}

func (s *Service) getRaw(ctx context.Context, in *receiptIDIn) (*huma.StreamResponse, error) {
	rc, err := s.loadForRead(ctx, in.ReceiptID)
	if err != nil {
		return nil, err
	}
	if s.Raw == nil {
		return nil, errs.New(errs.CodeInternalError, "Raw receipt source is not configured.")
	}
	reader, err := s.Raw.Open(ctx, rc)
	if err != nil {
		return nil, err
	}
	digest := digestHeader(rc.SHA256)
	id := rc.ID
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		writeRaw(hctx, id, digest, reader)
	}}, nil
}

type headerWriter interface {
	SetHeader(name, value string)
	BodyWriter() io.Writer
}

func writeRaw(h headerWriter, id, digest string, reader io.ReadCloser) {
	defer func() { _ = reader.Close() }()
	h.SetHeader("Content-Type", "message/rfc822")
	h.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.eml"`, id))
	if digest != "" {
		h.SetHeader("Digest", digest)
	}
	_, _ = io.Copy(h.BodyWriter(), reader)
}

func (s *Service) loadForRead(ctx context.Context, id string) (*Receipt, error) {
	if !store.IsID(store.PrefixPECReceipt, id) {
		return nil, errs.New(errs.CodeNotFound, "The PEC receipt does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	rc, err := s.Repo.Get(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if err := tenancy.RequireReadAccount(ctx, "messages", rc.AccountID); err != nil {
		return nil, err
	}
	if err := s.requirePEC(ctx, rc.AccountID); err != nil {
		return nil, err
	}
	return rc, nil
}

func (s *Service) requirePEC(ctx context.Context, accountID string) error {
	ok, err := s.pecEnabled(ctx, accountID)
	if err != nil {
		return err
	}
	if !ok {
		return errs.New(errs.CodeFeatureDisabled, "PEC is disabled for this account.")
	}
	return nil
}

func (s *Service) pecEnabled(ctx context.Context, accountID string) (bool, error) {
	if s.Enabled != nil {
		return s.Enabled(ctx, accountID)
	}
	return s.resolveEnabled(ctx, accountID)
}

// resolveEnabled reads pec.enabled (org settings + account.switches), default off.
func (s *Service) resolveEnabled(ctx context.Context, accountID string) (bool, error) {
	reg := s.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	sw, ok := reg.Get(switchPEC)
	if !ok {
		return false, nil
	}
	vals := features.Values{}
	var acc struct {
		OrganizationID string         `bson:"organizationId"`
		Switches       map[string]any `bson:"switches"`
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": accountID})
	if err != nil {
		return false, err
	}
	err = s.Repo.DB.Collection("accounts").FindOne(ctx, filter).Decode(&acc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternalError, "pecreceipts.feature")
	}
	if acc.Switches != nil {
		if v, ok := acc.Switches[switchPEC].(bool); ok {
			vals.Lower = &features.Setting{Value: features.BoolValue(v)}
		}
	}
	var doc settings.Doc
	_ = s.Repo.DB.Collection(settings.SettingsCollection).FindOne(ctx, bson.M{"_id": settings.OrgDocID(acc.OrganizationID)}).Decode(&doc)
	if doc.Switches != nil {
		if sv, ok := doc.Switches[switchPEC]; ok {
			if b, ok := sv.Value.(bool); ok {
				vals.Organization = &features.Setting{Value: features.BoolValue(b), Locked: sv.Locked}
			}
		}
	}
	env := s.Env
	if env == "" {
		env = "production"
	}
	v, _ := features.Resolve(sw, env, vals)
	return v.Bool != nil && *v.Bool, nil
}

func digestHeader(shaHex string) string {
	b, err := hex.DecodeString(shaHex)
	if err != nil || len(b) == 0 {
		return ""
	}
	return "sha-256=" + base64.StdEncoding.EncodeToString(b)
}
