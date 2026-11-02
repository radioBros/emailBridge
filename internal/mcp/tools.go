package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Gateway holds dependencies for MCP tools.
type Gateway struct {
	Accounts   Accounts
	Messages   Messages
	Submit     Submitter
	Deliveries Deliveries
	Events     Events
	Audit      Auditor
	AI         AIOps
	// API serves generated tools through the full HTTP stack (the httpapi server); nil → none.
	API http.Handler
	// PIIRedact / BodyMax are resolved per-request via ResolvePII.
	ResolvePII func(ctx context.Context) (redact bool, maxChars int)
}

func (g *Gateway) audit(ctx context.Context, tool string, ok bool, errMsg string) {
	if g.Audit == nil {
		return
	}
	ch := audit.Changes{"tool": {After: tool}, "ok": {After: ok}}
	if errMsg != "" {
		ch["error"] = audit.FieldChange{After: errMsg}
	}
	_ = g.Audit.Record(ctx, "mcp.tool", audit.Target{Type: "mcp", ID: tool}, ch)
}

func (g *Gateway) pii(ctx context.Context) (bool, int) {
	if g.ResolvePII != nil {
		return g.ResolvePII(ctx)
	}
	return true, 4000
}

func (g *Gateway) result(ctx context.Context, tool string, payload any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		msg := err.Error()
		g.audit(ctx, tool, false, msg)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "error: " + msg}},
			IsError: true,
		}, nil, nil
	}
	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		g.audit(ctx, tool, false, marshalErr.Error())
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "error: marshal failed"}},
			IsError: true,
		}, nil, nil
	}
	text := string(raw)
	if redact, max := g.pii(ctx); redact {
		text = RedactJSON(text, max)
	}
	g.audit(ctx, tool, true, "")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func requireScope(ctx context.Context, want string) error {
	c, ok := tenancy.CallerFrom(ctx)
	if !ok || c.Kind != tenancy.CallerAPIKey {
		return fmt.Errorf("API key required")
	}
	if !hasScope(c.Scopes, want) {
		return fmt.Errorf("missing scope %s", want)
	}
	return nil
}

func hasScope(scopes []string, want string) bool {
	if want == "" {
		return false
	}
	for _, s := range scopes {
		if s == "admin" || s == want {
			return true
		}
		res, mode, ok := strings.Cut(want, ":")
		if !ok {
			continue
		}
		if mode == "read" && s == res+":write" {
			return true
		}
		// deliveries:read satisfied by webhooks:read/write
		if want == "deliveries:read" && (s == "webhooks:read" || s == "webhooks:write") {
			return true
		}
		// messages:read satisfied by accounts:read/write
		if want == "messages:read" && (s == "accounts:read" || s == "accounts:write") {
			return true
		}
		if want == "events:read" && (s == "events:write") {
			return true
		}
	}
	return false
}

type listAccountsIn struct {
	Limit int    `json:"limit,omitempty" jsonschema:"max accounts to return"`
	Q     string `json:"q,omitempty" jsonschema:"search name or email"`
}

func (g *Gateway) listAccounts(ctx context.Context, _ *mcp.CallToolRequest, in listAccountsIn) (*mcp.CallToolResult, any, error) {
	const tool = "list_accounts"
	if err := requireScope(ctx, "accounts:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Accounts == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("accounts service unavailable"))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	list, err := g.Accounts.List(ctx, accounts.ListFilter{Limit: limit, Q: in.Q, Sort: "id"})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	type row struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		State string `json:"state"`
	}
	out := make([]row, 0, len(list.Data))
	for _, a := range list.Data {
		out = append(out, row{ID: a.ID, Name: a.Name, Email: a.Email, State: a.State})
	}
	return g.result(ctx, tool, out, nil)
}

type listMessagesIn struct {
	AccountID string `json:"accountId" jsonschema:"account id acc_…"`
	MailboxID string `json:"mailboxId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Q         string `json:"q,omitempty"`
}

func (g *Gateway) listMessages(ctx context.Context, _ *mcp.CallToolRequest, in listMessagesIn) (*mcp.CallToolResult, any, error) {
	const tool = "list_messages"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Messages == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("messages service unavailable"))
	}
	if in.AccountID == "" {
		return g.result(ctx, tool, nil, fmt.Errorf("accountId required"))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	res, err := g.Messages.ListMessages(ctx, in.AccountID, messages.ListFilter{
		MailboxID: in.MailboxID, Limit: limit, Q: in.Q, Sort: "-internalDate",
	})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, summariseMessages(res.Data), nil)
}

type searchMessagesIn struct {
	AccountID string `json:"accountId"`
	Text      string `json:"text,omitempty"`
	Subject   string `json:"subject,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	MailboxID string `json:"mailboxId,omitempty"`
}

