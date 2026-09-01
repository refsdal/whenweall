-- +goose Up

-- Domain schema for polls (scheduling polls, sign-up sheets, RSVP), transcribed from
-- drizzle/0000_orange_zzzax.sql with the spec §6 re-cut applied: every text-typed timestamp
-- column (created_at/updated_at/deleted_at/deadline_at/start_at/end_at) becomes timestamptz,
-- and channels stays jsonb. push_subscriptions and the auth/billing tables are dropped —
-- Better-Auth is gone, Limen owns auth (migrations/00002_auth.sql).
--
-- Ruling: user/org FK columns (organization_id, created_by, user_id) are bigint, referencing
-- Limen's users/organizations (migrations/00002_auth.sql — both BIGSERIAL/BIGINT). Domain entity
-- ids (polls.id, poll_options.id, participants.id, comments.id) stay text nanoids, unchanged from
-- drizzle.

CREATE TABLE polls (
  id text PRIMARY KEY NOT NULL,
  organization_id bigint NOT NULL,
  created_by bigint,
  type text NOT NULL,
  title text NOT NULL,
  description text,
  location text,
  timezone text NOT NULL,
  status text NOT NULL DEFAULT 'open',
  deadline_at timestamptz,
  finalized_option_id text,
  require_participant_email boolean NOT NULL DEFAULT false,
  allow_comments boolean NOT NULL DEFAULT true,
  allow_if_need_be boolean NOT NULL DEFAULT true,
  signup_max_claims integer NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz,
  CONSTRAINT fk_polls_organization_id FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE ON UPDATE NO ACTION,
  CONSTRAINT fk_polls_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION
);
CREATE INDEX polls_org_created_idx ON polls (organization_id, created_at);

CREATE TABLE poll_options (
  id text PRIMARY KEY NOT NULL,
  poll_id text NOT NULL,
  position integer NOT NULL,
  kind text NOT NULL,
  start_at timestamptz,
  end_at timestamptz,
  label text,
  capacity integer,
  CONSTRAINT fk_poll_options_poll_id FOREIGN KEY (poll_id) REFERENCES polls (id) ON DELETE CASCADE ON UPDATE NO ACTION
);
CREATE INDEX poll_options_poll_position_idx ON poll_options (poll_id, position);

CREATE TABLE participants (
  id text PRIMARY KEY NOT NULL,
  poll_id text NOT NULL,
  name text NOT NULL,
  email text,
  user_id bigint,
  edit_token_hash text,
  locale text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CONSTRAINT fk_participants_poll_id FOREIGN KEY (poll_id) REFERENCES polls (id) ON DELETE CASCADE ON UPDATE NO ACTION,
  CONSTRAINT fk_participants_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION
);
CREATE INDEX participants_poll_idx ON participants (poll_id);

CREATE TABLE votes (
  participant_id text NOT NULL,
  option_id text NOT NULL,
  answer text NOT NULL,
  PRIMARY KEY (participant_id, option_id),
  CONSTRAINT fk_votes_participant_id FOREIGN KEY (participant_id) REFERENCES participants (id) ON DELETE CASCADE ON UPDATE NO ACTION,
  CONSTRAINT fk_votes_option_id FOREIGN KEY (option_id) REFERENCES poll_options (id) ON DELETE CASCADE ON UPDATE NO ACTION
);

CREATE TABLE comments (
  id text PRIMARY KEY NOT NULL,
  poll_id text NOT NULL,
  author_name text NOT NULL,
  participant_id text,
  user_id bigint,
  body text NOT NULL,
  created_at timestamptz NOT NULL,
  deleted_at timestamptz,
  CONSTRAINT fk_comments_poll_id FOREIGN KEY (poll_id) REFERENCES polls (id) ON DELETE CASCADE ON UPDATE NO ACTION,
  CONSTRAINT fk_comments_participant_id FOREIGN KEY (participant_id) REFERENCES participants (id) ON DELETE SET NULL ON UPDATE NO ACTION,
  CONSTRAINT fk_comments_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION
);
CREATE INDEX comments_poll_created_idx ON comments (poll_id, created_at);

CREATE TABLE notification_prefs (
  user_id bigint PRIMARY KEY NOT NULL,
  channels jsonb,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CONSTRAINT fk_notification_prefs_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE ON UPDATE NO ACTION
);

CREATE TABLE notification_subscriptions (
  scope_type text NOT NULL,
  scope_id text NOT NULL,
  user_id bigint NOT NULL,
  source text NOT NULL,
  channels jsonb,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (scope_type, scope_id, user_id),
  CONSTRAINT fk_notification_subscriptions_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE ON UPDATE NO ACTION
);
CREATE INDEX notification_subscriptions_scope_idx ON notification_subscriptions (scope_type, scope_id);

-- +goose Down
DROP TABLE notification_subscriptions;
DROP TABLE notification_prefs;
DROP TABLE comments;
DROP TABLE votes;
DROP TABLE participants;
DROP TABLE poll_options;
DROP TABLE polls;
