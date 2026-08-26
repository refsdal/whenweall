import { APIError, betterAuth } from 'better-auth'
import { captcha, organization } from 'better-auth/plugins'
import { tanstackStartCookies } from 'better-auth/tanstack-start'
import { drizzleAdapter } from '@better-auth/drizzle-adapter'
import { passkey } from '@better-auth/passkey'
import { eq } from 'drizzle-orm'
import { env as workerEnv } from 'cloudflare:workers'
import { createDb } from '#/server/db/client'
import * as schema from '#/server/db/schema'
import { member } from '#/server/db/schema'
import { appConfig } from '#/app.config'
import { sendMail } from '#/server/mailer/mailer'
import { renderOrgInvite, renderResetPassword, renderVerifyEmail } from '#/server/mailer/templates'
import { handleSchema } from '#/server/bookings/schemas'
import { createPersonalOrganization, deleteOrphanedOwnerOrganizations } from './personal-org'

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

type AuthEnv = Pick<
  Env,
  | 'APP_URL'
  | 'BETTER_AUTH_SECRET'
  | 'GOOGLE_CLIENT_ID'
  | 'GOOGLE_CLIENT_SECRET'
  | 'TURNSTILE_SECRET_KEY'
  | 'EMAIL_FROM'
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
        await sendMail(env, {
          to: user.email,
          ...(await renderResetPassword({ name: user.name, url: resetUrl, locale })),
        })
      },
    },
    emailVerification: {
      sendOnSignUp: true,
      autoSignInAfterVerification: true,
      sendVerificationEmail: async ({ user, url: verifyUrl }) => {
        const locale = (user as { locale?: string }).locale ?? appConfig.defaultLocale
        await sendMail(env, {
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
          // Phase 1 has no seat model and no `/accept-invitation` route yet — every (Free) org
          // can't invite anyway, so block it here rather than ship a dead-end invite flow.
          // `sendInvitationEmail`/`renderOrgInvite` stay wired below for Phase 2/3.
          beforeCreateInvitation: async () => {
            throw new APIError('FORBIDDEN', {
              message: 'Invitations are not available yet',
            })
          },
        },
        sendInvitationEmail: async ({ email, organization: org, inviter, id }) => {
          const locale = (inviter.user as { locale?: string }).locale ?? appConfig.defaultLocale
          await sendMail(env, {
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
  })
}

export type Auth = ReturnType<typeof createAuth>
export type Session = NonNullable<Awaited<ReturnType<Auth['api']['getSession']>>>

let cached: Auth | undefined

export function getAuth(): Auth {
  return (cached ??= createAuth({ d1: workerEnv.DB, env: workerEnv as unknown as AuthEnv }))
}
