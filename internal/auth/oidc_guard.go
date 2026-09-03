package auth

import (
	"context"
	"net/http"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/oauth"
)

// ErrOIDCEmailUnverified is returned to Limen's OAuth callback when the generic OIDC provider's
// userinfo does not assert email_verified. Limen turns it into the callback's error redirect
// (?error=...), so the browser lands back on the SPA with a message instead of a session.
var ErrOIDCEmailUnverified = limen.NewLimenError("your identity provider did not report a verified email address", http.StatusForbidden, nil)

// verifiedEmailUserInfo builds the oauth.WithGetUserInfo override: it delegates to the named
// provider's own GetUserInfo (so Google, and the generic provider's id_token/userinfo handling,
// keep working exactly as before) and, for guardedProvider only, refuses a profile whose
// EmailVerified is false.
//
// Why: Limen's CreateOrLinkAccount links a provider account to an EXISTING user found by email
// with no verified-email check (plugins/oauth account_linker.go). With Google that is fine — it
// only ever asserts verified addresses — but a bring-your-own OIDC issuer (Authentik, Keycloak)
// may let its users set an unverified email, and anyone who can present victim@example.com from
// that IdP would take over the victim's password account. Refusing at userinfo time is the
// earliest fork-free point: nothing is created or linked yet.
func verifiedEmailUserInfo(providers []oauth.Provider, guardedProvider string) func(context.Context, string, *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
	byName := make(map[string]oauth.Provider, len(providers))
	for _, p := range providers {
		byName[p.Name()] = p
	}
	return func(ctx context.Context, providerName string, token *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
		p, ok := byName[providerName]
		if !ok {
			return nil, limen.NewLimenError("unknown oauth provider", http.StatusNotFound, nil)
		}
		info, err := p.GetUserInfo(ctx, token)
		if err != nil {
			return nil, err
		}
		if providerName == guardedProvider && (info == nil || !info.EmailVerified) {
			return nil, ErrOIDCEmailUnverified
		}
		return info, nil
	}
}
