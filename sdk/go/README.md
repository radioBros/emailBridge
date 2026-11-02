# EmailBridge Go SDK

Typed API client (generated from OpenAPI), webhook verification, and CloudEvents
envelope helpers.

```bash
go get github.com/radiobros/emailbridge/sdk/go@latest
```

## Quick start

```go
package main

import (
	"context"
	"log"

	"github.com/radiobros/emailbridge/sdk/go/client"
	"github.com/radiobros/emailbridge/sdk/go/openapi"
	"github.com/radiobros/emailbridge/sdk/go/webhook"
)

func main() {
	c := client.New(client.Options{
		BaseURL:        "https://mail.example.com",
		APIKey:         "ebk_…",
		OrganizationID: "org_…", // optional for API keys that imply org
	})

	var page client.Page[openapi.AccountView]
	if err := c.Get(context.Background(), "/v1/accounts", &page, client.RequestOptions{
		Query: map[string]string{"limit": "50"},
	}); err != nil {
		log.Fatal(err)
	}

	for row, err := range client.IterateCursor[openapi.AccountView](context.Background(), c, "/v1/accounts", client.IterateOptions{Limit: 100}) {
		if err != nil {
			log.Fatal(err)
		}
		log.Println(row.Id)
	}

	// Typed generated client (optional) — same OpenAPI operations:
	// oc, _ := openapi.NewClientWithResponses(c's base URL, openapi.WithRequestEditorFn(...))

	// Webhook (Standard Webhooks)
	_ = webhook.Verify
}
```

SSE resume:

```go
err := c.StreamEvents(ctx, client.StreamEventsOptions{
	LastEventID: "evt_…",
	OnEvent: func(ev client.SSEEvent) error {
		log.Println(ev.Event, ev.Data)
		return nil
	},
})
```

## Errors and retries

Non-2xx responses decode as `*client.ProblemError` (RFC 9457 fields: `code`,
`kind`, `traceId`, …). Status `429` and `503` are retried (default 3) honouring
`Retry-After`.

## Regenerate OpenAPI client

From this directory (needs `api/openapi/openapi.yaml` in the repo):

```bash
go generate ./openapi/...
```

CI regenerates and fails on drift of `openapi/client.gen.go`.
