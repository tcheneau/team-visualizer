package toml

import (
	"bytes"
	"fmt"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/teamviz/team-visualizer/internal/model"
	"github.com/teamviz/team-visualizer/internal/store"
)

// Serialize produces a TOML representation of all exported data.
func Serialize(data *store.ExportData) ([]byte, error) {
	if data.ExportedAt == "" || data.ExportedAt == "now" {
		data.ExportedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	if data.People == nil {
		data.People = []store.ExportPerson{}
	}
	if data.Planning == nil {
		data.Planning = []store.ExportPlanning{}
	}
	if data.Projects == nil {
		data.Projects = []model.Project{}
	}
	if data.OnCall == nil {
		data.OnCall = []store.ExportKeyVal{}
	}
	if data.Rotation == nil {
		data.Rotation = []store.ExportKeyVal{}
	}

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Parse decodes TOML bytes into ExportData for import.
func Parse(data []byte) (*store.ExportData, error) {
	var ed store.ExportData
	if err := toml.Unmarshal(data, &ed); err != nil {
		return nil, fmt.Errorf("toml decode: %w", err)
	}
	if ed.SchemaVersion == 0 {
		ed.SchemaVersion = 1
	}
	return &ed, nil
}

// HolidayData is the TOML structure for holiday import files.
type HolidayData struct {
	SchemaVersion int            `toml:"schema_version"`
	Country       string         `toml:"country"`
	Holidays      []HolidayEntry `toml:"holidays"`
}

type HolidayEntry struct {
	Date  string `toml:"date"`
	Label string `toml:"label"`
}

// ParseHolidays decodes a holidays TOML file.
func ParseHolidays(data []byte) ([]model.Holiday, error) {
	var hd HolidayData
	if err := toml.Unmarshal(data, &hd); err != nil {
		return nil, fmt.Errorf("toml decode: %w", err)
	}
	holidays := make([]model.Holiday, 0, len(hd.Holidays))
	for _, h := range hd.Holidays {
		holidays = append(holidays, model.Holiday{
			Date:    h.Date,
			Label:   h.Label,
			Country: hd.Country,
		})
	}
	return holidays, nil
}