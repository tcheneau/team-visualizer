# Team Activity Visualizer

A self-hosted web application for planning and visualizing a team's activity across away time, project time, and run time.

## Quick Start

### Run locally (development)

```bash
# Set required JWT secret
export TVZ_JWT_SECRET=$(openssl rand -hex 32)

# Run
go run .
```

The server starts on `http://localhost:8080`.

Without a reverse proxy, API endpoints require auth headers. Test with:

```bash
# Admin user
curl -s -H "X-Forwarded-User: jdoe" -H "X-Forwarded-Groups: admin" \
  http://localhost:8080/api/auth/session | jq .

# Read-only user
curl -s -H "X-Forwarded-User: bob" -H "X-Forwarded-Groups: readonly" \
  http://localhost:8080/api/auth/session | jq .
```

### Docker

```bash
# Build and run
docker build -t teamviz .
docker run -p 8080:8080 \
  -e TVZ_JWT_SECRET=$(openssl rand -hex 32) \
  -v teamviz-data:/data \
  teamviz

# Or with docker-compose
TVZ_JWT_SECRET=$(openssl rand -hex 32) docker-compose up -d
```

### Production deployment

The app runs **behind a reverse proxy** that handles authentication. The proxy must forward:

| Header                | Value                              |
|-----------------------|------------------------------------|
| `X-Forwarded-User`    | Username (e.g. `jdoe`)             |
| `X-Forwarded-Groups`  | Comma-separated groups (e.g. `admin`) |

#### Traefik example

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.teamviz.rule=Host(`teamviz.example.com`)"
  - "traefik.http.routers.teamviz.middlewares=authelia@docker"
```

#### Nginx + auth_request example

```nginx
location / {
    auth_request /auth;
    auth_request_set $user $upstream_http_x_forwarded_user;
    auth_request_set $groups $upstream_http_x_forwarded_groups;
    proxy_set_header X-Forwarded-User $user;
    proxy_set_header X-Forwarded-Groups $groups;
    proxy_pass http://localhost:8080;
}
```

## Configuration

| Variable                  | Default              | Description                          |
|---------------------------|----------------------|--------------------------------------|
| `TVZ_LISTEN`              | `:8080`              | Listen address                       |
| `TVZ_DB_PATH`             | `teamviz.db`         | SQLite database file path            |
| `TVZ_JWT_SECRET`          | *(required)*         | Secret for signing JWT tokens        |
| `TVZ_JWT_TTL`             | `24h`                | JWT token lifetime                   |
| `TVZ_PROXY_HEADER_USER`   | `X-Forwarded-User`   | Header containing the username       |
| `TVZ_PROXY_HEADER_GROUPS` | `X-Forwarded-Groups` | Header containing user groups/roles  |
| `TVZ_ADMIN_GROUP`         | `admin`              | Group name mapping to admin role     |
| `TVZ_NORMAL_GROUP`        | `normal`             | Group name mapping to normal role    |
| `TVZ_READONLY_GROUP`      | `readonly`           | Group name mapping to read-only role |
| `TVZ_WS_ENABLED`          | `true`               | Enable WebSocket real-time updates   |

## Roles & Permissions

| Action                        | Admin | Normal | Read-Only |
|-------------------------------|:-----:|:------:|:---------:|
| View all views                |  ✅   |  ✅    |    ✅     |
| Edit planning                |  ✅   |  ✅    |    ❌     |
| Add/edit/archive people      |  ✅   |  ✅    |    ❌     |
| Add/edit projects            |  ✅   |  ✅    |    ❌     |
| Change settings              |  ✅   |  ❌    |    ❌     |
| Import TOML / CSV            |  ✅   |  ❌    |    ❌     |
| Export TOML                  |  ✅   |  ✅    |    ✅     |
| Prune / reset data           |  ✅   |  ❌    |    ❌     |

## API

All endpoints under `/api/`. Auth via JWT (cookie or `Authorization: Bearer <token>`).

### Core endpoints

| Method | Path                     | Description                  | Role    |
|--------|--------------------------|------------------------------|---------|
| GET    | `/api/health`            | Health check                 | any     |
| GET    | `/api/auth/session`      | Current user + JWT           | any     |
| GET    | `/api/people`            | List all people              | any     |
| POST   | `/api/people`            | Add person                   | normal+ |
| PUT    | `/api/people/:id`        | Update person                | normal+ |
| DELETE | `/api/people/:id`        | Delete person                | normal+ |
| POST   | `/api/people/:id/archive`| Archive person               | normal+ |
| GET    | `/api/planning`          | Get planning (date range)    | any     |
| PUT    | `/api/planning/slot`     | Set a half-day slot          | normal+ |
| PUT    | `/api/planning/range`    | Set a range of slots         | normal+ |
| GET    | `/api/projects`          | List all projects            | any     |
| POST   | `/api/projects`          | Add project                  | normal+ |
| POST   | `/api/projects/import-csv`| Import CSV                  | admin   |
| GET    | `/api/settings`          | Get settings                 | any     |
| PUT    | `/api/settings`          | Update settings              | admin   |
| GET    | `/api/oncall`            | Get on-call                  | any     |
| PUT    | `/api/oncall`            | Set on-call                  | normal+ |
| GET    | `/api/rotation`          | Get rotation                 | any     |
| POST   | `/api/rotation/assign`   | Assign run person            | normal+ |
| GET    | `/api/export`            | Export all data as TOML      | any     |
| POST   | `/api/import`            | Import TOML                  | admin   |
| GET    | `/api/ws`                | WebSocket (real-time)        | any     |

## WebSocket

Real-time updates via WebSocket at `/api/ws`. Events broadcast on all mutations:

- `person_added`, `person_updated`, `person_deleted`
- `planning_updated`, `planning_range`, `planning_copied`
- `project_added`, `project_updated`, `project_deleted`
- `oncall_changed`, `rotation_changed`
- `settings_updated`, `data_imported`, `data_reset`

Connection auto-reconnects with 3-second backoff.

## Legacy app

The original single-file HTML5 prototype is served at `/legacy/` for prototyping. It uses browser localStorage (not the Go backend's database).

## Testing

```bash
# Build
go build -o /tmp/teamviz .

