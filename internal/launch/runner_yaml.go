package launch

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadAllowedTools reads `allowed_tools` from .steermi/runner.yaml, which
// teams commit to widen what a background run may execute without asking.
// Only the simple form is supported (entries use Claude Code syntax; the
// Copilot adapter maps Bash(x) to shell(x)):
//
//	profile: codex
//	allowed_tools:
//	  - "Bash(make test)"
//	  - "Bash(go test *)"
func loadAllowedTools(dir string) []string { return loadRunner(dir).AllowedTools }

// runnerConfig is .steermi/runner.yaml.
type runnerConfig struct {
	// Profile is the team's preferred agent profile (`profile: codex`); a
	// developer without that profile keeps their own default.
	Profile      string
	AllowedTools []string
}

func loadRunner(dir string) runnerConfig {
	var rc runnerConfig
	f, err := os.Open(filepath.Join(dir, MarkerDir, "runner.yaml"))
	if err != nil {
		return rc
	}
	defer func() { _ = f.Close() }()
	inList := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#") || trimmed == "":
			continue
		case strings.HasPrefix(line, "profile:"):
			rc.Profile = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "profile:")), `"'`)
			inList = false
		case strings.HasPrefix(line, "allowed_tools:"):
			inList = true
		case inList && strings.HasPrefix(trimmed, "- "):
			v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), `"'`)
			if v != "" && !strings.ContainsAny(v, "\n\r") {
				rc.AllowedTools = append(rc.AllowedTools, v)
			}
		case !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			inList = false
		}
	}
	return rc
}
