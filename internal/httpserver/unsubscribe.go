package httpserver

// The unsubscribe endpoints (#47). Three methods on one path, all unauthenticated:
//
//	POST   /api/v1/unsubscribe?token=…  suppress   — RFC 8058 one-click, and the page's button
//	DELETE /api/v1/unsubscribe?token=…  resubscribe — the same page, undoing a misclick
//	GET    /api/v1/unsubscribe?token=…  303 to the SPA page, acting on nothing
//
// No session, by design: the people this exists for mostly have no account. A guest who left an
// address to hear a poll's outcome cannot sign in to withdraw consent, and GDPR Art. 7(3)
// requires withdrawing to be as easy as giving. The token is the whole authorisation — an HMAC
// over the address (internal/mailer.UnsubscribeToken), so it names exactly one mailbox and
// holding your own cannot touch anyone else's.
//
// Deliberately NOT rate limited, unlike the auth routes next door. A one-click POST arrives from
// the mail provider's own servers, so thousands of unrelated recipients share a handful of Google
// and Yahoo source IPs — a per-IP budget would start silently failing real unsubscribes at
// exactly the volume where they matter most, and a failed unsubscribe is what turns into a spam
// complaint. The endpoint is safe to leave open: a forged token is rejected by an HMAC comparison
// that touches no database, and a valid one is an idempotent single-row upsert of a fact its
// holder is entitled to set.

import (
	"net/http"
	"net/url"

	"github.com/refsdal/whenweall/internal/mailer"
)

func (s *Server) registerUnsubscribeRoutes() {
	s.mux.HandleFunc("POST /api/v1/unsubscribe", s.handleUnsubscribe)
	s.mux.HandleFunc("DELETE /api/v1/unsubscribe", s.handleResubscribe)
	s.mux.HandleFunc("GET /api/v1/unsubscribe", s.handleUnsubscribeGet)
}

// unsubscribeAddress verifies the ?token= and returns the address it names, or writes the 400 and
// returns "". Every rejection is the same response: the holder of a bad link has nothing to learn
// from a more specific one, and "no such address" would make this an address oracle.
func (s *Server) unsubscribeAddress(w http.ResponseWriter, r *http.Request) string {
	email, ok := mailer.ParseUnsubscribeToken(s.cfg.AuthSecret, r.URL.Query().Get("token"))
	if !ok {
		Err(w, http.StatusBadRequest, "invalid_token", "this unsubscribe link is not valid", nil)
		return ""
	}
	return email
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	email := s.unsubscribeAddress(w, r)
	if email == "" {
		return
	}
	// RFC 8058's POST body ("List-Unsubscribe=One-Click") is deliberately not read: the token
	// already says who, and the source below is only ever a hint for support. Distinguishing the
	// provider's POST from the page's own is worth that hint — a burst of one-click rows says
	// something different about a campaign than a burst of confirmed clicks does.
	source := mailer.SourceOneClick
	if r.URL.Query().Get("via") == "web" {
		source = mailer.SourceLink
	}
	if err := mailer.Suppress(r.Context(), s.db, email, source); err != nil {
		Err(w, http.StatusInternalServerError, "internal", "could not record the unsubscribe", nil)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"status": "unsubscribed", "email": email})
}

func (s *Server) handleResubscribe(w http.ResponseWriter, r *http.Request) {
	email := s.unsubscribeAddress(w, r)
	if email == "" {
		return
	}
	if err := mailer.Resubscribe(r.Context(), s.db, email); err != nil {
		Err(w, http.StatusInternalServerError, "internal", "could not record the change", nil)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"status": "subscribed", "email": email})
}

// handleUnsubscribeGet exists because some mail clients render List-Unsubscribe as an ordinary
// link and simply follow it. It must not act: a GET on that URL is also what every link scanner,
// spam filter and chat preview issues, none of which is a person choosing anything. So it
// redirects to the page, where a real click can confirm — the same reason the footer link points
// at the page directly.
func (s *Server) handleUnsubscribeGet(w http.ResponseWriter, r *http.Request) {
	// Verified before redirecting so a junk link lands on a plain error rather than on a page
	// that has to explain itself.
	email := s.unsubscribeAddress(w, r)
	if email == "" {
		return
	}
	http.Redirect(w, r, "/unsubscribe?token="+url.QueryEscape(r.URL.Query().Get("token")), http.StatusSeeOther)
}
