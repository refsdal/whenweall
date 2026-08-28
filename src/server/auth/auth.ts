import { APIError, betterAuth } from 'better-auth'
import { createAuthMiddleware, getSessionFromCtx } from 'better-auth/api'
import { admin, captcha, organization } from 'better-auth/plugins'
import { adminAc, userAc } from 'better-auth/plugins/admin/access'
import { tanstackStartCookies } from 'better-auth/tanstack-start'
import { drizzleAdapter } from '@better-auth/drizzle-adapter'
import { passkey } from '@better-auth/passkey'
import { stripe } from '@better-auth/stripe'
import { eq } from 'drizzle-orm'
import { env as workerEnv } from 'cloudflare:workers'
import { createDb, type Db } from '#/server/db/client'
import * as schema from '#/server/db/schema'
import { member } from '#/server/db/schema'
import { appConfig } from '#/app.config'
import { sendMailOrThrow } from '#/server/mailer/mailer'
import { renderOrgInvite, renderResetPassword, renderVerifyEmail } from '#/server/mailer/templates'
import { handleSchema } from '#/server/bookings/schemas'
import { createPersonalOrganization, deleteOrphanedOwnerOrganizations } from './personal-org'
import { isAuditedAdminAction, recordAdminAction } from '#/server/admin/audit'
import { createRateLimitStorage } from './rate-limit-storage'
import { authorizeSubscriptionReference, createStripeClient } from '#/server/billing/stripe'
import { getEntitlements, getSeatsUsed } from '#/server/billing/entitlements'
import { PREMIUM_PLAN_NAME } from '#/lib/billing'

/** Shared by `beforeCreateOrganization`/`beforeUpdateOrganization`: any slug the caller supplies
 * (direct API calls included — this is what actually stops `POST /api/auth/organization/create|
 * update` from bypassing `handleSchema`) must satisfy the same rules as a booking-page handle,
 * since an org's slug *is* its public booking handle (`/book/<slug>/...`). */
function assertValidOrgSlug(slug: string | undefined): void {
  if (slug === undefined) return
  const result = handleSchema.safeParse(slug)
  if (!result.success) {
    throw new APIError('BAD_REQUEST', {
      message: result.error.issues[0]?.message ?? 'Invalid organization slug',
    })
  }
}

/** Spec §3 seat gate, shared by `beforeCreateInvitation` below: Free orgs can't invite at all
 * (`UPGRADE_REQUIRED` points the UI at an upgrade CTA); Premium orgs may invite until current
 * members + pending invitations reach `maxSeats` (`SEAT_LIMIT_REACHED`). Runs inside the hook so
 * the raw `POST /api/auth/organization/invite-member` endpoint stays gated, not just the UI. */
async function assertSeatAvailable(db: Db, organizationId: string) {
  const entitlements = await getEntitlements(db, organizationId)
  if (entitlements.plan === 'free') {
    throw new APIError('FORBIDDEN', { message: 'UPGRADE_REQUIRED' })
  }

  const seatsUsed = await getSeatsUsed(db, organizationId)
  if (seatsUsed >= entitlements.maxSeats) {
    throw new APIError('FORBIDDEN', { message: 'SEAT_LIMIT_REACHED' })
  }
}

/** Spec §3 acceptance gate, run from `beforeAcceptInvitation` below. This is deliberately NOT
 * `assertSeatAvailable` re-run at acceptance time: accepting converts a pending invitation into
 * a member, so the occupied-seat count (`getSeatsUsed` = members + pending) doesn't change — the
 * invite already counted itself. The real hole this closes is the *window* between an invite
 * being sent and it being accepted: the org can be downgraded to Free in that window (Free orgs
 * can't have a second member at all), or — independent of pending invitations — its member count
 * alone can reach the Premium cap. So this checks `members` only, not `getSeatsUsed`. */
async function assertOrgCanAcceptInvitation(db: Db, organizationId: string) {
  const entitlements = await getEntitlements(db, organizationId)
  const members = await db.query.member.findMany({
    where: eq(member.organizationId, organizationId),
  })

  if (entitlements.plan === 'free') {
    // A Free org's one seat is always occupied by its owner, so any acceptance — which would
    // add a second member — is blocked.
    if (members.length >= 1) {
      throw new APIError('FORBIDDEN', { message: 'UPGRADE_REQUIRED' })
    }
    return
  }

  if (members.length >= entitlements.maxSeats) {
    throw new APIError('FORBIDDEN', { message: 'SEAT_LIMIT_REACHED' })
  }
}

