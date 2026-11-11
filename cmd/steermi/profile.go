package main

import (
	"errors"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"io"
	"strconv"
	"strings"

	"github.com/steermi/agent-contracts/hint"

	"github.com/steermi/cli/internal/agent"
	"github.com/steermi/cli/internal/profile"
	"github.com/steermi/cli/internal/ui"
)

const profileUsage = `Usage:
  steermi profile list
  steermi profile add [NAME] [--agent A] [--model M] [--fast M] [--standard M] [--deep M] [--default]
  steermi profile use NAME [--runner answer|code|findings]
  steermi profile rm NAME
  steermi profile follow ask|auto|never
  steermi profile path

A profile is an agent plus a model per tier. Steermi suggests a tier
(fast, standard, deep) and an effort for each run; the profile names the
model. follow: ask shows the suggestion in the launch card (default), auto
applies it silently, never always runs the profile's default model.
Without arguments, add asks for each value.
`

func cmdProfile(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	f, err := profile.Load()
	if err != nil {
		return err
	}
	known := func(n string) bool { _, ok := agent.Get(n); return ok }
	switch sub {
	case "list", "ls":
		return profileList(f, stdout)
	case "path":
		p, err := profile.Path()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, p)
		return nil
	case "add", "set":
		return profileAdd(f, args, stdin, stdout, stderr, known)
	case "use", "default":
		fs := newFlags("profile use", stderr)
		runner := fs.String("runner", "", "use NAME only for skills that hand back this (answer, code, findings)")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errors.New("usage: steermi profile use NAME [--runner answer|code|findings]")
		}
		if _, ok := f.Profiles[pos[0]]; !ok {
			return fmt.Errorf("profile %q does not exist", pos[0])
		}
		if *runner != "" {
			if _, ok := activity.Lookup(activity.Runner(*runner)); !ok {
				return errors.New("--runner must be answer, code or findings")
			}
			if f.Runners == nil {
				f.Runners = map[activity.Runner]string{}
			}
			f.Runners[activity.Runner(*runner)] = pos[0]
		} else {
			f.Default = pos[0]
		}
		if err := f.Save(); err != nil {
			return err
		}
		if *runner != "" {
			fmt.Fprintf(stdout, "Skills that hand back %s run with %s.\n", *runner, pos[0])
		} else {
			fmt.Fprintf(stdout, "Default profile: %s.\n", pos[0])
		}
		return nil
	case "rm", "remove", "delete":
		if len(args) != 1 {
			return errors.New("usage: steermi profile rm NAME")
		}
		if _, ok := f.Profiles[args[0]]; !ok {
			return fmt.Errorf("profile %q does not exist", args[0])
		}
		delete(f.Profiles, args[0])
		if f.Default == args[0] {
			f.Default = ""
		}
		for k, v := range f.Runners {
			if v == args[0] {
				delete(f.Runners, k)
			}
		}
		if err := f.Save(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed %s.\n", args[0])
		return nil
	case "follow":
		if len(args) != 1 {
			return errors.New("usage: steermi profile follow ask|auto|never")
		}
		f.FollowHints = args[0]
		if err := f.Save(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Model suggestions: %s.\n", f.Follow())
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, profileUsage)
		return nil
	}
	return fmt.Errorf("unknown profile command %q\n\n%s", sub, profileUsage)
}

func profileList(f *profile.File, w io.Writer) error {
	s := ui.For(w)
	if len(f.Profiles) == 0 {
		fmt.Fprintln(w, "No profiles yet. Launches use the first installed agent with its default model.")
		fmt.Fprintf(w, "Create one with %s.\n", s.Accent("steermi profile add"))
		return nil
	}
	runnersOf := map[string][]string{}
	for r, n := range f.Runners {
		runnersOf[n] = append(runnersOf[n], string(r))
	}
	fmt.Fprintf(w, " %s %s %s %s %s\n", "  ", s.Dim(ui.Pad("NAME", 12)), s.Dim(ui.Pad("AGENT", 9)), s.Dim(ui.Pad("FAST / STANDARD / DEEP", 40)), s.Dim("USED FOR"))
	for _, name := range f.Names() {
		p := f.Profiles[name]
		mark := "  "
		if name == f.Default {
			mark = s.Accent("● ")
		}
		ladder := []string{}
		for _, t := range []hint.Tier{hint.Fast, hint.Standard, hint.Deep} {
			m := p.Models[t]
			if m == "" {
				m = "–"
			}
			ladder = append(ladder, m)
		}
		models := strings.Join(ladder, " / ")
		if p.Model != "" {
			models += s.Dim(" (default " + p.Model + ")")
		}
		used := strings.Join(runnersOf[name], ", ")
		if name == f.Default {
			used = strings.TrimPrefix(used+", default", ", ")
		}
		fmt.Fprintf(w, " %s %s %s %s %s\n", mark, s.Bold(ui.Pad(name, 12)), ui.Pad(p.Agent, 9), ui.Pad(models, 40), s.Dim(used))
	}
	fmt.Fprintf(w, "\n Model suggestions: %s %s\n", f.Follow(), s.Dim("(steermi profile follow ask|auto|never)"))
	return nil
}

