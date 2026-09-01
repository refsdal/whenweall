package mailer

import (
	"strings"
	"testing"
)

// footerMarker is the id on the shared footer paragraph layout.html emits — the cheapest
// possible proof that a rendered template actually went through the layout. (An HTML comment
// would be simpler, but html/template strips comments from its output.)
const footerMarker = `id="wwa-footer"`

func TestRenderAllTemplates(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
	}{
		{
			name: "verify_email",
			data: map[string]any{
				"AppURL": "https://app.example",
				"Name":   "Ada",
				"URL":    "https://app.example/verify/abc123",
			},
		},
		{
			name: "reset_password",
			data: map[string]any{
				"AppURL": "https://app.example",
				"Name":   "Ada",
				"URL":    "https://app.example/reset/abc123",
			},
		},
		{
			name: "magic_link",
			data: map[string]any{
				"AppURL": "https://app.example",
				"Name":   "Ada",
				"URL":    "https://app.example/signin/abc123",
			},
		},
		{
			name: "org_invite",
			data: map[string]any{
				"AppURL":      "https://app.example",
				"OrgName":     "Acme",
				"InviterName": "Bea",
				"URL":         "https://app.example/invite/abc123",
			},
		},
		{
			name: "finalized",
			data: map[string]any{
				"AppURL":        "https://app.example",
				"PollTitle":     "Team offsite",
				"PollURL":       "https://app.example/p/abc123",
				"OptionLabel":   "Tue 10:00",
				"RecipientName": "Ada",
			},
		},
		{
			name: "closed",
			data: map[string]any{
				"AppURL":    "https://app.example",
				"PollTitle": "Team offsite",
				"PollURL":   "https://app.example/p/abc123",
			},
		},
		{
			name: "digest",
			data: map[string]any{
				"AppURL":    "https://app.example",
				"PollTitle": "Team offsite",
				"PollURL":   "https://app.example/p/abc123",
				"Lines": []DigestLine{
					{Event: "response.created", Names: []string{"Ada", "Bob"}, Count: 2},
					{Event: "signup.full", Count: 0},
				},
			},
		},
		{
			name: "notification",
			data: map[string]any{
				"AppURL": "https://app.example",
				"Event":  "booking.created",
				"Title":  "Office hours",
				"URL":    "https://app.example/b/abc123",
				"Detail": "Tomorrow at 10:00",
			},
		},
		{
			name: "claim_confirmation",
			data: map[string]any{
				"AppURL":    "https://app.example",
				"Name":      "Ada",
				"PollTitle": "Bake sale sign-up",
				"PollURL":   "https://app.example/p/abc123",
				"Slots":     []string{"Bring cupcakes", "Set up 9am"},
			},
		},
		{
			name: "booking_confirmed",
			data: map[string]any{
				"AppURL":        "https://app.example",
				"VisitorName":   "Ada",
				"PageTitle":     "30 min chat",
				"OrganiserName": "Bea",
				"When":          "Tue 10:00",
				"Location":      "Zoom",
				"ManageURL":     "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_cancelled",
			data: map[string]any{
				"AppURL":        "https://app.example",
				"RecipientName": "Ada",
				"PageTitle":     "30 min chat",
				"When":          "Tue 10:00",
				"CancelledBy":   "organiser",
				"ViewURL":       "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_rescheduled",
			data: map[string]any{
				"AppURL":        "https://app.example",
				"VisitorName":   "Ada",
				"PageTitle":     "30 min chat",
				"OrganiserName": "Bea",
				"PreviousWhen":  "Mon 09:00",
				"When":          "Tue 10:00",
				"Location":      "Zoom",
				"ManageURL":     "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_rescheduled_organiser",
			data: map[string]any{
				"AppURL":       "https://app.example",
				"PageTitle":    "30 min chat",
				"VisitorName":  "Ada",
				"PreviousWhen": "Mon 09:00",
				"When":         "Tue 10:00",
				"Location":     "Zoom",
				"ViewURL":      "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_organiser_notice",
			data: map[string]any{
				"AppURL":       "https://app.example",
				"PageTitle":    "30 min chat",
				"VisitorName":  "Ada",
				"VisitorEmail": "ada@example.com",
				"VisitorNote":  "Looking forward to it",
				"When":         "Tue 10:00",
				"Location":     "Zoom",
				"ViewURL":      "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_reminder",
			data: map[string]any{
				"AppURL":        "https://app.example",
				"RecipientName": "Ada",
				"PageTitle":     "30 min chat",
				"When":          "Tue 10:00",
				"Location":      "Zoom",
				"ViewURL":       "https://app.example/bk/abc123",
			},
		},
		{
			name: "booking_sync_failed",
			data: map[string]any{
				"AppURL":    "https://app.example",
				"PageTitle": "30 min chat",
			},
		},
	}

	if len(cases) != len(names) {
		t.Fatalf("test table has %d cases but %d templates are registered — every template must be covered", len(cases), len(names))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := Render(tc.name, tc.data)
			if err != nil {
				t.Fatalf("Render(%q) returned error: %v", tc.name, err)
			}
			if rendered.Subject == "" {
				t.Errorf("Render(%q).Subject is empty", tc.name)
			}
			if rendered.Text == "" {
				t.Errorf("Render(%q).Text is empty", tc.name)
			}
			if !strings.Contains(rendered.HTML, footerMarker) {
				t.Errorf("Render(%q).HTML missing footer marker %q", tc.name, footerMarker)
			}
		})
	}

	// Every locale in the catalog must round-trip too, not just the default.
	for locale := range catalog {
		data := map[string]any{
			"AppURL": "https://app.example",
			"Name":   "Ada",
			"URL":    "https://app.example/verify/abc123",
			"Locale": locale,
		}
		rendered, err := Render("verify_email", data)
		if err != nil {
			t.Fatalf("Render(verify_email, locale=%s) error: %v", locale, err)
		}
		if rendered.Subject == "" || rendered.Text == "" {
			t.Errorf("Render(verify_email, locale=%s) produced empty output", locale)
		}
	}
}

func TestRenderEscapesHTML(t *testing.T) {
	data := map[string]any{
		"AppURL": "https://app.example",
		"Name":   `<script>alert(1)</script>`,
		"URL":    "https://app.example/verify/abc123",
	}

	rendered, err := Render("verify_email", data)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if strings.Contains(rendered.HTML, "<script>") {
		t.Errorf("HTML contains an unescaped <script> tag:\n%s", rendered.HTML)
	}
	if !strings.Contains(rendered.HTML, "&lt;script&gt;") {
		t.Errorf("HTML does not contain the escaped script tag &lt;script&gt;:\n%s", rendered.HTML)
	}
}

// TestRenderSubjectIsNotHTMLEscaped guards against subjects being rendered through the
// html-escaping template set. `&`, `'`, and `<` are ordinary characters in real org names and
// poll titles ("Smith & Co", "David's Wedding") — a subject line must carry them verbatim, since
// it is a mail header, not markup embedded in a document.
func TestRenderSubjectIsNotHTMLEscaped(t *testing.T) {
	data := map[string]any{
		"AppURL":      "https://app.example",
		"OrgName":     `Smith & Co <ok>`,
		"InviterName": `O'Brien`,
		"URL":         "https://app.example/invite/abc123",
	}

	rendered, err := Render("org_invite", data)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	const wantSubject = `O'Brien invited you to Smith & Co <ok> on whenweall`
	if rendered.Subject != wantSubject {
		t.Errorf("Subject = %q, want %q (subjects must not be HTML-escaped)", rendered.Subject, wantSubject)
	}

	// Text is also non-escaping (text/template), so the same fields should come back verbatim
	// wherever the body interpolates them.
	if !strings.Contains(rendered.Text, `Smith & Co <ok>`) {
		t.Errorf("Text does not contain the org name verbatim:\n%s", rendered.Text)
	}

	// The HTML body uses the same fields and must still be escaped — this fix must not weaken
	// that.
	if !strings.Contains(rendered.HTML, "&amp;") {
		t.Errorf("HTML body does not escape '&':\n%s", rendered.HTML)
	}
	if !strings.Contains(rendered.HTML, "&#39;Brien") {
		t.Errorf("HTML body does not escape the apostrophe in O'Brien:\n%s", rendered.HTML)
	}
	if !strings.Contains(rendered.HTML, "&lt;ok&gt;") {
		t.Errorf("HTML body does not escape '<ok>':\n%s", rendered.HTML)
	}
}

func TestRenderUnknownTemplateErrors(t *testing.T) {
	_, err := Render("does_not_exist", map[string]any{})
	if err == nil {
		t.Fatal("expected an error for an unknown template name, got nil")
	}
}
