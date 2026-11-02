package autoconfig

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// Module wires GET /v1/autoconfig on API nodes.
type Module struct {
	Log *slog.Logger
	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "autoconfig" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	// Discovery targets are public ISP hosts derived from any email domain (the connect page is public), so
	// it never reaches private networks even where the instance allows them for webhooks (S10 §8).
	dialer, _ := app.Get[*egress.Dialer](a)
	if dialer == nil {
		dialer = egress.NewDialer(egress.Policy{})
	}
	m.svc = NewService(dialer) // NewService narrows it to PublicOnly
	app.Provide(a, m.svc)
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	m.register(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

func (m *Module) register(server *httpapi.Server) {
	phase := httpapi.Phase("P6")
	httpapi.Register(server, huma.Operation{
		OperationID: "getAutoconfig", Method: http.MethodGet, Path: "/v1/autoconfig",
		Summary: "Discover IMAP/SMTP settings", Tags: []string{"autoconfig"},
		Description: "Looks up the IMAP and SMTP settings for an email address so a setup form can prefill them: " +
			"the built-in provider list first, then Mozilla ISPDB, Autodiscover, RFC 6186 SRV records and finally a " +
			"guess from the MX host (`source` says which answered; `none` with no servers when nothing was found). " +
			"`oauthHint` tells when the provider prefers OAuth (Gmail, Microsoft 365), `pec` / `pecDefaults` when the " +
			"domain is a known PEC provider. Results are cached; nothing is stored and no login is tried (use " +
			"`POST /v1/accounts/verify` for that). Outbound lookups go through the egress policy. Limited to 30 " +
			"lookups per minute per organisation. Requires the `read` permission (API keys `accounts:read`).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden}, httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "A valid email address is required."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Autoconfig is limited to 30 requests per minute per organisation."}),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {
			Examples: map[string]*huma.Example{
				"ispdb": {Summary: "Settings from Mozilla ISPDB", Value: map[string]any{
					"email": "maria.rossi@example.com", "domain": "example.com", "source": "ispdb",
					"imap": map[string]any{"host": "imap.example.com", "port": 993, "tls": "implicit", "user": "%EMAIL%"},
					"smtp": map[string]any{"host": "smtp.example.com", "port": 465, "tls": "implicit", "user": "%EMAIL%"},
				}},
				"oauth": {Summary: "Microsoft 365 via Autodiscover (OAuth preferred)", Value: map[string]any{
					"email": "sales@example.org", "domain": "example.org", "source": "autodiscover", "oauthHint": "outlook",
					"imap": map[string]any{"host": "outlook.office365.com", "port": 993, "tls": "implicit", "user": "%EMAIL%"},
					"smtp": map[string]any{"host": "smtp.office365.com", "port": 587, "tls": "starttls", "user": "%EMAIL%"},
				}},
				"pec": {Summary: "Known PEC provider", Value: map[string]any{
					"email": "studio.rossi@pec.example.it", "domain": "pec.example.it", "source": "builtin", "pec": true,
					"imap": map[string]any{"host": "imaps.pec.example.it", "port": 993, "tls": "implicit", "user": "%EMAIL%"},
					"smtp": map[string]any{"host": "smtps.pec.example.it", "port": 465, "tls": "implicit", "user": "%EMAIL%"},
					"pecDefaults": map[string]any{"switches": map[string]any{"pec.enabled": true},
						"syncPaths": []any{"INBOX"}, "sendPolicy": "atMostOnce"},
				}},
			}}}}},
		Extensions: httpapi.WithScope(phase, "accounts:read"),
	}, m.get)
}

type getIn struct {
	Email string `query:"email" doc:"Mailbox address to look up." example:"maria.rossi@example.com"`
}

type getOut struct {
	Body Result
}

func (m *Module) get(ctx context.Context, in *getIn) (*getOut, error) {
	r, err := m.svc.DiscoverForOrg(ctx, in.Email)
	if err != nil {
		return nil, err
	}
	return &getOut{Body: *r}, nil
}
