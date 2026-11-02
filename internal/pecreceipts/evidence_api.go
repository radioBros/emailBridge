package pecreceipts

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// EvidenceVerifier checks archived evidence (P9.2, S17 §9).
type EvidenceVerifier interface {
	Verify(ctx context.Context, ref evidence.Ref, expectedSHA256 string) (evidence.ArchiveCheck, error)
}

// PECEvidenceView is GET /v1/pec/receipts/{receiptId}/evidence.
type PECEvidenceView struct {
	ReceiptID   string                `json:"receiptId" doc:"Receipt ID (rcp_…)" example:"rcp_01m5d20y80n4ps0apz3e6baqq3"`
	Key         string                `json:"key" doc:"Object key of the archived receipt" example:"evidence/org_01m5caqpk0kast63yc13wpy2d9/acc_01m5cvnpb031g57sqct6mzhq94/2026/10/rcp_01m5d20y80n4ps0apz3e6baqq3.eml"`
	SHA256      string                `json:"sha256" doc:"SHA-256 of the receipt as stored at arrival (hex)" example:"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"`
	Verified    evidence.ArchiveCheck `json:"verification" doc:"Result of the checks"`
	ManifestKey string                `json:"manifestKey" doc:"Object key of the manifest" example:"evidence/org_01m5caqpk0kast63yc13wpy2d9/acc_01m5cvnpb031g57sqct6mzhq94/2026/10/rcp_01m5d20y80n4ps0apz3e6baqq3.json"`
}

type evidenceOut struct {
	Body PECEvidenceView
}

func (s *Service) registerEvidence(srv *httpapi.Server) {
	httpapi.Register(srv, huma.Operation{
		OperationID: "verifyPECReceiptEvidence", Method: http.MethodGet,
		Path:    "/v1/pec/receipts/{receiptId}/evidence",
		Summary: "Verify the archived evidence of a PEC receipt",
		Description: "Re-hashes the archived object, compares it with the manifest and the receipt, checks the Object Lock " +
			"retention and the RFC 3161 token. `verification.intact` is true only when every check passes; a " +
			"tampered or replaced object answers `200` with `intact: false` and `hashMatches: false`. `404 not_found` " +
			"when the receipt has no archived evidence (`pec.evidenceArchive` was off when it arrived) or its manifest " +
			"is missing; `503 service_unavailable` when this node has no evidence archive. Needs `pec.enabled` on the " +
			"receipt's account. Requires the `read` permission (API keys `messages:read` or " +
			"`messages:read:<accountId>`). Read-only (nothing is rewritten).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "PEC is disabled for this account."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The receipt has no archived evidence (pec.evidenceArchive was off when it arrived)."},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "The evidence archive is not configured on this node."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleEvidenceView())}},
		Tags:       []string{"pec"},
		Extensions: httpapi.WithScope(httpapi.Phase("P9"), "messages:read"),
	}, s.verifyEvidence)
}

func (s *Service) verifyEvidence(ctx context.Context, in *receiptIDIn) (*evidenceOut, error) {
	rc, err := s.loadForRead(ctx, in.ReceiptID)
	if err != nil {
		return nil, err
	}
	if rc.Evidence == nil || rc.Evidence.Key == "" {
		return nil, errs.New(errs.CodeNotFound, "The receipt has no archived evidence (pec.evidenceArchive was off when it arrived).")
	}
	if s.Evidence == nil {
		return nil, errs.New(errs.CodeServiceUnavailable, "The evidence archive is not configured on this node.")
	}
	ref := evidence.Ref{Key: rc.Evidence.Key, ManifestKey: evidence.ManifestKeyFor(rc.Evidence.Key), SHA256: rc.SHA256}
	if rc.Evidence.TSA {
		ref.TSRKey, ref.TSA = evidence.TSRKeyFor(rc.Evidence.Key), true
	}
	res, err := s.Evidence.Verify(ctx, ref, rc.SHA256)
	if err != nil {
		return nil, err
	}
	return &evidenceOut{Body: PECEvidenceView{ReceiptID: rc.ID, Key: ref.Key, SHA256: rc.SHA256, Verified: res, ManifestKey: ref.ManifestKey}}, nil
}
