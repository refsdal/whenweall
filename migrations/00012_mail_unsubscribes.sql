-- +goose Up
-- The suppression list behind the unsubscribe link in notification mail (#47).
--
-- Keyed on the ADDRESS, not on a user or participant id, and deliberately so: the people this
-- exists for mostly have no account. A guest who leaves an email address to hear a poll's outcome
-- has no settings page and no row to hang a preference off — the address is the only identity
-- they have here, and it is the thing the send path already knows. One entry stops notification
-- mail to that address from every poll, sheet and booking page at once, which is also what
-- someone clicking "unsubscribe" means.
--
-- email is stored normalised (trimmed, lowercased — internal/mailer.NormalizeEmail) so that
-- "Ada@Example.com" and "ada@example.com" cannot become two entries with one of them still
-- receiving mail. The primary key makes the whole table an idempotent upsert target.
--
-- This suppresses NOTIFICATION mail only. Verification, password reset, an invitation, a booking
-- confirmation and its .ics still send to a suppressed address: those are transactional — the
-- direct answer to something the person just did — and withholding them would break the account
-- rather than respect a preference. internal/mailer.Send is the single choke point that knows
-- the difference (Message.Unsubscribable).
CREATE TABLE mail_unsubscribes (
  email      text PRIMARY KEY,
  -- How it was withdrawn: 'link' (the footer link, confirmed on a page) or 'one-click' (RFC 8058
  -- List-Unsubscribe-Post, the button Gmail and Yahoo render next to the sender). Kept for
  -- support ("I never unsubscribed") and for spotting a provider that one-clicks aggressively.
  source     text NOT NULL,
  created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE mail_unsubscribes;
