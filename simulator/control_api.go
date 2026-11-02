package simulator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

func (sim *Simulator) controlMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /addrs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sim.addr)
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, _ *http.Request) {
		sim.Reset()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("POST /users", sim.handleCreateUser)
	mux.HandleFunc("DELETE /users/{user}", sim.handleDeleteUser)
	mux.HandleFunc("POST /users/{user}/mailboxes", sim.handleCreateMailbox)
	mux.HandleFunc("POST /users/{user}/mailbox/delete", sim.handleDeleteMailbox)
	mux.HandleFunc("POST /users/{user}/mailbox/inject", sim.handleInject)
	mux.HandleFunc("POST /users/{user}/mailbox/flags", sim.handleFlags)
	mux.HandleFunc("POST /users/{user}/mailbox/expunge", sim.handleExpunge)
	mux.HandleFunc("POST /users/{user}/mailbox/rename", sim.handleRename)
	mux.HandleFunc("POST /users/{user}/mailbox/recreate", sim.handleRecreate)
	mux.HandleFunc("PUT /users/{user}/caps", sim.handleCaps)
	mux.HandleFunc("GET /users/{user}/caps", sim.handleGetCaps)
	mux.HandleFunc("GET /users/{user}/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sim.SessionStats(r.PathValue("user")))
	})
	mux.HandleFunc("PUT /users/{user}/faults/imap", sim.handleIMAPFault)
	mux.HandleFunc("DELETE /users/{user}/faults/imap", func(w http.ResponseWriter, r *http.Request) {
		sim.stateOf(r.PathValue("user")).clearIMAPFault()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("PUT /users/{user}/faults/smtp", sim.handleSMTPFault)
	mux.HandleFunc("DELETE /users/{user}/faults/smtp", func(w http.ResponseWriter, r *http.Request) {
		sim.stateOf(r.PathValue("user")).clearSMTP()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("GET /smtp/messages", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sim.ListSMTP())
	})
	mux.HandleFunc("DELETE /smtp/messages", func(w http.ResponseWriter, _ *http.Request) {
		sim.ClearSMTP()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("POST /fleet", sim.handleFleet)
	mux.HandleFunc("DELETE /fleet", func(w http.ResponseWriter, _ *http.Request) {
		sim.StopFleet()
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("GET /pec/ca.pem", sim.handlePECCA)
	mux.HandleFunc("GET /pec/receipts", sim.handlePECReceipts)
	mux.HandleFunc("PUT /pec/users/{user}/delay", sim.handlePECDelay)
	mux.HandleFunc("PUT /pec/users/{user}/force-receipt", sim.handlePECForce)
	mux.HandleFunc("DELETE /pec/users/{user}/force-receipt", func(w http.ResponseWriter, r *http.Request) {
		if sim.pec == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "pec mode off"})
			return
		}
		sim.pec.ClearForceReceipt(r.PathValue("user"))
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	return mux
}

func (sim *Simulator) handlePECCA(w http.ResponseWriter, _ *http.Request) {
	if sim.pec == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pec mode off"})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sim.pec.CAPEM())
}

func (sim *Simulator) handlePECReceipts(w http.ResponseWriter, r *http.Request) {
	if sim.pec == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pec mode off"})
		return
	}
	writeJSON(w, http.StatusOK, sim.pec.ListReceipts(r.URL.Query().Get("user")))
}

func (sim *Simulator) handlePECDelay(w http.ResponseWriter, r *http.Request) {
	if sim.pec == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pec mode off"})
		return
	}
	var body struct {
		DelayMs int64 `json:"delayMs"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	sim.pec.SetDeliveryDelay(r.PathValue("user"), time.Duration(body.DelayMs)*time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handlePECForce(w http.ResponseWriter, r *http.Request) {
	if sim.pec == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pec mode off"})
		return
	}
	var body struct {
		Type string `json:"type"`
	}
	if err := readJSON(r, &body); err != nil || body.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type required"})
		return
	}
	sim.pec.ForceNextReceipt(r.PathValue("user"), body.Type)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func (sim *Simulator) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil || body.Username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required"})
		return
	}
	if body.Password == "" {
		body.Password = "pass"
	}
	u, err := sim.CreateUser(body.Username, body.Password)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"username": u.Username(), "mailboxes": u.MailboxNames()})
}

func (sim *Simulator) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !sim.DeleteUser(r.PathValue("user")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleCreateMailbox(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Name       string   `json:"name"`
		SpecialUse []string `json:"specialUse"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	var opts *imap.CreateOptions
	if len(body.SpecialUse) > 0 {
		attrs := make([]imap.MailboxAttr, len(body.SpecialUse))
		for i, a := range body.SpecialUse {
			attrs[i] = imap.MailboxAttr(a)
		}
		opts = &imap.CreateOptions{SpecialUse: attrs}
	}
	if err := u.Create(body.Name, opts); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})
}

func (sim *Simulator) handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if err := u.Delete(body.Name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleInject(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Mailbox      string    `json:"mailbox"`
		Raw          string    `json:"raw"`
		Flags        []string  `json:"flags"`
		InternalDate time.Time `json:"internalDate"`
	}
	if err := readJSON(r, &body); err != nil || body.Raw == "" || body.Mailbox == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mailbox and raw required"})
		return
	}
	uid, uidv, err := u.InjectMessage(body.Mailbox, []byte(body.Raw), body.Flags, body.InternalDate)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"uid": uid, "uidValidity": uidv})
}

