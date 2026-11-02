package submit

import (
	"context"
	"io"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/httpsend"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// GmailSender sends raw RFC822 through the Gmail API (P6.2). Wired from internal/providers/gmail.
type GmailSender interface {
	Send(ctx context.Context, accountID string, rawRFC822 []byte) (messageID, threadID string, err error)
}

const providerGmail = "gmail"

// sendGmail delivers via users.messages.send instead of SMTP, recording the same attempt outcomes.
func (s *Service) sendGmail(ctx context.Context, m *Message, acc *SMTPAccount, body io.ReadCloser, rewriteTo []string, started time.Time, idx int) error {
	defer func() { _ = body.Close() }()
	if s.Gmail == nil {
		return s.finish(ctx, m, acc, idx, started, nil, errs.New(errs.CodeServiceUnavailable, "Gmail send is not configured."))
	}
	raw, err := io.ReadAll(io.LimitReader(body, 50<<20))
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, errs.Wrap(err, errs.CodeInternalError, "submit.gmail.read"))
	}
	gid, thr, err := s.Gmail.Send(ctx, m.AccountID, headerAddressedRaw(raw, m.Bcc, rewriteTo))
	if err != nil {
		return s.finish(ctx, m, acc, idx, started, nil, httpsend.Classify(err))
	}
	_ = thr
	res := &smtpc.Result{Response: "250 gmail id " + gid}
	return s.finish(ctx, m, acc, idx, started, res, nil)
}
