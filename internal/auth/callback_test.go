package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
)

// mockIDP is a minimal OIDC provider used to drive CallbackHandler end to
// end: discovery document + JWKS + a token endpoint that returns the
// currently staged ID token (RS256, signed by a throwaway key).
type mockIDP struct {
	t      *testing.T
	server *httptest.Server
	issuer string
	key    *rsa.PrivateKey
	idTok  atomic.Value // string
}

func newMockIDP(t *testing.T) *mockIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	m := &mockIDP{t: t, key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/realms/teamviz/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 m.issuer,
			"authorization_endpoint": m.issuer + "/protocol/openid-connect/auth",
			"token_endpoint":         m.issuer + "/token",
			"jwks_uri":               m.issuer + "/certs",
		})
	})
	mux.HandleFunc("/realms/teamviz/token", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := m.idTok.Load().(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"id_token":     tok,
		})
	})
	mux.HandleFunc("/realms/teamviz/certs", func(w http.ResponseWriter, r *http.Request) {
		pub := m.key.Public().(*rsa.PublicKey)
		n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","use":"sig","kid":"test-key","alg":"RS256","n":"` + n + `","e":"` + e + `"}]}`))
	})
	m.server = httptest.NewServer(mux)
	m.issuer = m.server.URL + "/realms/teamviz"
	t.Cleanup(m.server.Close)
	return m
}

// issue signs and stages the ID token returned by the token endpoint.
func (m *mockIDP) issue(claims map[string]any) {
	m.t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	payload, err := json.Marshal(claims)
	if err != nil {
		m.t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, sum[:])
	if err != nil {
		m.t.Fatalf("sign id token: %v", err)
	}
	m.idTok.Store(signingInput + "." + base64.RawURLEncoding.EncodeToString(sig))
}

// TestCallbackHandlerRoleFlow drives the OIDC callback end to end against a
// mock provider: a user with no recognized group is denied (403, deny page,
// stale session cookie evicted, nothing persisted) while a tvz-readonly
// member is mapped and issued a readonly session.
func TestCallbackHandlerRoleFlow(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer db.Close()

	idp := newMockIDP(t)
	cfg := &config.Config{
		JWTTTL:           time.Hour,
		JWTSecret:        []byte("test-secret"),
		OIDCIssuer:       idp.issuer,
		OIDCClientID:     "teamviz-demo",
		OIDCClientSecret: "shh",
		OIDCRedirectURL:  "http://localhost:8080/auth/callback",
		AdminGroup:       "tvz-admin",
		NormalGroup:      "tvz-normal",
		ReadonlyGroup:    "tvz-readonly",
	}
	svc := New(cfg, db)

	doCallback := func(username string, groups []string) *httptest.ResponseRecorder {
		idp.issue(map[string]any{
			"iss":                idp.issuer,
			"sub":                username,
			"aud":                "teamviz-demo",
			"exp":                time.Now().Add(time.Hour).Unix(),
			"iat":                time.Now().Unix(),
			"preferred_username": username,
			"groups":             groups,
		})
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?state=st&code=the-code", nil)
		req.AddCookie(&http.Cookie{Name: "teamviz_oauth_state", Value: "st"})
		req.AddCookie(&http.Cookie{Name: "teamviz_oauth_verifier", Value: "vf"})
		rec := httptest.NewRecorder()
		svc.CallbackHandler(rec, req)
		return rec
	}

	t.Run("user without recognized group is denied", func(t *testing.T) {
		rec := doCallback("nouser", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body: %.200s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Access denied") {
			t.Fatal("deny page not served")
		}
		// A stale session cookie must be evicted by a denied login.
		cleared := false
		for _, c := range rec.Result().Cookies() {
			if c.Name == "teamviz_token" && c.MaxAge <= 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Fatalf("teamviz_token not cleared on denial (cookies: %v)", rec.Result().Cookies())
		}
		if _, err := db.GetUser("nouser"); err == nil {
			t.Fatal("denied user must not be persisted")
		}
	})

	t.Run("readonly member is mapped and logged in", func(t *testing.T) {
		rec := doCallback("rouser", []string{"tvz-readonly"})
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (body: %s)", rec.Code, rec.Body.String())
		}
		var session *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "teamviz_token" && c.Value != "" {
				session = c
			}
		}
		if session == nil {
			t.Fatal("no session cookie issued")
		}
		claims, err := svc.ValidateToken(session.Value)
		if err != nil {
			t.Fatalf("session JWT invalid: %v", err)
		}
		if claims.Role != string(model.RoleReadonly) {
			t.Fatalf("role = %q, want readonly", claims.Role)
		}
		user, err := db.GetUser("rouser")
		if err != nil {
			t.Fatalf("user not persisted: %v", err)
		}
		if user.Role != model.RoleReadonly {
			t.Fatalf("persisted role = %q, want readonly", user.Role)
		}
	})
}
