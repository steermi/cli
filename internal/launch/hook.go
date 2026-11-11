package launch

import (
	"context"
	"encoding/json"
	"io"
)

// hookInput is the JSON Claude Code passes to hook commands on stdin.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// Hook handles `steermi hook <event>`. It mirrors the transcript of a
// launch session and is silent (exit 0) everywhere else, so the plugin can
// stay enabled in sessions that are not Steermi launches.
func Hook(ctx context.Context, event string, stdin io.Reader, fallbackCwd string) error {
	var in hookInput
	data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	_ = json.Unmarshal(data, &in)
	cwd := in.Cwd
	if cwd == "" {
		cwd = fallbackCwd
	}
	a, err := LoadActive(cwd)
	if err != nil {
		return nil
	}
	// Hooks come from the steermi plugin; a launch whose agent has no plugin
	// mirrors itself. Only the session that belongs to this launch counts.
	if !adapterFor(a.State).Caps().Plugin {
		return nil
	}
	if id := a.State.AgentRun.SessionID; in.SessionID != "" && id != "" && in.SessionID != id {
		return nil
	}
	path := in.TranscriptPath
	switch event {
	case "flush", "session-end", "stop":
		return Flush(ctx, a, path)
	}
	return nil
}
