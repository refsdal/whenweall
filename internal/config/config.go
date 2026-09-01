// Package config is the single source of application configuration.
//
// This replaces two separate Cloudflare mechanisms that used to feed `env`: the non-secret `vars`
// block in `wrangler.jsonc` and the secrets in `.dev.vars`. Splitting them was a deployment
// detail of Workers, and it was invisible until something read a var that only one of the two
// sources defined — so both now arrive the same way, as process environment, and are validated
// once at boot rather than trusted at each use site.
//
// Boot fails loudly on invalid config. A container that starts and then 500s on the first request
// that happens to touch a missing var is strictly worse than one that refuses to start.
package config

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Capabilities records which optional integrations are actually wired up.
//
// Each is all-or-nothing on purpose: a half-configured integration (a Turnstile site key with no
// secret) fails at verification time with an error that points nowhere near the missing var, so
// a partial pair is treated as "off" and warned about at boot instead.
type Capabilities struct {
	Turnstile bool
	Google    bool
	OIDC      bool
}

// Config is the fully validated, defaulted application configuration.
type Config struct {
	AppEnv           string // development | test | production
	AppURL           string // absolute http(s) URL, no trailing slash
	Port             int    // default 3000
	DatabaseURL      string
	DatabasePoolSize int    // default 10, max 100
	AuthSecret       string // >= 32 chars
	LimenSecret      []byte // sha256(AuthSecret) — exactly 32 bytes, Limen requires this
	SMTPHost         string // required
	SMTPPort         int    // default 587
	SMTPUser         string
	SMTPPassword     string
	SMTPSecure       bool
	EmailFrom        string // default "whenweall <no-reply@localhost>"

	TurnstileSiteKey   string
	TurnstileSecretKey string

	GoogleClientID     string
	GoogleClientSecret string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // default "sso"

	EnableTestRoutes bool
	TrustProxy       bool // default true
	MigrateOnBoot    bool // default true

	Capabilities Capabilities
	IsProduction bool
}

