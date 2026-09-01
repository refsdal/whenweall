# Go Rewrite Plan 8/8 — Frontend Conversion, Docs, Release & Cloudflare Removal

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The React app becomes a pure SPA under `web/` talking to the Go API; the Playwright oracle runs green against the real container; README/CONTRIBUTING/runbooks tell the self-host story; a GHCR release pipeline ships multi-arch images; every Cloudflare artifact is deleted.

**Architecture:** Components, routes, styling, and paraglide stay. What changes: TanStack **Start → Router-only** (Vite SPA), server-function imports → `web/src/api/*` fetch wrappers (contracts = the endpoint tables in plans 3–7 + `internal/auth/routes.txt`), better-auth client → Limen-backed hooks, ws clients → the plan 6 wire format. The e2e suite is the acceptance gate for the whole rewrite.

**Tech Stack:** Vite + `@tanstack/react-router` (drop `@tanstack/react-start`), Bun as frontend toolchain only, Playwright, GitHub Actions + buildx.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§1, §2, §7, §8, §9)

## Global Constraints

Plan 1's Global Constraints apply. Plus:
- No SSR, no prerendering, no OG injection — pure SPA (user decision; do not sneak any back in).
- Passkey and billing UI are **deleted**, not hidden.
- Frontend unit tests stay on Vitest (`web/`); the vitest *workers* project dies with the runtime.
- e2e specs are edited as little as possible — they are the oracle; a spec change needs a reason in the commit message (deleted feature, changed auth route, seed shape).

---

### Task 1: Move the app to `web/` and strip TanStack Start

**Files:**
- Move: `src/` (frontend parts), `public/`, `messages/`, `project.inlang/`, `components.json`, `index.html` → `web/…`; frontend config files (`vite.config.ts`, `tsconfig.json`, `eslint.config.js`, `.prettierrc`, `tsr.config.json`, `vitest.config.ts`) → `web/`
- Create: `web/package.json` (frontend deps only), `web/src/main.tsx` (SPA bootstrap: `createRouter` + `RouterProvider`)
- The old backend dirs (`src/server`, `src/do`, `src/rooms`, `src/routes/api`) stay behind at the repo root for now — Task 8 deletes them; nothing under `web/` may import them.

