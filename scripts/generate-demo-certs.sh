#!/bin/sh
# generate-demo-certs.sh — Ephemeral demo PKI for docker-compose.demo.yml.
#
# Generates, into the shared "demo-certs" volume:
#   root-ca.pem / root-ca.key — demo root CA (ECDSA P-256, CA:TRUE, 10y)
#   tls.crt    / tls.key      — server cert for Keycloak, signed by the root
#
# The server cert carries SANs "DNS:localhost, DNS:keycloak, IP:127.0.0.1":
#   - browsers and the app reach Keycloak via https://localhost:8443 (the issuer
#     URL host, which is what Go validates the certificate against, even though
#     TVZ_OIDC_INTERNAL_HOST rewrites the TCP dial to keycloak:8443),
#   - "keycloak" covers clients that use the Docker-internal name directly.
#
# The teamviz app trusts the demo root via TVZ_OIDC_CA_FILE — the same
# mechanism a production deployment uses for its private/internal CA.
#
# Idempotent: skips generation when the certificates already exist, so this is
# safe on every `docker compose up`.

set -eu

CERTS_DIR="${CERTS_DIR:-/certs}"
CA_DAYS="${CA_DAYS:-3650}"     # demo root: throwaway, host-local, so 10y is fine
LEAF_DAYS="${LEAF_DAYS:-90}"

if [ -s "$CERTS_DIR/root-ca.pem" ] && [ -s "$CERTS_DIR/tls.crt" ] && [ -s "$CERTS_DIR/tls.key" ]; then
    echo "certs: demo certificates already present in $CERTS_DIR — skipping"
    exit 0
fi

# openssl is not part of the plain alpine base image
command -v openssl >/dev/null 2>&1 || apk add --no-cache openssl >/dev/null

mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"
umask 077

echo "certs: generating demo root CA (CN=teamviz-demo-root-ca, ${CA_DAYS}d) ..."
openssl ecparam -name prime256v1 -genkey -noout -out root-ca.key
openssl req -new -x509 \
    -key root-ca.key -sha256 -days "$CA_DAYS" \
    -subj "/O=TeamViz Demo/CN=teamviz-demo-root-ca" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -out root-ca.pem

echo "certs: issuing server certificate for Keycloak (SANs: localhost, keycloak, 127.0.0.1) ..."
openssl ecparam -name prime256v1 -genkey -noout -out tls.key
openssl req -new \
    -key tls.key \
    -subj "/O=TeamViz Demo/CN=localhost" \
    -out tls.csr

openssl x509 -req \
    -in tls.csr -CA root-ca.pem -CAkey root-ca.key -CAcreateserial \
    -days "$LEAF_DAYS" -sha256 \
    -out tls.crt \
    -extfile - <<EXT
subjectAltName = DNS:localhost, DNS:keycloak, IP:127.0.0.1
keyUsage = critical, digitalSignature
extendedKeyUsage = serverAuth
EXT

rm -f tls.csr root-ca.srl

# The Keycloak image runs as uid 1000 — let it read the key/cert. Fall back to
# owner-only permissions when chown is unavailable (e.g. local testing).
chown 1000:0 tls.key tls.crt root-ca.pem 2>/dev/null || true
chmod 640 tls.key
chmod 644 tls.crt root-ca.pem

echo "certs: done — root CA: $CERTS_DIR/root-ca.pem, server cert: $CERTS_DIR/tls.crt"