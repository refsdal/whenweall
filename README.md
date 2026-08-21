# samla

Find a time everyone can make.

[![CI](https://github.com/andersro93/scheduler/actions/workflows/ci.yml/badge.svg)](https://github.com/andersro93/scheduler/actions/workflows/ci.yml)
[![CodeQL](https://github.com/andersro93/scheduler/actions/workflows/codeql.yml/badge.svg)](https://github.com/andersro93/scheduler/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

**Status: in development.** samla is not yet usable — this repository is
being built in public, in small tasks.

samla is a free, fast scheduling poll: propose dates, share a link, let
everyone vote, pick the winner.

## Quick start

```bash
bun install
cp .dev.vars.example .dev.vars
bun run cf-typegen
bun run dev
```

## Stack

- [TanStack Start](https://tanstack.com/start) (React) on
  [Cloudflare Workers](https://workers.cloudflare.com/)
- [Cloudflare D1](https://developers.cloudflare.com/d1/) with
  [Drizzle ORM](https://orm.drizzle.team/)
- [Better Auth](https://www.better-auth.com/) for authentication
- [Tailwind CSS](https://tailwindcss.com/)
- [Vitest](https://vitest.dev/) and [Playwright](https://playwright.dev/)
  for testing

## Roadmap

- **v1** — core scheduling poll: create a poll, share a link, vote, see
  results.
- **v2** — accounts, saved polls, calendar integration.
- **v3** — teams/orgs, recurring polls, richer notifications.

## Notes

- TypeScript is pinned to 5.9 until typescript-eslint supports TypeScript 7.

## Licence

[MIT](./LICENSE) © 2026 Anders Refsdal Olsen
