from __future__ import annotations

import base64

import pytest

from emailbridge import (
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

KEY = b"0123456789abcdef0123456789abcdef"
SECRET = "whsec_" + base64.b64encode(KEY).decode("ascii")
BODY = b'{"ok":true}'
ID = "evt_1"
TS = 1700000000


def test_sign_verify() -> None:
    sig = sign(SECRET, ID, TS, BODY)
    headers = {
        HEADER_WEBHOOK_ID: ID,
        HEADER_WEBHOOK_TIMESTAMP: str(TS),
        HEADER_WEBHOOK_SIGNATURE: sig,
    }
    verify(SECRET, headers, BODY, now=TS)


def test_bad_signature() -> None:
    headers = {
        HEADER_WEBHOOK_ID: ID,
        HEADER_WEBHOOK_TIMESTAMP: str(TS),
        HEADER_WEBHOOK_SIGNATURE: "v1,aaaa",
    }
    with pytest.raises(WebhookError):
        verify(SECRET, headers, BODY, now=TS)


def test_stale_timestamp() -> None:
    sig = sign(SECRET, ID, TS, BODY)
    headers = {
        HEADER_WEBHOOK_ID: ID,
        HEADER_WEBHOOK_TIMESTAMP: str(TS),
        HEADER_WEBHOOK_SIGNATURE: sig,
    }
    with pytest.raises(WebhookError):
        verify(SECRET, headers, BODY, now=TS + 600)


def test_body_only() -> None:
    raw = "org-secret"
    sig = sign_body_only(raw, BODY)
    verify_body_only(raw, {HEADER_EB_SIGNATURE: sig}, BODY)


def test_dual_signatures() -> None:
    sig = sign(SECRET, ID, TS, BODY)
    headers = {
        HEADER_WEBHOOK_ID: ID,
        HEADER_WEBHOOK_TIMESTAMP: str(TS),
        HEADER_WEBHOOK_SIGNATURE: f"v1,deadbeef {sig}",
    }
    verify(SECRET, headers, BODY, now=TS)
