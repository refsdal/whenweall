-- name: GetPoll :one
SELECT * FROM polls WHERE id = $1 AND deleted_at IS NULL;

-- name: InsertPoll :exec
INSERT INTO polls (
  id, organization_id, created_by, type, title, description, location, timezone,
  status, deadline_at, require_participant_email, allow_comments, allow_if_need_be,
  signup_max_claims, created_at, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
);

-- name: ListOptionsByPoll :many
SELECT * FROM poll_options WHERE poll_id = $1 ORDER BY position;

-- name: InsertPollOption :exec
INSERT INTO poll_options (
  id, poll_id, position, kind, start_at, end_at, label, capacity
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8
);

-- name: ListPollsByOrg :many
SELECT * FROM polls WHERE organization_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC;

-- name: SetPollStatus :exec
UPDATE polls SET status = $2, updated_at = $3 WHERE id = $1;

-- name: FinalizePoll :exec
UPDATE polls SET finalized_option_id = $2, status = $3, updated_at = $4 WHERE id = $1;

-- name: SoftDeletePoll :exec
UPDATE polls SET deleted_at = $2, updated_at = $2 WHERE id = $1;

-- name: ListParticipantsByPoll :many
SELECT * FROM participants WHERE poll_id = $1 ORDER BY created_at;

-- name: InsertParticipant :exec
INSERT INTO participants (
  id, poll_id, name, email, user_id, edit_token_hash, locale, created_at, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9
);

-- name: ListVotesByPoll :many
SELECT v.* FROM votes v
JOIN participants p ON p.id = v.participant_id
WHERE p.poll_id = $1;

-- name: UpsertVote :exec
INSERT INTO votes (participant_id, option_id, answer)
VALUES ($1, $2, $3)
ON CONFLICT (participant_id, option_id) DO UPDATE SET answer = EXCLUDED.answer;

-- name: ListCommentsByPoll :many
SELECT * FROM comments WHERE poll_id = $1 AND deleted_at IS NULL ORDER BY created_at;

-- name: InsertComment :exec
INSERT INTO comments (
  id, poll_id, author_name, participant_id, user_id, body, created_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7
);

-- name: UpdatePollScalars :exec
UPDATE polls SET
  title = $2,
  description = $3,
  location = $4,
  timezone = $5,
  deadline_at = $6,
  require_participant_email = $7,
  allow_comments = $8,
  allow_if_need_be = $9,
  signup_max_claims = $10,
  updated_at = $11
WHERE id = $1;

-- name: UpdatePollOption :exec
UPDATE poll_options SET
  position = $2,
  kind = $3,
  start_at = $4,
  end_at = $5,
  label = $6,
  capacity = $7
WHERE id = $1;

-- name: DeletePollOption :exec
DELETE FROM poll_options WHERE id = $1;

-- name: GetOrganizationName :one
SELECT name FROM organizations WHERE id = $1;

-- Task 3 (participants/votes/comments/claims) queries below.

-- name: GetParticipant :one
SELECT * FROM participants WHERE id = $1;

-- name: GetParticipantByPollAndUser :one
SELECT * FROM participants WHERE poll_id = $1 AND user_id = $2;

-- name: UpdateParticipantName :exec
UPDATE participants SET name = $2, updated_at = $3 WHERE id = $1;

-- name: DeleteParticipant :exec
DELETE FROM participants WHERE id = $1;

-- name: ListVotesByParticipant :many
SELECT * FROM votes WHERE participant_id = $1;

-- name: DeleteVote :exec
DELETE FROM votes WHERE participant_id = $1 AND option_id = $2;

-- name: DeleteVotesByParticipant :exec
DELETE FROM votes WHERE participant_id = $1;

