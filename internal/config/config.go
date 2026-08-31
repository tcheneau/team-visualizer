// Package config loads the complete Team Visualizer configuration from a
// single TOML file (located via the -config flag; default ./teamviz.toml).
//
// Structure of the file:
//
//	[server]      — process-wide settings (db, JWT, WebSocket)
//	[roles]       — global group→role mapping (defaults: admin/normal/readonly)
//	[[listener]]  — one entry per listening port, each with its own auth mode
//	                (oidc | headers | dev) and optional per-listener role
//	                overrides.
//
// Any ${VAR} in any string value is interpolated from the process
// environment before parsing (fail fast when unset); $${…} escapes a
// literal ${…}.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// DefaultConfigFile is used when -config is not provided.
const DefaultConfigFile = "teamviz.toml"

// AuthMode selects the authentication mechanism of a listener.
type AuthMode string

const (
	AuthModeOIDC    AuthMode = "oidc"
	AuthModeHeaders AuthMode = "headers"
	AuthModeDev     AuthMode = "dev"
)

// Server holds process-wide settings.
type Server struct {
	DBPath    string
	JWTSecret string
	JWTTTL    time.Duration
	WSEnabled bool
}

// RoleMapping maps OIDC/proxy groups to application roles.
type RoleMapping struct {
	Admin    string `toml:"admin"`
	Normal   string `toml:"normal"`
	Readonly string `toml:"readonly"`
}

// OIDC is the per-listener OIDC (Authorization Code + PKCE) profile.
type OIDC struct {
	Issuer                string   `toml:"issuer"`
	ClientID              string   `toml:"client_id"`
	ClientSecret          string   `toml:"client_secret"`
	RedirectURL           string   `toml:"redirect_url"`
	PostLogoutRedirectURL string   `toml:"post_logout_redirect_url"` // optional; defaults to the web root of redirect_url
	InternalHost          string   `toml:"internal_host"`            // optional: rewrite issuer host for Docker-internal calls
	Scopes                []string `toml:"scopes"`
	CAFile                string   `toml:"ca_file"` // optional: PEM file with root CA(s) to trust
	CA                    string   `toml:"ca"`      // optional: PEM with root CA(s), inline
}

// Headers is the per-listener trusted-proxy profile (e.g. Kerberos via
// Apache mod_auth_gssapi).
type Headers struct {
	User   string `toml:"user"`
	Groups string `toml:"groups"`
}

// Listener is one full application instance on one port with one auth mode.
type Listener struct {
	Name    string
	Listen  string
	Auth    AuthMode
	OIDC    OIDC
	Headers Headers
	Roles   RoleMapping // effective mapping (global + overrides merged)
}

// Config is the complete, validated application configuration.
type Config struct {
	Server    Server
	Roles     RoleMapping // global mapping (already defaulted)
	Listeners []Listener
}

// Load reads, interpolates, parses and validates the configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config file %q not found — pass -config <path> (default is ./%s)", path, DefaultConfigFile)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// parse interpolates ${VAR} references into the raw TOML document, then
// strictly decodes it into the typed configuration.
func parse(data []byte) (*Config, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if err := interpolateMap(raw); err != nil {
		return nil, err
	}
	b, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encode: %w", err)
	}
	var rawCfg rawConfig
	dec := toml.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rawCfg); err != nil {
		return nil, fmt.Errorf("invalid: %w", err)
	}
	return rawCfg.finalize()
}

// ---- raw (pre-validation) structures --------------------------------------

type rawConfig struct {
	Server    serverRaw     `toml:"server"`
	Roles     RoleMapping   `toml:"roles"`
	Listeners []listenerRaw `toml:"listener"`
}

type serverRaw struct {
	DBPath    string `toml:"db_path"`
	JWTSecret string `toml:"jwt_secret"`
	JWTTTL    string `toml:"jwt_ttl"`
	WSEnabled *bool  `toml:"ws_enabled"`
}

type listenerRaw struct {
	Name    string       `toml:"name"`
	Listen  string       `toml:"listen"`
	Auth    string       `toml:"auth"`
	OIDC    *OIDC        `toml:"oidc"`
	Headers *Headers     `toml:"headers"`
	Roles   *RoleMapping `toml:"roles"`
}

