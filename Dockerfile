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

# OPTIONAL — trust a private/internal root CA for the whole container. Prefer
# the TOML-level option instead: [listener.oidc] ca_file / ca in the config.
ARG EXTRA_CA_PEM=""
RUN if [ -n "$EXTRA_CA_PEM" ]; then \
        printf '%s\n' "$EXTRA_CA_PEM" > /usr/local/share/ca-certificates/extra-root-ca.crt && \
        update-ca-certificates; \
    fi

WORKDIR /app
COPY --from=builder /teamviz /app/teamviz

# Pre-create the config directory: podman/crun will NOT create missing parent
# directories when bind-mounting a single config file (docker does), e.g.:
#   volumes: ["./teamviz.toml:/etc/teamviz/teamviz.toml:ro"]
RUN mkdir -p /etc/teamviz

# The legacy HTML is embedded in the binary via go:embed
# If you want to override it at runtime, mount a volume at /app/web/legacy/

EXPOSE 8080

# All configuration comes from a TOML file:
#   docker run -v $PWD/teamviz.toml:/etc/teamviz/teamviz.toml -p 8080:8080 \
#     teamviz -config /etc/teamviz/teamviz.toml
VOLUME ["/data"]

ENTRYPOINT ["/app/teamviz"]