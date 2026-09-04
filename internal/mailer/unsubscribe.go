package mailer

// Unsubscribe tokens: the credential in an unsubscribe link.
//
// The requirement is narrow — a recipient must be able to stop notification mail with no account
// and no sign-in, and holding their own link must not let them stop anyone else's. That is
// exactly a signature over the address, so there is no token TABLE: the token IS the address plus
// proof we minted it, and it stays valid forever. Forever matters. An unsubscribe link is read at
// the moment someone is annoyed, which may be a year after the mail arrived, and "your
// unsubscribe link has expired" is not an answer GDPR Art. 7(3) accepts ("as easy to withdraw as
// to give").
//
// Signed with AUTH_SECRET, the same secret the rest of the app authenticates with, so rotating it
// invalidates outstanding links — an acceptable trade for having no second secret to manage. The
// address is normalised (trimmed, lowercased) before both signing and comparison so that
// "Ada@Example.com" and "ada@example.com" are one identity rather than two suppression entries.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// NormalizeEmail is the canonical form an address is signed, stored and compared in: trimmed and
// lowercased. Only the domain is case-insensitive per RFC 5321 — the local part technically is
// not — but every mailbox provider in practice treats it that way, and treating "Ada@" and "ada@"
// as different people would silently leave one of them subscribed.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// UnsubscribeToken returns the opaque token identifying email in an unsubscribe link:
// base64url(address) + "." + base64url(HMAC-SHA256(secret, address)), both unpadded so the whole
// thing survives a URL, a mail client's link rewriter and a chat preview without escaping.
func UnsubscribeToken(secret, email string) string {
	addr := NormalizeEmail(email)
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(addr)) + "." + enc.EncodeToString(unsubscribeMAC(secret, addr))
}

// ParseUnsubscribeToken returns the normalised address a token was minted for, or ok=false if it
// was not minted by this deployment. Every failure mode — wrong shape, undecodable, signature for
// a different address, signature from a different secret — returns the same (empty, false): the
// caller has nothing useful to tell the holder apart from "this link isn't valid".
func ParseUnsubscribeToken(secret, token string) (email string, ok bool) {
	payload, sig, found := strings.Cut(token, ".")
	if !found || payload == "" || sig == "" {
		return "", false
	}
	enc := base64.RawURLEncoding
	addrBytes, err := enc.DecodeString(payload)
	if err != nil {
		return "", false
	}
	gotMAC, err := enc.DecodeString(sig)
	if err != nil {
		return "", false
	}
	addr := string(addrBytes)
	// Constant-time: this comparison is the whole guard against forging a token for someone
	// else's address, and it runs on attacker-supplied input.
	if !hmac.Equal(gotMAC, unsubscribeMAC(secret, addr)) {
		return "", false
	}
	// A token minted for a non-normalised address would verify above but is not one we ever
	// issue, and accepting it would suppress an address that never matches at send time.
	if addr != NormalizeEmail(addr) {
		return "", false
	}
	return addr, true
}

func unsubscribeMAC(secret, normalizedEmail string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(normalizedEmail))
	return mac.Sum(nil)
}
