package api

import (
	"time"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
	r.hub.Broadcast("project_added", result)
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
	r.hub.Broadcast("person_updated", p)
	writeJSON(w, http.StatusOK, p)
}

func (r *Router) deletePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.DeletePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_deleted", map[string]string{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (r *Router) archivePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.ArchivePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_archived", map[string]string{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (r *Router) unarchivePerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.UnarchivePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("person_unarchived", map[string]string{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

// ===== Planning (write) =====

type setSlotReq struct {
	PersonID string          `json:"person_id"`
	Date     string          `json:"date"`
	Slot     string          `json:"slot"`
	Data     model.SlotData  `json:"data"`
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

type setRangeReq struct {
	PersonIDs []string        `json:"person_ids"`
	StartDate string          `json:"start_date"`
	StartSlot string          `json:"start_slot"`
	EndDate   string          `json:"end_date"`
	EndSlot   string          `json:"end_slot"`
	Data      model.SlotData  `json:"data"`
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
	r.hub.Broadcast("planning_range", map[string]any{"person_ids": body.PersonIDs, "start_date": body.StartDate, "end_date": body.EndDate, "data": body.Data})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"slots_set":  len(refs),
		"people":     len(body.PersonIDs),
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
		settings, _ := r.db.GetSettings()
		body.WeeksOld = settings.PruneWeeks
	}
	deleted, err := r.db.PruneOldData(body.WeeksOld)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("planning_pruned", map[string]any{"weeks_old": body.WeeksOld, "deleted": deleted})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deleted": deleted, "weeks_old": body.WeeksOld})
}

func (r *Router) resetData(w http.ResponseWriter, req *http.Request) {
	if err := r.db.ResetAllData(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("data_reset", nil)
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
	r.hub.Broadcast("project_updated", p)
	writeJSON(w, http.StatusOK, p)
}

func (r *Router) deleteProject(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.DeleteProject(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	r.hub.Broadcast("project_deleted", map[string]string{"id": id})
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
	r.hub.Broadcast("projects_imported", map[string]any{"created": created, "updated": updated})
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
