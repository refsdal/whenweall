type D1Migration = import('cloudflare:test').D1Migration

interface Env {
  TEST_MIGRATIONS: D1Migration[]
}
