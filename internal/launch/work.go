package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/next"
	"github.com/steermi/cli/internal/ui"
)

// WorkOptions control `steermi work <KEY>`.
type WorkOptions struct {
	RunOptions
	Key   string
	Skill string
}

// Work creates a launch for a work item key and runs it.
func Work(ctx context.Context, o WorkOptions) error {
	client, err := api.Load()
	if err != nil {
		return err
	}
	mode := ""
	if o.Background {
		mode = "background"
	}
	l, err := client.CreateLaunch(ctx, api.CreateLaunchRequest{WorkItemIdentifier: o.Key, SkillID: o.Skill, Mode: mode, NoAgent: o.NoAgent})
	if err != nil {
		if api.IsCode(err, "NOT_FOUND") || api.IsCode(err, "WORK_ITEM_NOT_FOUND") {
			return fmt.Errorf("work item %s not found (or not visible to you)", o.Key)
		}
		return err
	}
	o.PublicID = l.PublicID
	return Run(ctx, o.RunOptions)
}

// NextOptions control `steermi next`.
type NextOptions struct {
	RunOptions
	TeamID string
	All    bool
	JSON   bool
	// Pick selects an item (1-based) without prompting.
	Pick int
	In   io.Reader
}

// Next lists what to work on and starts the chosen item.
func Next(ctx context.Context, o NextOptions) error {
	client, err := api.Load()
	if err != nil {
		return err
	}
	list, err := client.Next(ctx, o.TeamID, o.All, 20)
	if err != nil {
		return err
	}
	if o.JSON && o.Pick == 0 {
		b, _ := json.MarshalIndent(list, "", "  ")
		fmt.Fprintln(o.Out, string(b))
		return nil
	}
	if o.Pick == 0 {
		fmt.Fprint(o.Out, next.Render(list))
	}
	if len(list.Items) == 0 {
		return nil
	}
	choice := o.Pick
	if choice == 0 {
		choice, err = prompt(o.In, o.Out, list.Items)
		if err != nil || choice == 0 {
			return err
		}
	}
	if choice < 1 || choice > len(list.Items) {
		return fmt.Errorf("pick a number between 1 and %d", len(list.Items))
	}
	item := list.Items[choice-1]
	if item.Tier == "pick_up" {
		if err := client.ClaimWorkItem(ctx, item.WorkItemID); err != nil {
			if api.IsCode(err, "WORK_ITEM_ALREADY_ASSIGNED") {
				return fmt.Errorf("%s was just taken by someone else — run `steermi next` again", item.Identifier)
			}
			return err
		}
		fmt.Fprintf(o.Out, "Assigned %s to you.\n", item.Identifier)
	}
	mode := ""
	if o.Background {
		mode = "background"
	}
	l, err := client.CreateLaunch(ctx, api.CreateLaunchRequest{WorkItemID: item.WorkItemID, Mode: mode, NoAgent: o.NoAgent})
	if err != nil {
		return err
	}
	o.PublicID = l.PublicID
	return Run(ctx, o.RunOptions)
}

// prompt asks for an item: a number, Enter for the first, q to quit, or
// text that filters by key and title (one match starts it).
func prompt(in io.Reader, out io.Writer, items []api.NextItem) (int, error) {
	p := ui.NewPrompter(in, out)
	s := ui.For(out)
	for {
		line, err := p.Ask(fmt.Sprintf("Start which? %s ", s.Dim(fmt.Sprintf("[1-%d, Enter = 1, text filters, q = quit]", len(items)))), "")
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return 0, err
		}
		switch strings.ToLower(line) {
		case "":
			return 1, nil
		case "q", "quit":
			return 0, nil
		}
		if v, err := strconv.Atoi(line); err == nil {
			return v, nil
		}
		var hits []int
		needle := strings.ToLower(line)
		for i, it := range items {
			if strings.Contains(strings.ToLower(it.Identifier+" "+it.Title), needle) {
				hits = append(hits, i+1)
			}
		}
		switch {
		case len(hits) == 1:
			return hits[0], nil
		case len(hits) == 0:
			fmt.Fprintf(out, "  %s\n", s.Dim("no item matches "+strconv.Quote(line)))
		default:
			for _, n := range hits {
				fmt.Fprintf(out, "  %2d  %s  %s\n", n, s.Bold(items[n-1].Identifier), ui.Clip(items[n-1].Title, 60))
			}
		}
		if eof {
			return 0, nil
		}
	}
}
