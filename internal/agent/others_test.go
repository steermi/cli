package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	assert.Equal(t, []string{"claude", "codex", "copilot", "opencode"}, Names())
	a, ok := Get("claude-code")
	require.True(t, ok)
	assert.Equal(t, "claude", a.Name())
	_, ok = Get("vim")
	assert.False(t, ok)
}

func TestClaudeModelFlag(t *testing.T) {
	args := Args(Launch{Mode: Interactive, Runner: "code", Identifier: "STE-1", Model: "opus"})
	assert.Equal(t, "opus", args[indexOf(args, "--model")+1])
}

func TestCodexCommand(t *testing.T) {
	c, err := codex{}.Command(Launch{Mode: Background, Runner: "findings", Identifier: "STE-2", SteermiBin: "/opt/steermi", Model: "m", Effort: "low"})
	require.NoError(t, err)
	a := c.Args
	assert.Equal(t, []string{"exec", "--json"}, a[:2])
	assert.Contains(t, a, `mcp_servers.steermi.command="/opt/steermi"`)
	assert.Equal(t, "read-only", a[indexOf(a, "--sandbox")+1], "review never writes")
	assert.Contains(t, a, `model_reasoning_effort="low"`)

	c, _ = codex{}.Command(Launch{Mode: Background, Runner: "code", SessionID: "sid", Resume: true})
	assert.Equal(t, "workspace-write", c.Args[indexOf(c.Args, "--sandbox")+1])
	assert.Equal(t, "sid", c.Args[indexOf(c.Args, "resume")+1])

	c, _ = codex{}.Command(Launch{Mode: Interactive, Runner: "code", SessionID: "sid", Resume: true})
	assert.Equal(t, []string{"resume", "sid"}, c.Args[:2], "interactive resume is a subcommand")
}

func TestCodexFindSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := t.TempDir()
	d := filepath.Join(home, "sessions", "2026", "11", "06")
	require.NoError(t, os.MkdirAll(d, 0o755))
	write := func(id, cwd string) {
		line, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "cwd": cwd}})
		require.NoError(t, os.WriteFile(filepath.Join(d, "rollout-x-"+id+".jsonl"), append(line, '\n'), 0o644))
	}
	start := time.Now()
	write("other", t.TempDir())
	write("mine", dir)
	assert.Equal(t, "mine", codex{}.FindSession(dir, start))
	assert.True(t, strings.HasSuffix(codex{}.Transcript("mine"), "mine.jsonl"))
	assert.Equal(t, "", codex{}.FindSession(dir, start.Add(time.Hour)), "older sessions are not taken")
}

func TestCopilotCommand(t *testing.T) {
	dir := t.TempDir()
	c, err := copilot{}.Command(Launch{Mode: Background, Runner: "code", Dir: dir, SteermiBin: "/opt/steermi", Model: "m", AllowedTools: []string{"Bash(make test)", "Edit", "Bash"}})
	require.NoError(t, err)
	a := strings.Join(c.Args, " ")
	assert.Contains(t, a, "--additional-mcp-config @"+filepath.Join(dir, ".steermi", "agent", "copilot-mcp.json"))
	assert.Contains(t, a, "--allow-tool shell(make test)")
	assert.Contains(t, a, "--allow-tool write")
	assert.Contains(t, a, "--model m")
	assert.Equal(t, "-p", c.Args[len(c.Args)-2])
	cfg, err := os.ReadFile(filepath.Join(dir, ".steermi", "agent", "copilot-mcp.json"))
	require.NoError(t, err)
	assert.Contains(t, string(cfg), "/opt/steermi")

	c, _ = copilot{}.Command(Launch{Mode: Background, Runner: "findings", Dir: dir, AllowedTools: []string{"Bash"}})
	a = strings.Join(c.Args, " ")
	assert.Contains(t, a, "--deny-tool write")
	assert.NotContains(t, a, "--allow-tool write")
	assert.NotContains(t, a, "--allow-tool shell")

	c, _ = copilot{}.Command(Launch{Mode: Interactive, Runner: "code", Dir: dir})
	assert.Equal(t, "-i", c.Args[len(c.Args)-2])
}

func TestOpencodeCommand(t *testing.T) {
	dir := t.TempDir()
	c, err := opencode{}.Command(Launch{Mode: Interactive, Runner: "answer", Dir: dir, SteermiBin: "/opt/steermi", Model: "anthropic/x"})
	require.NoError(t, err)
	require.Len(t, c.Env, 1)
	path := strings.TrimPrefix(c.Env[0], "OPENCODE_CONFIG=")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, map[string]any{"edit": "deny"}, cfg["permission"], "plan is read-only")
	assert.Equal(t, []any{"/opt/steermi", "mcp"}, cfg["mcp"].(map[string]any)["steermi"].(map[string]any)["command"])
	assert.Equal(t, "--prompt", c.Args[len(c.Args)-2])
	info, _ := os.Stat(path)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestNeutralPrompt(t *testing.T) {
	p := NeutralPrompt(Launch{Identifier: "STE-3", Runner: "findings"})
	assert.Contains(t, p, ".steermi/SYSTEM.md")
	assert.Contains(t, p, "do not edit files")
	assert.Contains(t, NeutralPrompt(Launch{Runner: "code"}), "steermi done")
	assert.Contains(t, NeutralPrompt(Launch{Resume: true}), "Continue")
}

func indexOf(a []string, v string) int {
	for i, s := range a {
		if s == v {
			return i
		}
	}
	return -2
}
