package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
)

type AuthService struct {
	cfg *config.Config
	db  *store.Store

	// OIDC (nil if not configured)
	httpClient   *http.Client
	provider     *oidc.Provider
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
}

func New(cfg *config.Config, db *store.Store) *AuthService {
	svc := &AuthService{cfg: cfg, db: db}

	if cfg.OIDCIssuer == "" {
		log.Printf("auth: OIDC not configured (dev mode)")
		return svc
	}

	// Build the OIDC HTTP client used for all backend calls
	// (discovery, JWKS fetch, token exchange, userinfo).
	httpClt, err := buildOIDCHTTPClient(cfg)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	svc.httpClient = httpClt

	// Initialize OIDC provider (fetches discovery document)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClt)
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		log.Fatalf("auth: failed to init OIDC provider: %v", err)
	}
	svc.provider = provider

	// OAuth2 config for the Authorization Code flow
	svc.oauth2Config = &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.OIDCRedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       cfg.OIDCScopes,
	}

	// ID token verifier
	svc.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})

	log.Printf("auth: OIDC provider initialized (issuer=%s)", cfg.OIDCIssuer)
	return svc
}

// buildOIDCHTTPClient returns the HTTP client used for all OIDC backend calls.
//
// TLS policy: certificate validation is always enabled — there is deliberately
// no "skip TLS verify" switch. The client trusts the system root CA store by
// default (the Docker image installs ca-certificates). Root CA(s) of a private
// PKI (e.g. an internal Keycloak behind a corporate CA) can be added via
// TVZ_OIDC_CA_FILE (path(s) to a PEM file) and/or TVZ_OIDC_CA (inline PEM).
// Extra roots are appended to a copy of the system pool — they never replace
// it, and they are scoped to the OIDC client only.
func buildOIDCHTTPClient(cfg *config.Config) (*http.Client, error) {
	extraPEM, err := loadExtraCACertificates(cfg)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	custom := false

	if len(extraPEM) > 0 {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(extraPEM) {
			return nil, fmt.Errorf("TVZ_OIDC_CA_FILE/TVZ_OIDC_CA: no usable PEM certificate(s) found")
		}
		transport.TLSClientConfig.RootCAs = roots
		custom = true
		log.Printf("auth: OIDC client trusts extra root CA(s) (files=%v, inline=%t)",
			cfg.OIDCCAFiles, cfg.OIDCAInline != "")
	}

	// Docker networking: rewrite the public issuer host to the Docker-internal
	// host:port at the TCP dial level (keeps TLS/SNI and hostname validation intact).
	if cfg.OIDCInternalHost != "" {
		issuerURL, err := url.Parse(cfg.OIDCIssuer)
		if err != nil {
			return nil, fmt.Errorf("invalid OIDC issuer URL: %w", err)
		}
		issuerHost := issuerURL.Host
		internalHost := cfg.OIDCInternalHost
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == issuerHost {
				addr = internalHost
			}
			return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
		}
		custom = true
		log.Printf("auth: OIDC internal host rewrite %s -> %s", issuerHost, internalHost)
	}

	if !custom {
		return http.DefaultClient, nil
	}
	return &http.Client{Transport: transport}, nil
}

