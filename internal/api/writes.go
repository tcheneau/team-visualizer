package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
	"github.com/teamviz/team-visualizer/internal/toml"
)

// ===== People (write) =====

func (r *Router) addPerson(w http.ResponseWriter, req *http.Request) {
	var p model.Person
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if p.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	result, err := r.db.AddPerson(p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_added", result)
	r.recordEvent(req.Context(), "person_add", result.Name, "", map[string]any{"person_id": result.ID, "person_name": result.Name})
	writeJSON(w, http.StatusCreated, result)
}

func (r *Router) updatePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	var p model.Person
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.UpdatePerson(id, p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	persisted, err := r.db.GetPerson(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_updated", persisted)
	r.recordEvent(req.Context(), "person_update", persisted.Name, "", map[string]any{"person_id": persisted.ID, "person_name": persisted.Name})
	writeJSON(w, http.StatusOK, persisted)
}

func (r *Router) deletePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.DeletePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_deleted", map[string]string{"id": id})
	r.recordEvent(req.Context(), "person_delete", id, "", map[string]any{"person_ids": []string{id}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (r *Router) archivePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.ArchivePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_archived", map[string]string{"id": id})
	r.recordEvent(req.Context(), "person_archive", id, "", map[string]any{"person_ids": []string{id}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (r *Router) unarchivePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.UnarchivePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_unarchived", map[string]string{"id": id})
	r.recordEvent(req.Context(), "person_unarchive", id, "", map[string]any{"person_ids": []string{id}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

// ===== Planning (write) =====

type setSlotReq struct {
	PersonID string         `json:"person_id"`
	Date     string         `json:"date"`
	Slot     string         `json:"slot"`
	Data     model.SlotData `json:"data"`
}

func (r *Router) setSlot(w http.ResponseWriter, req *http.Request) {
	var body setSlotReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.PersonID == "" || body.Date == "" || body.Slot == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "person_id, date, and slot are required"})
		return
	}
	if err := r.db.SetSlot(body.PersonID, body.Date, body.Slot, body.Data); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_updated", body)
	detail := body.Data.State
	if body.Data.Away != nil {
		detail = "away:" + body.Data.Away.Type
	} else if body.Data.Incident != nil {
		detail = "incident:" + body.Data.Incident.Text
	} else if len(body.Data.Projects) > 0 {
		names := make([]string, 0, len(body.Data.Projects))
		for _, p := range body.Data.Projects {
			names = append(names, p.Name)
		}
		detail = strings.Join(names, ",")
		if body.Data.Run {
			detail += "+run"
		}
	} else if body.Data.Run {
		detail = "run"
	}
	r.recordEvent(req.Context(), "planning_set", fmt.Sprintf("%s %s %s", body.PersonID, body.Date, body.Slot), detail, slotMeta(body.PersonID, body.Data, map[string]any{"date": body.Date, "slot": body.Slot}))
	writeJSON(w, http.StatusOK, body)
}

func (r *Router) clearSlot(w http.ResponseWriter, req *http.Request) {
	var body setSlotReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.ClearSlot(body.PersonID, body.Date, body.Slot); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_cleared", body)
	r.recordEvent(req.Context(), "planning_clear", fmt.Sprintf("%s %s %s", body.PersonID, body.Date, body.Slot), "", map[string]any{"person_ids": []string{body.PersonID}, "date": body.Date, "slot": body.Slot, "state": "cleared"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

type setRangeReq struct {
	PersonIDs []string       `json:"person_ids"`
	StartDate string         `json:"start_date"`
	StartSlot string         `json:"start_slot"`
	EndDate   string         `json:"end_date"`
	EndSlot   string         `json:"end_slot"`
	Data      model.SlotData `json:"data"`
}

func (r *Router) setSlotRange(w http.ResponseWriter, req *http.Request) {
	var body setRangeReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(body.PersonIDs) == 0 || body.StartDate == "" || body.EndDate == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "person_ids, start_date, and end_date are required"})
		return
	}
	if body.StartSlot == "" {
		body.StartSlot = "am"
	}
	if body.EndSlot == "" {
		body.EndSlot = "pm"
	}

	// Generate all slots in range
	slots := generateSlotsInRange(body.StartDate, body.StartSlot, body.EndDate, body.EndSlot)

	// Build refs: person_ids × slots
	var refs []store.SlotRef
	for _, pid := range body.PersonIDs {
		for _, sl := range slots {
			refs = append(refs, store.SlotRef{PersonID: pid, Date: sl.Date, Slot: sl.Slot})
		}
	}

	if err := r.db.SetSlotRange(refs, body.Data); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_range", map[string]any{"person_ids": body.PersonIDs, "start_date": body.StartDate, "end_date": body.EndDate, "data": body.Data})
	meta := slotRangeMeta(body.PersonIDs, body.Data)
	meta["start_date"] = body.StartDate
	meta["start_slot"] = body.StartSlot
	meta["end_date"] = body.EndDate
	meta["end_slot"] = body.EndSlot
	r.recordEvent(req.Context(), "planning_range", fmt.Sprintf("%s-%s %s-%s", body.StartDate, body.StartSlot, body.EndDate, body.EndSlot), fmt.Sprintf("%d people", len(body.PersonIDs)), meta)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"slots_set": len(refs),
		"people":    len(body.PersonIDs),
	})
}

func (r *Router) clearSlotRange(w http.ResponseWriter, req *http.Request) {
	var body setRangeReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	slots := generateSlotsInRange(body.StartDate, body.StartSlot, body.EndDate, body.EndSlot)
	var refs []store.SlotRef
	for _, pid := range body.PersonIDs {
		for _, sl := range slots {
			refs = append(refs, store.SlotRef{PersonID: pid, Date: sl.Date, Slot: sl.Slot})
		}
	}
	if err := r.db.ClearSlotRange(refs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_range_cleared", map[string]any{"person_ids": body.PersonIDs, "start_date": body.StartDate, "end_date": body.EndDate})
	meta := slotRangeMeta(body.PersonIDs, model.SlotData{})
	meta["start_date"] = body.StartDate
	meta["start_slot"] = body.StartSlot
	meta["end_date"] = body.EndDate
	meta["end_slot"] = body.EndSlot
	meta["state"] = "cleared"
	r.recordEvent(req.Context(), "planning_range_clear", fmt.Sprintf("%s-%s %s-%s", body.StartDate, body.StartSlot, body.EndDate, body.EndSlot), fmt.Sprintf("%d people", len(body.PersonIDs)), meta)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "slots_cleared": len(refs)})
}

type copyWeekReq struct {
	PersonID      string `json:"person_id"`
	FromWeekStart string `json:"from_week_start"`
	ToWeekStart   string `json:"to_week_start"`
}

func (r *Router) copyWeek(w http.ResponseWriter, req *http.Request) {
	var body copyWeekReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	copied, err := r.db.CopyWeek(body.PersonID, body.FromWeekStart, body.ToWeekStart)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_copied", map[string]any{"person_id": body.PersonID, "to_week_start": body.ToWeekStart})
	r.recordEvent(req.Context(), "planning_copy", fmt.Sprintf("%s -> %s", body.FromWeekStart, body.ToWeekStart), fmt.Sprintf("person %s", body.PersonID), map[string]any{"person_ids": []string{body.PersonID}, "from_week": body.FromWeekStart, "to_week": body.ToWeekStart})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "copied": copied})
}

