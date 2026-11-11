package plugin

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	root, err := Write(dir, "/Users/me/go/bin/steermi")
	require.NoError(t, err)

	for _, f := range []string{
		".claude-plugin/plugin.json", ".mcp.json", "hooks/hooks.json",
		"commands/next.md", "commands/work.md", "commands/done.md", "commands/handoff.md",
		"skills/steermi-implement/SKILL.md", "skills/steermi-review/SKILL.md", "skills/steermi-followups/SKILL.md",
	} {
		assert.FileExists(t, filepath.Join(root, f))
	}

	var mcpCfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &mcpCfg))
	assert.Equal(t, "/Users/me/go/bin/steermi", mcpCfg.MCPServers["steermi"].Command)
	assert.Equal(t, []string{"mcp"}, mcpCfg.MCPServers["steermi"].Args)

	hooks, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(hooks, &parsed), "hooks.json stays valid JSON")
	assert.Contains(t, string(hooks), "/Users/me/go/bin/steermi hook flush")
	assert.NotContains(t, string(hooks), `"steermi hook`, "every hook calls the running executable")
}

// hookCommands returns the rendered hook command lines.
func hookCommands(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	require.NoError(t, err)
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg), "hooks.json stays valid JSON")
	var out []string
	for _, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out = append(out, h.Command)
			}
		}
	}
	require.NotEmpty(t, out)
	return out
}

func TestWriteQuotesPathsWithSpaces(t *testing.T) {
	root, err := Write(t.TempDir(), "/Applications/My Tools/steermi")
	require.NoError(t, err)
	assert.Contains(t, hookCommands(t, root), `'/Applications/My Tools/steermi' hook flush`)
	mcpCfg, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	require.NoError(t, err)
	assert.Contains(t, string(mcpCfg), `"command": "/Applications/My Tools/steermi"`, "MCP command is exec'd directly: raw path")
}

// TestHookCommandsRunTheRightBinary executes the rendered hook commands
// through a shell for executables in hostile directories: nothing in the
// path may be interpreted.
func TestHookCommandsRunTheRightBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell quoting")
	}
	for _, dir := range []string{"My Tools", "it's", "$(touch pwned)", "`id`", `back\slash`, "semi;colon && x", "new\nline"} {
		t.Run(dir, func(t *testing.T) {
			base := t.TempDir()
			binDir := filepath.Join(base, dir)
			require.NoError(t, os.MkdirAll(binDir, 0o755))
			bin := filepath.Join(binDir, "steermi")
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"ran $*\"\n"), 0o755))

			root, err := Write(t.TempDir(), bin)
			require.NoError(t, err)
			for _, cmd := range hookCommands(t, root) {
				c := exec.Command("/bin/sh", "-c", cmd)
				c.Dir = base
				out, err := c.CombinedOutput()
				require.NoError(t, err, "%s: %s", cmd, out)
				assert.True(t, strings.HasPrefix(string(out), "ran hook "), "%q printed %q", cmd, out)
			}
			assert.NoFileExists(t, filepath.Join(base, "pwned"), "command substitution in the path must not run")
		})
	}
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, "/usr/local/bin/steermi", ShellQuote("/usr/local/bin/steermi"))
	assert.Equal(t, `'/a b/steermi'`, ShellQuote("/a b/steermi"))
	assert.Equal(t, `'/it'\''s/steermi'`, ShellQuote("/it's/steermi"))
	assert.Equal(t, `'$HOME/steermi'`, ShellQuote("$HOME/steermi"))
}

func TestSkillsHaveFrontmatter(t *testing.T) {
	root, err := Write(t.TempDir(), "steermi")
	require.NoError(t, err)
	matches, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	require.NoError(t, err)
	require.Len(t, matches, 3)
	for _, m := range matches {
		data, err := os.ReadFile(m)
		require.NoError(t, err)
		s := string(data)
		assert.True(t, strings.HasPrefix(s, "---\nname: "), m)
		assert.Contains(t, s, "\ndescription: Use when", m)
	}
}
