## Summary

<!-- What does this PR change, and why? -->

## Screenshots

<!-- UI changes: before/after screenshots or a short clip. Delete this section if not applicable. -->

## Test plan

- [ ] Go: `go test ./... && go vet ./... && golangci-lint run ./... && sqlc diff`
- [ ] Web: `cd web && bun run typecheck && bun run lint && bunx vitest run`
- [ ] End-to-end (for user-visible changes): `bunx playwright test`
