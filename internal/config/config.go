package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen    string
	DBPath    string
	JWTSecret []byte
	JWTTTL    time.Duration

	// OIDC
	OIDCIssuer             string
	OIDCClientID           string
	OIDCClientSecret       string
	OIDCRedirectURL        string
	OIDCInternalHost       string   // optional: Docker-internal host:port for OIDC backend calls
	OIDCCAFiles            []string // optional: extra root CA PEM file(s) to trust for OIDC HTTPS calls
	OIDCAInline            string   // optional: extra root CA(s) as inline PEM (or base64-encoded PEM)
	OIDCScopes             []string
	OIDCPostLogoutRedirect string

	// Role mapping
	AdminGroup    string
	NormalGroup   string
	ReadonlyGroup string

	// Dev mode
	DevMode bool

	WSEnabled bool
}

func Load() (*Config, error) {
	cfg := &Config{
		Listen:    envOr("TVZ_LISTEN", ":8080"),
		DBPath:    envOr("TVZ_DB_PATH", "teamviz.db"),
		JWTSecret: []byte(envOr("TVZ_JWT_SECRET", "")),
		JWTTTL:    envDurationOr("TVZ_JWT_TTL", 24*time.Hour),

		OIDCIssuer:       envOr("TVZ_OIDC_ISSUER", ""),
		OIDCClientID:     envOr("TVZ_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: envOr("TVZ_OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  envOr("TVZ_OIDC_REDIRECT_URL", ""),
		OIDCInternalHost: envOr("TVZ_OIDC_INTERNAL_HOST", ""),
		OIDCCAFiles:      splitCommaList(envOr("TVZ_OIDC_CA_FILE", "")),
		OIDCAInline:      envOr("TVZ_OIDC_CA", ""),
		OIDCScopes:       splitCommaList(envOr("TVZ_OIDC_SCOPES", "openid,email,profile")),

		AdminGroup:    envOr("TVZ_ADMIN_GROUP", "admin"),
		NormalGroup:   envOr("TVZ_NORMAL_GROUP", "normal"),
		ReadonlyGroup: envOr("TVZ_READONLY_GROUP", "readonly"),

		DevMode: envBoolOr("TVZ_DEV_MODE", false),

		WSEnabled: envBoolOr("TVZ_WS_ENABLED", true),
	}

	// Derive post-logout redirect URL
	cfg.OIDCPostLogoutRedirect = envOr("TVZ_OIDC_POST_LOGOUT_REDIRECT_URL", "")
	if cfg.OIDCPostLogoutRedirect == "" && cfg.OIDCRedirectURL != "" {
		if u, err := url.Parse(cfg.OIDCRedirectURL); err == nil {
			cfg.OIDCPostLogoutRedirect = u.Scheme + "://" + u.Host + "/"
		}
	}

	// Validation
	if len(cfg.JWTSecret) == 0 {
		return nil, fmt.Errorf("TVZ_JWT_SECRET is required")
	}

	if !cfg.DevMode {
		if cfg.OIDCIssuer == "" {
			return nil, fmt.Errorf("TVZ_OIDC_ISSUER is required (set TVZ_DEV_MODE=true to skip)")
		}
		if cfg.OIDCClientID == "" {
			return nil, fmt.Errorf("TVZ_OIDC_CLIENT_ID is required")
		}
		if cfg.OIDCClientSecret == "" {
			return nil, fmt.Errorf("TVZ_OIDC_CLIENT_SECRET is required")
		}
		if cfg.OIDCRedirectURL == "" {
			return nil, fmt.Errorf("TVZ_OIDC_REDIRECT_URL is required")
		}
	}

	return cfg, nil
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envDurationOr(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultVal
}

func envBoolOr(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		switch v {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return defaultVal
}

func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
