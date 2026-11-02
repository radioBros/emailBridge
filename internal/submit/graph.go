package submit

import (
	"context"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/httpsend"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// GraphSender sends raw RFC822 through Microsoft Graph sendMail (P6.3 MIME upload).
type GraphSender interface {
	Send(ctx context.Context, accountID string, rawRFC822 []byte) (messageID, conversationID string, err error)
}

const providerGraph = "graph"

// sendGraph delivers via Graph sendMail instead of SMTP, recording the same attempt outcomes.
func (s *Service) sendGraph(ctx context.Context, m *Message, acc *SMTPAccount, body io.ReadCloser, rewriteTo []string, started time.Time, idx int) error {
	defer func() { _ = body.Close() }()
	if s.Graph == nil {
		return s.finish(ctx, m, acc, idx, started, nil, errs.New(errs.CodeServiceUnavailable, "Graph send is not configured."))
	}
	raw, err := io.ReadAll(io.LimitReader(body, 50<<20))
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, errs.Wrap(err, errs.CodeInternalError, "submit.graph.read"))
	}
	gid, conv, err := s.Graph.Send(ctx, m.AccountID, headerAddressedRaw(raw, m.Bcc, rewriteTo))
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, httpsend.Classify(err))
	}
	_ = conv
	res := &smtpc.Result{Response: "250 graph id " + gid}
	return s.finish(ctx, m, acc, idx, started, res, nil)
}
