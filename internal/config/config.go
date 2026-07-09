package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	Listen          string
	DBPath          string
	JWTSecret       []byte
	JWTTTL          time.Duration
	ProxyHeaderUser   string
	ProxyHeaderGroups string
	AdminGroup      string
	NormalGroup     string
	ReadonlyGroup   string
	WSEnabled       bool
}

func Load() (*Config, error) {
	cfg := &Config{
		Listen:            envOr("TVZ_LISTEN", ":8080"),
		DBPath:            envOr("TVZ_DB_PATH", "teamviz.db"),
		JWTSecret:         []byte(envOr("TVZ_JWT_SECRET", "")),
		JWTTTL:            envDurationOr("TVZ_JWT_TTL", 24*time.Hour),
		ProxyHeaderUser:   envOr("TVZ_PROXY_HEADER_USER", "X-Forwarded-User"),
		ProxyHeaderGroups: envOr("TVZ_PROXY_HEADER_GROUPS", "X-Forwarded-Groups"),
		AdminGroup:        envOr("TVZ_ADMIN_GROUP", "admin"),
		NormalGroup:       envOr("TVZ_NORMAL_GROUP", "normal"),
		ReadonlyGroup:     envOr("TVZ_READONLY_GROUP", "readonly"),
		WSEnabled:         envBoolOr("TVZ_WS_ENABLED", true),
	}

	if len(cfg.JWTSecret) == 0 {
		return nil, fmt.Errorf("TVZ_JWT_SECRET is required")
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