// Package graph is an in-process fake of the Microsoft Graph REST subset used by EmailBridge (P6.3).
// Deterministic IDs; never talks to Microsoft.
package graph

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server is the fake Graph API + token endpoint.
type Server struct {
	mu          sync.Mutex
	srv         *httptest.Server
	users       map[string]*user  // email/upn → state
	tokens      map[string]string // accessToken → email
	deltaExpire map[string]bool   // email → next delta returns syncStateNotFound
	subs        map[string]*subscription
	seq         atomic.Uint64
	now         func() time.Time
}

type user struct {
	email   string
	folders map[string]*folder // id → folder
	byKnown map[string]string  // wellKnownName → folder id
}

type folder struct {
	ID, DisplayName, WellKnown string
	Messages                   map[string]*message
	DeltaGen                   uint64 // bumped to invalidate delta links
}

type message struct {
	ID, ConversationID string
	FolderID           string
	Raw                []byte
	IsRead, IsDraft    bool
	Flagged            bool
	Subject            string
	From               string
	To                 []string
	InternetMessageID  string
	Received           time.Time
	Size               int64
	Removed            bool
}

type subscription struct {
	ID, Resource, ClientState, NotificationURL, Owner string
	Expiration                                        time.Time
	ChangeType                                        string
}

// New starts an in-memory httptest server. Caller must Close().
func New() *Server {
	s := &Server{
		users:       map[string]*user{},
		tokens:      map[string]string{},
		deltaExpire: map[string]bool{},
		subs:        map[string]*subscription{},
		now:         func() time.Time { return time.Now().UTC() },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/", s.handleAPI)
	s.srv = httptest.NewServer(mux)
	return s
}

// URL is the API base (no trailing slash).
func (s *Server) URL() string { return s.srv.URL }

// Close shuts the server down.
func (s *Server) Close() { s.srv.Close() }

// SetToken registers an access token for email/upn.
func (s *Server) SetToken(email, accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[accessToken] = strings.ToLower(email)
	s.ensureUserLocked(strings.ToLower(email))
}

// EnsureUser creates default mail folders for email.
func (s *Server) EnsureUser(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureUserLocked(strings.ToLower(email))
}

func (s *Server) ensureUserLocked(email string) *user {
	u := s.users[email]
	if u != nil {
		return u
	}
	u = &user{email: email, folders: map[string]*folder{}, byKnown: map[string]string{}}
	for _, wk := range []struct{ known, name string }{
		{"inbox", "Inbox"},
		{"sentitems", "Sent Items"},
		{"drafts", "Drafts"},
		{"deleteditems", "Deleted Items"},
		{"junkemail", "Junk Email"},
		{"archive", "Archive"},
	} {
		id := "fld" + strconv.FormatUint(s.seq.Add(1), 16)
		f := &folder{ID: id, DisplayName: wk.name, WellKnown: wk.known, Messages: map[string]*message{}}
		u.folders[id] = f
		u.byKnown[wk.known] = id
	}
	s.users[email] = u
	return u
}

// PutMessage adds an RFC822 message into a well-known folder; returns Graph ids.
func (s *Server) PutMessage(email string, raw []byte, wellKnown string) (id, conversationID, folderID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putMessageLocked(email, raw, wellKnown)
}

// SeedN adds n lightweight messages into a well-known folder (state-size / load tests).
func (s *Server) SeedN(email, wellKnown string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: S\r\nMessage-ID: <s@ex.com>\r\n\r\nx\r\n")
	for i := 0; i < n; i++ {
		s.putMessageLocked(email, raw, wellKnown)
	}
}

func (s *Server) putMessageLocked(email string, raw []byte, wellKnown string) (id, conversationID, folderID string) {
	u := s.ensureUserLocked(strings.ToLower(email))
	if wellKnown == "" {
		wellKnown = "inbox"
	}
	fid := u.byKnown[strings.ToLower(wellKnown)]
	if fid == "" {
		fid = u.byKnown["inbox"]
	}
	f := u.folders[fid]
	n := s.seq.Add(1)
	id = "AAMk" + strconv.FormatUint(n, 16)
	conversationID = "conv" + strconv.FormatUint(n, 16)
	subj, from, to, mid := parseEnvelope(raw)
	// Unique Message-ID so builders that key on it stay distinct under SeedN.
	if mid == "" || mid == "<s@ex.com>" {
		mid = "<s" + strconv.FormatUint(n, 10) + "@ex.com>"
	}
	m := &message{
		ID: id, ConversationID: conversationID, FolderID: fid,
		Raw: append([]byte(nil), raw...), Subject: subj, From: from, To: to,
		InternetMessageID: mid, Received: s.now(), Size: int64(len(raw)),
	}
	f.Messages[id] = m
	return id, conversationID, fid
}

// ExpireDelta makes the next delta query for email return syncStateNotFound.
func (s *Server) ExpireDelta(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deltaExpire[strings.ToLower(email)] = true
	u := s.users[strings.ToLower(email)]
	if u == nil {
		return
	}
	for _, f := range u.folders {
		f.DeltaGen++
	}
}

// SetRead sets isRead on a message.
func (s *Server) SetRead(email, msgID string, read bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.findMsgLocked(strings.ToLower(email), msgID); m != nil {
		m.IsRead = read
	}
}

// DeleteMessage soft-removes a message (appears as @removed on next delta).
func (s *Server) DeleteMessage(email, msgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.findMsgLocked(strings.ToLower(email), msgID); m != nil {
		m.Removed = true
	}
}

// FolderID returns the folder id for a well-known name.
func (s *Server) FolderID(email, wellKnown string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.ensureUserLocked(strings.ToLower(email))
	return u.byKnown[strings.ToLower(wellKnown)]
}

// Subscription returns a copy of the subscription or nil.
func (s *Server) Subscription(id string) *subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := s.subs[id]
	if sub == nil {
		return nil
	}
	cp := *sub
	return &cp
}

