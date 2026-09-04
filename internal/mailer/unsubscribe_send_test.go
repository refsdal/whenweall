package mailer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

// notificationMsg is a "closed" poll notification — notification-class mail, the kind that
// carries an unsubscribe path. verifyMsg below is its transactional counterpart.
func notificationMsg(to string) mailer.Message {
	return mailer.Message{
		To:       to,
		Template: "closed",
		Data: map[string]any{
			"PollTitle":     "Team offsite",
			"PollURL":       "https://whenweall.example/p/abc123456789",
			"RecipientName": "Ada",
		},
	}
}

func verifyMsg(to string) mailer.Message {
	return mailer.Message{
		To:       to,
		Template: "verify_email",
		Data:     map[string]any{"Name": "Ada", "URL": "https://whenweall.example/verify/abc"},
	}
}

// One Mailpit and one Postgres for the whole unsubscribe surface — both are containers, and each
// case below is distinguished by its own recipient address rather than by a clean mailbox.
func TestSendHonoursTheSuppressionList(t *testing.T) {
	d := testdb.New(t)
	smtpHost, smtpPort, apiBaseURL := startMailpit(t)
	ctx := context.Background()

	cfg := &config.Config{
		SMTPHost:   smtpHost,
		SMTPPort:   smtpPort,
		EmailFrom:  "whenweall <no-reply@whenweall.example>",
		AppURL:     "https://whenweall.example",
		AuthSecret: testSecret,
	}
	m := mailer.New(cfg, d)

	if err := mailer.Suppress(ctx, d, "gone@example.com", mailer.SourceLink); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	t.Run("notification mail to an unsubscribed address is not sent", func(t *testing.T) {
		if err := m.Send(ctx, notificationMsg("gone@example.com")); err != nil {
			t.Fatalf("Send returned an error; a suppressed send is a success, not a failure: %v", err)
		}
		if n := countMessagesTo(t, apiBaseURL, "gone@example.com"); n != 0 {
			t.Errorf("%d messages delivered to an unsubscribed address, want 0", n)
		}
	})

	// Withdrawing consent to notifications must never break the account. A password reset or a
	// verification link is the direct answer to something the person just did, not marketing.
	t.Run("transactional mail to an unsubscribed address still sends", func(t *testing.T) {
		if err := m.Send(ctx, verifyMsg("gone@example.com")); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if n := countMessagesTo(t, apiBaseURL, "gone@example.com"); n != 1 {
			t.Errorf("%d transactional messages delivered, want 1", n)
		}
	})

	t.Run("notification mail to a subscribed address sends with one-click headers", func(t *testing.T) {
		if err := m.Send(ctx, notificationMsg("here@example.com")); err != nil {
			t.Fatalf("Send: %v", err)
		}
		id := onlyMessageTo(t, apiBaseURL, "here@example.com")
		headers := fetchHeaders(t, apiBaseURL, id)

		unsub := strings.Join(headers["List-Unsubscribe"], " ")
		if unsub == "" {
			t.Fatal("no List-Unsubscribe header")
		}
		// RFC 8058: the URI receiving the POST must be https, and bracketed in the header.
		if !strings.HasPrefix(unsub, "<https://whenweall.example/api/v1/unsubscribe?token=") {
			t.Errorf("List-Unsubscribe = %q, want a bracketed https one-click URL", unsub)
		}
		if !strings.Contains(unsub, mailer.UnsubscribeToken(testSecret, "here@example.com")) {
			t.Error("List-Unsubscribe carries a token that is not this recipient's")
		}
		if got := strings.Join(headers["List-Unsubscribe-Post"], " "); got != "List-Unsubscribe=One-Click" {
			t.Errorf("List-Unsubscribe-Post = %q, want List-Unsubscribe=One-Click", got)
		}

		// And the body carries a link a person can click, since not every client renders the
		// header's button.
		detail := fetchMessage(t, apiBaseURL, id)
		wantLink := "https://whenweall.example/unsubscribe?token=" +
			mailer.UnsubscribeToken(testSecret, "here@example.com")
		if !strings.Contains(detail.HTML, wantLink) {
			t.Error("HTML body has no unsubscribe link for this recipient")
		}
		if !strings.Contains(detail.Text, wantLink) {
			t.Error("text body has no unsubscribe link for this recipient")
		}
	})

	// A verification or reset mail with an unsubscribe control would be inviting someone to
	// opt out of the one message they need.
	t.Run("transactional mail carries no unsubscribe header or link", func(t *testing.T) {
		if err := m.Send(ctx, verifyMsg("plain@example.com")); err != nil {
			t.Fatalf("Send: %v", err)
		}
		id := onlyMessageTo(t, apiBaseURL, "plain@example.com")

		headers := fetchHeaders(t, apiBaseURL, id)
		if got := headers["List-Unsubscribe"]; len(got) != 0 {
			t.Errorf("List-Unsubscribe = %v on transactional mail, want none", got)
		}
		detail := fetchMessage(t, apiBaseURL, id)
		if strings.Contains(detail.HTML, "/unsubscribe?token=") {
			t.Error("transactional HTML body carries an unsubscribe link")
		}
	})
}

