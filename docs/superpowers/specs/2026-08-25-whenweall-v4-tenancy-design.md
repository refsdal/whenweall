# whenweall v4 — organizations, tenancy & billing

**Date:** 2026-08-25 · **Status:** approved in discussion, pending spec review

## Context

whenweall (polls, sign-up sheets, booking pages; v1–v3 complete, not yet deployed) is being
restructured around multi-tenancy before public launch. Organizations become the unit of
ownership and billing: every poll, sheet, and booking page belongs to an organization; users are
members of organizations. There is no production data, so the schema can be restructured freely —
no live migration is needed.

Decisions taken with Anders (2026-08-25): auto-created personal orgs (GitHub model); Stripe via
the Better-Auth plugin; tenancy ships **before** launch; Premium gates = team seats, Google
Calendar sync, branding (no volume caps, retention/auto-archive deferred); price **$7/month flat
per org, up to 10 seats** (~$70/yr annual); org handle takes over booking URLs.

## Goals

- Every content row is owned by an organization; a single authorization path (no dual user/org ownership).
- Solo users feel nothing: signup auto-creates a personal org, org UI stays hidden until a second org or an invite exists.
- Organization = billing account: Free (default) or Premium ($7/mo flat, ≤10 seats) via Stripe.
- One `entitlements` module is the single source of truth for what a plan allows.

## Non-goals (deferred)

- Retention/auto-archival of old content (needs scheduled Worker + emails + restore UX).
- Volume caps on Free (explicitly rejected — gates are seats/sync/branding only).
- Per-seat pricing, >10-seat plans (manual "contact us" for now), Vipps/MobilePay, member vanity booking URLs.

## §1 Auth layer

- Add Better-Auth `organization` plugin: `organization`, `member`, `invitation` tables; roles
  `owner` / `admin` / `member`; `activeOrganizationId` on the session.
- Personal org: database hook after user creation creates an org named after the user with the
  user as `owner`, and sets it active. Every session always has an active org.
- Handle: `user.handle` is **removed**; the org's `slug` (server-managed, same validation as
  today's `handleSchema`) is the public handle. Booking URLs: `/book/{orgSlug}/{pageSlug}`.
  Personal orgs inherit the slug the user would have picked (settings UI moves to org settings).
- Roles: `member` creates content and manages what they created; `admin` manages all org content,
  members, and invitations; `owner` additionally manages billing, org deletion, and slug.
- Invitations: Better-Auth invitation flow with our mailer (new email template, EN+NB).
  Accepting an invite requires an account (existing sign-up flow, then accept).

## §2 Data model

- `polls`: `ownerId` → `organizationId` (cascade delete with org), plus `createdBy` →
  `user.id` (`set null` on user deletion — org content survives members leaving).
- `bookingPages`: same `organizationId` + `createdBy`; plus `memberUserId` → the member whose
  availability and Google connection the page books against (calendar auth stays personal).
  Slug uniqueness: per organization (`booking_pages_org_slug_uidx`, still partial on live rows).
- `participants`, `votes`, `comments`, `bookings`: unchanged — guests never see organizations.
- Subscription state: table from the Stripe plugin (`subscription`), `referenceId` = org id.
- Migrations: squash into a fresh baseline migration set (nothing is deployed; CI applies
  migrations to a fresh local D1 either way).

## §3 Billing

- `@better-auth/stripe` plugin with `subscription` enabled; plans: `free` (implicit default, no
  Stripe object) and `premium` ($7/mo, annual ~$70/yr; Stripe Prices created once, ids via env
  vars `STRIPE_PRICE_PREMIUM_MONTHLY` / `_YEARLY`; secrets `STRIPE_SECRET_KEY`,
  `STRIPE_WEBHOOK_SECRET`).
- `referenceId` = organization id; only org `owner` can subscribe/manage billing; Stripe Customer
  Portal for card/cancel/invoices.
- Webhooks (`/api/auth/stripe/webhook`) keep subscription state in D1; request-path checks read
  D1 only, never Stripe.
- Seat enforcement: inviting is blocked when `members + pending invitations ≥ 10` on Premium, and
  entirely blocked (with upgrade CTA) on Free.

## §4 Entitlements

`src/server/billing/entitlements.ts` exposes `getEntitlements(orgId)` → `{ plan: 'free' |
'premium', maxSeats: 1 | 10, googleSync: boolean, branding: boolean }`. All gating — server
functions (invite, google-connect, branding save, booking-page sync toggle) and UI (disabled
states, upgrade CTAs) — reads this module. No other file may hard-code a plan rule.

Gates at launch: **seats** (Free = 1 member, i.e. inviting requires Premium), **Google Calendar
sync** (Premium; existing per-user Google connection flow becomes reachable only in Premium
orgs), **branding** (org accent colour + logo on poll/booking pages and emails, and removal of
"made with whenweall" attribution; Free shows attribution, default brand).

## §5 UI

- Header: org switcher (list orgs, create org, settings link) — rendered only when the user has
  >1 org or their org has >1 member/invitation; otherwise invisible (personal org stays silent).
- `/settings/organization`: profile (name, slug with same live validation as today's handle
  field), members (role management, remove), invitations (send/revoke), billing (plan card,
  upgrade checkout, Stripe portal link), branding (Premium: colour + logo), danger zone (delete).
- Dashboard, creators (`/new`), bookings pages: scope queries to the active org; switching org
  switches the dashboard.
- Upgrade CTAs where gates bite: invite screen, Google connect, branding settings.
- i18n: all new strings EN + NB as usual.

## §6 Testing & verification

- Unit: entitlements matrix; personal-org creation hook; slug validation reuse.
- Workers tests: authorization matrix (member/admin/owner/outsider × read/write/delete on org
  content), invite seat-cap enforcement, webhook → subscription-state transitions (msw-mocked
  Stripe events), booking-page `memberUserId` calendar binding.
- e2e: sign up (org invisible) → create poll → settings → upgrade (Stripe stubbed via test
  route/msw) → invite member → second user accepts → sees org dashboard → member creates poll →
  admin deletes it. Existing e2e journeys re-pass with org-scoped URLs.
- Full suite green (unit + workers + e2e), build clean, before PR.

## Phasing

1. Org plugin + personal orgs + schema move + authorization (largest chunk; app works exactly as
   before for solo users at the end of it).
2. Stripe plugin + entitlements + seat gate + billing UI.
3. Google-sync gate + branding gate + org switcher/settings polish.
4. (later, separate) retention archival, Vipps, >10-seat plans, privacy/terms pages updated for
   orgs + billing before launch.
