package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/steermi/agent-contracts/locallaunch"

	"github.com/steermi/cli/internal/mirror"
)

// NeutralPrompt is the first turn for agents without the Claude Code
// plugin: the Steermi system prompt lives in .steermi/SYSTEM.md and the
// slash commands become plain instructions.
func NeutralPrompt(l Launch) string {
	if l.Resume {
		return "Continue the Steermi task. Re-read .steermi/SYSTEM.md and .steermi/TASK.md if you need the brief again."
	}
	var b strings.Builder
	b.WriteString("You are working on a Steermi task. First read .steermi/SYSTEM.md (your instructions from Steermi) and follow it for the whole session. ")
	b.WriteString(InitialPrompt(l.Identifier, l.ReadOnly()))
	b.WriteString(" Use the steermi MCP tools to read context and to submit results.")
	if l.ReadOnly() {
		b.WriteString(" When finished, call steermi_complete_task with your outputs or findings, then tell the developer to run `steermi done`.")
	} else {
		b.WriteString(" When the work is done and tests pass, call steermi_complete_task with a summary, then run `steermi done` in the shell to push and open the merge request.")
	}
	return b.String()
}

// mcpCommand is the steermi MCP server command (bin defaults to steermi).
func mcpCommand(l Launch) string {
	if l.SteermiBin != "" {
		return l.SteermiBin
	}
	return "steermi"
}

// --- Codex CLI --------------------------------------------------------------

type codex struct{}

func init() { register(codex{}) }

func (codex) Name() string          { return "codex" }
func (codex) ID() locallaunch.Agent { return locallaunch.Codex }
func (codex) Bin() string           { return "codex" }
func (codex) Install() string {
	return "install the Codex CLI (npm i -g @openai/codex) and run `codex login`"
}
func (codex) Caps() Caps { return Caps{Effort: true} }

// Command passes the MCP server and model through `-c` overrides (TOML
// values), so ~/.codex/config.toml is never edited. Shape:
//
//	interactive:  codex [resume ID] OPTIONS PROMPT
//	background:   codex exec --json OPTIONS [resume ID] PROMPT
func (codex) Command(l Launch) (Command, error) {
	var resume []string
	if l.Resume && l.SessionID != "" {
		resume = []string{"resume", l.SessionID}
	}
	opts := []string{
		"-c", "mcp_servers.steermi.command=" + strconv.Quote(mcpCommand(l)),
		"-c", `mcp_servers.steermi.args=["mcp"]`,
	}
	if l.Model != "" {
		opts = append(opts, "--model", l.Model)
	}
	if l.Effort != "" {
		opts = append(opts, "-c", "model_reasoning_effort="+strconv.Quote(l.Effort))
	}
	switch {
	case l.ReadOnly():
		opts = append(opts, "--sandbox", "read-only")
	case l.Mode == Background:
		opts = append(opts, "--sandbox", "workspace-write")
	}
	var args []string
	if l.Mode == Background {
		args = append(append(append([]string{"exec", "--json"}, opts...), resume...), NeutralPrompt(l))
	} else {
		args = append(append(resume, opts...), NeutralPrompt(l))
	}
	return Command{Args: args}, nil
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func codexRollouts() []string {
	var out []string
	root := filepath.Join(codexHome(), "sessions")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// codexMeta reads the session_meta line of a rollout.
func codexMeta(path string) (id, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	if !sc.Scan() {
		return "", ""
	}
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			ID  string `json:"id"`
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "session_meta" {
		return "", ""
	}
	return l.Payload.ID, l.Payload.Cwd
}

func (codex) FindSession(dir string, since time.Time) string {
	want := cleanPath(dir)
	p := newestSince(codexRollouts(), since, func(p string) bool {
		_, cwd := codexMeta(p)
		return cleanPath(cwd) == want
	})
	id, _ := codexMeta(p)
	return id
}

func (codex) Transcript(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	for _, p := range codexRollouts() {
		if strings.HasSuffix(p, sessionID+".jsonl") {
			return p
		}
	}
	return ""
}

func (codex) Parse(data []byte, offset int, ns string) ([]mirror.Message, int, error) {
	return mirror.ConvertCodex(data, offset, ns)
}

// --- GitHub Copilot CLI -----------------------------------------------------

type copilot struct{}

func init() { register(copilot{}) }

func (copilot) Name() string          { return "copilot" }
func (copilot) ID() locallaunch.Agent { return locallaunch.Copilot }
func (copilot) Bin() string           { return "copilot" }
func (copilot) Install() string {
	return "install the GitHub Copilot CLI (npm i -g @github/copilot) and run `copilot` once to log in"
}
func (copilot) Caps() Caps { return Caps{} }

// Command adds the MCP server with --additional-mcp-config (a file under
// .steermi/agent) and maps the runner.yaml allowlist (Bash(x) → shell(x)).
func (copilot) Command(l Launch) (Command, error) {
	cfg, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"steermi": map[string]any{"type": "local", "command": mcpCommand(l), "args": []string{"mcp"}, "tools": []string{"*"}},
	}}, "", "  ")
	path, err := writeAgentFile(l.Dir, "copilot-mcp.json", cfg)
	if err != nil {
		return Command{}, err
	}
	args := []string{"--additional-mcp-config", "@" + path}
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	if l.Resume && l.SessionID != "" {
		args = append(args, "--resume", l.SessionID)
	}
	if l.ReadOnly() {
		args = append(args, "--deny-tool", "write")
	}
	args = append(args, "--allow-tool", "steermi")
	if l.Mode == Background {
		if !l.ReadOnly() {
			args = append(args, "--allow-tool", "write")
		}
		for _, t := range l.AllowedTools {
			if c, ok := copilotTool(t); ok && !(l.ReadOnly() && c == "shell") {
				args = append(args, "--allow-tool", c)
			}
		}
		args = append(args, "-p", NeutralPrompt(l))
	} else {
		args = append(args, "-i", NeutralPrompt(l))
	}
	return Command{Args: args}, nil
}

