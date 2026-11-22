package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		body := g.record(r)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/ai/local-launches/lx_7Q4XK2M9ab/claim":
			assert.Equal(t, "Bearer device-token", r.Header.Get("Authorization"))
			assert.NotNil(t, body, "the claim always sends its body")
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
	const link = "https://steermi.test/work-items/key/STE-1"
	run := func(hops map[activity.Outcome]activity.FlowHop) runView {
		return runView{Run: activity.Run{Runner: activity.Code, State: activity.Submitted, Flow: &activity.FlowRef{VersionID: "v", Node: "build", Hops: hops}}}
	}
	review := activity.FlowHop{Node: "review", Name: "Review", Runner: activity.Findings, Auto: true, Location: activity.Cloud}

	auto := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: review}), link)
	assert.Equal(t, "Review starts in Steermi: "+link, auto)

	paused := run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: review})
	reason := activity.PausedNoProviderKey
	paused.AutoPausedReason = &reason
	assert.Equal(t, "Paused: no AI key for Steermi runs. Next: Review — start it from the work item ("+link+"), in the cloud or on this machine.", describeNext(paused, link))

	stepped := run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: review})
	stepped.StepMode = activity.StepModeAsk
	assert.Contains(t, describeNext(stepped, link), "Paused after this step")

	local := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "build", Name: "Build", Runner: activity.Code, Location: activity.Local}}), link)
	assert.Equal(t, "Next: Build runs on this machine: start it from the work item ("+link+") for its `steermi run` command.", local)

	ask := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "review", Name: "Review", Runner: activity.Findings, Statuses: []activity.StatusTarget{{Category: "in_review"}}}}), "")
	assert.Equal(t, "Next: Review — start it from the work item, in the cloud or on this machine. The item moves to in review.", ask)
	assert.NotContains(t, ask, "steermi work", "never a new run beside the flow")

	unresolved := describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "review", Name: "Review", Runner: activity.Findings, Auto: true}}), "")
	assert.Contains(t, unresolved, "Next: Review — start it", "a hop without a pinned location never starts by itself")

	assert.Equal(t, "Next: the delivery flow ends here.", describeNext(run(map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Stop: "end"}}), link))
	assert.Equal(t, nextStepHint, describeNext(runView{Run: activity.Run{Runner: activity.Code, State: activity.Submitted}}, link))
}

func TestConsentLineNamesTheLoop(t *testing.T) {
	loop := &api.FlowLoop{Steps: []string{"Build", "Review"}, MaxIterations: 3, BudgetUSD: 20, Advance: activity.LoopAdvanceAuto}
	assert.Equal(t, "Zoe will run Build → Review automatically · max 3 rounds · no new round after $20", consentLine(loop, ""))

	loop.BudgetUSD = 7.5
	assert.Equal(t, "Zoe will run Build → Review, pausing after each step · max 3 rounds · no new round after $7.50",
		consentLine(loop, activity.StepModeAsk), "the claim recorded --pause-after-step")

	asks := &api.FlowLoop{Steps: []string{"Build", "Review"}, MaxIterations: 2, Advance: activity.LoopAdvanceAsk}
	assert.Equal(t, "Zoe will run Build → Review, pausing after each step · max 2 rounds", consentLine(asks, ""))

	reviewed := &api.FlowLoop{Steps: []string{"Build", "Security audit"}, MaxIterations: 3, Advance: activity.LoopAdvanceAuto, ReviewStep: "Security audit"}
	assert.Equal(t, "Zoe will run Build → Security audit automatically until Security audit is clean · max 3 rounds", consentLine(reviewed, ""),
		"the server names the loop's findings step")
	assert.Empty(t, consentLine(nil, ""), "no loop, no line")
	assert.Empty(t, consentLine(&api.FlowLoop{}, ""))
}

// done watches an automatic next step briefly and reports why it paused.
func TestNextStepWatchesTheAutomaticHop(t *testing.T) {
	prevFor, prevEvery := pausePollFor, pausePollEvery
	pausePollFor, pausePollEvery = 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { pausePollFor, pausePollEvery = prevFor, prevEvery })

	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/ai/activities/run-1", r.URL.Path)
		assert.Equal(t, "Bearer launch-token", r.Header.Get("Authorization"), "a finished launch reads its own run")
		mu.Lock()
		reads++
		n := reads
		mu.Unlock()
		view := map[string]any{"id": "run-1", "runner": "code", "state": "submitted", "skill_name": "Build", "auto_paused_reason": nil,
			"flow": map[string]any{"version_id": "system:2", "node": "build", "hops": map[string]any{"done": map[string]any{"node": "review", "name": "Review", "runner": "findings", "auto": true, "location": "cloud"}}}}
		if n >= 3 {
			view["auto_paused_reason"] = "no_provider_key"
		}
		reply(w, view)
	}))
	defer srv.Close()
	a := &Active{
		State:  &config.LaunchState{ActivityRunID: "run-1", Identifier: "STE-1", LaunchToken: "launch-token", TokenExpiry: time.Now().Add(time.Hour)},
		Client: api.New(srv.URL), Now: time.Now,
	}
	got := nextStep(context.Background(), a)
	assert.Equal(t, "Paused: no AI key for Steermi runs. Next: Review — start it from the work item ("+srv.URL+"/work-items/key/STE-1), in the cloud or on this machine.", got)
	assert.Equal(t, 3, reads)

	// Nothing paused within the window: the step starts in Steermi.
	pausePollFor = 30 * time.Millisecond
	mu.Lock()
	reads = -100
	mu.Unlock()
	assert.Equal(t, "Review starts in Steermi: "+srv.URL+"/work-items/key/STE-1", nextStep(context.Background(), a))
}

