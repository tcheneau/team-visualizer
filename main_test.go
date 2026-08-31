package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/teamviz/team-visualizer/internal/auth"
	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/store"
	"github.com/teamviz/team-visualizer/internal/ws"
)

// TestMultiListenerRuntime builds two listeners with different auth modes on a
// shared store + hub and verifies that each enforces its own auth profile:
//   - a "dev" listener authenticates via X-Dev-User / X-Dev-Groups
//   - a "headers" listener authenticates via its configured proxy headers
//   - a session JWT issued on one listener is honored by the other
//     (same TLS/JWT secret — SSO-like behavior across auth environments)
func TestMultiListenerRuntime(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer db.Close()

	hub := ws.NewHub()
	go hub.Run()

	server := config.Server{JWTSecret: "test-secret", JWTTTL: time.Hour}

	devListener := config.Listener{
		Name: "dev", Listen: "127.0.0.1:0", Auth: config.AuthModeDev,
		Headers: config.Headers{User: "X-Dev-User", Groups: "X-Dev-Groups"},
		Roles:   config.RoleMapping{Admin: "tvz-admin", Normal: "tvz-normal", Readonly: "tvz-readonly"},
	}
	kerbListener := config.Listener{
		Name: "kerberos", Listen: "127.0.0.1:0", Auth: config.AuthModeHeaders,
		Headers: config.Headers{User: "X-Remote-User", Groups: "X-Remote-Groups"},
		Roles:   config.RoleMapping{Admin: "server-admins", Normal: "staff", Readonly: "all-staff"},
	}

	// Example files shipped in the repo must stay valid: parse them here.
	t.Run("shipped example configs parse", func(t *testing.T) {
		for _, name := range []string{"teamviz.demo.toml", "teamviz.example.toml"} {
			t.Setenv("TEAMVIZ_JWT_SECRET", "test")
			t.Setenv("KC_CORP_CLIENT_SECRET", "test")
			t.Setenv("KC_PARTNER_CLIENT_SECRET", "test")
			if _, err := config.Load(name); err != nil {
				t.Errorf("%s does not parse: %v", name, err)
			}
		}
	})

	devHandler := buildRouter(auth.New(devListener, server, db), db, hub)
	kerbHandler := buildRouter(auth.New(kerbListener, server, db), db, hub)
	devSrv := httptest.NewServer(devHandler)
	defer devSrv.Close()
	kerbSrv := httptest.NewServer(kerbHandler)
	defer kerbSrv.Close()

	sessionRole := func(t *testing.T, srv *httptest.Server, headers []string, cookie *http.Cookie) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/session", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			User struct{ Role string }
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.User.Role
	}

	// Health is public on every listener.
	for name, srv := range map[string]*httptest.Server{"dev": devSrv, "kerberos": kerbSrv} {
		resp, err := http.Get(srv.URL + "/api/health")
		if err != nil {
			t.Fatalf("%s health: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s health status = %d", name, resp.StatusCode)
		}
	}

	// Dev listener: X-Dev headers → admin role (its own role vocabulary).
	if role := sessionRole(t, devSrv, []string{"X-Dev-User", "alice", "X-Dev-Groups", "tvz-admin"}, nil); role != "admin" {
		t.Errorf("dev listener role = %q, want admin", role)
	}

	// Kerberos listener: custom headers, own role vocabulary.
	if role := sessionRole(t, kerbSrv, []string{"X-Remote-User", "jdoe", "X-Remote-Groups", "all-staff"}, nil); role != "readonly" {
		t.Errorf("kerberos listener role = %q, want readonly", role)
	}
	if role := sessionRole(t, kerbSrv, []string{"X-Remote-User", "root", "X-Remote-Groups", "server-admins"}, nil); role != "admin" {
		t.Errorf("kerberos listener role = %q, want admin", role)
	}

	// Session issued on the Kerberos listener authenticated at the dev
	// listener too (shared token secret → portable session).
	req, _ := http.NewRequest(http.MethodGet, devSrv.URL+"/api/auth/session", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Both users ended up in the shared store.
	if _, err := db.GetUser("jdoe"); err != nil {
		t.Errorf("user jdoe missing from shared store: %v", err)
	}
	if _, err := db.GetUser("root"); err != nil {
		t.Errorf("user root missing from shared store: %v", err)
	}
}