// Load parses and validates env into a Config. warnings are human-readable lines the caller
// should log; err is non-nil (and cfg nil) when the configuration is invalid.
func Load(env map[string]string) (*Config, []string, error) {
	var errs, warnings []string

	get := func(k string) string { return strings.TrimSpace(env[k]) }

	// Present-and-non-empty, or absent. Rejects `FOO=` (set-but-blank), which is almost never
	// intentional and otherwise reads as "configured" to a truthiness check.
	optionalNonEmpty := func(k string) string { return get(k) }

	boolEnv := func(k string, def bool) bool {
		v := get(k)
		if v == "" {
			return def
		}
		return strings.EqualFold(v, "true") || v == "1"
	}

	intEnv := func(k string, def int) int {
		v := get(k)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Sprintf("%s must be a positive integer", k))
			return def
		}
		return n
	}

	cfg := &Config{
		AppEnv:           get("APP_ENV"),
		Port:             intEnv("PORT", 3000),
		DatabasePoolSize: 10,
		SMTPPort:         intEnv("SMTP_PORT", 587),
		SMTPUser:         optionalNonEmpty("SMTP_USER"),
		SMTPPassword:     optionalNonEmpty("SMTP_PASSWORD"),
		SMTPSecure:       boolEnv("SMTP_SECURE", false),
		EmailFrom:        get("EMAIL_FROM"),

		TurnstileSiteKey:   optionalNonEmpty("TURNSTILE_SITE_KEY"),
		TurnstileSecretKey: optionalNonEmpty("TURNSTILE_SECRET_KEY"),

		GoogleClientID:     optionalNonEmpty("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: optionalNonEmpty("GOOGLE_CLIENT_SECRET"),

		OIDCIssuer:       optionalNonEmpty("OIDC_ISSUER"),
		OIDCClientID:     optionalNonEmpty("OIDC_CLIENT_ID"),
		OIDCClientSecret: optionalNonEmpty("OIDC_CLIENT_SECRET"),
		OIDCName:         get("OIDC_NAME"),

		EnableTestRoutes: boolEnv("ENABLE_TEST_ROUTES", false),
		TrustProxy:       boolEnv("TRUST_PROXY", true),
		MigrateOnBoot:    boolEnv("MIGRATE_ON_BOOT", true),
	}

	if cfg.AppEnv == "" {
		cfg.AppEnv = "development"
	} else if cfg.AppEnv != "development" && cfg.AppEnv != "test" && cfg.AppEnv != "production" {
		errs = append(errs, "APP_ENV must be one of development, test, production")
	}
	if cfg.EmailFrom == "" {
		cfg.EmailFrom = "whenweall <no-reply@localhost>"
	}
	if cfg.OIDCName == "" {
		cfg.OIDCName = "sso"
	}
	if v := get("DATABASE_POOL_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 100 {
			errs = append(errs, "DATABASE_POOL_SIZE must be a positive integer no greater than 100")
		} else {
			cfg.DatabasePoolSize = n
		}
	}

	// --- APP_URL: required, absolute http(s), no trailing slash ---------------------------
	rawAppURL := get("APP_URL")
	if rawAppURL == "" {
		errs = append(errs, "APP_URL is required, e.g. https://whenweall.example")
	} else if u, err := url.Parse(rawAppURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, "APP_URL must be an absolute http(s) URL, e.g. https://whenweall.example")
	} else {
		cfg.AppURL = strings.TrimSuffix(rawAppURL, "/")
	}

	// --- DATABASE_URL: required, non-empty -------------------------------------------------
	cfg.DatabaseURL = get("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required (postgres://…)")
	}

	// --- AUTH_SECRET: required, >= 32 chars -------------------------------------------------
	cfg.AuthSecret = get("AUTH_SECRET")
	if len(cfg.AuthSecret) < 32 {
		errs = append(errs, "AUTH_SECRET is required and must be at least 32 characters — generate one with `openssl rand -base64 32`")
	} else {
		sum := sha256.Sum256([]byte(cfg.AuthSecret))
		cfg.LimenSecret = sum[:]
	}

	// --- SMTP_HOST: required ----------------------------------------------------------------
	cfg.SMTPHost = get("SMTP_HOST")
	if cfg.SMTPHost == "" {
		errs = append(errs, "SMTP_HOST is required — whenweall cannot function without e-mail")
	}

	// --- capabilities: half-configured pairs are off, with a warning ----------------------
	// Mirrors deriveCapabilities() in src/config.ts.
	pair := func(name, a, b string) bool {
		if a != "" && b != "" {
			return true
		}
		if a != "" || b != "" {
			warnings = append(warnings, fmt.Sprintf("%s is only half-configured — both values are required, so it stays disabled.", name))
		}
		return false
	}

	cfg.Capabilities.Turnstile = pair("Turnstile", cfg.TurnstileSiteKey, cfg.TurnstileSecretKey)
	cfg.Capabilities.Google = pair("Google sign-in", cfg.GoogleClientID, cfg.GoogleClientSecret)

	// OIDC needs a triple, not a pair: issuer, client id and client secret must all be set.
	oidcParts := []string{cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret}
	oidcSet := 0
	for _, p := range oidcParts {
		if p != "" {
			oidcSet++
		}
	}
	cfg.Capabilities.OIDC = oidcSet == len(oidcParts)
	if oidcSet > 0 && !cfg.Capabilities.OIDC {
		warnings = append(warnings, "OIDC is only partially configured — sign-in needs OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET, so it stays disabled.")
	}

	cfg.IsProduction = cfg.AppEnv == "production"

	// Disabling the captcha is a real reduction in abuse protection on public, unauthenticated
	// endpoints (poll creation, voting). Fine for a private instance, worth saying out loud for
	// one on the open internet — so it is a warning in production and silent elsewhere.
	if !cfg.Capabilities.Turnstile && cfg.IsProduction {
		warnings = append(warnings, "Turnstile is not configured — captcha protection is OFF on public endpoints.")
	}

	if cfg.EnableTestRoutes && cfg.IsProduction {
		errs = append(errs, "ENABLE_TEST_ROUTES must not be set when APP_ENV=production — the seed routes it exposes would let anyone reset your data.")
	}

	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return cfg, warnings, nil
}

// FromOS loads configuration from the process environment.
func FromOS() (*Config, []string, error) {
	env := make(map[string]string, len(os.Environ()))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return Load(env)
}
