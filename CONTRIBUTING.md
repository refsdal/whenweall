# Contributing to samla

Thanks for your interest in contributing.

## Setup

See the Quick start section in [README.md](./README.md) for how to install
dependencies and run the app locally.

## Development approach

This project is developed test-driven: write a failing test first, then the
code that makes it pass. New behaviour should land with tests, not after.

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`, ...).

## Before opening a pull request

Run the checks locally and make sure they pass:

```bash
bun run lint && bun run test
```

Then open a PR using the provided template, describing the change and how
you tested it.
