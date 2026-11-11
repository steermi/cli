// Package config stores the steermi CLI's settings, credentials, repository
// links and per-launch state under ~/.config/steermi (0700, files 0600).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultURL is the Steermi installation the CLI talks to by default. The
// gateway is served under /v1 on the same host.
const DefaultURL = "https://steermi.com"

// Dir is the configuration directory; tests override it.
var Dir = func() (string, error) {
	if d := os.Getenv("STEERMI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "steermi"), nil
}

// ErrNotLoggedIn is returned when no credentials are stored.
var ErrNotLoggedIn = errors.New("not logged in — run `steermi login`")

// Credentials are the paired device's secrets.
type Credentials struct {
	URL          string    `json:"url"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token,omitempty"`
	AccessExpiry time.Time `json:"access_expires_at,omitempty"`
	DeviceID     string    `json:"device_id"`
	DeviceName   string    `json:"device_name"`
	OrgID        string    `json:"org_id"`
	UserID       string    `json:"user_id"`
}

// LaunchState is the private, per-launch state (launch token, mirror offset).
type LaunchState struct {
	ActivityRunID      string          `json:"activity_run_id"`
	InputDigest        string          `json:"input_digest"`
	ActivityGeneration int64           `json:"activity_generation"`
	LaunchID           string          `json:"launch_id"`
	PublicID           string          `json:"public_id"`
	SessionID          string          `json:"session_id"`
	WorkItemID         string          `json:"work_item_id"`
	Identifier         string          `json:"identifier"`
	Runner             activity.Runner `json:"runner"`
	// SkillName is the run's skill as people see it (display only).
	SkillName    string    `json:"skill_name"`
	Branch       string    `json:"branch"`
	BaseBranch   string    `json:"base_branch"`
	Worktree     string    `json:"worktree"`
	RepoKey      string    `json:"repo_key"`
	RepositoryID string    `json:"repository_id"`
	LaunchToken  string    `json:"launch_token"`
	TokenExpiry  time.Time `json:"token_expires_at"`
	// ClaudeSessionID is the session id Steermi assigns to the launch; agents
	// that take a preset session (Claude Code) run with it.
	ClaudeSessionID string `json:"claude_session_id"`
	// AgentRun is the agent the launch last ran with.
	AgentRun AgentRun `json:"agent_run,omitzero"`
	// MirrorOffset is how many transcript lines were already mirrored.
	MirrorOffset int  `json:"mirror_offset"`
	Finished     bool `json:"finished"`
}

func path(parts ...string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{dir}, parts...)...), nil
}

func ensureDir(p string) error {
	return os.MkdirAll(filepath.Dir(p), 0o700)
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes atomically with 0600 permissions.
func writeJSON(p string, v any) error {
	if err := ensureDir(p); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// LoadCredentials reads stored credentials.
func LoadCredentials() (*Credentials, error) {
	p, err := path("credentials.json")
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := readJSON(p, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotLoggedIn
		}
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	if c.RefreshToken == "" {
		return nil, ErrNotLoggedIn
	}
	return &c, nil
}

// SaveCredentials stores credentials (0600).
func SaveCredentials(c *Credentials) error {
	p, err := path("credentials.json")
	if err != nil {
		return err
	}
	return writeJSON(p, c)
}

// DeleteCredentials removes stored credentials.
func DeleteCredentials() error {
	p, err := path("credentials.json")
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RepoLinks maps normalized remotes ("gitlab.com/a/b") to local checkouts.
type RepoLinks map[string]string

// LoadRepoLinks reads repository links.
func LoadRepoLinks() (RepoLinks, error) {
	p, err := path("repos.json")
	if err != nil {
		return nil, err
	}
	links := RepoLinks{}
	if err := readJSON(p, &links); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read repo links: %w", err)
	}
	return links, nil
}

// SaveRepoLinks writes repository links.
func SaveRepoLinks(links RepoLinks) error {
	p, err := path("repos.json")
	if err != nil {
		return err
	}
	return writeJSON(p, links)
}

func launchFile(launchID string) (string, error) {
	if launchID == "" || strings.ContainsAny(launchID, `/\.`) {
		return "", fmt.Errorf("invalid launch id")
	}
	return path("launches", launchID+".json")
}

// LoadLaunch reads a launch's private state.
func LoadLaunch(launchID string) (*LaunchState, error) {
	p, err := launchFile(launchID)
	if err != nil {
		return nil, err
	}
	var s LaunchState
	if err := readJSON(p, &s); err != nil {
		return nil, err
	}
	s.upgrade()
	return &s, nil
}

// SaveLaunch replaces a launch's private state (0600) under its state lock.
// Prefer UpdateLaunch, which keeps fields other processes wrote.
func SaveLaunch(s *LaunchState) error {
	_, err := UpdateLaunch(s.LaunchID, func(cur *LaunchState) { *cur = *s })
	return err
}

// AgentRun is what the launch's agent ran with. A new agent starts a new
// AgentRun (and transcript).
type AgentRun struct {
	// Agent is the adapter name (claude, codex, ...).
	Agent string `json:"agent"`
	// SessionID is the agent session the mirror and resume follow: the
	// preset id for Claude Code, the discovered id for Codex and Copilot.
	SessionID string    `json:"session_id,omitempty"`
	Version   string    `json:"version,omitempty"`
	Model     string    `json:"model,omitempty"`
	Effort    string    `json:"effort,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
}

// upgrade reads state written before AgentRun as a Claude Code launch.
func (s *LaunchState) upgrade() {
	if s.AgentRun.Agent == "" && s.ClaudeSessionID != "" {
		s.AgentRun = AgentRun{Agent: "claude", SessionID: s.ClaudeSessionID}
	}
}

// ListLaunches returns every stored launch state (unreadable files are
// skipped), newest first by modification time.
func ListLaunches() ([]*LaunchState, error) {
	dir, err := path("launches")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type item struct {
		s   *LaunchState
		mod time.Time
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		s, err := LoadLaunch(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		info, _ := e.Info()
		var mod time.Time
		if info != nil {
			mod = info.ModTime()
		}
		items = append(items, item{s, mod})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	out := make([]*LaunchState, 0, len(items))
	for _, it := range items {
		out = append(out, it.s)
	}
	return out, nil
}