type AuthEnv = Pick<
  Env,
  | 'APP_URL'
  | 'BETTER_AUTH_SECRET'
  | 'GOOGLE_CLIENT_ID'
  | 'GOOGLE_CLIENT_SECRET'
  | 'TURNSTILE_SECRET_KEY'
  | 'EMAIL_FROM'
  | 'STRIPE_SECRET_KEY'
  | 'STRIPE_WEBHOOK_SECRET'
  | 'STRIPE_PRICE_PREMIUM_MONTHLY'
  | 'STRIPE_PRICE_PREMIUM_YEARLY'
> & { EMAIL?: SendEmail; APP_ENV?: string }

export function createAuth({ d1, env }: { d1: D1Database; env: AuthEnv }) {
  const url = new URL(env.APP_URL)

  return betterAuth({
    appName: appConfig.name,
    baseURL: env.APP_URL,
    secret: env.BETTER_AUTH_SECRET,
    database: drizzleAdapter(createDb(d1), { provider: 'sqlite', schema }),
    emailAndPassword: {
      enabled: true,
      requireEmailVerification: true,
      sendResetPassword: async ({ user, url: resetUrl }) => {
        const locale = (user as { locale?: string }).locale ?? appConfig.defaultLocale
        await sendMailOrThrow(env, {
          to: user.email,
          ...(await renderResetPassword({ name: user.name, url: resetUrl, locale })),
        })
      },
    },
    emailVerification: {
      sendOnSignUp: true,
      autoSignInAfterVerification: true,
      // `sendMailOrThrow`, not `sendMail`, and the trade is deliberate. Better-Auth creates the
      // user row before sending, so a throw here surfaces an error to someone whose account now
      // exists — retrying the same address gets them "email taken", which is confusing.
      //
      // The alternative is worse. With `requireEmailVerification: true`, a verification mail that
      // silently fails leaves an account that can never be signed in to, and the account holder
      // cannot tell that apart from a slow inbox — so they wait, and nothing ever comes. A visible
      // error at least says something went wrong, and the account is recoverable through the
      // resend flow (`/send-verification-email`). Loud and recoverable beats silent and stuck.
      sendVerificationEmail: async ({ user, url: verifyUrl }) => {
        const locale = (user as { locale?: string }).locale ?? appConfig.defaultLocale
        await sendMailOrThrow(env, {
          to: user.email,
          ...(await renderVerifyEmail({ name: user.name, url: verifyUrl, locale })),
        })
      },
    },
    socialProviders:
      env.GOOGLE_CLIENT_ID && env.GOOGLE_CLIENT_SECRET
        ? { google: { clientId: env.GOOGLE_CLIENT_ID, clientSecret: env.GOOGLE_CLIENT_SECRET } }
        : {},
    user: {
      additionalFields: {
        locale: { type: 'string', required: false, input: true },
      },
      deleteUser: { enabled: true },
    },
    databaseHooks: {
      user: {
        create: {
          after: async (u) => {
            await createPersonalOrganization(
              createDb(d1),
              u as { id: string; name: string; email: string },
            )
          },
        },
        delete: {
          before: async (u) => {
            // Runs before the user row (and its `member` rows, via their own cascading FK) is
            // actually deleted — see `deleteOrphanedOwnerOrganizations`'s own doc comment.
            await deleteOrphanedOwnerOrganizations(createDb(d1), (u as { id: string }).id)
          },
        },
      },
      session: {
        create: {
          before: async (s) => {
            // Every session starts scoped to the user's first org (their personal one).
            const m = await createDb(d1).query.member.findFirst({
              where: eq(member.userId, s.userId),
              orderBy: (mm, { asc }) => [asc(mm.createdAt)],
            })
            return { data: { ...s, activeOrganizationId: m?.organizationId ?? null } }
          },
        },
      },
    },
    plugins: [
      // The platform role is deliberately 'staff', not the plugin's default 'admin': `OrgRole`
      // already uses 'admin' for a per-org role on `member`, and two different `role` values
      // sharing a name is how authorization bugs get written.
      // `adminRoles` must name roles that exist in `roles`, so 'staff' is mapped explicitly to
      // the plugin's own full admin permission set. Note `adminAc` deliberately omits
      // 'impersonate-admins': one staff user cannot impersonate another.
      admin({
        defaultRole: 'user',
        adminRoles: ['staff'],
        roles: { user: userAc, staff: adminAc },
      }),
      passkey({ rpID: url.hostname, rpName: appConfig.name, origin: env.APP_URL }),
      organization({
        creatorRole: 'owner',
        // Every (currently Free-tier) org can have at most 5 — counts the user's personal org too.
        organizationLimit: 5,
        organizationHooks: {
          beforeCreateOrganization: async ({ organization: org }) => {
            assertValidOrgSlug(org.slug)
          },
          beforeUpdateOrganization: async ({ organization: org }) => {
            assertValidOrgSlug(org.slug)
          },
          // Phase 2 §3: Free orgs can't invite (UPGRADE_REQUIRED); Premium orgs can invite until
          // members + pending invitations reach `getEntitlements(...).maxSeats`
          // (SEAT_LIMIT_REACHED). See `assertSeatAvailable` above.
          beforeCreateInvitation: async ({ invitation }) => {
            await assertSeatAvailable(createDb(d1), invitation.organizationId)
          },
          // Closes the other half of Phase 2 §3's seat gate: `beforeCreateInvitation` only
          // covers invite *creation*, but an org can be downgraded to Free (or hit its member
          // cap) in the window between an invite being sent and it being accepted. See
          // `assertOrgCanAcceptInvitation`'s own doc comment for why this isn't just
          // `assertSeatAvailable` again.
          beforeAcceptInvitation: async ({ invitation }) => {
            await assertOrgCanAcceptInvitation(createDb(d1), invitation.organizationId)
          },
        },
        sendInvitationEmail: async ({ email, organization: org, inviter, id }) => {
          const locale = (inviter.user as { locale?: string }).locale ?? appConfig.defaultLocale
          await sendMailOrThrow(env, {
            to: email,
            ...(await renderOrgInvite({
              orgName: org.name,
              inviterName: inviter.user.name,
              url: `${env.APP_URL}/accept-invitation/${id}`,
              locale,
            })),
          })
        },
      }),
      stripe({
        stripeClient: createStripeClient(env.STRIPE_SECRET_KEY),
        stripeWebhookSecret: env.STRIPE_WEBHOOK_SECRET,
        createCustomerOnSignUp: false,
        subscription: {
          enabled: true,
          plans: [
            {
              name: PREMIUM_PLAN_NAME,
              priceId: env.STRIPE_PRICE_PREMIUM_MONTHLY,
              annualDiscountPriceId: env.STRIPE_PRICE_PREMIUM_YEARLY,
            },
          ],
          // Spec §3: subscriptions are org-scoped (referenceId is the org id) and only the org
          // owner may manage billing for it.
          authorizeReference: async ({ user, referenceId }) =>
            authorizeSubscriptionReference(createDb(d1), { userId: user.id, referenceId }),
        },
      }),
      captcha({
        provider: 'cloudflare-turnstile',
        secretKey: env.TURNSTILE_SECRET_KEY,
        // Default-enforced Better-Auth endpoints (sign-up, sign-in, password reset) plus
        // `/send-verification-email`, whose resend flow otherwise ships unprotected.
        endpoints: [
          '/sign-up/email',
          '/sign-in/email',
          '/request-password-reset',
          '/send-verification-email',
        ],
      }),
      // tanstackStartCookies dynamically imports '@tanstack/react-start/server', which relies on
      // virtual modules only provided by the tanstackStart() Vite plugin during a real app
      // request. The workers Vitest project runs auth.api.* handlers outside that plugin/request
      // context, so the import throws there. Better-Auth's own cookie handling still sets the
      // session cookie on the returned Response either way, so tests are unaffected; this plugin
      // is skipped only in the isolated test environment and stays active in dev/production.
      ...(env.APP_ENV === 'test' ? [] : [tanstackStartCookies()]),
    ],
    // Better-Auth defaults `enabled` to `isProduction`, which it derives from a *runtime*
    // `process.env.NODE_ENV` lookup — a variable this worker never sets, so the default resolves
    // to "development" and rate limiting silently switches itself off. Set explicitly rather than
    // inferred. `window`/`max`/`customRules` are deliberately left alone: the defaults (and the
    // stricter built-in rules for sign-in, sign-up and password reset) are what we want, and only
    // the storage needed replacing — the default is a `Map` inside the isolate, which in Workers
    // means a fresh, empty counter per isolate and per colo.
    rateLimit: {
      enabled: true,
      customStorage: createRateLimitStorage(),
    },
    hooks: {
      /**
       * Audits every mutating admin endpoint, wherever the call came from.
       *
       * Deliberately here rather than inside the admin server functions our UI calls:
       * `/api/auth/admin/*` is reachable directly by any staff user with `curl`, and a trail that
       * only records the calls made through our own screens is not a trail.
       *
       * Runs after the endpoint succeeded, so this records outcomes rather than attempts. Note
       * that Better-Auth only runs hooks for endpoints dispatched through the HTTP router —
       * calling `auth.api.*` as a plain function bypasses them by design, which is why the test
       * for this drives the handler with a real Request.
       */
      after: createAuthMiddleware(async (ctx) => {
        if (!ctx.path?.startsWith('/admin/')) return
        const action = ctx.path.slice('/admin/'.length)
        if (!isAuditedAdminAction(action)) return

        try {
          const session = await getSessionFromCtx(ctx)
          if (!session) return

          const body = (ctx.body ?? {}) as { userId?: string; data?: Record<string, unknown> }

          await recordAdminAction(createDb(d1), {
            actorUserId: session.user.id,
            actorEmail: session.user.email,
            action,
            targetType: 'user',
            targetId: body.userId ?? null,
            reason: ctx.headers?.get('x-admin-reason') ?? null,
            // Field NAMES only. An audit row must never become a second copy of the data it
            // describes, and must never carry password material.
            metadata: body.data ? { fields: Object.keys(body.data) } : null,
          })
        } catch (err) {
          // The action already happened; failing the response now would tell the caller it did
          // not. Log loudly instead — a gap in the trail is worth noticing.
          console.error(JSON.stringify({ event: 'admin.audit_write_failed', action }), err)
        }
      }),
      before: createAuthMiddleware(async (ctx) => {
        if (ctx.path === '/organization/invite-member') {
          // Better-Auth's invite-member endpoint returns *before* calling
          // `organizationHooks.beforeCreateInvitation` when `ctx.body.resend` is true and a
          // pending invitation already exists for that email (see `createInvitation` in
          // node_modules/better-auth/dist/plugins/organization/routes/crud-invites.mjs) — so an
          // org downgraded to Free (or already at its seat cap) after sending an invite could
          // resend its way around the gate forever. Run the same check unconditionally here;
          // it's an idempotent double-check on the normal (non-resend) path, since
          // `beforeCreateInvitation` still runs for that case too.
          //
          // The endpoint resolves a missing `organizationId` from the session's active org
          // (`ctx.body.organizationId || session.session.activeOrganizationId`); most callers
          // never pass it explicitly, so this hook resolves it the same way rather than only
          // trusting the body.
          const body = ctx.body as { organizationId?: string } | undefined
          const organizationId =
            body?.organizationId ?? (await getSessionFromCtx(ctx))?.session.activeOrganizationId
          if (organizationId) {
            await assertSeatAvailable(createDb(d1), organizationId)
          }
        }

        if (ctx.path === '/subscription/upgrade') {
          // Without an explicit `referenceId`, @better-auth/stripe silently defaults to the
          // session user's id and never calls `authorizeReference` at all — a member could buy
          // a subscription that grants no org anything (see `referenceMiddleware` in
          // node_modules/@better-auth/stripe/dist/index.mjs). Every upgrade must say which org
          // it's for.
          const body = ctx.body as { referenceId?: string } | undefined
          if (!body?.referenceId) {
            throw new APIError('BAD_REQUEST', { message: 'referenceId required' })
          }
        }
      }),
    },
  })
}

export type Auth = ReturnType<typeof createAuth>
export type Session = NonNullable<Awaited<ReturnType<Auth['api']['getSession']>>>

let cached: Auth | undefined

export function getAuth(): Auth {
  return (cached ??= createAuth({ d1: workerEnv.DB, env: workerEnv as unknown as AuthEnv }))
}
