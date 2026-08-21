# Contributing to samla

Thanks for your interest in contributing.

## Setup

See the [Quick start](./README.md#quick-start) section in the README for how to install
dependencies and run the app locally. This project uses **bun** for everything — please
don't introduce `npm`/`pnpm` lockfiles or scripts.

## Development approach

This project is developed test-driven: write a failing test first, then the code that makes
it pass. New behaviour should land with tests, not after. The [Testing](./README.md#testing)
section of the README explains the three test layers and how to run a single file.

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`, ...).

## Before opening a pull request

Run the same checks CI runs, and make sure they pass:

```bash
bun run typecheck && bun run lint && bun run format:check && bun run test && bun run build
```

End-to-end tests need a browser once per machine, then run against a built Worker:

```bash
bunx playwright install --with-deps chromium
bun run test:e2e
```

If your change alters the UI in a way the README screenshots show, regenerate them with
`bun run screenshots` and include the updated PNGs — see
[docs/screenshots/README.md](./docs/screenshots/README.md).

Then open a PR using the provided template, describing the change and how you tested it.
