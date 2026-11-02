package messages

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gomail "github.com/emersion/go-message/mail"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/mime"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ListDrafts lists draft rows for an account.
func (s *Service) ListDrafts(ctx context.Context, accountID string, limit int, cursor string) ([]DraftView, *string, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, nil, err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return nil, nil, err
	}
	if limit <= 0 || limit > maxMbxList {
		limit = maxMbxList
	}
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID})
	if err != nil {
		return nil, nil, err
	}
	if cursor != "" {
		cur, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		filter["_id"] = bson.M{"$lt": cur.ID}
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(int64(limit + 1))
	c, err := s.DB.Collection(Drafts).Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "messages.drafts.list")
	}
	defer func() { _ = c.Close(ctx) }()
	var rows []draftDoc
	if err := c.All(ctx, &rows); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		nc := encodeCursor(cursorPayload{ID: rows[len(rows)-1].ID})
		next = &nc
	}
	out := make([]DraftView, len(rows))
	for i := range rows {
		out[i] = draftView(&rows[i])
	}
	return out, next, nil
}

// GetDraft returns one draft; withText fetches body from IMAP.
func (s *Service) GetDraft(ctx context.Context, accountID, draftID string, withText bool) (*DraftView, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, err
	}
	d, err := s.loadDraft(ctx, accountID, draftID)
	if err != nil {
		return nil, err
	}
	v := draftView(d)
	if !withText || d.UID == 0 {
		return &v, nil
	}
	err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
		if _, err := s.selectChecked(ctx, cl, d.Path, d.UIDValidity); err != nil {
			return err
		}
		raw, err := cl.Raw(ctx, d.UID, defaultText)
		if err != nil {
			return err
		}
		ent, err := mime.ParseEntity(raw)
		if err != nil {
			return nil
		}
		plain, html, _, _ := mime.TextParts(ent, defaultText)
		v.Text, v.HTML = plain, html
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateDraft APPENDS a draft to the Drafts folder and records a drf_ row.
func (s *Service) CreateDraft(ctx context.Context, accountID string, in DraftInput) (*DraftView, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	raw, subject, mid, err := buildDraftMIME(in)
	if err != nil {
		return nil, err
	}
	path, err := s.draftsPath(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var uid, uv uint32
	err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
		u, err := cl.Append(ctx, path, []string{`\Draft`}, s.now(), raw)
		if err != nil {
			return err
		}
		uid = u
		if st, err := cl.Status(ctx, path); err == nil {
			uv = st.UIDValidity
		}
		if uid == 0 && mid != "" {
			if _, err := cl.Select(ctx, path, true); err == nil {
				if found, err := cl.SearchMessageID(ctx, mid); err == nil && len(found) > 0 {
					uid = found[len(found)-1]
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	now := s.now()
	d := &draftDoc{
		ID: store.NewID(store.PrefixDraft), OrganizationID: org, AccountID: accountID,
		Path: path, UID: uid, UIDValidity: uv, MessageIDHeader: mid, Subject: subject,
		Size: int64(len(raw)), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := s.DB.Collection(Drafts).InsertOne(ctx, d); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.drafts.insert")
	}
	v := draftView(d)
	return &v, nil
}

// DeleteDraft expunges the IMAP draft and removes the row.
func (s *Service) DeleteDraft(ctx context.Context, accountID, draftID string) error {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return err
	}
	d, err := s.loadDraft(ctx, accountID, draftID)
	if err != nil {
		return err
	}
	if d.UID != 0 {
		_ = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
			if _, err := s.selectChecked(ctx, cl, d.Path, d.UIDValidity); err != nil {
				return err
			}
			if err := cl.StoreFlags(ctx, []uint32{d.UID}, true, []string{`\Deleted`}); err != nil {
				return err
			}
			return cl.Expunge(ctx, []uint32{d.UID})
		})
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": draftID, "accountId": accountID})
	if err != nil {
		return err
	}
	_, err = s.DB.Collection(Drafts).DeleteOne(ctx, filter)
	return err
}

// SendDraft fetches the draft raw, submits it, then deletes the draft.
func (s *Service) SendDraft(ctx context.Context, accountID, draftID, idemKey string) (*submit.SubmitResult, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	if s.Submit == nil {
		return nil, errs.New(errs.CodeInternalError, "Submit service is not configured.")
	}
	d, err := s.loadDraft(ctx, accountID, draftID)
	if err != nil {
		return nil, err
	}
	if d.UID == 0 {
		return nil, errs.New(errs.CodeDraftNotFound, "The draft has no IMAP UID yet.")
	}
	var raw []byte
	err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
		if _, err := s.selectChecked(ctx, cl, d.Path, d.UIDValidity); err != nil {
			return err
		}
		var err error
		raw, err = cl.Raw(ctx, d.UID, maxUpload)
		return err
	})
	if err != nil {
		return nil, err
	}
	res, err := s.Submit.Submit(ctx, accountID, submit.SubmitRequest{
		Raw: base64.StdEncoding.EncodeToString(raw),
	}, idemKey, false)
	if err != nil {
		return nil, err
	}
	if res.Status == http.StatusAccepted || res.Accepted != nil {
		_ = s.DeleteDraft(ctx, accountID, draftID)
	}
	return res, nil
}

