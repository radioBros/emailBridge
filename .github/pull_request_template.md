## Summary

<!-- What changed and why. Link the issue it closes, if any. -->

## Tests

- [ ] Unit tests
- [ ] Integration tests (testcontainers)
- [ ] End-to-end tests, where the change crosses modules
- [ ] Admin UI tests (`pnpm test:ui`), for UI changes

## Quality

- [ ] Logging and errors follow the existing conventions (error kinds, RFC 9457 problem codes, correlation IDs, redaction)
- [ ] A new optional feature is behind a switch with a safe default
- [ ] Security notes (input validation, authorisation, egress):
- [ ] Personal data touched (retention, erasure):
- [ ] Docs updated (which files):

## Safety

- [ ] No secrets or credentials in code, logs, tests or docs
- [ ] Tests never send real mail except to a test mailbox you own