type pruneReq struct {
	WeeksOld int `json:"weeks_old"`
}

func (r *Router) pruneData(w http.ResponseWriter, req *http.Request) {
	var body pruneReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.WeeksOld <= 0 {
		settings, err := r.db.GetSettings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		body.WeeksOld = settings.PruneWeeks
		if body.WeeksOld < 1 {
			body.WeeksOld = 12
		}
	}
	deleted, err := r.db.PruneOldData(body.WeeksOld)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_pruned", map[string]any{"weeks_old": body.WeeksOld, "deleted": deleted})
	r.recordEvent(req.Context(), "prune", fmt.Sprintf("weeks_old %d", body.WeeksOld), fmt.Sprintf("deleted %d", deleted), map[string]any{"weeks_old": body.WeeksOld, "deleted": deleted})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deleted": deleted, "weeks_old": body.WeeksOld})
}

func (r *Router) resetData(w http.ResponseWriter, req *http.Request) {
	if err := r.db.ResetAllData(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("data_reset", nil)
	r.recordEvent(req.Context(), "reset", "", "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// ===== Projects (write) =====

func (r *Router) addProject(w http.ResponseWriter, req *http.Request) {
	var p model.Project
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if p.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	existing, _ := r.db.GetProjectByName(p.Name)
	if existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "project with this name already exists"})
		return
	}
	result, err := r.db.AddProject(p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("project_added", result)
	r.recordEvent(req.Context(), "project_add", result.Name, "", map[string]any{"project_id": result.ID, "project_name": result.Name})
	writeJSON(w, http.StatusCreated, result)
}

func (r *Router) updateProject(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	var p model.Project
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.UpdateProject(id, p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	persisted, err := r.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("project_updated", persisted)
	r.recordEvent(req.Context(), "project_update", persisted.Name, "", map[string]any{"project_id": persisted.ID, "project_name": persisted.Name})
	writeJSON(w, http.StatusOK, persisted)
}

func (r *Router) deleteProject(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.DeleteProject(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("project_deleted", map[string]string{"id": id})
	r.recordEvent(req.Context(), "project_delete", id, "", map[string]any{"project_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (r *Router) importProjectCSV(w http.ResponseWriter, req *http.Request) {
	// Accept raw CSV text in body
	body, _ := io.ReadAll(req.Body)
	csvText := string(body)
	if csvText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty CSV body"})
		return
	}

	rows := parseCSV(csvText)
	if len(rows) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no valid rows found"})
		return
	}

	created, updated := 0, 0
	for _, fields := range rows {
		if len(fields) < 1 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		p := model.Project{
			Name:        getField(fields, 0),
			Emoji:       getFieldOr(fields, 1, "📁"),
			Description: getField(fields, 2),
			URL:         getField(fields, 3),
			StartDate:   getField(fields, 4),
			EndDate:     getField(fields, 5),
			Status:      getFieldOr(fields, 6, "unstarted"),
		}
		// Validate status
		valid := map[string]bool{"unstarted": true, "in_progress": true, "paused": true, "done": true}
		if !valid[p.Status] {
			p.Status = "unstarted"
		}
		isNew, err := r.db.UpsertProjectByName(p)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if isNew {
			created++
		} else {
			updated++
		}
	}
	r.hub.Broadcast("projects_imported", map[string]any{"created": created, "updated": updated})
	r.recordEvent(req.Context(), "project_import_csv", "", fmt.Sprintf("created %d updated %d", created, updated), map[string]any{"created": created, "updated": updated})
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "updated": updated})
}

// ===== On-Call & Rotation (write) =====

type onCallReq struct {
	PersonID  string `json:"person_id"`
	WeekStart string `json:"week_start"`
}

func (r *Router) setOnCall(w http.ResponseWriter, req *http.Request) {
	var body onCallReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetOnCall(body.PersonID, body.WeekStart, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("oncall_changed", body)
	r.recordEvent(req.Context(), "oncall_set", fmt.Sprintf("%s %s", body.PersonID, body.WeekStart), "", map[string]any{"person_ids": []string{body.PersonID}, "week_start": body.WeekStart})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) removeOnCall(w http.ResponseWriter, req *http.Request) {
	var body onCallReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetOnCall(body.PersonID, body.WeekStart, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("oncall_changed", body)
	r.recordEvent(req.Context(), "oncall_remove", fmt.Sprintf("%s %s", body.PersonID, body.WeekStart), "", map[string]any{"person_ids": []string{body.PersonID}, "week_start": body.WeekStart})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) setRotation(w http.ResponseWriter, req *http.Request) {
	var body onCallReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetRotation(body.PersonID, body.WeekStart, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("rotation_changed", body)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) assignRunPerson(w http.ResponseWriter, req *http.Request) {
	var body onCallReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	overwritten, err := r.db.AssignRunPerson(body.PersonID, body.WeekStart)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("rotation_changed", map[string]any{"person_id": body.PersonID, "week_start": body.WeekStart, "overwritten": overwritten})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "overwritten_slots": overwritten})
}

func (r *Router) removeRotation(w http.ResponseWriter, req *http.Request) {
	var body onCallReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetRotation(body.PersonID, body.WeekStart, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("rotation_changed", body)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ===== Import (TOML + holidays) =====

func (r *Router) importTOML(w http.ResponseWriter, req *http.Request) {
	mode := req.URL.Query().Get("mode")
	if mode == "" {
		mode = "merge"
	}

	body, _ := io.ReadAll(req.Body)
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}

	data, err := toml.Parse(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "TOML parse error: " + err.Error()})
		return
	}

	created, updated, err := r.db.ImportTOMLData(data, mode)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("data_imported", map[string]any{"mode": mode, "created": created, "updated": updated})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"created": created,
		"updated": updated,
		"mode":    mode,
	})
}

