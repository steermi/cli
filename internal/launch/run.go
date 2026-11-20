package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/steermi/agent-contracts/activity"

	"github.com/steermi/agent-contracts/locallaunch"
	"github.com/steermi/cli/internal/agent"
	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/gitx"
	"github.com/steermi/cli/internal/profile"
	"github.com/steermi/cli/internal/ui"
	"github.com/steermi/cli/plugin"
)

// RunOptions control `steermi run`.
type RunOptions struct {
	PublicID string
	// Here prepares the current checkout instead of a new worktree.
	Here bool
	// NoAgent only prepares the checkout (branch + TASK.md).
	NoAgent    bool
	Background bool
	// PauseAfterStep asks the flow to wait for a person after each step
	// (step mode ask) instead of continuing by itself.
	PauseAfterStep bool
	// Agent picks the agent, profile and model (see AgentFlags).
	Agent AgentFlags
	Cwd   string
	// In answers the launch card; prompts appear only when In and Out are
	// terminals.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type workItem struct {
	ID            string   `json:"id"`
	Identifier    string   `json:"identifier"`
	Title         string   `json:"title"`
	Type          string   `json:"type"`
	ProjectID     string   `json:"project_id"`
	RepositoryIDs []string `json:"repository_ids"`
}

// Run claims a launch, prepares the checkout and starts the agent.
func Run(ctx context.Context, o RunOptions) error {
	if !locallaunch.IsPublicID(o.PublicID) {
		return fmt.Errorf("%q is not a launch id (expected lx_ followed by 10 letters or digits)", o.PublicID)
	}
	client, err := api.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "Claiming %s…\n", o.PublicID)
	bundle, err := client.Claim(ctx, o.PublicID, o.stepMode())
	if err != nil {
		return friendlyClaimError(err)
	}
	var wi workItem
	_ = json.Unmarshal([]byte(bundle.WorkItemJSON), &wi)
	if wi.Identifier == "" {
		wi.Identifier = o.PublicID
	}
	if wi.ProjectID == "" {
		wi.ProjectID = bundle.Launch.ProjectID
	}

	repos, err := client.Repositories(ctx, wi.ProjectID, api.TokenAuth(bundle.LaunchToken))
	if err != nil {
		fmt.Fprintf(o.Err, "warning: could not list the project's repositories: %v\n", err)
	}
	repo, repoDir, err := resolveRepo(o.Cwd, repos, wi.RepositoryIDs)
	if err != nil {
		if bundle.Launch.Runner.ChangesCode() {
			return err
		}
		// Planning and document reviews need no repository. Keep task files in the
		// developer's chosen directory without changing git configuration.
		repoDir = o.Cwd
		if repoDir == "" {
			repoDir, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		repo = api.Repository{}
	}

	if bundle.Launch.ActivityRunID == "" || bundle.Launch.InputDigest == "" || bundle.Launch.ActivityGeneration < 1 {
		return errors.New("launch has no canonical activity binding")
	}
	var bound runView
	if err := client.Do(ctx, http.MethodGet, "/ai/activities/"+bundle.Launch.ActivityRunID, nil, &bound, api.TokenAuth(bundle.LaunchToken)); err != nil {
		return err
	}
	if bound.ID != bundle.Launch.ActivityRunID || bound.InputDigest != bundle.Launch.InputDigest || bound.Generation != bundle.Launch.ActivityGeneration {
		return errors.New("activity binding changed; resume from Steermi")
	}
	// What the flow does after this step, before anything is prepared or
	// the agent starts (the device cannot read the run before the claim,
	// which records --pause-after-step on it).
	if line := consentLine(bundle.Loop, bound.StepMode); line != "" {
		fmt.Fprintln(o.Out, line)
	}
	if line := o.stepModeRefused(bound.StepMode); line != "" {
		fmt.Fprintln(o.Out, line)
	}
	pin := ""
	for _, ref := range bound.Inputs {
		if ref.Kind == "repository" && ref.RepositoryID == repo.ID {
			if pin != "" && pin != ref.CommitSHA {
				return errors.New("ambiguous repository input revisions")
			}
			pin = ref.CommitSHA
		}
	}
	if bundle.Launch.Runner.ChangesCode() && (repo.ID == "" || pin == "") {
		return errors.New("a code run needs a pinned repository revision")
	}
	branch := bundle.Launch.Branch
	base := bundle.Launch.BaseBranch
	if base == "" {
		base = gitx.DefaultBranch(repoDir)
	}
	dir := repoDir
	switch {
	case o.Here && branch != "":
		if err := gitx.CheckoutPinnedBranch(repoDir, branch, pin); err != nil {
			return fmt.Errorf("switch to %s: %w", branch, err)
		}
	case !o.Here && branch != "":
		dir, err = worktreePath(repoDir, repo, wi.Identifier)
		if err != nil {
			return err
		}
		if existing, e := gitx.CurrentBranch(dir); e == nil && existing != branch {
			dir += "-" + safeName(bundle.Launch.ActivityRunID)[:8]
		}
		fmt.Fprintf(o.Out, "Preparing worktree %s on %s…\n", dir, branch)
		if err := gitx.EnsureWorktree(repoDir, dir, branch, pin); err != nil {
			return err
		}
	}
	if repo.ID != "" {
		if err := gitx.ExcludeSteermi(dir); err != nil {
			return err
		}

	}
	state := &config.LaunchState{
		ActivityRunID: bundle.Launch.ActivityRunID, InputDigest: bundle.Launch.InputDigest, ActivityGeneration: bundle.Launch.ActivityGeneration,
		LaunchID: bundle.Launch.ID, PublicID: bundle.Launch.PublicID, SessionID: bundle.Launch.SessionID,
		WorkItemID: bundle.Launch.WorkItemID, Identifier: wi.Identifier, Runner: bundle.Launch.Runner, SkillName: bundle.SkillName,
		Branch: branch, BaseBranch: base, Worktree: dir, RepositoryID: repo.ID,
		RepoKey: gitx.NormalizeRemote(repo.CloneURL), LaunchToken: bundle.LaunchToken,
		ClaudeSessionID: bundle.ClaudeSessionID,
	}
	if t, err := time.Parse(time.RFC3339, bundle.TokenExpiresAt); err == nil {
		state.TokenExpiry = t
	}
	saved, err := config.UpdateLaunch(state.LaunchID, func(s *config.LaunchState) {
		// Re-running: keep the mirror position and the agent run so nothing
		// is mirrored twice and the agent continues where it was.
		offset, run := s.MirrorOffset, s.AgentRun
		*s = *state
		s.MirrorOffset, s.AgentRun = offset, run
	})
	if err != nil {
		return err
	}
	state = saved
	if err := writeCheckoutFiles(dir, bundle, wi); err != nil {
		return err
	}

	if o.NoAgent {
		fmt.Fprintf(o.Out, "\n%s · %s\n  checkout  %s\n  branch    %s (from %s)\n\n", wi.Identifier, wi.Title, dir, orDash(branch), base)
		fmt.Fprintf(o.Out, "Checkout ready. Brief: %s\nWhen finished: steermi done\n", filepath.Join(dir, MarkerDir, "TASK.md"))
		return nil
	}

	profiles, err := profile.Load()
	if err != nil {
		return err
	}
	sel, err := resolveAgent(profiles, o.Agent, bundle.Launch.Runner, dir, bundle.ModelHint)
	if err != nil {
		return err
	}
	mode := agent.Interactive
	if o.Background || bundle.Launch.Mode == "background" {
		mode = agent.Background
	}
	allowed := loadAllowedTools(dir)
	redraw := func(s selection) { launchCard(o.Out, state, wi.Title, dir, branch, base, s, len(allowed)) }
	redraw(sel)
	if mode == agent.Interactive && !o.Agent.Yes && profiles.Follow() != profile.FollowAuto && ui.IsTerminal(o.In) && ui.IsTerminal(o.Out) {
		sel, err = confirmCard(o.In, o.Out, profiles, o.Agent, bundle.Launch.Runner, dir, bundle.ModelHint, sel, redraw)
		if errors.Is(err, ui.ErrCancelled) {
			fmt.Fprintf(o.Out, "\nCheckout ready, agent not started. Start it later with `steermi run %s`.\n", state.PublicID)
			return nil
		}
		if err != nil {
			return err
		}
	}
	bin, err := agent.Binary(sel.adapter)
	if err != nil {
		return err
	}
	run, resume := nextAgentRun(state, sel, timeNow())
	l := agent.Launch{
		Mode: mode, Identifier: wi.Identifier, Title: wi.Title, SystemPrompt: bundle.SystemPrompt,
		SessionID: run.SessionID, Resume: resume, Runner: bundle.Launch.Runner, AllowedTools: allowed,
		Model: sel.Model, Effort: sel.Effort, Dir: dir, SteermiBin: steermiBin(),
	}
	if sel.adapter.Caps().Plugin {
		if l.PluginDir, err = pluginDirFor(); err != nil {
			return err
		}
	}
	cmd, err := sel.adapter.Command(l)
	if err != nil {
		return err
	}
	state, err = config.UpdateLaunch(state.LaunchID, func(s *config.LaunchState) {
		if !resume {
			s.MirrorOffset = 0 // a new transcript
		}
		s.AgentRun = run
	})
	if err != nil {
		return err
	}
	return runAgent(ctx, client, state, sel.adapter, bin, cmd, dir, mode, o)
}

