// Package gmail is an in-process fake of the Gmail REST subset used by EmailBridge (P6.2).
// Deterministic IDs; never talks to Google.
package gmail

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

// Server is the fake Gmail API + token endpoint.
type Server struct {
	mu            sync.Mutex
	srv           *httptest.Server
	users         map[string]*user  // email → state
	tokens        map[string]string // accessToken → email
	historyExpire map[string]bool   // email → next history.list returns 404
	seq           atomic.Uint64
	now           func() time.Time
}

type user struct {
	email     string
	historyID uint64
	messages  map[string]*message // id → msg
	labels    map[string]*label
	watch     *watchState
	nextUID   map[string]uint32 // labelId → next uid (for tests; API does not expose)
	events    []historyEvent
}

type message struct {
	ID       string
	ThreadID string
	LabelIDs []string
	Raw      []byte // RFC822
	Internal time.Time
	Size     int64
	History  uint64 // history id when created/last changed
}

type label struct {
	ID, Name, Type string // Type: system | user
}

type watchState struct {
	Topic      string
	Expiration time.Time
	HistoryID  uint64
}

// New starts an in-memory httptest server. Caller must Close().
func New() *Server {
	s := &Server{
		users:         map[string]*user{},
		tokens:        map[string]string{},
		historyExpire: map[string]bool{},
		now:           func() time.Time { return time.Now().UTC() },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/", s.handleAPI)
	s.srv = httptest.NewServer(mux)
	return s
}

// URL is the API base (no trailing slash), e.g. http://127.0.0.1:port
func (s *Server) URL() string { return s.srv.URL }

// Close shuts the server down.
func (s *Server) Close() { s.srv.Close() }

// SetToken registers an access token for email.
func (s *Server) SetToken(email, accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[accessToken] = strings.ToLower(email)
	s.ensureUserLocked(strings.ToLower(email))
}

// EnsureUser creates an empty mailbox for email.
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
	u = &user{
		email: email, historyID: 1,
		messages: map[string]*message{},
		labels:   defaultLabels(),
		nextUID:  map[string]uint32{},
	}
	s.users[email] = u
	return u
}

func defaultLabels() map[string]*label {
	sys := []struct{ id, name string }{
		{"INBOX", "INBOX"}, {"SENT", "SENT"}, {"DRAFT", "DRAFT"},
		{"TRASH", "TRASH"}, {"SPAM", "SPAM"}, {"STARRED", "STARRED"},
		{"UNREAD", "UNREAD"}, {"IMPORTANT", "IMPORTANT"},
	}
	out := map[string]*label{}
	for _, l := range sys {
		out[l.id] = &label{ID: l.id, Name: l.name, Type: "system"}
	}
	return out
}

// PutMessage adds an RFC822 message with the given label IDs; returns Gmail ids.
func (s *Server) PutMessage(email string, raw []byte, labelIDs []string) (id, threadID string, historyID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.ensureUserLocked(strings.ToLower(email))
	u.historyID++
	n := s.seq.Add(1)
	id = "msg" + strconv.FormatUint(n, 16)
	threadID = "thr" + strconv.FormatUint(n, 16)
	if labelIDs == nil {
		labelIDs = []string{"INBOX", "UNREAD"}
	}
	m := &message{
		ID: id, ThreadID: threadID, LabelIDs: append([]string(nil), labelIDs...),
		Raw: append([]byte(nil), raw...), Internal: s.now(), Size: int64(len(raw)), History: u.historyID,
	}
	u.messages[id] = m
	u.events = append(u.events, historyEvent{ID: u.historyID, Added: []string{id}})
	return id, threadID, u.historyID
}

