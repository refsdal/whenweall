package bookings

// Test-only exports for package bookings_test (the usual Go export-test-file convention; see
// internal/rooms/export_test.go for the precedent). Nothing here is compiled into the binary.

import (
	"context"

	"github.com/refsdal/whenweall/internal/mailer"
)

// MailBookingPayload exposes the "mail:booking" job payload shape, which external tests otherwise
// only see as raw JSON in scheduled_jobs.
type MailBookingPayload = mailBookingPayload

// ComposeBookingMailForTest exposes composeBookingMail so tests can assert what ONE "mail:booking"
// job would send — recipient, template, locale, the rendered When line — without SMTP: the
// composition is this package's own logic, delivery is internal/mailer's.
func (s *Service) ComposeBookingMailForTest(ctx context.Context, appURL string, p MailBookingPayload) (*mailer.Message, error) {
	return s.composeBookingMail(ctx, appURL, p)
}