func (r *Router) importHolidays(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}

	holidays, err := toml.ParseHolidays(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	count, err := r.db.ImportHolidays(holidays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("holidays_imported", map[string]any{"imported": count})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "imported": count})
}

// ===== Helpers =====

// slotMeta builds the structured meta map for a single-slot action (e.g.
// planning_set) given the person id and the slot data. Extra key/values can be
// merged in (date/slot).
func slotMeta(personID string, d model.SlotData, extra map[string]any) map[string]any {
	m := map[string]any{"person_ids": []string{personID}}
	for k, v := range extra {
		m[k] = v
	}
	applySlotDataMeta(m, d)
	return m
}

// slotRangeMeta builds the structured meta map for a range action affecting
// one or more people.
func slotRangeMeta(personIDs []string, d model.SlotData) map[string]any {
	ids := make([]string, len(personIDs))
	copy(ids, personIDs)
	m := map[string]any{"person_ids": ids, "people_count": len(personIDs)}
	applySlotDataMeta(m, d)
	return m
}

// applySlotDataMeta adds state/project/run/remote/offsite info from a SlotData
// into a meta map.
func applySlotDataMeta(m map[string]any, d model.SlotData) {
	if d.Away != nil {
		m["state"] = "away"
		m["away_type"] = d.Away.Type
		if d.Away.Note != "" {
			m["away_note"] = d.Away.Note
		}
	} else if d.Incident != nil {
		m["state"] = "incident"
		if d.Incident.Text != "" {
			m["incident_text"] = d.Incident.Text
		}
	} else if len(d.Projects) > 0 {
		m["state"] = "project"
		names := make([]string, 0, len(d.Projects))
		for _, p := range d.Projects {
			names = append(names, p.Name)
		}
		m["projects"] = names
		if d.Run {
			m["run"] = true
		}
	} else if d.Run {
		m["state"] = "run"
	} else {
		m["state"] = d.State
	}
	if d.Remote {
		m["remote"] = true
	}
	if d.Offsite {
		m["offsite"] = true
	}
}

