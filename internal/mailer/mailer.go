// Package mailer (this file) is the SMTP transport and job-queue integration on top of Render:
// Send renders a Message and delivers it over SMTP via go-mail; Enqueue is the only API request
// handlers may use to actually send mail, since it goes through scheduled_jobs (retries, a
// dead-letter after MaxAttempts, and no send happening inline on the request's goroutine).
package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
)

// mailSendKind is the scheduled_jobs kind carrying a Message payload — the only kind this
// package registers a handler for.
const mailSendKind = "mail:send"

// mailMaxAttempts bounds retries for a queued send: an SMTP relay hiccup or a momentary DNS
// failure is worth retrying, but a message that still can't go out after 10 attempts is more
// likely a bad address or a broken relay than something one more retry will fix.
const mailMaxAttempts = 10

// Mailer renders Message values (via Render) and delivers them over SMTP with go-mail.
//
// NewClient is deferred to Send rather than done once in New, since New has no error return —
// building the transport here would leave a config mistake (an empty host, say) silently
// unreported until the first send fails anyway, so it may as well fail there.
type Mailer struct {
	host       string
	clientOpts []gomail.Option
	fromName   string
	fromEmail  string
	appURL     string
}

// New builds a Mailer from validated application config: the SMTP host/port/credentials/TLS mode,
// and EMAIL_FROM (parsed via ParseFromAddress into the sender name/address go-mail sends with).
func New(cfg *config.Config) *Mailer {
	fromName, fromEmail := ParseFromAddress(cfg.EmailFrom)

	opts := []gomail.Option{gomail.WithPort(cfg.SMTPPort)}
	if cfg.SMTPSecure {
		// Implicit TLS (SMTPS) with no unencrypted fallback: SMTPSecure is an operator's explicit
		// choice of transport, not something to silently downgrade if the handshake fails.
		opts = append(opts, gomail.WithSSLPort(false))
	} else {
		// Opportunistic STARTTLS: upgrade to TLS when the server offers it, but still send when it
		// doesn't — true for Mailpit in dev/test and for some local/private-network relays.
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	}
	if cfg.SMTPUser != "" && cfg.SMTPPassword != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(cfg.SMTPUser),
			gomail.WithPassword(cfg.SMTPPassword),
		)
	}
	// else: leave auth unset. go-mail's Client defaults to SMTPAuthNoAuth, which is exactly right
	// for Mailpit and other unauthenticated local relays.

	return &Mailer{
		host:       cfg.SMTPHost,
		clientOpts: opts,
		fromName:   fromName,
		fromEmail:  fromEmail,
		appURL:     cfg.AppURL,
	}
}

// AppURL returns the application base URL this Mailer was configured with — the same value Send
// injects into every rendered template's .AppURL. Entity-mail job handlers (internal/polls) need
// it themselves, to build the body links (PollURL, URL, ...) that are template-specific data
// rather than part of the shared layout.
func (m *Mailer) AppURL() string { return m.appURL }

// Send renders msg (via Render) and delivers it immediately over SMTP. Only the "mail:send" job
// handler and tests call this directly — request handlers must go through Enqueue instead, so a
// slow or failing SMTP relay never blocks the request that triggered the mail.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	// Render takes AppURL from data rather than injecting it itself, so it's merged in here — a
	// copy, not a mutation of msg.Data, since that map may be shared with the caller.
	data := make(map[string]any, len(msg.Data)+1)
	for k, v := range msg.Data {
		data[k] = v
	}
	data["AppURL"] = m.appURL

	rendered, err := Render(msg.Template, data)
	if err != nil {
		return fmt.Errorf("mailer: render %q: %w", msg.Template, err)
	}

	gm := gomail.NewMsg()

	if m.fromName != "" {
		err = gm.FromFormat(m.fromName, m.fromEmail)
	} else {
		err = gm.From(m.fromEmail)
	}
	if err != nil {
		return fmt.Errorf("mailer: from address: %w", err)
	}

	if msg.ToName != "" {
		err = gm.AddToFormat(msg.ToName, msg.To)
	} else {
		err = gm.To(msg.To)
	}
	if err != nil {
		// go-mail's own error here is `failed to parse mail address %q: %w` — msg.To verbatim,
		// same privacy problem redactSendErr exists to solve for a rejected-RCPT failure, just
		// hit earlier (address syntax rejected locally, before any SMTP round trip). No %w: that
		// would leave the address recoverable via errors.Unwrap/Is despite the message text
		// being clean.
		return errors.New("mailer: invalid recipient address")
	}

	gm.Subject(rendered.Subject)
	gm.SetBodyString(gomail.TypeTextPlain, rendered.Text)
	gm.AddAlternativeString(gomail.TypeTextHTML, rendered.HTML)

	for _, a := range msg.Attachments {
		reader := bytes.NewReader(a.Content)
		if err := gm.AttachReader(a.Filename, reader, gomail.WithFileContentType(gomail.ContentType(a.ContentType))); err != nil {
			return fmt.Errorf("mailer: attach %q: %w", a.Filename, err)
		}
	}

	client, err := gomail.NewClient(m.host, m.clientOpts...)
	if err != nil {
		return fmt.Errorf("mailer: smtp client: %w", err)
	}

	if err := client.DialAndSendWithContext(ctx, gm); err != nil {
		return redactSendErr(err)
	}
	return nil
}

