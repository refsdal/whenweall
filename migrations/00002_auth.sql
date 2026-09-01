-- +goose Up

-- Everything below down to (not including) staff_users is Limen's own schema — generated, not
-- hand-written. See docs/limen-migrations.md for how to reproduce or update it: run
-- `go run ./internal/auth/schemagen` against a throwaway Postgres to make Limen write
-- .limen/schemas.json, then Limen's own CLI
-- (`go run github.com/thecodearcher/limen/cmd/limen@<pinned-sha> generate migrations`) to
-- introspect that database and emit the CREATE TABLEs below. Do not hand-edit the generated
-- section — regenerate it instead, and diff.
--
-- One deliberate hand-edit to the generated CREATE TABLEs: `IF NOT EXISTS` has been dropped from
-- all of them (Limen's CLI emits it on every one, but none of the CREATE INDEX statements below
-- get the same treatment) so this file is internally consistent — goose runs each migration
-- exactly once, so neither needs it. If you regenerate this section, drop `IF NOT EXISTS` from
-- the CREATE TABLEs again rather than adding it to every index.
--
-- users.id (and every other table's *_id column below) is BIGSERIAL/BIGINT: Limen's default
-- schema config uses an auto-increment id generator when Config.Schema.IDGenerator is left unset
-- (see limen's schema_config.go GetIDColumnType), and internal/auth.buildLimenConfig doesn't
-- override it. The internal/auth seam normalizes every id to a Go string regardless
-- (fmt.Sprint(user.ID)), so nothing outside this migration and internal/auth needs to know that.
CREATE TABLE users (
  id BIGSERIAL,
  email VARCHAR(255) NOT NULL,
  password VARCHAR(255),
  email_verified_at TIMESTAMPTZ,
  first_name VARCHAR(255),
  last_name VARCHAR(255),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL,
  two_factor_enabled BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_users_email ON users (email);

CREATE TABLE accounts (
  id BIGSERIAL,
  user_id BIGINT NOT NULL,
  provider VARCHAR(255) NOT NULL,
  provider_account_id VARCHAR(255),
  access_token TEXT NOT NULL,
  refresh_token TEXT,
  access_token_expires_at TIMESTAMPTZ,
  scope VARCHAR(255) NOT NULL,
  id_token TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (id),
  CONSTRAINT fk_accounts_users_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT ON UPDATE CASCADE
);
CREATE INDEX idx_accounts_user_id_provider ON accounts (user_id);
CREATE UNIQUE INDEX idx_accounts_provider_provider_account_id ON accounts (provider, provider_account_id);

CREATE TABLE organizations (
  id BIGSERIAL,
  name VARCHAR(255) NOT NULL,
  slug VARCHAR(255) NOT NULL,
  logo VARCHAR(255),
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_organizations_slug ON organizations (slug);

CREATE TABLE organization_invitations (
  id BIGSERIAL,
  organization_id BIGINT NOT NULL,
  inviter_id BIGINT,
  email VARCHAR(255) NOT NULL,
  roles TEXT,
  status VARCHAR(255) NOT NULL,
  token TEXT NOT NULL,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT fk_organization_invitations_organization FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT fk_organization_invitations_inviter FOREIGN KEY (inviter_id) REFERENCES users (id) ON DELETE SET NULL ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_organization_invitations_token ON organization_invitations (token);
CREATE INDEX idx_organization_invitations_org ON organization_invitations (organization_id);
CREATE INDEX idx_organization_invitations_email ON organization_invitations (email);

CREATE TABLE organization_members (
  id BIGSERIAL,
  organization_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT fk_organization_members_organization FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT fk_organization_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_organization_members_org_user ON organization_members (organization_id, user_id);

CREATE TABLE organization_member_roles (
  id BIGSERIAL,
  member_id BIGINT NOT NULL,
  organization_id BIGINT NOT NULL,
  role VARCHAR(255),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT fk_organization_member_roles_member FOREIGN KEY (member_id) REFERENCES organization_members (id) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT fk_organization_member_roles_organization FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_organization_member_roles_member_role ON organization_member_roles (member_id, role);

-- Named limen_rate_limits, not rate_limits: Limen's core schema set always includes a
-- "rate_limits" table (its own HTTP rate limiter's optional database-backed store), which
-- collides with our pre-existing rate_limits table from migrations/00001_infra.sql (different
-- columns, different purpose — see internal/auth/auth.go's buildLimenConfig for the full
-- explanation and confirmation that Limen's default rate limiter config uses an in-memory store,
-- so this table is currently unused at runtime; it exists only because Limen's schema discovery
-- includes it unconditionally). Renamed via limen.WithRateLimitTableName rather than left to
-- collide.
CREATE TABLE limen_rate_limits (
  id BIGSERIAL,
  key VARCHAR(255) NOT NULL,
  count INTEGER NOT NULL,
  last_request_at BIGINT NOT NULL,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_rate_limits_key ON limen_rate_limits (key);

CREATE TABLE sessions (
  id BIGSERIAL,
  token VARCHAR(255) NOT NULL,
  user_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMPTZ NOT NULL,
  last_access TIMESTAMPTZ NOT NULL,
  metadata JSONB,
  active_organization_id BIGINT,
  PRIMARY KEY (id),
  CONSTRAINT fk_sessions_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_sessions_token ON sessions (token);
CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_active_organization ON sessions (active_organization_id);

CREATE TABLE two_factors (
  id BIGSERIAL,
  user_id BIGINT NOT NULL,
  secret VARCHAR(255),
  backup_codes TEXT,
  PRIMARY KEY (id),
  CONSTRAINT fk_two_factors_users_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_two_factors_user_id ON two_factors (user_id);

CREATE TABLE verifications (
  id BIGSERIAL,
  subject VARCHAR(255) NOT NULL,
  value TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_verifications_value ON verifications (value);
CREATE INDEX idx_verifications_subject ON verifications (subject);

-- Platform staff. Ours, not Limen's: extending Limen's user schema would couple the admin
-- console to the auth library — a flag table does not. user_id is bigint (not text) to match
-- users.id above, since Limen's default schema uses an auto-increment id generator.
CREATE TABLE staff_users (
  user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS staff_users;
DROP TABLE IF EXISTS verifications;
DROP TABLE IF EXISTS two_factors;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS limen_rate_limits;
DROP TABLE IF EXISTS organization_member_roles;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organization_invitations;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS users;
