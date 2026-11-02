from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import httpx

from .client import AsyncClient, Client


@dataclass
class SSEEvent:
    id: str | None
    event: str | None
    data: str


def _parse_block(block: str) -> SSEEvent | None:
    eid: str | None = None
    etype: str | None = None
    data_lines: list[str] = []
    for line in block.split("\n"):
        if not line or line.startswith(":"):
            continue
        if ":" in line:
            field, value = line.split(":", 1)
            if value.startswith(" "):
                value = value[1:]
        else:
            field, value = line, ""
        if field == "id":
            eid = value
        elif field == "event":
            etype = value
        elif field == "data":
            data_lines.append(value)
    if eid is None and etype is None and not data_lines:
        return None
    return SSEEvent(id=eid, event=etype, data="\n".join(data_lines))


def _iter_blocks(text_iter: Any) -> Any:
    buf = ""
    for chunk in text_iter:
        buf += chunk if isinstance(chunk, str) else chunk.decode("utf-8")
        while "\n\n" in buf:
            block, buf = buf.split("\n\n", 1)
            yield block.replace("\r", "")


def stream_events(
    client: Client,
    *,
    on_event: Callable[[SSEEvent], None],
    types: str | None = None,
    account_id: str | None = None,
    last_event_id: str | None = None,
) -> None:
    params: dict[str, str] = {}
    if types:
        params["types"] = types
    if account_id:
        params["accountId"] = account_id
    headers = {
        "Accept": "text/event-stream",
        "Authorization": f"Bearer {client.api_key}",
    }
    if client.organization_id:
        headers["EB-Organization"] = client.organization_id
    if last_event_id:
        headers["Last-Event-ID"] = last_event_id
    with client._client.stream("GET", "/v1/events/stream", params=params, headers=headers) as res:
        res.raise_for_status()
        for block in _iter_blocks(res.iter_text()):
            ev = _parse_block(block)
            if ev:
                on_event(ev)


async def stream_events_async(
    client: AsyncClient,
    *,
    on_event: Callable[[SSEEvent], Any],
    types: str | None = None,
    account_id: str | None = None,
    last_event_id: str | None = None,
) -> None:
    params: dict[str, str] = {}
    if types:
        params["types"] = types
    if account_id:
        params["accountId"] = account_id
    headers = {
        "Accept": "text/event-stream",
        "Authorization": f"Bearer {client.api_key}",
    }
    if client.organization_id:
        headers["EB-Organization"] = client.organization_id
    if last_event_id:
        headers["Last-Event-ID"] = last_event_id
    async with client._client.stream("GET", "/v1/events/stream", params=params, headers=headers) as res:
        res.raise_for_status()
        buf = ""
        async for chunk in res.aiter_text():
            buf += chunk
            while "\n\n" in buf:
                block, buf = buf.split("\n\n", 1)
                ev = _parse_block(block.replace("\r", ""))
                if ev:
                    maybe = on_event(ev)
                    if hasattr(maybe, "__await__"):
                        await maybe
