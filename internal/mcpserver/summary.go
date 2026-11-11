package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/steermi/cli/internal/config"
)

// summaryPath is ~/.config/steermi/launches/<id>.summary.md.
func summaryPath(launchID string) string {
	if launchID == "" || strings.ContainsAny(launchID, `/\.`) {
		return ""
	}
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "launches", launchID+".summary.md")
}

func writeSummary(path, summary string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(summary), 0o600)
}

// LoadSummary returns the summary recorded by steermi_complete_task.
func LoadSummary(launchID string) string {
	path := summaryPath(launchID)
	if path == "" {
		return ""
	}
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

func SaveActivityResult(state *config.LaunchState) func(string) error {
	return func(raw string) error {
		p := summaryPath(state.LaunchID)
		if p == "" || !json.Valid([]byte(raw)) {
			return errors.New("invalid activity result")
		}
		return writeSummary(p+".result.json", raw)
	}
}
func LoadActivityResult(id string) (json.RawMessage, error) {
	p := summaryPath(id)
	if p == "" {
		return nil, errors.New("invalid launch")
	}
	raw, err := os.ReadFile(p + ".result.json")
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, errors.New("invalid saved activity result")
	}
	return raw, nil
}
