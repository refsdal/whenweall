# whenweall — live usage counters on the landing page

**Date:** 2026-08-27 · **Status:** draft for review

## Context

The landing page currently makes its case with copy and one animated example poll (`VoteGridMock`).
It says nothing about whether anyone actually uses the product. Live counters — polls created, and
responses broken down by answer — turn an empty claim into evidence, and a number that visibly
moves while you are reading it does more than a static one.

Decisions taken with Anders (2026-08-27): count **polls decided** (the headline), **polls
created**, and **responses split by yes / if-need-be / no**; updates should be **real time**;
durable objects are the assumed mechanism; low numbers are shown rather than hidden.

This is deliberately scoped as its own project. It shares no code with the notification subsystem
on `feat/notifications` (PR #26) — the increment points are in the poll and claim services, not the
notification emit boundary — so the two branches can land in either order.

## Goals

- A public, aggregate view of how much whenweall is being used, on `/`.
- Numbers that move without a page reload while someone is looking at the section.
- Correct starting values: seeded from the real history, never starting from zero.
- No measurable cost to the write paths (creating a poll, casting a vote) and no new failure mode
  for them.

## Non-goals (deferred)

- Per-day time series and sparklines. The counter model below leaves room for it; nothing stores
  a retained series.
- Any per-organization, per-poll, or per-user analytics. These counters are global and anonymous.
- An admin dashboard, export, or historical query interface.
- Geographic or referrer breakdowns.

## §1 What is counted

Five monotonic lifetime totals, held together in one record:

| Counter             | Incremented when                                                               |
| ------------------- | ------------------------------------------------------------------------------ |
| `pollsFinalized`    | `finalizePoll` picks a winning time — **the headline number**                  |
| `pollsCreated`      | `createPoll` inserts a poll (including duplicates — a duplicate is a new poll) |
| `responsesYes`      | An answer of `yes` is submitted                                                |
| `responsesIfNeedBe` | An answer of `ifneedbe` is submitted                                           |
| `responsesNo`       | An answer of `no` is submitted                                                 |

**These count submissions, not current state.** Someone who answers "yes" and later changes it to
"no" adds one to each — the totals do not net out, and `responsesYes` never goes down. This is a
deliberate choice: the counters answer "how much has this been used", which is what a landing page
is for, and a number that ticks _downwards_ while a visitor watches reads as a bug. The honest
cost is that the totals slightly overstate distinct opinions held. Worth saying plainly here
because the alternative — net current state — is what someone would assume from the label unless
the copy is chosen carefully. **The UI must therefore say "responses recorded", not "people said
yes".**

`pollsCreated` is not decremented when a poll is deleted, for the same reason.

**`pollsFinalized` is the number the section leads with** (added 2026-08-27). Polls created counts
attempts; polls decided counts outcomes — it says the product did its job rather than that someone
tried it. `finalizePoll` rejects an already-finalized poll with `CONFLICT`, so every increment is a
genuine not-decided → decided transition and cannot double-count.

It is also, structurally, the **smallest** of the counters: polls get abandoned, deadlines pass
without a winner being picked, and sign-up sheets cannot be finalized at all. That shapes §5 —
presenting it as a large figure beside a larger "polls created" would invite reading the pair as a
conversion rate.

## §2 Storage and the durable object

A single `StatsRoom` durable object, addressed by a constant name (`global`), holding:

- `counters` — the five totals.
- `seeded` — a flag, see §4.
- WebSocket connections from landing-page visitors, accepted through
  `ctx.acceptWebSocket()` so they hibernate exactly as `PollRoom`'s do.

**Write batching.** Increments arrive as fire-and-forget RPC and accumulate in an in-memory delta.
An alarm flushes the delta to storage every `FLUSH_INTERVAL_MS` (10s) and clears it. Storage writes
are therefore bounded at ~6/minute regardless of traffic, rather than one per vote.

The cost of batching is a crash window: a DO evicted between flushes loses up to 10 seconds of
increments. For a marketing counter that is an acceptable trade against a storage write on every
vote — and it is one of the few places in this codebase where losing data is genuinely fine. It
must not be copied to anything that matters.

**Single-instance contention is a known, accepted limit.** Every poll creation and every vote in
the product routes an RPC to one durable object, which serialises them. Decided with Anders
(2026-08-27): traffic is nowhere near this being a problem, and it is not worth designing around
in advance.

Recorded so the decision has a number attached rather than being rediscovered under load: the
point to revisit is roughly **10 writes/second sustained**. The escape routes, in increasing order
of effort, are to put a queue in front of the increments, to make the object read-mostly by
absorbing writes elsewhere, or to shard into N instances (`global-0` … `global-N`) and sum on
read. The read path is already an aggregate, so sharding in particular is additive rather than a
rewrite — none of this is work that gets harder by being deferred, which is what makes deferring
it the right call.

## §3 Increment points

`src/server/stats/stats-client.ts` exposes one best-effort helper, mirroring
`notifications/do-client.ts`'s contract — it catches and logs, and never fails the request that
triggered it:

```
recordPollCreated(): Promise<void>
recordPollFinalized(): Promise<void>
recordResponses(answers: Answer[]): Promise<void>
```

Called from the **server-function** layer, not the services:

- `createPoll` and `duplicatePoll` (`src/server/polls/polls.functions.ts`).
- `finalizePoll` (`src/server/polls/polls.functions.ts`) — the headline counter.
- `addParticipant` and `updateParticipant` (`src/server/polls/participants.functions.ts`) — with
  the answers actually submitted.
- `claimSlot` (`src/server/polls/participants.functions.ts`) — a sign-up claim is stored as a `yes`
  vote, so it counts as one.

**Changed during implementation.** The draft put these in the services (`service.ts`,
`participants.ts`, `claims.ts`). Two problems surfaced:

- `applyClaim` runs **inside** `PollRoom`, within the serialised `#serialize` block that makes
  capacity checks atomic. Calling a second durable object from there would put every sign-up claim
  in the product behind a round-trip to the stats object — turning a marketing counter into a
  bottleneck on a correctness-critical path.
- The services are also what `test/helpers.ts` calls, so every `makePoll` in the suite would
  generate stats traffic and couple unrelated tests to the counter.

The server-function boundary avoids both, and is the layer that already owns other best-effort
side effects (`notifyChanged`, `syncDeadline`, `emitPollEvent`).

A failed stats call must never surface to a user or roll back a write. This is the same rule the
notification emit boundary follows, and for the same reason.

## §4 Seeding from history

The counters must not start at zero on a database that already has polls in it. A migration cannot
call a durable object, so seeding is lazy: on first read or first increment, if `seeded` is absent,
the DO runs three aggregate queries against D1 —

```sql
SELECT COUNT(*) FROM polls WHERE deleted_at IS NULL;
SELECT COUNT(*) FROM polls WHERE deleted_at IS NULL AND status = 'finalized';
SELECT answer, COUNT(*) FROM votes GROUP BY answer;
```

— stores the result as the starting totals, and sets `seeded`. The whole seed runs inside the DO's
input gate, so two concurrent first-requests cannot double-seed.

**The seed is a floor, not a true history.** `votes` holds current state, so answers that were
later changed or withdrawn are not in it, and deleted polls are excluded. The seeded totals are
therefore lower than the true lifetime submissions. That is the correct direction to be wrong in —
understating is safe, and everything after the seed is counted exactly.

## §5 Delivery to the browser

**Initial value is server-rendered.** The landing route's loader reads the counters over HTTP from
the DO and renders real numbers into the HTML. No zero-flash, no layout shift, and the section is
fully correct with JavaScript disabled.

**Live updates over a WebSocket, but only when it matters.** The client opens a socket to
`StatsRoom` only once the counter section scrolls into view (`IntersectionObserver`), and closes it
on exit. A visitor who never scrolls past the hero never opens one. Broadcasts are throttled inside
the DO to at most one per `BROADCAST_THROTTLE_MS` (2s), so a burst of votes produces a smooth tick
rather than a flood of frames.

If the socket fails or is closed, the server-rendered numbers simply stay put. There is no polling
fallback and no retry storm — a stale marketing counter is not worth a reconnect loop on the
busiest page in the product.

**A sentence, not a dashboard** (decided 2026-08-27). The headline reads as prose — "whenweall has
settled _340_ dates for people so far." — with the number highlighted in the brand ink, bold, and
counting up live. Polls created and responses recorded sit below it as a quiet supporting line,
followed by the yes / if-need-be / no breakdown. This sidesteps the size problem in §1 entirely:
there is no second large figure to compare against.

Paraglide messages return plain strings, so the number cannot be JSX inside the message. The
sentence interpolates a `\u0000` sentinel and the component splits on it (`splitAroundSlot`),
which keeps the sentence as **one** translatable unit and lets each locale place the number
wherever its grammar wants — a prefix/suffix pair could not. A translation that loses the
placeholder degrades to plain prose rather than throwing.

**Animation.** The number animates from its previous value to the new one rather than snapping,
and respects `prefers-reduced-motion` by swapping to an instant update. It never animates on first
render, so the server-rendered figure does not count up from zero on hydration.

## §6 Low numbers are shown, not hidden

Decided with Anders (2026-08-27): **no threshold.** The real numbers render from the first poll
onwards, however small.

The reasoning is that a small number is not an embarrassment here, it is the message. Early
figures signal early access to the people being invited to test, and being visibly early is a
reason to join rather than a reason to leave. The numbers grow on their own once those testers
start using it, so a threshold would only hide the product's own trajectory during the period it
is most worth showing.

This removes the `STATS_MIN_POLLS` constant and the conditional render the earlier draft proposed.
The section is always present.

## §7 Privacy

The counters are global aggregates with no attribution — no organization, poll, participant or
geographic dimension, and nothing that can be traced to a person. They are safe to expose publicly.

One second-order property is worth naming: a live counter leaks _timing_. Someone watching closely
could infer that a vote was cast somewhere in the world at a given moment. The 10-second flush and
2-second broadcast throttle already blur this to the point of uselessness, and no identity is
attached to it. Noted so the decision is explicit rather than accidental.

## §8 Testing

- **Unit**: counter arithmetic and delta merging; the "below `STATS_MIN_POLLS` renders nothing"
  rule; number-animation component with reduced motion.
- **Workers** (real D1 + DO): seeding from a database with existing polls and votes; seeding
  exactly once under two concurrent first-requests; increments accumulating across a flush;
  broadcast throttling; a socket receiving an update after an increment.
- **E2E**: the landing page renders server-side numbers with JavaScript disabled.

## Phasing

1. **Counters and seeding** — `StatsRoom`, the client helper, increment points, lazy seed, and an
   HTTP read. No UI. Verifiable through tests alone.
2. **Landing section** — server-rendered numbers and the animated display.
3. **Live updates** — WebSocket, intersection-gated connection, throttled broadcast.

Each phase is independently shippable, and phase 1 is useful on its own as a number to check.

## Resolved during review

1. **Threshold** — dropped entirely, see §6. Low numbers are the message, not something to hide.
2. **Single-DO contention** — accepted as a known limit with a documented revisit point, see §2.

## Still open (implemented as specified, cheap to change)

3. **Is "responses recorded" the right framing**, given §1's decision that edits double-count? The
   alternative is counting _participants_ rather than answers, which is smaller but unambiguous.
   Implemented as answers; changing it later is a counter-definition change plus a re-seed.
4. **Should deleting a poll decrement `pollsCreated`?** Implemented as no — the number is a claim
   about lifetime usage rather than current inventory.
