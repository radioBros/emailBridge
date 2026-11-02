package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P7")
	httpapi.Register(server, huma.Operation{
		OperationID: "listNotifications", Method: http.MethodGet, Path: "/v1/notifications",
		Summary: "List notifications", Tags: []string{"notifications"},
		Extensions: httpapi.WithScope(phase, "notifications:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "getNotificationsSummary", Method: http.MethodGet, Path: "/v1/notifications/summary",
		Summary: "Notification bell summary", Tags: []string{"notifications"},
		Extensions: httpapi.WithScope(phase, "notifications:read"),
	}, m.summary)
	httpapi.Register(server, huma.Operation{
		OperationID: "markNotificationsRead", Method: http.MethodPost, Path: "/v1/notifications/read",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Mark notifications read", Tags: []string{"notifications"},
		Extensions: httpapi.WithScope(phase, "notifications:write"),
	}, m.markRead)
	httpapi.Register(server, huma.Operation{
		OperationID: "markAllNotificationsRead", Method: http.MethodPost, Path: "/v1/notifications/read-all",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Mark all notifications read", Tags: []string{"notifications"},
		Extensions: httpapi.WithScope(phase, "notifications:write"),
	}, m.markAllRead)
	httpapi.Register(server, huma.Operation{
		OperationID: "resolveNotification", Method: http.MethodPost, Path: "/v1/notifications/{notificationId}/resolve",
		Summary: "Manually resolve a notification", Tags: []string{"notifications"},
		Extensions: httpapi.WithScope(phase, "notifications:write"),
	}, m.resolve)
}

type listIn struct {
	Page         int    `query:"page"`
	Limit        int    `query:"limit"`
	IncludeTotal bool   `query:"includeTotal"`
	Sort         string `query:"sort"`
	State        string `query:"state"`
	Category     string `query:"category"`
	Severity     string `query:"severity"`
	Unread       string `query:"unread"` // "true" | "false" | empty
	From         string `query:"from" doc:"Only notifications with lastSeen ≥ this RFC 3339 instant."`
	To           string `query:"to" doc:"Only notifications with lastSeen ≤ this RFC 3339 instant."`
}

type listOut struct {
	Body Page
}

func (m *Module) list(ctx context.Context, in *listIn) (*listOut, error) {
	f := ListFilter{
		Page: in.Page, Limit: in.Limit, IncludeTotal: in.IncludeTotal, Sort: in.Sort,
		State: in.State, Category: in.Category, Severity: in.Severity,
	}
	switch in.Unread {
	case "true", "1":
		v := true
		f.Unread = &v
	case "false", "0":
		v := false
		f.Unread = &v
	}
	if in.From != "" {
		t, err := parseRFC3339(in.From)
		if err != nil {
			return nil, err
		}
		f.From = &t
	}
	if in.To != "" {
		t, err := parseRFC3339(in.To)
		if err != nil {
			return nil, err
		}
		f.To = &t
	}
	page, err := m.svc.List(ctx, f)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *page}, nil
}

func parseRFC3339(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return time.Time{}, errs.New(errs.CodeValidationFailed, "from/to must be RFC 3339 timestamps.")
	}
	return t.UTC(), nil
}

type summaryOut struct {
	Body Summary
}

func (m *Module) summary(ctx context.Context, _ *struct{}) (*summaryOut, error) {
	sum, err := m.svc.Summary(ctx)
	if err != nil {
		return nil, err
	}
	return &summaryOut{Body: *sum}, nil
}

type markReadIn struct {
	Body struct {
		IDs []string `json:"ids"`
	}
}

func (m *Module) markRead(ctx context.Context, in *markReadIn) (*struct{}, error) {
	if err := m.svc.MarkRead(ctx, in.Body.IDs); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) markAllRead(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if err := m.svc.MarkAllRead(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}

type resolveIn struct {
	NotificationID string `path:"notificationId"`
}

type resolveOut struct {
	Body Notification
}

func (m *Module) resolve(ctx context.Context, in *resolveIn) (*resolveOut, error) {
	n, err := m.svc.Resolve(ctx, in.NotificationID)
	if err != nil {
		return nil, err
	}
	return &resolveOut{Body: *n}, nil
}
