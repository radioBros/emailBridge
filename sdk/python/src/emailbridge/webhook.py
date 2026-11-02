from __future__ import annotations

import base64
import hashlib
import hmac
import time
from typing import Mapping

HEADER_WEBHOOK_ID = "webhook-id"
HEADER_WEBHOOK_TIMESTAMP = "webhook-timestamp"
HEADER_WEBHOOK_SIGNATURE = "webhook-signature"
HEADER_EB_SIGNATURE = "eb-signature"

DEFAULT_TOLERANCE = 5 * 60  # seconds


class WebhookError(Exception):
    pass


def _header(headers: Mapping[str, str], name: str) -> str:
    lower = name.lower()
    for k, v in headers.items():
        if k.lower() == lower:
            return v
    return ""


def _decode_secret(secret: str) -> bytes:
    raw = secret[6:] if secret.startswith("whsec_") else secret
    key = base64.b64decode(raw)
    if not key:
        raise WebhookError("webhook: empty secret")
    return key


def _match_signatures(header: str, expected_b64: str) -> None:
    for part in header.split(" "):
        part = part.strip()
        if not part or "," not in part:
            continue
        version, sig = part.split(",", 1)
        if version == "v1" and hmac.compare_digest(sig, expected_b64):
            return
    raise WebhookError("webhook: signature mismatch")


def sign(secret: str, webhook_id: str, ts: int, body: bytes | str) -> str:
    key = _decode_secret(secret)
    raw = body if isinstance(body, bytes) else body.encode("utf-8")
    to_sign = f"{webhook_id}.{ts}.".encode("utf-8") + raw
    digest = hmac.new(key, to_sign, hashlib.sha256).digest()
    return "v1," + base64.b64encode(digest).decode("ascii")


def sign_body_only(secret: str, body: bytes | str) -> str:
    raw = body if isinstance(body, bytes) else body.encode("utf-8")
    digest = hmac.new(secret.encode("utf-8"), raw, hashlib.sha256).digest()
    return "v1," + base64.b64encode(digest).decode("ascii")


def verify(
    secret: str,
    headers: Mapping[str, str],
    body: bytes | str,
    *,
    now: float | None = None,
    tolerance: float = DEFAULT_TOLERANCE,
) -> None:
    wid = _header(headers, HEADER_WEBHOOK_ID)
    ts_hdr = _header(headers, HEADER_WEBHOOK_TIMESTAMP)
    sig_hdr = _header(headers, HEADER_WEBHOOK_SIGNATURE)
    if not wid or not ts_hdr or not sig_hdr:
        raise WebhookError("webhook: missing headers")
    try:
        ts = int(ts_hdr)
    except ValueError as e:
        raise WebhookError("webhook: timestamp out of tolerance") from e
    if now is None:
        now = time.time()
    if abs(now - ts) > tolerance:
        raise WebhookError("webhook: timestamp out of tolerance")
    expected = sign(secret, wid, ts, body).split(",", 1)[1]
    _match_signatures(sig_hdr, expected)


def verify_body_only(secret: str, headers: Mapping[str, str], body: bytes | str) -> None:
    sig_hdr = _header(headers, HEADER_EB_SIGNATURE)
    if not sig_hdr:
        raise WebhookError("webhook: missing headers")
    expected = sign_body_only(secret, body).split(",", 1)[1]
    _match_signatures(sig_hdr, expected)
