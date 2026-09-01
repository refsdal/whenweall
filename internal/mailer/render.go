// Package mailer renders transactional email — subject, HTML, and a plain-text alternative —
// from the templates embedded in templates/. It is a straight port of the React Email templates
// under emails/*.tsx: same visual shell (_Layout.tsx becomes layout.html, an inline-styled table
// so the markup survives an email client's stripped-down CSS support), same copy (ported from
// messages/en.json and messages/nb.json into catalog, see messages.go), same set of templates.
package mailer

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	textTemplate "text/template"
)

//go:embed templates
var templatesFS embed.FS

// names is the fixed list of template names Render accepts, and the source of truth for what
// gets parsed at init. Keep in sync with the <name>.html/<name>.txt pairs under templates/.
var names = []string{
	"verify_email",
	"reset_password",
	"magic_link",
	"org_invite",
	"finalized",
	"closed",
	"digest",
	"notification",
	"claim_confirmation",
	"booking_confirmed",
	"booking_cancelled",
	"booking_rescheduled",
	"booking_rescheduled_organiser",
	"booking_organiser_notice",
	"booking_reminder",
	"booking_sync_failed",
}

var (
	htmlTemplates = make(map[string]*template.Template, len(names))
	textTemplates = make(map[string]*textTemplate.Template, len(names))
)

// funcMap is shared between the HTML and text template sets: both need translate (as "t") and
// the digest/notification/booking-cancelled copy-selection helpers, and text parts reuse the same
// wording as their HTML counterpart rather than a separate hand-written text catalog.
func funcMap() map[string]any {
	return map[string]any{
		"t":             translate,
		"join":          joinStrings,
		"notifSubject":  notifSubject,
		"notifBody":     notifBody,
		"digestLine":    digestLineLabel,
		"cancelledBody": bookingCancelledBody,
	}
}

func init() {
	for _, name := range names {
		html, err := template.New("layout.html").Funcs(funcMap()).
			ParseFS(templatesFS, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			// Templates are compiled into the binary via go:embed; a parse failure here means
			// the binary itself is broken, not something a caller can work around.
			panic(fmt.Sprintf("mailer: parsing templates/%s.html: %v", name, err))
		}
		htmlTemplates[name] = html

		// The text/template set also parses <name>.html, purely to pick up its "subject" define
		// block: subjects are plain text (a mail header line), and the brief keeps them in one
		// obvious place per template — the {{define "subject"}}...{{end}} in <name>.html — so
		// rather than duplicating that copy into the .txt file, Render executes "subject" through
		// this text (non-escaping) template set instead of the html one. <name>.html's "content"
		// define is parsed along for the ride here but never executed from this set — the text
		// body comes from <name>.txt's top-level content instead.
		text, err := textTemplate.New(name+".txt").Funcs(funcMap()).
			ParseFS(templatesFS, "templates/"+name+".txt", "templates/"+name+".html")
		if err != nil {
			panic(fmt.Sprintf("mailer: parsing templates/%s.txt: %v", name, err))
		}
		textTemplates[name] = text
	}
}

// DigestLine is one summarised row of the "digest" template: "3 new responses — Ada, Bob, Cleo".
// Names is empty for events where naming the actor adds nothing (e.g. a full sign-up sheet).
// Mirrors DigestLine in emails/Digest.tsx.
type DigestLine struct {
	Event string
	Names []string
	Count int
}

// Rendered is the fully rendered form of one email: a subject line, an HTML body, and a
// plain-text alternative.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// Attachment is a file attached to an outgoing message — an .ics invite, added by the booking
// plans that send through this package.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Message is one outgoing mail: which template to render, the data it needs, who it goes to, and
// any attachments. The SMTP transport (a later task) consumes this directly.
type Message struct {
	To          string
	ToName      string
	Template    string
	Data        map[string]any
	Attachments []Attachment
}

// Render executes templates/<name>.html and templates/<name>.txt inside the shared layout. data
// is template-specific (see the per-template doc in templates/<name>.html), but every template's
// layout can use .AppURL (the link on the brand header) and .Locale ("en" or "nb"; missing or
// unrecognised falls back to "en", same as the app's paraglide runtime). An unknown name returns
// an error rather than panicking, since the name usually comes from caller-controlled data (a
// queued job's row).
func Render(name string, data map[string]any) (Rendered, error) {
	html, ok := htmlTemplates[name]
	if !ok {
		return Rendered{}, fmt.Errorf("mailer: unknown template %q", name)
	}
	text := textTemplates[name]

	var subjectBuf, htmlBuf, textBuf strings.Builder

	// Subject is executed through the text (non-escaping) template set even though "subject" is
	// defined inside <name>.html — a subject line is plain text, not an HTML body, and running it
	// through html/template would HTML-escape interpolated data (e.g. "Smith & Co" -> "Smith
	// &amp; Co", "David's" -> "David&#39;s"). The HTML body below still needs (and gets) full
	// escaping, since that output really is embedded in an HTML document.
	if err := text.ExecuteTemplate(&subjectBuf, "subject", data); err != nil {
		return Rendered{}, fmt.Errorf("mailer: rendering %q subject: %w", name, err)
	}
	if err := html.ExecuteTemplate(&htmlBuf, "layout.html", data); err != nil {
		return Rendered{}, fmt.Errorf("mailer: rendering %q html: %w", name, err)
	}
	if err := text.ExecuteTemplate(&textBuf, name+".txt", data); err != nil {
		return Rendered{}, fmt.Errorf("mailer: rendering %q text: %w", name, err)
	}

	return Rendered{
		Subject: strings.TrimSpace(subjectBuf.String()),
		HTML:    htmlBuf.String(),
		Text:    strings.TrimSpace(textBuf.String()),
	}, nil
}
