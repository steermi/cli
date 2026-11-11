package launch

import (
	"context"
	"net/http"
	"net/url"
	"os"

	"github.com/steermi/cli/internal/config"
)

// maxBatch keeps one mirror request well under the gateway's 4 MB limit.
const maxBatch = 20

// Flush mirrors new transcript lines into the launch's Steermi session and
// advances the stored offset after each stored batch. It never sends token
// usage: local runs do not count against the organization's AI message
// quota.
//
// Flushes of one launch (hooks, the run heartbeat, done/handoff) run one at
// a time under the launch's mirror lock and start from the offset on disk.
// Every message carries its transcript entry's identity and Steermi stores
// each identity once, so re-sending after a failure or a lost response
// never duplicates messages.
//
// transcript overrides the file (hooks pass the path Claude Code gives
// them); "" uses the launch agent's own transcript.
func Flush(ctx context.Context, a *Active, transcript string) error {
	if a.State.Finished {
		return nil
	}
	path, t := transcriptFor(a.State)
	if transcript == "" {
		transcript = path
	}
	if transcript == "" || t == nil {
		return nil
	}
	unlock, err := config.LockLaunchMirror(ctx, a.State.LaunchID)
	if err != nil {
		return err
	}
	defer unlock()
	if cur, err := config.LoadLaunch(a.State.LaunchID); err == nil {
		a.State.MirrorOffset = cur.MirrorOffset
	}
	data, err := os.ReadFile(transcript)
	if err != nil {
		return err
	}
	msgs, offset, err := t.Parse(data, a.State.MirrorOffset, a.State.AgentRun.SessionID)
	if err != nil {
		return err
	}
	endpoint := "/ai/chat/sessions/" + url.PathEscape(a.State.SessionID) + "/messages"
	for start := 0; start < len(msgs); start += maxBatch {
		end := min(start+maxBatch, len(msgs))
		if err := a.Do(ctx, http.MethodPost, endpoint, map[string]any{"messages": msgs[start:end]}, nil); err != nil {
			return err
		}
		// Lines after the last message (tool results, meta entries) carry
		// nothing to mirror, so the final batch may skip past them.
		next := msgs[end-1].EndLine
		if end == len(msgs) {
			next = offset
		}
		if err := a.saveOffset(next); err != nil {
			return err
		}
	}
	if len(msgs) == 0 && offset != a.State.MirrorOffset {
		return a.saveOffset(offset)
	}
	return nil
}

// saveOffset stores the mirror offset without touching other state fields.
func (a *Active) saveOffset(offset int) error {
	saved, err := config.UpdateLaunch(a.State.LaunchID, func(s *config.LaunchState) {
		if offset > s.MirrorOffset {
			s.MirrorOffset = offset
		}
	})
	if err != nil {
		return err
	}
	a.State.MirrorOffset = saved.MirrorOffset
	return nil
}