// loadExtraCACertificates concatenates the PEM data of all extra root CA(s)
// configured via TVZ_OIDC_CA_FILE (comma-separated file paths) and
// TVZ_OIDC_CA (inline PEM, or base64-encoded PEM).
func loadExtraCACertificates(cfg *config.Config) ([]byte, error) {
	var buf bytes.Buffer
	for _, path := range cfg.OIDCCAFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("TVZ_OIDC_CA_FILE %q: %w", path, err)
		}
		buf.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if inline := strings.TrimSpace(cfg.OIDCAInline); inline != "" {
		if !strings.Contains(inline, "-----BEGIN") {
			// Tolerate base64-encoded PEM (e.g. `base64 -w0 ca.crt`).
			if decoded, derr := base64.StdEncoding.DecodeString(inline); derr == nil &&
				strings.Contains(string(decoded), "-----BEGIN") {
				inline = string(decoded)
			}
		}
		buf.WriteString(inline)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// mapGroupsToRole maps Keycloak group names to app roles.
//
// All three configured groups are matched explicitly — including
// TVZ_READONLY_GROUP, which previously only existed as an implicit fallback.
// If the token contains none of the configured groups, ok=false is returned
// and the caller must deny access (fail closed): unknown or missing group
// claims no longer silently grant read-only access.
func (a *AuthService) mapGroupsToRole(groups []string) (model.Role, bool) {
	var role model.Role // "" until a configured group matches
	for _, g := range groups {
		switch g {
		case a.cfg.AdminGroup:
			role = model.RoleAdmin
		case a.cfg.NormalGroup:
			if role != model.RoleAdmin {
				role = model.RoleNormal
			}
		case a.cfg.ReadonlyGroup:
			if role == "" { // never downgrade an already-mapped higher role
				role = model.RoleReadonly
			}
		}
	}
	if role == "" {
		return "", false
	}
	return role, true
}

// extractUserFromDevHeaders reads dev-mode headers and returns
// (username, role, mapped). A non-empty username with mapped=false means the
// user authenticated but no configured group matched — access is denied.
func (a *AuthService) extractUserFromDevHeaders(r *http.Request) (string, model.Role, bool) {
	username := r.Header.Get("X-Dev-User")
	if username == "" {
		return "", "", false
	}
	role, mapped := a.mapGroupsToRole(splitGroups(r.Header.Get("X-Dev-Groups")))
	return username, role, mapped
}

// IssueToken creates a JWT for the given user.
func (a *AuthService) IssueToken(username string, role model.Role) (string, error) {
	claims := jwt.MapClaims{
		"username": username,
		"role":     string(role),
		"exp":      time.Now().Add(a.cfg.JWTTTL).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.cfg.JWTSecret)
}

// ValidateToken parses and validates a JWT string.
func (a *AuthService) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return a.cfg.JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// AuthFromRequest extracts or creates a session from the request.
//
// Priority: dev mode headers > Bearer JWT > JWT cookie.
func (a *AuthService) AuthFromRequest(r *http.Request) (*model.User, string, error) {
	// 1. Dev mode headers (only when DevMode is enabled)
	if a.cfg.DevMode {
		if username, role, mapped := a.extractUserFromDevHeaders(r); username != "" {
			if !mapped {
				// Fail closed: dev headers present but no recognized group.
				return nil, "", ErrAccessDenied
			}
			user, err := a.db.UpsertUser(username, role)
			if err != nil {
				return nil, "", err
			}
			tokenStr, err := a.tokenForUser(r, username, role)
			if err != nil {
				return nil, "", err
			}
			return user, tokenStr, nil
		}
	}

	// 2. Bearer JWT (direct API calls)
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if claims, err := a.ValidateToken(tokenStr); err == nil {
			user, err := a.db.UpsertUser(claims.Username, model.Role(claims.Role))
			if err != nil {
				return nil, "", err
			}
			return user, tokenStr, nil
		}
	}

	// 3. JWT cookie
	if cookie, err := r.Cookie("teamviz_token"); err == nil && cookie.Value != "" {
		if claims, err := a.ValidateToken(cookie.Value); err == nil {
			user, err := a.db.UpsertUser(claims.Username, model.Role(claims.Role))
			if err != nil {
				return nil, "", err
			}
			return user, cookie.Value, nil
		}
	}

	return nil, "", ErrNoAuth
}

// tokenForUser reuses the existing teamviz_token cookie when it is still valid
// and matches the given identity; otherwise issues a fresh JWT.
func (a *AuthService) tokenForUser(r *http.Request, username string, role model.Role) (string, error) {
	if cookie, err := r.Cookie("teamviz_token"); err == nil && cookie.Value != "" {
		if claims, err := a.ValidateToken(cookie.Value); err == nil {
			if claims.Username == username && claims.Role == string(role) {
				return cookie.Value, nil
			}
		}
	}
	return a.IssueToken(username, role)
}