- [ ] **Step 1:** `git mv` the frontend tree; write `web/package.json` from the current one minus: `@tanstack/react-start`, `better-auth`, `@better-auth/*`, `stripe`, `drizzle-orm`, `@react-email/*`, `@cloudflare/*`, `wrangler`, `@marsidev/react-turnstile` stays (it's a widget), server-only libs. Vite config: `@vitejs/plugin-react` + tailwind + paraglide plugins, `server.proxy = { '/api': { target: 'http://localhost:3000', ws: true } }`, `build.outDir = 'dist'`.
- [ ] **Step 2:** Replace Start entry points: file-route definitions keep `createFileRoute` (Router-only API is the same); delete `createServerFn` imports — they now fail typecheck, which is the worklist for Tasks 2–4.
- [ ] **Step 3:** `cd web && bun install && bun run generate-routes` — expect typecheck failures listing every server-function call site; save the list into the task notes for Tasks 2–4.
- [ ] **Step 4: Commit** — `git commit -m "refactor(web): move SPA to web/, drop TanStack Start"`

---

### Task 2: The API client

**Files:**
- Create: `web/src/api/client.ts` (fetch core), `web/src/api/{polls,bookings,admin,config}.ts`, `web/src/api/types.ts`
- Test: `web/src/api/__tests__/client.test.ts` (Vitest + msw, already a dep)

**Interfaces:**

```ts
// client.ts
export class ApiError extends Error { code: string; fields?: Record<string, string>; status: number }
export async function api<T>(method: string, path: string, body?: unknown, opts?: { guestToken?: string; captchaToken?: string }): Promise<T>
// - credentials: 'same-origin'; JSON in/out; unwraps the {"error":{code,message,fields}} envelope into ApiError
// - guestToken → X-Guest-Token header; captchaToken → X-Captcha-Token
```

Per-module functions mirror the old server-function names 1:1 so call sites change mechanically (`createPoll(input)`, `getPoll(id, guestToken?)`, `claimSlot(...)`, `bookSlot(...)`, `fetchAdminStats()`, `getPublicConfig()`, …) with `types.ts` hand-mirroring the Go response shapes (plans 4–7 tables). 

- [ ] Steps: msw tests (envelope unwrap incl. `fields`, guest/captcha headers, 401 → ApiError code `unauthenticated`) → implement → `bun test` green in `web/` (vitest) → commit `feat(web): typed api client for the go backend`.

---

### Task 3: Auth hooks + deletions

**Files:**
- Create: `web/src/api/auth.ts` (`signup/signin/signout/me/requestPasswordReset/resetPassword/verifyEmail/orgs…` per `internal/auth/routes.txt`), `web/src/lib/use-session.ts` (context + hook replacing better-auth's `useSession`)
- Modify: every component importing `#/server/auth/client` (login, signup, settings, org switcher, invitation accept)
- Delete: `web/src/components/billing/`, billing/settings-billing routes, passkey UI in login/settings, `src/routes/api/auth/$.ts` leftovers in web

- [ ] Steps: port call sites one route at a time (login → signup → verify-email → forgot/reset-password → settings → org invite accept), running `bun run typecheck` down to zero errors; existing route/component vitest suites green (update mocks from better-auth shapes to the new hook); commit `feat(web): limen-backed auth flows; billing and passkey ui removed`.

---

### Task 4: Live rooms client

**Files:**
- Modify: the ws hooks/components for poll, booking, stats (find via `grep -r "WebSocket\|/ws" web/src`)
- Create: `web/src/lib/room-socket.ts`

**Interfaces:**

```ts
// Reconnecting socket for the plan-6 wire format.
export function connectRoom(opts: {
  path: string                 // e.g. `/api/v1/polls/${id}/ws`
  guestToken?: string
  onSnapshot(data: unknown, seq: number): void
  onEvent(type: string, data: unknown, seq: number): void  // also receives "presence"
  onResync(): void             // server asked for a refetch — call onSnapshot path again
}): { close(): void }
// Tracks last seq; reconnects with `?since=` after backoff; "resync" clears since.
```

- [ ] Steps: unit tests with a mock ws server (snapshot → events ordering, reconnect sends since, resync clears it) → adapt the three consumers (event names already match `src/do/*protocol.ts` per plan 6) → vitest green → commit `feat(web): room socket client with replay`.

---

### Task 5: Go test-seed endpoint for e2e

The Playwright fixtures call `POST /api/test/seed` (see `src/routes/api/test/seed.ts` and `e2e/` fixtures — read both). Port it or the oracle can't run.

**Files:**
- Create: `internal/httpserver/testroutes.go`
- Test: `internal/httpserver/testroutes_test.go`

**Interfaces:** mounted **only when `cfg.EnableTestRoutes`** (config already hard-fails if set in production): `POST /api/test/seed` accepting the same JSON the e2e fixtures send (users incl. staff flag, orgs, polls, sheets, booking pages — mirror the TS seed's shape exactly) and resetting/creating state. Signs users up through the auth seam so password login works in specs.

- [ ] Steps: failing test (seed a spec-shaped payload → user can sign in, poll exists, staff user passes RequireStaff — the `a442f9f` lesson: forward the staff role) → implement → green → commit `feat(http): e2e seed routes behind ENABLE_TEST_ROUTES`.

---

### Task 6: Dockerfile stage 1 + dev docs wiring

- [ ] **Step 1:** Add the bun stage to `Dockerfile` and embed the real build:

```dockerfile
FROM oven/bun:1-alpine AS web
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

# in the go build stage, before `go build`:
COPY --from=web /web/dist ./internal/httpserver/dist
```

- [ ] **Step 2:** `docker compose build && docker compose up -d` → browse `http://localhost:3000`, sign up (Mailpit for SMTP: add a commented `mailpit` service block to compose.yaml for local trials), create a poll, vote from an incognito window, watch it live-update. Fix what this smoke test surfaces.
- [ ] **Step 3: Commit** — `git commit -m "feat(docker): full image with embedded spa build"`

---

### Task 7: Playwright green — the acceptance gate

- [ ] **Step 1:** Update `playwright.config.ts`: `webServer` builds the SPA (`cd web && bun run build`), copies dist, and runs `go run ./cmd/whenweall` with `ENABLE_TEST_ROUTES=true`, `APP_ENV=test`, testcontainer-or-compose Postgres, Mailpit for SMTP env.
- [ ] **Step 2:** Delete billing and passkey specs (list them in the commit message). Update auth-flow specs only where routes changed (`/api/auth/*` → `/api/v1/auth/*` etc.).
- [ ] **Step 3:** `bun run test:e2e` — iterate to green. **Every non-billing/passkey failure is a porting bug in plans 3–7, not a spec to edit** — fix the Go side (upgrade path per brainstorming ratchet if something structural surfaces).
- [ ] **Step 4:** `bun run screenshots` still works. Commit — `git commit -m "test(e2e): oracle green against the go backend"`

---

### Task 8: Delete Cloudflare & the TS backend

- [ ] **Step 1:** Delete: `src/` (everything left), `wrangler.jsonc`, `worker-configuration.d.ts`, `.dev.vars*`, `.wrangler/`, `spike/`, `drizzle/`, `drizzle.config.ts`, `emails/` (ported in plan 2), `vitest.config.ts` root remnants, `vite.config.spike.ts`, `bunfig.toml` + `test/setup.bun.ts` (Bun-era), root `package.json`/`bun.lock`/`node_modules` (web/ has its own), `.github/workflows/codeql.yml` if CF-specific config remains.
- [ ] **Step 2:** Root `.bun-version` moves into `web/`. `tsconfig`/eslint/prettier configs live only under `web/` now.
- [ ] **Step 3:** Full verification: `go test ./... && (cd web && bun run typecheck && bun run lint && bun test) && bun run test:e2e && docker compose build`.
- [ ] **Step 4: Commit** — `git commit -m "chore!: remove cloudflare workers runtime and the typescript backend"`

---

### Task 9: README, CONTRIBUTING, runbooks

- [ ] **Step 1:** Rewrite `README.md` per spec §8 — every bullet there is a required section: hero + badges (CI, MIT, Go, ghcr pulls), five-minute quickstart (compose + `.env`: `AUTH_SECRET`, SMTP; Mailpit tip), env reference table (from `internal/config`, incl. capability semantics "unset = feature invisible, never broken"), Caddy two-liner + nginx/Traefik ws note, ops story ("backup = pg_dump; upgrade = pull new tag"), `create-staff-user` admin bootstrap, development section (`docker compose up db` + `go run ./cmd/whenweall` + `cd web && bun dev`), non-goals (no Helm, no SQLite, no built-in TLS). Delete every Workers/D1/DO/deploy-to-CF passage, including the "no Node server, no container" virtue paragraph.
- [ ] **Step 2:** Update `CONTRIBUTING.md` (Go toolchain, test layout, sqlc/goose workflow, `docs/limen-migrations.md` pointer) and the admin runbook in `docs/` (seed-user section → the subcommand).
- [ ] **Step 3:** README claims audit: run the quickstart from scratch in a clean directory copy — every command in the README must actually work as written.
- [ ] **Step 4: Commit** — `git commit -m "docs: self-host story for the go rewrite"`

---

### Task 10: Release pipeline

**Files:**
- Create: `.github/workflows/release.yml`
- Modify: `.github/workflows/ci.yml` (final shape: go job from plan 1 + `web` job (typecheck/lint/vitest) + e2e job against the built image; drop the old TS jobs)

- [ ] **Step 1:** `release.yml` — on tag `v*` and on main (as `latest`): buildx `--platforms linux/amd64,linux/arm64`, push `ghcr.io/refsdal/whenweall:{latest,semver}`, `--sbom=true --provenance=true`, `VERSION` build-arg from the tag.
- [ ] **Step 2:** CI final shape green on the PR.
- [ ] **Step 3: Commit** — `git commit -m "ci: ghcr multi-arch release with sbom and provenance"`

---

### Task 11: Final review

- [ ] Run the full gate one last time (Task 8 Step 3 command set + `docker compose up` smoke).
- [ ] Invoke superpowers:requesting-code-review against the whole branch before merge.
