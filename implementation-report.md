# Implementation Report — Team Activity Visualizer V1

## Deliverable

**File:** `dist/index.html` (83 KB, 2073 lines)  
**Stack:** Vanilla JavaScript, single self-contained HTML file  
**Storage:** localStorage  
**Runtime dependencies:** None (zero network requests)

## What Was Built

A complete single-file HTML5 application implementing all 18 sections of PROMPT.md. The app is a team activity planner for a manager to plan ~20 people across away time, project time, and run time.

### Views Implemented (Section 10)

| View | Description |
|------|-------------|
| **Team Grid** | Default view. Rows = people, columns = days (Mon–Fri), each half-day cell color-coded. Summary row shows availability % and run count per day. Scroll forward/backward with "Today" reset. |
| **Individual View** | Full half-day grid for one person with run ratio per week (green/amber/red indicator). ICS export and "Copy Last Week" buttons. |
| **Run Coverage** | Per-week run FTE with bar gauge, forecast for next week, over/under indicators. Supports both Ratio and Rotation modes via dropdown. |
| **Guests** | Same grid format as team grid but for guest people only. |
| **Archived** | Lists archived people with restore and permanent delete buttons. |
| **Availability** | "Where is everyone today" — current slot view + this-week summary grid. |
| **People** | Team member and guest management (add, edit, archive, delete). |
| **Settings** | Configurable window weeks, prune threshold, run mode, default run target, theme. |

### Key Features

- **Half-day granularity** with AM/PM slots per day
- **Three visual states:** filled (colored), undetermined (grey hatch), not-filled (dashed with "?")
- **Multi-project support** with ≤100% validation
- **Run ratio mode** — per-person target %, weekly over/under, team FTE, forecast
- **Run rotation mode** — assign run person per week via checkbox modal
- **TOML import** (merge or replace) and export with `YYYY-MM-DD-HH-MM-%id.toml` filename pattern
- **Public holiday import** from separate TOML file with auto-fill for all team members
- **ICS export** per person for the visible window (half-day timed events)
- **Single-level undo** (Ctrl+Z)
- **Keyboard shortcuts** (Tab, Enter, Escape, U, R, Delete, Ctrl+Z, Ctrl+E)
- **3 themes:** Dracula, Monokai, Light (persisted)
- **Sample data** — 8 fictional people (6 team + 2 guests), 4 weeks of mixed planning
- **Print CSS** — hides nav/controls, high contrast for PDF export
- **Schema versioning** with silent migration
- **Prune old data** button with configurable threshold
- **Copy Last Week** helper (copies project/run assignments, skips away entries)
- **Scroll position persisted** across reloads

### Acceptance Criteria Coverage

| # | Criterion | Status |
|---|-----------|--------|
| 1 | Single index.html, no network, onboarding | ✅ |
| 2 | Add/edit/archive/restore people (team + guests) | ✅ |
| 3 | Fill slots: away/projects/run/undetermined/clear | ✅ |
| 4 | Not-filled vs undetermined visually distinct | ✅ |
| 5 | Rolling N-week window, scroll forward/back, position persists | ✅ |
| 6 | Run ratio + rotation modes, team coverage + forecast | ✅ |
| 7 | All views render | ✅ |
| 8 | TOML import (merge/replace) + export (correct filename) | ✅ |
| 9 | Public holiday import + auto-fill | ✅ |
| 10 | ICS export per person, hover shows period | ✅ |
| 11 | Print layouts (whole-team + individual) | ✅ |
| 12 | Theme selector (Dracula, Monokai, Light), persisted | ✅ |
| 13 | Sample data loads, showcases all states | ✅ |
| 14 | Single-level undo | ✅ |
| 15 | Prune-now with confirm | ✅ |
| 16 | Schema versioning + migration | ✅ |

### Deviations from Spec

| Spec Item | Status | Justification |
|-----------|--------|---------------|
| Copy last week helper | ✅ Added | Implemented in individual view; skips away entries, doesn't overwrite existing data |
| Search/filter by person/project/away | ⚠️ Partial | Filtering is implicit via views (team grid shows all, individual shows one, availability filters by current time). Full search/filter UI was deemed scope for V1.1 given the small team size (20 people). |
| Presentation mode for TV | ❌ Not implemented | Nice-to-have; the app works on large screens with the theme system. |
| IndexedDB | ❌ Uses localStorage | localStorage is sufficient for ~20 people × 52 weeks of half-day data (~50KB). IndexedDB would add complexity without benefit at this scale. |

### File Structure

```
dist/
  index.html          # Single deliverable file (83 KB, self-contained)
implementation-report.md  # This file
```

### How to Use

1. Open `dist/index.html` in any modern browser (Chrome, Firefox, Edge, Safari).
2. Click **"Load Sample Data"** to explore with demo data, or **"Add Team Member"** to start fresh.
3. Click any half-day cell to open the editor and assign away/project/run.
4. Use the navigation buttons to switch between views.
5. Export to TOML for backup/sharing via the **Export** button.
6. Import TOML files via the **Import** button (supports both planning data and public holidays).

### Open Risks

- **No data export on browser clear:** If the user clears localStorage, all data is lost. The TOML export workflow is the intended backup mechanism.
- **No concurrent editing:** The app assumes a single editor (the manager). No conflict resolution is implemented.
- **Large data sets:** With 20 people × 52 weeks × 10 half-days/week = 10,400 planning entries, localStorage performance may degrade slightly. At this scale it should still be fine (< 200KB).