// SeedMessages bulk-adds n identical messages without per-message history events (baseline mailbox).
// Advances historyId once. Used by large-mailbox sync tests.
func (s *Server) SeedMessages(email string, n int, raw []byte, labelIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.ensureUserLocked(strings.ToLower(email))
	if labelIDs == nil {
		labelIDs = []string{"INBOX"}
	}
	now := s.now()
	for i := 0; i < n; i++ {
		seq := s.seq.Add(1)
		id := "msg" + strconv.FormatUint(seq, 16)
		u.messages[id] = &message{
			ID: id, ThreadID: "thr" + strconv.FormatUint(seq, 16),
			LabelIDs: append([]string(nil), labelIDs...),
			Raw:      append([]byte(nil), raw...), Internal: now, Size: int64(len(raw)),
		}
	}
	u.historyID++
	for _, m := range u.messages {
		if m.History == 0 {
			m.History = u.historyID
		}
	}
}

// ExpireHistory makes the next history.list for email return 404.
func (s *Server) ExpireHistory(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.historyExpire[strings.ToLower(email)] = true
}

// HistoryID returns the current history id.
func (s *Server) HistoryID(email string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[strings.ToLower(email)]
	if u == nil {
		return 0
	}
	return u.historyID
}

type historyEvent struct {
	ID            uint64
	Added         []string
	Deleted       []string
	LabelsAdded   map[string][]string // msgID → labels
	LabelsRemoved map[string][]string
}

// ModifyLabels adds/removes labels on a message and advances history.
func (s *Server) ModifyLabels(email, msgID string, add, remove []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[strings.ToLower(email)]
	if u == nil {
		return
	}
	m := u.messages[msgID]
	if m == nil {
		return
	}
	u.historyID++
	m.History = u.historyID
	ev := historyEvent{ID: u.historyID, LabelsAdded: map[string][]string{}, LabelsRemoved: map[string][]string{}}
	if len(add) > 0 {
		ev.LabelsAdded[msgID] = append([]string(nil), add...)
	}
	if len(remove) > 0 {
		ev.LabelsRemoved[msgID] = append([]string(nil), remove...)
	}
	u.events = append(u.events, ev)
	set := map[string]bool{}
	for _, l := range m.LabelIDs {
		set[l] = true
	}
	for _, l := range remove {
		delete(set, l)
	}
	for _, l := range add {
		set[l] = true
	}
	m.LabelIDs = m.LabelIDs[:0]
	for l := range set {
		m.LabelIDs = append(m.LabelIDs, l)
	}
}

// DeleteMessage removes a message and records a history deletion.
func (s *Server) DeleteMessage(email, msgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[strings.ToLower(email)]
	if u == nil || u.messages[msgID] == nil {
		return
	}
	u.historyID++
	u.events = append(u.events, historyEvent{ID: u.historyID, Deleted: []string{msgID}})
	delete(u.messages, msgID)
}

// AddUserLabel registers a user label.
func (s *Server) AddUserLabel(email, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.ensureUserLocked(strings.ToLower(email))
	u.labels[id] = &label{ID: id, Name: name, Type: "user"}
}

