# Team Activity Visualizer

A self-hosted web application for planning and visualising a team's activity across away time, project time, run time, and remote work — with real-time collaboration, role-based access, and a full Keycloak + oauth2-proxy demo setup.

## Quick Start

### Run locally (development)

```bash
export TVZ_JWT_SECRET=$(openssl rand -hex 32)
go run .
```

The server starts on `http://localhost:8080`. Without a reverse proxy, API endpoints require auth headers:

```bash
curl -s -H "X-Forwarded-User: jdoe" -H "X-Forwarded-Groups: admin" \
  http://localhost:8080/api/auth/session | jq .
```

### Demo with Keycloak (Docker Compose)

A full demo with Keycloak authentication, 3 pre-provisioned users, and oauth2-proxy:

```bash
docker compose -f docker-compose.demo.yml up -d --build
```

Wait ~40s for Keycloak to start and provision, then open `http://localhost:8080`.

#### Demo users (password = username)

| Username | Password | Keycloak Group  | App Role    |
|----------|----------|-----------------|-------------|
| `admin`  | `admin`  | `tvz-admin`     | admin       |
| `user`   | `user`   | `tvz-normal`    | normal      |
| `rouser` | `rouser` | `tvz-readonly`  | read-only   |

#### Architecture

```
Browser → oauth2-proxy (:8080) → Team Visualizer (:8080 internal)
              ↓
         Keycloak (:8090) — OIDC provider
```

- **Keycloak** — OIDC identity provider with pre-provisioned realm, users, groups, and client
- **oauth2-proxy** — handles the OAuth2/OIDC flow, sets `X-Forwarded-Preferred-Username` and `X-Forwarded-Groups` headers
- **Team Visualizer** — Go app that trusts the proxy headers and maps groups to roles

Keycloak admin console: `http://localhost:8090` (admin/admin). Realm: `teamviz`.

### Docker (standalone)

```bash
docker build -t teamviz .
docker run -p 8080:8080 \
  -e TVZ_JWT_SECRET=$(openssl rand -hex 32) \
  -v teamviz-data:/data \
  teamviz
```

## Features

### Views (tabs)

| Tab | Roles | Description |
|-----|-------|-------------|
| **Team Grid** | All | Half-day grid (people × weeks) with drag-select range editing, on-call, project colours, remote indicators, conflict warnings |
| **Availability** | All | "Where is everyone" — per-person AM/PM status for a selected day |
| **Run Coverage** | All | Per-half-day run coverage vs target, with below-target warnings |
| **Guests** | All | Same grid as Team Grid but for guest people |
| **Archived** | All | List of archived people with restore/delete actions |
| **People** | All | Team member cards with add/edit/archive/delete |
| **Projects** | All | Project cards (general + Gantt views) with team lead, status, people assigned |
| **Workload** | All | Per-person daily allocation (average of AM+PM), remote %, weekly presence counter |
| **Activity** | All | Live audit feed of recent changes (who/what/when), updated via WebSocket |
| **My Week** | All | Mobile-friendly personal schedule — shows your selected person's week as large tappable cards |
| **Admin** | Admin | Prune threshold, prune/reset data, import TOML, holiday country selection |
| **Users** | Admin | Read-only list of all logged-in users (username, role, created, last seen, assigned person) |
| **Settings** | All | Window weeks, run mode, run target, theme (client-side per-browser) |

### Key features

- **Real-time collaboration** — WebSocket broadcasts all changes to all connected clients; presence indicators show who's online (with their assigned person's avatar emoji)
- **Remote work** — per-slot 🏠 Remote flag, visible as a dashed border on any cell (project, away, run, undetermined). Toggle in single or bulk range edits. Excluded from run coverage counts.
- **Undo/redo** — 20-level stack with `Ctrl+Z` / `Ctrl+Shift+Z` (or `Ctrl+Y`), reverting to the server (not just visual)
- **Multi-level undo** — per-slot snapshots so concurrent edits by other users aren't clobbered
- **Persistent view preferences** — scroll offset, group-by, weekend toggle, current tab, etc. saved in `localStorage`
- **Search/filter + jump-to-date** — filter the team grid by name/sub-team/project; jump to any week via date picker
- **Holidays** — imported via TOML, shown as day-header badges (admin-selectable country), excluded from coverage counts
- **ICS calendar subscription** — public per-person feed at `/api/ics/public/{token}` (admin generates/revokes tokens); also one-off ICS export per person
- **"I am this person"** — each user maps themselves to a Person (self-service picker in topbar, admin-assignable); powers the My Week view and presence avatars
- **Team lead** — each project can have a team lead (any person or guest), shown with ⭐ in the project view; guest leads styled distinctly
- **12 themes** — Dracula, Monokai, Light, Nord, Solarized Light/Dark, GitHub, GitHub Dark, One Dark, Gruvbox, Tokyo Night, Catppuccin Mocha (per-browser via localStorage)
- **Expanded emoji picker** — ~430 emojis with a 🎲 random picker as the first standout item
- **Day-of-month headers** — day columns show `Mon / 14` format
- **Keyboard shortcuts** — ←/→ move timeframe, `U` undetermined, `R` toggle run, `Ctrl+Z` undo, `Ctrl+Shift+Z` redo, `Esc` close
- **Sign out / switch user** — clears both oauth2-proxy and Keycloak sessions, redirects to login page
- **Responsive** — collapsible nav, scrollable grid, mobile-optimised My Week view

