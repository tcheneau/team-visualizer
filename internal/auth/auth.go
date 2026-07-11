package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
)

type AuthService struct {
	cfg *config.Config
	db  *store.Store
}

func New(cfg *config.Config, db *store.Store) *AuthService {
	return &AuthService{cfg: cfg, db: db}
}

// extractUserFromProxy reads reverse proxy headers and returns (username, role).
func (a *AuthService) extractUserFromProxy(r *http.Request) (string, model.Role) {
	username := r.Header.Get(a.cfg.ProxyHeaderUser)
	if username == "" {
		return "", ""
	}

	groups := r.Header.Get(a.cfg.ProxyHeaderGroups)
	groupList := splitGroups(groups)

	role := model.RoleReadonly
	for _, g := range groupList {
		switch g {
		case a.cfg.AdminGroup:
			role = model.RoleAdmin
		case a.cfg.NormalGroup:
			if role != model.RoleAdmin {
				role = model.RoleNormal
			}
		}
	}

	return username, role
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
// Priority: proxy headers > Bearer JWT > JWT cookie.
//
// The app runs behind oauth2-proxy, which sets the X-Forwarded-* headers
// fresh on every authenticated request from the current IdP session. Treating
// them as authoritative prevents a stale teamviz_token cookie (issued during
// an earlier login as a different user) from freezing the identity/role —
// which otherwise let a signed-out user keep their old role (e.g. a "readonly"
// user still able to edit) and show the wrong username.
func (a *AuthService) AuthFromRequest(r *http.Request) (*model.User, string, error) {
	// 1. Proxy headers (authoritative when present)
	if username, role := a.extractUserFromProxy(r); username != "" {
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

	// 2. Bearer JWT (direct API calls without the proxy)
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
// and matches the given identity; otherwise issues a fresh JWT. Reusing a
// matching token avoids re-issuing and re-cookieing on every single request,
// while a mismatch (user switched accounts) issues a fresh token so the
// middleware overwrites the stale cookie.
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
