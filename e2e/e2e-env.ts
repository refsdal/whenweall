/**
 * Constants shared between playwright.config.ts's `webServer`, `global-setup.ts` (starts the
 * throwaway Postgres/Mailpit containers) and `global-teardown.ts` (stops them) — one place so the
 * container names/ports/credentials can't drift apart between the three files that need them.
 *
 * Ports are deliberately different from compose.yaml's own dev/smoke ports (5433/1025/8025), so
 * this suite can run alongside a `docker compose up` session without colliding.
 */
export const DB_CONTAINER = 'whenweall-e2e-db'
export const DB_PORT = 5434
export const DB_USER = 'whenweall'
export const DB_PASSWORD = 'e2e-test-password'
export const DB_NAME = 'whenweall'

export const MAILPIT_CONTAINER = 'whenweall-e2e-mailpit'
export const MAILPIT_SMTP_PORT = 1026
export const MAILPIT_HTTP_PORT = 8026

export const APP_PORT = 3000

/** Not a real secret — a fixed, throwaway value for a database that lives for one test run. */
export const AUTH_SECRET = 'e2e-not-a-real-secret-YuerLHH9iaTZviHi0y2hkpP'

/** Cloudflare Turnstile's own published test keys: the widget auto-solves, every verify passes. */
export const TURNSTILE_SITE_KEY = '1x00000000000000000000AA'
export const TURNSTILE_SECRET_KEY = '1x0000000000000000000000000000000AA'
