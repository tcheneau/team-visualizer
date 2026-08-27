# --- Build stage ---
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 go build -o /teamviz .

# --- Runtime stage ---
FROM alpine:3.20

# Standard public root CA bundle (Go picks it up from /etc/ssl/certs/ at runtime),
# plus update-ca-certificates so an extra CA can be baked in below.
RUN apk add --no-cache ca-certificates

# OPTIONAL — trust a private/internal root CA for the whole container (e.g. an
# on-prem OIDC provider behind a corporate CA):
#   docker build --build-arg EXTRA_CA_PEM="$(cat corp-root-ca.pem)" -t teamviz .
# The code-level alternative (no rebuild, OIDC-scoped) is TVZ_OIDC_CA_FILE / TVZ_OIDC_CA.
ARG EXTRA_CA_PEM=""
RUN if [ -n "$EXTRA_CA_PEM" ]; then \
        printf '%s\n' "$EXTRA_CA_PEM" > /usr/local/share/ca-certificates/extra-root-ca.crt && \
        update-ca-certificates; \
    fi

WORKDIR /app
COPY --from=builder /teamviz /app/teamviz

# The legacy HTML is embedded in the binary via go:embed
# If you want to override it at runtime, mount a volume at /app/web/legacy/

EXPOSE 8080

ENV TVZ_LISTEN=:8080
ENV TVZ_DB_PATH=/data/teamviz.db
ENV TVZ_JWT_SECRET=changeme

VOLUME ["/data"]

ENTRYPOINT ["/app/teamviz"]
