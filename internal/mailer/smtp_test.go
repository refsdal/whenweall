package mailer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

// mailpitMessageSummary is the subset of Mailpit's GET /api/v1/messages response this test needs.
type mailpitMessagesResponse struct {
	Total    int                     `json:"total"`
	Messages []mailpitMessageSummary `json:"messages"`
}

type mailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

type mailpitMessageSummary struct {
	ID      string           `json:"ID"`
	To      []mailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
}

// mailpitMessageDetail is the subset of Mailpit's GET /api/v1/message/{ID} response this test
// needs: Text and HTML confirm both body alternatives arrived, Attachments confirms the .ics.
type mailpitMessageDetail struct {
	Subject     string              `json:"Subject"`
	Text        string              `json:"Text"`
	HTML        string              `json:"HTML"`
	Attachments []mailpitAttachment `json:"Attachments"`
	To          []mailpitAddress    `json:"To"`
}

type mailpitAttachment struct {
	FileName    string `json:"FileName"`
	ContentType string `json:"ContentType"`
}

// startMailpit runs axllent/mailpit via testcontainers and returns the SMTP host/port to send
// through, and the base URL of its HTTP API to assert delivery against.
func startMailpit(t *testing.T) (smtpHost string, smtpPort int, apiBaseURL string) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("8025/tcp").WithStartupTimeout(60 * time.Second),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		testdb.Unavailable(t, "mailpit testcontainer", err)
	}
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	smtpMapped, err := ctr.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatalf("smtp mapped port: %v", err)
	}
	apiMapped, err := ctr.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatalf("api mapped port: %v", err)
	}

	return host, int(smtpMapped.Num()), fmt.Sprintf("http://%s:%s", host, apiMapped.Port())
}

// TestSendDeliversToMailpit sends a real message with an HTML+text body and one .ics attachment
// through (*Mailer).Send against a live Mailpit container, then asserts delivery via Mailpit's
// HTTP API: exactly one message, correct To/Subject, and both body parts plus the attachment
// intact on the far side.
func TestSendDeliversToMailpit(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpit(t)

	cfg := &config.Config{
		SMTPHost:  smtpHost,
		SMTPPort:  smtpPort,
		EmailFrom: "whenweall <no-reply@whenweall.example>",
		AppURL:    "https://whenweall.example",
	}
	m := mailer.New(cfg, nil)

	msg := mailer.Message{
		To:       "ada@example.com",
		ToName:   "Ada Lovelace",
		Template: "verify_email",
		Data: map[string]any{
			"Name": "Ada Lovelace",
			"URL":  "https://whenweall.example/verify/abc123",
		},
		Attachments: []mailer.Attachment{
			{
				Filename:    "invite.ics",
				ContentType: "text/calendar",
				Content:     []byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR\n"),
			},
		},
	}

	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	list := fetchMessages(t, apiBaseURL)
	if list.Total != 1 {
		t.Fatalf("Total = %d, want 1", list.Total)
	}
	summary := list.Messages[0]
	if len(summary.To) != 1 || summary.To[0].Address != msg.To {
		t.Errorf("To = %+v, want address %q", summary.To, msg.To)
	}
	if summary.To[0].Name != msg.ToName {
		t.Errorf("To name = %q, want %q", summary.To[0].Name, msg.ToName)
	}

	rendered, err := mailer.Render(msg.Template, map[string]any{
		"Name":   "Ada Lovelace",
		"URL":    "https://whenweall.example/verify/abc123",
		"AppURL": cfg.AppURL,
	})
	if err != nil {
		t.Fatalf("Render (for expected subject): %v", err)
	}
	if summary.Subject != rendered.Subject {
		t.Errorf("Subject = %q, want %q", summary.Subject, rendered.Subject)
	}

	detail := fetchMessage(t, apiBaseURL, summary.ID)
	if detail.Text == "" {
		t.Error("Text part is empty, want the rendered plain-text alternative")
	}
	if detail.HTML == "" {
		t.Error("HTML part is empty, want the rendered HTML alternative")
	}
	if len(detail.Attachments) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.FileName != "invite.ics" {
		t.Errorf("attachment FileName = %q, want %q", att.FileName, "invite.ics")
	}
	if att.ContentType != "text/calendar" {
		t.Errorf("attachment ContentType = %q, want %q", att.ContentType, "text/calendar")
	}
}

func fetchMessages(t *testing.T, apiBaseURL string) mailpitMessagesResponse {
	t.Helper()
	resp, err := http.Get(apiBaseURL + "/api/v1/messages")
	if err != nil {
		t.Fatalf("GET /api/v1/messages: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/v1/messages: %v", err)
	}
	var out mailpitMessagesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal /api/v1/messages: %v", err)
	}
	return out
}

func fetchMessage(t *testing.T, apiBaseURL, id string) mailpitMessageDetail {
	t.Helper()
	resp, err := http.Get(apiBaseURL + "/api/v1/message/" + id)
	if err != nil {
		t.Fatalf("GET /api/v1/message/%s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/v1/message/%s: %v", id, err)
	}
	var out mailpitMessageDetail
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal /api/v1/message/%s: %v", id, err)
	}
	return out
}