-- name: GetPollOptionForUpdate :one
-- Locks the option row for the duration of the enclosing transaction — THE atomicity primitive
-- Claim relies on (see claims.go's doc comment): every concurrent claimant on the same option
-- blocks here until the transaction holding the lock commits or rolls back, so the capacity count
-- taken after this line and the vote inserted before commit can never race with another claimant's
-- count-then-insert on the same option.
SELECT * FROM poll_options WHERE id = $1 FOR UPDATE;

-- name: GetParticipantForUpdate :one
-- Locks the participant row for the duration of the enclosing transaction — Claim's second
-- atomicity primitive, protecting a participant's own signupMaxClaims cap (see claims.go's Claim
-- doc comment on lock ordering: option row first, then participant row, always in that order,
-- everywhere): the SAME participant claiming two DIFFERENT options concurrently would otherwise
-- each lock a different option row (no conflict there) and both read the same pre-claim count of
-- existing votes, both pass the maxClaims check, and both insert — exceeding the cap. Locking the
-- participant row before that count serializes concurrent claims by the same participant,
-- regardless of which option each one targets.
SELECT * FROM participants WHERE id = $1 FOR UPDATE;

-- name: CountYesVotesForOption :one
SELECT count(*) FROM votes WHERE option_id = $1 AND answer = 'yes';

-- name: GetComment :one
SELECT * FROM comments WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteComment :exec
UPDATE comments SET deleted_at = $3 WHERE id = $1 AND poll_id = $2;

-- name: MemberHasManagingRole :one
-- Ports canManageContent's role half (org-roles.ts): does userId hold an 'owner' or 'admin' role
-- in organizationId? (The creator-manages-their-own-content half is checked separately by the
-- caller against polls.created_by — this query only ever answers the role question.)
SELECT EXISTS (
  SELECT 1 FROM organization_members om
  JOIN organization_member_roles omr ON omr.member_id = om.id
  WHERE om.organization_id = $1 AND om.user_id = $2 AND omr.role IN ('owner', 'admin')
) AS has_role;

-- Task 4 (notifications/finalize+claim mail/deadline+digest timers) queries below.

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: IsOrgMember :one
-- Ports the membership-is-the-authority rule (recipients.ts): does userId still belong to
-- organizationId at all, regardless of role? A subscription row surviving past someone leaving
-- the org must not keep mailing them.
SELECT EXISTS (
  SELECT 1 FROM organization_members WHERE organization_id = $1 AND user_id = $2
) AS is_member;

-- name: GetNotificationPref :one
SELECT * FROM notification_prefs WHERE user_id = $1;

-- name: ListSubscriptionsByScope :many
SELECT * FROM notification_subscriptions WHERE scope_type = $1 AND scope_id = $2;

-- name: GetSubscription :one
-- One viewer's own subscription row for one scope (buildView's per-viewer notifications block —
-- getPollView, service.ts) — distinct from ListSubscriptionsByScope, which lists every
-- subscriber and is used for mail fan-out (resolveRecipients), not a single viewer's own row.
SELECT * FROM notification_subscriptions WHERE scope_type = $1 AND scope_id = $2 AND user_id = $3;

-- name: UpsertNotificationSubscription :exec
-- Ports subscriptions.ts's upsert (ensureCreatorSubscription/followScope): a conflict on the
-- (scope_type, scope_id, user_id) PK is a no-op, so re-following never resets an override the
-- user already tuned.
INSERT INTO notification_subscriptions (scope_type, scope_id, user_id, source, channels, created_at, updated_at)
VALUES ($1, $2, $3, $4, NULL, $5, $5)
ON CONFLICT (scope_type, scope_id, user_id) DO NOTHING;

-- name: DeleteNotificationSubscription :exec
DELETE FROM notification_subscriptions WHERE scope_type = $1 AND scope_id = $2 AND user_id = $3;

-- name: SetNotificationSubscriptionChannels :exec
-- $4 is NULL to clear an override back to the user's defaults (setScopeChannels's own doc
-- comment in subscriptions.ts).
UPDATE notification_subscriptions SET channels = $4::jsonb, updated_at = $5
WHERE scope_type = $1 AND scope_id = $2 AND user_id = $3;
