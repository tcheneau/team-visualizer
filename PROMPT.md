# Team Activity Visualizer — V1 Specification

## 1. Overview

A self-hosted HTML5 application that lets a single editor (the team manager) plan and visualize the activity of a ~20-person team across three categories of time: **away time**, **project time**, and **run time**. The whole team can view the result (via screenshare, exported TOML, or printed PDF), but only the manager edits.

The application is a **single openable `index.html`** file with no external requests at runtime — all JS, CSS, and assets are inlined. A build step (e.g. Vite + `vite-plugin-singlefile`) is acceptable; the deliverable is one file. No backend; all data lives in the browser (localStorage or IndexedDB). Data is backed up and shared via TOML export/import.

---

## 2. Personas & Roles

| Role        | Capabilities                                                                 |
|-------------|-----------------------------------------------------------------------------|
| **Manager** | Single editor. Adds/removes/archives people, fills in planning, imports/exports TOML, configures settings. |
| **Teammate**| Read-only. Views the dashboard (via screenshare, shared exported file, or PDF). Cannot edit. |
| **Guest**   | Not a team member but works closely with the team; the manager can plan their time the same way as a team member. Excluded from team aggregates and run-coverage math. |

There is no authentication in V1 — the single-file app is open; editing is available to whoever opens it (the manager). Sharing is handled out-of-band (screenshare, TOML file, PDF).

---

## 3. People Model

### 3.1 Team Members

Each team member has:

- **id** — stable unique identifier (UUID or slug).
- **name** — display name.
- **role/title** — free-text (e.g. "Backend Dev", "QA Engineer").
- **team/sub-team** — optional grouping (e.g. "Platform", "Frontend").
- **avatar** — emoji or single character + an assigned color (auto-generated, overridable).
- **start date** — when the person joined (for historical context).
- **default project list** — projects this person typically works on (used as suggestions during entry).
- **run ratio target** — default percentage of working time expected on run duty (e.g. 20%). See §7.
- **status** — one of: `active`, `archived`.
- **archived date** — set when status becomes `archived`.
- **guest** — boolean (default `false`). `true` marks the person as a guest.

### 3.2 Guests

- Same data model as team members.
- `guest = true`.
- Excluded from all team-level aggregates and run-coverage calculations.
- Appear in their own **Guests view** (separate page/list).
- Can be archived like team members.

### 3.3 Archived People

- When archived: hidden from active views, kept in data with `status=archived` and an `archived date`.
- Still appear in historical views when scrolled to their active period.
- Excluded from team aggregates going forward.
- A **"Show archived"** filter toggle reveals them in active views (dimmed/styled differently).
- A dedicated **Archived page** lists all archived people (team members and guests separately) with their archive date and a button to restore (un-archive).

### 3.4 Adding / Removing People

- Manager can add a person at any time (name + optional fields; id auto-generated).
- Manager can remove a person — this **permanently deletes** their data (confirm dialog); distinct from archiving.
- Manager can archive/un-archive at any time.

---

## 4. Time Categories

### 4.1 Away Time

Sub-types (the away-time category matters; these are the V1 set):

| Code            | Label              |
|-----------------|--------------------|
| `vacation`      | Vacation           |
| `public_holiday`| Public holiday     |
| `sick_leave`    | Sick leave         |
| `training`      | Training           |
| `conference`    | Conference         |
| `parental_leave`| Parental leave     |
| `sabbatical`    | Sabbatical         |
| `other`         | Other (free-text note) |

Each away entry: `type` (one of the codes above) + optional `note`.

### 4.2 Project Time

- A person can be assigned to **multiple projects** within a half-day.
- Each project assignment carries a **percentage**.
- **Validation rule:** the sum of all project percentages within a half-day must be **≤ 100%**. The app blocks/warns on overflow.
- The special project name **"undetermined"** means: the manager has explicitly decided the person is on project work but hasn't assigned a specific project yet (distinct from "not filled" — see §5.3).

### 4.3 Run Time

- Run time is tagged at the half-day level alongside projects (a half-day can be split between run and projects — see §6).
- Run time represents handling incidents and tickets.

### 4.4 Public Holidays

- Loaded from a **separate TOML file** (not mixed with team planning data).
- French public holidays are the default/starting set.
- When loaded, the app can **auto-fill** public holidays as `away: public_holiday` for all active team members (manager triggers this action; it can be scoped to a date range).
- The holiday file is a standalone import; the manager can load a different locale's file at any time.