// A Mailer with no suppression list (unit tests, and any construction that forgets to pass one)
// must still deliver rather than silently swallowing every notification.
func TestSendWithoutSuppressionListStillDelivers(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpit(t)

	m := mailer.New(&config.Config{
		SMTPHost:   smtpHost,
		SMTPPort:   smtpPort,
		EmailFrom:  "whenweall <no-reply@whenweall.example>",
		AppURL:     "https://whenweall.example",
		AuthSecret: testSecret,
	}, nil)

	if err := m.Send(context.Background(), notificationMsg("nodb@example.com")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := countMessagesTo(t, apiBaseURL, "nodb@example.com"); n != 1 {
		t.Errorf("%d messages delivered, want 1", n)
	}
}

// go-mail refuses one-click headers without an https URL (RFC 8058), which is every local dev
// setup. The mail must still go out, and must still carry a usable link.
func TestSendOverPlainHTTPOriginSkipsOneClickButKeepsTheLink(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpit(t)

	m := mailer.New(&config.Config{
		SMTPHost:   smtpHost,
		SMTPPort:   smtpPort,
		EmailFrom:  "whenweall <no-reply@whenweall.example>",
		AppURL:     "http://localhost:3000",
		AuthSecret: testSecret,
	}, nil)

	if err := m.Send(context.Background(), notificationMsg("dev@example.com")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	id := onlyMessageTo(t, apiBaseURL, "dev@example.com")

	if got := fetchHeaders(t, apiBaseURL, id)["List-Unsubscribe-Post"]; len(got) != 0 {
		t.Errorf("List-Unsubscribe-Post = %v over http, want none", got)
	}
	detail := fetchMessage(t, apiBaseURL, id)
	if !strings.Contains(detail.Text, "http://localhost:3000/unsubscribe?token=") {
		t.Error("text body has no unsubscribe link")
	}
}

func messageIDsTo(t *testing.T, apiBaseURL, address string) []string {
	t.Helper()
	var ids []string
	for _, msg := range fetchMessages(t, apiBaseURL).Messages {
		for _, to := range msg.To {
			if strings.EqualFold(to.Address, address) {
				ids = append(ids, msg.ID)
			}
		}
	}
	return ids
}

func countMessagesTo(t *testing.T, apiBaseURL, address string) int {
	t.Helper()
	return len(messageIDsTo(t, apiBaseURL, address))
}

func onlyMessageTo(t *testing.T, apiBaseURL, address string) string {
	t.Helper()
	ids := messageIDsTo(t, apiBaseURL, address)
	if len(ids) != 1 {
		t.Fatalf("%d messages to %s, want exactly 1", len(ids), address)
	}
	return ids[0]
}

func fetchHeaders(t *testing.T, apiBaseURL, id string) map[string][]string {
	t.Helper()
	resp, err := http.Get(apiBaseURL + "/api/v1/message/" + id + "/headers")
	if err != nil {
		t.Fatalf("GET headers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read headers: %v", err)
	}
	var out map[string][]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal headers %s: %v", body, err)
	}
	return out
}
