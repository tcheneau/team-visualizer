package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) (path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "teamviz.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFullConfig(t *testing.T) {
	t.Setenv("TEAMVIZ_JWT_SECRET", "s3cret")
	t.Setenv("KC_CORP_SECRET", "corp-secret")

	path := writeTemp(t, `
[server]
db_path    = "/srv/teamviz/data.db"
jwt_secret = "${TEAMVIZ_JWT_SECRET}"
jwt_ttl    = "12h"
ws_enabled = false

[roles]
admin    = "admins"
normal   = "staff"
readonly = "viewers"

[[listener]]
name    = "corp"
listen  = ":8080"
auth    = "oidc"

  [listener.oidc]
  issuer        = "https://kc.corp/realm"
  client_id     = "teamviz"
  client_secret = "${KC_CORP_SECRET}"
  redirect_url  = "https://tvz.corp/auth/callback"
  ca_file       = "/certs/root.pem"

[[listener]]
name    = "kerberos"
listen  = "127.0.0.1:8082"
auth    = "headers"

  [listener.headers]
  user   = "X-Remote-User"
  groups = "X-Remote-Groups"

  [listener.roles]
  admin = "server-admins"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// server
	if cfg.Server.DBPath != "/srv/teamviz/data.db" {
		t.Errorf("db_path = %q", cfg.Server.DBPath)
	}
	if cfg.Server.JWTSecret != "s3cret" {
		t.Errorf("jwt_secret not interpolated: %q", cfg.Server.JWTSecret)
	}
	if cfg.Server.JWTTTL != 12*time.Hour {
		t.Errorf("jwt_ttl = %v", cfg.Server.JWTTTL)
	}
	if cfg.Server.WSEnabled {
		t.Error("ws_enabled = true, want false")
	}

	// global roles
	if cfg.Roles.Admin != "admins" || cfg.Roles.Normal != "staff" || cfg.Roles.Readonly != "viewers" {
		t.Errorf("global roles = %+v", cfg.Roles)
	}

	// listeners
	if len(cfg.Listeners) != 2 {
		t.Fatalf("listeners = %d, want 2", len(cfg.Listeners))
	}
	corp, kerb := cfg.Listeners[0], cfg.Listeners[1]

	if corp.OIDC.Issuer != "https://kc.corp/realm" || corp.OIDC.ClientSecret != "corp-secret" {
		t.Errorf("oidc profile = %+v", corp.OIDC)
	}
	if corp.OIDC.CAFile != "/certs/root.pem" {
		t.Errorf("ca_file = %q", corp.OIDC.CAFile)
	}
	if len(corp.OIDC.Scopes) != 3 || corp.OIDC.Scopes[0] != "openid" {
		t.Errorf("scopes not defaulted: %v", corp.OIDC.Scopes)
	}
	// listener without overrides inherits global roles
	if corp.Roles.Admin != "admins" || corp.Roles.Readonly != "viewers" {
		t.Errorf("corp roles = %+v", corp.Roles)
	}

	// headers listener with per-listener role override:
	// admin overridden, others inherited from [roles]
	if kerb.Headers.User != "X-Remote-User" || kerb.Headers.Groups != "X-Remote-Groups" {
		t.Errorf("headers = %+v", kerb.Headers)
	}
	if kerb.Roles.Admin != "server-admins" {
		t.Errorf("kerb roles.admin = %q", kerb.Roles.Admin)
	}
	if kerb.Roles.Normal != "staff" || kerb.Roles.Readonly != "viewers" {
		t.Errorf("kerb roles should inherit global: %+v", kerb.Roles)
	}
}

func TestLoadDefaults(t *testing.T) {
	// A minimal config: defaults must fill everything except jwt_secret.
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "dev"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.DBPath != "teamviz.db" {
		t.Errorf("db_path default = %q", cfg.Server.DBPath)
	}
	if cfg.Server.JWTTTL != 24*time.Hour {
		t.Errorf("jwt_ttl default = %v", cfg.Server.JWTTTL)
	}
	if !cfg.Server.WSEnabled {
		t.Error("ws_enabled default = false, want true")
	}
	if cfg.Roles.Admin != "admin" || cfg.Roles.Normal != "normal" || cfg.Roles.Readonly != "readonly" {
		t.Errorf("default roles = %+v", cfg.Roles)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("listeners = %d", len(cfg.Listeners))
	}
	ln := cfg.Listeners[0]
	if ln.Name != "listener-1" {
		t.Errorf("generated name = %q", ln.Name)
	}
	if ln.Headers.User != "X-Dev-User" || ln.Headers.Groups != "X-Dev-Groups" {
		t.Errorf("dev listener header defaults = %+v", ln.Headers)
	}
}

func TestLoadMissingSecret(t *testing.T) {
	path := writeTemp(t, `
[[listener]]
listen = ":8080"
auth   = "dev"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "jwt_secret") {
		t.Fatalf("want jwt_secret error, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil || !strings.Contains(err.Error(), "-config") {
		t.Fatalf("want helpful not-found error, got %v", err)
	}
}

func TestLoadUnknownKeyFails(t *testing.T) {
	// strict decoding: typos must be caught, not silently dropped
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "dev"
cafiles = "nope"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("unknown key not rejected")
	}
}

