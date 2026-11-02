package auth

import (
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// OpenAPI documentation of the raw /v1/auth/* routes (Limen and the OIDC flow). The handlers are mounted in
// Mount; these operations only describe them (W70). Bodies follow Limen's snake_case JSON; every error is
// translated to an RFC 9457 problem (translate.go).

// AuthUser is the Limen user record in session responses (no ID, no password hash).
type AuthUser struct {
	Email            string     `json:"email" doc:"Sign-in email address." example:"maria.rossi@example.com"`
	EmailVerifiedAt  *time.Time `json:"email_verified_at" doc:"When the address was verified (set at bootstrap / invitation)." example:"2026-10-20T09:00:00Z"`
	TwoFactorEnabled bool       `json:"two_factor_enabled" doc:"Whether TOTP two-factor authentication is set up." example:"true"`
	CreatedAt        time.Time  `json:"created_at" doc:"When the sign-in was created." example:"2026-10-20T09:00:00Z"`
	UpdatedAt        time.Time  `json:"updated_at" doc:"Last change of the sign-in." example:"2026-10-24T14:32:10Z"`
}

// AuthSessionBody is the body of every response that issues or returns a session.
type AuthSessionBody struct {
	User AuthUser `json:"user" doc:"The signed-in user."`
}

// AuthSignInBody is the password sign-in response: a session, or a second-factor challenge.
type AuthSignInBody struct {
	User              *AuthUser `json:"user,omitempty" doc:"The signed-in user (no second factor configured)."`
	TwoFactorRequired bool      `json:"two_factor_required,omitempty" doc:"True when a second factor is needed: no session yet, a session_2fa challenge cookie was set; continue with POST /v1/auth/two-factor/verify." example:"true"`
}

// AuthMessage is Limen's informational response.
type AuthMessage struct {
	Message string `json:"message" doc:"Human-readable outcome (deliberately the same whether or not the account exists)." example:"An OTP code will be sent to your email if it is associated with an account"`
}

// AuthTOTPURI carries the otpauth:// URI for the authenticator app.
type AuthTOTPURI struct {
	URI string `json:"uri" doc:"otpauth:// URI; show it as a QR code (the secret is its secret parameter)." example:"otpauth://totp/EmailBridge:maria.rossi@example.com?algorithm=SHA1&digits=6&issuer=EmailBridge&period=30&secret=JBSWY3DPEHPK3PXP"`
}

// AuthPasskey is one registered passkey.
type AuthPasskey struct {
	ID             string     `json:"id" doc:"Passkey ID." example:"7d1e9b2c-4a6f-4c3e-8b5d-2f1a0e9c8b7a"`
	Name           string     `json:"name" doc:"Name given by the user (1–100 characters)." example:"MacBook Touch ID"`
	CredentialID   string     `json:"credential_id" doc:"WebAuthn credential ID (base64url)." example:"kUbDdA0sQh2fN8xV3pLr7Q"`
	Transports     []string   `json:"transports,omitempty" doc:"Authenticator transports." example:"[\"internal\",\"hybrid\"]"`
	BackupEligible bool       `json:"backup_eligible" doc:"Whether the credential can be synced (multi-device passkey)." example:"true"`
	BackupState    bool       `json:"backup_state" doc:"Whether it is currently backed up / synced." example:"true"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty" doc:"Last sign-in with this passkey." example:"2026-10-27T08:01:12Z"`
	CreatedAt      time.Time  `json:"created_at" doc:"Registration time." example:"2026-10-21T11:20:00Z"`
}

// AuthPasskeyPage is the paginated passkey list.
type AuthPasskeyPage struct {
	Items      []AuthPasskey `json:"items" doc:"Passkeys of this page."`
	Total      int64         `json:"total" doc:"Number of passkeys." example:"2"`
	Page       int           `json:"page" doc:"Page number (1-based)." example:"1"`
	PerPage    int           `json:"per_page" doc:"Page size." example:"20"`
	TotalPages int           `json:"total_pages" doc:"Number of pages." example:"1"`
}

// Request bodies.
type (
	// AuthSignInRequest is POST /v1/auth/signin/credential.
	AuthSignInRequest struct {
		Credential string `json:"credential" doc:"Email address." example:"maria.rossi@example.com"`
		Password   string `json:"password" doc:"Password." example:"correct-horse-battery-staple"`
		RememberMe *bool  `json:"remember_me,omitempty" doc:"false issues a short session (at most 1 hour) instead of the normal one." example:"true"`
	}
	// AuthVerifyRequest is POST /v1/auth/two-factor/verify.
	AuthVerifyRequest struct {
		Code   string `json:"code" doc:"6-digit TOTP or email code, or a backup code." example:"492817"`
		Method string `json:"method,omitempty" doc:"totp (default; also accepts a backup code) or otp (emailed code)." example:"totp"`
	}
	// AuthEmailRequest carries an email address.
	AuthEmailRequest struct {
		Email string `json:"email" doc:"Email address of the account." example:"maria.rossi@example.com"`
	}
	// AuthPasswordConfirm re-confirms the current password.
	AuthPasswordConfirm struct {
		Password string `json:"password" doc:"Current password." example:"correct-horse-battery-staple"`
	}
	// AuthCodeRequest carries a TOTP code.
	AuthCodeRequest struct {
		Code string `json:"code" doc:"6-digit code from the authenticator app." example:"492817"`
	}
	// AuthResetRequest is POST /v1/auth/passwords/reset.
	AuthResetRequest struct {
		Token       string `json:"token" doc:"Token from the reset email link." example:"Tq7mZ2vK9xR4bN8cW1pL6sD3fH0jG5aE"`
		NewPassword string `json:"new_password" doc:"New password, 12–128 characters, not a common password." example:"plum-river-lantern-42"`
	}
	// AuthChangePasswordRequest is POST /v1/auth/passwords/change.
	AuthChangePasswordRequest struct {
		CurrentPassword     string `json:"current_password" doc:"Current password." example:"correct-horse-battery-staple"`
		NewPassword         string `json:"new_password" doc:"New password, 12–128 characters, not a common password." example:"plum-river-lantern-42"`
		RevokeOtherSessions bool   `json:"revoke_other_sessions,omitempty" doc:"Also sign out every other session." example:"true"`
	}
	// AuthSetPasswordRequest is PUT /v1/auth/passwords.
	AuthSetPasswordRequest struct {
		NewPassword         string `json:"new_password" doc:"Password to set, 12–128 characters, not a common password." example:"plum-river-lantern-42"`
		RevokeOtherSessions bool   `json:"revoke_other_sessions,omitempty" doc:"Also sign out every other session." example:"false"`
	}
	// AuthPasskeyName renames a passkey.
	AuthPasskeyName struct {
		Name string `json:"name" doc:"New name, 1–100 characters." example:"YubiKey 5C"`
	}
)

var (
	exUser    = map[string]any{"email": "maria.rossi@example.com", "email_verified_at": "2026-10-20T09:00:00Z", "two_factor_enabled": true, "created_at": "2026-10-20T09:00:00Z", "updated_at": "2026-10-24T14:32:10Z"}
	exSession = map[string]any{"user": exUser}
	exPasskey = map[string]any{"id": "7d1e9b2c-4a6f-4c3e-8b5d-2f1a0e9c8b7a", "name": "MacBook Touch ID", "credential_id": "kUbDdA0sQh2fN8xV3pLr7Q", //nolint:gosec // G101: documentation example, not a credential
		"transports": []any{"internal", "hybrid"}, "backup_eligible": true, "backup_state": true, "last_used_at": "2026-10-27T08:01:12Z", "created_at": "2026-10-21T11:20:00Z"}
	exCreationOptions = map[string]any{"publicKey": map[string]any{
		"rp":                     map[string]any{"name": "EmailBridge", "id": "mail.example.com"},
		"user":                   map[string]any{"name": "maria.rossi@example.com", "displayName": "maria.rossi@example.com", "id": "M2Y2YzJhMWUtOGI0ZA"},
		"challenge":              "Y2hhbGxlbmdlLWV4YW1wbGUtMTIzNDU2Nzg5MA",
		"pubKeyCredParams":       []any{map[string]any{"type": "public-key", "alg": -7}, map[string]any{"type": "public-key", "alg": -257}},
		"timeout":                300000,
		"authenticatorSelection": map[string]any{"residentKey": "required", "requireResidentKey": true, "userVerification": "required"},
		"attestation":            "none",
	}}
	exRequestOptions = map[string]any{"publicKey": map[string]any{
		"challenge": "YXV0aC1jaGFsbGVuZ2UtZXhhbXBsZS0wOTg3NjU", "timeout": 300000, "rpId": "mail.example.com", "userVerification": "required",
	}}
	exAttestation = map[string]any{
		"id": "kUbDdA0sQh2fN8xV3pLr7Q", "rawId": "kUbDdA0sQh2fN8xV3pLr7Q", "type": "public-key",
		"response": map[string]any{"clientDataJSON": "eyJ0eXBlIjoid2ViYXV0aG4uY3JlYXRlIn0", "attestationObject": "o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YQ", "transports": []any{"internal", "hybrid"}},
		"name":     "MacBook Touch ID",
	}
	exAssertion = map[string]any{
		"id": "kUbDdA0sQh2fN8xV3pLr7Q", "rawId": "kUbDdA0sQh2fN8xV3pLr7Q", "type": "public-key",
		"response": map[string]any{"clientDataJSON": "eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0", "authenticatorData": "SZYN5YgOjGh0NBcPZHZgW4_krrmihjLHmVzzuoMdl2MFAAAAAQ",
			"signature": "MEUCIQDx3kVb2XyLsFh5mTqN8pR1cYwZ", "userHandle": "M2Y2YzJhMWUtOGI0ZA"},
	}
)

// documentRoutes adds the /v1/auth/* operations to the OpenAPI document.
func (s *Service) documentRoutes(srv *httpapi.Server) {
	reg := srv.API.OpenAPI().Components.Schemas
	schemaOf := func(t reflect.Type) *huma.Schema { return reg.Schema(t, true, "") }
	open := func() *huma.Schema { return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true} }
	public := []map[string][]string{}
	session := []map[string][]string{{"session": {}}}
	cookie := map[string]*huma.Param{"Set-Cookie": {Description: "Session cookie (HttpOnly, Secure, SameSite=Strict); send it on every later request.",
		Schema: &huma.Schema{Type: huma.TypeString}, Example: "eb_session=Qm9vdHN0cmFwLXNlc3Npb24tdG9rZW4; Path=/; HttpOnly; Secure; SameSite=Strict"}}
	retry := map[string]*huma.Param{"Retry-After": {Description: "Seconds until the lockout ends.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "900"}}
	sessionResp := func(desc string) map[string]*huma.Response {
		return map[string]*huma.Response{"200": {Description: desc, Headers: cookie, Content: map[string]*huma.MediaType{
			"application/json": {Schema: schemaOf(reflect.TypeFor[AuthSessionBody]()), Example: exSession}}}}
	}
	jsonBody := func(t reflect.Type, ex any) *huma.RequestBody {
		return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: schemaOf(t), Example: ex}}}
	}
	jsonResp := func(desc string, sch *huma.Schema, ex any) map[string]*huma.Response {
		return map[string]*huma.Response{"200": {Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: sch, Example: ex}}}}
	}
	noContent := func(desc string) map[string]*huma.Response {
		return map[string]*huma.Response{"204": {Description: desc}}
	}
	withLock := func(r map[string]*huma.Response) map[string]*huma.Response {
		r["423"] = &huma.Response{Description: "Locked — too many failed attempts; see Retry-After.", Headers: retry}
		return r
	}
	ext := httpapi.Phase("P0")
	origin := " Browser requests must come from a trusted origin (`EB_PUBLIC_URL` / `EB_CORS_ORIGINS`); others get `403 origin_rejected`."
	doc := func(op *huma.Operation) {
		op.Tags = []string{"auth"}
		if op.Extensions == nil {
			op.Extensions = ext
		}
		srv.Document(op)
	}
	signedIn := httpapi.Problems(errs.CodeUnauthenticated, errs.CodeSessionExpired, errs.CodeOriginRejected, errs.CodeRateLimited)

	// --- password sign-in and second factor ---
	doc(&huma.Operation{OperationID: "authSignIn", Method: http.MethodPost, Path: BasePath + "/signin/credential",
		Summary: "Sign in with email and password",
		Description: "Checks the password and either sets the `eb_session` cookie and returns the user, or — when the " +
			"user has a second factor — sets the short-lived `session_2fa` challenge cookie and returns " +
			"`{\"two_factor_required\": true}` (no session yet; continue with `POST /v1/auth/two-factor/verify`). " +
			"Rate limited per client IP (`EB_RATE_LOGIN`, default 10/min). After `EB_AUTH_LOCKOUT_THRESHOLD` " +
			"failures (default 5) the email and IP are locked for `EB_AUTH_LOCKOUT_DURATION` (default 15 min): " +
			"`423 account_locked` with `Retry-After`. Wrong email and wrong password give the same `401`." + origin,
		Security:    public,
		RequestBody: jsonBody(reflect.TypeFor[AuthSignInRequest](), map[string]any{"credential": "maria.rossi@example.com", "password": "correct-horse-battery-staple", "remember_me": true}),
		Responses: withLock(map[string]*huma.Response{"200": {Description: "Signed in, or a second factor is required.", Headers: cookie,
			Content: map[string]*huma.MediaType{"application/json": {Schema: schemaOf(reflect.TypeFor[AuthSignInBody]()), Examples: map[string]*huma.Example{
				"signedIn":          {Summary: "Session issued", Value: exSession},
				"twoFactorRequired": {Summary: "Second factor required (session_2fa cookie set)", Value: map[string]any{"two_factor_required": true}},
			}}}}}),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeInvalidCredentials, Detail: "Invalid credential."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Credential is required."},
			httpapi.ProblemDoc{Code: errs.CodeOriginRejected}, httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeAccountLocked, Detail: "Too many failed sign-ins; try again later."}),
	})
	doc(&huma.Operation{OperationID: "authVerifyTwoFactor", Method: http.MethodPost, Path: BasePath + "/two-factor/verify",
		Summary: "Complete sign-in with a second factor",
		Description: "Verifies the TOTP code (default), an emailed code (`method: otp`) or a backup code against the " +
			"`session_2fa` challenge cookie from the password step, then issues the session. TOTP codes are single " +
			"use; backup codes are consumed. Repeated failures lock the challenge (`423 account_locked` with " +
			"`Retry-After`); also rate limited per IP." + origin,
		Security:    public,
		RequestBody: jsonBody(reflect.TypeFor[AuthVerifyRequest](), map[string]any{"code": "492817", "method": "totp"}),
		Responses:   withLock(sessionResp("Second factor accepted; session issued.")),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeMFAInvalid, Detail: "Invalid code."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Code is required."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeAccountLocked, Detail: "Too many failed verification attempts; try again later."}),
	})
	doc(&huma.Operation{OperationID: "authSendOtp", Method: http.MethodPost, Path: BasePath + "/two-factor/otp/send",
		Summary: "Email a one-time sign-in code",
		Description: "Sends a one-time code to the email address during sign-in, as an alternative second factor. Only " +
			"when system mail is configured (`EB_SYSMAIL_MODE` ≠ off). Requires the `session_2fa` challenge cookie of " +
			"the same user (`401` otherwise); at most 5 codes per 15 minutes per user (`423` with `Retry-After`) and " +
			"rate limited per IP. The answer does not reveal whether the address has an account.",
		Security:    public,
		RequestBody: jsonBody(reflect.TypeFor[AuthEmailRequest](), map[string]any{"email": "maria.rossi@example.com"}),
		Responses: withLock(jsonResp("Code sent if the address belongs to the challenged user.", schemaOf(reflect.TypeFor[AuthMessage]()),
			map[string]any{"message": "An OTP code will be sent to your email if it is associated with an account"})),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated, Detail: "Sign in with your password first; then request a code."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest}, httpapi.ProblemDoc{Code: errs.CodeRateLimited},
			httpapi.ProblemDoc{Code: errs.CodeAccountLocked, Detail: "Too many codes requested; try again later."}),
	})

	// --- TOTP enrolment and backup codes ---
	doc(&huma.Operation{OperationID: "authInitiateTwoFactorSetup", Method: http.MethodPost, Path: BasePath + "/two-factor/initiate-setup",
		Summary: "Start TOTP enrolment",
		Description: "Re-checks the password and returns an `otpauth://` URI for the authenticator app (show it as QR " +
			"code); also prepares 10 backup codes. Two-factor stays off until `finalize-setup` confirms a code. " +
			"Signed-in users (session cookie); allowed before MFA enrolment is complete. At most 10 calls per minute.",
		Security:    session,
		RequestBody: jsonBody(reflect.TypeFor[AuthPasswordConfirm](), map[string]any{"password": "correct-horse-battery-staple"}),
		Responses:   jsonResp("Setup URI.", schemaOf(reflect.TypeFor[AuthTOTPURI]()), map[string]any{"uri": "otpauth://totp/EmailBridge:maria.rossi@example.com?algorithm=SHA1&digits=6&issuer=EmailBridge&period=30&secret=JBSWY3DPEHPK3PXP"}),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeInvalidCredentials, Detail: "Invalid password."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Two factor already enabled."}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authFinalizeTwoFactorSetup", Method: http.MethodPost, Path: BasePath + "/two-factor/finalize-setup",
		Summary: "Confirm TOTP enrolment",
		Description: "Turns two-factor on with the first code from the app, rotates the session (new `eb_session` " +
			"cookie, method password+totp) and returns the user. Fetch the backup codes next with " +
			"`GET /v1/auth/two-factor/backup-codes` and show them once. Signed-in users.",
		Security:    session,
		RequestBody: jsonBody(reflect.TypeFor[AuthCodeRequest](), map[string]any{"code": "492817"}),
		Responses:   sessionResp("Two-factor enabled; session rotated."),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeMFAInvalid, Detail: "Invalid code."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authDisableTwoFactor", Method: http.MethodPost, Path: BasePath + "/two-factor/disable",
		Summary: "Turn TOTP off",
		Description: "Re-checks the password, removes the TOTP secret and backup codes and rotates the session. Where " +
			"`EB_AUTH_MFA_REQUIRED` is on (default), the user must enrol a second factor again before using the " +
			"API (`401 mfa_required` until then). Signed-in users.",
		Security:    session,
		RequestBody: jsonBody(reflect.TypeFor[AuthPasswordConfirm](), map[string]any{"password": "correct-horse-battery-staple"}),
		Responses:   sessionResp("Two-factor disabled; session rotated."),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeInvalidCredentials, Detail: "Invalid password."},
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authGetTotpUri", Method: http.MethodGet, Path: BasePath + "/two-factor/totp/uri",
		Summary: "Show the TOTP setup URI again",
		Description: "Returns the `otpauth://` URI of the enrolled (or pending) TOTP secret, e.g. to add a second " +
			"device. Signed-in users with two-factor set up. Read-only.",
		Security:  session,
		Responses: jsonResp("Setup URI.", schemaOf(reflect.TypeFor[AuthTOTPURI]()), map[string]any{"uri": "otpauth://totp/EmailBridge:maria.rossi@example.com?algorithm=SHA1&digits=6&issuer=EmailBridge&period=30&secret=JBSWY3DPEHPK3PXP"}),
		Metadata:  signedIn,
	})
	codes := &huma.Schema{Type: huma.TypeArray, Description: "Backup codes (10 characters each), each usable once instead of a TOTP code.",
		Items: &huma.Schema{Type: huma.TypeString, Examples: []any{"7KQ2M9XW4P"}}}
	exCodes := []any{"7KQ2M9XW4P", "H3TZ8N1VRC", "Q9DF2LMX6A", "W4BN7CJ0ES", "P2YH5KXT8R", "M6RV3QZ9LD", "C8GW1TNF5K", "X5LP9DJ2HB", "T1ZQ6RWM3V", "B7NK4XCE0Y"}
	doc(&huma.Operation{OperationID: "authGetBackupCodes", Method: http.MethodGet, Path: BasePath + "/two-factor/backup-codes",
		Summary: "List the unused backup codes",
		Description: "Returns the user's remaining backup codes (decrypted). Show them only on explicit request; " +
			"each code works once in `POST /v1/auth/two-factor/verify`. Signed-in users with two-factor enabled.",
		Security: session, Responses: jsonResp("Remaining backup codes.", codes, exCodes), Metadata: signedIn,
	})
	doc(&huma.Operation{OperationID: "authRegenerateBackupCodes", Method: http.MethodPut, Path: BasePath + "/two-factor/backup-codes",
		Summary: "Replace the backup codes",
		Description: "Generates 10 new backup codes and invalidates every old one. Signed-in users with two-factor " +
			"enabled. Not idempotent: every call produces new codes.",
		Security: session, Responses: jsonResp("The new backup codes.", codes, exCodes), Metadata: signedIn,
	})

	// --- passwords ---
	doc(&huma.Operation{OperationID: "authRequestPasswordReset", Method: http.MethodPost, Path: BasePath + "/passwords/request-reset",
		Summary: "Request a password-reset email",
		Description: "Emails a reset link (`{EB_PUBLIC_URL}/reset-password?token=…`) when the address has a password " +
			"sign-in. The response is the same whether or not it does, so it cannot be used to discover accounts. " +
			"Needs system mail. Public, rate limited.",
		Security:    public,
		RequestBody: jsonBody(reflect.TypeFor[AuthEmailRequest](), map[string]any{"email": "maria.rossi@example.com"}),
		Responses: jsonResp("Accepted.", schemaOf(reflect.TypeFor[AuthMessage]()),
			map[string]any{"message": "if the email address is associated with an account, you will receive an email with instructions to reset your password"}),
		Metadata: httpapi.Problems(errs.CodeMalformedRequest, errs.CodeOriginRejected, errs.CodeRateLimited),
	})
	doc(&huma.Operation{OperationID: "authResetPassword", Method: http.MethodPost, Path: BasePath + "/passwords/reset",
		Summary: "Set a new password with a reset token",
		Description: "Sets the password from the token of the reset email (single use, time limited). The password " +
			"must be 12–128 characters and not on the common-password list. Existing sessions are not signed in " +
			"automatically; sign in again. Public, rate limited.",
		Security:    public,
		RequestBody: jsonBody(reflect.TypeFor[AuthResetRequest](), map[string]any{"token": "Tq7mZ2vK9xR4bN8cW1pL6sD3fH0jG5aE", "new_password": "plum-river-lantern-42"}), //nolint:gosec // G101: documentation example, not a credential
		Responses:   jsonResp("Password changed.", schemaOf(reflect.TypeFor[AuthMessage]()), map[string]any{"message": "password reset successfully"}),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Invalid or expired token."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Choose a less common password."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authChangePassword", Method: http.MethodPost, Path: BasePath + "/passwords/change",
		Summary: "Change the password",
		Description: "Changes the signed-in user's password after checking the current one; optionally signs out every " +
			"other session. Rotates the session (new cookie). Same password rules as reset. Signed-in users.",
		Security:    session,
		RequestBody: jsonBody(reflect.TypeFor[AuthChangePasswordRequest](), map[string]any{"current_password": "correct-horse-battery-staple", "new_password": "plum-river-lantern-42", "revoke_other_sessions": true}), //nolint:gosec // G101: documentation example, not a credential
		Responses:   sessionResp("Password changed; session rotated."),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeInvalidCredentials, Detail: "Current password is invalid."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Choose a less common password."}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authSetPassword", Method: http.MethodPut, Path: BasePath + "/passwords",
		Summary: "Set a password (account without one)",
		Description: "Adds a password to a user who signs in only with a passkey or OIDC. Same password rules as " +
			"reset; optionally signs out the other sessions. Rotates the session. Signed-in users.",
		Security:    session,
		RequestBody: jsonBody(reflect.TypeFor[AuthSetPasswordRequest](), map[string]any{"new_password": "plum-river-lantern-42", "revoke_other_sessions": false}), //nolint:gosec // G101: documentation example, not a credential
		Responses:   sessionResp("Password set; session rotated."),
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Password already set."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Choose a less common password."}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})

	// --- session ---
	doc(&huma.Operation{OperationID: "authGetSession", Method: http.MethodGet, Path: BasePath + "/me",
		Summary: "Current session user",
		Description: "Returns the user of the session cookie (and slides its expiry). Used by the admin UI on load; for " +
			"organisations, role and permissions use `GET /v1/me`. `401` (and the cookie is cleared) when the " +
			"session is missing, expired or revoked.",
		Security: session, Responses: sessionResp("The session is valid."), Metadata: signedIn,
	})
	doc(&huma.Operation{OperationID: "authSignOut", Method: http.MethodPost, Path: BasePath + "/signout",
		Summary:     "Sign out",
		Description: "Revokes the current session and clears the `eb_session` cookie. Signed-in users. Answers `204`." + origin,
		Security:    session, Responses: noContent("Signed out; cookie cleared."), Metadata: signedIn,
	})
	doc(&huma.Operation{OperationID: "authRevokeAllSessions", Method: http.MethodPost, Path: BasePath + "/revoke-sessions",
		Summary: "Sign out everywhere",
		Description: "Revokes every session of the user, the current one included. To keep the current session use " +
			"`POST /v1/me/sessions/revoke-others`. Signed-in users. Answers `204`.",
		Security: session, Responses: noContent("Every session revoked."), Metadata: signedIn,
	})

	// --- passkeys ---
	doc(&huma.Operation{OperationID: "authBeginPasskeyRegistration", Method: http.MethodGet, Path: BasePath + "/passkeys/begin-registration",
		Summary: "Start registering a passkey",
		Description: "Returns WebAuthn `PublicKeyCredentialCreationOptions` for `navigator.credentials.create()` " +
			"(resident key and user verification required) and sets a short-lived challenge cookie. Optional query / " +
			"body field `authenticator_attachment`: `platform` or `cross-platform`. Signed-in users; allowed before " +
			"MFA enrolment is complete.",
		Security:  session,
		Responses: jsonResp("WebAuthn creation options.", open(), exCreationOptions),
		Metadata:  signedIn,
	})
	doc(&huma.Operation{OperationID: "authFinishPasskeyRegistration", Method: http.MethodPost, Path: BasePath + "/passkeys/finish-registration",
		Summary: "Finish registering a passkey",
		Description: "Verifies the attestation from `navigator.credentials.create()` against the challenge cookie and " +
			"stores the passkey with an optional `name` (1–100 characters). A password session is upgraded to " +
			"password+passkey (satisfies the MFA requirement). Signed-in users.",
		Security: session,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {
			Schema: open(), Example: exAttestation}}},
		Responses: jsonResp("The new passkey.", schemaOf(reflect.TypeFor[AuthPasskey]()), exPasskey),
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodePasskeyFailed, Detail: "Passkey verification failed."}, httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
	doc(&huma.Operation{OperationID: "authBeginPasskeyAuthentication", Method: http.MethodGet, Path: BasePath + "/passkeys/begin-authentication",
		Summary: "Start signing in with a passkey",
		Description: "Returns WebAuthn `PublicKeyCredentialRequestOptions` for `navigator.credentials.get()` " +
			"(discoverable credentials, no email needed) and sets a challenge cookie. Public, rate limited like " +
			"password sign-in.",
		Security:  public,
		Responses: jsonResp("WebAuthn request options.", open(), exRequestOptions),
		Metadata:  httpapi.Problems(errs.CodeRateLimited, errs.CodeOriginRejected),
	})
	doc(&huma.Operation{OperationID: "authFinishPasskeyAuthentication", Method: http.MethodPost, Path: BasePath + "/passkeys/finish-authentication",
		Summary: "Finish signing in with a passkey",
		Description: "Verifies the assertion from `navigator.credentials.get()` and issues a session (method passkey; " +
			"a passkey is a strong factor, so no second step). Public, rate limited." + origin,
		Security: public,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {
			Schema: open(), Example: exAssertion}}},
		Responses: sessionResp("Signed in with the passkey."),
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodePasskeyFailed, Detail: "Passkey not recognized."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited}, httpapi.ProblemDoc{Code: errs.CodeOriginRejected}),
	})
	passkeyID := []*huma.Param{{Name: "id", In: "path", Required: true, Description: "Passkey ID.", Schema: &huma.Schema{Type: huma.TypeString},
		Example: "7d1e9b2c-4a6f-4c3e-8b5d-2f1a0e9c8b7a"}}
	doc(&huma.Operation{OperationID: "authListPasskeys", Method: http.MethodGet, Path: BasePath + "/passkeys",
		Summary:     "List my passkeys",
		Description: "Lists the signed-in user's passkeys (no key material), paginated with `page` and `per_page`. Read-only.",
		Security:    session,
		Parameters: []*huma.Param{
			{Name: "page", In: "query", Description: "Page number (1-based).", Schema: &huma.Schema{Type: huma.TypeInteger}, Example: 1},
			{Name: "per_page", In: "query", Description: "Page size.", Schema: &huma.Schema{Type: huma.TypeInteger}, Example: 20},
		},
		Responses: jsonResp("A page of passkeys.", schemaOf(reflect.TypeFor[AuthPasskeyPage]()), map[string]any{
			"items": []any{exPasskey, map[string]any{"id": "0b8e6d4c-2f1a-4b9e-8c7d-6a5b4c3d2e1f", "name": "YubiKey 5C", "credential_id": "vR8sT2qLm4WnX6yZ0aB1cD", //nolint:gosec // G101: documentation example, not a credential
				"transports": []any{"usb", "nfc"}, "backup_eligible": false, "backup_state": false, "created_at": "2026-10-22T16:05:00Z"}},
			"total": 2, "page": 1, "per_page": 20, "total_pages": 1}),
		Metadata: signedIn,
	})
	doc(&huma.Operation{OperationID: "authRenamePasskey", Method: http.MethodPatch, Path: BasePath + "/passkeys/{id}",
		Summary: "Rename a passkey", Description: "Changes the name of one of the user's passkeys. Signed-in users.",
		Security: session, Parameters: passkeyID,
		RequestBody: jsonBody(reflect.TypeFor[AuthPasskeyName](), map[string]any{"name": "YubiKey 5C"}),
		Responses:   jsonResp("The renamed passkey.", schemaOf(reflect.TypeFor[AuthPasskey]()), exPasskey),
		Metadata:    httpapi.Problems(errs.CodeUnauthenticated, errs.CodeNotFound, errs.CodeMalformedRequest, errs.CodeRateLimited),
	})
	doc(&huma.Operation{OperationID: "authDeletePasskey", Method: http.MethodDelete, Path: BasePath + "/passkeys/{id}",
		Summary: "Delete a passkey",
		Description: "Removes one of the user's passkeys; it can no longer sign in. If it was the only second factor " +
			"and MFA is required, the user must enrol another before using the API. Signed-in users. Answers `204`.",
		Security: session, Parameters: passkeyID, Responses: noContent("Passkey deleted."),
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeNotFound, errs.CodeRateLimited),
	})

	// --- OIDC single sign-on ---
	oidcID := []*huma.Param{{Name: "providerId", In: "path", Required: true, Description: "OIDC provider ID (oidc_…).",
		Schema: &huma.Schema{Type: huma.TypeString, Pattern: "^oidc_[0-9a-z]{26}$"}, Example: "oidc_01m5d68e607knapbtf5smmk5re"}}
	ext3 := httpapi.Phase("P3")
	doc(&huma.Operation{OperationID: "listPublicOidcProviders", Method: http.MethodGet, Path: BasePath + "/oidc/providers",
		Summary:     "List enabled OIDC providers for the login page",
		Description: "Public. Returns only the ID and button label of every enabled provider, for the login page buttons. Read-only.",
		Security:    public, Extensions: ext3,
		Responses: jsonResp("Enabled providers.", &huma.Schema{Type: huma.TypeObject, Required: []string{"data"}, Properties: map[string]*huma.Schema{
			"data": {Type: huma.TypeArray, Description: "Enabled providers.", Items: schemaOf(reflect.TypeFor[OIDCPublicProvider]())}}},
			map[string]any{"data": []any{map[string]any{"id": "oidc_01m5d68e607knapbtf5smmk5re", "buttonLabel": "Sign in with Authentik"}}}),
		Metadata: httpapi.Problems(errs.CodeInternalError),
	})
	found := map[string]*huma.Param{"Location": {Description: "Where the browser goes next.", Schema: &huma.Schema{Type: huma.TypeString},
		Example: "https://auth.example.com/application/o/authorize/?client_id=emailbridge-admin&response_type=code&scope=openid+email+profile&state=b3f1c2&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256&nonce=n-0S6_WzA2Mj"}}
	doc(&huma.Operation{OperationID: "startOidcLogin", Method: http.MethodGet, Path: BasePath + "/oidc/{providerId}/start",
		Summary: "Begin OIDC Authorization Code + PKCE flow",
		Description: "Stores the PKCE verifier, nonce and return path server-side and redirects (`302`) the browser to the " +
			"provider's authorization endpoint. `redirect_uri` must be a trusted same-origin path (`422` " +
			"otherwise). Public, rate limited like sign-in. A disabled or unknown provider is `404`.",
		Security: public, Extensions: ext3,
		Parameters: append(append([]*huma.Param{}, oidcID...), &huma.Param{Name: "redirect_uri", In: "query",
			Description: "Same-origin path to return to after sign-in (default /).", Schema: &huma.Schema{Type: huma.TypeString}, Example: "/accounts"}),
		Responses: map[string]*huma.Response{"302": {Description: "Redirect to the provider.", Headers: found}},
		Metadata:  httpapi.Problems(errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeRateLimited, errs.CodeServiceUnavailable),
	})
	doc(&huma.Operation{OperationID: "oidcCallback", Method: http.MethodGet, Path: BasePath + "/oidc/{providerId}/callback",
		Summary: "OIDC callback — verify the ID token and issue a session",
		Description: "The provider redirects here. EmailBridge exchanges the code (with PKCE), verifies the ID token " +
			"(issuer, audience, expiry, nonce, JWKS signature), requires a verified email in an allowed domain, " +
			"finds or provisions the user (group → role mapping), sets `eb_session` and redirects to the stored " +
			"path. On any failure it redirects to `/login?sso_error=1` (or answers `401 oidc_failed` to JSON " +
			"clients). Writes `auth.oidc.*` audit records. Public.",
		Security: public, Extensions: ext3,
		Parameters: append(append([]*huma.Param{}, oidcID...),
			&huma.Param{Name: "code", In: "query", Description: "Authorization code from the provider.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "c2b7a1f0e9d84c3b"},
			&huma.Param{Name: "state", In: "query", Description: "State from the start step.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "b3f1c2"},
			&huma.Param{Name: "error", In: "query", Description: "Error reported by the provider instead of a code.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "access_denied"}),
		Responses: map[string]*huma.Response{"302": {Description: "Back to the app (signed in) or to /login?sso_error=1.", Headers: map[string]*huma.Param{
			"Location":   {Description: "Return path, or the login page with sso_error=1.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "/accounts"},
			"Set-Cookie": cookie["Set-Cookie"],
		}}},
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeOIDCFailed, Detail: "Sign-in with this provider failed."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited}),
	})
}
