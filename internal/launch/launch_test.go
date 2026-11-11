package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"

	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/gitx"
	"github.com/steermi/cli/internal/mcpserver"
)

const remoteURL = "https://gitlab.com/acme/app.git"

type gatewayCall struct {
	method, path, auth string
	body               map[string]any
}

type fakeGateway struct {
	mu    sync.Mutex
	calls []gatewayCall
}

func (g *fakeGateway) record(r *http.Request) map[string]any {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	g.mu.Lock()
	g.calls = append(g.calls, gatewayCall{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
	g.mu.Unlock()
	return body
}

func (g *fakeGateway) find(method, path string) []gatewayCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []gatewayCall
	for _, c := range g.calls {
		if c.method == method && c.path == path {
			out = append(out, c)
		}
	}
	return out
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (g *fakeGateway) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.record(r)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/ai/local-launches/lx_7Q4XK2M9ab/claim":
			assert.Equal(t, "Bearer device-token", r.Header.Get("Authorization"))
			reply(w, map[string]any{
				"launch": map[string]any{
					"id": "launch-1", "public_id": "lx_7Q4XK2M9ab", "project_id": "p1", "work_item_id": "wi-1",
					"activity_run_id": "run-1", "input_digest": "pin-1", "activity_generation": 1,
					"session_id": "s1", "runner": "code", "mode": "interactive", "branch": "feat/STE-1-add-retry",
					"base_branch": "main", "state": "running",
				},
				"launch_token":      "launch-token",
				"token_expires_at":  time.Now().Add(15 * time.Minute).Format(time.RFC3339),
				"system_prompt":     "SYSTEM",
				"task_markdown":     "# STE-1 · Add retry\n",
				"work_item_json":    `{"id":"wi-1","identifier":"STE-1","title":"Add retry","type":"story","project_id":"p1"}`,
				"claude_session_id": "11111111-2222-3333-4444-555555555555",
				"skill_name":        "Build",
				"model_hint":        map[string]string{"tier": "deep", "effort": "high", "reason": "loop round 2"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/ai/activities/run-1":
			cfg, _ := config.Dir()
			sha, e := gitx.HeadSHA(filepath.Join(filepath.Dir(cfg), "origin.git"))
			require.NoError(t, e)
			reply(w, map[string]any{"id": "run-1", "input_digest": "pin-1", "generation": 1, "inputs": []map[string]string{{"kind": "repository", "resource_id": "r1", "repository_id": "r1", "commit_sha": sha}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/integrations/git/repositories":
			assert.Equal(t, "p1", r.URL.Query().Get("project_id"))
			assert.Equal(t, "Bearer launch-token", r.Header.Get("Authorization"), "repo listing uses the scoped launch token")
			reply(w, map[string]any{"repositories": []map[string]string{{"id": "r1", "project_id": "p1", "clone_url": remoteURL, "default_branch": "main"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/integrations/git/repositories/r1/pulls":
			reply(w, map[string]string{"pr_url": "https://gitlab.com/acme/app/-/merge_requests/7"})
		default:
			reply(w, map[string]any{})
		}
	})
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	require.NoError(t, err)
	return out
}

// setup creates a bare origin reachable as remoteURL (insteadOf), a clone of
// it, isolated config and worktree directories, and stored credentials.
func setup(t *testing.T, gatewayURL string) (clone, origin string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "app")
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(
		"[user]\n\temail = t@example.com\n\tname = T\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"+
			"[url \""+origin+"\"]\n\tinsteadOf = "+remoteURL+"\n"), 0o644))

	gitT(t, root, "init", "--bare", "-b", "main", origin)
	gitT(t, root, "init", "-b", "main", seed)
	require.NoError(t, os.WriteFile(filepath.Join(seed, "main.go"), []byte("package main\n"), 0o644))
	gitT(t, seed, "add", ".")
	gitT(t, seed, "commit", "-m", "init")
	gitT(t, seed, "remote", "add", "origin", remoteURL)
	gitT(t, seed, "push", "origin", "main")
	gitT(t, root, "clone", remoteURL, clone)

	cfgDir := filepath.Join(root, "config")
	prevDir, prevWT := config.Dir, WorktreeRoot
	config.Dir = func() (string, error) { return cfgDir, nil }
	WorktreeRoot = func() (string, error) { return filepath.Join(root, "worktrees"), nil }
	t.Cleanup(func() { config.Dir, WorktreeRoot = prevDir, prevWT })
	require.NoError(t, config.SaveCredentials(&config.Credentials{
		URL: gatewayURL, RefreshToken: "std_x", AccessToken: "device-token", AccessExpiry: time.Now().Add(time.Hour),
		DeviceID: "dev-1", DeviceName: "mbp", OrgID: "org-1", UserID: "user-1",
	}))
	return clone, origin
}

func TestRunPreparesWorktreeAndDoneDelivers(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	clone, origin := setup(t, srv.URL)
	var out bytes.Buffer

	// steermi run lx_… --no-agent from inside the developer's checkout.
	require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Cwd: clone, Out: &out, Err: &out}))
	wt, err := WorktreeRoot()
	require.NoError(t, err)
	wt = filepath.Join(wt, "app", "STE-1")
	branch, err := gitx.CurrentBranch(wt)
	require.NoError(t, err)
	assert.Equal(t, "feat/STE-1-add-retry", branch)
	task, err := os.ReadFile(filepath.Join(wt, ".steermi", "TASK.md"))
	require.NoError(t, err)
	assert.Equal(t, "# STE-1 · Add retry\n", string(task))
	marker, _, err := FindMarker(filepath.Join(wt, "sub", "dir"))
	require.NoError(t, err, "the marker is found from subdirectories")
	assert.Equal(t, "launch-1", marker.LaunchID)
	markerRaw, _ := os.ReadFile(filepath.Join(wt, ".steermi", "launch.json"))
	assert.NotContains(t, string(markerRaw), "launch-token", "tokens never land in the checkout")
	dirty, err := gitx.Dirty(wt)
	require.NoError(t, err)
	assert.False(t, dirty, ".steermi/ is excluded from git")
	state, err := config.LoadLaunch("launch-1")
	require.NoError(t, err)
	assert.Equal(t, "r1", state.RepositoryID)
	assert.Equal(t, "launch-token", state.LaunchToken)
	assert.Contains(t, out.String(), "Checkout ready")

	// The agent's Stop hook mirrors new transcript lines into the session.
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte(
		`{"type":"user","message":{"role":"user","content":"Work on STE-1"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"On it."}]}}`+"\n"), 0o644))
	hookIn := `{"session_id":"11111111-2222-3333-4444-555555555555","transcript_path":"` + transcript + `","cwd":"` + wt + `"}`
	require.NoError(t, Hook(context.Background(), "flush", strings.NewReader(hookIn), ""))
	posts := gw.find(http.MethodPost, "/v1/ai/chat/sessions/s1/messages")
	require.Len(t, posts, 1)
	assert.Equal(t, "Bearer launch-token", posts[0].auth)
	assert.Len(t, posts[0].body["messages"], 2)
	assert.Nil(t, posts[0].body["usage"], "local runs never report token usage")
	require.NoError(t, Hook(context.Background(), "flush", strings.NewReader(hookIn), ""))
	assert.Len(t, gw.find(http.MethodPost, "/v1/ai/chat/sessions/s1/messages"), 1, "already mirrored lines are not sent again")

	// A hook from an unrelated Claude session is ignored.
	other := `{"session_id":"other","transcript_path":"` + transcript + `","cwd":"` + wt + `"}`
	require.NoError(t, Hook(context.Background(), "flush", strings.NewReader(other), ""))

	// Uncommitted work blocks `done` unless --commit.
	require.NoError(t, os.WriteFile(filepath.Join(wt, "retry.go"), []byte("package main\n"), 0o644))
	err = Done(context.Background(), DoneOptions{Cwd: wt, Out: io.Discard})
	assert.ErrorContains(t, err, "uncommitted")
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-m", "feat: add retry (STE-1)")

	require.NoError(t, os.WriteFile(filepath.Join(state.Worktree, ".steermi", "ignored"), []byte("x"), 0o644))
	require.NoError(t, Done(context.Background(), DoneOptions{Cwd: wt, Summary: "Added retries with backoff", Out: &out}))
	assert.Contains(t, gitT(t, origin, "branch", "--list", "feat/STE-1-add-retry"), "feat/STE-1-add-retry", "branch pushed with the developer's git")

	prs := gw.find(http.MethodPost, "/v1/integrations/git/repositories/r1/pulls")
	require.Len(t, prs, 1)
	assert.Equal(t, "feat/STE-1-add-retry", prs[0].body["head"])
	assert.Equal(t, "main", prs[0].body["base"])
	assert.Equal(t, "STE-1: Added retries with backoff", prs[0].body["title"])
	comments := gw.find(http.MethodPost, "/v1/work_items/wi-1/comments")
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0].body["body_markdown"], "merge_requests/7")
	finish := gw.find(http.MethodPost, "/v1/ai/local-launches/launch-1/finish")
	require.Len(t, finish, 1)
	assert.Equal(t, "completed", finish[0].body["state"])
	assert.Len(t, finish[0].body["commits"], 1)
	assert.Contains(t, out.String(), "STE-1 delivered")

	err = Done(context.Background(), DoneOptions{Cwd: wt, Out: io.Discard})
	assert.ErrorContains(t, err, "already finished")
}

