package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/teamviz/team-visualizer/internal/model"
)

type contextKey string

const (
	ctxUser  contextKey = "user"
	ctxToken contextKey = "token"
)

// Middleware authenticates every request via JWT or dev headers.
// It stores the user and token in the request context.
func (a *AuthService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, token, err := a.AuthFromRequest(r)
		if err != nil {
			if errors.Is(err, ErrAccessDenied) {
				// Authenticated, but no group maps to an application role.
				http.Error(w, `{"error":"forbidden","message":"account is not a member of any group that grants access"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// Set JWT cookie if we got a new token (from dev headers or OIDC callback)
		if cookie, _ := r.Cookie("teamviz_token"); cookie == nil || cookie.Value != token {
			http.SetCookie(w, &http.Cookie{
				Name:     "teamviz_token",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(a.jwtTTL.Seconds()),
			})
		}

		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxToken, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole returns middleware that checks the user has at least the given role.
// Role hierarchy: admin > normal > readonly.
func (a *AuthService) RequireRole(minRole model.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if !roleSatisfies(user.Role, minRole) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserFromContext(ctx context.Context) *model.User {
	if u, ok := ctx.Value(ctxUser).(*model.User); ok {
		return u
	}
	return nil
}

func TokenFromContext(ctx context.Context) string {
	if t, ok := ctx.Value(ctxToken).(string); ok {
		return t
	}
	return ""
}

// roleSatisfies returns true if the user's role meets the minimum required role.
func roleSatisfies(user, min model.Role) bool {
	level := func(r model.Role) int {
		switch r {
		case model.RoleAdmin:
			return 3
		case model.RoleNormal:
			return 2
		case model.RoleReadonly:
			return 1
		default:
			return 0
		}
	}
	return level(user) >= level(min)
}