// LoginHandler initiates the OIDC Authorization Code flow with PKCE.
func (a *AuthService) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if a.oauth2Config == nil {
		http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
		return
	}

	state, err := generateRandomString(32)
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	verifier := oauth2.GenerateVerifier()

	http.SetCookie(w, &http.Cookie{
		Name: "teamviz_oauth_state", Value: state,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "teamviz_oauth_verifier", Value: verifier,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300,
	})

	authURL := a.oauth2Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler handles the OIDC callback, exchanges the code for tokens,
// verifies the ID token, extracts user info, and sets the session cookie.
func (a *AuthService) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	if a.oauth2Config == nil || a.verifier == nil {
		http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
		return
	}

	stateCookie, err := r.Cookie("teamviz_oauth_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	verifierCookie, err := r.Cookie("teamviz_oauth_verifier")
	if err != nil {
		http.Error(w, "missing verifier cookie", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, a.httpClient)
	token, err := a.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "missing id_token in response", http.StatusInternalServerError)
		return
	}

	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "id token verification failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var claims struct {
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "failed to parse claims: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if claims.PreferredUsername == "" {
		http.Error(w, "no preferred_username in token", http.StatusInternalServerError)
		return
	}

	role, mapped := a.mapGroupsToRole(claims.Groups)
	if !mapped {
		// Authenticated at the provider, but holds none of the groups that
		// grant access — fail closed with an explanatory page, no session.
		// The app session cookie is cleared as well: a stale session from an
		// earlier login must not survive a denied login.
		clearCookie(w, "teamviz_oauth_state")
		clearCookie(w, "teamviz_oauth_verifier")
		clearCookie(w, "teamviz_token")
		log.Printf("auth: access denied for user %s: no recognized group in token (groups=%v)",
			claims.PreferredUsername, claims.Groups)
		a.renderAccessDenied(w, claims.PreferredUsername, claims.Groups)
		return
	}

	user, err := a.db.UpsertUser(claims.PreferredUsername, role)
	if err != nil {
		http.Error(w, "failed to upsert user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jwtToken, err := a.IssueToken(claims.PreferredUsername, role)
	if err != nil {
		http.Error(w, "failed to issue token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	clearCookie(w, "teamviz_oauth_state")
	clearCookie(w, "teamviz_oauth_verifier")

	http.SetCookie(w, &http.Cookie{
		Name: "teamviz_token", Value: jwtToken,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(a.cfg.JWTTTL.Seconds()),
	})

	log.Printf("auth: user %s logged in (role=%s)", user.Username, user.Role)
	http.Redirect(w, r, "/", http.StatusFound)
}

// LogoutHandler clears the app session and redirects to Keycloak's end_session endpoint.
func (a *AuthService) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, "teamviz_token")

	if a.cfg.OIDCIssuer == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	endSessionURL := strings.TrimSuffix(a.cfg.OIDCIssuer, "/") + "/protocol/openid-connect/logout"
	params := url.Values{}
	if a.cfg.OIDCClientID != "" {
		params.Set("client_id", a.cfg.OIDCClientID)
	}
	if a.cfg.OIDCPostLogoutRedirect != "" {
		params.Set("post_logout_redirect_uri", a.cfg.OIDCPostLogoutRedirect)
	}
	logoutURL := endSessionURL
	if len(params) > 0 {
		logoutURL += "?" + params.Encode()
	}

	http.Redirect(w, r, logoutURL, http.StatusFound)
}

// ===== Helper types and functions =====

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

var ErrNoAuth = &authError{"no authentication provided"}

// ErrAccessDenied is returned when the user authenticated (dev headers) but
// their group claims map to no application role. The OIDC flow renders the
// access-denied page instead; the middleware answers API calls with 403.
var ErrAccessDenied = &authError{"account is not a member of any group that grants access"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func splitGroups(s string) []string {
	s = strings.TrimSpace(s)
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

func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
