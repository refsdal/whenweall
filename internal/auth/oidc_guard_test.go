package auth

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/oauth2"

	"github.com/thecodearcher/limen/plugins/oauth"
)

type fakeProvider struct {
	name string
	info *oauth.ProviderUserInfo
	err  error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) OAuth2Config() (*oauth2.Config, []oauth2.AuthCodeOption) {
	return &oauth2.Config{}, nil
}
func (f *fakeProvider) GetUserInfo(context.Context, *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
	return f.info, f.err
}

func TestVerifiedEmailUserInfoGuardsOnlyTheNamedProvider(t *testing.T) {
	google := &fakeProvider{name: "google", info: &oauth.ProviderUserInfo{ID: "g1", Email: "g@example.com", EmailVerified: false}}
	sso := &fakeProvider{name: "sso", info: &oauth.ProviderUserInfo{ID: "s1", Email: "s@example.com", EmailVerified: false}}
	fn := verifiedEmailUserInfo([]oauth.Provider{google, sso}, "sso")
	ctx := context.Background()

	if _, err := fn(ctx, "google", &oauth.TokenResponse{}); err != nil {
		t.Errorf("google (unguarded) returned %v, want the provider's own info", err)
	}
	if _, err := fn(ctx, "sso", &oauth.TokenResponse{}); !errors.Is(err, ErrOIDCEmailUnverified) {
		t.Errorf("sso with email_verified=false returned %v, want ErrOIDCEmailUnverified", err)
	}

	sso.info.EmailVerified = true
	info, err := fn(ctx, "sso", &oauth.TokenResponse{})
	if err != nil || info == nil || info.Email != "s@example.com" {
		t.Errorf("sso with email_verified=true returned (%+v, %v), want the provider's info", info, err)
	}

	sso.err = errors.New("idp down")
	if _, err := fn(ctx, "sso", &oauth.TokenResponse{}); err == nil || errors.Is(err, ErrOIDCEmailUnverified) {
		t.Errorf("provider error was %v, want it passed through untouched", err)
	}
	if _, err := fn(ctx, "unknown", &oauth.TokenResponse{}); err == nil {
		t.Error("unknown provider returned nil error")
	}
}
