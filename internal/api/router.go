package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamviz/team-visualizer/internal/auth"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
	"github.com/teamviz/team-visualizer/internal/toml"
	"github.com/teamviz/team-visualizer/internal/ws"
)

type Router struct {
	auth *auth.AuthService
	db   *store.Store
	hub  *ws.Hub
}

func New(authSvc *auth.AuthService, db *store.Store, hub *ws.Hub) *Router {
	return &Router{auth: authSvc, db: db, hub: hub}
}

func (r *Router) Routes() chi.Router {
	mux := chi.NewRouter()
	r.RegisterRoutes(mux)
	return mux
}

func (r *Router) RegisterRoutes(mux chi.Router) {
	// Apply auth middleware to all API routes
	mux.Use(r.auth.Middleware)

	// Auth
	mux.Get("/auth/session", r.getSession)

	// People (read)
	mux.Get("/people", r.listPeople)
	mux.Get("/people/{id}", r.getPerson)

	// Planning (read)
	mux.Get("/planning", r.getPlanning)

	// Projects (read)
	mux.Get("/projects", r.listProjects)
	mux.Get("/projects/{id}", r.getProject)
	mux.Get("/projects/{id}/people", r.getProjectPeople)

	// Settings
	mux.Get("/settings", r.getSettings)
	mux.Put("/settings", r.updateSettings)

	// On-call & Rotation (read)
	mux.Get("/oncall", r.getOnCall)
	mux.Get("/rotation", r.getRotation)

	// Holidays (read)
	mux.Get("/holidays", r.listHolidays)

	// Activity (read) — any role
	mux.Get("/activity", r.listActivity)

	// Incidents (read) — any role
	mux.Get("/incidents", r.listIncidents)

	// Me / person mapping — any role
	mux.Get("/me/person", r.getMyPerson)
	mux.Put("/me/person", r.setMyPerson)

	// Users (admin)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Get("/users", r.listUsers)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Put("/users/{id}/person", r.setUserPerson)

	// ICS token management (admin)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/people/{id}/ics-token", r.generateICSToken)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Delete("/people/{id}/ics-token", r.revokeICSToken)

	// WebSocket (real-time updates)
	mux.Get("/ws", r.handleWS)
	// Export (TOML)
	mux.Get("/export", r.exportTOML)

	// === Write routes (mutations) ===

	// People (write) — normal+
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/people", r.addPerson)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/people/{id}", r.updatePerson)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/people/{id}", r.deletePerson)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/people/{id}/archive", r.archivePerson)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/people/{id}/unarchive", r.unarchivePerson)

	// Planning (write) — normal+
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/planning/slot", r.setSlot)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/planning/slot", r.clearSlot)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/planning/range", r.setSlotRange)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/planning/range", r.clearSlotRange)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/planning/copy-week", r.copyWeek)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/planning/prune", r.pruneData)

	// Projects (write) — normal+
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/projects", r.addProject)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/projects/{id}", r.updateProject)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/projects/{id}", r.deleteProject)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/projects/import-csv", r.importProjectCSV)

	// On-call & Rotation (write) — normal+
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/oncall", r.setOnCall)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/oncall", r.removeOnCall)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Put("/rotation", r.setRotation)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Post("/rotation/assign", r.assignRunPerson)
	mux.With(r.auth.RequireRole(model.RoleNormal)).Delete("/rotation", r.removeRotation)

	// Import (TOML + holidays) — admin
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/import", r.importTOML)
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/holidays/import", r.importHolidays)

	// Reset — admin
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Post("/reset", r.resetData)
}

// ===== Health =====

// Health is the public health-check handler (no auth required).
func (r *Router) Health(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "0.2.0",
	})
}

// ===== Auth =====

func (r *Router) getSession(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFromContext(req.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	token := auth.TokenFromContext(req.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  user,
		"token": token,
	})
}

// ===== People =====

