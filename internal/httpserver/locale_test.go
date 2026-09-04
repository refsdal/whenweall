package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
)

func TestRequestLocale(t *testing.T) {
	supported := []string{"en", "nb"}
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: httpserver.LocaleCookieName, Value: tc.cookie})
			}
			if tc.acceptLanguage != "" {
				r.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if got := httpserver.RequestLocale(r, supported); got != tc.want {
				t.Errorf("RequestLocale = %q, want %q", got, tc.want)
			}
		})
	}
}
