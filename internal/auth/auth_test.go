package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
)

// ---- certificate test helpers ----

type testCA struct {
	pem  []byte
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
}

// newTestCA creates a self-signed root CA that is NOT in the system trust store.
func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "teamviz-test-root-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  key,
		cert: cert,
	}
}

// issueServerCert issues a leaf TLS certificate for 127.0.0.1 signed by the CA.
func (ca *testCA) issueServerCert(t *testing.T, ip net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal server key: %v", err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("build tls.Certificate: %v", err)
	}
	return cert
}

// startTLSServer runs an HTTPS server whose certificate is signed by a test CA
// (i.e. untrusted by the system root store). Returns the https:// base URL.
func startTLSServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	go func() { _ = srv.Serve(tls.NewListener(ln, srv.TLSConfig)) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return "https://" + ln.Addr().String()
}

func doGet(t *testing.T, client *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return nil
}

// ---- OIDC client TLS trust tests ----

// Default (system roots only) must reject a cert from an unknown CA,
// while oidc.ca (inline PEM) must make it trusted.
func TestOIDCClientTrustsInlineCA(t *testing.T) {
	ca := newTestCA(t)
	baseURL := startTLSServer(t, ca.issueServerCert(t, net.ParseIP("127.0.0.1")))

	if err := doGet(t, http.DefaultClient, baseURL); err == nil {
		t.Fatal("expected TLS failure with system roots only, got nil")
	} else {
		t.Logf("default client error (expected): %v", err)
	}

	client, err := buildOIDCHTTPClient("test", &config.OIDC{CA: string(ca.pem)})
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with inline CA failed: %v", err)
	}
}

func TestOIDCClientTrustsCAFile(t *testing.T) {
	ca := newTestCA(t)
	baseURL := startTLSServer(t, ca.issueServerCert(t, net.ParseIP("127.0.0.1")))

	caPath := filepath.Join(t.TempDir(), "root-ca.pem")
	if err := os.WriteFile(caPath, ca.pem, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}

	client, err := buildOIDCHTTPClient("test", &config.OIDC{CAFile: caPath})
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with CA file failed: %v", err)
	}
}

func TestOIDCClientRejectsInvalidCAData(t *testing.T) {
	if _, err := buildOIDCHTTPClient("test", &config.OIDC{CA: "definitely-not-a-certificate"}); err == nil {
		t.Fatal("expected error for invalid CA data")
	}
}