func (sim *Simulator) handleFlags(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Mailbox string   `json:"mailbox"`
		UIDs    []uint32 `json:"uids"`
		Flags   []string `json:"flags"`
		Op      string   `json:"op"`
	}
	if err := readJSON(r, &body); err != nil || body.Mailbox == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	mbox, err := u.Mailbox(body.Mailbox)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	uids := make([]imap.UID, len(body.UIDs))
	for i, u := range body.UIDs {
		uids[i] = imap.UID(u)
	}
	flags := make([]imap.Flag, len(body.Flags))
	for i, f := range body.Flags {
		flags[i] = imap.Flag(f)
	}
	op := imap.StoreFlagsAdd
	switch strings.ToLower(body.Op) {
	case "remove", "del", "-":
		op = imap.StoreFlagsDel
	case "set":
		op = imap.StoreFlagsSet
	}
	mbox.SetFlags(uids, flags, op)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleExpunge(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Mailbox string   `json:"mailbox"`
		UIDs    []uint32 `json:"uids"`
	}
	if err := readJSON(r, &body); err != nil || body.Mailbox == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mailbox required"})
		return
	}
	mbox, err := u.Mailbox(body.Mailbox)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	var set *imap.UIDSet
	if len(body.UIDs) > 0 {
		s := imap.UIDSetNum(uidSlice(body.UIDs)...)
		set = &s
	}
	_ = mbox.ExpungeDeleted(set)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func uidSlice(u []uint32) []imap.UID {
	out := make([]imap.UID, len(u))
	for i, x := range u {
		out[i] = imap.UID(x)
	}
	return out
}

func (sim *Simulator) handleRename(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Name    string `json:"name"`
		NewName string `json:"newName"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" || body.NewName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and newName required"})
		return
	}
	if err := u.Rename(body.Name, body.NewName, nil); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleRecreate(w http.ResponseWriter, r *http.Request) {
	u := sim.store.User(r.PathValue("user"))
	if u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	uidv, err := u.RecreateFolder(body.Name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uidValidity": uidv})
}

func (sim *Simulator) handleCaps(w http.ResponseWriter, r *http.Request) {
	var c Caps
	if err := readJSON(r, &c); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	sim.stateOf(r.PathValue("user")).setCaps(c)
	writeJSON(w, http.StatusOK, c)
}

func (sim *Simulator) handleGetCaps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sim.stateOf(r.PathValue("user")).getCaps())
}

func (sim *Simulator) handleIMAPFault(w http.ResponseWriter, r *http.Request) {
	var f IMAPFault
	if err := readJSON(r, &f); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	sim.stateOf(r.PathValue("user")).setIMAPFault(&f)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleSMTPFault(w http.ResponseWriter, r *http.Request) {
	var script SMTPScript
	if err := readJSON(r, &script); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	sim.stateOf(r.PathValue("user")).setSMTP(script)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (sim *Simulator) handleFleet(w http.ResponseWriter, r *http.Request) {
	var opt FleetOptions
	if err := readJSON(r, &opt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// Detach from the HTTP request context — fleet outlives the control call.
	if err := sim.StartFleet(context.Background(), opt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