### TOML import/export

- Export all data (people, planning, projects, on-call, rotation, settings) as TOML
- Import with `merge` or `replace` mode (admin only)
- Legacy format compatibility (nested `avatar` table, `guest` field, integer settings)
- Round-trips `remote` and `team_lead` fields

## Configuration

| Variable                  | Default              | Description                          |
|---------------------------|----------------------|--------------------------------------|
| `TVZ_LISTEN`              | `:8080`              | Listen address                       |
| `TVZ_DB_PATH`             | `/data/teamviz.db`   | SQLite database file path            |
| `TVZ_JWT_SECRET`          | *(required)*         | Secret for signing JWT tokens        |
| `TVZ_JWT_TTL`             | `24h`                | JWT token lifetime                   |
| `TVZ_PROXY_HEADER_USER`   | `X-Forwarded-User`   | Header containing the username       |
| `TVZ_PROXY_HEADER_GROUPS` | `X-Forwarded-Groups` | Header containing user groups/roles  |
| `TVZ_ADMIN_GROUP`         | `admin`              | Group name mapping to admin role     |
| `TVZ_NORMAL_GROUP`        | `normal`             | Group name mapping to normal role    |
| `TVZ_READONLY_GROUP`      | `readonly`           | Group name mapping to read-only role |
| `TVZ_WS_ENABLED`          | `true`               | Enable WebSocket real-time updates   |

> **Note:** In the Keycloak demo, `TVZ_PROXY_HEADER_USER` is set to `X-Forwarded-Preferred-Username` so the app displays the Keycloak username (e.g. `admin`) rather than the `sub` UUID.

## Roles & Permissions

| Action                        | Admin | Normal | Read-Only |
|-------------------------------|:-----:|:------:|:---------:|
| View all views                |  ✅   |  ✅    |    ✅     |
| Edit planning                |  ✅   |  ✅    |    ❌     |
| Add/edit/archive people      |  ✅   |  ✅    |    ❌     |
| Add/edit projects            |  ✅   |  ✅    |    ❌     |
| Change operational settings  |  ✅   |  ✅    |    ✅     |
| Change admin-only settings   |  ✅   |  ❌    |    ❌     |
| Import TOML                  |  ✅   |  ❌    |    ❌     |
| Prune / reset data           |  ✅   |  ❌    |    ❌     |
| Manage users / ICS tokens    |  ✅   |  ❌    |    ❌     |
| Export TOML                  |  ✅   |  ✅    |    ✅     |
| Change theme                 |  ✅   |  ✅    |    ✅     |

**Auth model:** The app runs behind a reverse proxy (oauth2-proxy in the demo). Proxy headers are **authoritative** — they take priority over any stale JWT cookie, so switching users in Keycloak immediately updates the app's identity and role. The JWT is used only as a session token for API/WebSocket auth.

**Settings key access:**
- All roles: `window_weeks`, `run_mode`, `run_target_persons`
- Admin only: `prune_weeks`, `holiday_country`

## API

All endpoints under `/api/`. Auth via JWT (cookie or `Authorization: Bearer <token>`), except the public ICS feed.

### Core endpoints