func profileAdd(f *profile.File, args []string, stdin io.Reader, stdout, stderr io.Writer, known func(string) bool) error {
	fs := newFlags("profile add", stderr)
	agentName := fs.String("agent", "", "claude, codex, copilot or opencode")
	model := fs.String("model", "", "default model")
	fast := fs.String("fast", "", "model for the fast tier")
	standard := fs.String("standard", "", "model for the standard tier")
	deep := fs.String("deep", "", "model for the deep tier")
	makeDefault := fs.Bool("default", false, "make it the default profile")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	interactive := *agentName == "" && ui.IsTerminal(stdin)
	s := ui.For(stdout)
	if interactive {
		p := ui.NewPrompter(stdin, stdout)
		installed := agent.Installed()
		names := agent.Names()
		fmt.Fprintln(stdout, s.Bold("New agent profile"))
		for i, n := range names {
			a, _ := agent.Get(n)
			state := s.Dim("not installed")
			for _, in := range installed {
				if in.Name() == n {
					state = s.Good("installed")
				}
			}
			fmt.Fprintf(stdout, "  %d  %s  %s\n", i+1, ui.Pad(agent.Title(a), 20), state)
		}
		def := "1"
		if len(installed) > 0 {
			for i, n := range names {
				if n == installed[0].Name() {
					def = strconv.Itoa(i + 1)
				}
			}
		}
		ans, _ := p.Ask(fmt.Sprintf("Agent [%s]: ", def), def)
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(names) {
			*agentName = names[n-1]
		} else {
			*agentName = ans
		}
		if name == "" {
			name, _ = p.Ask(fmt.Sprintf("Profile name [%s]: ", *agentName), *agentName)
		}
		a, ok := agent.Get(*agentName)
		if !ok {
			return fmt.Errorf("unknown agent %q", *agentName)
		}
		if l, ok := a.(agent.ModelLister); ok && len(l.Models()) > 0 {
			ms := l.Models()
			fmt.Fprintf(stdout, "%s\n", s.Dim("Known models: "+strings.Join(ms, ", ")))
		}
		fmt.Fprintln(stdout, s.Dim("Model ids as the agent accepts them; enter skips a tier (the agent default runs)."))
		*fast, _ = p.Ask("  fast tier (quick, cheap): ", *fast)
		*standard, _ = p.Ask("  standard tier: ", *standard)
		*deep, _ = p.Ask("  deep tier (hard problems, loop rounds): ", *deep)
		if f.Default == "" {
			*makeDefault = true
		} else {
			ans, _ := p.Ask(fmt.Sprintf("Make it the default instead of %s? [y/N]: ", f.Default), "n")
			*makeDefault = strings.HasPrefix(strings.ToLower(ans), "y")
		}
	}
	if name == "" {
		name = *agentName
	}
	if *agentName == "" {
		if existing, ok := f.Profiles[name]; ok {
			*agentName = existing.Agent
		} else {
			return errors.New("usage: steermi profile add NAME --agent claude|codex|copilot|opencode [--fast M] [--standard M] [--deep M]")
		}
	}
	if a, ok := agent.Get(*agentName); ok {
		*agentName = a.Name()
	}
	p := f.Profiles[name]
	p.Agent = *agentName
	if *model != "" {
		p.Model = *model
	}
	for t, v := range map[hint.Tier]string{hint.Fast: *fast, hint.Standard: *standard, hint.Deep: *deep} {
		if v = strings.TrimSpace(v); v != "" {
			if p.Models == nil {
				p.Models = map[hint.Tier]string{}
			}
			p.Models[t] = v
		}
	}
	f.Profiles[name] = p
	if *makeDefault || f.Default == "" {
		f.Default = name
	}
	if err := f.Validate(known); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s Saved profile %s (%s)%s.\n", s.Mark("ok"), s.Bold(name), p.Agent, map[bool]string{true: ", the default", false: ""}[f.Default == name])
	return nil
}

// suggestProfiles prints the detected agents after login.
func suggestProfiles(w io.Writer) {
	s := ui.For(w)
	installed := agent.Installed()
	if len(installed) == 0 {
		fmt.Fprintln(w, s.Warn("No coding agent found on PATH.")+" Install Claude Code, Codex CLI, Copilot CLI or opencode; `steermi doctor` checks the setup.")
		return
	}
	var names []string
	for _, a := range installed {
		names = append(names, agent.Title(a))
	}
	fmt.Fprintf(w, "Found %s. Launches use %s unless you set a profile (%s).\n", strings.Join(names, ", "), agent.Title(installed[0]), s.Accent("steermi profile add"))
}
