package mailer

// catalog holds the transactional-email copy for every supported locale, keyed the same way as
// the app's paraglide message catalog (messages/en.json, messages/nb.json) so the wording here
// stays a straight port rather than a parallel translation effort. Placeholders use the same
// `{name}` syntax as paraglide; translate() below does the substitution.
//
// email_magic_link_* is the one set with no paraglide source — magic_link is a new template
// (brief: "copy modelled on VerifyEmail: sign in with this link"), so its copy is authored here
// directly, in both locales, to match the surrounding templates rather than being English-only.
var catalog = map[string]map[string]string{
	"en": {
		"email_verify_subject": "Verify your email address",
		"email_verify_heading": "Verify your email",
		"email_verify_body":    "Hi {name}, please confirm your email address to finish setting up your account.",
		"email_verify_cta":     "Verify email",

		"email_reset_subject": "Reset your password",
		"email_reset_heading": "Reset your password",
		"email_reset_body":    "Hi {name}, we received a request to reset your password. Click the button below to choose a new one.",
		"email_reset_cta":     "Reset password",

		"email_magic_link_subject": "Sign in to whenweall",
		"email_magic_link_heading": "Sign in",
		"email_magic_link_body":    "Hi {name}, use the link below to sign in. It expires shortly.",
		"email_magic_link_cta":     "Sign in",

		"email_org_invite_subject": "{inviter} invited you to {org} on whenweall",
		"email_org_invite_heading": "Join {org}",
		"email_org_invite_body":    "{inviter} has invited you to the {org} organization on whenweall. Accept the invitation to schedule together.",
		"email_org_invite_cta":     "Accept invitation",

		"email_digest_subject":                 "New responses on {title}",
		"email_digest_heading":                 "New responses",
		"email_digest_cta":                     "View poll",
		"email_digest_line_response_created":   "{count} new responses",
		"email_digest_line_response_updated":   "{count} changed responses",
		"email_digest_line_response_withdrawn": "{count} withdrawn responses",
		"email_digest_line_comment_created":    "{count} new comments",
		"email_digest_line_signup_full":        "All slots are now claimed",

		"email_notification_cta":                         "Open",
		"email_notification_deadline_subject":            "{title} closes soon",
		"email_notification_deadline_body":               "The deadline for {title} is less than 24 hours away.",
		"email_notification_finalized_subject":           "{title} was finalized",
		"email_notification_finalized_body":              "A time has been picked for {title}.",
		"email_notification_booking_created_subject":     "New booking on {title}",
		"email_notification_booking_created_body":        "A new booking was made: {detail}",
		"email_notification_booking_cancelled_subject":   "Booking cancelled on {title}",
		"email_notification_booking_cancelled_body":      "A booking was cancelled: {detail}",
		"email_notification_booking_rescheduled_subject": "Booking rescheduled on {title}",
		"email_notification_booking_rescheduled_body":    "A booking moved to: {detail}",

		"email_finalized_subject": "{title} is decided",
		"email_finalized_heading": "The time is set",
		"email_finalized_body":    "Hi {name}, {option} was picked as the winning time.",
		"email_finalized_cta":     "View details",

		"email_closed_subject": "Voting for {title} is closed",
		"email_closed_body":    "The poll closed without a winning time. Check the results and follow up with the group.",
		"email_closed_cta":     "View poll",

		"email_claim_subject": "Your sign-up for {title}",
		"email_claim_heading": "You're signed up",
		"email_claim_body":    "Hi {name}, here's what you signed up for.",
		"email_claim_cta":     "View sign-up sheet",

		"email_footer": "You're receiving this email because you used {name}.",

		"email_booking_confirmed_subject": "Confirmed: {title}",
		"email_booking_confirmed_heading": "Booking confirmed",
		"email_booking_confirmed_body":    "Hi {name}, your booking with {organiser} is confirmed for {when}.",
		"email_booking_location":          "Location: {location}",
		"email_booking_confirmed_cta":     "Manage booking",

		"email_booking_organiser_subject": "New booking: {title}",
		"email_booking_organiser_heading": "New booking",
		"email_booking_organiser_body":    "{name} ({email}) booked {when}.",
		"email_booking_organiser_note":    "Note: {note}",
		"email_booking_organiser_cta":     "View bookings",

		"email_booking_cancelled_subject":        "Cancelled: {title}",
		"email_booking_cancelled_heading":        "Booking cancelled",
		"email_booking_cancelled_body_you":       "Hi {name}, you cancelled the booking for {when}.",
		"email_booking_cancelled_body_organiser": "Hi {name}, the organiser cancelled your booking for {when}.",
		"email_booking_cancelled_body_visitor":   "Hi {name}, {visitor} cancelled their booking for {when}.",
		"email_booking_cancelled_cta":            "View page",

		"email_booking_reminder_subject":  "Reminder: {title}",
		"email_booking_reminder_heading":  "Upcoming booking",
		"email_booking_reminder_body":     "Hi {name}, this is a reminder about your booking for {when}.",
		"email_booking_reminder_location": "Location: {location}",
		"email_booking_reminder_cta":      "View details",

		"email_booking_rescheduled_subject": "Rescheduled: {title}",
		"email_booking_rescheduled_heading": "Booking rescheduled",
		"email_booking_rescheduled_body":    "Hi {name}, your booking with {organiser} has moved from {previousWhen} to {when}.",
		"email_booking_rescheduled_cta":     "Manage booking",

		"email_booking_rescheduled_org_subject": "Booking moved: {title}",
		"email_booking_rescheduled_org_heading": "Booking moved",
		"email_booking_rescheduled_org_body":    "{name} moved their booking from {previousWhen} to {when}.",
		"email_booking_rescheduled_org_cta":     "View bookings",

		"email_booking_sync_failed_subject": "Google Calendar sync issue: {title}",
		"email_booking_sync_failed_heading": "Calendar sync issue",
		"email_booking_sync_failed_body":    "We couldn't update your Google Calendar for a booking on {title}. Please check your calendar manually to make sure it matches your bookings.",
	},
	"nb": {
		"email_verify_subject": "Bekreft e-postadressen din",
		"email_verify_heading": "Bekreft e-posten din",
		"email_verify_body":    "Hei {name}, bekreft e-postadressen din for å fullføre opprettelsen av kontoen.",
		"email_verify_cta":     "Bekreft e-post",

		"email_reset_subject": "Tilbakestill passordet ditt",
		"email_reset_heading": "Tilbakestill passordet",
		"email_reset_body":    "Hei {name}, vi har mottatt en forespørsel om å tilbakestille passordet ditt. Klikk på knappen under for å velge et nytt.",
		"email_reset_cta":     "Tilbakestill passord",

		"email_magic_link_subject": "Logg inn på whenweall",
		"email_magic_link_heading": "Logg inn",
		"email_magic_link_body":    "Hei {name}, bruk lenken under for å logge inn. Den utløper snart.",
		"email_magic_link_cta":     "Logg inn",

		"email_org_invite_subject": "{inviter} inviterte deg til {org} på whenweall",
		"email_org_invite_heading": "Bli med i {org}",
		"email_org_invite_body":    "{inviter} har invitert deg til organisasjonen {org} på whenweall. Godta invitasjonen for å planlegge sammen.",
		"email_org_invite_cta":     "Godta invitasjon",

		"email_digest_subject":                 "Nye svar på {title}",
		"email_digest_heading":                 "Nye svar",
		"email_digest_cta":                     "Se avstemningen",
		"email_digest_line_response_created":   "{count} nye svar",
		"email_digest_line_response_updated":   "{count} endrede svar",
		"email_digest_line_response_withdrawn": "{count} trukne svar",
		"email_digest_line_comment_created":    "{count} nye kommentarer",
		"email_digest_line_signup_full":        "Alle plasser er nå tatt",

		"email_notification_cta":                         "Åpne",
		"email_notification_deadline_subject":            "{title} stenger snart",
		"email_notification_deadline_body":               "Fristen for {title} er mindre enn 24 timer unna.",
		"email_notification_finalized_subject":           "{title} er avgjort",
		"email_notification_finalized_body":              "Det er valgt et tidspunkt for {title}.",
		"email_notification_booking_created_subject":     "Ny booking på {title}",
		"email_notification_booking_created_body":        "Det kom en ny booking: {detail}",
		"email_notification_booking_cancelled_subject":   "Booking avlyst på {title}",
		"email_notification_booking_cancelled_body":      "En booking ble avlyst: {detail}",
		"email_notification_booking_rescheduled_subject": "Booking flyttet på {title}",
		"email_notification_booking_rescheduled_body":    "En booking ble flyttet til: {detail}",

		"email_finalized_subject": "{title} er avgjort",
		"email_finalized_heading": "Tidspunktet er satt",
		"email_finalized_body":    "Hei {name}, {option} ble valgt som vinnende tidspunkt.",
		"email_finalized_cta":     "Se detaljer",

		"email_closed_subject": "Avstemningen for {title} er stengt",
		"email_closed_body":    "Avstemningen ble stengt uten at et tidspunkt ble valgt. Se resultatene og følg opp med gruppen.",
		"email_closed_cta":     "Se avstemningen",

		"email_claim_subject": "Påmeldingen din til {title}",
		"email_claim_heading": "Du er påmeldt",
		"email_claim_body":    "Hei {name}, her er det du har meldt deg på.",
		"email_claim_cta":     "Se påmeldingsskjemaet",

		"email_footer": "Du mottar denne e-posten fordi du har brukt {name}.",

		"email_booking_confirmed_subject": "Bekreftet: {title}",
		"email_booking_confirmed_heading": "Bookingen er bekreftet",
		"email_booking_confirmed_body":    "Hei {name}, bookingen din med {organiser} er bekreftet for {when}.",
		"email_booking_location":          "Sted: {location}",
		"email_booking_confirmed_cta":     "Administrer booking",

		"email_booking_organiser_subject": "Ny booking: {title}",
		"email_booking_organiser_heading": "Ny booking",
		"email_booking_organiser_body":    "{name} ({email}) booket {when}.",
		"email_booking_organiser_note":    "Notat: {note}",
		"email_booking_organiser_cta":     "Se bookinger",

		"email_booking_cancelled_subject":        "Avlyst: {title}",
		"email_booking_cancelled_heading":        "Bookingen er avlyst",
		"email_booking_cancelled_body_you":       "Hei {name}, du avlyste bookingen for {when}.",
		"email_booking_cancelled_body_organiser": "Hei {name}, arrangøren avlyste bookingen din for {when}.",
		"email_booking_cancelled_body_visitor":   "Hei {name}, {visitor} avlyste bookingen sin for {when}.",
		"email_booking_cancelled_cta":            "Se siden",

		"email_booking_reminder_subject":  "Påminnelse: {title}",
		"email_booking_reminder_heading":  "Kommende booking",
		"email_booking_reminder_body":     "Hei {name}, dette er en påminnelse om bookingen din for {when}.",
		"email_booking_reminder_location": "Sted: {location}",
		"email_booking_reminder_cta":      "Se detaljer",

		"email_booking_rescheduled_subject": "Flyttet: {title}",
		"email_booking_rescheduled_heading": "Bookingen er flyttet",
		"email_booking_rescheduled_body":    "Hei {name}, bookingen din med {organiser} er flyttet fra {previousWhen} til {when}.",
		"email_booking_rescheduled_cta":     "Administrer booking",

		"email_booking_rescheduled_org_subject": "Booking flyttet: {title}",
		"email_booking_rescheduled_org_heading": "Bookingen er flyttet",
		"email_booking_rescheduled_org_body":    "{name} flyttet bookingen sin fra {previousWhen} til {when}.",
		"email_booking_rescheduled_org_cta":     "Se bookinger",

		"email_booking_sync_failed_subject": "Problem med Google Kalender-synkronisering: {title}",
		"email_booking_sync_failed_heading": "Problem med kalendersynkronisering",
		"email_booking_sync_failed_body":    "Vi klarte ikke å oppdatere Google Kalender for en booking på {title}. Sjekk kalenderen din manuelt for å være sikker på at den stemmer med bookingene dine.",
	},
}

// defaultLocale is used whenever data has no Locale, or an unrecognised one, or a locale that is
// missing a specific key — mirroring paraglide's fallback-to-source-locale behaviour.
const defaultLocale = "en"

// translate looks up key in locale's message set (falling back to defaultLocale for an unknown
// locale or a hole in the requested one), then substitutes each {name} placeholder with the
// following kvs value. kvs is a flat name, value, name, value... list; values are formatted with
// fmt.Sprint.
func translate(locale, key string, kvs ...any) string {
	msg, ok := catalog[locale][key]
	if !ok {
		msg = catalog[defaultLocale][key]
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		name, _ := kvs[i].(string)
		msg = replacePlaceholder(msg, name, kvs[i+1])
	}
	return msg
}
