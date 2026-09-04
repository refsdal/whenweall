package routekey

import (
	"net/http"
	"testing"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"plain path", http.MethodPost, "/api/v1/auth/signin/credential", "POST /api/v1/auth/signin/credential"},
		{"trailing slash collapses", http.MethodPost, "/api/v1/auth/signin/credential/", "POST /api/v1/auth/signin/credential"},
		{"double trailing slash collapses", http.MethodPost, "/api/v1/auth/signin/credential//", "POST /api/v1/auth/signin/credential"},
		{"dot segments collapse", http.MethodGet, "/api/v1/./me", "GET /api/v1/me"},
		// path.Clean(strings.TrimSuffix("/", "/")) is path.Clean("") == "." — a pre-existing quirk
		// of the exact expression this package replaces (never a registered route in any of the
		// three route tables that key on this), preserved here rather than changed by the
		// refactor.
		{"root", http.MethodGet, "/", "GET ."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(tc.method, "http://x"+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if got := Of(r); got != tc.want {
				t.Errorf("Of() = %q, want %q", got, tc.want)
			}
		})
	}
}
