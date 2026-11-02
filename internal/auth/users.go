package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thecodearcher/limen"
	twofactor "github.com/thecodearcher/limen/plugins/two-factor"

	"github.com/radiobros/emailbridge/internal/errs"
)

// ValidatePassword applies the password policy (S10 §2.4): 12 to 128 characters, no composition rules,
// refused when it appears in the bundled common-password list. password_too_common is not in the
// catalogue yet, so the failure is validation_failed with safe field reason=common_password.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < PasswordMinLength {
		return errs.New(errs.CodeValidationFailed, "The password must be at least 12 characters.")
	}
	if n > PasswordMaxLength {
		return errs.New(errs.CodeValidationFailed, "The password must be at most 128 characters.")
	}
	if isCommonPassword(password) {
		return errs.New(errs.CodeValidationFailed, "Choose a less common password.").
			WithSafe("reason", "common_password")
	}
	return nil
}

// CreateUser creates a Limen user with a password and marks the address verified (bootstrap and
// invitations prove ownership; Limen's programmatic sign-up ignores EmailVerifiedAt, SP3 finding 8).
func (s *Service) CreateUser(ctx context.Context, email, password string) (string, error) {
	email = limen.NormalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return "", errs.New(errs.CodeValidationFailed, "A valid email address is required.")
	}
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	res, err := s.password.SignUpWithCredentialAndPassword(ctx, &limen.User{Email: email, Password: &password}, nil)
	if err != nil {
		var le *limen.LimenError
		if errors.As(err, &le) && le.Status() == 409 {
			return "", errs.Wrap(err, errs.CodeConflict, "auth.CreateUser")
		}
		return "", fmt.Errorf("auth: create user: %w", err)
	}
	if res == nil || res.User == nil {
		return "", errors.New("auth: create user: no user returned")
	}
	id := fmt.Sprint(res.User.ID)
	if _, err := s.db.Update(ctx, "users", []limen.Where{limen.Eq("id", id)},
		map[string]any{"email_verified_at": time.Now().UTC()}); err != nil {
		return "", fmt.Errorf("auth: mark email verified: %w", err)
	}
	return id, nil
}

// SetPassword replaces the user's password (invitation acceptance, admin reset) and revokes every session.
func (s *Service) SetPassword(ctx context.Context, authUserID, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := s.password.HashPassword(password)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	res, err := s.db.Update(ctx, "users", []limen.Where{limen.Eq("id", authUserID)},
		map[string]any{"password": hash, "updated_at": time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	if res.RowsAffected == 0 {
		return errs.New(errs.CodeNotFound, "No such user.")
	}
	return s.RevokeSessions(ctx, authUserID)
}

// DeleteUser removes the Limen user and everything that belongs to it (sessions, second factors,
// passkeys, API keys, linked accounts) in one transaction. Deleting a missing user is not an error.
func (s *Service) DeleteUser(ctx context.Context, authUserID string) error {
	tx, err := s.db.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	row, err := tx.FindOne(ctx, "users", []limen.Where{limen.Eq("id", authUserID)}, nil)
	if errors.Is(err, limen.ErrRecordNotFound) {
		_ = tx.Rollback()
		return nil
	}
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("auth: delete user: %w", err)
	}
	email, _ := row["email"].(string)
	deletes := []struct {
		table limen.SchemaTableName
		where []limen.Where
	}{
		{"sessions", []limen.Where{limen.Eq("user_id", authUserID)}},
		{"two_factors", []limen.Where{limen.Eq("user_id", authUserID)}},
		{"passkeys", []limen.Where{limen.Eq("user_id", authUserID)}},
		{"api_keys", []limen.Where{limen.Eq("principal_id", authUserID)}},
		{"accounts", []limen.Where{limen.Eq("user_id", authUserID)}},
		{"verifications", []limen.Where{limen.EndsWith("subject", "::"+email)}},
		{"users", []limen.Where{limen.Eq("id", authUserID)}},
	}
	for _, d := range deletes {
		if d.table == "verifications" && email == "" {
			continue
		}
		if _, err := tx.Delete(ctx, d.table, d.where); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("auth: delete user: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	return nil
}

// RevokeSessions signs the user out everywhere and purges any cached session entries.
func (s *Service) RevokeSessions(ctx context.Context, authUserID string) error {
	sessions, err := s.limen.ListSessions(ctx, authUserID)
	if err != nil {
		return fmt.Errorf("auth: list sessions: %w", err)
	}
	if err := s.limen.RevokeAllSessions(ctx, authUserID); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	for _, sess := range sessions {
		if err := s.purgeSessionCache(ctx, authUserID, sess.Token); err != nil {
			return err
		}
	}
	return nil
}

// HasMFA reports whether the user has a second factor: TOTP (which also enables email codes and backup
// codes — Limen has no email-only enrolment) or at least one passkey.
func (s *Service) HasMFA(ctx context.Context, authUserID string) (bool, error) {
	row, err := s.db.FindOne(ctx, "users", []limen.Where{limen.Eq("id", authUserID)}, nil)
	if errors.Is(err, limen.ErrRecordNotFound) {
		return false, errs.New(errs.CodeNotFound, "No such user.")
	}
	if err != nil {
		return false, fmt.Errorf("auth: has mfa: %w", err)
	}
	if enabled, _ := row[string(twofactor.UserWithTwoFactorSchemaEnabledField)].(bool); enabled {
		return true, nil
	}
	keys, err := s.passkeys.FindPasskeysByUserID(ctx, authUserID)
	if err != nil {
		return false, fmt.Errorf("auth: has mfa: %w", err)
	}
	return len(keys) > 0, nil
}