| Method | Path                       | Description                  | Role    |
|--------|----------------------------|------------------------------|---------|
| GET    | `/api/health`              | Health check                 | any     |
| GET    | `/api/auth/session`        | Current user + JWT           | any     |
| GET    | `/api/people`              | List all people              | any     |
| POST   | `/api/people`              | Add person                   | normal+ |
| PUT    | `/api/people/:id`          | Update person                | normal+ |
| DELETE | `/api/people/:id`          | Delete person                | normal+ |
| POST   | `/api/people/:id/archive`  | Archive person               | normal+ |
| GET    | `/api/planning`            | Get planning (date range)    | any     |
| PUT    | `/api/planning/slot`       | Set a half-day slot          | normal+ |
| DELETE | `/api/planning/slot`       | Clear a half-day slot        | normal+ |
| PUT    | `/api/planning/range`      | Set a range of slots         | normal+ |
| DELETE | `/api/planning/range`       | Clear a range of slots       | normal+ |
| POST   | `/api/planning/copy-week`  | Copy last week's assignments  | normal+ |
| POST   | `/api/planning/prune`      | Prune old data               | admin   |
| POST   | `/api/reset`               | Reset all data               | admin   |
| GET    | `/api/projects`            | List all projects            | any     |
| POST   | `/api/projects`            | Add project                  | normal+ |
| PUT    | `/api/projects/:id`        | Update project               | normal+ |
| DELETE | `/api/projects/:id`        | Delete project               | normal+ |
| POST   | `/api/projects/import-csv` | Import projects from CSV     | admin   |
| GET    | `/api/settings`            | Get settings                 | any     |
| PUT    | `/api/settings`            | Update settings              | any*    |
| GET    | `/api/oncall`              | Get on-call                  | any     |
| PUT    | `/api/oncall`              | Set on-call                  | normal+ |
| DELETE | `/api/oncall`              | Remove on-call               | normal+ |
| GET    | `/api/rotation`            | Get rotation                 | any     |
| PUT    | `/api/rotation`            | Set rotation                 | normal+ |
| DELETE | `/api/rotation`            | Remove rotation              | normal+ |
| POST   | `/api/rotation/assign`     | Assign run person            | normal+ |
| GET    | `/api/holidays`            | List holidays                | any     |
| POST   | `/api/holidays/import`     | Import holidays from TOML    | admin   |
| GET    | `/api/export`              | Export all data as TOML     | any     |
| POST   | `/api/import`              | Import TOML                  | admin   |
| GET    | `/api/ws`                  | WebSocket (real-time)        | any     |

### New feature endpoints

| Method | Path                          | Description                      | Role    |
|--------|-------------------------------|----------------------------------|---------|
| GET    | `/api/activity?limit=N`       | Audit log (newest first)         | any     |
| GET    | `/api/me/person`              | Get current user's person mapping| any     |
| PUT    | `/api/me/person`              | Set current user's person mapping| any     |
| GET    | `/api/users`                  | List all users (with last seen)  | admin   |
| PUT    | `/api/users/:id/person`       | Assign a person to a user        | admin   |
| POST   | `/api/people/:id/ics-token`   | Generate/regenerate ICS token    | admin   |
| DELETE | `/api/people/:id/ics-token`   | Revoke ICS token                 | admin   |
| GET    | `/api/ics/public/:token`      | Public ICS calendar feed         | public  |

\* `PUT /api/settings` is available to all roles, but admin-only keys (`prune_weeks`, `holiday_country`) are rejected for non-admins.

## WebSocket

Real-time updates via WebSocket at `/api/ws`. Events broadcast on all mutations:

- `person_added`, `person_updated`, `person_deleted`, `person_archived`, `person_unarchived`
- `planning_updated`, `planning_cleared`, `planning_range`, `planning_copied`, `planning_pruned`
- `project_added`, `project_updated`, `project_deleted`
- `oncall_changed`, `rotation_changed`
- `settings_updated`, `data_imported`, `data_reset`, `holidays_imported`
- `presence` — list of online users (with their assigned `person_id`)
- `activity_new` — new audit event (for live Activity tab updates)

Connection auto-reconnects with 3-second backoff. Origin is validated (same-origin check).

## Database migrations

Migrations are embedded SQL files run on startup (idempotent — `ALTER TABLE` errors for existing columns are silently ignored):

| Migration | Description |
|-----------|-------------|
| `001_init` | People, planning, settings, oncall, rotation, users tables |
| `002_projects` | Projects table |
| `003_features` | `audit_log` table, `users.selected_person_id`, `people.ics_token` |
| `004_remote` | `planning.remote` column |
| `005_team_lead` | `projects.team_lead` column |

## Project structure

```
main.go                      — Entry point, server setup, public ICS route
internal/
  config/   — Environment configuration
  auth/     — Reverse proxy auth → JWT, role middleware (proxy headers authoritative)
  model/    — Domain structs (Person, Project, SlotData with Remote, Settings with HolidayCountry)
  store/    — SQLite data access + migrations + audit log + ICS tokens
  api/      — REST API handlers (read + write + activity + users + ICS)
  ws/       — WebSocket hub (broadcast + presence)
  toml/     — TOML serialize/parse (pelletier/go-toml/v2, legacy compat)
web/
  embed.go  — go:embed for assets + SPA handler
  assets/   — SPA frontend (index.html, app.js, styles.css)
  legacy/   — Legacy HTML5 app (served at /legacy/)
scripts/
  init-keycloak.sh — Idempotent Keycloak provisioning (realm, users, groups, client, mappers)
docker-compose.demo.yml — Full demo: Keycloak + oauth2-proxy + Team Visualizer
Dockerfile                — Multi-stage build (Go + Alpine)
test-api.sh              — End-to-end API test script
```

## Build

```bash
go build -o teamviz .
```

## Testing

```bash
# Build
go build -o /tmp/teamviz .

# Run end-to-end tests
TVZ_JWT_SECRET=testsecret ./test-api.sh
```

## Legacy app

The original single-file HTML5 prototype is served at `/legacy/` for prototyping. It uses browser localStorage (not the Go backend's database).