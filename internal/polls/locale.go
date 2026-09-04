package polls

import "context"

// LocaleSource resolves a signed-in user's preferred mail locale. In production this is
// auth.Service (its LocaleFor reads user_preferences.locale, "en" fallback — Plan A); tests pass
// a map. nil (SetLocaleSource never called) means every user-identified recipient renders in "en",
// which is what this package did before user locales existed at all.
type LocaleSource interface {
	LocaleFor(ctx context.Context, userID string) string
}

// SetLocaleSource wires the user-locale lookup — a post-construction setter, like SetStats, so
// every existing NewService call site (tests included) stays unchanged. main.go calls it before
// the job worker starts, so no mail:poll job can run against a half-wired Service.
func (s *Service) SetLocaleSource(src LocaleSource) {
	s.locales = src
}

// userLocale is every user-recipient send path's locale lookup (resolveRecipients, and the
// userId branches of sendFinalizedMail/sendClosedMail/sendReminderMail/sendDigestMail): the
// LocaleSource's answer, or "en" when there is no source, no user, or an empty answer.
// Participants (guests) never come through here — they carry their own participants.locale
// column, read via orDefaultLocale.
func (s *Service) userLocale(ctx context.Context, userID string) string {
	if s.locales == nil || userID == "" {
		return "en"
	}
	if l := s.locales.LocaleFor(ctx, userID); l != "" {
		return l
	}
	return "en"
}
