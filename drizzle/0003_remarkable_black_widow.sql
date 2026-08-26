CREATE TABLE `notification_prefs` (
	`user_id` text PRIMARY KEY NOT NULL,
	`channels` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE `notification_subscriptions` (
	`scope_type` text NOT NULL,
	`scope_id` text NOT NULL,
	`user_id` text NOT NULL,
	`source` text NOT NULL,
	`channels` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	PRIMARY KEY(`scope_type`, `scope_id`, `user_id`),
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `notification_subscriptions_scope_idx` ON `notification_subscriptions` (`scope_type`,`scope_id`);--> statement-breakpoint
CREATE TABLE `push_subscriptions` (
	`id` text PRIMARY KEY NOT NULL,
	`user_id` text NOT NULL,
	`endpoint` text NOT NULL,
	`p256dh` text NOT NULL,
	`auth` text NOT NULL,
	`user_agent` text,
	`created_at` text NOT NULL,
	`last_seen_at` text NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `push_subscriptions_endpoint_uidx` ON `push_subscriptions` (`endpoint`);--> statement-breakpoint
CREATE INDEX `push_subscriptions_user_idx` ON `push_subscriptions` (`user_id`);--> statement-breakpoint
--> Backfill (hand-written): every existing poll creator becomes a 'creator' subscriber carrying
--> the intent of the two booleans this migration is about to drop. `response.updated` inherits
--> `notify_on_vote` deliberately — the organiser asked to hear about vote activity, and edits are
--> vote activity that previously had nowhere to go.
INSERT INTO `notification_subscriptions`
  (`scope_type`, `scope_id`, `user_id`, `source`, `channels`, `created_at`, `updated_at`)
SELECT
  'poll',
  p.`id`,
  p.`created_by`,
  'creator',
  json_object(
    'response.created', json_object('email', json(CASE WHEN p.`notify_on_vote` THEN 'true' ELSE 'false' END), 'push', json('false')),
    'response.updated', json_object('email', json(CASE WHEN p.`notify_on_vote` THEN 'true' ELSE 'false' END), 'push', json('false')),
    'comment.created', json_object('email', json(CASE WHEN p.`notify_on_comment` THEN 'true' ELSE 'false' END), 'push', json('false'))
  ),
  p.`created_at`,
  p.`updated_at`
FROM `polls` p
WHERE p.`created_by` IS NOT NULL;--> statement-breakpoint
--> Booking pages get NULL channels (inherit the user's defaults, whose booking events are all
--> on) because organiser notices are unconditional today — defaults reproduce that exactly.
INSERT INTO `notification_subscriptions`
  (`scope_type`, `scope_id`, `user_id`, `source`, `channels`, `created_at`, `updated_at`)
SELECT
  'booking_page',
  bp.`id`,
  COALESCE(bp.`member_user_id`, bp.`created_by`),
  'creator',
  NULL,
  bp.`created_at`,
  bp.`updated_at`
FROM `booking_pages` bp
WHERE COALESCE(bp.`member_user_id`, bp.`created_by`) IS NOT NULL;--> statement-breakpoint
ALTER TABLE `polls` DROP COLUMN `notify_on_vote`;--> statement-breakpoint
ALTER TABLE `polls` DROP COLUMN `notify_on_comment`;