// slotInDateRange represents a {date, slot} pair.
type slotInDateRange struct {
	Date string
	Slot string
}

// generateSlotsInRange produces all half-day slots from start to end chronologically.
func generateSlotsInRange(startDate, startSlot, endDate, endSlot string) []slotInDateRange {
	var result []slotInDateRange
	start, err := parseDateSimple2(startDate)
	if err != nil {
		return result
	}
	end, err := parseDateSimple2(endDate)
	if err != nil {
		return result
	}

	curDate := start
	curSlot := startSlot
	for curDate.Before(end) || (curDate.Equal(end) && curSlot <= endSlot) {
		result = append(result, slotInDateRange{Date: formatDateSimple2(curDate), Slot: curSlot})
		if curSlot == "am" {
			curSlot = "pm"
		} else {
			curSlot = "am"
			curDate = curDate.AddDate(0, 0, 1)
		}
	}
	return result
}

func parseDateSimple2(s string) (time.Time, error) {
	return time.Parse("2006/01/02", s)
}

func formatDateSimple2(t time.Time) string {
	return t.Format("2006/01/02")
}

// parseCSV parses simple CSV (handles quoted fields).
func parseCSV(text string) [][]string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 1 {
		return nil
	}
	// Detect header
	firstLine := strings.ToLower(strings.TrimSpace(lines[0]))
	hasHeader := strings.Contains(firstLine, "name") && strings.Contains(firstLine, "status")
	startIdx := 0
	if hasHeader {
		startIdx = 1
	}

	var result [][]string
	for i := startIdx; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := parseCSVLine(line)
		if len(fields) >= 1 && strings.TrimSpace(fields[0]) != "" {
			result = append(result, fields)
		}
	}
	return result
}

func parseCSVLine(line string) []string {
	var fields []string
	cur := strings.Builder{}
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote {
			if c == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					cur.WriteByte('"')
					i++
				} else {
					inQuote = false
				}
			} else {
				cur.WriteByte(c)
			}
		} else {
			if c == '"' {
				inQuote = true
			} else if c == ',' {
				fields = append(fields, cur.String())
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		}
	}
	fields = append(fields, cur.String())
	return fields
}

func getField(fields []string, idx int) string {
	if idx < len(fields) {
		return strings.TrimSpace(fields[idx])
	}
	return ""
}

func getFieldOr(fields []string, idx int, def string) string {
	v := getField(fields, idx)
	if v == "" {
		return def
	}
	return v
}