---

## 5. Time Model & Granularity

### 5.1 Granularity

- Minimum unit: **half-day**.
- Each day has two slots: **AM** and **PM**.
- A full day = both slots filled with the same or different assignments.

### 5.2 Rolling Window

- The visible window is a **rolling N weeks**, where **N is configurable** (minimum 3, default to be chosen during implementation, e.g. 4).
- The window is **anchored to the current week** (week starts Monday).
- **Scrolling forward** is a must when all data doesn't fit at once.
- **Scrolling backward** is a nice-to-have for historical inspection.
- The current scroll position is persisted across reloads.

### 5.3 Three Visual States for a Half-Day Cell

The app must visually distinguish three states:

| State                | Meaning                                              | Visual (proposed)                            |
|----------------------|------------------------------------------------------|----------------------------------------------|
| **Filled**           | Project / run / away assigned.                       | Colored by category.                         |
| **Undetermined**     | Explicitly set to project "undetermined".            | Neutral grey with diagonal hatch pattern.    |
| **Not filled**       | Genuinely blank — manager hasn't entered anything.  | Light background, dashed outline, tiny "?" marker. |

Default behavior: if the manager has not filled a slot for a person, it is treated as **not filled** (not "undetermined"). The manager can explicitly mark a slot as "undetermined" to signal "I know this is project work, project TBD."

> Note: Earlier the spec said "unfilled defaults to project 'undetermined'." This is refined: the default is **not filled** (a visible gap), and **undetermined** is an explicit choice. This lets the manager spot gaps quickly.

### 5.4 Pruning Old Data

- A **"Prune now"** button deletes all planning data older than a configurable **X weeks** from today (confirm dialog before deletion).
- **No auto-pruning.** The manager prunes manually.
- `X` is configurable in settings (default e.g. 12 weeks).

---

## 6. Half-Day Assignment Model

A half-day slot for a person can contain:

1. **Away** — one away type (+ optional note). Fills the entire slot; no projects/run on that slot.
2. **Project(s) + optional Run** — one or more project assignments, each with a percentage, and optionally a `run` flag indicating this slot includes run duty.
   - Projects and run can **share** the half-day.
   - The 100% validation applies to **project percentages only** (run is not a percentage; it's a flag/tag on the slot).
   - Example: a half-day could be 60% ProjectA + 40% ProjectB + `run=true`.
3. **Undetermined** — the slot is marked as project work with project = "undetermined" (no percentage; counts as 100% project).

Data representation (per half-day slot):

```
{
  "state": "filled" | "undetermined" | "not_filled",
  "away": { "type": "vacation", "note": "..." } | null,
  "projects": [{ "name": "ProjectA", "pct": 60 }, { "name": "ProjectB", "pct": 40 }] | [],
  "run": true | false
}
```

---

## 7. Run / Project Ratio Mechanism

### 7.1 Two Supported Modes

The app supports **both** run modes (the manager chooses per person or globally):

1. **Ratio mode** — each person has a **run ratio target** (default e.g. 20% of working time). The app computes actual run ratio per week and compares to the target.
2. **Rotation mode** — a **dedicated run person** is assigned per week (or per configurable period), at 100% run; the manager rotates people through.

### 7.2 Ratio Mode Calculations

For each person, each week:

- **working half-days** = total half-days in the week − away half-days.
- **run half-days** = count of half-day slots where `run = true`.
- **actual run ratio** = `run_half_days / working_half_days`.
- Compare to the person's **run ratio target** → show **over/under** indicator.

Team-level, each week:

- **Σ run half-days** across all active team members.
- **Σ working half-days** across all active team members.
- **team run ratio** = `Σ run_half_days / Σ working_half_days`.
- **average collaborators on run** = `Σ run_half_days / half_days_per_week` (e.g. if total run = 8 half-days in a 5-day week → 0.8 FTE on run).
- **Forecast**: next week's projected run coverage vs. needed, flagging **understaffed/overstaffed** on run (e.g. "next week: understaffed on run by 0.5 FTE").

### 7.3 Rotation Mode

- Manager designates one (or more) person as **"run person"** for a given week.
- That person's slots default to `run=true` for all working half-days that week.
- The manager can override individual slots if needed (e.g. a half-day of training).
- A rotation schedule view shows who is on run for each upcoming week.

### 7.4 Visual Cues

- Person's week cell is colored **green** (at target), **amber** (slightly off), or **red** (significantly off) based on actual vs. target run ratio.
- Team summary shows the weekly run coverage and forecast.

---

## 8. Data Entry

### 8.1 Manual Entry (Manager)

- Click a half-day cell → opens an editor popover/panel:
  - Choose away type (fills slot) **or**
  - Add projects with percentages (validated ≤100%) + toggle run on/off **or**
  - Mark as "undetermined".
- Keyboard-friendly: tab between cells, quick shortcuts (see §13).
- **"Copy last week"** helper: duplicate a person's previous week of assignments into the current week (manager confirms; away entries are not copied by default, configurable).

### 8.2 TOML Import

- Manager can import a TOML file to load/replace planning data.
- Import can **merge** or **replace** (manager chooses; replace requires confirm).
- TOML schema mirrors the internal data model (see §12).

### 8.3 Public Holiday Import

- Separate TOML file for public holidays (date + label).
- Manager loads it; can auto-fill holidays as away entries.

---

## 9. ICS Export (Per Person)

- Each person (team member or guest) can click their name → **download an ICS file** with their planning for the **currently visible window**.
- Each half-day block becomes a **VEVENT**:
  - Away → event titled `Away: <type>` (all-day if full day, timed if half-day).
  - Project → event titled `Project: <name>` (with percentage in description).
  - Run → event titled `Run duty` (or a `Run` tag on a project event if shared).
  - Undetermined → event titled `Project: undetermined`.
- Events include a category/color mapping so calendar apps show them distinctly.
- **Hover text** on the person's name / export button indicates the **period covered** (e.g. "2025-01-06 → 2025-02-02, 4 weeks").

---

## 10. Views & Visualizations

V1 ships with multiple visualization formats. The manager will iterate over time. Initial set:

### 10.1 Team Grid (Default)

- Rows: people (active team members). Columns: weeks (rolling N). Each cell: a week compactly showing half-day colors per day.
- Color legend: away types (distinct colors), project (blue family), run (orange/red), undetermined (grey hatch), not-filled (dashed).
- Click a cell to edit; click a person name to open individual view / export ICS.
- Summary row at bottom: team run coverage, away count, available count per week.

### 10.2 Individual View

- Single person, expanded: full half-day grid for the visible window, all details visible, run ratio per week, project breakdown.
- ICS export button here too.
- Printable layout available (see §11).

### 10.3 Run Coverage View

- Per week: how many FTE on run, vs. needed (sum of targets), forecast for next weeks.
- Color-coded bar/gauge per week.
- Lists who is on run each week.

### 10.4 Guests View

- Separate page listing guest people with their planning (same grid format as team grid but separate).
- Guests excluded from all team aggregates.

### 10.5 Archived View

- Separate page listing archived people (team members and guests), with archive date.
- Restore button per person.
- Historical planning viewable if scrolling to their active period.

### 10.6 Availability Summary ("Where is everyone today/this week")

- Quick summary: who's available, who's away (and type), who's on run, who's on projects — for the current day or selected week.
- Designed for at-a-glance scanning.

### 10.7 Future Views (Placeholder)

The app is architected to make adding views easy. Candidate future views: donut/stacked bars of time split, Gantt-like project timeline, heatmap of away patterns, project staffing overview. Not in V1 but the data model supports them.

---

## 11. Print / PDF

Two printable layouts (manager uses browser "Save to PDF"):

1. **Whole-team report** — the team grid for the visible window + run coverage summary + away summary. A4 landscape, fit-to-width, nav/controls hidden.
2. **Single individual report** — the individual view for one person over the visible window. A4 portrait or landscape, fit-to-width, nav/controls hidden.

Print CSS: hide interactive controls, use high-contrast colors, ensure half-day cells are legible at print scale, include date range header and team/person name.

---

## 12. Data Model & Storage

### 12.1 Storage

- **localStorage** or **IndexedDB** (implementer's choice; IndexedDB preferred if data size warrants).
- All planning data, people, settings, and the export counter persist locally.
- No backend, no network calls at runtime.

### 12.2 TOML Schema (Export / Import)

The exported/imported TOML file mirrors the internal model. Structure (illustrative):

```toml
schema_version = 1
exported_at = "2025-01-15T14:30:00Z"

[settings]
window_weeks = 4
prune_weeks = 12
week_starts = "monday"
run_mode = "ratio"   # or "rotation"
default_run_ratio_target = 20

[[people]]
id = "uuid-or-slug"
name = "Alice Martin"
role = "Backend Dev"
sub_team = "Platform"
avatar = { emoji = "🦊", color = "#e07b00" }
start_date = "2023-03-01"
default_projects = ["Atlas", "Beacon"]
run_ratio_target = 20
status = "active"
guest = false
archived_date = ""

# ... more people ...

# Planning entries keyed by person_id, then date (YYYY/MM/DD), then slot (am/pm)
[[planning]]
person_id = "uuid-or-slug"
date = "2025/01/06"
slot = "am"
state = "filled"
away_type = ""       # empty if not away
away_note = ""
run = false
projects = [
  { name = "Atlas", pct = 100 },
]

[[planning]]
person_id = "uuid-or-slug"
date = "2025/01/06"
slot = "pm"
state = "filled"
away_type = ""
away_note = ""
run = true
projects = [
  { name = "Atlas", pct = 60 },
  { name = "Beacon", pct = 40 },
]
```

Public holiday TOML file (separate):

```toml
schema_version = 1
country = "FR"

[[holidays]]
date = "2025/01/01"
label = "New Year's Day"

[[holidays]]
date = "2025/04/21"
label = "Easter Monday"
```

### 12.3 Export Filename Convention

All exports (TOML and JSON) use the filename pattern:

```
YYYY-MM-DD-HH-MM-%id.toml
```

- `YYYY-MM-DD-HH-MM` — timestamp at export time.
- `%id` — monotonically increasing counter (stored in the app's settings, never resets).
- Example: `2025-01-15-14-30-007.toml`

JSON export uses the same pattern with `.json` extension.

### 12.4 Data Versioning

- TOML/JSON files carry a `schema_version` field.
- The app **migrates old data forward silently** on import/load.
- If data is **incompatible** (too old / unknown schema), the app warns the manager and offers to load as-is (read-only) or abort.
- Current schema version: `1`.

---

## 13. UX Details

### 13.1 Undo

- **Single-level undo** ("Undo last change"). One step back is sufficient.
- TOML export is the broader safety net (manager exports frequently and re-imports on mistakes).

### 13.2 Keyboard Shortcuts

- Tab / Shift+Tab — move between half-day cells.
- Enter — open cell editor.
- Escape — close editor / cancel.
- `u` — mark selected cell as "undetermined".
- `r` — toggle run on selected cell.
- `Delete` / `Backspace` — clear selected cell (back to "not filled").
- `Ctrl+Z` — undo.
- `Ctrl+E` — export current view to TOML.
- Implementer may add more; shortcuts documented in an in-app help panel.

### 13.3 Search & Filter

Filterable dimensions:

- **By person** — show/hide specific people.
- **By project** — highlight who is on a given project.
- **By away type** — highlight who has a given away type.
- **"Who's on run this week"** — quick filter.
- **"Available people this week"** — quick filter (not away, not fully on run).
- **"Show archived"** — toggle to reveal archived people in views.

### 13.4 Onboarding / Empty State

First load (no data detected):

- Short intro overlay: what the app is, how to add people, how to load a TOML, how to fill planning.
- **"Load sample data"** button — fills a demo team (e.g. 8 fictional people, 4 weeks of mixed planning covering all states and categories) so the manager can explore all visualizations before entering real data.
- Sample data is replaceable (manager can clear and start fresh).

### 13.5 Locale

- Week starts **Monday**.
- Date format: **YYYY/MM/DD**.
- Language: English (V1). UI is structured for easy future localization.

### 13.6 Themes

- **Dark mode** supported.
- **Theme selector** with well-known presets: at minimum **Dracula**, **Monokai**, plus a light/default theme.
- Themes are vendored (inlined in the single file); no external requests.
- Theme preference persists across reloads.

### 13.7 Responsive & Multi-Format

- **Desktop browser** — primary target.
- **Wall-mounted TV / large screen** — large fonts, high contrast, no hover dependency (a "presentation mode" that enlarges the team grid and auto-scrolls is a nice-to-have).
- **Mobile-friendly** — basic responsive layout so it's legible on a phone (read-only viewing); editing on mobile is acceptable but not optimized.
- **Printable** — see §11.

---

## 14. Settings (Manager-Configurable)

| Setting                   | Default       | Description                                          |
|---------------------------|---------------|------------------------------------------------------|
| `window_weeks`            | 4             | Number of weeks visible in the rolling window (≥3). |
| `prune_weeks`             | 12            | Threshold for the "Prune now" button.                |
| `week_starts`             | `monday`      | First day of the week.                               |
| `run_mode`                | `ratio`       | `ratio` or `rotation`.                               |
| `default_run_ratio_target`| 20            | Default run ratio target for new people (%).        |
| `theme`                   | `dracula`     | Active theme.                                        |
| `export_counter`          | 1             | Monotonic counter for export filenames.              |

---

## 15. Tech Stack & Build

### 15.1 Framework

- No strong preference; any framework is acceptable **as long as the output is a single openable `index.html`** with everything inlined (JS, CSS, fonts, theme definitions — no external requests at runtime).
- Suggested: Svelte or Vue (lightweight, easy to inline) with Vite + `vite-plugin-singlefile`. Vanilla JS is also acceptable.

### 15.2 Dependencies

- TOML parsing/serialization library (vendored/inlined).
- ICS generation (can be a small vendored lib or hand-rolled; the ICS format is simple enough).
- Chart/visualization: lightweight or hand-rolled (no heavy charting framework required for V1 grid views).
- Everything vendored; the app must work fully offline.

### 15.3 File Structure (Source)

```
src/
  main.*            # app entry
  components/        # UI components (grid, editor, views, etc.)
  store/             # data store (people, planning, settings)
  lib/
    toml.*           # TOML import/export
    ics.*            # ICS generation
    migrate.*        # schema versioning & migration
  themes/            # theme definitions (dracula, monokai, light)
  sample-data.*      # embedded sample data for onboarding
dist/
  index.html         # the single deliverable file (build output)
```

### 15.4 Deliverable

- `dist/index.html` — the single file to deploy/serve/open.
- Can be served by nginx/Apache, opened directly as a local file, or hosted on any static file server.

---

## 16. Non-Goals (V1)

- No backend, no server-side storage, no API.
- No authentication / user accounts.
- No real-time multi-user editing (single editor; sharing is out-of-band).
- No sync with external tools (Jira, ServiceNow, calendars) — manual entry + TOML only.
- No automated notifications/alerts (manager checks the dashboard).
- No localization beyond English UI (date format is YYYY/MM/DD regardless).

---

## 17. Acceptance Criteria (V1)

1. Single `index.html` opens in a browser with no network and displays the onboarding/empty state.
2. Manager can add people (team members and guests), set all person fields, archive and restore them.
3. Manager can fill half-day slots with away / projects (≤100% validated) / run / undetermined, and clear them.
4. "Not filled" vs "undetermined" are visually distinct.
5. Rolling N-week window scrolls forward (and backward as nice-to-have); position persists.
6. Run ratio (ratio mode) and run rotation (rotation mode) both work; team run coverage + forecast display.
7. All listed views render: team grid, individual, run coverage, guests, archived, availability summary.
8. TOML import (merge/replace) and export (correct filename pattern) work round-trip without data loss.
9. Public holiday TOML import + auto-fill works.
10. ICS export per person covers the visible window; hover shows the covered period.
11. Print layouts (whole-team + single-individual) produce clean PDFs via browser.
12. Theme selector (Dracula, Monokai, light) works; preference persists.
13. Sample data loads and showcases all states/categories/views.
14. Single-level undo works.
15. Prune-now deletes data older than X weeks (confirm dialog).
16. Schema versioning: old-format data migrates silently; incompatible data warns.

---

## 18. Open Questions for Implementation

These are left to the implementer's judgment within the constraints above:

- Exact default for `window_weeks` (suggested 4).
- Exact default for `prune_weeks` (suggested 12).
- Exact color palette per category (ensure colorblind-friendly distinguishability).
- Whether to use localStorage or IndexedDB (IndexedDB if data size warrants).
- Exact framework choice (any that produces a single inlined `index.html`).
- Half-day ICS events: timed (e.g. 09:00–13:00 / 14:00–18:00) vs. all-day-with-status — implementer picks; must be consistent and documented.
- Presentation/auto-scroll mode for TV display — nice-to-have, not blocking V1.