# Problem codes

EmailBridge API errors use [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json` responses. The `type` URI for each code is `https://emailbridge.dev/problems/<code>`.

Codes are stable forever once released: they are never renamed, removed, or reused. This file is generated from `internal/errs/codes.yaml` by `go run ./tools/errgen`.

| Code | Kind | HTTP status | Title |
| --- | --- | --- | --- |
| `malformed_request` | validation | 400 | Malformed request |
| `validation_failed` | validation | 422 | Validation failed |
| `sort_not_allowed` | validation | 422 | Sort field not allowed |
| `payload_too_large` | validation | 413 | Payload too large |
| `unsupported_media_type` | validation | 415 | Unsupported media type |
| `unauthenticated` | auth | 401 | Unauthenticated |
| `invalid_credentials` | auth | 401 | Invalid credentials |
| `mfa_required` | auth | 401 | MFA required |
| `mfa_invalid` | auth | 401 | MFA invalid |
| `passkey_failed` | auth | 401 | Passkey failed |
| `oidc_failed` | auth | 401 | OIDC sign-in failed |
| `session_expired` | auth | 401 | Session expired |
| `account_locked` | rate_limited | 423 | Account locked |
| `api_key_invalid` | auth | 401 | API key invalid |
| `api_key_revoked` | auth | 401 | API key revoked |
| `api_key_expired` | auth | 401 | API key expired |
| `forbidden` | forbidden | 403 | Forbidden |
| `scope_missing` | forbidden | 403 | Scope missing |
| `organization_mismatch` | forbidden | 403 | Organization mismatch |
| `api_key_ip_denied` | forbidden | 403 | API key IP denied |
| `api_key_referrer_denied` | forbidden | 403 | API key referrer denied |
| `csrf_failed` | forbidden | 403 | CSRF failed |
| `origin_rejected` | forbidden | 403 | Origin rejected |
| `feature_disabled` | forbidden | 403 | Feature disabled |
| `setting_locked` | forbidden | 403 | Setting locked |
| `setting_not_runtime` | validation | 422 | Setting not runtime |
| `switch_unknown` | validation | 422 | Unknown feature switch |
| `not_found` | not_found | 404 | Not found |
| `conflict` | conflict | 409 | Conflict |
| `version_mismatch` | conflict | 412 | Version mismatch |
| `idempotency_key_reused` | conflict | 409 | Idempotency key reused |
| `idempotency_in_progress` | conflict | 409 | Idempotency in progress |
| `last_owner` | conflict | 409 | Last owner |
| `rate_limited` | rate_limited | 429 | Rate limited |
| `log_range_too_large` | validation | 422 | Log range too large |
| `internal_error` | internal | 500 | Internal error |
| `service_unavailable` | temporary | 503 | Service unavailable |
| `migrations_pending` | temporary | 503 | Migrations pending |
| `timeout` | temporary | 504 | Timeout |
| `external_id_taken` | conflict | 409 | External ID taken |
| `account_not_disabled` | conflict | 409 | Account not disabled |
| `account_disabled` | conflict | 409 | Account disabled |
| `account_deleting` | conflict | 409 | Account deleting |
| `quota_accounts_exceeded` | rate_limited | 429 | Account quota exceeded |
| `quota_sends_exceeded` | rate_limited | 429 | Send quota exceeded |
| `mail_connect_failed` | temporary | 502 | Mail connect failed |
| `mail_tls_failed` | config | 502 | Mail TLS failed |
| `mail_auth_failed` | auth | 502 | Mail authentication failed |
| `mail_timeout` | temporary | 504 | Mail timeout |
| `mail_protocol_error` | permanent | 502 | Mail protocol error |
| `mail_capability_missing` | config | 422 | Mail capability missing |
| `message_not_found` | not_found | 404 | Message not found |
| `archive_not_found` | not_found | 404 | Archive not found |
| `message_too_large` | validation | 413 | Message too large |
| `no_recipients` | validation | 422 | No recipients |
| `invalid_address` | validation | 422 | Invalid address |
| `raw_and_fields` | validation | 422 | Raw and fields both set |
| `outbound_not_cancellable` | conflict | 409 | Outbound not cancellable |
| `smtp_temporary` | temporary | — | SMTP temporary failure |
| `smtp_permanent` | permanent | — | SMTP permanent failure |
| `smtp_outcome_unknown` | outcome_unknown | — | SMTP outcome unknown |
| `smtp_auth_failed` | auth | — | SMTP authentication failed |
| `smtp_rate_limited` | rate_limited | — | SMTP rate limited |
| `endpoint_url_blocked` | forbidden | 422 | Endpoint URL blocked |
| `endpoint_name_taken` | conflict | 409 | Endpoint name taken |
| `webhook_timeout` | temporary | — | Webhook timeout |
| `webhook_http_error` | temporary | — | Webhook HTTP error |
| `webhook_tls_failed` | temporary | — | Webhook TLS failed |
| `webhook_breaker_open` | temporary | — | Webhook breaker open |
| `route_compile_error` | validation | 422 | Route compile error |
| `route_eval_error` | permanent | — | Route evaluation error |
| `route_cost_exceeded` | permanent | — | Route cost exceeded |
| `route_timeout` | permanent | — | Route timeout |
| `route_name_taken` | conflict | 409 | Route name taken |
| `replay_window_exceeded` | validation | 422 | Replay window exceeded |
| `config_drift` | conflict | 409 | Config drift |
| `mailbox_not_found` | not_found | 404 | Mailbox not found |
| `draft_not_found` | not_found | 404 | Draft not found |
| `uid_validity_mismatch` | conflict | 409 | UIDVALIDITY mismatch |
| `connection_busy` | rate_limited | 429 | On-demand connection busy |
| `batch_too_large` | validation | 422 | Batch too large |
| `attachment_not_found` | not_found | 404 | Attachment not found |
| `attachment_infected` | forbidden | 403 | Attachment infected |
| `signed_url_invalid` | forbidden | 403 | Signed URL invalid |
| `signed_url_expired` | forbidden | 403 | Signed URL expired |
| `template_not_found` | not_found | 404 | Template not found |
| `template_name_taken` | conflict | 409 | Template name taken |
| `template_render_failed` | validation | 422 | Template render failed |
| `template_variable_missing` | validation | 422 | Template variable missing |
| `mail_merge_too_large` | validation | 422 | Mail merge too large |
| `reference_invalid` | validation | 422 | Reference invalid |
| `gateway_name_taken` | conflict | 409 | Gateway name taken |
| `gateway_unavailable` | permanent | 422 | Gateway unavailable |
| `outbound_not_retryable` | conflict | 409 | Outbound not retryable |
| `outbound_not_sendable` | conflict | 409 | Outbound not sendable now |
| `delivery_test_timeout` | temporary | 504 | Delivery test timed out |
| `oauth_app_name_taken` | conflict | 409 | OAuth app name taken |
| `oauth_app_disabled` | permanent | 422 | OAuth app disabled |
| `oauth_state_invalid` | validation | 400 | OAuth state invalid |
| `oauth_exchange_failed` | temporary | 502 | OAuth token exchange failed |
| `oauth_revoked` | auth | 401 | OAuth grant revoked |
| `connect_link_invalid` | not_found | 404 | Connect link invalid |
| `oauth_not_connected` | conflict | 409 | OAuth not connected |
| `oauth_app_link_invalid` | validation | 422 | OAuth app link invalid |
| `oauth_provider_mismatch` | conflict | 409 | OAuth provider mismatch |
| `auth_server_failed` | temporary | 502 | Auth server failed |
| `auth_server_unavailable` | temporary | 503 | Auth server unavailable |
| `auth_server_misconfigured` | config | 422 | Auth server misconfigured |
| `delegated_in_use` | conflict | 409 | Delegated account in use |
| `delegated_invalid` | validation | 422 | Delegated mailbox invalid |
| `recipient_suppressed` | validation | 422 | Recipient suppressed |
| `suppression_list_name_taken` | conflict | 409 | Suppression list name taken |
| `suppression_list_kind_taken` | conflict | 409 | Suppression list kind taken |
| `presend_rejected` | permanent | 422 | Pre-send policy rejected |
| `presend_held` | conflict | 409 | Pre-send policy hold |
| `export_not_found` | not_found | 404 | Export not found |
| `export_not_ready` | conflict | 409 | Export not ready |
| `export_expired` | not_found | 410 | Export expired |
| `identity_mismatch` | conflict | 409 | Identity mismatch |
| `connect_csrf_invalid` | forbidden | 403 | Connect CSRF invalid |
| `autoconfig_failed` | temporary | 502 | Autoconfig failed |
| `gateway_kind_immutable` | conflict | 409 | Gateway kind immutable |
| `evidence_hash_conflict` | conflict | 409 | Evidence hash conflict |
| `evidence_lock_unavailable` | config | 503 | Evidence Object Lock unavailable |
| `evidence_tsa_failed` | temporary | 502 | Evidence TSA stamp failed |
| `unsupported_for_provider` | validation | 400 | Unsupported for provider |
| `branding_asset_type` | validation | 415 | Branding asset type not allowed |
| `branding_asset_dimensions` | validation | 422 | Branding asset dimensions too large |
| `branding_asset_quota` | validation | 422 | Branding asset quota exceeded |
| `branding_asset_kind` | validation | 422 | Branding asset kind mismatch |
| `branding_asset_in_use` | conflict | 409 | Branding asset still referenced |
| `branding_palette_contrast` | validation | 422 | Branding palette fails WCAG AA |
| `rule_list_name_taken` | conflict | 409 | Rule list name taken |
| `rule_list_not_found` | not_found | 404 | Rule list not found |
| `organization_quota_exceeded` | rate_limited | 429 | Organisation quota exceeded |
| `invitation_expired` | not_found | 410 | Invitation expired |
| `queue_item_not_cancellable` | conflict | 409 | Queue item not cancellable |
| `queue_read_only` | conflict | 409 | Queue is read-only |
| `queue_confirmation_required` | validation | 422 | Queue confirmation required |
| `oauth_app_in_use` | conflict | 409 | OAuth app still referenced by accounts |
| `smtp_gateway_in_use` | conflict | 409 | SMTP gateway still referenced by accounts |
| `quota_endpoints_exceeded` | rate_limited | 429 | Webhook endpoint quota exceeded |
