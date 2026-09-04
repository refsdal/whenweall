package mailer_test

import (
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/mailer"
)

const testSecret = "test-secret-at-least-32-characters-long"

func TestUnsubscribeTokenRoundTrip(t *testing.T) {
	token := mailer.UnsubscribeToken(testSecret, "ada@example.com")
	if token == "" {
		t.Fatal("token is empty")
	}

	got, ok := mailer.ParseUnsubscribeToken(testSecret, token)
	if !ok {
		t.Fatalf("ParseUnsubscribeToken(%q) rejected a token it just minted", token)
	}
	if got != "ada@example.com" {
		t.Errorf("email = %q, want ada@example.com", got)
	}
}

// The token is pasted into a URL and travels through mail clients, link scanners and chat
// previews, so it must survive as-is with no escaping.
func TestUnsubscribeTokenIsURLSafe(t *testing.T) {
	token := mailer.UnsubscribeToken(testSecret, "ada+weekly@example.com")
	for _, r := range token {
		safe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !safe {
			t.Fatalf("token contains %q, which needs URL-escaping: %s", r, token)
		}
	}
}

// Addresses are compared and suppressed lowercased, so the token must normalise too — otherwise
// "Ada@Example.com" and "ada@example.com" become two different suppression entries and
// unsubscribing from one leaves the other receiving mail.
func TestUnsubscribeTokenNormalisesCase(t *testing.T) {
	upper := mailer.UnsubscribeToken(testSecret, "  Ada@Example.COM ")
	lower := mailer.UnsubscribeToken(testSecret, "ada@example.com")
	if upper != lower {
		t.Errorf("token differs by case/whitespace:\n %s\n %s", upper, lower)
	}

	got, ok := mailer.ParseUnsubscribeToken(testSecret, upper)
	if !ok || got != "ada@example.com" {
		t.Errorf("ParseUnsubscribeToken = (%q, %v), want (ada@example.com, true)", got, ok)
	}
}

// The whole point of signing: holding your own link must not let you unsubscribe anyone else.
func TestUnsubscribeTokenRejectsTampering(t *testing.T) {
	valid := mailer.UnsubscribeToken(testSecret, "ada@example.com")
	parts := strings.SplitN(valid, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("token has no signature separator: %q", valid)
	}
	victim := mailer.UnsubscribeToken(testSecret, "bob@example.com")
	victimPayload := strings.SplitN(victim, ".", 2)[0]

	cases := map[string]string{
		"another address, own signature": victimPayload + "." + parts[1],
		"own address, no signature":      parts[0],
		"empty":                          "",
		"separator only":                 ".",
		"garbage":                        "not-a-token",
		"truncated signature":            parts[0] + "." + parts[1][:len(parts[1])-1],
	}
	for name, token := range cases {
		if _, ok := mailer.ParseUnsubscribeToken(testSecret, token); ok {
			t.Errorf("%s: accepted %q", name, token)
		}
	}
}

// A token minted by another deployment (or before a secret rotation) must not be honoured.
func TestUnsubscribeTokenRejectsForeignSecret(t *testing.T) {
	token := mailer.UnsubscribeToken(testSecret, "ada@example.com")
	if _, ok := mailer.ParseUnsubscribeToken("a-completely-different-secret-32ch", token); ok {
		t.Error("a token signed with another secret was accepted")
	}
}

// Every template must be deliberately classified as notification or transactional. Without this,
// adding a template silently defaults it to transactional — mail nobody can opt out of, which is
// exactly the state #47 was filed about. The failure message says what to do.
func TestEveryTemplateIsClassified(t *testing.T) {
	for _, name := range mailer.TemplateNames() {
		if _, ok := mailer.TemplateClassification(name); !ok {
			t.Errorf("template %q has no entry in notificationTemplates (render.go): "+
				"decide whether it is notification mail (consent, needs an unsubscribe path) "+
				"or transactional (the answer to something the person just did)", name)
		}
	}
}

// The reverse: an entry for a template that no longer exists is a rule guarding nothing.
func TestNoClassificationForAMissingTemplate(t *testing.T) {
	known := map[string]bool{}
	for _, name := range mailer.TemplateNames() {
		known[name] = true
	}
	for _, name := range mailer.ClassifiedTemplateNames() {
		if !known[name] {
			t.Errorf("notificationTemplates classifies %q, which is not a template", name)
		}
	}
}