func TestLoadUnknownAuthMode(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "kerberos-native"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown auth mode") {
		t.Fatalf("want unknown auth mode error, got %v", err)
	}
}

func TestLoadOIDCRequiresSubTable(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "oidc"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "[listener.oidc]") {
		t.Fatalf("want oidc sub-table error, got %v", err)
	}
}

func TestLoadOIDCRequiresFields(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "oidc"

  [listener.oidc]
  issuer = "https://issuer"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("want missing-fields error, got %v", err)
	}
}

func TestLoadHeadersRequiresSubTable(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "headers"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "[listener.headers]") {
		t.Fatalf("want headers sub-table error, got %v", err)
	}
}

func TestLoadRedirectPostLogoutDefault(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "oidc"

  [listener.oidc]
  issuer        = "https://kc/realm"
  client_id     = "c"
  client_secret = "s"
  redirect_url  = "https://tvz.corp/auth/callback"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listeners[0].OIDC.PostLogoutRedirectURL != "https://tvz.corp/" {
		t.Errorf("derived post-logout redirect = %q", cfg.Listeners[0].OIDC.PostLogoutRedirectURL)
	}
}

func TestLoadDuplicateListen(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "dev"

[[listener]]
listen = ":8080"
auth   = "dev"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate listen error, got %v", err)
	}
}

func TestLoadMalformedListen(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = "8080"
auth   = "dev"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("want listen format error, got %v", err)
	}
}

func TestLoadNoListeners(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "x"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "[[listener]]") {
		t.Fatalf("want no-listeners error, got %v", err)
	}
}

func TestInterpolationMissingVar(t *testing.T) {
	path := writeTemp(t, `
[server]
jwt_secret = "${DEFINITELY_NOT_SET_12345}"

[[listener]]
listen = ":8080"
auth   = "dev"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "DEFINITELY_NOT_SET_12345") {
		t.Fatalf("want missing-env error naming the variable, got %v", err)
	}
}

func TestInterpolationEscape(t *testing.T) {
	t.Setenv("A_VAR", "value")
	path := writeTemp(t, `
[server]
jwt_secret = "$${A_VAR}"

[[listener]]
listen = ":8080"
auth   = "dev"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.JWTSecret != "${A_VAR}" {
		t.Fatalf("escape failed: %q", cfg.Server.JWTSecret)
	}
}

func TestInterpolationInScopes(t *testing.T) {
	t.Setenv("SCOPE_EXTRA", "email")
	path := writeTemp(t, `
[server]
jwt_secret = "x"

[[listener]]
listen = ":8080"
auth   = "oidc"

  [listener.oidc]
  issuer        = "https://kc/realm"
  client_id     = "c"
  client_secret = "s"
  redirect_url  = "http://localhost/auth/callback"
  scopes        = ["openid", "${SCOPE_EXTRA}"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := strings.Join(cfg.Listeners[0].OIDC.Scopes, ","); got != "openid,email" {
		t.Fatalf("scopes = %q", got)
	}
}
