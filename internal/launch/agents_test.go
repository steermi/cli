package launch

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/hint"

	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/profile"
)

// fakeCodex is a `codex` that records its argv and writes a rollout for the
// directory it runs in, like Codex CLI does.
const fakeCodex = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex-cli 0.9.0"; exit 0; fi
printf '%s\n' "$@" > "$CODEX_ARGS"
d="$CODEX_HOME/sessions/2026/11/06"
mkdir -p "$d"
f="$d/rollout-2026-11-06T10-00-00-0199aaaa-bbbb.jsonl"
printf '{"type":"session_meta","payload":{"id":"0199aaaa-bbbb","cwd":"%s"}}\n' "$PWD" > "$f"
printf '%s\n' '{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Work on STE-1"}]}}' >> "$f"
printf '%s\n' '{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Retry added."}]}}' >> "$f"
`

func TestRunWithCodexProfileMirrorsAndReportsRunner(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	clone, _ := setup(t, srv.URL)

	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", t.TempDir())
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("CODEX_ARGS", argsFile)

	require.NoError(t, (&profile.File{
		Default: "codex",
		Profiles: map[string]profile.Profile{"codex": {Agent: "codex", Models: map[hint.Tier]string{
			hint.Standard: "gpt-std", hint.Deep: "gpt-deep",
		}}},
	}).Save())

	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", Cwd: clone, In: strings.NewReader(""), Out: &out, Err: &out}))
	card := out.String()
	assert.Contains(t, card, "Codex CLI 0.9.0")
	assert.Contains(t, card, "gpt-deep")
	assert.Contains(t, card, "Steermi suggests deep/high: loop round 2")

	raw, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Contains(t, args, "gpt-deep", "the hint's tier picks the profile's deep model")
	assert.Contains(t, args, `model_reasoning_effort="high"`)
	assert.Contains(t, args, `mcp_servers.steermi.args=["mcp"]`)
	assert.Contains(t, args[len(args)-1], ".steermi/SYSTEM.md", "instructions reach agents without the plugin")

	state, err := config.LoadLaunch("launch-1")
	require.NoError(t, err)
	assert.Equal(t, "codex", state.AgentRun.Agent)
	assert.Equal(t, "0199aaaa-bbbb", state.AgentRun.SessionID, "the session is found by directory after start")
	assert.Equal(t, "gpt-deep", state.AgentRun.Model)
	assert.Equal(t, "high", state.AgentRun.Effort)

	posts := gw.find(http.MethodPost, "/v1/ai/chat/sessions/s1/messages")
	require.NotEmpty(t, posts, "the final flush mirrors the Codex rollout")
	msgs := posts[0].body["messages"].([]any)
	require.Len(t, msgs, 2)
	assert.True(t, strings.HasPrefix(msgs[0].(map[string]any)["external_id"].(string), "cx:"))
	assert.Contains(t, out.String(), "Codex CLI exited")

	// A second run resumes the same Codex session and mirrors nothing twice.
	require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", Cwd: clone, In: strings.NewReader(""), Out: &out, Err: &out}))
	raw, _ = os.ReadFile(argsFile)
	assert.True(t, strings.HasPrefix(string(raw), "resume\n"), string(raw))
	assert.Contains(t, string(raw), "0199aaaa-bbbb")
	assert.Len(t, gw.find(http.MethodPost, "/v1/ai/chat/sessions/s1/messages"), 1, "already mirrored lines are not sent again")

	wt := state.Worktree
	require.NoError(t, os.WriteFile(filepath.Join(wt, "retry.go"), []byte("package main\n"), 0o644))
	require.NoError(t, Done(context.Background(), DoneOptions{Cwd: wt, Summary: "Added retries", Commit: true, Out: &out}))
	finish := gw.find(http.MethodPost, "/v1/ai/local-launches/launch-1/finish")
	require.Len(t, finish, 1)
	assert.Equal(t, map[string]any{"agent": "codex", "agent_version": "0.9.0", "model": "gpt-deep", "effort": "high"}, finish[0].body["runner"])
}

func TestRunFlagsOverrideProfileAndRejectUnknownAgent(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	clone, _ := setup(t, srv.URL)
	var out bytes.Buffer
	err := Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", Cwd: clone, Agent: AgentFlags{Agent: "vim"}, Out: &out, Err: &out})
	assert.ErrorContains(t, err, `unknown agent "vim"`)
	err = Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", Cwd: clone, Agent: AgentFlags{Agent: "codex", Effort: "max"}, Out: &out, Err: &out})
	assert.ErrorContains(t, err, "--effort")
}
