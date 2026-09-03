package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/thecodearcher/limen"

	"github.com/refsdal/whenweall/internal/mailer"
)

// localeCookieName is paraglide's cookie (web/src/paraglide/runtime.js: cookieName) — the SPA sets
// it on every locale switch, so a guest who picked Norwegian before signing up carries it here.
const localeCookieName = "whenweall_locale"

// pendingSignupProfile is one entry of Service.pendingSignupProfiles: the name/locale beforeSignup
// extracted from a signup request, already validated the way SetProfile would (name is nil rather
// than something SetProfile would reject), so enqueueTokenMail and afterSignup always agree on
// what the new user's profile is going to be.
type pendingSignupProfile struct {
	name   *string
	locale string
}

// signupHooks builds the Limen HTTP hooks: a Before and an After hook, both matched to
// credential-password's "signup" route only. Body access is confirmed against the pinned source
// (router.go parses the JSON body once before hooks run; HookContext.GetJSONBodyValue reads it),
// and so is auth-result access: SignUpWithCredentialAndPassword calls Responder.SessionResponse in
// BOTH the auto-sign-in and no-auto-sign-in branches, and SessionResponse stores the
// AuthenticationResult on the response writer before doing anything else — so GetAuthResult() is
// populated for every successful signup regardless of WithAutoSignInOnSignUp. (The earlier
// personal-org attempt at a hook failed for the OAuth callback, which redirects instead of calling
// SessionResponse; this hook is only ever matched to "signup", where that problem does not exist.)
//
// Both hooks exist (not just an After hook) because of a second timing fact confirmed against the
// same pinned source: credential-password's plugin-level SignUpWithCredentialAndPassword calls
// core.SendEmailVerificationMail — synchronously, inside itself — before the HTTP handler ever
// calls SessionResponse, i.e. strictly before any After hook can run. An After-hook-only design
// would always build the very first mail from the pre-signup state. The Before hook captures and
// pre-validates the submitted name/locale into pendingSignupProfiles (see its doc comment) so
// enqueueTokenMail can read it in time; the After hook does the actual persistence and always
// removes the entry, win or lose.
func (s *Service) signupHooks() *limen.Hooks {
	matchSignup := func(hc *limen.HookContext) bool { return hc.RouteID() == "signup" }
	return &limen.Hooks{
		Before: []*limen.Hook{{PathMatcher: matchSignup, Run: s.beforeSignup}},
		After:  []*limen.Hook{{PathMatcher: matchSignup, Run: s.afterSignup}},
	}
}

// beforeSignup reads the signup request's `name`/`locale` (and the locale-cookie/Accept-Language
// fallbacks) before the account is created, and stashes them in pendingSignupProfiles keyed by the
// (normalized) email in the request body — the only key available before the user has an id. When
// the body carries no email at all (malformed request; the handler itself will reject it) there is
// nothing to key on, so it stores nothing. Always returns true (Before hooks' false return would
// abort the request, which this must never do).
func (s *Service) beforeSignup(hc *limen.HookContext) bool {
	email, _ := hc.GetJSONBodyValue("email").(string)
	if email == "" {
		return true
	}

	var name *string
	if raw, _ := hc.GetJSONBodyValue("name").(string); raw != "" {
		name = validatedSignupName(raw)
	}
	locale := requestLocale(hc.Request(), hc.GetJSONBodyValue("locale"))

	s.pendingSignupProfiles.Store(limen.NormalizeEmail(email), pendingSignupProfile{name: name, locale: locale})
	return true
}

// afterSignup persists the profile beforeSignup captured — `name` (the form's optional Name
// field) and the locale — for the user the request just created, then always removes the pending
// entry (success, validation failure, or duplicate email all end the request the same way: no
// reason to keep the request's data around). It never fails the request: the account exists (or
// doesn't) by the time this runs, and a profile hiccup is not a reason to tell the user their
// signup failed. Always returns true (After hooks' return value is ignored anyway).
func (s *Service) afterSignup(hc *limen.HookContext) bool {
	email, _ := hc.GetJSONBodyValue("email").(string)
	key := limen.NormalizeEmail(email)
	defer s.pendingSignupProfiles.Delete(key)

	result := hc.GetAuthResult()
	if result == nil || result.User == nil {
		return true // signup failed (validation, duplicate email, ...) — nothing was created
	}
	userID := fmt.Sprint(result.User.ID)

	pending, _ := s.pendingSignupProfiles.Load(key)
	p, _ := pending.(pendingSignupProfile)

	// context.Background(): same reasoning as sendMail — the request context may be on its way
	// out, and this is bookkeeping for a row that already exists. name/locale were already
	// validated by beforeSignup, so this should never fail on ProfileValidationError; any error
	// here is just logged rather than surfaced.
	if err := s.SetProfile(context.Background(), userID, p.name, &p.locale); err != nil {
		s.logger.Error("auth: storing signup profile failed", "user_id", userID, "error", err)
	}
	return true
}

// validatedSignupName trims and collapses whitespace in name the same way SetProfile does, and
// returns nil if the result is blank or over maxProfileNameRunes — mirrors SetProfile's own check
// so the value cached for the verification-email dispatch always matches what afterSignup ends up
// persisting (an invalid name is silently dropped, not sent to a user and then contradicted by
// their stored profile).
func validatedSignupName(name string) *string {
	trimmed := strings.Join(strings.Fields(name), " ")
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maxProfileNameRunes {
		return nil
	}
	return &trimmed
}

// requestLocale picks the signup's locale: an explicit supported `locale` body value first, then
// the whenweall_locale cookie, then the first supported language in Accept-Language (base tag,
// so "nb-NO" counts as "nb"), else "en". Only exact members of mailer.SupportedLocales are ever
// returned.
func requestLocale(r *http.Request, bodyLocale any) string {
	if l, ok := bodyLocale.(string); ok && mailer.IsSupportedLocale(l) {
		return l
	}
	if r != nil {
		if c, err := r.Cookie(localeCookieName); err == nil && mailer.IsSupportedLocale(c.Value) {
			return c.Value
		}
		if l := acceptLanguageLocale(r.Header.Get("Accept-Language")); l != "" {
			return l
		}
	}
	return "en"
}

// acceptLanguageLocale returns the first supported base language in an Accept-Language header
// ("nb-NO,nb;q=0.9,en;q=0.8" -> "nb"), or "" when none is supported. Quality values are ignored
// beyond list order, which is how browsers order the list anyway.
func acceptLanguageLocale(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" || tag == "*" {
			continue
		}
		base := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		if mailer.IsSupportedLocale(base) {
			return base
		}
	}
	return ""
}
