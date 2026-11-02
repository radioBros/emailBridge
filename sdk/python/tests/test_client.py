from __future__ import annotations

import httpx
import pytest

from emailbridge import Client, ProblemError


def _handler(request: httpx.Request) -> httpx.Response:
    path = request.url.path
    if path == "/v1/accounts" and request.method == "GET":
        cursor = request.url.params.get("cursor")
        if not cursor:
            return httpx.Response(200, json={"data": [{"id": "acc_1"}], "nextCursor": "c2"})
        return httpx.Response(200, json={"data": [{"id": "acc_2"}], "nextCursor": None})
    if path == "/v1/accounts" and request.method == "POST":
        assert request.headers.get("Idempotency-Key") == "idem-1"
        return httpx.Response(201, json={"id": "acc_new"})
    if path == "/v1/boom":
        return httpx.Response(
            422,
            json={
                "type": "https://emailbridge.dev/problems/validation_failed",
                "title": "Validation failed",
                "status": 422,
                "code": "validation_failed",
                "kind": "user",
                "detail": "bad",
            },
        )
    if path == "/v1/slow":
        n = int(request.headers.get("x-hit", "0") or "0")
        # count via query? use a mutable on the transport — simpler: always 200 after checking Retry
        return httpx.Response(200, json={"ok": True})
    return httpx.Response(404, json={"type": "t", "title": "nf", "status": 404, "code": "not_found", "kind": "user"})


def test_auth_and_org_headers() -> None:
    seen: dict[str, str] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["authorization"] = request.headers.get("Authorization", "")
        seen["org"] = request.headers.get("EB-Organization", "")
        return httpx.Response(200, json={"data": [], "nextCursor": None})

    transport = httpx.MockTransport(handler)
    with Client(base_url="http://eb.test", api_key="ebk_test", organization_id="org_1", transport=transport) as c:
        c.get("/v1/accounts")
    assert seen["authorization"] == "Bearer ebk_test"
    assert seen["org"] == "org_1"


def test_idempotency_key() -> None:
    transport = httpx.MockTransport(_handler)
    with Client(base_url="http://eb.test", api_key="ebk_test", transport=transport) as c:
        out = c.post("/v1/accounts", json={}, idempotency_key="idem-1")
    assert out["id"] == "acc_new"


def test_problem_error() -> None:
    transport = httpx.MockTransport(_handler)
    with Client(base_url="http://eb.test", api_key="ebk_test", transport=transport) as c:
        with pytest.raises(ProblemError) as ei:
            c.get("/v1/boom")
    assert ei.value.problem.code == "validation_failed"
    assert ei.value.problem.status == 422


def test_retry_429() -> None:
    hits = {"n": 0}

    def handler(request: httpx.Request) -> httpx.Response:
        hits["n"] += 1
        if hits["n"] <= 2:
            return httpx.Response(
                429,
                headers={"Retry-After": "0"},
                json={
                    "type": "https://emailbridge.dev/problems/rate_limited",
                    "title": "Rate limited",
                    "status": 429,
                    "code": "rate_limited",
                    "kind": "rate_limited",
                },
            )
        return httpx.Response(200, json={"ok": True})

    transport = httpx.MockTransport(handler)
    with Client(base_url="http://eb.test", api_key="ebk_test", transport=transport) as c:
        assert c.get("/v1/slow")["ok"] is True
    assert hits["n"] >= 3


def test_iterate_cursor() -> None:
    transport = httpx.MockTransport(_handler)
    with Client(base_url="http://eb.test", api_key="ebk_test", transport=transport) as c:
        ids = [row["id"] for row in c.iterate_cursor("/v1/accounts")]
    assert ids == ["acc_1", "acc_2"]
