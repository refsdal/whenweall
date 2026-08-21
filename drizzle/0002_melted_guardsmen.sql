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
CREATE UNIQUE INDEX `booking_pages_owner_slug_uidx` ON `booking_pages` (`owner_id`,`slug`);--> statement-breakpoint
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
-- collide on NULL; drizzle-kit's own schema diffing does not model this table (it lives in
-- auth-schema.ts, generated separately), so it will not attempt to drop this index later.
CREATE UNIQUE INDEX `user_handle_unique` ON `user` (`handle`) WHERE `handle` IS NOT NULL;