// stepMode is the step mode the claim asks for: ask with
// --pause-after-step, else the run's own.
func (o RunOptions) stepMode() activity.StepMode {
	if o.PauseAfterStep {
		return activity.StepModeAsk
	}
	return ""
}

// stepModeRefused words a --pause-after-step the claim could not record:
// Steermi applies it only while the run's flow can still continue (its
// guard decides), so the run read after the claim may keep its own mode.
func (o RunOptions) stepModeRefused(got activity.StepMode) string {
	want := o.stepMode()
	if want == "" || got.OrDefault() == want.OrDefault() {
		return ""
	}
	return "Note: --pause-after-step was not applied (Steermi records it only on a run whose flow can still continue); this run keeps its step mode."
}

func friendlyClaimError(err error) error {
	switch {
	case api.IsCode(err, "LOCAL_LAUNCH_NOT_FOUND"):
		return errors.New("launch not found — it may belong to someone else, or the id is mistyped")
	case api.IsCode(err, "LOCAL_LAUNCH_EXPIRED"):
		return errors.New("launch expired — start a new one from Steermi (Work locally) or with `steermi work <KEY>`")
	case api.IsCode(err, "LOCAL_LAUNCH_ALREADY_CLAIMED"):
		return errors.New("this launch is already running on another device")
	case api.IsCode(err, "LOCAL_LAUNCH_DISABLED"):
		return errors.New("local launch is not enabled on this Steermi installation")
	}
	return err
}