// PushPayload returns the Pub/Sub message data JSON for email's current history.
func (s *Server) PushPayload(email string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[strings.ToLower(email)]
	hid := uint64(1)
	if u != nil {
		hid = u.historyID
	}
	b, _ := json.Marshal(map[string]any{"emailAddress": strings.ToLower(email), "historyId": hid})
	return b
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
	// Accept any refresh / jwt-bearer; mint a token bound to "sub" or a default user.
	email := strings.ToLower(r.Form.Get("subject"))
	if email == "" {
		email = "user@example.com"
	}
	tok := "sim-tok-" + strconv.FormatUint(s.seq.Add(1), 10)
	s.mu.Lock()
	s.tokens[tok] = email
	s.ensureUserLocked(email)
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": tok, "token_type": "Bearer", "expires_in": 3600,
	})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	email, ok := s.authEmail(r)
	if !ok {
		http.Error(w, `{"error":{"code":401,"message":"Unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	// gmail/v1/users/{userId}/...
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[0] != "gmail" || parts[1] != "v1" || parts[2] != "users" {
		http.NotFound(w, r)
		return
	}
	userID := parts[3]
	if userID != "me" && !strings.EqualFold(userID, email) {
		http.Error(w, `{"error":{"code":403,"message":"Forbidden"}}`, http.StatusForbidden)
		return
	}
	rest := parts[4:]
	switch {
	case len(rest) == 1 && rest[0] == "profile" && r.Method == http.MethodGet:
		s.writeProfile(w, email)
	case len(rest) == 1 && rest[0] == "labels" && r.Method == http.MethodGet:
		s.writeLabels(w, email)
	case len(rest) == 1 && rest[0] == "messages" && r.Method == http.MethodGet:
		s.listMessages(w, email, r)
	case len(rest) == 1 && rest[0] == "messages" && r.Method == http.MethodPost:
		s.sendMessage(w, email, r)
	case len(rest) == 2 && rest[0] == "messages" && r.Method == http.MethodGet:
		s.getMessage(w, email, rest[1], r)
	case len(rest) == 3 && rest[0] == "messages" && rest[2] == "modify" && r.Method == http.MethodPost:
		s.modifyMessage(w, email, rest[1], r)
	case len(rest) == 1 && rest[0] == "history" && r.Method == http.MethodGet:
		s.listHistory(w, email, r)
	case len(rest) == 1 && rest[0] == "watch" && r.Method == http.MethodPost:
		s.watch(w, email, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) writeProfile(w http.ResponseWriter, email string) {
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	hid := u.historyID
	n := len(u.messages)
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"emailAddress": email, "messagesTotal": n, "threadsTotal": n,
		"historyId": strconv.FormatUint(hid, 10),
	})
}

func (s *Server) writeLabels(w http.ResponseWriter, email string) {
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	var list []map[string]any
	for _, l := range u.labels {
		list = append(list, map[string]any{"id": l.ID, "name": l.Name, "type": l.Type})
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"labels": list})
}

func (s *Server) listMessages(w http.ResponseWriter, email string, r *http.Request) {
	q := r.URL.Query().Get("q")
	label := r.URL.Query().Get("labelIds")
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	var list []map[string]string
	for _, m := range u.messages {
		if label != "" && !hasLabel(m.LabelIDs, label) {
			continue
		}
		_ = q
		list = append(list, map[string]string{"id": m.ID, "threadId": m.ThreadID})
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": list, "resultSizeEstimate": len(list)})
}

func (s *Server) getMessage(w http.ResponseWriter, email, id string, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "full"
	}
	s.mu.Lock()
	u := s.users[email]
	var m *message
	if u != nil {
		m = u.messages[id]
	}
	s.mu.Unlock()
	if m == nil {
		http.Error(w, `{"error":{"code":404,"message":"Not Found"}}`, http.StatusNotFound)
		return
	}
	out := map[string]any{
		"id": m.ID, "threadId": m.ThreadID, "labelIds": m.LabelIDs,
		"internalDate": strconv.FormatInt(m.Internal.UnixMilli(), 10),
		"sizeEstimate": m.Size, "historyId": strconv.FormatUint(m.History, 10),
	}
	switch format {
	case "raw":
		out["raw"] = base64.RawURLEncoding.EncodeToString(m.Raw)
	case "metadata", "full", "minimal":
		out["payload"] = map[string]any{
			"mimeType": "message/rfc822",
			"headers":  headersFromRaw(m.Raw),
		}
		if format == "full" || format == "raw" {
			out["raw"] = base64.RawURLEncoding.EncodeToString(m.Raw)
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func headersFromRaw(raw []byte) []map[string]string {
	text := string(raw)
	i := strings.Index(text, "\r\n\r\n")
	if i < 0 {
		i = strings.Index(text, "\n\n")
	}
	if i < 0 {
		return nil
	}
	var out []map[string]string
	for _, line := range strings.Split(text[:i], "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out = append(out, map[string]string{"name": strings.TrimSpace(k), "value": strings.TrimSpace(v)})
	}
	return out
}

func (s *Server) modifyMessage(w http.ResponseWriter, email, id string, r *http.Request) {
	var body struct {
		AddLabelIds    []string `json:"addLabelIds"`
		RemoveLabelIds []string `json:"removeLabelIds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.ModifyLabels(email, id, body.AddLabelIds, body.RemoveLabelIds)
	s.getMessage(w, email, id, r)
}

func (s *Server) sendMessage(w http.ResponseWriter, email string, r *http.Request) {
	var body struct {
		Raw string `json:"raw"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	raw, err := base64.RawURLEncoding.DecodeString(body.Raw)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(body.Raw)
	}
	if err != nil {
		http.Error(w, `{"error":{"code":400,"message":"bad raw"}}`, http.StatusBadRequest)
		return
	}
	id, thr, _ := s.PutMessage(email, raw, []string{"SENT"})
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "threadId": thr, "labelIds": []string{"SENT"}})
}

func (s *Server) listHistory(w http.ResponseWriter, email string, r *http.Request) {
	startStr := r.URL.Query().Get("startHistoryId")
	start, _ := strconv.ParseUint(startStr, 10, 64)
	s.mu.Lock()
	if s.historyExpire[email] {
		delete(s.historyExpire, email)
		s.mu.Unlock()
		http.Error(w, `{"error":{"code":404,"message":"Requested entity was not found."}}`, http.StatusNotFound)
		return
	}
	u := s.ensureUserLocked(email)
	var records []map[string]any
	for _, ev := range u.events {
		if ev.ID <= start {
			continue
		}
		rec := map[string]any{"id": strconv.FormatUint(ev.ID, 10)}
		if len(ev.Added) > 0 {
			var added []map[string]any
			for _, id := range ev.Added {
				m := u.messages[id]
				item := map[string]any{"id": id}
				if m != nil {
					item["threadId"] = m.ThreadID
					item["labelIds"] = m.LabelIDs
				}
				added = append(added, map[string]any{"message": item})
			}
			rec["messagesAdded"] = added
		}
		if len(ev.Deleted) > 0 {
			var del []map[string]any
			for _, id := range ev.Deleted {
				del = append(del, map[string]any{"message": map[string]any{"id": id}})
			}
			rec["messagesDeleted"] = del
		}
		if len(ev.LabelsAdded) > 0 {
			var la []map[string]any
			for id, labs := range ev.LabelsAdded {
				la = append(la, map[string]any{"message": map[string]any{"id": id}, "labelIds": labs})
			}
			rec["labelsAdded"] = la
		}
		if len(ev.LabelsRemoved) > 0 {
			var lr []map[string]any
			for id, labs := range ev.LabelsRemoved {
				lr = append(lr, map[string]any{"message": map[string]any{"id": id}, "labelIds": labs})
			}
			rec["labelsRemoved"] = lr
		}
		records = append(records, rec)
	}
	hid := u.historyID
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"history": historyRecordsSorted(records), "historyId": strconv.FormatUint(hid, 10),
	})
}

func historyRecordsSorted(in []map[string]any) []map[string]any {
	// stable enough for tests; order by id string
	if len(in) <= 1 {
		return in
	}
	for i := 0; i < len(in); i++ {
		for j := i + 1; j < len(in); j++ {
			a, _ := in[i]["id"].(string)
			b, _ := in[j]["id"].(string)
			if a > b {
				in[i], in[j] = in[j], in[i]
			}
		}
	}
	return in
}

func (s *Server) watch(w http.ResponseWriter, email string, r *http.Request) {
	var body struct {
		TopicName string `json:"topicName"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	s.mu.Lock()
	u := s.ensureUserLocked(email)
	exp := s.now().Add(7 * 24 * time.Hour)
	u.watch = &watchState{Topic: body.TopicName, Expiration: exp, HistoryID: u.historyID}
	hid := u.historyID
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"historyId": strconv.FormatUint(hid, 10), "expiration": strconv.FormatInt(exp.UnixMilli(), 10),
	})
}

func hasLabel(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
