# Moving a D1 database into the EU jurisdiction

`privacy_processors_body` tells users their data is stored in the EU. A D1 database only
_enforces_ that when it is created with `--jurisdiction eu`; `running_in_region` is where the
primary happens to be scheduled and is not a constraint. **A jurisdiction cannot be added to an
existing database** — it is fixed at creation — so honouring that promise means creating a new
database and moving the data across.

This is what was done on 2026-08-27, kept here because the FK-ordering problem in step 3 is not
obvious and will bite anyone repeating it.

## The databases

| Role                                    | Name                | Jurisdiction |
| --------------------------------------- | ------------------- | ------------ |
| Production (current)                    | `whenweall-eu`      | `eu`         |
| Production (previous, kept as rollback) | `whenweall`         | none         |
| Staging (current)                       | `whenweall-test-eu` | `eu`         |
| Staging (previous)                      | `whenweall-test`    | none         |

The previous databases are deliberately **not deleted**. They are the rollback, and D1 has no
undo.

## Procedure

Rehearse on staging first, in full. Every step below is non-destructive to the source.

### 1. Create the replacement

```bash
bunx wrangler d1 create whenweall-eu --jurisdiction eu
bunx wrangler d1 info whenweall-eu          # confirm: jurisdiction eu
```

A location hint is ignored when a jurisdiction is set, so don't pass both.

### 2. Export schema and data separately

```bash
bunx wrangler d1 export whenweall --remote --no-data   --output prod-schema.sql
bunx wrangler d1 export whenweall --remote --no-schema --output prod-data.sql
```

Not one combined dump. A combined export interleaves `CREATE TABLE` and `INSERT` in roughly
alphabetical order, so `INSERT INTO account` (which references `user`) executes before
`CREATE TABLE user` and the import dies with `no such table: main.user`.

### 3. Reorder the data parents-first

The export orders tables alphabetically, not by dependency. The dump opens with
`PRAGMA defer_foreign_keys=TRUE`, but **that pragma only holds for the transaction it is issued
in** — D1 executes an imported file in batches, so it lapses at the first batch boundary and any
`INSERT` whose parent row does not exist yet fails with:

```
D1 DB was reset and rolled back to its last known good state because the application left
the database in a state where constraints were violated: FOREIGN KEY constraint failed
```

Rewrite the data file parents-first. The order used was:

```
d1_migrations
user, organization, verification          # no FK parents
account, session, passkey                 # -> user
member, invitation                        # -> organization, user
subscription                              # referenceId is an org id, but carries no FK
polls                                     # -> organization, user
poll_options                              # -> polls
participants                              # -> polls, user
votes                                     # -> participants, poll_options
comments                                  # -> polls, participants, user
booking_pages                             # -> organization, user
bookings                                  # -> booking_pages
notification_prefs                        # -> user
notification_subscriptions                # polymorphic scopeId, no FK
push_subscriptions                        # -> user
```

**Drop `sqlite_sequence` from the data file.** SQLite maintains it automatically as the
`AUTOINCREMENT` rows are inserted, and re-inserting it produces a duplicate row for the same table
(`sqlite_sequence` has no unique constraint on `name`). If a duplicate does appear:

```bash
bunx wrangler d1 execute <db> --remote \
  --command "delete from sqlite_sequence where name='d1_migrations'; \
             insert into sqlite_sequence (name, seq) values ('d1_migrations', 4);"
```

### 4. Load, schema first

```bash
bunx wrangler d1 execute whenweall-eu --remote --file prod-schema.sql -y
bunx wrangler d1 execute whenweall-eu --remote --file prod-data-ordered.sql -y
```

`d1_migrations` travels with the export, so `bun run db:migrate:remote` against the new database
is a no-op afterwards. Confirm that rather than assuming it.

### 5. Verify before switching anything

Row counts are not sufficient on their own — diff the actual contents:

```bash
bunx wrangler d1 export whenweall    --remote --output before.sql
bunx wrangler d1 export whenweall-eu --remote --output after.sql
diff <(sort before.sql) <(sort after.sql) && echo IDENTICAL

bunx wrangler d1 execute whenweall-eu --remote --command "pragma foreign_key_check"
```

Expect an empty `foreign_key_check` and an identical diff. Re-run this immediately before the
cutover to catch writes that landed during the window.

### 6. Cut over

Update `database_name` and `database_id` in `wrangler.jsonc` (both the top level and `env.test`,
which inherits nothing) and the three `db:migrate:*` scripts in `package.json`, which resolve the
database by name. Then merge — deploy applies migrations and switches the binding.

Writes to the old database between the final export and the deploy are lost. Production was taking
~37 writes/day when this was done, so the window was accepted rather than scheduled — but it is
real: a single sign-in touches `user.updated_at`, and drift was observed within an hour of the
first copy.

Re-run the copy immediately before merging. To make that repeatable, wipe the target's rows
children-first and reload, rather than recreating the database (which would change the
`database_id` already committed to `wrangler.jsonc`):

```
push_subscriptions, notification_subscriptions, notification_prefs, bookings, booking_pages,
comments, votes, participants, poll_options, polls, subscription, invitation, member, passkey,
session, account, verification, organization, user, d1_migrations
```

That is the load order from step 3 reversed. Then reload the reordered data file and re-run the
verification in step 5. The whole cycle takes under a minute at this data size.

### 7. Afterwards

`.github/workflows/deploy.yml` asserts the production database still reports `jurisdiction: eu`
before every deploy, so a database swapped for an unrestricted one fails the deploy rather than
silently breaking the privacy policy.

Keep the old databases for at least a week. Delete them only once the new ones have taken real
traffic.