// resolveRepo picks the local checkout: the current directory when its remote
// is one of the project's repositories, otherwise a linked checkout.
func resolveRepo(cwd string, repos []api.Repository, preferred []string) (api.Repository, string, error) {
	if len(preferred) > 0 {
		order := map[string]int{}
		for i, id := range preferred {
			order[id] = i + 1
		}
		sortRepos(repos, order)
	}
	links, _ := config.LoadRepoLinks()
	if top, err := gitx.Toplevel(cwd); err == nil {
		if remote, err := gitx.RemoteURL(top, "origin"); err == nil {
			norm := gitx.NormalizeRemote(remote)
			for _, r := range repos {
				if gitx.NormalizeRemote(r.CloneURL) == norm {
					return r, top, nil
				}
			}
			if len(repos) == 0 {
				return api.Repository{CloneURL: remote}, top, nil
			}
		}
	}
	for _, r := range repos {
		if dir := links[gitx.NormalizeRemote(r.CloneURL)]; dir != "" {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return r, dir, nil
			}
		}
	}
	if len(repos) == 0 {
		return api.Repository{}, "", errors.New("this project has no connected repository and the current directory is not a git checkout — cd into your checkout and run again")
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, gitx.NormalizeRemote(r.CloneURL))
	}
	return api.Repository{}, "", fmt.Errorf("no local checkout linked for %s — run `steermi repos link` inside your checkout, or cd into it and run again", strings.Join(names, ", "))
}

func sortRepos(repos []api.Repository, order map[string]int) {
	rank := func(r api.Repository) int {
		if o := order[r.ID]; o > 0 {
			return o
		}
		return len(order) + 1
	}
	for i := 1; i < len(repos); i++ {
		for j := i; j > 0 && rank(repos[j]) < rank(repos[j-1]); j-- {
			repos[j], repos[j-1] = repos[j-1], repos[j]
		}
	}
}

// WorktreeRoot is ~/.steermi/worktrees; tests override it.
var WorktreeRoot = func() (string, error) {
	if d := os.Getenv("STEERMI_WORKTREES"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".steermi", "worktrees"), nil
}

func worktreePath(repoDir string, repo api.Repository, identifier string) (string, error) {
	root, err := WorktreeRoot()
	if err != nil {
		return "", err
	}
	slug := filepath.Base(repoDir)
	if norm := gitx.NormalizeRemote(repo.CloneURL); norm != "" {
		slug = filepath.Base(norm)
	}
	return filepath.Join(root, safeName(slug), safeName(identifier)), nil
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "task"
	}
	return out
}

