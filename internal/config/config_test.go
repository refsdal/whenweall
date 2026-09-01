package config

import (
	"strings"
	"testing"
)

func valid() map[string]string {
	return map[string]string{
		"APP_URL":      "https://whenweall.example",
		"DATABASE_URL": "postgres://x@localhost/x",
		"AUTH_SECRET":  strings.Repeat("s", 32),
		"SMTP_HOST":    "smtp.example.com",
	}
}

func TestLoadMinimalValid(t *testing.T) {
	cfg, warnings, err := Load(valid())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want 3000", cfg.Port)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTPPort = %d, want 587", cfg.SMTPPort)
	}
	if !cfg.TrustProxy || !cfg.MigrateOnBoot {
		t.Error("TrustProxy/MigrateOnBoot should default true")
	}
	if len(cfg.LimenSecret) != 32 {
		t.Errorf("LimenSecret len = %d, want 32", len(cfg.LimenSecret))
	}
	if cfg.Capabilities.Google || cfg.Capabilities.Turnstile || cfg.Capabilities.OIDC {
		t.Error("no optional capability should be on")
	}
	_ = warnings
}

func TestLoadCollectsAllErrors(t *testing.T) {
	_, _, err := Load(map[string]string{})
	if err == nil {
		t.Fatal("want error")
	}
	for _, needle := range []string{"APP_URL", "DATABASE_URL", "AUTH_SECRET", "SMTP_HOST"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("error should mention %s; got %q", needle, err.Error())
		}
	}
}

func TestHalfConfiguredPairIsOffWithWarning(t *testing.T) {
	env := valid()
	env["GOOGLE_CLIENT_ID"] = "id-only"
	cfg, warnings, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capabilities.Google {
		t.Error("google must stay off")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Google") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a Google warning, got %v", warnings)
	}
}

func TestSetButBlankOptionalIsUnset(t *testing.T) {
	env := valid()
	env["GOOGLE_CLIENT_ID"] = ""
	env["GOOGLE_CLIENT_SECRET"] = ""
	cfg, warnings, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capabilities.Google {
		t.Error("blank vars must not enable google")
	}
	if len(warnings) != 0 {
		t.Errorf("blank pair should not warn: %v", warnings)
	}
}

func TestTestRoutesForbiddenInProduction(t *testing.T) {
	env := valid()
	env["APP_ENV"] = "production"
	env["ENABLE_TEST_ROUTES"] = "true"
	if _, _, err := Load(env); err == nil {
		t.Fatal("want error")
	}
}

func TestAppURLTrailingSlashStripped(t *testing.T) {
	env := valid()
	env["APP_URL"] = "https://whenweall.example/"
	cfg, _, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppURL != "https://whenweall.example" {
		t.Errorf("AppURL = %q", cfg.AppURL)
	}
}

func TestShortAuthSecretRejected(t *testing.T) {
	env := valid()
	env["AUTH_SECRET"] = "short"
	if _, _, err := Load(env); err == nil {
		t.Fatal("want error")
	}
}

func TestOIDCNeedsAllThree(t *testing.T) {
	env := valid()
	env["OIDC_ISSUER"] = "https://id.example.com"
	env["OIDC_CLIENT_ID"] = "abc"
	cfg, _, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capabilities.OIDC {
		t.Error("OIDC must stay off without client secret")
	}
	env["OIDC_CLIENT_SECRET"] = "xyz"
	cfg, _, err = Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Capabilities.OIDC {
		t.Error("OIDC should be on")
	}
	if cfg.OIDCName != "sso" {
		t.Errorf("OIDCName default = %q, want sso", cfg.OIDCName)
	}
}

func TestMigrateOnBootEnvOverride(t *testing.T) {
	env := valid()
	env["MIGRATE_ON_BOOT"] = "false"
	cfg, _, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MigrateOnBoot {
		t.Error("MigrateOnBoot should be false when MIGRATE_ON_BOOT=false")
	}

	cfg, _, err = Load(valid())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MigrateOnBoot {
		t.Error("MigrateOnBoot should default true when MIGRATE_ON_BOOT is unset")
	}
}

func TestAppEnvMustBeValidEnum(t *testing.T) {
	env := valid()
	env["APP_ENV"] = "prod"
	_, _, err := Load(env)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "APP_ENV") {
		t.Errorf("error should mention APP_ENV; got %q", err.Error())
	}
}

func TestAppURLRequiresHost(t *testing.T) {
	for _, bad := range []string{"https://", "https:example.com"} {
		env := valid()
		env["APP_URL"] = bad
		_, _, err := Load(env)
		if err == nil {
			t.Fatalf("APP_URL=%q: want error", bad)
		}
		if !strings.Contains(err.Error(), "APP_URL") {
			t.Errorf("APP_URL=%q: error should mention APP_URL; got %q", bad, err.Error())
		}
	}
}
