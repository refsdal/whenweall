# Security Policy

## Supported versions

Only the `main` branch is supported. Security fixes are released as soon as
possible after a report is triaged; there are no maintained release branches.

## Reporting a vulnerability

Please **do not** open a public issue for security vulnerabilities.

Report privately using one of these channels:

1. GitHub's [private vulnerability reporting](https://github.com/refsdal/whenweall/security/advisories/new)
   ("Report a vulnerability" under the Security tab) — preferred.
2. Email `security@whenweall.com`.

We aim to acknowledge new reports within **72 hours** and will work with you
on a fix and disclosure timeline.

## Repository security settings (owner checklist)

The following must be enabled in the GitHub UI (not managed by code):

- Branch protection on `main`: require the CI status check and require a
  pull request before merging.
- Secret scanning, with push protection enabled.
- Dependabot alerts and Dependabot security updates.
- Private vulnerability reporting.
- CodeQL default setup: **off** — this repository runs its own CodeQL
  workflow (`.github/workflows/codeql.yml`) instead.
