package clientip

import (
	"net/http"
	"testing"
)

func TestFromRequest(t *testing.T) {
	cases := []struct {
		name       string
		remote     string
		xff        string
		trustProxy bool
		want       string
	}{
		{"remote addr host:port", "203.0.113.9:4444", "", false, "203.0.113.9"},
		{"ignores XFF without trust", "203.0.113.9:4444", "198.51.100.1", false, "203.0.113.9"},
		{"rightmost XFF with trust", "10.0.0.2:4444", "198.51.100.1, 198.51.100.2", true, "198.51.100.2"},
		{"single XFF with trust", "10.0.0.2:4444", "198.51.100.1", true, "198.51.100.1"},
		{"blank XFF with trust falls back", "10.0.0.2:4444", " , ", true, "10.0.0.2"},
		{"remote addr without port", "203.0.113.9", "", false, "203.0.113.9"},
		{"ipv6 remote addr", "[2001:db8::1]:443", "", false, "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := FromRequest(r, tc.trustProxy); got != tc.want {
				t.Errorf("FromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}
