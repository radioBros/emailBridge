package messages

import (
	"context"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Batch applies one action to up to 1000 messages, grouped by mailbox.
func (s *Service) Batch(ctx context.Context, accountID string, in BatchInput) (*BatchResult, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	if len(in.IDs) == 0 {
		return nil, errs.New(errs.CodeValidationFailed, "ids is required.")
	}
	if len(in.IDs) > maxBatch {
		return nil, errs.New(errs.CodeBatchTooLarge,
			"A batch may contain at most 1000 message ids.").WithSafe("limit", maxBatch)
	}
	switch in.Action {
	case "flags", "move", "delete", "copy":
	default:
		return nil, errs.New(errs.CodeValidationFailed, "action must be flags, move, delete or copy.")
	}
	byMbx := map[string][]*msgDoc{}
	var failed []string
	for _, id := range in.IDs {
		m, err := s.loadMessage(ctx, accountID, id)
		if err != nil {
			failed = append(failed, id)
			continue
		}
		byMbx[m.MailboxID] = append(byMbx[m.MailboxID], m)
	}
	var destPath string
	if in.Action == "move" || in.Action == "copy" {
		dest, err := s.resolveMailbox(ctx, accountID, in.MailboxID, in.Path)
		if err != nil {
			if in.Path == "" {
				return nil, err
			}
			destPath = in.Path
		} else {
			destPath = dest.Path
		}
	}
	processed := 0
	for mbxID, msgs := range byMbx {
		mbx, err := s.loadMailbox(ctx, accountID, mbxID)
		if err != nil {
			for _, m := range msgs {
				failed = append(failed, m.ID)
			}
			continue
		}
		uids := make([]uint32, len(msgs))
		idByUID := map[uint32]string{}
		for i, m := range msgs {
			uids[i] = m.UID
			idByUID[m.UID] = m.ID
		}
		uv := msgs[0].UIDValidity
		err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
			if _, err := s.selectChecked(ctx, cl, mbx.Path, uv); err != nil {
				return err
			}
			for _, chunk := range chunkUIDs(uids, uidChunk) {
				switch in.Action {
				case "flags":
					if in.SetFlags != nil {
						if err := cl.StoreFlagsSet(ctx, chunk, in.SetFlags); err != nil {
							return err
						}
					} else {
						if len(in.AddFlags) > 0 {
							if err := cl.StoreFlags(ctx, chunk, true, in.AddFlags); err != nil {
								return err
							}
						}
						if len(in.RemoveFlags) > 0 {
							if err := cl.StoreFlags(ctx, chunk, false, in.RemoveFlags); err != nil {
								return err
							}
						}
					}
				case "delete":
					if err := cl.StoreFlags(ctx, chunk, true, []string{`\Deleted`}); err != nil {
						return err
					}
					if err := cl.Expunge(ctx, chunk); err != nil {
						return err
					}
				case "move":
					if err := cl.Move(ctx, chunk, destPath); err != nil {
						return err
					}
				case "copy":
					if err := cl.Copy(ctx, chunk, destPath); err != nil {
						return err
					}
				}
				processed += len(chunk)
			}
			return nil
		})
		if err != nil {
			for _, m := range msgs {
				failed = append(failed, m.ID)
			}
			// undo processed count for this mailbox
			processed -= len(msgs)
			if processed < 0 {
				processed = 0
			}
		}
	}
	return &BatchResult{Processed: processed, Failed: failed}, nil
}
