# Abuse reports and takedowns

What to do when someone reports a poll, sign-up sheet or booking page — a scam link, harassment,
anything that should not stay up. Decide this here, before it happens, not in the moment.

## Where reports arrive

Every public poll, sign-up sheet and booking page has a **Report abuse** link under it, and the
site footer has a **Contact** link. Both are `mailto:` links to the support address
(`supportEmail` in `web/src/app.config.ts`, also the address the privacy policy gives for data
protection requests). A report's subject reads `Report: poll <id>` or
`Report: booking page <id>`, and the body carries the link the reporter was looking at.

Reporting needs no account: the person best placed to report a scam link is someone who was sent
it and never signed up.

**Whoever runs the instance must make sure that address delivers to a mailbox somebody reads** —
send it a test message after any change to mail routing. The privacy policy names it as the
channel for GDPR requests, which have a one-month legal deadline, so a silently dead address is
a compliance problem, not just a missed report.

## Triage

1. Open the link from the report. If it already says it was removed or cannot be found, reply
   and close.
2. Decide whether it breaks the [terms](../web/src/routes/terms.tsx). When in doubt about
   something actively harmful (phishing, a payment scam), take it down first and look closer
   afterwards — suspension is fully reversible.
3. If it does, suspend it (below), then email the organiser — the owner of the poll's
   organization — saying what was removed and why, and how to reply if they think it was a
   mistake.
4. If the account itself is the problem rather than one link (several bad polls, a throwaway
   account made for the purpose), lock the account from the admin console instead or as well —
   see [admin-console.md](./admin-console.md).

## Suspending one poll or booking page

Suspension hides exactly one poll, sign-up sheet or booking page without deleting it or touching
the rest of the account. A suspended item:

- answers its public link with a **"This poll has been removed"** (or booking page) card, rather
  than "not found";
- disappears from the organiser's dashboard and can no longer be edited, voted on, booked or
  commented on;
- sends no more mail (deadline reminders, digests, booking reminders all skip it);
- is **kept in full** — participants, votes, comments, bookings — as evidence, and is never
  purged by the 30-day soft-delete sweep (that only looks at `deleted_at`).

The id is the one in the report's subject (the `<id>` in `/p/<id>`; for a booking page, the id in
the report, not the slug in its URL).

```bash
# Suspend a poll or sign-up sheet
docker compose exec db psql -U whenweall -d whenweall -c \
  "update polls set suspended_at = now() where id = '<id>' and suspended_at is null"

# Suspend a booking page
docker compose exec db psql -U whenweall -d whenweall -c \
  "update booking_pages set suspended_at = now() where id = '<id>' and suspended_at is null"
```

`UPDATE 1` means it worked; `UPDATE 0` means the id is wrong or it was already suspended.

To **undo** a suspension (the report was wrong, or the organiser fixed the problem), set the
column back to `NULL` the same way; everything comes back exactly as it was.

To see what is currently suspended:

```bash
docker compose exec db psql -U whenweall -d whenweall -c \
  "select 'poll' as kind, id, title, suspended_at from polls where suspended_at is not null
   union all
   select 'booking page', id, title, suspended_at from booking_pages where suspended_at is not null
   order by suspended_at desc"
```

There is no button for this in the admin console yet, and so no audit row: keep a line in the
report's email thread saying who suspended what and when.
