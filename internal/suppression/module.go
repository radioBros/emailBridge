package suppression

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires suppression CRUD on API nodes.
type Module struct {
	Registry *features.Registry
	Env      string
	Log      *slog.Logger
	svc      *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "suppression" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	_ = log
	m.svc = &Service{
		Repo: &Repo{DB: db}, Registry: m.Registry, Env: m.Env,
	}
	app.Provide(a, m.svc)
	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(srv)
		srv.Router.Post("/v1/suppression-lists/{listId}/entries:import", m.serveImport)
		srv.Router.Get("/v1/suppression-lists/{listId}/entries:export", m.serveExport)
		m.documentCSV(srv)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

func (m *Module) serveImport(w http.ResponseWriter, r *http.Request) {
	listID := chi.URLParam(r, "listId")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes+1))
	if err != nil {
		httpapi.WriteProblem(w, r, errs.New(errs.CodeMalformedRequest, "Cannot read body."))
		return
	}
	if len(body) > maxImportBytes {
		// Never import a silently truncated file.
		httpapi.WriteProblem(w, r, errs.New(errs.CodePayloadTooLarge, "The CSV body exceeds 2 MiB.").
			WithSafe("limit", maxImportBytes))
		return
	}
	res, err := m.svc.ImportCSV(r.Context(), listID, strings.NewReader(string(body)))
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func (m *Module) serveExport(w http.ResponseWriter, r *http.Request) {
	listID := chi.URLParam(r, "listId")
	// Build the whole file first: a failure is answered as a problem, never as a half-written attachment.
	var buf bytes.Buffer
	if err := m.svc.ExportCSV(r.Context(), listID, &buf); err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="suppression.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// maxImportBytes caps a CSV import body (2 MiB); a larger body is 413 payload_too_large.
const maxImportBytes = 2 << 20
