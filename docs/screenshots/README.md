# Screenshots

No screenshots are committed to this repository (see the root README's "Screenshots" section) —
there is no hosted instance to keep them honest against, and CI never generates them. This
directory holds only this note; `bun run screenshots` writes its output here, git-ignored.

To generate a set locally, from the running app:

```bash
bunx playwright install --with-deps chromium   # once
bun run screenshots
```

That runs [`e2e/screenshots.spec.ts`](../../e2e/screenshots.spec.ts) (with `SCREENSHOTS=1`, which
opts it back into the Playwright run — it is excluded from `bunx playwright test` and CI via
`testIgnore` in `playwright.config.ts`). It seeds a user and a poll through the test-only seed
route, drives the real UI, and captures at 1280×800:

| File                | What it shows                                                |
| ------------------- | ------------------------------------------------------------ |
| `landing-light.png` | The landing page, light theme                                |
| `landing-dark.png`  | The landing page, dark theme                                 |
| `poll.png`          | A poll with three participants voting, seen by the organiser |
| `creator.png`       | Step 2 of the poll creator, with two days picked             |
| `dashboard.png`     | The organiser's poll list                                    |
| `signup.png`        | A sign-up sheet with a claimed slot, seen by the organiser   |
| `booking.png`       | A public 1:1 booking page, with a day picked and slots shown |

Use them in a blog post, a release note, or your own fork's README — just do not commit them here.
