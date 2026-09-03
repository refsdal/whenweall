-- +goose Up

-- Per-user application preferences. Ours, not Limen's — same reasoning as staff_users and
-- locked_users (migrations/00002_auth.sql, 00007_admin_locks.sql): extending Limen's generated
-- users table would couple our profile fields to the auth library's schema generator. Keyed on
-- Limen's bigint user id; ON DELETE CASCADE so internal/auth.CascadeDeleteUser never has to
-- know this table exists.
--
-- locale is the user's preferred UI/mail locale ("en" or "nb" — internal/mailer.SupportedLocales
-- is the authoritative list; the application validates before writing, so no CHECK here that
-- would need a migration every time a locale is added). Written at signup (from the request's
-- `locale` body field, the whenweall_locale cookie, or Accept-Language) and by PATCH /api/v1/me.
CREATE TABLE user_preferences (
  user_id    bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  locale     text NOT NULL DEFAULT 'en',
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS user_preferences;
