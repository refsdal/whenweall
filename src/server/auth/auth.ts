import { betterAuth } from 'better-auth'
import { captcha } from 'better-auth/plugins'
import { tanstackStartCookies } from 'better-auth/tanstack-start'
import { drizzleAdapter } from '@better-auth/drizzle-adapter'
import { passkey } from '@better-auth/passkey'
import { env as workerEnv } from 'cloudflare:workers'
import { createDb } from '#/server/db/client'
import * as schema from '#/server/db/schema'
import { appConfig } from '#/app.config'
import { sendMail } from '#/server/mailer/mailer'
import { renderResetPassword, renderVerifyEmail } from '#/server/mailer/templates'

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
        handle: { type: 'string', required: false, input: true },
      },
      deleteUser: { enabled: true },
    },
    plugins: [
      passkey({ rpID: url.hostname, rpName: appConfig.name, origin: env.APP_URL }),
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
