package sync

import (
	"context"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// sentNames are tried when no folder carries \Sent (S6 §5).
var sentNames = []string{"Sent", "INBOX.Sent", "Sent Items", "Sent Messages", "Posta inviata", "INBOX/Sent"}

// SentCopier appends a sent message to the account's Sent folder (S6 §5) over a short-lived IMAP
// connection (the on-demand connection of S5 §3). The sync owner's connection is not used in P1, so the
// copy works wherever the submit worker runs; the owner sees the new message as a normal arrival in Sent
// (indexed, and reported only if Sent is watched and the no-replay rule allows it).
type SentCopier struct {
	Accounts       Accounts
	Dial           imapc.DialFunc
	Egress         *egress.Dialer
	IPPool         *ippool.Selector
	IMAPID         map[string]string
	CommandTimeout time.Duration
}

// CopyToSent appends raw with \Seen and the original date; path overrides the folder (accounts
// copyToSent.path). It returns the folder used and the new UID (0 without UIDPLUS).
func (s *SentCopier) CopyToSent(ctx context.Context, accountID, path string, raw []byte, date time.Time) (string, uint32, error) {
	acc, err := s.Accounts.Load(ctx, accountID)
	if err != nil {
		return "", 0, err
	}
	dial := s.Dial
	if s.Egress != nil {
		local := acc.LocalAddress
		if s.IPPool != nil {
			local = s.IPPool.ResolveLocal(acc.OrganizationID, acc.ID, "imap", acc.LocalAddress, acc.LocalAddresses, acc.IMAPStrategy)
		}
		dial = s.Egress.MailDialFunc(egress.MailNetwork{Proxy: acc.Proxy, LocalAddr: local})
	}
	o := imapc.Options{Addr: acc.IMAPAddr, TLS: acc.IMAPTLS, InsecureSkipVerify: acc.IgnoreCertErrors,
		ID: MergeIMAPID(s.IMAPID, acc.IMAPID), Dial: dial, CommandTimeout: s.CommandTimeout, TapConn: acc.ID + "/sent",
		DisableRev2: acc.DisableRev2}
	if err := AuthOptions(ctx, acc, &o); err != nil {
		return "", 0, err
	}
	c, err := imapc.Dial(ctx, o)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = c.Logout(lctx)
		cancel()
	}()
	folder := path
	if folder == "" {
		if folder, err = sentFolder(ctx, c); err != nil {
			return "", 0, err
		}
	}
	uid, err := c.Append(ctx, folder, []string{`\Seen`}, date, raw)
	if err != nil {
		return folder, 0, err
	}
	return folder, uid, nil
}

func sentFolder(ctx context.Context, c *imapc.Client) (string, error) {
	list, err := c.List(ctx)
	if err != nil {
		return "", err
	}
	for _, m := range list {
		if m.SpecialUse == `\Sent` {
			return m.Name, nil
		}
	}
	for _, name := range sentNames {
		for _, m := range list {
			if strings.EqualFold(m.Name, name) {
				return m.Name, nil
			}
		}
	}
	return "", errs.New(errs.CodeNotFound, "The account has no Sent folder; set copyToSent.path or disable copy to Sent.")
}
