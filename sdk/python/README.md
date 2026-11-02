# `emailbridge` (Python SDK)

Sync and async HTTP clients (`httpx`), Standard Webhooks / `bodyOnly` verifier, and SSE helper.

Requires Python ≥ 3.10.

## Install

```bash
pip install emailbridge
# from this tree:
pip install -e ".[dev]"
```

## Quick start

```python
from emailbridge import Client, verify, stream_events

with Client(
    base_url="https://mail.example.com",
    api_key="ebk_…",
    organization_id="org_…",
) as client:
    page = client.get("/v1/accounts", params={"limit": 50})
    for row in client.iterate_cursor("/v1/accounts", limit=100):
        print(row)

verify(secret, request.headers, body)

stream_events(client, last_event_id="evt_…", on_event=print)
```

Async twin: `AsyncClient` + `stream_events_async`.

## Regenerate

```bash
./scripts/generate
```

## Tests

```bash
pip install -e ".[dev]"
pytest
```
