package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/steermi/cli/internal/agent"
	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/profile"
	"github.com/steermi/cli/internal/ui"
)

// cmdHome is `steermi` without arguments: who you are, what is running,
// what is next and which agents are ready, with the next commands.
func cmdHome(ctx context.Context, _ string, w io.Writer) error {
	s := ui.For(w)
	client, err := api.Load()
	if err != nil {
		fmt.Fprintf(w, "%s — work on Steermi tasks with your own coding agent\n\n", s.Bold("steermi"))
		fmt.Fprintf(w, "  Not logged in. Start with %s, then %s.\n  %s for every command.\n", s.Accent("steermi login"), s.Accent("steermi next"), s.Accent("steermi help"))
		return nil
	}
	c := client.Credentials()
	var agents []string
	for _, a := range agent.Installed() {
		label := a.Name()
		if v := agent.Version(a); v != "" {
			label += " " + v
		}
		agents = append(agents, label)
	}
	agentLine := s.Warn("no agent installed")
	if len(agents) > 0 {
		agentLine = s.Good("●") + " " + strings.Join(agents, "  ")
	}
	device := c.DeviceName
	if device == "" {
		device = "this device"
	}
	fmt.Fprintf(w, "\n %s %s %s   %s\n", s.Bold("steermi"), s.Dim("·"), s.Dim(device+" · "+strings.TrimPrefix(strings.TrimPrefix(c.URL, "https://"), "http://")), agentLine)
	fmt.Fprintln(w, " "+s.Rule(72))

	launches, _ := config.ListLaunches()
	active := 0
	for _, l := range launches {
		if l.Finished || active >= 3 {
			continue
		}
		label := "ACTIVE"
		if active > 0 {
			label = ""
		}
		active++
		what := l.SkillName
		if l.AgentRun.Agent != "" {
			what += " · " + l.AgentRun.Agent
		}
		if l.AgentRun.Model != "" {
			what += "/" + l.AgentRun.Model
		}
		if !l.AgentRun.StartedAt.IsZero() {
			what += " · " + since(l.AgentRun.StartedAt)
		}
		fmt.Fprintf(w, " %s %s  %s\n", s.Accent(ui.Pad(label, 7)), s.Bold(ui.Pad(l.Identifier, 9)), s.Dim(what+" · "+l.Worktree))
	}

	nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := client.Next(nctx, "", false, 3)
	switch {
	case err != nil:
		fmt.Fprintf(w, " %s %s\n", ui.Pad("NEXT", 7), s.Dim("could not load ("+err.Error()+")"))
	case len(list.Items) == 0:
		fmt.Fprintf(w, " %s %s\n", ui.Pad("NEXT", 7), s.Dim("nothing waiting for you in the current cycle"))
	default:
		for i, it := range list.Items {
			label := ""
			if i == 0 {
				label = "NEXT"
			}
			meta := strings.ReplaceAll(it.Status, "_", " ")
			if it.Priority != "" && it.Priority != "none" {
				meta += " · " + it.Priority
			}
			fmt.Fprintf(w, " %s %s  %s  %s\n", ui.Pad(label, 7), s.Bold(ui.Pad(it.Identifier, 9)), ui.Clip(it.Title, 44), s.Dim(meta))
		}
	}
	fmt.Fprintln(w, " "+s.Rule(72))
	hints := []string{s.Accent("steermi next") + " pick work"}
	if active > 0 {
		hints = append(hints, s.Accent("steermi done")+" finish (in the checkout)")
	}
	if p, err := profile.Load(); err == nil && len(p.Profiles) == 0 {
		hints = append(hints, s.Accent("steermi profile add")+" choose models")
	}
	hints = append(hints, s.Accent("steermi help"))
	fmt.Fprintln(w, " "+strings.Join(hints, "   "))
	return nil
}

func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
