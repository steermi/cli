// Package agent adapts coding agents (Claude Code, Codex CLI, GitHub Copilot
// CLI, opencode) to Steermi launches. An adapter builds the agent's command
// line, gives it the steermi MCP server and the launch instructions, and
// tells the mirror where the agent keeps its transcript. Adapters only pass
// per-run flags, environment and files inside the checkout's .steermi/
// directory; they never edit the agent's global configuration.
package agent

import (
	"encoding/json"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steermi/agent-contracts/locallaunch"

	"github.com/steermi/cli/internal/mirror"
)

// Mode is how the agent runs.
type Mode string

const (
	// Interactive runs the agent in the developer's terminal.
	Interactive Mode = "interactive"
	// Background runs the agent headless.
	Background Mode = "background"
)

// Launch describes one agent start.
type Launch struct {
	Mode         Mode
	Identifier   string
	Title        string
	SystemPrompt string
	SessionID    string
	// Resume continues SessionID instead of starting it.
	Resume bool
	// PluginDir loads the embedded steermi plugin for this session only
	// (Claude Code); empty when the plugin is installed globally.
	PluginDir string
	Runner    activity.Runner
	// AllowedTools extends the background permission allowlist
	// (from .steermi/runner.yaml, Claude Code syntax: `Bash(make test)`).
	AllowedTools []string
	// Model is the agent's model id ("" = the agent's own default).
	Model string
	// Effort is low|medium|high ("" = default); ignored by agents without
	// an effort control.
	Effort string
	// Dir is the checkout the agent runs in.
	Dir string
	// SteermiBin is the steermi executable the MCP server runs from.
	SteermiBin string
}

// ReadOnly reports whether the launch may not edit files: every runner
// except code.
func (l Launch) ReadOnly() bool { return !l.Runner.ChangesCode() }

// Command is a prepared agent process.
type Command struct {
	Args []string
	// Env is added to the CLI's environment.
	Env []string
}

// Caps are the switches an adapter has beyond its command line.
type Caps struct {
	// Plugin: the agent loads the embedded steermi plugin (Claude Code),
	// whose hooks call `steermi hook` to mirror the transcript.
	Plugin bool
	// Effort: the agent takes a reasoning effort.
	Effort bool
}

// Agent is one coding-agent adapter.
type Agent interface {
	// Name is the profile name: claude, codex, copilot, opencode.
	Name() string
	// ID is the agent as Steermi records it (agent-contracts locallaunch).
	ID() locallaunch.Agent
	// Bin is the executable looked up on PATH.
	Bin() string
	// Install is the install hint shown when the binary is missing.
	Install() string
	Caps() Caps
	// Command builds the process for l. It may write files under
	// l.Dir/.steermi/agent/.
	Command(l Launch) (Command, error)
}

// Transcripts is implemented by agents whose sessions can be mirrored live
// and resumed. Without it Steermi sees MCP activity and the summary only.
type Transcripts interface {
	// Transcript is the transcript file of sessionID ("" when unknown).
	Transcript(sessionID string) string
	// Parse converts transcript lines into Steermi messages.
	Parse(data []byte, offset int, ns string) ([]mirror.Message, int, error)
}

// SessionFinder is implemented by agents that pick their own session id;
// the others (Claude Code) take the launch's preset id.
type SessionFinder interface {
	// FindSession returns the session that started in dir at or after since
	// ("" when not found yet).
	FindSession(dir string, since time.Time) string
}

// ModelLister suggests model ids for the profile wizard.
type ModelLister interface{ Models() []string }

// Title is the agent's display name.
func Title(a Agent) string { return a.ID().Title() }

// TranscriptsOf returns a's transcript support, or nil.
func TranscriptsOf(a Agent) Transcripts {
	t, _ := a.(Transcripts)
	return t
}

var registry = map[string]Agent{}

func register(a Agent) { registry[a.Name()] = a }

// Get returns the adapter by profile name or Steermi agent id.
func Get(name string) (Agent, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if a, ok := registry[name]; ok {
		return a, true
	}
	for _, a := range registry {
		if string(a.ID()) == name {
			return a, true
		}
	}
	return nil, false
}

// Names lists the adapters, Claude Code first.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == "claude") != (out[j] == "claude") {
			return out[i] == "claude"
		}
		return out[i] < out[j]
	})
	return out
}

// Binary locates a's executable on PATH.
func Binary(a Agent) (string, error) {
	p, err := exec.LookPath(a.Bin())
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH — %s", a.Bin(), a.Install())
	}
	return p, nil
}

// Version returns `<bin> --version`'s version token, or "" when unavailable.
func Version(a Agent) string {
	bin, err := Binary(a)
	if err != nil {
		return ""
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return ""
	}
	for _, f := range strings.Fields(string(out)) {
		f = strings.TrimPrefix(f, "v")
		if f != "" && f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	return ""
}

// Installed lists the adapters whose binary is on PATH.
func Installed() []Agent {
	var out []Agent
	for _, n := range Names() {
		if _, err := exec.LookPath(registry[n].Bin()); err == nil {
			out = append(out, registry[n])
		}
	}
	return out
}

// VersionsJSON is the agent_versions payload sent when pairing.
func VersionsJSON() string {
	v := map[string]string{}
	for _, a := range Installed() {
		if ver := Version(a); ver != "" {
			v[string(a.ID())] = ver
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// writeAgentFile writes name under dir/.steermi/agent (0600: it holds a
// path to the CLI, never a token) and returns its path.
func writeAgentFile(dir, name string, data []byte) (string, error) {
	d := filepath.Join(dir, ".steermi", "agent")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(d, name)
	return p, os.WriteFile(p, data, 0o600)
}

// newestSince returns the newest path in paths modified at or after since
// for which accept returns true.
func newestSince(paths []string, since time.Time, accept func(string) bool) string {
	best, bestTime := "", time.Time{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.ModTime().Before(since.Add(-2*time.Second)) {
			continue
		}
		if info.ModTime().After(bestTime) && (accept == nil || accept(p)) {
			best, bestTime = p, info.ModTime()
		}
	}
	return best
}