func TestOIDCClientRejectsMissingCAFile(t *testing.T) {
	if _, err := buildOIDCHTTPClient("test", &config.OIDC{CAFile: "/nonexistent/root-ca.pem"}); err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

// ---- role mapping (fail closed) ----

func TestMapGroupsToRole(t *testing.T) {
	svc := &AuthService{roles: config.RoleMapping{
		Admin: "tvz-admin", Normal: "tvz-normal", Readonly: "tvz-readonly",
	}}
	cases := []struct {
		name   string
		groups []string
		want   model.Role
		wantOK bool
	}{
		{"no groups claim", nil, "", false},
		{"empty groups claim", []string{}, "", false},
		{"only unknown groups", []string{"some-other-app", "random"}, "", false},
		{"readonly group maps explicitly", []string{"tvz-readonly"}, model.RoleReadonly, true},
		{"normal group", []string{"tvz-normal"}, model.RoleNormal, true},
		{"admin group", []string{"tvz-admin"}, model.RoleAdmin, true},
		{"readonly never downgrades admin", []string{"tvz-admin", "tvz-readonly"}, model.RoleAdmin, true},
		{"normal beats readonly", []string{"tvz-readonly", "tvz-normal"}, model.RoleNormal, true},
		{"normal never downgrades admin", []string{"tvz-admin", "tvz-normal"}, model.RoleAdmin, true},
		{"unknown alongside recognized", []string{"unknown", "tvz-readonly"}, model.RoleReadonly, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, ok := svc.mapGroupsToRole(tc.groups)
			if ok != tc.wantOK || (ok && role != tc.want) {
				t.Fatalf("mapGroupsToRole(%v) = (%q, %v), want (%q, %v)", tc.groups, role, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// Dev-header requests with no recognized group must be denied (fail closed)
// and the user must not be persisted; a recognized readonly group maps
// explicitly.
func TestAuthFromRequestDevDeniesUnmappedGroups(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer db.Close()

	svc := New(config.Listener{
		Name:    "dev-test",
		Listen:  ":0",
		Auth:    config.AuthModeDev,
		Headers: config.Headers{User: "X-Dev-User", Groups: "X-Dev-Groups"},
		Roles: config.RoleMapping{
			Admin: "tvz-admin", Normal: "tvz-normal", Readonly: "tvz-readonly",
		},
	}, config.Server{JWTSecret: "test-secret", JWTTTL: time.Hour}, db)

	// Unrecognized group → denied, no user row created.
	req := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	req.Header.Set("X-Dev-User", "mallory")
	req.Header.Set("X-Dev-Groups", "unrelated-group")
	if _, _, err := svc.AuthFromRequest(req); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("want ErrAccessDenied, got %v", err)
	}
	if _, err := db.GetUser("mallory"); err == nil {
		t.Fatal("denied user must not be persisted")
	}
}

// "headers" mode reads the listener-configured header names and maps roles.
func TestAuthFromRequestHeadersMode(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer db.Close()

	svc := New(config.Listener{
		Name:   "kerberos-test",
		Listen: "127.0.0.1:0",
		Auth:   config.AuthModeHeaders,
		Headers: config.Headers{
			User: "X-Remote-User", Groups: "X-Remote-Groups",
		},
		Roles: config.RoleMapping{
			Admin: "server-admins", Normal: "staff", Readonly: "all-staff",
		},
	}, config.Server{JWTSecret: "test-secret", JWTTTL: time.Hour}, db)

	// No headers at all → no authentication (401 upstream), NOT a denial.
	req := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	if _, _, err := svc.AuthFromRequest(req); !errors.Is(err, ErrNoAuth) {
		t.Fatalf("want ErrNoAuth for headerless request, got %v", err)
	}

	// Mapped headers (Kerberos/Lua style) → readonly session.
	req2 := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	req2.Header.Set("X-Remote-User", "jdoe")
	req2.Header.Set("X-Remote-Groups", "other-staff,all-staff")
	user, _, err := svc.AuthFromRequest(req2)
	if err != nil {
		t.Fatalf("headers auth failed: %v", err)
	}
	if user.Role != model.RoleReadonly {
		t.Fatalf("role = %q, want readonly", user.Role)
	}

	// Mapped admin group wins over readonly.
	req3 := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	req3.Header.Set("X-Remote-User", "chief")
	req3.Header.Set("X-Remote-Groups", "all-staff,server-admins")
	user3, _, err := svc.AuthFromRequest(req3)
	if err != nil {
		t.Fatalf("headers auth failed: %v", err)
	}
	if user3.Role != model.RoleAdmin {
		t.Fatalf("role = %q, want admin", user3.Role)
	}

	// Present headers but no recognized group → denied.
	req4 := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	req4.Header.Set("X-Remote-User", "intruder")
	req4.Header.Set("X-Remote-Groups", "not-ours")
	if _, _, err := svc.AuthFromRequest(req4); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("want ErrAccessDenied for unmapped headers user, got %v", err)
	}
	if _, err := db.GetUser("intruder"); err == nil {
		t.Fatal("denied user must not be persisted")
	}
}

func TestRenderAccessDeniedPage(t *testing.T) {
	svc := &AuthService{roles: config.RoleMapping{
		Admin: "tvz-admin", Normal: "tvz-normal", Readonly: "tvz-readonly",
	}}
	rec := httptest.NewRecorder()
	svc.renderAccessDenied(rec, `<script>alert(1)</script>`, []string{"weird & group"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("username not HTML-escaped")
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("raw script tag leaked into page")
	}
	for _, want := range []string{"tvz-admin", "tvz-normal", "tvz-readonly", "weird &amp; group", "/auth/logout", "/auth/login"} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}

// TestDemoCertsIntegration validates the PKI produced by
// scripts/generate-demo-certs.sh (the docker-compose demo certificates)
// against the real OIDC client: system roots must reject the demo leaf,
// the demo root CA via oidc.ca_file must accept it.
//
// Enable with: CERTS_DIR=/path/to/certs go test ./internal/auth -run TestDemoCertsIntegration
func TestDemoCertsIntegration(t *testing.T) {
	dir := os.Getenv("CERTS_DIR")
	if dir == "" {
		t.Skip("CERTS_DIR not set (generate certs via scripts/generate-demo-certs.sh to enable)")
	}

	leaf, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		t.Fatalf("load demo leaf cert: %v", err)
	}
	// Reach the server via its 127.0.0.1 IP SAN (present on the demo leaf). The
	// demo issuer URL's "localhost" hostname resolves to 127.0.0.1 in real
	// environments; DNS may not resolve here, so use the IP directly.
	baseURL := startTLSServer(t, leaf)

	if err := doGet(t, http.DefaultClient, baseURL); err == nil {
		t.Fatal("expected system roots to reject the demo leaf certificate")
	} else {
		t.Logf("system roots error (expected): %v", err)
	}

	client, err := buildOIDCHTTPClient("demo", &config.OIDC{CAFile: filepath.Join(dir, "root-ca.pem")})
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with demo root CA failed: %v", err)
	}
}
