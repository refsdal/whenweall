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
//
// count is `any`, not `int`: a queued job's payload arrives via a JSON round-trip
// (job.Payload -> json.Unmarshal(..., &msg) -> Message.Data map[string]any), and
// encoding/json decodes every JSON number into a Go float64 when the destination is an
// interface{} (as map[string]any values are) rather than a concrete int. A template func
// declared to take `int` panics with "wrong type for value; expected int; got float64" the
// moment it's called with that decoded data — which is every real send, only unit tests that
// build DigestLine{Count: N} in Go and hand it to Render directly happened to avoid it. See
// toInt.
func digestLineLabel(locale, event string, count any) string {
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
	return translate(locale, key, "count", toInt(count))
}

// toInt coerces a template value to int, tolerating every shape a DigestLine.Count can arrive
// in: a plain int (data built directly in Go, e.g. in tests), or a float64/int64/json.Number
// (the same value after a JSON round-trip through the job queue). Anything else — including a
// missing field the caller forgot to set, which text/template represents as a nil interface —
// coerces to 0 rather than panicking; a missing required key is a caller bug the required-keys
// contract catches elsewhere, not something this helper needs to enforce.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

// toStringSlice coerces a template value to []string, tolerating both shapes DigestLine.Names/
// claim_confirmation's Slots can arrive in: a plain []string (data built directly in Go), or
// []any of strings (the same value after a JSON round-trip, since encoding/json decodes a JSON
// array into []any when the destination is an interface{}). Anything else — including nil/a
// missing key — coerces to nil, matching {{if .Names}}'s existing "no names" behaviour.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			} else {
				out = append(out, fmt.Sprint(item))
			}
		}
		return out
	default:
		return nil
	}
}

// joinStrings is the "join" template func: strings.Join, but taking `any` instead of []string
// for the same reason digestLineLabel takes `any` for count — see toStringSlice.
func joinStrings(v any, sep string) string {
	return strings.Join(toStringSlice(v), sep)
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
