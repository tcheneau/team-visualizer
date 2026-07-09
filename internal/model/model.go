package model

import "time"

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleNormal   Role = "normal"
	RoleReadonly Role = "readonly"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

type Person struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	SubTeam         string   `json:"sub_team"`
	AvatarEmoji     string   `json:"avatar_emoji"`
	AvatarColor     string   `json:"avatar_color"`
	StartDate       string   `json:"start_date"`
	DefaultProjects []string `json:"default_projects"`
	Status          string   `json:"status"`
	ArchivedDate    string   `json:"archived_date"`
	IsGuest         bool     `json:"is_guest"`
}

type SlotData struct {
	State    string          `json:"state"`
	Away     *AwayData       `json:"away"`
	Projects []ProjectAssign `json:"projects"`
	Run      bool            `json:"run"`
}

type AwayData struct {
	Type string `json:"type"`
	Note string `json:"note"`
}

type ProjectAssign struct {
	Name string `json:"name"`
	Pct  int    `json:"pct"`
}

type PlanningEntry struct {
	PersonID string    `json:"person_id"`
	Date     string    `json:"date"`
	Slot     string    `json:"slot"`
	Data     SlotData  `json:"data"`
}

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Emoji       string `json:"emoji"`
	Description string `json:"description"`
	URL         string `json:"url"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Status      string `json:"status"`
}

type Settings struct {
	WindowWeeks      int    `json:"window_weeks"`
	PruneWeeks       int    `json:"prune_weeks"`
	WeekStarts       string `json:"week_starts"`
	RunMode          string `json:"run_mode"`
	RunTargetPersons int    `json:"run_target_persons"`
	Theme            string `json:"theme"`
	ExportCounter    int    `json:"export_counter"`
}

type Holiday struct {
	Date    string `json:"date"`
	Label   string `json:"label"`
	Country string `json:"country"`
}