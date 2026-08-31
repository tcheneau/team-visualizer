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

// AuthService performs authentication for one listener. Every listener gets
// its own service instance (with its own auth mode and provider config) while
// all listeners share the underlying store and the app's JWT secret.
type AuthService struct {
	listener  config.Listener
	jwtSecret []byte
	jwtTTL    time.Duration
	roles     config.RoleMapping
	db        *store.Store

	httpClient   *http.Client
	provider     *oidc.Provider
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
}

// New builds the auth service for one listener. The mode-specific parts of
// the listener configuration are prepared here; the JWT/session handling is
// identical for every mode.
func New(ln config.Listener, server config.Server, db *store.Store) *AuthService {
	svc := &AuthService{
		listener:  ln,
		jwtSecret: []byte(server.JWTSecret),
		jwtTTL:    server.JWTTTL,
		roles:     ln.Roles,
		db:        db,
	}

	switch ln.Auth {
	case config.AuthModeOIDC:
		// Build the OIDC HTTP client used for all backend calls
		// (discovery, JWKS fetch, token exchange, userinfo).
		httpClient, err := buildOIDCHTTPClient(ln.Name, &ln.OIDC)
		if err != nil {
			log.Fatalf("auth: listener %q: %v", ln.Name, err)
		}
		svc.httpClient = httpClient

		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
		provider, err := oidc.NewProvider(ctx, ln.OIDC.Issuer)
		if err != nil {
			log.Fatalf("auth: listener %q: failed to init OIDC provider: %v", ln.Name, err)
		}
		svc.provider = provider
		svc.oauth2Config = &oauth2.Config{
			ClientID:     ln.OIDC.ClientID,
			ClientSecret: ln.OIDC.ClientSecret,
			RedirectURL:  ln.OIDC.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       ln.OIDC.Scopes,
		}
		svc.verifier = provider.Verifier(&oidc.Config{ClientID: ln.OIDC.ClientID})

		log.Printf("auth: listener %q OIDC provider initialized (issuer=%s)", ln.Name, ln.OIDC.Issuer)

	case config.AuthModeHeaders:
		log.Printf("auth: listener %q trusts headers %q / %q — ensure only the trusted proxy can reach %s",
			ln.Name, ln.Headers.User, ln.Headers.Groups, ln.Listen)

	case config.AuthModeDev:
		log.Printf("auth: listener %q runs in dev mode (headers %q / %q)", ln.Name, ln.Headers.User, ln.Headers.Groups)
	}
	return svc
}

