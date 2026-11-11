// Package launch implements `steermi run`, `work`, `next`, `done` and
// `handoff`, plus the active-launch context the MCP server and hooks use.
package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/config"
)

// MarkerDir is the per-checkout directory (git-excluded).
const MarkerDir = ".steermi"

// Marker is .steermi/launch.json: identifiers only, never tokens.
type Marker struct {
	LaunchID   string `json:"launch_id"`
	PublicID   string `json:"public_id"`
	SessionID  string `json:"session_id"`
	WorkItemID string `json:"work_item_id"`
	Identifier string `json:"identifier"`
	ProjectID  string `json:"project_id"`
}

// ErrNoActiveLaunch means the directory is not inside a launch checkout.
var ErrNoActiveLaunch = errors.New("no Steermi launch in this directory — run `steermi next` or `steermi run <id>`")

// FindMarker walks up from dir to the nearest .steermi/launch.json.
func FindMarker(dir string) (*Marker, string, error) {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(cur, MarkerDir, "launch.json"))
		if err == nil {
			var m Marker
			if err := json.Unmarshal(data, &m); err != nil || m.LaunchID == "" {
				return nil, "", fmt.Errorf("corrupt %s/launch.json", MarkerDir)
			}
			return &m, cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, "", ErrNoActiveLaunch
		}
		cur = parent
	}
}

// Active is a launch the current process acts for.
type Active struct {
	Marker *Marker
	Root   string
	State  *config.LaunchState
	Client *api.Client
	// Now is a test seam.
	Now func() time.Time
}

// LoadActive resolves the launch for dir.
func LoadActive(dir string) (*Active, error) {
	m, root, err := FindMarker(dir)
	if err != nil {
		return nil, err
	}
	state, err := config.LoadLaunch(m.LaunchID)
	if err != nil {
		return nil, fmt.Errorf("launch state missing — run `steermi run %s` again: %w", m.PublicID, err)
	}
	client, err := api.Load()
	if err != nil {
		return nil, err
	}
	return &Active{Marker: m, Root: root, State: state, Client: client, Now: time.Now}, nil
}

// Token returns a valid launch token, refreshing it (and the session lock)
// through the device credential when it expires within two minutes.
func (a *Active) Token(ctx context.Context) (string, error) {
	if a.State.LaunchToken != "" && a.Now().Add(2*time.Minute).Before(a.State.TokenExpiry) {
		return a.State.LaunchToken, nil
	}
	token, exp, err := a.Client.RefreshLaunchToken(ctx, a.State.LaunchID)
	if err != nil {
		return "", err
	}
	expiry, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		expiry = a.Now().Add(10 * time.Minute)
	}
	a.State.LaunchToken, a.State.TokenExpiry = token, expiry
	if _, err := config.UpdateLaunch(a.State.LaunchID, func(s *config.LaunchState) {
		s.LaunchToken, s.TokenExpiry = token, expiry
	}); err != nil {
		return "", err
	}
	return token, nil
}

// Do calls the gateway with the launch token.
func (a *Active) Do(ctx context.Context, method, path string, body, out any) error {
	token, err := a.Token(ctx)
	if err != nil {
		return err
	}
	return a.Client.Do(ctx, method, path, body, out, api.TokenAuth(token))
}

// TaskMarkdown reads .steermi/TASK.md.
func (a *Active) TaskMarkdown() string {
	data, _ := os.ReadFile(filepath.Join(a.Root, MarkerDir, "TASK.md"))
	return string(data)
}
