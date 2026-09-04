package mailer_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestParseFromAddress(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantName  string
		wantEmail string
	}{
		{
			name:      "name and address pair",
			in:        "whenweall <no-reply@whenweall.com>",
			wantName:  "whenweall",
			wantEmail: "no-reply@whenweall.com",
		},
		{
			name:      "quoted name with surrounding whitespace",
			in:        `  "whenweall"  <no-reply@whenweall.com>  `,
			wantName:  "whenweall",
			wantEmail: "no-reply@whenweall.com",
		},
		{
			name:      "bare address unchanged",
			in:        "no-reply@whenweall.com",
			wantName:  "",
			wantEmail: "no-reply@whenweall.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotEmail := mailer.ParseFromAddress(tc.in)
			if gotName != tc.wantName || gotEmail != tc.wantEmail {
				t.Errorf("ParseFromAddress(%q) = (%q, %q), want (%q, %q)",
					tc.in, gotName, gotEmail, tc.wantName, tc.wantEmail)
			}
		})
	}
}

// TestEnqueueClaimRoundTrip verifies Enqueue schedules kind "mail:send" with the Message as its
// payload, and that a claimed job's payload unmarshals back to an equal Message — the job queue
// is only useful as a transport if what comes out is what went in.
func TestEnqueueClaimRoundTrip(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	msg := mailer.Message{
		To:       "ada@example.com",
		ToName:   "Ada",
		Template: "verify_email",
		Data: map[string]any{
			"Name":  "Ada",
			"Token": "abc123",
		},
	}

	if err := mailer.Enqueue(ctx, d, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	job := claimed[0]
	if job.Kind != "mail:send" {
		t.Errorf("Kind = %q, want %q", job.Kind, "mail:send")
	}
	if job.MaxAttempts != 10 {
		t.Errorf("MaxAttempts = %d, want 10", job.MaxAttempts)
	}
	if job.RunAt.After(time.Now().Add(time.Second)) {
		t.Errorf("RunAt = %v, want approximately now", job.RunAt)
	}

	var got mailer.Message
	if err := json.Unmarshal(job.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.To != msg.To || got.ToName != msg.ToName || got.Template != msg.Template {
		t.Errorf("got = %+v, want %+v", got, msg)
	}
	if got.Data["Name"] != "Ada" || got.Data["Token"] != "abc123" {
		t.Errorf("Data = %+v, want %+v", got.Data, msg.Data)
	}
}

// rejectedRecipient is the address a rejectingSMTPServer's RCPT TO handler always rejects with a
// 550 response that echoes the address back — mirroring how a real SMTP server's rejection text
// usually does — so the test can catch a leak coming from anywhere in the error, not just the
// SendError.rcpt list.
const rejectedRecipient = "ada@example.com"

// TestSendRedactsRecipientAddressFromError drives (*Mailer).Send against a fake SMTP server that
// rejects RCPT TO, producing a genuine *gomail.SendError (Reason: ErrSMTPRcptTo) with the
// recipient address recorded on it. It asserts the error Send actually returns carries the
// failure reason but never the recipient address — the Go equivalent of the TS predecessor's
// "never logs the recipient address" guarantee (src/server/mailer/__tests__/mailer.test.ts).
func TestSendRedactsRecipientAddressFromError(t *testing.T) {
	host, port := startRejectingSMTPServer(t)

	cfg := &config.Config{
		SMTPHost:  host,
		SMTPPort:  port,
		EmailFrom: "whenweall <no-reply@whenweall.example>",
		AppURL:    "https://whenweall.example",
	}
	m := mailer.New(cfg, nil)

	msg := mailer.Message{
		To:       rejectedRecipient,
		Template: "verify_email",
		Data: map[string]any{
			"Name": "Ada",
			"URL":  "https://whenweall.example/verify/abc123",
		},
	}

	err := m.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send returned nil, want an error from the rejected RCPT TO")
	}

	got := err.Error()
	if strings.Contains(got, rejectedRecipient) {
		t.Errorf("error %q contains the recipient address %q, want it redacted", got, rejectedRecipient)
	}
	if !strings.Contains(strings.ToLower(got), "rcpt") {
		t.Errorf("error %q does not convey the failure reason (expected something mentioning RCPT)", got)
	}
}

// TestSendRedactsUnparseableRecipientAddress covers the other place a recipient address could
// leak: an unparseable To that go-mail's gm.To/AddToFormat rejects locally, before any SMTP
// round trip. go-mail's own error text there is `failed to parse mail address %q: ...` — the bad
// address verbatim — so Send must swap in a fixed, address-free error rather than wrapping it.
func TestSendRedactsUnparseableRecipientAddress(t *testing.T) {
	const badAddress = "not an address\r\n<x@y>"

	cfg := &config.Config{
		SMTPHost:  "127.0.0.1",
		SMTPPort:  1, // unused: the address is rejected before any network dial is attempted.
		EmailFrom: "whenweall <no-reply@whenweall.example>",
		AppURL:    "https://whenweall.example",
	}
	m := mailer.New(cfg, nil)

	msg := mailer.Message{
		To:       badAddress,
		Template: "verify_email",
		Data: map[string]any{
			"Name": "Ada",
			"URL":  "https://whenweall.example/verify/abc123",
		},
	}

	err := m.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send returned nil, want an error from the unparseable recipient address")
	}

	got := err.Error()
	if strings.Contains(got, "x@y") || strings.Contains(got, "not an address") {
		t.Errorf("error %q contains the recipient address text, want it redacted", got)
	}
}

// startRejectingSMTPServer starts a minimal single-connection fake SMTP server on 127.0.0.1: it
// completes EHLO/MAIL FROM normally (no STARTTLS/AUTH extensions offered, so go-mail's
// TLSOpportunistic policy and no-auth default both proceed without negotiating either), then
// rejects RCPT TO for rejectedRecipient with a 550 response, and handles the RSET/QUIT that
// follow. Returns the host and port to dial.
func startRejectingSMTPServer(t *testing.T) (host string, port int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed by t.Cleanup
		}
		defer func() { _ = conn.Close() }()
		handleRejectingSMTPConn(conn)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func handleRejectingSMTPConn(conn net.Conn) {
	r := bufio.NewReader(conn)
	writeLine := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	writeLine("220 fake.test ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			// No extensions advertised: STARTTLS and AUTH are both left off the table, so the
			// client (TLSOpportunistic, no credentials configured) proceeds without either.
			writeLine("250 fake.test")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			writeLine("250 2.1.0 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			writeLine(fmt.Sprintf("550 5.1.1 <%s>: Recipient address rejected", rejectedRecipient))
		case strings.HasPrefix(upper, "RSET"):
			writeLine("250 2.0.0 OK")
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 2.0.0 Bye")
			return
		default:
			writeLine("250 2.0.0 OK")
		}
	}
}