// buildOIDCHTTPClient returns the HTTP client used for all OIDC backend calls.
//
// TLS policy: certificate validation is always enabled — there is deliberately
// no "skip TLS verify" switch. The client trusts the system root CA store by
// default (the Docker image installs ca-certificates). Root CA(s) of a private
// PKI (e.g. an internal Keycloak behind a corporate CA) can be added via
// oidc.ca_file (path to a PEM file) and/or oidc.ca (inline PEM).
// Extra roots are appended to a copy of the system pool — they never replace
// it, and they are scoped to the OIDC client only.
func buildOIDCHTTPClient(lnName string, o *config.OIDC) (*http.Client, error) {
	extraPEM, err := loadCACertificates(o.CAFile, o.CA)
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
			return nil, fmt.Errorf("oidc.ca_file/oidc.ca: no usable PEM certificate(s) found")
		}
		transport.TLSClientConfig.RootCAs = roots
		custom = true
		log.Printf("auth: listener %q trusts extra root CA(s) from the config (%d byte(s))", lnName, len(extraPEM))
	}

	// Docker networking: rewrite the public issuer host to the Docker-internal
	// host:port at the TCP dial level (keeps TLS/SNI and hostname validation intact).
	if o.InternalHost != "" {
		issuerURL, err := url.Parse(o.Issuer)
		if err != nil {
			return nil, fmt.Errorf("invalid OIDC issuer URL: %w", err)
		}
		issuerHost := issuerURL.Host
		internalHost := o.InternalHost
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

// loadCACertificates concatenates the PEM data of the optional extra root
// CA(s): oidc.ca_file (path) and oidc.ca (inline PEM).
func loadCACertificates(caFile, caPEM string) ([]byte, error) {
	var buf bytes.Buffer
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("oidc.ca_file %q: %w", caFile, err)
		}
		buf.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if inline := strings.TrimSpace(caPEM); inline != "" {
		buf.WriteString(inline)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// mapGroupsToRole maps provider group names to application roles.
//
// All three configured groups are matched explicitly — including the
// readonly group. If the token contains none of the configured groups,
// ok=false is returned and the caller must deny access (fail closed):
// unknown or missing group claims no longer silently grant read-only access.
func (a *AuthService) mapGroupsToRole(groups []string) (model.Role, bool) {
	var role model.Role // "" until a configured group matches
	for _, g := range groups {
		switch g {
		case a.roles.Admin:
			role = model.RoleAdmin
		case a.roles.Normal:
			if role != model.RoleAdmin {
				role = model.RoleNormal
			}
		case a.roles.Readonly:
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

// userFromHeaders reads the identity headers configured for a "headers" or
// "dev" listener and returns (username, role, mapped). A non-empty username
// with mapped=false means the user authenticated but no configured group
// matched — access is denied.
func (a *AuthService) userFromHeaders(r *http.Request, userHeader, groupsHeader string) (string, model.Role, bool) {
	username := strings.TrimSpace(r.Header.Get(userHeader))
	if username == "" {
		return "", "", false
	}
	role, mapped := a.mapGroupsToRole(splitGroups(r.Header.Get(groupsHeader)))
	return username, role, mapped
}

// identifyFromListenerHeaders reads the identity headers of the listener's
// auth mode: "dev" authenticates with X-Dev-User / X-Dev-Groups, "headers"
// with the listener-configured headers. Returns mapped=false when the user
// authenticated but no group matched (callers must deny access).
func (a *AuthService) identifyFromHeaders(r *http.Request) (username string, role model.Role, mapped bool) {
	var userHeader, groupsHeader string
	switch a.mode() {
	case config.AuthModeDev:
		userHeader, groupsHeader = "X-Dev-User", "X-Dev-Groups"
	case config.AuthModeHeaders:
		userHeader, groupsHeader = a.listener.Headers.User, a.listener.Headers.Groups
	default:
		return "", "", false
	}
	return a.userFromHeaders(r, userHeader, groupsHeader)
}

// AuthFromRequest extracts or creates a session from the request.
//
// Identity source by auth mode:
//   - dev / headers: X-Dev-User-style headers (per request)
//   - oidc: session JWT (cookie or bearer), issued by the callback flow
//
// The session-JWT path is shared by every mode.
func (a *AuthService) AuthFromRequest(r *http.Request) (*model.User, string, error) {
	// 1. Header-based identity ("dev" and "headers" listeners)
	if a.mode() == config.AuthModeDev || a.mode() == config.AuthModeHeaders {
		username, role, mapped := a.identifyFromHeaders(r)
		if username != "" {
			if !mapped {
				// Fail closed: headers present but no recognized group.
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

	// 3. JWT cookie (session issued by the OIDC callback or a headers request)
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

// IssueToken creates a JWT for the given user.
func (a *AuthService) IssueToken(username string, role model.Role) (string, error) {
	claims := jwt.MapClaims{
		"username": username,
		"role":     string(role),
		"exp":      time.Now().Add(a.jwtTTL).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtSecret)
}

// ValidateToken parses and validates a JWT string.
func (a *AuthService) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func (a *AuthService) mode() config.AuthMode { return a.listener.Auth }

// LoginHandler initiates the OIDC Authorization Code flow with PKCE.
func (a *AuthService) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if a.oauth2Config == nil {
		http.Error(w, "This endpoint authenticates via the listener's auth mode; it has no OIDC login flow", http.StatusServiceUnavailable)
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
		http.Error(w, "This endpoint authenticates via the listener's auth mode; it has no OIDC callback", http.StatusServiceUnavailable)
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
		MaxAge: int(a.jwtTTL.Seconds()),
	})

	log.Printf("auth: user %s logged in (role=%s)", user.Username, user.Role)
	http.Redirect(w, r, "/", http.StatusFound)
}

// LogoutHandler clears the app session and redirects to the provider's
// end_session endpoint (only meaningful for OIDC listeners).
func (a *AuthService) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, "teamviz_token")

	if a.mode() != config.AuthModeOIDC {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	endSessionURL := strings.TrimSuffix(a.listener.OIDC.Issuer, "/") + "/protocol/openid-connect/logout"
	params := url.Values{}
	if a.listener.OIDC.ClientID != "" {
		params.Set("client_id", a.listener.OIDC.ClientID)
	}
	if a.postLogoutRedirect() != "" {
		params.Set("post_logout_redirect_uri", a.postLogoutRedirect())
	}
	logoutURL := endSessionURL
	if len(params) > 0 {
		logoutURL += "?" + params.Encode()
	}

	http.Redirect(w, r, logoutURL, http.StatusFound)
}

// postLogoutRedirect returns the configured post-logout redirect URL, or the
// web root of the OIDC redirect URL as the sensible default.
func (a *AuthService) postLogoutRedirect() string {
	if a.listener.OIDC.PostLogoutRedirectURL != "" {
		return a.listener.OIDC.PostLogoutRedirectURL
	}
	if u, err := url.Parse(a.listener.OIDC.RedirectURL); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/"
	}
	return ""
}

// ===== Helper types and functions =====

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

var ErrNoAuth = &authError{"no authentication provided"}

// ErrAccessDenied is returned when the user authenticated (dev/headers) but
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
