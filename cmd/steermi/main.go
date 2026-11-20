// Command steermi runs Steermi tasks on your own machine with your own coding
// agent (docs/architecture.md).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/steermi/cli/internal/agent"
	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/buildinfo"
	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/deeplink"
	"github.com/steermi/cli/internal/gitx"
	"github.com/steermi/cli/internal/launch"
	"github.com/steermi/cli/internal/login"
	"github.com/steermi/cli/internal/mcpserver"
	"github.com/steermi/cli/internal/profile"
	"github.com/steermi/cli/internal/ui"
)

const usage = `steermi — work on Steermi tasks with your own coding agent

Usage:
  steermi                  status: active launch, next work, agents
  steermi login [--url URL] [--no-browser] [--name NAME]
  steermi logout
  steermi whoami
  steermi doctor
  steermi next [--all] [--team ID] [--pick N] [--json] [RUN FLAGS]
  steermi work KEY [--skill ID] [RUN FLAGS]
  steermi run LAUNCH_ID [RUN FLAGS]
  steermi done [--summary TEXT] [--commit] [--no-mr]
                           finish; says the next step (waits up to 5 s for an automatic one)
  steermi handoff [--summary TEXT]
  steermi profile list|add|use|rm|follow|path
  steermi repos link [PATH] | steermi repos list
  steermi setup            register the steermi:// link handler
  steermi version [--json]

Run flags:
  --profile NAME   agent profile (steermi profile list)
  --agent NAME     claude, codex, copilot or opencode (without a profile)
  --model ID       model for this run (overrides Steermi's suggestion)
  --effort LEVEL   low, medium or high (agents with an effort control)
  --yes            start without the launch card prompt
  --here           use the current checkout instead of a new worktree
  --no-agent       only prepare the checkout
  --background     run the agent headless and follow it in Steermi
  --pause-after-step
                   the flow waits for you after each step instead of
                   continuing by itself (Build → Review)

Run steermi COMMAND --help for a command's flags. STEERMI_URL selects the
Steermi installation, STEERMI_CONFIG_DIR the configuration directory,
NO_COLOR turns colour off.

Inside Claude Code (loaded automatically by steermi run): /steermi:next,
/steermi:work KEY, /steermi:done, /steermi:handoff. Other agents get the
same steps as instructions and MCP tools.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		// --help already printed the command's flags.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "steermi:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, _ := os.Getwd()
	if len(args) == 0 {
		return cmdHome(ctx, cwd, stdout)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "version", "--version":
		info := buildinfo.Get()
		if len(rest) > 0 && rest[0] == "--json" {
			b, _ := json.MarshalIndent(info, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		fmt.Fprintln(stdout, info)
		return nil
	case "login":
		return cmdLogin(ctx, rest, stdin, stdout)
	case "logout":
		if err := config.DeleteCredentials(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Logged out. Revoke the device in Steermi (Settings → Devices) to invalidate it everywhere.")
		return nil
	case "whoami":
		return cmdWhoami(ctx, stdout)
	case "doctor":
		return cmdDoctor(ctx, cwd, stdout)
	case "repos":
		return cmdRepos(rest, cwd, stdout)
	case "profile", "profiles":
		return cmdProfile(rest, stdin, stdout, stderr)
	case "next":
		return cmdNext(ctx, rest, cwd, stdin, stdout, stderr)
	case "work":
		return cmdWork(ctx, rest, cwd, stdin, stdout, stderr)
	case "run":
		return cmdRun(ctx, rest, cwd, stdin, stdout, stderr)
	case "done":
		fs := newFlags("done", stderr)
		summary := fs.String("summary", "", "summary for the merge request and work item")
		commit := fs.Bool("commit", false, "commit uncommitted changes first")
		noMR := fs.Bool("no-mr", false, "push without opening a merge request")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return launch.Done(ctx, launch.DoneOptions{Cwd: cwd, Summary: *summary, Commit: *commit, NoMR: *noMR, Out: stdout})
	case "handoff":
		fs := newFlags("handoff", stderr)
		summary := fs.String("summary", "", "where the work stands")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return launch.Handoff(ctx, launch.HandoffOptions{Cwd: cwd, Summary: *summary, Out: stdout})
	case "mcp":
		return cmdMCP(cwd)
	case "hook":
		event := ""
		if len(rest) > 0 {
			event = rest[0]
		}
		// Hooks must never break the agent session: report and exit 0.
		if err := launch.Hook(ctx, event, stdin, cwd); err != nil {
			fmt.Fprintln(stderr, "steermi hook:", err)
		}
		return nil
	case "setup":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		msg, err := deeplink.Register(exe)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, msg)
		return nil
	case "open-terminal":
		// Invoked by the registered link handler: validate, then open a
		// terminal running `steermi open <link>`.
		if len(rest) != 1 {
			return errors.New("usage: steermi open-terminal steermi://launch/LAUNCH_ID")
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return deeplink.OpenTerminal(exe, rest[0])
	case "open":
		// Invoked by the steermi:// handler with the raw URL.
		if len(rest) != 1 {
			return errors.New("usage: steermi open steermi://launch/LAUNCH_ID")
		}
		id, err := deeplink.Parse(rest[0])
		if err != nil {
			return err
		}
		return launch.Run(ctx, launch.RunOptions{PublicID: id, Cwd: cwd, In: stdin, Out: stdout, Err: stderr})
	}
	return fmt.Errorf("unknown command %q (see `steermi help`)", cmd)
}

func defaultURL() string {
	if u := os.Getenv("STEERMI_URL"); u != "" {
		return u
	}
	if c, err := config.LoadCredentials(); err == nil && c.URL != "" {
		return c.URL
	}
	return config.DefaultURL
}

func cmdLogin(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	host, _ := os.Hostname()
	fs := newFlags("login", stdout)
	url := fs.String("url", defaultURL(), "Steermi URL")
	noBrowser := fs.Bool("no-browser", false, "print a link and paste the code instead of opening a browser")
	name := fs.String("name", strings.TrimSuffix(host, ".local"), "device name shown in Steermi")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := login.Request{BaseURL: strings.TrimRight(*url, "/"), Name: *name, Hostname: host, OS: runtime.GOOS}
	var (
		res *login.Result
		err error
	)
	if *noBrowser {
		res, err = login.Paste(req, stdin, stdout)
	} else {
		res, err = login.Browser(ctx, req, stdout)
	}
	if err != nil {
		return err
	}
	client := api.New(req.BaseURL)
	tok, err := client.Exchange(ctx, res.Code, res.Verifier, agent.VersionsJSON())
	if err != nil {
		return fmt.Errorf("pairing failed: %w", err)
	}
	creds := &config.Credentials{
		URL: req.BaseURL, RefreshToken: tok.RefreshToken, AccessToken: tok.AccessToken,
		OrgID: tok.OrgID, UserID: tok.UserID,
	}
	if t, err := time.Parse(time.RFC3339, tok.ExpiresAt); err == nil {
		creds.AccessExpiry = t
	}
	if tok.Device != nil {
		creds.DeviceID, creds.DeviceName = tok.Device.ID, tok.Device.Name
	}
	if err := config.SaveCredentials(creds); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Paired %q with %s.\n", creds.DeviceName, req.BaseURL)
	suggestProfiles(stdout)
	fmt.Fprintln(stdout, "Next: `steermi next`, or link a checkout with `steermi repos link`.")
	return nil
}

func cmdWhoami(ctx context.Context, stdout io.Writer) error {
	client, err := api.Load()
	if err != nil {
		return err
	}
	dev, err := client.Me(ctx)
	if err != nil {
		return err
	}
	c := client.Credentials()
	fmt.Fprintf(stdout, "device  %s (%s)\nuser    %s\norg     %s\nserver  %s\n", dev.Name, dev.ID, c.UserID, c.OrgID, c.URL)
	return nil
}

// cmdDoctor checks what local launches need. Problems (✗) make it exit
// non-zero; warnings (!) are optional setup.
func cmdDoctor(ctx context.Context, cwd string, stdout io.Writer) error {
	problems := 0
	st := ui.For(stdout)
	report := func(mark, label, detail string) {
		fmt.Fprintf(stdout, " %s %s %s\n", mark, ui.Pad(label, 12), detail)
	}
	ok := func(good bool, label, detail string) {
		if !good {
			problems++
			report(st.Mark("bad"), label, detail)
			return
		}
		report(st.Mark("ok"), label, detail)
	}
	ok(true, "steermi", buildinfo.Get().String())
	if dir, err := config.Dir(); err != nil {
		ok(false, "config", err.Error())
	} else if info, err := os.Stat(filepath.Join(dir, "credentials.json")); err == nil && info.Mode().Perm()&0o077 != 0 {
		ok(false, "config", fmt.Sprintf("%s is readable by other users (%v) — run chmod 600 on it", filepath.Join(dir, "credentials.json"), info.Mode().Perm()))
	} else {
		ok(true, "config", dir)
	}
	installed := 0
	for _, name := range agent.Names() {
		a, _ := agent.Get(name)
		if _, err := agent.Binary(a); err != nil {
			report(st.Dim("·"), name, st.Dim("not installed — "+a.Install()))
			continue
		}
		installed++
		detail := agent.Title(a)
		if v := agent.Version(a); v != "" {
			detail += " " + v
		}
		if agent.TranscriptsOf(a) == nil {
			detail += st.Dim(" · transcript not mirrored (summary only)")
		}
		ok(true, name, detail)
	}
	if installed == 0 {
		ok(false, "agent", "no coding agent on PATH — install Claude Code, Codex CLI, Copilot CLI or opencode")
	}
	if profiles, err := profile.Load(); err != nil {
		ok(false, "profiles", err.Error())
	} else if len(profiles.Profiles) == 0 {
		report(st.Mark("warn"), "profiles", "none — launches use the first installed agent and its default model; `steermi profile add` to set models")
	} else {
		def := profiles.Default
		if def == "" {
			def = "first installed agent"
		}
		ok(true, "profiles", fmt.Sprintf("%d (default %s, suggestions: %s)", len(profiles.Profiles), def, profiles.Follow()))
	}
	if out, err := exec.Command("git", "--version").Output(); err == nil {
		ok(true, "git", strings.TrimSpace(string(out)))
	} else {
		ok(false, "git", "not found")
	}
	client, err := api.Load()
	if err != nil {
		ok(false, "login", err.Error())
	} else if dev, err := client.Me(ctx); err != nil {
		ok(false, "login", err.Error())
	} else {
		ok(true, "login", fmt.Sprintf("%s on %s", dev.Name, client.URL))
	}
	if top, err := gitx.Toplevel(cwd); err == nil {
		remote, _ := gitx.RemoteURL(top, "origin")
		links, _ := config.LoadRepoLinks()
		norm := gitx.NormalizeRemote(remote)
		if links[norm] != "" {
			ok(true, "repo link", fmt.Sprintf("%s → %s", norm, links[norm]))
		} else {
			report(st.Mark("warn"), "repo link", fmt.Sprintf("%s is not linked — run `steermi repos link` so launches find this checkout", orDash(norm)))
		}
	} else {
		ok(true, "repo link", "not in a git checkout (fine)")
	}
	ok(true, "plugin", map[bool]string{true: "installed in Claude Code", false: "Claude Code: loaded per session by steermi run"}[agent.PluginInstalled()])
	if problems > 0 {
		return fmt.Errorf("doctor found %d problem(s)", problems)
	}
	return nil
}

func cmdRepos(args []string, cwd string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		links, err := config.LoadRepoLinks()
		if err != nil {
			return err
		}
		if len(links) == 0 {
			fmt.Fprintln(stdout, "No linked checkouts. Run `steermi repos link` inside one.")
			return nil
		}
		keys := make([]string, 0, len(links))
		for k := range links {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "%s → %s\n", k, links[k])
		}
		return nil
	}
	if args[0] != "link" {
		return fmt.Errorf("unknown repos command %q", args[0])
	}
	dir := cwd
	if len(args) > 1 {
		dir = args[1]
	}
	top, err := gitx.Toplevel(dir)
	if err != nil {
		return fmt.Errorf("%s is not a git checkout", dir)
	}
	remote, err := gitx.RemoteURL(top, "origin")
	if err != nil {
		return errors.New("the checkout has no `origin` remote")
	}
	norm := gitx.NormalizeRemote(remote)
	if norm == "" {
		return fmt.Errorf("cannot parse remote %q", remote)
	}
	links, err := config.LoadRepoLinks()
	if err != nil {
		return err
	}
	links[norm] = top
	if err := config.SaveRepoLinks(links); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Linked %s → %s\n", norm, top)
	return nil
}

// newFlags returns a subcommand flag set that reports errors and --help to w.
func newFlags(name string, w io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(w)
	return fs
}

func runFlags(name string, w io.Writer) (*flag.FlagSet, func() launch.RunOptions) {
	fs := newFlags(name, w)
	here := fs.Bool("here", false, "use the current checkout instead of a new worktree")
	noAgent := fs.Bool("no-agent", false, "only prepare the checkout (branch + .steermi/TASK.md)")
	background := fs.Bool("background", false, "run the agent headless and follow it in Steermi")
	prof := fs.String("profile", "", "agent profile (steermi profile list)")
	agentName := fs.String("agent", "", "claude, codex, copilot or opencode")
	model := fs.String("model", "", "model for this run")
	effort := fs.String("effort", "", "low, medium or high")
	yes := fs.Bool("yes", false, "start without the launch card prompt")
	pause := fs.Bool("pause-after-step", false, "the flow waits for you after each step instead of continuing by itself")
	return fs, func() launch.RunOptions {
		return launch.RunOptions{
			Here: *here, NoAgent: *noAgent, Background: *background, PauseAfterStep: *pause,
			Agent: launch.AgentFlags{Profile: *prof, Agent: *agentName, Model: *model, Effort: *effort, Yes: *yes},
		}
	}
}

// parseArgs parses fs over args and returns the positional arguments, which
// may come before, between or after flags (`steermi run lx_… --here`).
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

func cmdRun(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs, opts := runFlags("run", stderr)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: steermi run LAUNCH_ID")
	}
	o := opts()
	o.PublicID, o.Cwd, o.In, o.Out, o.Err = pos[0], cwd, stdin, stdout, stderr
	return launch.Run(ctx, o)
}

func cmdWork(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs, opts := runFlags("work", stderr)
	skill := fs.String("skill", "", "skill to run: a built-in id such as steermi:review or a skill uuid (default steermi:build)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: steermi work KEY (for example STE-123)")
	}
	o := opts()
	o.Cwd, o.In, o.Out, o.Err = cwd, stdin, stdout, stderr
	return launch.Work(ctx, launch.WorkOptions{Key: pos[0], Skill: *skill, RunOptions: o})
}

func cmdNext(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs, opts := runFlags("next", stderr)
	all := fs.Bool("all", false, "include items outside the current cycle")
	team := fs.String("team", "", "only this team (id)")
	pick := fs.Int("pick", 0, "start item N without prompting")
	asJSON := fs.Bool("json", false, "print the list as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o := opts()
	o.Cwd, o.In, o.Out, o.Err = cwd, stdin, stdout, stderr
	return launch.Next(ctx, launch.NextOptions{TeamID: *team, All: *all, JSON: *asJSON, Pick: *pick, In: stdin, RunOptions: o})
}

func cmdMCP(cwd string) error {
	resolve := func() (*mcpserver.Launch, error) {
		dir := cwd
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
		a, err := launch.LoadActive(dir)
		if err != nil {
			return nil, err
		}
		return &mcpserver.Launch{
			Runner: a.State.Runner, Caller: a, WorkItemID: a.Marker.WorkItemID, ProjectID: a.Marker.ProjectID, SessionID: a.Marker.SessionID,
			Identifier: a.Marker.Identifier, Task: a.TaskMarkdown, Complete: mcpserver.SaveSummary(a.State), SaveResult: mcpserver.SaveActivityResult(a.State),
		}, nil
	}
	return server.ServeStdio(mcpserver.New(resolve, buildinfo.Version()))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