// redactSendErr turns an SMTP delivery failure into an error safe to log and to persist in
// scheduled_jobs.last_error. go-mail's *gomail.SendError.Error() includes the affected
// recipient address(es) (e.g. "sending SMTP RCPT TO command: 550 5.1.1 ada@example.com:
// Recipient address rejected, affected recipient(s): ada@example.com") — mail privacy requires
// this package never let a recipient address reach either destination, matching the guarantee
// src/server/mailer/mailer.ts's sendMail made (logs subject/code, never the recipient).
//
// The redacted message keeps only the failure reason, the server's error code, and whether it
// looks temporary (worth retrying) — never the rcpt list or the raw SMTP response text (which
// can itself echo the address back, e.g. in a "Recipient address rejected" line). It deliberately
// returns a flat errors.New rather than wrapping the original with %w: wrapping would leave the
// *SendError reachable via errors.As/errors.Unwrap, letting a caller pull the address back out of
// it despite the message text being clean.
func redactSendErr(err error) error {
	var sendErr *gomail.SendError
	if errors.As(err, &sendErr) {
		reason := fmt.Sprintf("mailer: send failed: %s", sendErr.Reason.String())
		if code := sendErr.ErrorCode(); code != 0 {
			reason = fmt.Sprintf("%s (smtp code %d)", reason, code)
		}
		if sendErr.IsTemp() {
			reason += " (temporary, will retry)"
		}
		return errors.New(reason)
	}
	// Not a SendError — a dial/connection failure, say — which doesn't carry recipient
	// addresses, so the original is safe to wrap as-is.
	return fmt.Errorf("mailer: send: %w", err)
}

// Enqueue schedules kind "mail:send" with msg as its JSON payload (MaxAttempts 10, run
// immediately). This is the only send API request handlers may use — Send itself is reserved for
// the job handler (RegisterHandler) and tests, so a request never blocks on an SMTP round trip.
//
// This path — a fully-rendered Message queued as-is — is for auth/org mail only: verify_email,
// reset_password, magic_link, org_invite, and the like, whose tokens are minted once, live only
// inside the request that minted them, and cannot be re-derived later (see ParseFromAddress's
// callers and the token packages upstream of them). There is nothing to go stale, so queuing the
// finished Message is safe.
//
// Entity mail — poll/booking notifications, landing in plans 4 and 5 (finalized, closed, digest,
// notification, claim_confirmation, booking_*) — must NOT enqueue through this function with a
// pre-rendered Message. It must enqueue ids-only, under its own job kind, and re-read the entity
// at send time instead. This mirrors src/server/mailer/queue.ts's MailJob (ids only, never a
// rendered message or address) and the reasoning behind it: a retry — or here, any queued send
// that sits in scheduled_jobs for a while before a worker claims it — must reflect the world as
// it is when it actually sends, not as it was when it was queued. A booking cancelled in the
// meantime must not have its confirmation sent anyway just because the job was already sitting in
// the queue with the old (now stale) data baked into its payload.
func Enqueue(ctx context.Context, tx db.DBTX, msg Message) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:        mailSendKind,
		RunAt:       time.Now(),
		Payload:     msg,
		MaxAttempts: mailMaxAttempts,
	})
}

// RegisterHandler wires kind "mail:send" into w: each claimed job's payload is unmarshalled back
// into a Message and passed to m.Send.
func (m *Mailer) RegisterHandler(w *jobs.Worker) {
	w.Register(mailSendKind, func(ctx context.Context, job jobs.Job) error {
		var msg Message
		if err := json.Unmarshal(job.Payload, &msg); err != nil {
			// Deliberately no %s of job.Payload here — that's the message body/data, and this
			// error ends up in the worker's structured log (see Worker.process).
			return fmt.Errorf("mailer: unmarshal mail:send payload: %w", err)
		}
		return m.Send(ctx, msg)
	})
}

// fromAddressRe ports parseFromAddress from src/server/mailer/mailer.ts: an optional display name
// (bare or double-quoted), then the address in angle brackets. A value with no "<addr>" suffix
// (a bare address) doesn't match, so ParseFromAddress falls back to returning it unchanged.
var fromAddressRe = regexp.MustCompile(`^\s*(.*?)\s*<([^<>\s]+)>\s*$`)

// quotedNameRe strips one pair of surrounding double quotes from a display name, e.g. `"whenweall"`
// -> `whenweall`. Mirrors the TS `.replace(/^"(.*)"$/, '$1')`.
var quotedNameRe = regexp.MustCompile(`^"(.*)"$`)

// ParseFromAddress parses an EMAIL_FROM-style value ("whenweall <no-reply@whenweall.com>") into a
// display name and an address. A bare address (no "<...>") returns an empty name and the address
// unchanged; a name that isn't present returns an empty name too.
func ParseFromAddress(v string) (name, email string) {
	m := fromAddressRe.FindStringSubmatch(v)
	if m == nil {
		return "", v
	}
	name = strings.TrimSpace(quotedNameRe.ReplaceAllString(m[1], "$1"))
	return name, m[2]
}