func TestDoneRefusesDefaultBranch(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	clone, _ := setup(t, srv.URL)
	require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Here: true, Cwd: clone, Out: io.Discard, Err: io.Discard}))
	branch, err := gitx.CurrentBranch(clone)
	require.NoError(t, err)
	assert.Equal(t, "feat/STE-1-add-retry", branch, "--here switches the current checkout to the launch branch")
	gitT(t, clone, "checkout", "main")
	err = Done(context.Background(), DoneOptions{Cwd: clone, Out: io.Discard})
	assert.ErrorContains(t, err, "refusing to push")
}

func TestRunNeedsLinkedCheckout(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	_, _ = setup(t, srv.URL)
	elsewhere := t.TempDir()
	err := Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Cwd: elsewhere, Out: io.Discard, Err: io.Discard})
	assert.ErrorContains(t, err, "steermi repos link")
}

func TestRunWithLinkedCheckoutFromAnywhere(t *testing.T) {
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	clone, _ := setup(t, srv.URL)
	require.NoError(t, config.SaveRepoLinks(config.RepoLinks{gitx.NormalizeRemote(remoteURL): clone}))
	require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Cwd: t.TempDir(), Out: io.Discard, Err: io.Discard}))
}

func TestRunRejectsMalformedIDs(t *testing.T) {
	for _, bad := range []string{"", "lx_short", "../../etc", "STE-123"} {
		err := Run(context.Background(), RunOptions{PublicID: bad, Out: io.Discard, Err: io.Discard})
		assert.ErrorContains(t, err, "not a launch id", bad)
	}
}

