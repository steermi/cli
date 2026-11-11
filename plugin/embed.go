// Package plugin embeds the steermi Claude Code plugin. `steermi run` writes
// it to the config directory and loads it per session with --plugin-dir, so
// no installation is needed. The same files are published as a marketplace
// plugin (.claude-plugin/marketplace.json at the repository root); that copy
// calls `steermi` from PATH.
package plugin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed all:steermi
var files embed.FS

// Command is how the plugin sources call the CLI. Write replaces it with the
// running executable so a session never picks up another steermi from PATH.
const Command = "steermi"

// Write materializes the plugin under dir, pointing MCP and hooks at the
// given steermi executable. Existing files are replaced so the plugin always
// matches the running CLI.
func Write(dir, steermiBin string) (string, error) {
	root := filepath.Join(dir, "steermi")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	err := fs.WalkDir(files, "steermi", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		switch path {
		case "steermi/.mcp.json":
			data, err = renderMCP(data, steermiBin)
		case "steermi/hooks/hooks.json":
			data, err = renderHooks(data, steermiBin)
		}
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	return root, nil
}

// renderMCP points the steermi MCP server at bin. Claude Code executes the
// command directly (no shell), so the path stays raw.
func renderMCP(data []byte, bin string) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	server, _ := servers["steermi"].(map[string]any)
	if server == nil || server["command"] != Command {
		return nil, fmt.Errorf("mcpServers.steermi.command must be %q", Command)
	}
	server["command"] = bin
	return marshal(cfg)
}

// renderHooks rewrites every hook command that starts with `steermi ` to the
// shell-quoted bin. Hook commands run through a shell.
func renderHooks(data []byte, bin string) ([]byte, error) {
	var cfg any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if s, ok := child.(string); ok && k == "command" && strings.HasPrefix(s, Command+" ") {
					v[k] = ShellQuote(bin) + strings.TrimPrefix(s, Command)
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(cfg)
	return marshal(cfg)
}

func marshal(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// ShellQuote returns s as one POSIX shell word: unchanged when it has no
// special characters, otherwise single-quoted so $, `, \, quotes and spaces
// are never interpreted.
func ShellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
