# EmailBridge patch of github.com/thecodearcher/limen/plugins/passkey v0.1.0 (MIT)

Pinned copy used through a `replace` directive in the root `go.mod`.

One change, in `authentication.go` (`BeginAuthentication`): the login options also pass
`webauthn.WithUserVerification(p.config.authenticatorSelection.UserVerification)`, so the configured
user-verification requirement applies at sign-in, not only at registration. Drop this copy when upstream
offers the option.