func writeCheckoutFiles(dir string, b *api.Bundle, wi workItem) error {
	md := filepath.Join(dir, MarkerDir)
	if err := os.MkdirAll(md, 0o755); err != nil {
		return err
	}
	marker, _ := json.MarshalIndent(Marker{
		LaunchID: b.Launch.ID, PublicID: b.Launch.PublicID, SessionID: b.Launch.SessionID,
		WorkItemID: b.Launch.WorkItemID, Identifier: wi.Identifier, ProjectID: wi.ProjectID,
	}, "", "  ")
	for name, content := range map[string][]byte{
		"launch.json": marker,
		"TASK.md":     []byte(b.TaskMarkdown),
		"SYSTEM.md":   []byte(b.SystemPrompt),
	} {
		if err := os.WriteFile(filepath.Join(md, name), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func pluginDirFor() (string, error) {
	if agent.PluginInstalled() {
		return "", nil
	}
	cfg, err := config.Dir()
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "steermi"
	}
	return plugin.Write(filepath.Join(cfg, "plugin"), exe)
}

func runAgent(ctx context.Context, client *api.Client, state *config.LaunchState, a agent.Agent, bin string, ac agent.Command, dir string, mode agent.Mode, o RunOptions) error {
	cmd := exec.CommandContext(ctx, bin, ac.Args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "STEERMI_LAUNCH_ID="+state.LaunchID), ac.Env...)
	if mode == agent.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		// The agent owns Ctrl+C while it runs; the CLI just waits.
		signal.Ignore(syscall.SIGINT)
		defer signal.Reset(syscall.SIGINT)
	} else {
		logFile, err := os.OpenFile(filepath.Join(dir, MarkerDir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = logFile.Close() }()
		cmd.Stdout, cmd.Stderr = logFile, logFile
		fmt.Fprintf(o.Out, "\nRunning %s in the background (log: %s). Follow along in Steermi.\n", agent.Title(a), logFile.Name())
	}
	if mode == agent.Interactive {
		fmt.Fprintf(o.Out, "\nStarting %s…\n\n", agent.Title(a))
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", a.Bin(), err)
	}
	stop := make(chan struct{})
	go heartbeat(ctx, client, state, cmd, mode, !a.Caps().Plugin, stop)
	err := cmd.Wait()
	close(stop)

	ensureSession(state)
	active := activeFromDisk(state, client)
	active.Root = dir
	if ferr := Flush(context.Background(), active, ""); ferr != nil {
		fmt.Fprintf(o.Err, "warning: final transcript sync failed: %v\n", ferr)
	}
	fmt.Fprintf(o.Out, "\n%s exited. Next: `steermi done` to finish, `steermi handoff` to continue in Steermi, or `steermi run %s` to resume.\n", agent.Title(a), state.PublicID)
	if err != nil && mode == agent.Background {
		return fmt.Errorf("%s: %w", a.Bin(), err)
	}
	return nil
}

// heartbeat keeps the launch token (and the session lock) fresh while the
// agent runs, mirrors background runs and agents without hooks, and stops a background run the
// developer cancelled in Steermi.
func heartbeat(ctx context.Context, client *api.Client, state *config.LaunchState, cmd *exec.Cmd, mode agent.Mode, mirrorHere bool, stop <-chan struct{}) {
	refresh := time.NewTicker(5 * time.Minute)
	defer refresh.Stop()
	poll := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-stop:
			return
		case <-refresh.C:
			if token, exp, err := client.RefreshLaunchToken(ctx, state.LaunchID); err == nil {
				_, _ = config.UpdateLaunch(state.LaunchID, func(s *config.LaunchState) {
					s.LaunchToken = token
					if t, perr := time.Parse(time.RFC3339, exp); perr == nil {
						s.TokenExpiry = t
					}
				})
			} else if api.IsCode(err, "LOCAL_LAUNCH_NOT_RUNNING") && mode == agent.Background && cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		case <-poll.C:
			// Agents without hooks are mirrored from here while they run.
			if mode == agent.Background || mirrorHere {
				ensureSession(state)
				_ = Flush(ctx, activeFromDisk(state, client), "")
			}
			if mode != agent.Background {
				continue
			}
			if l, err := client.GetLaunch(ctx, state.LaunchID); err == nil && l.State == "cancelled" && cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		}
	}
}

// activeFromDisk builds an Active from the stored launch state, which other
// processes (hooks) and the heartbeat update, falling back to fallback.
func activeFromDisk(fallback *config.LaunchState, client *api.Client) *Active {
	state := fallback
	if cur, err := config.LoadLaunch(fallback.LaunchID); err == nil {
		state = cur
	}
	return &Active{State: state, Client: client, Now: time.Now}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
