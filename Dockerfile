# --- Build stage ---
FROM golang:1.22-alpine AS builder

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

RUN apk add --no-cache ca-certificates

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