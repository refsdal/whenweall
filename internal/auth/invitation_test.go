package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestInvitationFlow drives the whole chain a real team goes through: the owner (with a stored
// name and locale) invites by email through Limen's route, our WithSendInvitationMail callback
// enqueues an org_invite mail whose link carries the token, the invitee signs up with that
// address, reads the invitation by token and accepts it, and ends up a member of the owner's
// organization.
func TestInvitationFlow(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	ownerEmail := "owner-invites@example.com"
	ts.signUpVerifiedAndSignIn(t, ownerEmail)
	triggerSessionResolution(t, ts) // personal org exists and is the active org
	ownerID := fmt.Sprint(lookupUserID(t, ts, ownerEmail))
	ownerName, nb := "Ada Lovelace", "nb"
	if err := ts.svc.SetProfile(ctx, ownerID, &ownerName, &nb); err != nil {
		t.Fatalf("SetProfile(owner): %v", err)
	}

	inviteeEmail := "invitee@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/invitations", map[string]any{
		"email": inviteeEmail, "role": "member",
	}), "invite")

	msg, ok := ts.mail.find("org_invite")
	if !ok {
		t.Fatal("no org_invite mail captured")
	}
	if msg.To != inviteeEmail {
		t.Errorf("org_invite To = %q, want %q", msg.To, inviteeEmail)
	}
	if got, _ := msg.Data["InviterName"].(string); got != ownerName {
		t.Errorf("Data.InviterName = %q, want %q (the stored display name, not the email local part)", got, ownerName)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("Data.Locale = %q, want nb (inviter's locale — the invitee has no account yet)", got)
	}
	url, _ := msg.Data["URL"].(string)
	const prefix = "http://app.example/accept-invitation/"
	if !strings.HasPrefix(url, prefix) || url == prefix {
		t.Fatalf("org_invite URL = %q, want %q plus a token", url, prefix)
	}
	token := strings.TrimPrefix(url, prefix)

	// The invitee: a separate browser (cookie jar) against the same server.
	invitee := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	invitee.client = &http.Client{Jar: jar}
	invitee.signUpVerifiedAndSignIn(t, inviteeEmail)
	triggerSessionResolution(t, invitee)

	inv := decodeJSON(t, invitee.get(t, "/api/v1/auth/organizations/invitations/token/"+token))
	if got, _ := inv["email"].(string); got != inviteeEmail {
		t.Errorf("invitation.email = %q, want %q (body %+v)", got, inviteeEmail, inv)
	}
	org, _ := inv["organization"].(map[string]any)
	ownerOrgSlug, _ := org["slug"].(string)
	if ownerOrgSlug == "" {
		t.Fatalf("invitation carries no organization.slug: %+v", inv)
	}

	requireStatus2xx(t, invitee.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	}), "accept")

	var members int
	if err := ts.svc.db.QueryRowContext(ctx, `
		SELECT count(*) FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE o.slug = $1 AND m.user_id = $2
	`, ownerOrgSlug, lookupUserID(t, ts, inviteeEmail)).Scan(&members); err != nil {
		t.Fatalf("counting membership: %v", err)
	}
	if members != 1 {
		t.Fatalf("membership rows for invitee in %q = %d, want 1", ownerOrgSlug, members)
	}

	orgs, err := ts.svc.ListUserOrganizations(ctx, &Session{UserID: fmt.Sprint(lookupUserID(t, ts, inviteeEmail))})
	if err != nil {
		t.Fatalf("ListUserOrganizations(invitee): %v", err)
	}
	var joined bool
	for _, o := range orgs {
		if o.Slug == ownerOrgSlug {
			joined = true
		}
	}
	if !joined || len(orgs) != 2 {
		t.Errorf("invitee orgs = %+v, want their personal org plus %q", orgs, ownerOrgSlug)
	}

	// Accepting twice is refused (the invitation is no longer pending).
	again := invitee.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	})
	defer func() { _ = again.Body.Close() }()
	if again.StatusCode/100 == 2 {
		t.Error("second accept succeeded, want a 4xx")
	}
}

// TestInvitationRejectsMismatchedEmail: whoever holds the link still needs an account under the
// invited address (Limen string-compares invitation.Email with the session user's email).
func TestInvitationRejectsMismatchedEmail(t *testing.T) {
	ts := newTestService(t)
	ts.signUpVerifiedAndSignIn(t, "owner2@example.com")
	triggerSessionResolution(t, ts)
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/invitations", map[string]any{
		"email": "right-person@example.com", "role": "member",
	}), "invite")
	msg, _ := ts.mail.find("org_invite")
	token := strings.TrimPrefix(msg.Data["URL"].(string), "http://app.example/accept-invitation/")

	stranger := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, _ := cookiejar.New(nil)
	stranger.client = &http.Client{Jar: jar}
	stranger.signUpVerifiedAndSignIn(t, "wrong-person@example.com")
	triggerSessionResolution(t, stranger)

	resp := stranger.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 {
		t.Fatal("a different account accepted someone else's invitation")
	}
}
