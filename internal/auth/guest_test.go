package auth

import (
	"testing"

	"github.com/refsdal/whenweall/internal/config"
)

// guestTestService builds a bare Service carrying only the cfg.AuthSecret MintGuestToken and
// VerifyGuestToken actually touch — no Limen instance, no database, since guest tokens are pure
// HMAC and never consult either.
func guestTestService(secret string) *Service {
	return &Service{cfg: &config.Config{AuthSecret: secret}}
}

func TestGuestTokenRoundTrip(t *testing.T) {
	s := guestTestService("a-secret-at-least-32-chars-long!")
	token := s.MintGuestToken("participant-1")

	gotID, ok := s.VerifyGuestToken(token)
	if !ok {
		t.Fatalf("VerifyGuestToken(%q) ok = false, want true", token)
	}
	if gotID != "participant-1" {
		t.Errorf("VerifyGuestToken(%q) participantID = %q, want %q", token, gotID, "participant-1")
	}
}

func TestGuestTokenTamperedParticipantIDRejected(t *testing.T) {
	s := guestTestService("a-secret-at-least-32-chars-long!")
	token := s.MintGuestToken("participant-1")

	// Swap the id portion but keep the original signature — a forged token trying to claim a
	// different participant's edit token.
	otherToken := "participant-2" + token[len("participant-1"):]

	if _, ok := s.VerifyGuestToken(otherToken); ok {
		t.Errorf("VerifyGuestToken(%q) ok = true for a tampered participant id, want false", otherToken)
	}
}

func TestGuestTokenTamperedSignatureRejected(t *testing.T) {
	s := guestTestService("a-secret-at-least-32-chars-long!")
	token := s.MintGuestToken("participant-1")

	tampered := token[:len(token)-1] + "0"
	if tampered == token {
		tampered = token[:len(token)-1] + "1"
	}

	if _, ok := s.VerifyGuestToken(tampered); ok {
		t.Errorf("VerifyGuestToken(%q) ok = true for a tampered signature, want false", tampered)
	}
}

func TestGuestTokenEmptyRejected(t *testing.T) {
	s := guestTestService("a-secret-at-least-32-chars-long!")

	if _, ok := s.VerifyGuestToken(""); ok {
		t.Error("VerifyGuestToken(\"\") ok = true, want false")
	}
}

func TestGuestTokenMissingDotRejected(t *testing.T) {
	s := guestTestService("a-secret-at-least-32-chars-long!")

	if _, ok := s.VerifyGuestToken("just-some-opaque-string-with-no-separator"); ok {
		t.Error("VerifyGuestToken(no dot) ok = true, want false")
	}
}

func TestGuestTokenFromDifferentSecretRejected(t *testing.T) {
	minter := guestTestService("first-secret-at-least-32-chars-long")
	verifier := guestTestService("second-secret-at-least-32-chars-lon")

	token := minter.MintGuestToken("participant-1")
	if _, ok := verifier.VerifyGuestToken(token); ok {
		t.Errorf("VerifyGuestToken(%q) ok = true under a different secret, want false", token)
	}
}
