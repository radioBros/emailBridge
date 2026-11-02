from __future__ import annotations

import time
from collections.abc import AsyncIterator, Iterator, Mapping
from dataclasses import dataclass
from email.utils import parsedate_to_datetime
from typing import Any

import httpx

from .problem import ProblemError, parse_problem


@dataclass
class Page:
    data: list[Any] | None
    next_cursor: str | None
    total: int | None = None
    total_capped: bool | None = None

    @classmethod
    def from_json(cls, body: dict[str, Any]) -> Page:
        return cls(
            data=body.get("data"),
            next_cursor=body.get("nextCursor"),
            total=body.get("total"),
            total_capped=body.get("totalCapped"),
        )


def _retry_after_ms(headers: httpx.Headers, attempt: int) -> float:
    ra = headers.get("Retry-After")
    if ra:
        try:
            return max(0.0, float(ra) * 1000)
        except ValueError:
            try:
                when = parsedate_to_datetime(ra).timestamp()
                return max(0.0, (when - time.time()) * 1000)
            except (TypeError, ValueError, IndexError):
                pass
    return min(30_000.0, 500 * (2**attempt))


class _Base:
    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        organization_id: str | None = None,
        max_retries: int = 3,
        headers: Mapping[str, str] | None = None,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.organization_id = organization_id
        self.max_retries = max_retries
        self._extra_headers = dict(headers or {})

    def _headers(self, extra: Mapping[str, str] | None, idempotency_key: str | None) -> dict[str, str]:
        h = {
            "Accept": "application/json",
            "Authorization": f"Bearer {self.api_key}",
            **self._extra_headers,
            **(dict(extra) if extra else {}),
        }
        if self.organization_id and "EB-Organization" not in h:
            h["EB-Organization"] = self.organization_id
        if idempotency_key:
            h["Idempotency-Key"] = idempotency_key
        return h


class Client(_Base):
    def __init__(self, **kwargs: Any) -> None:
        transport = kwargs.pop("transport", None)
        timeout = kwargs.pop("timeout", 30.0)
        super().__init__(**kwargs)
        self._client = httpx.Client(base_url=self.base_url, timeout=timeout, transport=transport)

    def close(self) -> None:
        self._client.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

    def request(
        self,
        method: str,
        path: str,
        *,
        params: Mapping[str, Any] | None = None,
        json: Any = None,
        headers: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
    ) -> Any:
        hdrs = self._headers(headers, idempotency_key)
        if json is not None:
            hdrs.setdefault("Content-Type", "application/json")
        last: Exception | None = None
        for attempt in range(self.max_retries + 1):
            res = self._client.request(method, path, params=params, json=json, headers=hdrs)
            if res.status_code in (429, 503) and attempt < self.max_retries:
                time.sleep(_retry_after_ms(res.headers, attempt) / 1000.0)
                continue
            if res.status_code in (204, 205):
                return None
            body: Any
            try:
                body = res.json() if res.content else None
            except ValueError:
                body = res.text
            if res.is_error:
                raise ProblemError(parse_problem(body, res.status_code), dict(res.headers))
            return body
        assert last is not None
        raise last

    def get(self, path: str, **kw: Any) -> Any:
        return self.request("GET", path, **kw)

    def post(self, path: str, **kw: Any) -> Any:
        return self.request("POST", path, **kw)

    def patch(self, path: str, **kw: Any) -> Any:
        return self.request("PATCH", path, **kw)

    def put(self, path: str, **kw: Any) -> Any:
        return self.request("PUT", path, **kw)

    def delete(self, path: str, **kw: Any) -> Any:
        return self.request("DELETE", path, **kw)

    def iterate_cursor(self, path: str, *, limit: int | None = None, **kw: Any) -> Iterator[Any]:
        params = dict(kw.pop("params", None) or {})
        if limit is not None:
            params["limit"] = limit
        cursor = params.pop("cursor", None)
        while True:
            p = dict(params)
            if cursor:
                p["cursor"] = cursor
            page = Page.from_json(self.get(path, params=p, **kw))
            for item in page.data or []:
                yield item
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def list_page(self, path: str, page: int, *, limit: int | None = None, **kw: Any) -> Page:
        params = dict(kw.pop("params", None) or {})
        params["page"] = page
        if limit is not None:
            params["limit"] = limit
        return Page.from_json(self.get(path, params=params, **kw))


class AsyncClient(_Base):
    def __init__(self, **kwargs: Any) -> None:
        transport = kwargs.pop("transport", None)
        timeout = kwargs.pop("timeout", 30.0)
        super().__init__(**kwargs)
        self._client = httpx.AsyncClient(base_url=self.base_url, timeout=timeout, transport=transport)

    async def aclose(self) -> None:
        await self._client.aclose()

    async def __aenter__(self) -> AsyncClient:
        return self

    async def __aexit__(self, *args: object) -> None:
        await self.aclose()

    async def request(
        self,
        method: str,
        path: str,
        *,
        params: Mapping[str, Any] | None = None,
        json: Any = None,
        headers: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
    ) -> Any:
        import asyncio

        hdrs = self._headers(headers, idempotency_key)
        if json is not None:
            hdrs.setdefault("Content-Type", "application/json")
        for attempt in range(self.max_retries + 1):
            res = await self._client.request(method, path, params=params, json=json, headers=hdrs)
            if res.status_code in (429, 503) and attempt < self.max_retries:
                await asyncio.sleep(_retry_after_ms(res.headers, attempt) / 1000.0)
                continue
            if res.status_code in (204, 205):
                return None
            try:
                body = res.json() if res.content else None
            except ValueError:
                body = res.text
            if res.is_error:
                raise ProblemError(parse_problem(body, res.status_code), dict(res.headers))
            return body
        raise RuntimeError("request failed")

    async def get(self, path: str, **kw: Any) -> Any:
        return await self.request("GET", path, **kw)

    async def post(self, path: str, **kw: Any) -> Any:
        return await self.request("POST", path, **kw)

    async def patch(self, path: str, **kw: Any) -> Any:
        return await self.request("PATCH", path, **kw)

    async def put(self, path: str, **kw: Any) -> Any:
        return await self.request("PUT", path, **kw)

    async def delete(self, path: str, **kw: Any) -> Any:
        return await self.request("DELETE", path, **kw)

    async def iterate_cursor(self, path: str, *, limit: int | None = None, **kw: Any) -> AsyncIterator[Any]:
        params = dict(kw.pop("params", None) or {})
        if limit is not None:
            params["limit"] = limit
        cursor = params.pop("cursor", None)
        while True:
            p = dict(params)
            if cursor:
                p["cursor"] = cursor
            page = Page.from_json(await self.get(path, params=p, **kw))
            for item in page.data or []:
                yield item
            if not page.next_cursor:
                return
            cursor = page.next_cursor