func TestFriendlyClaimErrors(t *testing.T) {
	for code, want := range map[string]string{
		"LOCAL_LAUNCH_NOT_FOUND":       "belong to someone else",
		"LOCAL_LAUNCH_EXPIRED":         "expired",
		"LOCAL_LAUNCH_ALREADY_CLAIMED": "another device",
	} {
		err := friendlyClaimError(&api.Error{Status: 400, Code: code})
		assert.ErrorContains(t, err, want, code)
	}
}

func TestLoadAllowedTools(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".steermi"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".steermi", "runner.yaml"), []byte(
		"setup: [make deps]\n# comment\nallowed_tools:\n  - \"Bash(make test)\"\n  - 'Bash(go test *)'\nmax_turns: 80\n  - \"Bash(rm -rf /)\"\n"), 0o644))
	assert.Equal(t, []string{"Bash(make test)", "Bash(go test *)"}, loadAllowedTools(dir))
	assert.Nil(t, loadAllowedTools(t.TempDir()))
}

func TestDoneAnswerAndFindingsSubmitWithoutCommits(t *testing.T) {
	for _, runner := range []activity.Runner{activity.Answer, activity.Findings} {
		t.Run(string(runner), func(t *testing.T) {
			gw := &fakeGateway{}
			srv := httptest.NewServer(gw.handler(t))
			defer srv.Close()
			clone, _ := setup(t, srv.URL)
			require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Cwd: clone, Out: io.Discard, Err: io.Discard}))
			root, _ := WorktreeRoot()
			wt := filepath.Join(root, "app", "STE-1")
			_, err := config.UpdateLaunch("launch-1", func(s *config.LaunchState) { s.Runner = runner })
			require.NoError(t, err)
			require.NoError(t, mcpserver.SaveActivityResult(&config.LaunchState{LaunchID: "launch-1"})(`{"outputs":[{"kind":"docs","resource_id":"page"}],"recommendation":"approve","findings":[]}`))
			require.NoError(t, Done(context.Background(), DoneOptions{Cwd: wt, Summary: "Saved findings and source evidence", Out: io.Discard}))
			posts := gw.find(http.MethodPost, "/v1/ai/local-launches/launch-1/finish")
			require.Len(t, posts, 1)
			require.NotContains(t, posts[0].body, "commits")
			require.NotContains(t, posts[0].body, "mr_url")
			require.Empty(t, gw.find(http.MethodPost, "/v1/integrations/git/repositories/r1/pulls"))
			state, err := config.LoadLaunch("launch-1")
			require.NoError(t, err)
			require.True(t, state.Finished)
		})
	}
}