func (s *Service) loadDraft(ctx context.Context, accountID, draftID string) (*draftDoc, error) {
	if !store.IsID(store.PrefixDraft, draftID) {
		return nil, errs.New(errs.CodeDraftNotFound, "The draft does not exist.").WithSafe("draftId", draftID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": draftID, "accountId": accountID})
	if err != nil {
		return nil, err
	}
	var d draftDoc
	err = s.DB.Collection(Drafts).FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeDraftNotFound, "The draft does not exist.").WithSafe("draftId", draftID)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.drafts.get")
	}
	return &d, nil
}

func (s *Service) draftsPath(ctx context.Context, accountID string) (string, error) {
	filter, err := store.Scoped(ctx, bson.M{
		"accountId": accountID, "status": "active",
		"specialUse": bson.M{"$in": []string{`\Drafts`, "\\Drafts"}},
	})
	if err != nil {
		return "", err
	}
	var m mbxDoc
	err = s.DB.Collection(index.Mailboxes).FindOne(ctx, filter).Decode(&m)
	if err == nil {
		return m.Path, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return "", errs.Wrap(err, errs.CodeInternalError, "messages.drafts.path")
	}
	// Fall back to LIST via IMAP.
	var path string
	err = s.withIMAP(ctx, accountID, func(cl *imapc.Client, _ *ebsync.Account) error {
		list, err := cl.List(ctx)
		if err != nil {
			return err
		}
		for _, mb := range list {
			if mb.SpecialUse == `\Drafts` {
				path = mb.Name
				return nil
			}
		}
		path = "Drafts"
		// Ensure it exists.
		_ = cl.Create(ctx, path)
		_ = cl.Subscribe(ctx, path)
		return nil
	})
	return path, err
}

func buildDraftMIME(in DraftInput) (raw []byte, subject, messageID string, err error) {
	if in.Raw != "" {
		raw, err = decodeRaw(in.Raw)
		if err != nil {
			return nil, "", "", err
		}
		hdr, _ := mime.ParseHeaders(raw)
		subject, _ = mime.DecodeSubject(hdr)
		messageID = mime.MessageIDHeader(hdr)
		return raw, subject, messageID, nil
	}
	var buf bytes.Buffer
	var h gomail.Header
	h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	if in.Subject != "" {
		h.SetSubject(in.Subject)
	}
	subject = in.Subject
	if in.From != nil && in.From.Address != "" {
		h.SetAddressList("From", []*gomail.Address{{Name: in.From.Name, Address: in.From.Address}})
	}
	setAddrs := func(key string, list []submit.AddressInput) {
		if len(list) == 0 {
			return
		}
		addrs := make([]*gomail.Address, 0, len(list))
		for _, a := range list {
			addrs = append(addrs, &gomail.Address{Name: a.Name, Address: a.Address})
		}
		h.SetAddressList(key, addrs)
	}
	setAddrs("To", in.To)
	setAddrs("Cc", in.Cc)
	setAddrs("Reply-To", in.ReplyTo)
	messageID = in.MessageID
	if messageID == "" {
		messageID = fmt.Sprintf("<draft-%d@emailbridge.local>", time.Now().UnixNano())
	}
	h.Set("Message-Id", messageID)
	h.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	for k, v := range in.Headers {
		if strings.EqualFold(k, "from") || strings.EqualFold(k, "to") || strings.EqualFold(k, "subject") {
			continue
		}
		h.Set(k, v)
	}
	body := in.Text
	if body == "" && in.HTML != "" {
		h.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		body = in.HTML
	}
	w, err := gomail.CreateSingleInlineWriter(&buf, h)
	if err != nil {
		return nil, "", "", errs.Wrap(err, errs.CodeValidationFailed, "draft.mime")
	}
	if _, err := io.WriteString(w, body); err != nil {
		_ = w.Close()
		return nil, "", "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", "", err
	}
	return buf.Bytes(), subject, messageID, nil
}
