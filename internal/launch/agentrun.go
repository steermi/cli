package launch

import (
	"errors"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/steermi/agent-contracts/hint"
	"github.com/steermi/agent-contracts/locallaunch"

	"github.com/steermi/cli/internal/agent"
	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/profile"
	"github.com/steermi/cli/internal/ui"
)

// AgentFlags are the per-launch agent overrides (`--profile`, `--agent`,
// `--model`, `--effort`, `--yes`).
type AgentFlags struct {
	Profile, Agent, Model, Effort string
	// Yes starts without the launch card prompt.
	Yes bool
}

// selection is the resolved agent for one launch.
type selection struct {
	profile.Choice
	adapter agent.Agent
	version string
}

// agentVersions caches `<agent> --version` for the process: the launch
// card re-resolves on every change.
var agentVersions = map[string]string{}

func agentVersion(a agent.Agent) string {
	v, ok := agentVersions[a.Name()]
	if !ok {
		v = agent.Version(a)
		agentVersions[a.Name()] = v
	}
	return v
}

func installedNames() []string {
	var out []string
	for _, a := range agent.Installed() {
		out = append(out, a.Name())
	}
	return out
}

// resolveAgent applies profiles.json, runner.yaml and the flags.
func resolveAgent(profiles *profile.File, f AgentFlags, runner activity.Runner, repoDir string, h *hint.Hint) (selection, error) {
	if f.Effort != "" && hint.ParseEffort(f.Effort) == "" {
		return selection{}, errors.New("--effort must be low, medium or high")
	}
	if f.Agent != "" {
		if _, ok := agent.Get(f.Agent); !ok {
			return selection{}, fmt.Errorf("unknown agent %q (choose %s)", f.Agent, strings.Join(agent.Names(), ", "))
		}
	}
	c, err := profiles.Resolve(profile.Request{
		Profile: f.Profile, Agent: f.Agent, Model: f.Model, Effort: strings.ToLower(f.Effort),
		RepoProfile: loadRunner(repoDir).Profile, Runner: runner, Hint: h, Installed: installedNames(),
		TakesEffort: func(name string) bool {
			a, ok := agent.Get(name)
			return ok && a.Caps().Effort
		},
	})
	if err != nil {
		return selection{}, err
	}
	a, ok := agent.Get(c.Agent)
	if !ok {
		return selection{}, fmt.Errorf("profile %q uses unknown agent %q", c.Profile, c.Agent)
	}
	return selection{Choice: c, adapter: a, version: agentVersion(a)}, nil
}

// launchCard prints what will run, where, with what and why.
func launchCard(w io.Writer, st *config.LaunchState, title, dir, branch, base string, sel selection, allowed int) {
	s := ui.For(w)
	fmt.Fprintf(w, "\n %s %s  %s   %s\n", s.Accent("▸"), s.Bold(st.Identifier), ui.Clip(title, 56), s.Dim(st.SkillName))
	s.Row(w, "checkout", 9, dir)
	if branch != "" {
		s.Row(w, "branch", 9, fmt.Sprintf("%s %s", branch, s.Dim("(from "+orDash(base)+")")))
	}
	agentLine := agent.Title(sel.adapter)
	if sel.version != "" {
		agentLine += " " + sel.version
	}
	if sel.Profile != "" {
		agentLine += s.Dim(" · profile " + sel.Profile)
	}
	agentLine += s.Dim(" · " + sel.Source)
	s.Row(w, "agent", 9, agentLine)
	model := sel.Model
	if model == "" {
		model = "agent default"
	}
	if sel.Effort != "" {
		model += s.Dim(" · effort ") + sel.Effort
	}
	s.Row(w, "model", 9, model)
	if h := sel.Hint; h != nil {
		note := fmt.Sprintf("Steermi suggests %s/%s: %s", h.Tier, h.Effort, h.Reason)
		if !sel.Suggested {
			note += " (not applied)"
		} else if sel.Model == "" {
			note += " — add models to a profile to follow it (`steermi profile add`)"
		}
		s.Row(w, "", 9, s.Dim("↳ "+note))
	}
	access := "edits allowed"
	if !st.Runner.ChangesCode() {
		access = "read-only"
	}
	if allowed > 0 {
		access += s.Dim(fmt.Sprintf(" · runner.yaml allows %d command(s) in background runs", allowed))
	}
	s.Row(w, "access", 9, access)
	mirror := "live in Steermi"
	if agent.TranscriptsOf(sel.adapter) == nil {
		mirror = s.Warn("summary only") + s.Dim(" — "+agent.Title(sel.adapter)+" transcripts are not mirrored yet")
	}
	s.Row(w, "session", 9, mirror)
}

