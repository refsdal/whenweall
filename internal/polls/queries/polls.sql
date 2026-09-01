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
