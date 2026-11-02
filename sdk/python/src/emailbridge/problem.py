from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping


@dataclass
class ProblemItem:
    path: str
    code: str
    message: str


@dataclass
class Problem:
    type: str
    title: str
    status: int
    code: str
    kind: str
    detail: str | None = None
    instance: str | None = None
    trace_id: str | None = None
    request_id: str | None = None
    errors: list[ProblemItem] | None = None


@dataclass
class ProblemError(Exception):
    problem: Problem
    headers: Mapping[str, str] = field(default_factory=dict)

    def __str__(self) -> str:
        return self.problem.detail or self.problem.title or self.problem.code


def parse_problem(body: Any, status: int) -> Problem:
    if isinstance(body, dict):
        errors = None
        raw_errors = body.get("errors")
        if isinstance(raw_errors, list):
            errors = [
                ProblemItem(
                    path=str(e.get("path", "")),
                    code=str(e.get("code", "")),
                    message=str(e.get("message", "")),
                )
                for e in raw_errors
                if isinstance(e, dict)
            ]
        return Problem(
            type=str(body.get("type") or f"https://emailbridge.dev/problems/http_{status}"),
            title=str(body.get("title") or "Error"),
            status=int(body.get("status") or status),
            code=str(body.get("code") or f"http_{status}"),
            kind=str(body.get("kind") or "temporary"),
            detail=None if body.get("detail") is None else str(body.get("detail")),
            instance=None if body.get("instance") is None else str(body.get("instance")),
            trace_id=None if body.get("traceId") is None else str(body.get("traceId")),
            request_id=None if body.get("requestId") is None else str(body.get("requestId")),
            errors=errors,
        )
    return Problem(
        type=f"https://emailbridge.dev/problems/http_{status}",
        title="Error",
        status=status,
        code=f"http_{status}",
        kind="temporary",
    )