// confirmCard lets the developer change the model or profile before the
// agent starts. It returns the final selection or ui.ErrCancelled.
func confirmCard(in io.Reader, out io.Writer, profiles *profile.File, f AgentFlags, runner activity.Runner, repoDir string, h *hint.Hint, sel selection, redraw func(selection)) (selection, error) {
	s := ui.For(out)
	p := ui.NewPrompter(in, out)
	for {
		ans, err := p.Ask(fmt.Sprintf("\n   %s start   %s model   %s profile   %s cancel  ", s.Bold("[enter]"), s.Bold("m"), s.Bold("p"), s.Bold("q")), "")
		if err != nil && !errors.Is(err, io.EOF) {
			return sel, err
		}
		switch strings.ToLower(ans) {
		case "":
			return sel, nil
		case "q", "quit", "n":
			return sel, ui.ErrCancelled
		case "m":
			model, err := pickModel(p, out, profiles.Profiles[sel.Profile], sel.adapter)
			if err != nil {
				return sel, err
			}
			if model != "" {
				f.Model = model
			}
		case "p":
			name, err := pickProfile(p, out, profiles)
			if err != nil {
				return sel, err
			}
			if name == "" {
				continue
			}
			f.Profile, f.Agent, f.Model = "", "", ""
			if _, ok := profiles.Profiles[name]; ok {
				f.Profile = name
			} else {
				f.Agent = name
			}
		default:
			fmt.Fprintln(out, s.Dim("   press enter to start, m, p or q"))
			continue
		}
		next, err := resolveAgent(profiles, f, runner, repoDir, h)
		if err != nil {
			fmt.Fprintln(out, "   "+s.Bad(err.Error()))
			continue
		}
		sel = next
		redraw(sel)
	}
}

func pickModel(p *ui.Prompter, out io.Writer, prof profile.Profile, a agent.Agent) (string, error) {
	var opts []string
	seen := map[string]bool{}
	for _, t := range []hint.Tier{hint.Fast, hint.Standard, hint.Deep} {
		if m := prof.Models[t]; m != "" && !seen[m] {
			opts, seen[m] = append(opts, m), true
		}
	}
	var known []string
	if l, ok := a.(agent.ModelLister); ok {
		known = l.Models()
	}
	for _, m := range known {
		if !seen[m] {
			opts, seen[m] = append(opts, m), true
		}
	}
	for i, m := range opts {
		fmt.Fprintf(out, "     %d  %s\n", i+1, m)
	}
	ans, err := p.Ask("   model (number or id, enter = keep): ", "")
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n, e := strconv.Atoi(ans); e == nil && n >= 1 && n <= len(opts) {
		return opts[n-1], nil
	}
	return ans, nil
}

func pickProfile(p *ui.Prompter, out io.Writer, profiles *profile.File) (string, error) {
	opts := profiles.Names()
	for _, n := range installedNames() {
		if _, ok := profiles.Profiles[n]; !ok {
			opts = append(opts, n)
		}
	}
	for i, n := range opts {
		label := n
		if pr, ok := profiles.Profiles[n]; ok {
			label += "  (" + pr.Agent + ")"
		} else {
			label += "  (agent, no profile)"
		}
		fmt.Fprintf(out, "     %d  %s\n", i+1, label)
	}
	ans, err := p.Ask("   profile (number or name, enter = keep): ", "")
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n, e := strconv.Atoi(ans); e == nil && n >= 1 && n <= len(opts) {
		return opts[n-1], nil
	}
	return ans, nil
}

// adapterFor returns the launch's agent (Claude Code when none ran yet).
func adapterFor(st *config.LaunchState) agent.Agent {
	if a, ok := agent.Get(st.AgentRun.Agent); ok {
		return a
	}
	a, _ := agent.Get("claude")
	return a
}

// nextAgentRun is the agent run for starting sel on the launch: the same
// agent resumes its session when the transcript still exists; anything else
// starts fresh, with the launch's preset session for agents that take one
// and a start time to find the session by for the others.
func nextAgentRun(st *config.LaunchState, sel selection, now time.Time) (config.AgentRun, bool) {
	run := st.AgentRun
	if run.Agent != sel.Agent {
		run = config.AgentRun{}
	}
	t := agent.TranscriptsOf(sel.adapter)
	resume := t != nil && run.SessionID != "" && t.Transcript(run.SessionID) != ""
	if !resume {
		run = config.AgentRun{StartedAt: now}
		if _, finds := sel.adapter.(agent.SessionFinder); !finds {
			run.SessionID = st.ClaudeSessionID
		}
	}
	run.Agent, run.Version, run.Model, run.Effort = sel.Agent, sel.version, sel.Model, sel.Effort
	return run, resume
}

// ensureSession finds and stores the session of an agent that picks its own
// id (Codex, Copilot) once it has started; a no-op otherwise.
func ensureSession(st *config.LaunchState) {
	f, ok := adapterFor(st).(agent.SessionFinder)
	if !ok || st.AgentRun.SessionID != "" || st.AgentRun.StartedAt.IsZero() {
		return
	}
	id := f.FindSession(st.Worktree, st.AgentRun.StartedAt)
	if id == "" {
		return
	}
	st.AgentRun.SessionID = id
	_, _ = config.UpdateLaunch(st.LaunchID, func(s *config.LaunchState) {
		if s.AgentRun.SessionID == "" {
			s.AgentRun.SessionID = id
		}
	})
}

// transcriptFor returns the launch's transcript file and the agent's
// transcript support (nil when the agent cannot be mirrored).
func transcriptFor(st *config.LaunchState) (string, agent.Transcripts) {
	t := agent.TranscriptsOf(adapterFor(st))
	if t == nil {
		return "", nil
	}
	return t.Transcript(st.AgentRun.SessionID), t
}

// runnerReport is the self-reported agent sent with finish.
func runnerReport(st *config.LaunchState) locallaunch.Runner {
	return locallaunch.Runner{
		Agent: adapterFor(st).ID(), AgentVersion: st.AgentRun.Version,
		Model: st.AgentRun.Model, Effort: hint.Effort(st.AgentRun.Effort),
	}
}

// steermiBin is the running executable, so the agent's MCP server is this
// CLI and never another steermi on PATH.
func steermiBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "steermi"
	}
	return exe
}

var timeNow = time.Now
