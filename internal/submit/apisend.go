package submit

import (
	"context"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/httpsend"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// sendAPI sends through an API gateway (P8.3). The provider receives the transmitted raw copy and the
// envelope explicitly (To, Cc, Bcc), so blind copies are delivered without a Bcc header; a sandbox rewrite
// replaces both the envelope and the recipient headers. Failures go through the same retry policy as SMTP:
// provider verdicts keep their classification, and a request that may have reached the provider (timeout,
// reset) is an unknown outcome — `atMostOnce` never resends it.
func (s *Service) sendAPI(ctx context.Context, m *Message, acc *SMTPAccount, body io.ReadCloser, rewriteTo []string, started time.Time, idx int) error {
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, 50<<20))
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, errs.Wrap(err, errs.CodeInternalError, "submit.api.read"))
	}
	mail := APIMail{From: m.Envelope.From, To: m.Envelope.To, Cc: m.Envelope.Cc, Bcc: m.Bcc, Raw: raw,
		OutboundID: m.ID, MessageID: m.MessageIDHeader}
	if len(rewriteTo) > 0 {
		mail.To, mail.Cc, mail.Bcc = rewriteTo, nil, nil
		mail.Raw = headerAddressedRaw(raw, nil, rewriteTo)
	}
	id, err := acc.APISend(ctx, mail)
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, httpsend.Classify(err))
	}
	return s.finish(ctx, m, acc, idx, started, &smtpc.Result{Response: "250 accepted, provider id " + id}, nil)
}
