package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/teamviz/team-visualizer/internal/config"
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

// ---- tests ----

// Default (system roots only) must reject a cert from an unknown CA,
// while TVZ_OIDC_CA (inline PEM) must make it trusted.
func TestOIDCClientTrustsInlineCA(t *testing.T) {
	ca := newTestCA(t)
	baseURL := startTLSServer(t, ca.issueServerCert(t, net.ParseIP("127.0.0.1")))

	if err := doGet(t, http.DefaultClient, baseURL); err == nil {
		t.Fatal("expected TLS failure with system roots only, got nil")
	} else {
		t.Logf("default client error (expected): %v", err)
	}

	cfg := &config.Config{OIDCAInline: string(ca.pem)}
	client, err := buildOIDCHTTPClient(cfg)
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

	cfg := &config.Config{OIDCCAFiles: []string{caPath}}
	client, err := buildOIDCHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with CA file failed: %v", err)
	}
}

func TestOIDCClientAcceptsBase64EncodedCA(t *testing.T) {
	ca := newTestCA(t)
	baseURL := startTLSServer(t, ca.issueServerCert(t, net.ParseIP("127.0.0.1")))

	cfg := &config.Config{OIDCAInline: base64.StdEncoding.EncodeToString(ca.pem)}
	client, err := buildOIDCHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with base64 CA failed: %v", err)
	}
}

func TestOIDCClientRejectsInvalidCAData(t *testing.T) {
	cfg := &config.Config{OIDCAInline: "definitely-not-a-certificate"}
	if _, err := buildOIDCHTTPClient(cfg); err == nil {
		t.Fatal("expected error for invalid CA data")
	}
}

func TestOIDCClientRejectsMissingCAFile(t *testing.T) {
	cfg := &config.Config{OIDCCAFiles: []string{"/nonexistent/root-ca.pem"}}
	if _, err := buildOIDCHTTPClient(cfg); err == nil {
		t.Fatal("expected error for missing CA file")
	}
}
// TestDemoCertsIntegration validates the PKI produced by
// scripts/generate-demo-certs.sh (the docker-compose demo certificates)
// against the real OIDC client: system roots must reject the demo leaf,
// the demo root CA via TVZ_OIDC_CA_FILE must accept it.
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

	cfg := &config.Config{OIDCCAFiles: []string{filepath.Join(dir, "root-ca.pem")}}
	client, err := buildOIDCHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildOIDCHTTPClient: %v", err)
	}
	if err := doGet(t, client, baseURL); err != nil {
		t.Fatalf("request with demo root CA failed: %v", err)
	}
}
