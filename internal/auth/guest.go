package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MintGuestToken returns "<participantID>.<hex hmac-sha256(secret, participantID)>" — the edit
// token handed to an anonymous poll participant (no account, no session) so they can later prove
// they're the one who created a given participant row. Ports the intent of
// src/server/polls/claim-auth.ts / comment-auth.ts's editToken, which plays the same role there.
func (s *Service) MintGuestToken(participantID string) string {
	return participantID + "." + hex.EncodeToString(s.guestTokenSignature(participantID))
}

// VerifyGuestToken returns the participantID iff token's signature verifies against it under
// cfg.AuthSecret (compared in constant time via hmac.Equal). Any malformed or tampered token —
// empty, missing the "." separator, a participantID that doesn't match its own signature, a
// signature that isn't valid hex, or a token minted under a different secret — is rejected with
// ok == false.
func (s *Service) VerifyGuestToken(token string) (participantID string, ok bool) {
	id, sigHex, found := strings.Cut(token, ".")
	if !found || id == "" || sigHex == "" {
		return "", false
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", false
	}

	if !hmac.Equal(sig, s.guestTokenSignature(id)) {
		return "", false
	}
	return id, true
}

// guestTokenSignature computes hmac-sha256(cfg.AuthSecret, participantID).
func (s *Service) guestTokenSignature(participantID string) []byte {
	mac := hmac.New(sha256.New, []byte(s.cfg.AuthSecret))
	mac.Write([]byte(participantID))
	return mac.Sum(nil)
}
