package mailer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/refsdal/whenweall/internal/mailer"
)

func TestRequestLocale(t *testing.T) {
	cases := []struct {
		name, cookie, acceptLanguage, want string
	}{
		{"nothing → first supported", "", "", "en"},
		{"cookie wins", "nb", "en", "nb"},
		{"unsupported cookie ignored, Accept-Language used", "de", "nb-NO,nb;q=0.9,en;q=0.8", "nb"},
		{"Accept-Language base-language match", "", "nb-NO", "nb"},
		{"Accept-Language q-ordering", "", "en;q=0.5, nb;q=0.9", "nb"},
		{"Accept-Language skips unsupported and wildcard", "", "de, *;q=0.1, en;q=0.2", "en"},
		{"Accept-Language q=0 excluded", "", "nb;q=0, en", "en"},
		{"garbage header → default", "", ";;,,q=", "en"},
		{"cookie matches case-insensitively", "NB", "", "nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: mailer.LocaleCookieName, Value: tc.cookie})
			}
			if tc.acceptLanguage != "" {
				r.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if got := mailer.RequestLocale(r); got != tc.want {
				t.Errorf("RequestLocale = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRequestLocaleNilRequest pins RequestLocale's nil-safety: a nil *http.Request (which never
// happens for a real handler, but keeps the function total) falls straight through to
// SupportedLocales[0], same as a request with no cookie and no Accept-Language.
func TestRequestLocaleNilRequest(t *testing.T) {
	if got := mailer.RequestLocale(nil); got != "en" {
		t.Errorf("RequestLocale(nil) = %q, want en", got)
	}
}
