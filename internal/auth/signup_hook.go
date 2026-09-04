package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/thecodearcher/limen"

	"github.com/refsdal/whenweall/internal/mailer"
)

// pendingSignupProfile is one entry of Service.pendingSignupProfiles — see that field's doc
// comment (auth.go) for what this does and does not guarantee.
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
// would always build the very first mail from the pre-signup state. The Before hook exists purely
// to give enqueueTokenMail (which Limen calls with only an email and a token — it has no hc, no
// request) something to read in time; afterSignup does NOT depend on it — see afterSignup's doc
// comment for why.
func (s *Service) signupHooks() *limen.Hooks {
	matchSignup := func(hc *limen.HookContext) bool { return hc.RouteID() == "signup" }
	return &limen.Hooks{
		Before: []*limen.Hook{{PathMatcher: matchSignup, Run: s.beforeSignup}},
		After:  []*limen.Hook{{PathMatcher: matchSignup, Run: s.afterSignup}},
	}
}

// beforeSignup reads the signup request's `name`/`locale` (and the locale-cookie/Accept-Language
// fallbacks) before the account is created, and stashes them in pendingSignupProfiles keyed by the
// (normalized) email in the request body, purely as a hint for enqueueTokenMail's verify_email
// dispatch — see Service.pendingSignupProfiles' doc comment (auth.go) for the narrow, deliberately
// accepted race this leaves in place for that one consumer, and for why afterSignup does not read
// this map at all. When the body carries no email at all (malformed request; the handler itself
// will reject it) there is nothing to key on, so it stores nothing. Always returns true (Before
// hooks' false return would abort the request, which this must never do).
func (s *Service) beforeSignup(hc *limen.HookContext) bool {
	email, _ := hc.GetJSONBodyValue("email").(string)
	if email == "" {
		return true
	}
	name, locale := signupProfileFromRequest(hc)
	s.pendingSignupProfiles.Store(limen.NormalizeEmail(email), pendingSignupProfile{name: name, locale: locale})
	return true
}

// afterSignup persists the submitted name/locale onto the account this request just created, then
// always clears this request's pendingSignupProfiles entry (success, validation failure, or
// duplicate email all end the request the same way: no reason to keep a mail-dispatch hint around
// once the request is done with it).
//
// Deliberately recomputes name/locale straight from hc — via signupProfileFromRequest, the same
// helper beforeSignup uses — rather than reading pendingSignupProfiles. hc is never shared across
// requests: router.go's wrapHandler builds exactly one *HookContext per request
// (prepareHookContext) and threads that same pointer through both runBeforeHooks and
// runAfterHooks for it, so hc.GetJSONBodyValue here always reflects THIS request's own body,
// regardless of what any concurrent signup attempt for the same address is doing to the shared
// map. This is what actually closes the race a code review found in the first version of this
// mechanism (last-write-wins on an email-keyed map, read back by afterSignup): two concurrent
// signups for the same address can no longer cross-contaminate each other's persisted profile,
// because afterSignup never consults anything the other request could have touched.
//
// It never fails the request: the account exists (or doesn't) by the time this runs, and a
// profile hiccup is not a reason to tell the user their signup failed. Always returns true (After
// hooks' return value is ignored anyway).
func (s *Service) afterSignup(hc *limen.HookContext) bool {
	email, _ := hc.GetJSONBodyValue("email").(string)
	defer s.pendingSignupProfiles.Delete(limen.NormalizeEmail(email))

	result := hc.GetAuthResult()
	if result == nil || result.User == nil {
		return true // signup failed (validation, duplicate email, ...) — nothing was created
	}
	userID := fmt.Sprint(result.User.ID)
	name, locale := signupProfileFromRequest(hc)

	// context.Background(): same reasoning as sendMail — the request context may be on its way
	// out, and this is bookkeeping for a row that already exists. name/locale are already
	// validated by signupProfileFromRequest, so this should never fail on ProfileValidationError;
	// any error here is just logged rather than surfaced.
	if err := s.SetProfile(context.Background(), userID, name, &locale); err != nil {
		s.logger.Error("auth: storing signup profile failed", "user_id", userID, "error", err)
	}
	return true
}

// signupProfileFromRequest extracts and pre-validates a signup request's `name`/`locale` from hc:
// name is nil unless it survives normalizeProfileName (profile.go — the exact rule SetProfile
// itself enforces, kept in one place so the two can never drift), and locale is always one of
// mailer.SupportedLocales (see requestLocale). Called from both signup hooks against the SAME hc
// for a given request, so they always agree on what that one request's profile is.
func signupProfileFromRequest(hc *limen.HookContext) (name *string, locale string) {
	if raw, _ := hc.GetJSONBodyValue("name").(string); raw != "" {
		if trimmed, ok := normalizeProfileName(raw); ok {
			name = &trimmed
		}
	}
	return name, requestLocale(hc.Request(), hc.GetJSONBodyValue("locale"))
}

// requestLocale picks the signup's locale: an explicit supported `locale` body value first, else
// mailer.RequestLocale's cookie/Accept-Language resolution over r (the whenweall_locale cookie,
// then the best Accept-Language match), which is the SAME resolution the roster-CSV route uses —
// see mailer.RequestLocale's own doc comment for why that used to be two divergent
// implementations. Only exact members of mailer.SupportedLocales are ever returned.
func requestLocale(r *http.Request, bodyLocale any) string {
	if l, ok := bodyLocale.(string); ok && mailer.IsSupportedLocale(l) {
		return l
	}
	return mailer.RequestLocale(r)
}
