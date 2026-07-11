package api

import (
	"encoding/json"
	"net/http"
	"strconv"
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

	// Health check
	mux.Get("/health", r.health)

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
	mux.With(r.auth.RequireRole(model.RoleAdmin)).Put("/settings", r.updateSettings)

	// On-call & Rotation (read)
	mux.Get("/oncall", r.getOnCall)
	mux.Get("/rotation", r.getRotation)

	// Holidays (read)
	mux.Get("/holidays", r.listHolidays)

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

func (r *Router) health(w http.ResponseWriter, req *http.Request) {
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
	for k, v := range settings {
		if err := r.db.SetSetting(k, v); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
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
	r.hub.ServeWS(w, req, user.Username, string(user.Role))
}

// ===== Helpers =====

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
