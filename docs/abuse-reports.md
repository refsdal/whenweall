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
- sends no more mail (digests, "closes soon" reminders and booking reminders that come due
  during the suspension are skipped, not sent later);
- is **kept in full** — participants, votes, comments, bookings — as evidence. The 30-day
  soft-delete sweep skips it even if the owner deletes it after the report. Deleting the
  owner's **account** (by them, or from the admin console) still removes it immediately, like
  everything else they own.

**In the admin console** (`/admin/content`): paste the link the reporter sent, or the id from a
`Report: poll <id>` / `Report: booking page <id>` subject, press **Find**, check it is the right
item, and press **Suspend**. The dialog asks for a reason, which goes in the audit log with your
name. The item can be found and suspended even after its owner has deleted it, which keeps it
from being purged.

To **undo** a suspension (the report was wrong, or the organiser fixed the problem), find it the
same way and press **Lift suspension**, again with a reason. The content comes back as it was,
with three things to know:

- A poll whose deadline passed during the suspension closes within an hour of being lifted (its
  deadline job waits out the suspension rather than being lost).
- Mail that came due during the suspension (digests, reminders) is not sent afterwards.
- A visitor can still cancel their own booking on a suspended page from their manage link, but
  the cancellation mail and calendar clean-up for it are skipped and not replayed.

Every suspension and lift is in the audit log (`/admin/audit`, actions `suspend-content` and
`unsuspend-content`, target type `poll` or `booking-page`).

### Without the console

If the console is unavailable, the same change is one SQL statement, but it leaves **no audit
row**, so note in the report's e-mail thread who did what and when:

```bash
# Suspend a poll or sign-up sheet (use booking_pages for a booking page)
docker compose exec db psql -U whenweall -d whenweall -c \
  "update polls set suspended_at = now() where id = '<id>' and suspended_at is null"

# Lift it
docker compose exec db psql -U whenweall -d whenweall -c \
  "update polls set suspended_at = null where id = '<id>'"
```

To see what is currently suspended:

```bash
docker compose exec db psql -U whenweall -d whenweall -c \
  "select 'poll' as kind, id, title, suspended_at from polls where suspended_at is not null
   union all
   select 'booking page', id, title, suspended_at from booking_pages where suspended_at is not null
   order by suspended_at desc"
```
