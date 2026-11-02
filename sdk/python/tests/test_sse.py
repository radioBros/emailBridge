from __future__ import annotations

import httpx

from emailbridge import Client, SSEEvent, stream_events


def test_stream_events_last_event_id() -> None:
    seen: dict[str, str] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["last"] = request.headers.get("Last-Event-ID", "")
        body = 'id: evt_1\nevent: message.received\ndata: {"id":"evt_1"}\n\n:keepalive\n\n'
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, content=body.encode())

    transport = httpx.MockTransport(handler)
    events: list[SSEEvent] = []
    with Client(base_url="http://eb.test", api_key="ebk_x", transport=transport) as c:
        stream_events(c, last_event_id="evt_0", on_event=events.append)
    assert seen["last"] == "evt_0"
    assert len(events) == 1
    assert events[0].id == "evt_1"
    assert events[0].event == "message.received"
