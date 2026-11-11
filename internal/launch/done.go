package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/steermi/agent-contracts/activity"

	"github.com/steermi/cli/internal/config"
	"github.com/steermi/cli/internal/gitx"
	"github.com/steermi/cli/internal/mcpserver"
)

// DoneOptions control `steermi done`.
type DoneOptions struct {
	Cwd     string
	Summary string
	// Commit commits uncommitted changes before pushing.
	Commit bool
	// NoMR skips opening the merge request.
	NoMR bool
	Out  io.Writer
}

// Done pushes the launch branch, opens the merge request, posts the summary
// and completes the launch. Status moves to in_review through the existing
// merge request webhook mapping, not here.
func Done(ctx context.Context, o DoneOptions) error {
	a, err := LoadActive(o.Cwd)
	if err != nil {
		return err
	}
	dir, st := a.Root, a.State
	if st.Finished {
		return fmt.Errorf("%s is already finished", st.Identifier)
	}
	if !st.Runner.ChangesCode() {
		summary := strings.TrimSpace(o.Summary)
		if summary == "" {
			summary = mcpserver.LoadSummary(st.LaunchID)
		}
		if strings.TrimSpace(summary) == "" {
			return errors.New("provide the saved planning outputs or review findings with --summary")
		}
		result, err := mcpserver.LoadActivityResult(st.LaunchID)
		if err != nil {
			return errors.New("save structured planning outputs or review findings using steermi_complete_task(activity_result) before submitting")
		}
		_ = Flush(ctx, a, "")
		if err := a.Do(ctx, http.MethodPost, "/ai/local-launches/"+url.PathEscape(st.LaunchID)+"/finish", map[string]any{"state": "completed", "summary": summary, "activity_result": result, "runner": runnerReport(st)}, nil); err != nil {
			return fmt.Errorf("submit activity: %w", err)
		}
		if _, err := config.UpdateLaunch(st.LaunchID, func(s *config.LaunchState) { s.Finished = true }); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "%s %s submitted.\n", st.Identifier, st.SkillName)
		fmt.Fprintln(o.Out, nextStep(ctx, a))
		return nil
	}
	branch, err := gitx.CurrentBranch(dir)
	if err != nil {
		return err
	}
	if branch == st.BaseBranch || branch == gitx.DefaultBranch(dir) || branch == "HEAD" {
		return fmt.Errorf("refusing to push %q — work on the launch branch %q", branch, st.Branch)
	}
	if dirty, err := gitx.Dirty(dir); err != nil {
		return err
	} else if dirty {
		if !o.Commit {
			return errors.New("you have uncommitted changes — commit them, or run `steermi done --commit`")
		}
		if err := gitx.CommitAll(dir, fmt.Sprintf("chore: finish %s", st.Identifier)); err != nil {
			return err
		}
	}
	commits, err := gitx.CommitsSince(dir, st.BaseBranch)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return fmt.Errorf("no commits on %s since %s — nothing to deliver", branch, st.BaseBranch)
	}
	fmt.Fprintf(o.Out, "Pushing %s (%d commit(s))…\n", branch, len(commits))
	if err := gitx.Push(dir, branch); err != nil {
		return err
	}

	summary := strings.TrimSpace(o.Summary)
	if summary == "" {
		summary = mcpserver.LoadSummary(st.LaunchID)
	}
	if summary == "" {
		log, _ := gitx.Run(dir, "log", "--format=- %s", "origin/"+st.BaseBranch+"..HEAD")
		summary = "Changes:\n" + log
	}

	mrURL := ""
	if !o.NoMR && st.RepositoryID != "" {
		title := fmt.Sprintf("%s: %s", st.Identifier, firstLine(summary))
		var pr struct {
			PRURL string `json:"pr_url"`
		}
		err := a.Do(ctx, http.MethodPost, "/integrations/git/repositories/"+url.PathEscape(st.RepositoryID)+"/pulls", map[string]string{
			"title": truncate(title, 200), "body": summary + "\n\n---\nSteermi task " + st.Identifier + " · opened by steermi done",
			"head": branch, "base": st.BaseBranch,
		}, &pr)
		if err != nil {
			fmt.Fprintf(o.Out, "warning: could not open the merge request: %v\n", err)
		} else {
			mrURL = pr.PRURL
		}
	}

	comment := "**Delivered from a local session.**\n\n" + summary
	if mrURL != "" {
		comment += "\n\nMerge request: " + mrURL
	}
	if err := a.Do(ctx, http.MethodPost, "/work_items/"+url.PathEscape(st.WorkItemID)+"/comments", map[string]string{"body_markdown": comment}, nil); err != nil {
		fmt.Fprintf(o.Out, "warning: could not post the summary comment: %v\n", err)
	}
	_ = Flush(ctx, a, "")
	sha, err := gitx.HeadSHA(dir)
	if err != nil {
		return err
	}
	result := map[string]any{}
	if saved, e := mcpserver.LoadActivityResult(st.LaunchID); e == nil {
		_ = json.Unmarshal(saved, &result)
	}
	if st.RepositoryID != "" {
		result["outputs"] = []map[string]string{{"kind": "repository", "resource_id": st.RepositoryID, "repository_id": st.RepositoryID, "commit_sha": sha}}
	}
	if err := a.Do(ctx, http.MethodPost, "/ai/local-launches/"+url.PathEscape(st.LaunchID)+"/finish", map[string]any{
		"state": "completed", "summary": summary, "mr_url": mrURL, "commits": commits, "activity_result": result, "runner": runnerReport(st),
	}, nil); err != nil {
		return fmt.Errorf("finish launch: %w", err)
	}
	st.Finished = true
	if _, err := config.UpdateLaunch(st.LaunchID, func(s *config.LaunchState) { s.Finished = true }); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "\n%s delivered.\n", st.Identifier)
	if mrURL != "" {
		fmt.Fprintf(o.Out, "  merge request  %s\n", mrURL)
	}
	fmt.Fprintln(o.Out, nextStep(ctx, a))
	return nil
}