func (g *Gateway) searchMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchMessagesIn) (*mcp.CallToolResult, any, error) {
	const tool = "search_messages"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Messages == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("messages service unavailable"))
	}
	if in.AccountID == "" {
		return g.result(ctx, tool, nil, fmt.Errorf("accountId required"))
	}
	views, err := g.Messages.SearchMessages(ctx, in.AccountID, messages.SearchInput{
		MailboxID: in.MailboxID, Text: in.Text, Subject: in.Subject, From: in.From, To: in.To,
	})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, summariseMessages(views), nil)
}

type getMessageIn struct {
	AccountID string `json:"accountId"`
	MessageID string `json:"messageId"`
	Text      string `json:"text,omitempty" jsonschema:"plain (default), html, or both"`
}

func (g *Gateway) getMessage(ctx context.Context, _ *mcp.CallToolRequest, in getMessageIn) (*mcp.CallToolResult, any, error) {
	const tool = "get_message"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Messages == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("messages service unavailable"))
	}
	if in.AccountID == "" || in.MessageID == "" {
		return g.result(ctx, tool, nil, fmt.Errorf("accountId and messageId required"))
	}
	text := in.Text
	if text == "" {
		text = "plain"
	}
	v, err := g.Messages.GetMessage(ctx, in.AccountID, in.MessageID, messages.GetOptions{Text: text})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, v, nil)
}

type sendMessageIn struct {
	AccountID string   `json:"accountId"`
	To        []string `json:"to" jsonschema:"recipient addresses"`
	Subject   string   `json:"subject"`
	Text      string   `json:"text,omitempty"`
	HTML      string   `json:"html,omitempty"`
}

func (g *Gateway) sendMessage(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageIn) (*mcp.CallToolResult, any, error) {
	const tool = "send_message"
	if err := requireScope(ctx, "outbound:write"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Submit == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("submit service unavailable"))
	}
	if in.AccountID == "" || len(in.To) == 0 {
		return g.result(ctx, tool, nil, fmt.Errorf("accountId and to required"))
	}
	to := make([]submit.AddressInput, 0, len(in.To))
	for _, a := range in.To {
		to = append(to, submit.AddressInput{Address: a})
	}
	res, err := g.Submit.Submit(ctx, in.AccountID, submit.SubmitRequest{
		To: to, Subject: in.Subject, Text: in.Text, HTML: in.HTML,
	}, "", false)
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	out := map[string]any{}
	if res != nil && res.Accepted != nil {
		out["id"] = res.Accepted.ID
		out["state"] = res.Accepted.State
	}
	return g.result(ctx, tool, out, nil)
}

type listDeliveriesIn struct {
	Limit      int    `json:"limit,omitempty"`
	AccountID  string `json:"accountId,omitempty"`
	EndpointID string `json:"endpointId,omitempty"`
}

func (g *Gateway) listDeliveries(ctx context.Context, _ *mcp.CallToolRequest, in listDeliveriesIn) (*mcp.CallToolResult, any, error) {
	const tool = "list_deliveries"
	if err := requireScope(ctx, "deliveries:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Deliveries == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("deliveries service unavailable"))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	list, err := g.Deliveries.ListDeliveries(ctx, webhook.DeliveryFilter{
		Limit: limit, AccountID: in.AccountID, EndpointID: in.EndpointID, Sort: "-createdAt",
	})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, list.Data, nil)
}

type listEventsIn struct {
	Limit     int      `json:"limit,omitempty"`
	AccountID string   `json:"accountId,omitempty"`
	Types     []string `json:"types,omitempty"`
}

func (g *Gateway) listEvents(ctx context.Context, _ *mcp.CallToolRequest, in listEventsIn) (*mcp.CallToolResult, any, error) {
	const tool = "list_events"
	if err := requireScope(ctx, "events:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.Events == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("events service unavailable"))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	list, err := g.Events.List(ctx, events.ListFilter{
		Limit: limit, AccountID: in.AccountID, Type: in.Types, Sort: "-time",
	})
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, list.Data, nil)
}

func summariseMessages(views []messages.MessageView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, m := range views {
		row := map[string]any{
			"id": m.ID, "accountId": m.AccountID, "mailboxId": m.MailboxID,
			"subject": m.Subject, "internalDate": m.InternalDate, "size": m.Size, "flags": m.Flags,
		}
		if len(m.From) > 0 {
			row["from"] = m.From
		}
		out = append(out, row)
	}
	return out
}

