package mailer

import (
	"fmt"
	"strings"
)

// replacePlaceholder is a small helper kept separate from translate's loop so the fmt.Sprint
// formatting rule for interpolated values lives in one place.
func replacePlaceholder(msg, name string, value any) string {
	return strings.ReplaceAll(msg, "{"+name+"}", fmt.Sprint(value))
}

// notificationKeys maps the notification.tsx event union to its subject/body message keys, and
// whether the body interpolates {title} or {detail} — mirroring notificationSubject/
// notificationBody in emails/Notification.tsx exactly (poll.closed is deliberately absent there,
// too: it has its own dedicated "closed" template).
var notificationKeys = map[string]struct {
	subjectKey string
	bodyKey    string
	bodyArg    string // "title" or "detail" — which data field the body key interpolates
}{
	"deadline.approaching": {"email_notification_deadline_subject", "email_notification_deadline_body", "title"},
	"poll.finalized":       {"email_notification_finalized_subject", "email_notification_finalized_body", "title"},
	"booking.created":      {"email_notification_booking_created_subject", "email_notification_booking_created_body", "detail"},
	"booking.cancelled":    {"email_notification_booking_cancelled_subject", "email_notification_booking_cancelled_body", "detail"},
	"booking.rescheduled":  {"email_notification_booking_rescheduled_subject", "email_notification_booking_rescheduled_body", "detail"},
}

// notifSubject renders the subject line for the generic "notification" template, keyed by event
// (see NotificationTemplateEvent in emails/Notification.tsx).
func notifSubject(locale, event, title string) string {
	k, ok := notificationKeys[event]
	if !ok {
		return ""
	}
	return translate(locale, k.subjectKey, "title", title)
}

// notifBody renders the body line for the generic "notification" template. deadline.approaching
// and poll.finalized interpolate {title}; the booking.* events interpolate {detail} instead.
func notifBody(locale, event, title, detail string) string {
	k, ok := notificationKeys[event]
	if !ok {
		return ""
	}
	if k.bodyArg == "detail" {
		return translate(locale, k.bodyKey, "detail", detail)
	}
	return translate(locale, k.bodyKey, "title", title)
}

// digestLineLabel renders one summarised row of the "digest" template — "3 new responses" —
// keyed by DigestLine.Event (see lineLabel in emails/Digest.tsx).
func digestLineLabel(locale, event string, count int) string {
	key := map[string]string{
		"response.created":   "email_digest_line_response_created",
		"response.updated":   "email_digest_line_response_updated",
		"response.withdrawn": "email_digest_line_response_withdrawn",
		"comment.created":    "email_digest_line_comment_created",
		"signup.full":        "email_digest_line_signup_full",
	}[event]
	if key == "" {
		return ""
	}
	return translate(locale, key, "count", count)
}

// bookingCancelledBody picks the right first-person/third-person wording for
// "booking_cancelled" depending on who cancelled relative to the recipient (see the ternary in
// emails/BookingCancelled.tsx). cancelledBy is one of "you", "organiser", "visitor".
func bookingCancelledBody(locale, cancelledBy, name, visitor, when string) string {
	switch cancelledBy {
	case "you":
		return translate(locale, "email_booking_cancelled_body_you", "name", name, "when", when)
	case "organiser":
		return translate(locale, "email_booking_cancelled_body_organiser", "name", name, "when", when)
	default: // "visitor"
		return translate(locale, "email_booking_cancelled_body_visitor", "name", name, "visitor", visitor, "when", when)
	}
}