// finalize converts the raw (but already interpolated) config into a fully
// validated, normalised *Config.
func (raw *rawConfig) finalize() (*Config, error) {
	var errs []error

	// --- server ---
	server := Server{
		DBPath:    raw.Server.DBPath,
		JWTSecret: raw.Server.JWTSecret,
		WSEnabled: true, // default: enabled
	}
	if server.DBPath == "" {
		server.DBPath = "teamviz.db"
	}
	if raw.Server.WSEnabled != nil {
		server.WSEnabled = *raw.Server.WSEnabled
	}
	ttl := raw.Server.JWTTTL
	if ttl == "" {
		ttl = "24h"
	}
	ttlD, err := time.ParseDuration(ttl)
	if err != nil {
		errs = append(errs, fmt.Errorf("server.jwt_ttl %q: %w", ttl, err))
	}
	server.JWTTTL = ttlD

	// --- roles ---
	globalRoles := RoleMapping{
		Admin:    orDefault(raw.Roles.Admin, "admin"),
		Normal:   orDefault(raw.Roles.Normal, "normal"),
		Readonly: orDefault(raw.Roles.Readonly, "readonly"),
	}

	// --- listeners ---
	if len(raw.Listeners) == 0 {
		errs = append(errs, errors.New("at least one [[listener]] must be defined"))
	}
	listeners := make([]Listener, 0, len(raw.Listeners))
	seen := map[string]bool{}
	for i, lr := range raw.Listeners {
		name := orDefault(lr.Name, fmt.Sprintf("listener-%d", i+1))
		if lr.Listen == "" {
			errs = append(errs, fmt.Errorf("listener %q: listen is required (e.g. \":8080\")", name))
			continue
		}
		if _, _, err := net.SplitHostPort(lr.Listen); err != nil {
			errs = append(errs, fmt.Errorf("listener %q: listen %q: %w", name, lr.Listen, err))
			continue
		}
		if seen[lr.Listen] {
			errs = append(errs, fmt.Errorf("listener %q: duplicate listen address %q", name, lr.Listen))
			continue
		}
		seen[lr.Listen] = true

		ln := Listener{
			Name:   name,
			Listen: lr.Listen,
			Auth:   AuthMode(lr.Auth),
		}

		// role mapping: listener override > global > built-in defaults
		override := RoleMapping{}
		if lr.Roles != nil {
			override = *lr.Roles
		}
		lstRoles := RoleMapping{
			Admin:    firstNonEmpty(override.Admin, globalRoles.Admin),
			Normal:   firstNonEmpty(override.Normal, globalRoles.Normal),
			Readonly: firstNonEmpty(override.Readonly, globalRoles.Readonly),
		}

		switch ln.Auth {
		case AuthModeOIDC:
			if lr.OIDC == nil {
				errs = append(errs, fmt.Errorf("listener %q: [listener.oidc] is required when auth = \"oidc\"", name))
				continue
			}
			ln.OIDC = *lr.OIDC
			missing := missingRequired(map[string]string{
				"issuer":        ln.OIDC.Issuer,
				"client_id":     ln.OIDC.ClientID,
				"client_secret": ln.OIDC.ClientSecret,
				"redirect_url":  ln.OIDC.RedirectURL,
			})
			if len(missing) > 0 {
				errs = append(errs, fmt.Errorf("listener %q: [listener.oidc] missing required key(s): %v", name, missing))
				continue
			}
			if len(ln.OIDC.Scopes) == 0 {
				ln.OIDC.Scopes = []string{"openid", "email", "profile"}
			}
			if ln.OIDC.PostLogoutRedirectURL == "" && ln.OIDC.RedirectURL != "" {
				if u, err := url.Parse(ln.OIDC.RedirectURL); err == nil && u.Scheme != "" && u.Host != "" {
					ln.OIDC.PostLogoutRedirectURL = u.Scheme + "://" + u.Host + "/"
				}
			}
			ln.Roles = lstRoles
		case AuthModeHeaders:
			if lr.Headers == nil {
				errs = append(errs, fmt.Errorf("listener %q: [listener.headers] is required when auth = \"headers\"", name))
				continue
			}
			ln.Headers = *lr.Headers
			if missing := missingRequired(map[string]string{"user": ln.Headers.User, "groups": ln.Headers.Groups}); len(missing) > 0 {
				errs = append(errs, fmt.Errorf("listener %q: [listener.headers] missing required key(s): %v", name, missing))
				continue
			}
			ln.Roles = lstRoles
		case AuthModeDev:
			// dev mode authenticates via X-Dev-User / X-Dev-Groups with the
			// optional [listener.headers] table overriding the header names.
			if lr.Headers != nil {
				ln.Headers = *lr.Headers
			} else {
				ln.Headers = Headers{User: "X-Dev-User", Groups: "X-Dev-Groups"}
			}
			ln.Roles = lstRoles
		default:
			errs = append(errs, fmt.Errorf("listener %q: unknown auth mode %q (expected \"oidc\", \"headers\" or \"dev\")", name, lr.Auth))
			continue
		}
		listeners = append(listeners, ln)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &Config{
		Server:    server,
		Roles:     globalRoles,
		Listeners: listeners,
	}, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Server.JWTSecret) == "" {
		return errors.New("config: [server] jwt_secret is required (reference an environment variable, e.g. jwt_secret = \"$TEAMVIZ_JWT_SECRET\")")
	}
	return nil
}

// ---- ${VAR} interpolation ---------------------------------------------------

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolateMap walks the raw document and resolves ${VAR} in every string
// value against the process environment. "$${...}" is the escape for a
// literal "${...}".
func interpolateMap(m map[string]any) error {
	if _, err := interpolateValue(m); err != nil {
		return err
	}
	return nil
}

func interpolateValue(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return interpolateString(t)
	case map[string]any:
		for k, item := range t {
			nv, err := interpolateValue(item)
			if err != nil {
				return nil, err
			}
			t[k] = nv
		}
		return t, nil
	case []any:
		for i, item := range t {
			nv, err := interpolateValue(item)
			if err != nil {
				return nil, err
			}
			t[i] = nv
		}
		return t, nil
	default:
		return v, nil
	}
}

const escapedBrace = "\x00{" // sentinel for the literal "${" escape

func interpolateString(s string) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	s = strings.ReplaceAll(s, "$${", escapedBrace)
	var missing []string
	res := envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := envVarRe.FindStringSubmatch(match)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return match
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable(s) referenced in config but not set: %v", missing)
	}
	res = strings.ReplaceAll(res, escapedBrace, "${")
	return res, nil
}

// ---- small helpers ---------------------------------------------------------

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func missingRequired(kv map[string]string) []string {
	var missing []string
	for k, v := range kv {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	return missing
}