// --pause-after-step asks for step mode ask once, on the claim (create
// sends none).
func TestPauseAfterStepSendsStepMode(t *testing.T) {
	assert.Equal(t, activity.StepModeAsk, RunOptions{PauseAfterStep: true}.stepMode())
	assert.Empty(t, RunOptions{}.stepMode())
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, map[string]any{})
	}))
	defer srv.Close()
	c := api.FromCredentials(&config.Credentials{URL: srv.URL, AccessToken: "device-token", AccessExpiry: time.Now().Add(time.Hour)})
	_, err := c.Claim(context.Background(), "lx_7Q4XK2M9ab", activity.StepModeAsk)
	require.NoError(t, err)
	assert.Equal(t, "ask", body["step_mode"])
	_, err = c.Claim(context.Background(), "lx_7Q4XK2M9ab", "")
	require.NoError(t, err)
	assert.NotContains(t, body, "step_mode")
	_, err = c.CreateLaunch(context.Background(), api.CreateLaunchRequest{WorkItemIdentifier: "STE-1"})
	require.NoError(t, err)
	assert.NotContains(t, body, "step_mode")
}

// Once the server reports the run's successor, done names it and links its
// live session instead of waiting out the poll.
func TestNextStepNamesTheSuccessor(t *testing.T) {
	prevFor, prevEvery := pausePollFor, pausePollEvery
	pausePollFor, pausePollEvery = 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { pausePollFor, pausePollEvery = prevFor, prevEvery })

	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reads++
		n := reads
		mu.Unlock()
		view := map[string]any{"id": "run-1", "project_id": "p1", "runner": "code", "state": "submitted", "skill_name": "Build", "auto_paused_reason": nil, "successor": nil,
			"flow": map[string]any{"version_id": "system:2", "node": "build", "hops": map[string]any{"done": map[string]any{"node": "review", "name": "Review", "runner": "findings", "auto": true, "location": "cloud"}}}}
		if n >= 2 {
			view["successor"] = map[string]any{"id": "run-2", "runner": "findings", "skill_name": "Review", "state": "running", "execution_location": "cloud", "session_id": "s2"}
		}
		reply(w, view)
	}))
	defer srv.Close()
	a := &Active{
		State:  &config.LaunchState{ActivityRunID: "run-1", Identifier: "STE-1", LaunchToken: "launch-token", TokenExpiry: time.Now().Add(time.Hour)},
		Client: api.New(srv.URL), Now: time.Now,
	}
	assert.Equal(t, "Review started in Steermi: "+srv.URL+"/projects/p1/chat/s2", nextStep(context.Background(), a))
	assert.Equal(t, 2, reads, "the poll stops once the successor exists")

	local := runView{DisplayName: "Review", Successor: &runSuccessor{ID: "run-3", Runner: activity.Code, SkillName: "Build", State: activity.Ready, ExecutionLocation: activity.Local, LaunchPublicID: "lx_7Q4XK2M9ab"},
		Run: activity.Run{Runner: activity.Findings, State: activity.Submitted, Flow: &activity.FlowRef{Node: "review",
			Hops: map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: {Node: "build", Name: "Build", Runner: activity.Code}}}}}
	assert.Equal(t, "Next: Build waits for you on this machine: run `steermi run lx_7Q4XK2M9ab`.", describeNext(local, "x"))
	local.Successor.LaunchPublicID = ""
	assert.Equal(t, "Next: Build waits for you on this machine: its `steermi run` command is on the work item (x).", describeNext(local, "x"),
		"the work item link only before the launch exists")
}