func (r *Router) listPeople(w http.ResponseWriter, req *http.Request) {
	people, err := r.db.ListPeople()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if people == nil {
		people = []model.Person{}
	}
	writeJSON(w, http.StatusOK, people)
}

func (r *Router) getPerson(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	person, err := r.db.GetPerson(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if person == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "person not found"})
		return
	}
	writeJSON(w, http.StatusOK, person)
}

// ===== Planning =====

func (r *Router) getPlanning(w http.ResponseWriter, req *http.Request) {
	startDate := req.URL.Query().Get("start")
	endDate := req.URL.Query().Get("end")
	personID := req.URL.Query().Get("person_id")

	if startDate == "" || endDate == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start and end query params required (YYYY/MM/DD)"})
		return
	}

	var entries []model.PlanningEntry
	var err error
	if personID != "" {
		entries, err = r.db.GetPlanningForPerson(personID, startDate, endDate)
	} else {
		entries, err = r.db.GetPlanning(startDate, endDate)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []model.PlanningEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// ===== Projects =====

func (r *Router) listProjects(w http.ResponseWriter, req *http.Request) {
	projects, err := r.db.ListProjects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (r *Router) getProject(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	project, err := r.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if project == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (r *Router) getProjectPeople(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	project, err := r.db.GetProject(id)
	if err != nil || project == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	personIDs, err := r.db.GetPeopleOnProject(project.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Fetch full person details
	type personInfo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var people []personInfo
	for _, pid := range personIDs {
		p, _ := r.db.GetPerson(pid)
		if p != nil {
			people = append(people, personInfo{ID: p.ID, Name: p.Name})
		}
	}
	if people == nil {
		people = []personInfo{}
	}
	writeJSON(w, http.StatusOK, people)
}

// ===== Settings =====

func (r *Router) getSettings(w http.ResponseWriter, req *http.Request) {
	settings, err := r.db.GetSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (r *Router) updateSettings(w http.ResponseWriter, req *http.Request) {
	var settings map[string]string
	if err := json.NewDecoder(req.Body).Decode(&settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	user := auth.UserFromContext(req.Context())
	isAdmin := user != nil && user.Role == model.RoleAdmin
	// Keys any authenticated user may set (app-wide view/operational settings).
	allRoleKeys := map[string]bool{"window_weeks": true, "run_mode": true, "run_target_persons": true}
	// Admin-only keys. Anything outside this set is rejected for everyone.
	adminOnlyKeys := map[string]bool{"prune_weeks": true, "holiday_country": true}
	for k := range settings {
		if !allRoleKeys[k] && !adminOnlyKeys[k] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown setting: " + k})
			return
		}
		if adminOnlyKeys[k] && !isAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only: " + k})
			return
		}
	}
	for k, v := range settings {
		if err := r.db.SetSetting(k, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	r.hub.Broadcast("settings_updated", settings)
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	r.recordEvent(req.Context(), "settings_update", strings.Join(keys, ","), "", map[string]any{"keys": keys})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ===== On-Call =====

func (r *Router) getOnCall(w http.ResponseWriter, req *http.Request) {
	startDate := req.URL.Query().Get("start")
	endDate := req.URL.Query().Get("end")
	if startDate == "" || endDate == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start and end query params required"})
		return
	}
	result, err := r.db.GetOnCall(startDate, endDate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if result == nil {
		result = map[string][]string{}
	}
	writeJSON(w, http.StatusOK, result)
}

// ===== Rotation =====

func (r *Router) getRotation(w http.ResponseWriter, req *http.Request) {
	startDate := req.URL.Query().Get("start")
	endDate := req.URL.Query().Get("end")
	if startDate == "" || endDate == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start and end query params required"})
		return
	}
	result, err := r.db.GetRotation(startDate, endDate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if result == nil {
		result = map[string][]string{}
	}
	writeJSON(w, http.StatusOK, result)
}

// ===== Holidays =====

func (r *Router) listHolidays(w http.ResponseWriter, req *http.Request) {
	holidays, err := r.db.ListHolidays()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if holidays == nil {
		holidays = []model.Holiday{}
	}
	writeJSON(w, http.StatusOK, holidays)
}

// ===== TOML Export =====

func (r *Router) exportTOML(w http.ResponseWriter, req *http.Request) {
	data, err := r.db.GetExportData()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	data.ExportedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Increment export counter (Settings is map[string]any; the counter is
	// always stored as a string in the DB).
	counter, _ := data.Settings["export_counter"].(string)
	if counter == "" {
		counter = "1"
	}
	counterN, _ := strconv.Atoi(counter)
	counterN++
	data.Settings["export_counter"] = strconv.Itoa(counterN)
	r.db.SetSetting("export_counter", strconv.Itoa(counterN))

	tomlBytes, err := toml.Serialize(data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Build filename: YYYY-MM-DD-HH-MM-NNN.toml
	now := time.Now()
	counterStr := strconv.Itoa(counterN)
	for len(counterStr) < 3 {
		counterStr = "0" + counterStr
	}
	filename := now.Format("2006-01-02-15-04") + "-" + counterStr + ".toml"

	w.Header().Set("Content-Type", "text/toml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	w.Write(tomlBytes)
}

// ===== WebSocket =====

func (r *Router) handleWS(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFromContext(req.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	personID, _ := r.db.GetUserPerson(user.Username)
	r.hub.ServeWS(w, req, user.Username, string(user.Role), personID)
}

// ===== Activity =====

func (r *Router) listActivity(w http.ResponseWriter, req *http.Request) {
	limitStr := req.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := r.db.ListEvents(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if events == nil {
		events = []store.ActivityEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// ===== Incidents =====

func (r *Router) listIncidents(w http.ResponseWriter, req *http.Request) {
	incidents, err := r.db.ListIncidents()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if incidents == nil {
		incidents = []store.IncidentEntry{}
	}
	writeJSON(w, http.StatusOK, incidents)
}

// ===== Me / Person Mapping =====

func (r *Router) getMyPerson(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFromContext(req.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	personID, err := r.db.GetUserPerson(user.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"person_id": personID})
}

func (r *Router) setMyPerson(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFromContext(req.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		PersonID string `json:"person_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetUserPerson(user.Username, body.PersonID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"person_id": body.PersonID})
}

// ===== Users (admin) =====

func (r *Router) listUsers(w http.ResponseWriter, req *http.Request) {
	users, err := r.db.ListUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if users == nil {
		users = []store.UserRow{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (r *Router) setUserPerson(w http.ResponseWriter, req *http.Request) {
	idStr := chi.URLParam(req, "id")
	userID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	var body struct {
		PersonID string `json:"person_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := r.db.SetUserPersonByID(userID, body.PersonID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"person_id": body.PersonID})
}

// ===== ICS Token (admin) =====

func (r *Router) generateICSToken(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	// Generate a random 48-char hex token
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	token := hex.EncodeToString(tokenBytes)
	if err := r.db.SetPersonICSToken(id, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Broadcast person_updated so clients refresh ics_token
	if p, _ := r.db.GetPerson(id); p != nil {
		r.hub.Broadcast("person_updated", p)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token": token,
		"url":   "/api/ics/public/" + token,
	})
}

func (r *Router) revokeICSToken(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.db.ClearPersonICSToken(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Broadcast person_updated so clients refresh ics_token
	if p, _ := r.db.GetPerson(id); p != nil {
		r.hub.Broadcast("person_updated", p)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ===== Public ICS Feed (no auth, mounted in main.go) =====

func (r *Router) ServePublicICS(w http.ResponseWriter, req *http.Request) {
	token := chi.URLParam(req, "token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	person, err := r.db.GetPersonByICSToken(token)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if person == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Build ICS for a range: 4 weeks back to 12 weeks forward
	now := time.Now()
	start := now.AddDate(0, 0, -28) // ~4 weeks back
	end := now.AddDate(0, 0, 84)    // ~12 weeks forward
	startStr := start.Format("2006/01/02")
	endStr := end.Format("2006/01/02")

	entries, err := r.db.GetPlanningForPerson(person.ID, startStr, endStr)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	var ics strings.Builder
	ics.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//TeamVisualizer//ICS//EN\r\n")

	for _, e := range entries {
		dt, _ := time.Parse("2006/01/02", e.Date)
		amStart := time.Date(dt.Year(), dt.Month(), dt.Day(), 9, 0, 0, 0, time.UTC)
		amEnd := time.Date(dt.Year(), dt.Month(), dt.Day(), 13, 0, 0, 0, time.UTC)
		pmStart := time.Date(dt.Year(), dt.Month(), dt.Day(), 14, 0, 0, 0, time.UTC)
		pmEnd := time.Date(dt.Year(), dt.Month(), dt.Day(), 18, 0, 0, 0, time.UTC)

		var startTime, endTime time.Time
		if e.Slot == "am" {
			startTime, endTime = amStart, amEnd
		} else {
			startTime, endTime = pmStart, pmEnd
		}

		uid := fmt.Sprintf("%s-%s-%s@teamviz", person.ID, e.Date, e.Slot)
		dtStamp := now.Format("20060102T150405Z")
		dtStart := startTime.Format("20060102T150405Z")
		dtEnd := endTime.Format("20060102T150405Z")

		var summary, description string
		if e.Data.State == "undetermined" {
			summary = "Project: undetermined"
		} else if e.Data.Away != nil {
			summary = "Away: " + e.Data.Away.Type
			description = e.Data.Away.Note
		} else if e.Data.Incident != nil {
			summary = "Incident"
			if e.Data.Incident.Text != "" {
				summary += ": " + e.Data.Incident.Text
			}
		} else if len(e.Data.Projects) > 0 {
			names := make([]string, 0, len(e.Data.Projects))
			for _, p := range e.Data.Projects {
				names = append(names, p.Name)
			}
			summary = "Project: " + strings.Join(names, ", ")
			if e.Data.Run {
				summary += " + Run"
			}
		} else if e.Data.Run {
			summary = "Run duty"
		} else {
			continue
		}

		// Append location flag indicator to summary
		if e.Data.Remote {
			summary += " 🏠"
		} else if e.Data.Offsite {
			summary += " 🏢"
		}

		ics.WriteString(fmt.Sprintf("BEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:%s\r\nDTSTART:%s\r\nDTEND:%s\r\nSUMMARY:%s\r\n",
			uid, dtStamp, dtStart, dtEnd, icsEscape(summary)))
		if description != "" {
			ics.WriteString("DESCRIPTION:" + icsEscape(description) + "\r\n")
		}
		ics.WriteString("END:VEVENT\r\n")
	}

	ics.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(ics.String()))
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// recordEvent logs an audit event and broadcasts it via WebSocket.
// meta carries structured info (person_ids, date, slot, state, ...) so the
// Activity tab can resolve targeted team members and render rich badges.
func (r *Router) recordEvent(ctx context.Context, action, target, detail string, meta map[string]any) {
	actor := ""
	if user := auth.UserFromContext(ctx); user != nil {
		actor = user.Username
	}
	metaJSON := "{}"
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = string(b)
		}
	}
	if err := r.db.RecordEvent(actor, action, target, detail, metaJSON); err != nil {
		// Log but don't fail the request
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	broadcast := map[string]any{
		"actor":  actor,
		"action": action,
		"target": target,
		"detail": detail,
		"ts":     now,
		"meta":   meta,
	}
	if meta == nil {
		broadcast["meta"] = map[string]any{}
	}
	r.hub.Broadcast("activity_new", broadcast)
}

// ===== Helpers =====

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
