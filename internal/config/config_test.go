package config

import "testing"

func TestLoadOIDCCASettings(t *testing.T) {
	t.Setenv("TVZ_JWT_SECRET", "test-secret")
	t.Setenv("TVZ_DEV_MODE", "true")
	t.Setenv("TVZ_OIDC_CA_FILE", "/etc/certs/root-ca.pem, /etc/certs/intermediate-ca.pem ,")
	t.Setenv("TVZ_OIDC_CA", "-----BEGIN CERTIFICATE-----\ninline\n-----END CERTIFICATE-----")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.OIDCCAFiles) != 2 ||
		cfg.OIDCCAFiles[0] != "/etc/certs/root-ca.pem" ||
		cfg.OIDCCAFiles[1] != "/etc/certs/intermediate-ca.pem" {
		t.Fatalf("OIDCCAFiles = %q, want 2 trimmed paths", cfg.OIDCCAFiles)
	}
	if cfg.OIDCAInline == "" {
		t.Fatal("OIDCAInline not set")
	}
}