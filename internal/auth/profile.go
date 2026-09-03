package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thecodearcher/limen"

	"github.com/refsdal/whenweall/internal/mailer"
)

// Profile is the seam's view of a user's editable account data plus the two facts every other
// package keeps needing about a user: what to call them and which locale to write to them in.
// Name never comes back empty (falls back to the email's local part) and Locale is always one of
// mailer.SupportedLocales (falls back to "en").
type Profile struct {
	UserID        string
	Name          string
	Locale        string
	EmailVerified bool
}

// ErrNoSuchUser is returned by the profile/account methods for an id or email that matches no
// users row (including a non-numeric id string).
var ErrNoSuchUser = errors.New("auth: no such user")

// ProfileValidationError reports a rejected SetProfile input. Field is "name" or "locale";
// internal/httpserver maps it to the standard 422 "invalid" envelope with Fields{Field: Message}.
type ProfileValidationError struct {
	Field   string
	Message string
}

func (e *ProfileValidationError) Error() string {
	return "auth: invalid " + e.Field + ": " + e.Message
}

// maxProfileNameRunes mirrors the signup form's own limit (web/src/routes/signup.tsx: "Keep your
// name under 80 characters").
const maxProfileNameRunes = 80

// DisplayName composes a user's display name from Limen's nullable first_name/last_name columns,
// falling back to the email's local part when both are blank (same rule as internal/admin's
// composeUserName and the displayName helpers in internal/polls and internal/bookings).
func DisplayName(firstName, lastName, email string) string {
	name := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if name != "" {
		return name
	}
	return nameFromEmail(email)
}

// normalizeLocale returns locale if the mailer supports it, else "en" — Profile.Locale is never
// something a template can't render.
func normalizeLocale(locale string) string {
	if mailer.IsSupportedLocale(locale) {
		return locale
	}
	return "en"
}

// splitName stores "Ada Lovelace" as first_name "Ada" / last_name "Lovelace" (everything after
// the first space is the last name, so DisplayName reassembles it losslessly).
func splitName(name string) (first, last string) {
	parts := strings.SplitN(name, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

const profileSelect = `
	SELECT u.id, u.email, coalesce(u.first_name, ''), coalesce(u.last_name, ''),
	       u.email_verified_at IS NOT NULL, coalesce(p.locale, 'en')
	FROM users u
	LEFT JOIN user_preferences p ON p.user_id = u.id
	WHERE `

// GetProfile loads userID's Profile. ErrNoSuchUser for an unknown or non-numeric id.
func (s *Service) GetProfile(ctx context.Context, userID string) (Profile, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return Profile{}, ErrNoSuchUser
	}
	return s.loadProfile(ctx, "u.id = $1", uid)
}

// profileByEmail is GetProfile keyed on the (normalized) email — for Limen's mail callbacks,
// which only ever hand this package an email address.
func (s *Service) profileByEmail(ctx context.Context, email string) (Profile, error) {
	return s.loadProfile(ctx, "u.email = $1", limen.NormalizeEmail(email))
}

func (s *Service) loadProfile(ctx context.Context, where string, arg any) (Profile, error) {
	var (
		id                 int64
		email, first, last string
		verified           bool
		locale             string
	)
	err := s.db.QueryRowContext(ctx, profileSelect+where, arg).Scan(&id, &email, &first, &last, &verified, &locale)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNoSuchUser
	}
	if err != nil {
		return Profile{}, fmt.Errorf("auth: loading profile: %w", err)
	}
	return Profile{
		UserID:        strconv.FormatInt(id, 10),
		Name:          DisplayName(first, last, email),
		Locale:        normalizeLocale(locale),
		EmailVerified: verified,
	}, nil
}

// SetProfile updates the parts of a profile the user may edit. nil means "leave unchanged".
// name is whitespace-collapsed and must be 1..80 runes; locale must be one of
// mailer.SupportedLocales. Both are validated before anything is written, so a request that fails
// validation changes nothing.
func (s *Service) SetProfile(ctx context.Context, userID string, name *string, locale *string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}

	var first, last string
	if name != nil {
		trimmed := strings.Join(strings.Fields(*name), " ")
		if trimmed == "" {
			return &ProfileValidationError{Field: "name", Message: "name is required"}
		}
		if utf8.RuneCountInString(trimmed) > maxProfileNameRunes {
			return &ProfileValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxProfileNameRunes)}
		}
		first, last = splitName(trimmed)
	}
	if locale != nil && !mailer.IsSupportedLocale(*locale) {
		return &ProfileValidationError{Field: "locale", Message: "locale must be one of " + strings.Join(mailer.SupportedLocales, ", ")}
	}
	if name == nil && locale == nil {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin set-profile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if name != nil {
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET first_name = $2, last_name = NULLIF($3, ''), updated_at = now() WHERE id = $1`,
			uid, first, last)
		if err != nil {
			return fmt.Errorf("auth: updating name: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoSuchUser
		}
	}
	if locale != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_preferences (user_id, locale, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE SET locale = EXCLUDED.locale, updated_at = now()
		`, uid, *locale); err != nil {
			// 23503 = foreign_key_violation: no users row for uid.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return ErrNoSuchUser
			}
			return fmt.Errorf("auth: upserting locale: %w", err)
		}
	}
	return tx.Commit()
}

// LocaleFor is the cheap "which locale do I write this user in" helper for mail senders: the
// stored locale, or "en" when the user is unknown, has no preference, or the lookup fails (a mail
// in the wrong language beats no mail).
func (s *Service) LocaleFor(ctx context.Context, userID string) string {
	p, err := s.GetProfile(ctx, userID)
	if err != nil {
		return "en"
	}
	return p.Locale
}

// MarkEmailVerified sets email_verified_at (if not already set) for the user with this email.
// Used by the e2e seed route (internal/httpserver/testroutes.go) and by tests that need a usable
// account without driving the mailed token round trip; production code never calls it — Limen's
// POST /verify-email is the real path.
func (s *Service) MarkEmailVerified(ctx context.Context, email string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
		WHERE email = $1
	`, limen.NormalizeEmail(email))
	if err != nil {
		return fmt.Errorf("auth: marking %q verified: %w", email, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchUser
	}
	return nil
}