# Run end-to-end tests
TVZ_JWT_SECRET=testsecret ./test-api.sh
```

## Project structure

```
main.go                      — Entry point, server setup
internal/
  config/   — Environment configuration
  auth/     — Reverse proxy auth → JWT, role middleware
  model/    — Domain structs
  store/    — SQLite data access + migrations
  api/      — REST API handlers (read + write)
  ws/       — WebSocket hub for real-time
  toml/     — TOML serialize/parse (pelletier/go-toml/v2)
web/
  embed.go  — go:embed for assets + SPA handler
  assets/   — SPA frontend (index.html, app.js, styles.css)
  legacy/   — Legacy HTML5 app (served at /legacy/)
dist/       — Original single-file HTML5 app
Dockerfile  — Multi-stage build
docker-compose.yml — Easy local deployment
test-api.sh — End-to-end API test script
```

## Build

```bash
go build -o teamviz .
```
## Demo with Keycloak (Docker Compose)

A full demo setup with Keycloak authentication is available:

```bash
docker-compose -f docker-compose.demo.yml up -d
```

Wait ~40s for Keycloak to start and provision, then open http://localhost:8080.

### Demo users (password = username)

| Username | Password | Keycloak Group  | App Role    |
|----------|----------|-----------------|-------------|
| `admin`  | `admin`  | `tvz-admin`     | admin       |
| `user`   | `user`   | `tvz-normal`    | normal      |
| `rouser` | `rouser` | `tvz-readonly`  | read-only   |

### Architecture

```
Browser → oauth2-proxy (:8080) → Team Visualizer (:8080 internal)
              ↓
         Keycloak (:8090) — OIDC provider
```

- **Keycloak** — OIDC identity provider with pre-provisioned realm, users, groups, and client
- **oauth2-proxy** — handles the OAuth2/OIDC flow, sets `X-Forwarded-User` and `X-Forwarded-Groups` headers
- **Team Visualizer** — Go app that trusts the proxy headers and maps groups to roles

### Keycloak admin console

Accessible at http://localhost:8090 (admin/admin). The provisioned realm is `teamviz`.

### What the init script does

`scripts/init-keycloak.sh` runs once at startup and:
1. Creates the `teamviz` realm
2. Creates 3 groups: `tvz-admin`, `tvz-normal`, `tvz-readonly`
3. Creates 3 users and assigns them to the appropriate group
4. Creates an OIDC client (`teamviz-demo`) for oauth2-proxy
5. Creates protocol mappers so groups and preferred_username appear in tokens