func TestHandoffAnswerAndFindingsLeaveDirtyFilesUncommitted(t *testing.T) {
	for _, runner := range []activity.Runner{activity.Answer, activity.Findings} {
		t.Run(string(runner), func(t *testing.T) {
			gw := &fakeGateway{}
			srv := httptest.NewServer(gw.handler(t))
			defer srv.Close()
			clone, _ := setup(t, srv.URL)
			require.NoError(t, Run(context.Background(), RunOptions{PublicID: "lx_7Q4XK2M9ab", NoAgent: true, Cwd: clone, Out: io.Discard, Err: io.Discard}))
			root, _ := WorktreeRoot()
			wt := filepath.Join(root, "app", "STE-1")
			_, err := config.UpdateLaunch("launch-1", func(s *config.LaunchState) { s.Runner = runner })
			require.NoError(t, err)
			before, err := gitx.HeadSHA(wt)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(wt, "research-notes.md"), []byte("private draft"), 0600))
			require.NoError(t, Handoff(context.Background(), HandoffOptions{Cwd: wt, Summary: "Continue this research", Out: io.Discard}))
			after, err := gitx.HeadSHA(wt)
			require.NoError(t, err)
			require.Equal(t, before, after)
			dirty, err := gitx.Dirty(wt)
			require.NoError(t, err)
			require.True(t, dirty)
			posts := gw.find(http.MethodPost, "/v1/ai/local-launches/launch-1/handoff")
			require.Len(t, posts, 1)
			require.Empty(t, posts[0].body["branch"])
			require.Empty(t, posts[0].body["head_sha"])
		})
	}
}

func TestDescribeNextFollowsThePinnedHop(t *testing.T) {
	run := func(hops map[activity.Outcome]activity.FlowHop) activity.Run {
		return activity.Run{Runner: activity.Code, State: activity.Submitted, Flow: &activity.FlowRef{VersionID: "v", Node: "build", Hops: hops}}
	}
	auto := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "qa_check", Name: "Security audit", Runner: activity.Findings, Auto: true}}))
	if !strings.Contains(auto, "Security audit starts automatically in the cloud") {
		t.Fatalf("auto hop: %q", auto)
	}
	ask := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "review", Name: "Review", Runner: activity.Findings, Statuses: []activity.StatusTarget{{Category: "in_review"}}}}))
	if !strings.Contains(ask, "Next: Review. Start it from the work item") || !strings.Contains(ask, "moves to in review") {
		t.Fatalf("ask hop: %q", ask)
	}
	if got := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Stop: "end"}})); got != "Next: the delivery flow ends here." {
		t.Fatalf("end: %q", got)
	}
	if got := describeNext(activity.Run{Runner: activity.Code, State: activity.Submitted}); got != nextStepHint {
		t.Fatalf("no flow: %q", got)
	}
}