type aiMsgIn struct {
	AccountID string   `json:"accountId"`
	MessageID string   `json:"messageId"`
	Labels    []string `json:"labels,omitempty"`
	Schema    string   `json:"schema,omitempty"`
}

func (g *Gateway) aiSummarize(ctx context.Context, _ *mcp.CallToolRequest, in aiMsgIn) (*mcp.CallToolResult, any, error) {
	const tool = "ai_summarize"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.AI == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("ai service unavailable"))
	}
	sum, err := g.AI.SummarizeMessage(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, map[string]string{"summary": sum}, nil)
}

func (g *Gateway) aiClassify(ctx context.Context, _ *mcp.CallToolRequest, in aiMsgIn) (*mcp.CallToolResult, any, error) {
	const tool = "ai_classify"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.AI == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("ai service unavailable"))
	}
	label, err := g.AI.ClassifyMessage(ctx, in.AccountID, in.MessageID, in.Labels)
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, map[string]string{"label": label}, nil)
}

func (g *Gateway) aiExtract(ctx context.Context, _ *mcp.CallToolRequest, in aiMsgIn) (*mcp.CallToolResult, any, error) {
	const tool = "ai_extract"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.AI == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("ai service unavailable"))
	}
	data, err := g.AI.ExtractMessage(ctx, in.AccountID, in.MessageID, in.Schema)
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, map[string]string{"data": data}, nil)
}

func (g *Gateway) aiRisk(ctx context.Context, _ *mcp.CallToolRequest, in aiMsgIn) (*mcp.CallToolResult, any, error) {
	const tool = "ai_risk"
	if err := requireScope(ctx, "messages:read"); err != nil {
		return g.result(ctx, tool, nil, err)
	}
	if g.AI == nil {
		return g.result(ctx, tool, nil, fmt.Errorf("ai service unavailable"))
	}
	risk, rationale, err := g.AI.RiskMessage(ctx, in.AccountID, in.MessageID)
	if err != nil {
		return g.result(ctx, tool, nil, err)
	}
	return g.result(ctx, tool, map[string]string{"risk": risk, "rationale": rationale}, nil)
}

// curatedToolNames are the hand-written tools; generated tools with the same name are skipped.
var curatedToolNames = map[string]bool{
	"list_accounts": true, "list_messages": true, "search_messages": true, "get_message": true,
	"send_message": true, "list_deliveries": true, "list_events": true,
	"ai_summarize": true, "ai_classify": true, "ai_extract": true, "ai_risk": true,
}

// NewServer builds the MCP server with the curated EmailBridge tools.
func NewServer(g *Gateway) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "emailbridge", Version: "1.0.0"}, nil)
	addCurated(srv, g)
	return srv
}

// newToolsetServer builds a server with the curated tools (when selected) and the generated tools of the
// selected toolsets (nil = all).
func newToolsetServer(g *Gateway, gen []*genTool, sets map[string]bool) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "emailbridge", Version: "1.0.0"}, nil)
	if sets == nil || sets[ToolsetCurated] {
		addCurated(srv, g)
	}
	for _, t := range gen {
		if sets != nil && !sets[t.Toolset] {
			continue
		}
		tool := t
		srv.AddTool(tool.Tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.call(ctx, tool, req)
		})
	}
	return srv
}

func addCurated(srv *mcp.Server, g *Gateway) {
	mcp.AddTool(srv, &mcp.Tool{Name: "list_accounts", Description: "List mail accounts in the organisation"}, g.listAccounts)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_messages", Description: "List indexed messages for an account"}, g.listMessages)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_messages", Description: "Search messages in an account"}, g.searchMessages)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_message", Description: "Get one message (text only by default)"}, g.getMessage)
	mcp.AddTool(srv, &mcp.Tool{Name: "send_message", Description: "Send a message (requires outbound:write; no attachments)"}, g.sendMessage)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_deliveries", Description: "List webhook deliveries"}, g.listDeliveries)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_events", Description: "List CloudEvents"}, g.listEvents)
	mcp.AddTool(srv, &mcp.Tool{Name: "ai_summarize", Description: "Summarise a message with the AI plugin"}, g.aiSummarize)
	mcp.AddTool(srv, &mcp.Tool{Name: "ai_classify", Description: "Classify a message with the AI plugin"}, g.aiClassify)
	mcp.AddTool(srv, &mcp.Tool{Name: "ai_extract", Description: "Extract structured data from a message"}, g.aiExtract)
	mcp.AddTool(srv, &mcp.Tool{Name: "ai_risk", Description: "Assess message risk with the AI plugin"}, g.aiRisk)
}
