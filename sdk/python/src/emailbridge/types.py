"""TypedDict stubs for common EmailBridge API shapes (regenerate via scripts/generate)."""

from __future__ import annotations

from typing import NotRequired, TypedDict


class ProblemItemDict(TypedDict):
    path: str
    code: str
    message: str


class ProblemDict(TypedDict):
    type: str
    title: str
    status: int
    code: str
    kind: str
    detail: NotRequired[str]
    instance: NotRequired[str]
    traceId: NotRequired[str]
    requestId: NotRequired[str]
    errors: NotRequired[list[ProblemItemDict] | None]


class PageDict(TypedDict):
    data: list[dict] | None
    nextCursor: str | None
    total: NotRequired[int]
    totalCapped: NotRequired[bool]


class AccountView(TypedDict, total=False):
    id: str
    name: str
    state: str
    email: str