// nextStepHint explains what follows a submission when the run cannot be
// read: the delivery flow's next step is offered on the work item.
const nextStepHint = "Next: the work item's delivery flow offers the next step (for example Review). Automatic steps run in the cloud; to run the next step here, start it with `steermi work` or hand it to the cloud from the work item."

// nextStep reads the submitted run and names the step its pinned delivery
// flow hop leads to; the generic hint when the run cannot be read.
func nextStep(ctx context.Context, a *Active) string {
	var r activity.Run
	if a.State.ActivityRunID == "" || a.Do(ctx, http.MethodGet, "/ai/activities/"+url.PathEscape(a.State.ActivityRunID), nil, &r) != nil {
		return nextStepHint
	}
	return describeNext(r)
}

// describeNext words a run's flow hop. Steps set to start automatically run
// in the cloud only: a local run never chains on this machine.
func describeNext(r activity.Run) string {
	hop, stop, ok := r.FlowNext()
	if !ok {
		return nextStepHint
	}
	statuses := ""
	if len(hop.Statuses) > 0 {
		names := make([]string, len(hop.Statuses))
		for i, s := range hop.Statuses {
			names[i] = strings.ReplaceAll(s.Category, "_", " ")
		}
		statuses = " The item moves to " + strings.Join(names, ", then ") + "."
	}
	step := hop.Name
	switch {
	case stop != "":
		return fmt.Sprintf("Next: the quality loop stopped (%s); a person decides on the work item.%s", strings.ReplaceAll(string(stop), "_", " "), statuses)
	case step != "" && hop.Auto:
		return fmt.Sprintf("Next: %s starts automatically in the cloud (automatic steps never chain on this machine); to run it here instead, start it with `steermi work`.%s", step, statuses)
	case step != "":
		return fmt.Sprintf("Next: %s. Start it from the work item, or here with `steermi work`.%s", step, statuses)
	case hop.Approval:
		return "Next: a person decides on the work item." + statuses
	}
	return "Next: the delivery flow ends here." + statuses
}

// HandoffOptions control `steermi handoff`.
type HandoffOptions struct {
	Cwd     string
	Summary string
	Out     io.Writer
}

// Handoff returns the session to Steermi. Build also saves its work in progress.
func Handoff(ctx context.Context, o HandoffOptions) error {
	a, err := LoadActive(o.Cwd)
	if err != nil {
		return err
	}
	dir, st := a.Root, a.State
	branch, sha := "", ""
	if st.Runner.ChangesCode() {
		branch, err = gitx.CurrentBranch(dir)
		if err != nil {
			return err
		}
		if branch == st.BaseBranch || branch == "HEAD" {
			return fmt.Errorf("refusing to push %q — work on the launch branch %q", branch, st.Branch)
		}
		dirty, err := gitx.Dirty(dir)
		if err != nil {
			return err
		}
		if dirty {
			if err := gitx.CommitAll(dir, "wip: handoff "+st.Identifier); err != nil {
				return err
			}
		}
		if err := gitx.Push(dir, branch); err != nil {
			return err
		}
		sha, err = gitx.HeadSHA(dir)
		if err != nil {
			return err
		}
	}
	_ = Flush(ctx, a, "")
	if err := a.Do(ctx, http.MethodPost, "/ai/local-launches/"+url.PathEscape(st.LaunchID)+"/handoff", map[string]string{
		"summary": strings.TrimSpace(o.Summary), "branch": branch, "head_sha": sha,
	}, nil); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	st.Finished = true
	if _, err := config.UpdateLaunch(st.LaunchID, func(s *config.LaunchState) { s.Finished = true }); err != nil {
		return err
	}
	if branch != "" {
		fmt.Fprintf(o.Out, "%s handed back to Steermi (branch %s @ %s). Continue it from the session in Steermi.\n", st.Identifier, branch, shortSHA(sha))
	} else {
		fmt.Fprintf(o.Out, "%s handed back to Steermi. Continue it from the session in Steermi.\n", st.Identifier)
	}
	return nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-* "))
		if line != "" {
			return line
		}
	}
	return "changes"
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