// A successor is "started" only while it is ready or running; a stopped
// loop is worded before any successor, and other successor states leave
// the wording to the pinned hop.
func TestDescribeNextWordsTheSuccessorByState(t *testing.T) {
	review := activity.FlowHop{Node: "review", Name: "Review", Runner: activity.Findings, Location: activity.Cloud}
	view := func(s *runSuccessor, flow *activity.FlowRef) runView {
		return runView{DisplayName: "Build", Successor: s, Run: activity.Run{Runner: activity.Code, State: activity.Submitted, Flow: flow}}
	}
	plain := &activity.FlowRef{VersionID: "v", Node: "build", Hops: map[activity.Outcome]activity.FlowHop{activity.OutcomeDone: review}}

	// An exhausted loop creates its successor waiting with repair_limit:
	// the stop decides, never "started". The review needs a blocking finding:
	// without one a loop review is approved (agent-contracts 0.4.0).
	exhausted := runView{DisplayName: "Review", Successor: &runSuccessor{ID: "r2", SkillName: "Build", State: activity.Waiting, ExecutionLocation: activity.Cloud, SessionID: "s2"},
		Run: activity.Run{Runner: activity.Findings, State: activity.Submitted, Recommendation: activity.ChangesRequested,
			Findings: []activity.Finding{{ID: "f1", Severity: activity.High, Description: "still broken"}}, Flow: &activity.FlowRef{VersionID: "v", Node: "review",
				Hops: map[activity.Outcome]activity.FlowHop{activity.OutcomeChangesRequested: {Node: "build", Name: "Build", Runner: activity.Code, Back: true, Location: activity.Cloud}},
				Loop: &activity.LoopState{Key: "quality", LoopPolicy: activity.LoopPolicy{MaxIterations: 2}, Iteration: 2, Exhausted: activity.FlowHop{Approval: true}}}}}
	_, stop, _ := exhausted.FlowNext()
	require.Equal(t, activity.BlockerRepairLimit, stop)
	assert.Equal(t, "Next: the quality loop stopped (repair limit); a person decides on the work item (x).", describeNext(exhausted, "x"))

	suggested := view(&runSuccessor{ID: "r2", SkillName: "Review", State: activity.Suggested, ExecutionLocation: activity.Cloud}, plain)
	assert.Equal(t, "Next: Review waits for a person to confirm it on the work item (x).", describeNext(suggested, "x"))

	cancelled := view(&runSuccessor{ID: "r2", SkillName: "Review", State: activity.Cancelled, ExecutionLocation: activity.Cloud, SessionID: "s2"}, plain)
	got := describeNext(cancelled, "x")
	assert.NotContains(t, got, "started")
	assert.Equal(t, "Next: Review — start it from the work item (x), in the cloud or on this machine.", got)

	cloudWaiting := view(&runSuccessor{ID: "r2", SkillName: "Review", State: activity.Waiting, ExecutionLocation: activity.Cloud}, plain)
	assert.NotContains(t, describeNext(cloudWaiting, "x"), "started")

	running := view(&runSuccessor{ID: "r2", SkillName: "Review", State: activity.Running, ExecutionLocation: activity.Cloud}, plain)
	assert.Equal(t, "Review started in Steermi: x", describeNext(running, "x"))

	// A cancelled successor is never linked as the next step's session.
	a := &Active{State: &config.LaunchState{Identifier: "STE-1"}, Client: api.New("https://steermi.test")}
	cancelled.ProjectID = "p1"
	assert.Equal(t, "https://steermi.test/work-items/key/STE-1", a.linkFor(cancelled))

	// Without a pinned hop, a successor that has not started says nothing.
	off := view(&runSuccessor{ID: "r2", SkillName: "Review", State: activity.Cancelled, ExecutionLocation: activity.Cloud}, nil)
	assert.Equal(t, nextStepHint, describeNext(off, "x"))
}

// Every pause reason in the contract has its own wording.
func TestPausedWordingCoversTheContract(t *testing.T) {
	reasons := contractConsts(t, "AdvancePausedReason")
	require.NotEmpty(t, reasons)
	for _, r := range reasons {
		_, ok := pausedReasons[activity.AdvancePausedReason(r)]
		assert.True(t, ok, "no wording for %q", r)
	}
	assert.Len(t, pausedReasons, len(reasons), "a worded reason the contract no longer has")
}

// contractConsts lists the string values of the agent-contracts activity
// constants of the named type, read from the module the build uses.
func contractConsts(t *testing.T, typ string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/steermi/agent-contracts").Output()
	require.NoError(t, err)
	dir := filepath.Join(strings.TrimSpace(string(out)), "activity")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	require.NoError(t, err)
	var vals []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				g, ok := d.(*ast.GenDecl)
				if !ok || g.Tok != token.CONST {
					continue
				}
				for _, spec := range g.Specs {
					vs := spec.(*ast.ValueSpec)
					if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != typ {
						continue
					}
					for _, v := range vs.Values {
						if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, err := strconv.Unquote(lit.Value)
							require.NoError(t, err)
							vals = append(vals, s)
						}
					}
				}
			}
		}
	}
	return vals
}

// --pause-after-step that the claim could not record says so.
func TestStepModeRefusedIsReported(t *testing.T) {
	o := RunOptions{PauseAfterStep: true}
	assert.Contains(t, o.stepModeRefused(""), "--pause-after-step was not applied")
	assert.Contains(t, o.stepModeRefused(activity.StepModeFlow), "--pause-after-step was not applied")
	assert.Empty(t, o.stepModeRefused(activity.StepModeAsk))
	assert.Empty(t, RunOptions{}.stepModeRefused(""), "no flag, nothing to report")
}
