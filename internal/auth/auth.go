package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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

	// Build custom HTTP client if internal host is set (Docker networking)
	httpClt := http.DefaultClient
	if cfg.OIDCInternalHost != "" {
		issuerURL, err := url.Parse(cfg.OIDCIssuer)
		if err != nil {
			log.Fatalf("auth: invalid OIDC issuer URL: %v", err)
		}
		issuerHost := issuerURL.Host
		internalHost := cfg.OIDCInternalHost
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr == issuerHost {
					addr = internalHost
				}
				return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
			},
		}
		httpClt = &http.Client{Transport: transport}
		log.Printf("auth: OIDC internal host rewrite %s -> %s", issuerHost, internalHost)
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

// mapGroupsToRole maps Keycloak group names to app roles.
func (a *AuthService) mapGroupsToRole(groups []string) model.Role {
	role := model.RoleReadonly
	for _, g := range groups {
		switch g {
		case a.cfg.AdminGroup:
			role = model.RoleAdmin
		case a.cfg.NormalGroup:
			if role != model.RoleAdmin {
				role = model.RoleNormal
			}
		}
	}
	return role
}

// extractUserFromDevHeaders reads dev-mode headers and returns (username, role).
func (a *AuthService) extractUserFromDevHeaders(r *http.Request) (string, model.Role) {
	username := r.Header.Get("X-Dev-User")
	if username == "" {
		return "", ""
	}
	groups := splitGroups(r.Header.Get("X-Dev-Groups"))
	return username, a.mapGroupsToRole(groups)
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
		if username, role := a.extractUserFromDevHeaders(r); username != "" {
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
		Groups           []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "failed to parse claims: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if claims.PreferredUsername == "" {
		http.Error(w, "no preferred_username in token", http.StatusInternalServerError)
		return
	}

	role := a.mapGroupsToRole(claims.Groups)

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