// copilotTool maps a Claude Code allowlist entry to Copilot's syntax.
func copilotTool(t string) (string, bool) {
	switch {
	case t == "Bash":
		return "shell", true
	case strings.HasPrefix(t, "Bash(") && strings.HasSuffix(t, ")"):
		return "shell(" + strings.TrimSuffix(strings.TrimPrefix(t, "Bash("), ")") + ")", true
	}
	return "", false
}

func copilotHome() string {
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot")
}

// copilotLogs lists session event logs: session-state/<id>.jsonl or
// session-state/<id>/events.jsonl.
func copilotLogs() []string {
	root := filepath.Join(copilotHome(), "session-state")
	a, _ := filepath.Glob(filepath.Join(root, "*.jsonl"))
	b, _ := filepath.Glob(filepath.Join(root, "*", "events.jsonl"))
	return append(a, b...)
}

func copilotID(p string) string {
	if filepath.Base(p) == "events.jsonl" {
		return filepath.Base(filepath.Dir(p))
	}
	return strings.TrimSuffix(filepath.Base(p), ".jsonl")
}

// FindSession picks the newest session log written since the start. Copilot
// logs do not always record the directory, so the window is the only key;
// a second Copilot session started in the same seconds could be confused.
func (copilot) FindSession(_ string, since time.Time) string {
	if p := newestSince(copilotLogs(), since, nil); p != "" {
		return copilotID(p)
	}
	return ""
}

func (copilot) Transcript(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	for _, p := range copilotLogs() {
		if copilotID(p) == sessionID {
			return p
		}
	}
	return ""
}

func (copilot) Parse(data []byte, offset int, ns string) ([]mirror.Message, int, error) {
	return mirror.ConvertCopilot(data, offset, ns)
}

// --- opencode ---------------------------------------------------------------

type opencode struct{}

func init() { register(opencode{}) }

func (opencode) Name() string          { return "opencode" }
func (opencode) ID() locallaunch.Agent { return locallaunch.Opencode }
func (opencode) Bin() string           { return "opencode" }
func (opencode) Install() string {
	return "install opencode (https://opencode.ai) and run `opencode auth login`"
}

// opencode stores sessions as many small files; it does not implement
// Transcripts yet, so Steermi shows MCP activity and the final summary only.
func (opencode) Caps() Caps { return Caps{} }

// Command writes a per-run config (MCP server, read-only permissions) and
// points OPENCODE_CONFIG at it; opencode merges it over the user's config.
func (opencode) Command(l Launch) (Command, error) {
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp": map[string]any{
			"steermi": map[string]any{"type": "local", "command": []string{mcpCommand(l), "mcp"}, "enabled": true},
		},
	}
	if l.ReadOnly() {
		cfg["permission"] = map[string]any{"edit": "deny"}
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	path, err := writeAgentFile(l.Dir, "opencode.json", data)
	if err != nil {
		return Command{}, err
	}
	var args []string
	if l.Mode == Background {
		args = append(args, "run")
	}
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	if l.Mode == Background {
		args = append(args, NeutralPrompt(l))
	} else {
		args = append(args, "--prompt", NeutralPrompt(l))
	}
	return Command{Args: args, Env: []string{"OPENCODE_CONFIG=" + path}}, nil
}

func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}
