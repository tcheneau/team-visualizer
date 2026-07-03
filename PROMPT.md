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
- **avatar** — emoji or single character + an assigned color (auto-generated, overridable). An **emoji picker** popup (~80 curated emojis) is available in the add/edit person modals for quick selection; free-text input is also supported.
- **start date** — when the person joined (for historical context).
- **default project list** — projects this person typically works on (used as suggestions during entry).
- **run target (persons)** — removed per-person run ratio target. Run coverage is now a global headcount target (see §7).
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

The app supports **both** run modes (the manager chooses globally via settings):

1. **Ratio mode** — run duty is distributed across the team. A global **run target** (headcount, default 3 persons) defines how many people should be on run during any given half-day. The app counts actual people on run per half-day and compares to the target.
2. **Rotation mode** — a **dedicated run person** is assigned per week (or per configurable period), at 100% run; the manager rotates people through.

### 7.2 Ratio Mode Calculations (Headcount Model)

Run coverage is measured **per half-day slot**, not as a percentage:

For each half-day (e.g. "Wednesday AM"):
- **Actual on run** = count of active team members with `run = true` on that slot.
- **Target** = `run_target_persons` (global setting, default 3).
- If actual < target → **visual warning** (red cell, warning banner).

Team-level, each week:
- A **coverage table** shows days (Mon–Fri) × slots (AM/PM), each cell showing `actual/target` (e.g. `2/3`).
- Cells below target are highlighted red; cells at or above are green.
- A **warning banner** appears if any slot in the week is below target.
- Lists who is on run each week (by name and half-day count).

Per-person view:
- Shows run half-days count vs working half-days (e.g. "Run 4/10 half-days").

### 7.3 Rotation Mode

- Manager designates one (or more) person as **"run person"** for a given week.
- That person's slots default to `run=true` for all working half-days that week.
- The manager can override individual slots if needed (e.g. a half-day of training).
- A rotation schedule view shows who is on run for each upcoming week.

### 7.4 Visual Cues

- Run coverage table: cells show `actual/target`, red when below target, green when met.
- Warning banner per week if any slot is below target.
- Team grid summary row shows `runCount/target` per day, red when below target.

---

## 8. Data Entry

### 8.1 Manual Entry (Manager)

- Click a half-day cell → opens an editor popover/panel:
  - Choose away type (fills slot) **or**
  - Add projects with percentages (validated ≤100%) + toggle run on/off **or**
  - Mark as "undetermined".
- **Drag selection**: click and drag across multiple consecutive half-day cells for the same person → opens a **range editor** that applies the same assignment (project, away type, run, undetermined, or clear) to all selected slots at once.
- Keyboard-friendly: tab between cells, quick shortcuts (see §13).
- **"Copy last week"** helper: duplicate a person's previous week of assignments into the current week (manager confirms; away entries are not copied by default, configurable).

### 8.2 TOML Import

- Manager can import a TOML file to load/replace planning data.
- Import can **merge** or **replace** (manager chooses; replace requires confirm).
- TOML schema mirrors the internal data model (see §12).
- The TOML parser handles **multi-line arrays** (e.g. `projects = [\n  { name = "...", pct = 100 },\n]`) and **inline tables** with comma-separated key-value pairs.
- Comment stripping (`#`) respects quoted strings — a `#` inside a quoted string (e.g. hex color `"#e07b00"`) is not treated as a comment.
- Import **preserves original person IDs** from the TOML file (does not regenerate them).

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
- ICS events include a `DTSTAMP` property (RFC 5545 required).
- Special characters (commas, semicolons, newlines, backslashes) in SUMMARY, DESCRIPTION, and CATEGORIES are escaped per RFC 5545.
- **Hover text** on the person's name / export button indicates the **period covered** (e.g. "2025-01-06 → 2025-02-02, 4 weeks").

---

## 10. Views & Visualizations

V1 ships with multiple visualization formats. The manager will iterate over time. Initial set:

### 10.1 Team Grid (Default)

- Rows: people (active team members). Columns: weeks (rolling N). Each cell: a half-day slot.
- **Cell merging**: consecutive same-type slots for the same person are visually merged via colspan (e.g. 4 half-days of the same project appear as one wide cell). Multi-project or complex slots are not merged.
- **Project name labels**: project names (or away-type abbreviations) are shown in very small text (7px) within each cell.
- **Project color hashing**: same project name → same color across all people (hash-based HSL color). Away types and run use fixed colors.
- **Week separators**: a 3px accent-colored left border marks the first cell of each week for quick visual grouping.
- **Group by toggle**: a button bar lets the manager switch between **Name** (alphabetical, default) and **Sub-team** (grouped by sub-team field with highlighted header rows; people with no sub-team go into an "Other" group).
- Color legend: away types (distinct colors), project (hash-based per project), run (orange/red), undetermined (grey hatch), not-filled (dashed).
- Click a cell to edit; drag across cells for range editing; click a person name to open individual view / export ICS.
- Summary row at bottom: availability % and `runCount/target` per day (red when below run target).

### 10.2 Individual View

- Single person, expanded: full half-day grid for the visible window, all details visible, run half-day count per week, project breakdown.
- ICS export button here too.
- Printable layout available (see §11).

### 10.3 Run Coverage View