// NotifyPayload builds a Graph notification envelope for the subscription.
func (s *Server) NotifyPayload(subscriptionID string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := s.subs[subscriptionID]
	if sub == nil {
		return []byte(`{"value":[]}`)
	}
	b, _ := json.Marshal(map[string]any{
		"value": []map[string]any{{
			"subscriptionId": sub.ID,
			"clientState":    sub.ClientState,
			"changeType":     "created",
			"resource":       sub.Resource,
		}},
	})
	return b
}

func (s *Server) findMsgLocked(email, id string) *message {
	u := s.users[email]
	if u == nil {
		return nil
	}
	for _, f := range u.folders {
		if m := f.Messages[id]; m != nil {
			return m
		}
	}
	return nil
}

func (s *Server) authEmail(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	email, ok := s.tokens[tok]
	return email, ok
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	grant := r.Form.Get("grant_type")
	email := strings.ToLower(r.Form.Get("username"))
	if email == "" {
		email = strings.ToLower(r.Form.Get("subject"))
	}
	if email == "" {
		email = "user@example.com"
	}
	if grant == "client_credentials" {
		// App-only: scope may include a UPN hint via resource; bind to default user.
		email = "user@example.com"
	}
	tok := "graph-tok-" + strconv.FormatUint(s.seq.Add(1), 10)
	s.mu.Lock()
	s.tokens[tok] = email
	s.ensureUserLocked(email)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": tok, "token_type": "Bearer", "expires_in": 3600,
	})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	email, ok := s.authEmail(r)
	if !ok {
		http.Error(w, `{"error":{"code":"InvalidAuthenticationToken","message":"Unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	// Support /v1.0/... and bare paths.
	path = strings.TrimPrefix(path, "v1.0/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	// /users/{upn}/... → switch mailbox
	if parts[0] == "users" && len(parts) >= 2 {
		upn := strings.ToLower(parts[1])
		s.mu.Lock()
		s.ensureUserLocked(upn)
		s.mu.Unlock()
		email = upn
		parts = parts[2:]
	} else if parts[0] == "me" {
		parts = parts[1:]
	}

	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		s.writeMe(w, email)
	case len(parts) == 1 && parts[0] == "mailFolders" && r.Method == http.MethodGet:
		s.listFolders(w, email)
	case len(parts) >= 1 && (parts[0] == "mailFolders" || strings.HasPrefix(parts[0], "mailFolders(")):
		if parts[0] == "mailFolders" && len(parts) >= 2 {
			s.handleFolder(w, r, email, parts[1], parts[2:])
		} else if strings.HasPrefix(parts[0], "mailFolders(") {
			key := strings.TrimPrefix(parts[0], "mailFolders")
			s.handleFolder(w, r, email, key, parts[1:])
		} else {
			http.NotFound(w, r)
		}
	case len(parts) >= 1 && parts[0] == "messages":
		s.handleMessages(w, r, email, parts[1:])
	case len(parts) == 1 && parts[0] == "sendMail" && r.Method == http.MethodPost:
		s.sendMail(w, email, r)
	case parts[0] == "subscriptions":
		s.handleSubscriptions(w, r, email, parts[1:])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) writeMe(w http.ResponseWriter, email string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mail": email, "userPrincipalName": email, "id": "user-" + email,
	})
}

func (s *Server) listFolders(w http.ResponseWriter, email string) {
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	var list []map[string]any
	for _, f := range u.folders {
		list = append(list, map[string]any{
			"id": f.ID, "displayName": f.DisplayName, "wellKnownName": f.WellKnown,
		})
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"value": list})
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request, email, folderKey string, rest []string) {
	folderID := resolveFolderKey(folderKey)
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	fid := folderID
	if known := u.byKnown[strings.ToLower(folderID)]; known != "" {
		fid = known
	}
	// wellKnownName('inbox') style
	if strings.HasPrefix(folderKey, "'") || strings.Contains(folderKey, "(") {
		wk := extractWellKnown(folderKey)
		if id := u.byKnown[wk]; id != "" {
			fid = id
		}
	}
	f := u.folders[fid]
	s.mu.Unlock()
	if f == nil {
		http.Error(w, `{"error":{"code":"ErrorItemNotFound","message":"folder"}}`, http.StatusNotFound)
		return
	}
	if len(rest) >= 1 && rest[0] == "messages" {
		if len(rest) >= 2 && rest[1] == "delta" && r.Method == http.MethodGet {
			s.delta(w, email, f.ID, r)
			return
		}
		if len(rest) == 1 && r.Method == http.MethodGet {
			s.listFolderMessages(w, email, f.ID)
			return
		}
	}
	if len(rest) == 0 && r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": f.ID, "displayName": f.DisplayName, "wellKnownName": f.WellKnown,
		})
		return
	}
	http.NotFound(w, r)
}

func resolveFolderKey(key string) string {
	key = strings.Trim(key, "'")
	if i := strings.Index(key, "("); i >= 0 {
		return extractWellKnown(key)
	}
	return key
}

func extractWellKnown(key string) string {
	// mailFolders('inbox') or 'inbox'
	key = strings.Trim(key, "'")
	if i := strings.Index(key, "('"); i >= 0 {
		key = key[i+2:]
	}
	key = strings.TrimSuffix(key, "')")
	key = strings.Trim(key, "'")
	return strings.ToLower(key)
}

func (s *Server) listFolderMessages(w http.ResponseWriter, email, folderID string) {
	s.mu.Lock()
	u := s.users[email]
	f := u.folders[folderID]
	var list []map[string]any
	if f != nil {
		for _, m := range f.Messages {
			if m.Removed {
				continue
			}
			list = append(list, s.msgJSON(m, false))
		}
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"value": list})
}

func (s *Server) delta(w http.ResponseWriter, email, folderID string, r *http.Request) {
	s.mu.Lock()
	if s.deltaExpire[email] {
		delete(s.deltaExpire, email)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "syncStateNotFound", "message": "Sync state not found."},
		})
		return
	}
	u := s.ensureUserLocked(email)
	f := u.folders[folderID]
	token := r.URL.Query().Get("$deltatoken")
	if token == "" {
		// Also accept opaque delta link path style via query from stored link
		token = r.URL.Query().Get("token")
	}
	_ = token
	var list []map[string]any
	if f != nil {
		for _, m := range f.Messages {
			if m.Removed {
				list = append(list, map[string]any{
					"id": m.ID, "@removed": map[string]string{"reason": "deleted"},
				})
				continue
			}
			list = append(list, s.msgJSON(m, false))
		}
		gen := f.DeltaGen
		s.mu.Unlock()
		deltaLink := s.URL() + "/v1.0/me/mailFolders/" + folderID + "/messages/delta?$deltatoken=d" + strconv.FormatUint(gen, 10)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": list, "@odata.deltaLink": deltaLink,
		})
		return
	}
	s.mu.Unlock()
	http.NotFound(w, r)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request, email string, rest []string) {
	if len(rest) == 0 {
		http.NotFound(w, r)
		return
	}
	id := rest[0]
	if len(rest) >= 2 && rest[1] == "$value" && r.Method == http.MethodGet {
		s.getMIME(w, email, id)
		return
	}
	if len(rest) >= 2 && rest[1] == "move" && r.Method == http.MethodPost {
		s.moveMessage(w, email, id, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getMessage(w, email, id)
	case http.MethodPatch:
		s.patchMessage(w, email, id, r)
	case http.MethodDelete:
		s.DeleteMessage(email, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getMessage(w http.ResponseWriter, email, id string) {
	s.mu.Lock()
	m := s.findMsgLocked(email, id)
	s.mu.Unlock()
	if m == nil || m.Removed {
		http.Error(w, `{"error":{"code":"ErrorItemNotFound","message":"not found"}}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(s.msgJSON(m, true))
}

func (s *Server) getMIME(w http.ResponseWriter, email, id string) {
	s.mu.Lock()
	m := s.findMsgLocked(email, id)
	s.mu.Unlock()
	if m == nil || m.Removed {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	_, _ = w.Write(m.Raw)
}

func (s *Server) patchMessage(w http.ResponseWriter, email, id string, r *http.Request) {
	var body struct {
		IsRead *bool `json:"isRead"`
		Flag   *struct {
			FlagStatus string `json:"flagStatus"`
		} `json:"flag"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	s.mu.Lock()
	m := s.findMsgLocked(email, id)
	if m != nil {
		if body.IsRead != nil {
			m.IsRead = *body.IsRead
		}
		if body.Flag != nil {
			m.Flagged = strings.EqualFold(body.Flag.FlagStatus, "flagged")
		}
	}
	s.mu.Unlock()
	if m == nil {
		http.Error(w, `{"error":{"code":"ErrorItemNotFound"}}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(s.msgJSON(m, true))
}

func (s *Server) moveMessage(w http.ResponseWriter, email, id string, r *http.Request) {
	var body struct {
		DestinationID string `json:"destinationId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[email]
	if u == nil {
		http.NotFound(w, r)
		return
	}
	var m *message
	var src *folder
	for _, f := range u.folders {
		if msg := f.Messages[id]; msg != nil {
			m, src = msg, f
			break
		}
	}
	dst := u.folders[body.DestinationID]
	if m == nil || dst == nil {
		http.Error(w, `{"error":{"code":"ErrorItemNotFound"}}`, http.StatusNotFound)
		return
	}
	delete(src.Messages, id)
	m.FolderID = dst.ID
	dst.Messages[id] = m
	_ = json.NewEncoder(w).Encode(s.msgJSON(m, true))
}

func (s *Server) sendMail(w http.ResponseWriter, email string, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	rawBody, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	var raw []byte
	if strings.HasPrefix(strings.ToLower(ct), "text/plain") {
		// Documented Graph MIME upload: body is base64(RFC822).
		var err error
		raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(rawBody)))
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(rawBody)))
		}
		if err != nil {
			http.Error(w, `{"error":{"code":"ErrorMimeContentInvalidBase64String","message":"Invalid base64 string for MIME content."}}`, http.StatusBadRequest)
			return
		}
	} else {
		// JSON sendMail (application/json) — keep for other tests.
		var body struct {
			Message struct {
				Subject string `json:"subject"`
				Body    struct {
					ContentType string `json:"contentType"`
					Content     string `json:"content"`
				} `json:"body"`
				ToRecipients []struct {
					EmailAddress struct {
						Address string `json:"address"`
					} `json:"emailAddress"`
				} `json:"toRecipients"`
			} `json:"message"`
		}
		_ = json.Unmarshal(rawBody, &body)
		to := "undisclosed"
		if len(body.Message.ToRecipients) > 0 {
			to = body.Message.ToRecipients[0].EmailAddress.Address
		}
		raw = []byte("From: " + email + "\r\nTo: " + to + "\r\nSubject: " + body.Message.Subject +
			"\r\nMessage-ID: <send@" + email + ">\r\n\r\n" + body.Message.Body.Content)
	}
	id, conv, _ := s.PutMessage(email, raw, "sentitems")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "conversationId": conv})
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request, email string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		s.createSub(w, email, r)
	case len(rest) == 1 && r.Method == http.MethodPatch:
		s.renewSub(w, rest[0], r)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		s.mu.Lock()
		delete(s.subs, rest[0])
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && r.Method == http.MethodGet:
		s.mu.Lock()
		sub := s.subs[rest[0]]
		s.mu.Unlock()
		if sub == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(subJSON(sub))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) createSub(w http.ResponseWriter, email string, r *http.Request) {
	var body struct {
		ChangeType         string `json:"changeType"`
		NotificationURL    string `json:"notificationUrl"`
		Resource           string `json:"resource"`
		ExpirationDateTime string `json:"expirationDateTime"`
		ClientState        string `json:"clientState"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	exp, _ := time.Parse(time.RFC3339, body.ExpirationDateTime)
	if exp.IsZero() {
		exp = s.now().Add(3 * 24 * time.Hour)
	}
	id := "sub" + strconv.FormatUint(s.seq.Add(1), 16)
	sub := &subscription{
		ID: id, Resource: body.Resource, ClientState: body.ClientState,
		NotificationURL: body.NotificationURL, Owner: email, Expiration: exp.UTC(),
		ChangeType: body.ChangeType,
	}
	s.mu.Lock()
	s.subs[id] = sub
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(subJSON(sub))
}

func (s *Server) renewSub(w http.ResponseWriter, id string, r *http.Request) {
	var body struct {
		ExpirationDateTime string `json:"expirationDateTime"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	exp, _ := time.Parse(time.RFC3339, body.ExpirationDateTime)
	s.mu.Lock()
	sub := s.subs[id]
	if sub != nil && !exp.IsZero() {
		sub.Expiration = exp.UTC()
	}
	s.mu.Unlock()
	if sub == nil {
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(subJSON(sub))
}

func subJSON(sub *subscription) map[string]any {
	return map[string]any{
		"id": sub.ID, "resource": sub.Resource, "changeType": sub.ChangeType,
		"notificationUrl": sub.NotificationURL, "clientState": sub.ClientState,
		"expirationDateTime": sub.Expiration.Format(time.RFC3339),
	}
}

func (s *Server) msgJSON(m *message, _ bool) map[string]any {
	flagStatus := "notFlagged"
	if m.Flagged {
		flagStatus = "flagged"
	}
	to := make([]map[string]any, 0, len(m.To))
	for _, a := range m.To {
		to = append(to, map[string]any{"emailAddress": map[string]string{"address": a}})
	}
	from := map[string]any{}
	if m.From != "" {
		from = map[string]any{"emailAddress": map[string]string{"address": m.From}}
	}
	return map[string]any{
		"id": m.ID, "conversationId": m.ConversationID, "subject": m.Subject,
		"isRead": m.IsRead, "isDraft": m.IsDraft, "flag": map[string]string{"flagStatus": flagStatus},
		"receivedDateTime": m.Received.Format(time.RFC3339), "internetMessageId": m.InternetMessageID,
		"from": from, "toRecipients": to, "parentFolderId": m.FolderID,
		"bodyPreview": "", "size": m.Size,
	}
}

func parseEnvelope(raw []byte) (subject, from string, to []string, messageID string) {
	text := string(raw)
	i := strings.Index(text, "\r\n\r\n")
	if i < 0 {
		i = strings.Index(text, "\n\n")
	}
	if i < 0 {
		i = len(text)
	}
	for _, line := range strings.Split(text[:i], "\n") {
		line = strings.TrimRight(line, "\r")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "subject":
			subject = v
		case "from":
			from = extractAddr(v)
		case "to":
			for _, p := range strings.Split(v, ",") {
				if a := extractAddr(strings.TrimSpace(p)); a != "" {
					to = append(to, a)
				}
			}
		case "message-id":
			messageID = v
		}
	}
	return
}

func extractAddr(v string) string {
	if i := strings.Index(v, "<"); i >= 0 {
		v = v[i+1:]
		if j := strings.Index(v, ">"); j >= 0 {
			v = v[:j]
		}
	}
	return strings.TrimSpace(v)
}
