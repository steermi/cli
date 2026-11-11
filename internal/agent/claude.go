package agent

// Claude Code adapter. Flags were checked against Claude Code 2.1: the
// embedded plugin (--plugin-dir) carries the MCP server, hooks, slash
// commands and skills.

import (
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"os"
	"path/filepath"
	"strings"

	"github.com/steermi/agent-contracts/locallaunch"

	"github.com/steermi/cli/internal/mirror"
)

// InitialPrompt is the first user turn for a new session. What to do comes
// from the run's skill (the Steermi instructions and .steermi/TASK.md); the
// prompt only adds the boundary of the run: edit and test, or hand back
// outputs without touching files.
func InitialPrompt(identifier string, readOnly bool) string {
	if readOnly {
		return fmt.Sprintf("Work on Steermi task %s. Read .steermi/TASK.md and follow the run's skill from your Steermi instructions. This run hands back outputs or findings, not code: do not edit files, and save your results with the scoped steermi tools.", identifier)
	}
	return fmt.Sprintf("Work on Steermi task %s. Read .steermi/TASK.md and follow the run's skill from your Steermi instructions: plan with a todo list, make the changes and run the tests.", identifier)
}

// Args returns claude's arguments for l.
func Args(l Launch) []string {
	var args []string
	readOnly := l.ReadOnly()
	if readOnly {
		args = append(args, "--disallowedTools", "Edit,Write,NotebookEdit")
	}
	if l.Mode == Background {
		permissionMode := "acceptEdits"
		if readOnly {
			permissionMode = "default"
		}
		args = append(args, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", permissionMode)
		var allowed []string
		for _, name := range l.AllowedTools {
			if readOnly && (strings.HasPrefix(name, "Edit") || strings.HasPrefix(name, "Write") || strings.HasPrefix(name, "NotebookEdit")) {
				continue
			}
			// Verification commands in findings runs still require the developer's normal
			// Claude permission checks. A Steermi API token grants no filesystem rights.
			if readOnly && (name == "Bash" || l.Runner == activity.Answer && strings.HasPrefix(name, "Bash")) {
				continue
			}
			allowed = append(allowed, name)
		}
		if len(allowed) > 0 {
			args = append(args, "--allowedTools", strings.Join(allowed, ","))
		}
	}

	if l.SessionID != "" {
		if l.Resume {
			args = append(args, "--resume", l.SessionID)
		} else {
			args = append(args, "--session-id", l.SessionID)
		}
	}
	if name := sessionName(l.Identifier, l.Title); name != "" && l.Mode == Interactive {
		args = append(args, "--name", name)
	}
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	if l.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", l.SystemPrompt)
	}
	if l.PluginDir != "" {
		args = append(args, "--plugin-dir", l.PluginDir)
	}
	if !l.Resume || l.Mode == Background {
		prompt := InitialPrompt(l.Identifier, readOnly)
		if l.Resume {
			prompt = "Continue the Steermi task."
		}
		args = append(args, prompt)
	}
	return args
}

func sessionName(identifier, title string) string {
	name := strings.TrimSpace(identifier + " · " + strings.Join(strings.Fields(title), " "))
	name = strings.Trim(name, " ·")
	if r := []rune(name); len(r) > 60 {
		name = string(r[:59]) + "…"
	}
	return name
}

// PluginInstalled reports whether the steermi plugin is installed in Claude
// Code; then the CLI does not load its embedded copy a second time.
func PluginInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"steermi@`)
}

type claude struct{}

func init() { register(claude{}) }

func (claude) Name() string          { return "claude" }
func (claude) ID() locallaunch.Agent { return locallaunch.ClaudeCode }
func (claude) Bin() string           { return "claude" }
func (claude) Install() string {
	return "install Claude Code (https://claude.com/claude-code) and log in"
}
func (claude) Caps() Caps {
	return Caps{Plugin: true}
}
func (claude) Models() []string { return []string{"haiku", "sonnet", "opus"} }

func (claude) Command(l Launch) (Command, error) { return Command{Args: Args(l)}, nil }

func (claude) Transcript(sessionID string) string { return ClaudeTranscript(sessionID) }

func (claude) Parse(data []byte, offset int, ns string) ([]mirror.Message, int, error) {
	return mirror.Convert(data, offset, ns)
}

// ClaudeTranscript is ~/.claude/projects/*/<id>.jsonl ("" when missing).
func ClaudeTranscript(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}
