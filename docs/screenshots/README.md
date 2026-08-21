# Screenshots

The `*.png` files in this directory are the images the [root README](../../README.md) links
to. They are **generated from the running app**, never drawn or edited by hand:

```bash
bunx playwright install --with-deps chromium   # once
bun run screenshots
```

That runs [`e2e/screenshots.spec.ts`](../../e2e/screenshots.spec.ts), which seeds a user and a
poll through the test-only seed route, drives the real UI, and captures at 1280×800:

| File                | What it shows                                                |
| ------------------- | ------------------------------------------------------------ |
| `landing-light.png` | The landing page, light theme                                |
| `landing-dark.png`  | The landing page, dark theme                                 |
| `poll.png`          | A poll with three participants voting, seen by the organiser |
| `creator.png`       | Step 2 of the poll creator, with two days picked             |
| `dashboard.png`     | The organiser's poll list                                    |
| `signup.png`        | A sign-up sheet with a claimed slot, seen by the organiser   |

The spec is excluded from `bun run test:e2e` and from CI (see `testIgnore` in
`playwright.config.ts`), so the committed PNGs only ever change when a human deliberately
regenerates them. Review the diff before committing — a screenshot is documentation.