- Per week: a **coverage table** showing days (Mon–Fri) × slots (AM/PM), each cell showing `actual/target` (e.g. `2/3`).
- Cells below target are red; cells at or above are green.
- Warning banner per week if any slot is below target.
- Lists who is on run each week (by name and half-day count).
- Supports both ratio mode and rotation mode (toggle in the view).
- In rotation mode: button to assign run person(s) for each week.

### 10.4 Guests View

- Separate page listing guest people with their planning.
- Uses the same colspan-based merged cell rendering as the team grid (with project colors, labels, week separators).
- Has its own scroll navigation (Earlier / Later / Today).
- Guests excluded from all team aggregates.

### 10.5 Archived View

- Separate page listing archived people (team members and guests), with archive date.
- Restore button per person.
- Historical planning viewable if scrolling to their active period.

### 10.6 Availability Summary ("Where is everyone")

- Quick summary: who's available, who's away (and type), who's on run, who's on projects.
- **Week navigation**: Earlier / Today / Later buttons to browse any week (not just the current one).
- Per-person cards show a mini week summary with colored dots for each half-day.
- Current slot is highlighted when viewing the current week.
- Week overview grid at the bottom shows away/run/project/unassigned counts per day for the selected week.
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
run_target_persons = 3   # headcount target per half-day

[[people]]
id = "uuid-or-slug"
name = "Alice Martin"
role = "Backend Dev"
sub_team = "Platform"
avatar = { emoji = "🦊", color = "#e07b00" }
start_date = "2023-03-01"
default_projects = ["Atlas", "Beacon"]
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
- `u` — mark selected cell as "undetermined" (disabled when focus is in a text input).
- `r` — toggle run on selected cell (disabled when focus is in a text input).
- `Delete` / `Backspace` — clear selected cell (back to "not filled").
- `Ctrl+Z` — undo.
- `Ctrl+E` — export current view to TOML.
- Single-key shortcuts (`u`, `r`) are **disabled when the focused element is an input, select, or textarea** to prevent conflicts with typing.
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
| `run_target_persons`      | 3             | Run target: how many people should be on run per half-day. |
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
2. Manager can add people (team members and guests), set all person fields (with emoji picker), archive and restore them.
3. Manager can fill half-day slots with away / projects (≤100% validated) / run / undetermined, and clear them.
4. "Not filled" vs "undetermined" are visually distinct.
5. Rolling N-week window scrolls forward (and backward as nice-to-have); position persists.
6. Run coverage (ratio mode) shows per-half-day headcount vs target (default 3); run rotation mode works; warning banners when below target.
7. All listed views render: team grid, individual, run coverage, guests, archived, availability summary.
8. TOML import (merge/replace) and export (correct filename pattern) work round-trip without data loss. Multi-line arrays, inline tables, and comment-in-string handling all work. Person IDs preserved on import.
9. Public holiday TOML import + auto-fill works.
10. ICS export per person covers the visible window; hover shows the covered period; DTSTAMP and escaping correct.
11. Print layouts (whole-team + single-individual) produce clean PDFs via browser.
12. Theme selector (Dracula, Monokai, light) works; preference persists.
13. Sample data loads and showcases all states/categories/views.
14. Single-level undo works.
15. Prune-now deletes data older than X weeks (confirm dialog).
16. Schema versioning: old-format data migrates silently; incompatible data warns.
17. Drag selection across multiple half-day cells opens a range editor that applies changes to all selected slots.
18. Cell merging: consecutive same-type slots are visually merged via colspan; project names shown in small text.
19. Project color hashing: same project name → same color across people.
20. Week separators: 3px accent left border marks week boundaries.
21. Team grid grouping: toggle between Name (alphabetical) and Sub-team (with header rows).
22. Availability view has week navigation (Earlier / Today / Later).
23. Emoji picker popup in add/edit person modals.
24. Single-key keyboard shortcuts disabled when typing in input fields.

---

## 18. Implementation Notes

The app was implemented as a single vanilla JavaScript `index.html` file (~100KB, ~2400 lines). Key implementation decisions:

- **Vanilla JS** (no framework, no build step) — chosen for simplicity; the file is directly editable.
- **localStorage** for persistence (sufficient for ~20 people × 52 weeks).
- **TOML parser** is hand-rolled (no library) with support for multi-line arrays, inline tables, and string-aware comment stripping.
- **ICS generator** is hand-rolled with proper DTSTAMP and character escaping.
- **Project colors** use a hash-based HSL color generator (`projectColor()`).
- **Cell merging** uses colspan on consecutive same-type half-day slots; merge groups break at week boundaries.
- **Drag selection** uses mousedown/mousemove/mouseup with `data-cells` attributes (single-quoted to safely contain JSON with double quotes).
- **Run target** is a global headcount setting (`run_target_persons`, default 3), not a per-person percentage.

## 19. Open Questions for Implementation

These are left to the implementer's judgment within the constraints above:

- Exact default for `window_weeks` (suggested 4).
- Exact default for `prune_weeks` (suggested 12).
- Exact color palette per category (ensure colorblind-friendly distinguishability).
- Whether to use localStorage or IndexedDB (IndexedDB if data size warrants).
- Exact framework choice (any that produces a single inlined `index.html`).
- Half-day ICS events: timed (e.g. 09:00–13:00 / 14:00–18:00) vs. all-day-with-status — implementer picks; must be consistent and documented.
- Presentation/auto-scroll mode for TV display — nice-to-have, not blocking V1.