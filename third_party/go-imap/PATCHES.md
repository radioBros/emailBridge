# EmailBridge patches to go-imap v2

This directory is `github.com/emersion/go-imap/v2` **v2.0.0-beta.8** (MIT, see `LICENSE`) with two patches
(one combined diff file),
used through a `replace` directive in EmailBridge's `go.mod`. Upstream (beta.8 and the development branch as of
2026-09) has no QRESYNC support: `ENABLE QRESYNC` is refused and any `VANISHED` response ends the connection
with "unsupported response type".

## QRESYNC client support (RFC 7162)

- `ENABLE` accepts `CONDSTORE` and `QRESYNC`.
- `imap.SelectOptions.QResync` sends `SELECT/EXAMINE mailbox (QRESYNC (uidvalidity modseq [known-uids]))`.
- `imap.FetchOptions.Vanished` adds the `VANISHED` modifier to `UID FETCH … (CHANGEDSINCE n VANISHED)`.
- `VANISHED [(EARLIER)] uid-set` responses are parsed (`imapclient/vanished.go`) and delivered to the new
  `UnilateralDataHandler.Vanished`. Without `EARLIER` they replace `EXPUNGE` for the selected mailbox, so the
  client's message count drops by the number of UIDs; `EARLIER` responses leave it unchanged.
- Tests: `imapclient/vanished_test.go` (scripted server: wire encoding, both VANISHED forms, the message count,
  unilateral FETCH during SELECT, an unknown tag is a protocol error).

## IDLE delivers updates queued before it started (server side, `imapserver/tracker.go`)

`SessionTracker.Idle` only reacted to update signals that arrived after it registered its channel, so an
update queued between the previous command and IDLE (for example mail arriving right after a client selected
the mailbox) stayed unsent until some later update. Real servers send pending updates when IDLE starts
(RFC 2177). The patch polls once right after registering. Only the provider simulator uses `imapserver`; the
bug made it lose new-mail notifications right after an account hand-off (found by the cluster tests,
isolated by `internal/sync` `TestIntegrationSimulatorSessionHandover`: about 1 lost notification in 100
hand-overs before, none in 500 after; Dovecot never showed it).

Both live in `emailbridge-qresync.patch`. Regenerate it after a change: `diff -ruN <module cache>/github.com/emersion/go-imap/v2@v2.0.0-beta.8
third_party/go-imap -x PATCHES.md -x '*.patch' > third_party/go-imap/emailbridge-qresync.patch`.

Upgrading go-imap: re-apply the patch on the new version (or drop it once upstream supports QRESYNC). Offering
the patch upstream needs the owner's OK (public action).
