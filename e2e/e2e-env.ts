/**
 * Constants shared between playwright.config.ts's `webServer` (its `run-server.sh` starts the
 * throwaway Postgres/Mailpit containers), `global-teardown.ts` (stops them), `mailpit.ts` (reads
 * the inbox) and the specs — one place so container names, ports, images and credentials can't
 * drift apart between the files that need them.
 *
 * Ports are deliberately different from compose.yaml's own dev/smoke ports (3000/5433/1025/8025),
 * so this suite can run alongside a `docker compose up` session without colliding — including the
 * app port: on 3000, `reuseExistingServer` would silently point the suite at the compose app,
 * which has no seed route.
 */
export const DB_CONTAINER = 'whenweall-e2e-db'
export const DB_IMAGE = 'postgres:18-alpine'
export const DB_PORT = 5434
export const DB_USER = 'whenweall'
export const DB_PASSWORD = 'e2e-test-password'
export const DB_NAME = 'whenweall'

export const MAILPIT_CONTAINER = 'whenweall-e2e-mailpit'
/**
 * Pinned, not `:latest`: a Mailpit release that changed the /api/v1 search or message shape would
 * otherwise break `mailpit.ts` on an unrelated day. Bump deliberately, after
 * `docker manifest inspect axllent/mailpit:<tag>` confirms the tag exists. Duplicated in
 * compose.e2e.yaml (compose can't read TypeScript) — keep the two in sync.
 */
export const MAILPIT_IMAGE = 'axllent/mailpit:v1.31.0'
export const MAILPIT_SMTP_PORT = 1026
export const MAILPIT_HTTP_PORT = 8026

export const APP_PORT = 3100
/** What the Go server puts into every e-mailed link (APP_URL) in both server modes. */
export const APP_URL = `http://localhost:${APP_PORT}`

/**
 * `go` (default): run-server.sh builds the SPA and `go run`s the server against two `docker run`
 * containers. `image`: the built Docker image is already up on APP_PORT via the compose overlay
 * (`e2e/compose-e2e.sh up -d --wait`) and the suite only waits for it.
 */
export type ServerMode = 'go' | 'image'
export const SERVER_MODE: ServerMode = process.env.E2E_SERVER === 'image' ? 'image' : 'go'

/**
 * Written by run-server.sh when — and only when — IT started containers / overwrote dist; read and
 * removed by global-teardown.ts, so a developer-started server that Playwright merely reused is
 * never torn down under them.
 */
export const RUN_MARKER = 'e2e/.run-server-marker.json'

/**
 * Not a real secret — a fixed, throwaway value for a database that lives for one test run.
 * Duplicated in e2e/compose.e2e.env for image mode; keep the two in sync.
 */
export const AUTH_SECRET = 'e2e-not-a-real-secret-YuerLHH9iaTZviHi0y2hkpP'
