"""EmailBridge Python SDK."""

from .client import AsyncClient, Client, Page
from .problem import Problem, ProblemError, ProblemItem
from .sse import SSEEvent, stream_events, stream_events_async
from .webhook import (
    DEFAULT_TOLERANCE,
    HEADER_EB_SIGNATURE,
    HEADER_WEBHOOK_ID,
    HEADER_WEBHOOK_SIGNATURE,
    HEADER_WEBHOOK_TIMESTAMP,
    WebhookError,
    sign,
    sign_body_only,
    verify,
    verify_body_only,
)

__all__ = [
    "AsyncClient",
    "Client",
    "Page",
    "Problem",
    "ProblemError",
    "ProblemItem",
    "SSEEvent",
    "WebhookError",
    "DEFAULT_TOLERANCE",
    "HEADER_EB_SIGNATURE",
    "HEADER_WEBHOOK_ID",
    "HEADER_WEBHOOK_SIGNATURE",
    "HEADER_WEBHOOK_TIMESTAMP",
    "sign",
    "sign_body_only",
    "stream_events",
    "stream_events_async",
    "verify",
    "verify_body_only",
]
