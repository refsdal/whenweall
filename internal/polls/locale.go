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

// userLocaleMemo is userLocale with an optional per-call cache: when memo is non-nil, a userID
// already looked up on an earlier call (within the same memo) is returned from it instead of
// making another LocaleSource round trip. resolveRecipients uses this — see its own doc comment —
// so fanOutDigestItems can pass ONE memo shared across every distinct event it resolves recipients
// for inside a single "poll.digest" job, collapsing what would otherwise be one LocaleSource call
// per (event, recipient) pair sharing the same advisory-locked transaction into one per distinct
// recipient for the whole job. A nil memo (every other resolveRecipients call site — each fires at
// most once per job, so there is nothing to memoize) falls straight through to userLocale.
func (s *Service) userLocaleMemo(ctx context.Context, userID string, memo map[string]string) string {
	if memo == nil {
		return s.userLocale(ctx, userID)
	}
	if l, ok := memo[userID]; ok {
		return l
	}
	l := s.userLocale(ctx, userID)
	memo[userID] = l
	return l
}
