CREATE TABLE `booking_pages` (
	`id` text PRIMARY KEY NOT NULL,
	`owner_id` text NOT NULL,
	`slug` text NOT NULL,
	`title` text NOT NULL,
	`description` text,
	`location` text,
	`timezone` text NOT NULL,
	`slot_duration_min` integer NOT NULL,
	`buffer_before_min` integer NOT NULL,
	`buffer_after_min` integer NOT NULL,
	`min_notice_min` integer NOT NULL,
	`max_days_ahead` integer NOT NULL,
	`availability` text NOT NULL,
	`date_overrides` text,
	`google_sync` integer DEFAULT false NOT NULL,
	`reminders` integer DEFAULT true NOT NULL,
	`status` text DEFAULT 'active' NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	`deleted_at` text,
	FOREIGN KEY (`owner_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `booking_pages_owner_slug_uidx` ON `booking_pages` (`owner_id`,`slug`) WHERE "booking_pages"."deleted_at" IS NULL;--> statement-breakpoint
CREATE TABLE `bookings` (
	`id` text PRIMARY KEY NOT NULL,
	`page_id` text NOT NULL,
	`start_at` text NOT NULL,
	`end_at` text NOT NULL,
	`visitor_name` text NOT NULL,
	`visitor_email` text NOT NULL,
	`visitor_note` text,
	`visitor_locale` text,
	`visitor_timezone` text NOT NULL,
	`status` text DEFAULT 'confirmed' NOT NULL,
	`cancelled_by` text,
	`manage_token_hash` text NOT NULL,
	`google_event_id` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`page_id`) REFERENCES `booking_pages`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `bookings_page_start_idx` ON `bookings` (`page_id`,`start_at`);--> statement-breakpoint
ALTER TABLE `user` ADD `handle` text;--> statement-breakpoint
-- Hand-written: Better-Auth's `auth generate` emits the `handle` column above but has no way to
-- express a unique index on a Better-Auth additionalField, so this index is added by hand rather
-- than generated. Partial (WHERE handle IS NOT NULL) so multiple users without a handle yet don't
-- collide on NULL. drizzle-kit *does* model the `user` table (auth-schema.ts is re-exported by
-- schema.ts, drizzle.config.ts's schema entrypoint), but this index isn't declared on the `handle`
-- column there (no `.unique()` / index builder), so drizzle-kit's diffing doesn't see it and won't
-- try to drop it on the next `db:generate` — that stays true only as long as `handle` stays
-- undeclared in auth-schema.ts. If a future `bun run auth:generate` (or a hand-edit) adds
-- `.unique()` to `handle`, drizzle-kit will start modeling its own unique constraint/index for the
-- column, which would collide or duplicate with this hand-written partial index; reconcile the two
-- (likely by dropping this one) rather than letting both stand.
CREATE UNIQUE INDEX `user_handle_unique` ON `user` (`handle`) WHERE `handle` IS NOT NULL;