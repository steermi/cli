package launch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/steermi/agent-contracts/activity"

	"github.com/steermi/cli/internal/api"
)

// runView is the gateway's activity run view: the contract run plus the
// host facts the CLI words (the skill's display name, why an automatic next
// step did not start).
type runView struct {
	activity.Run
	DisplayName string `json:"skill_name"`
	// AutoPausedReason is set when the run's automatic next step could not
	// start; nil otherwise.
	AutoPausedReason *activity.AdvancePausedReason `json:"auto_paused_reason"`
	// Successor is the run this run handed its flow to (next, apply fixes
	// or an automatic hop); nil until one exists.
	Successor *runSuccessor `json:"successor"`
}

// runSuccessor names the run a run handed its flow to.
type runSuccessor struct {
	ID                string            `json:"id"`
	Runner            activity.Runner   `json:"runner"`
	SkillName         string            `json:"skill_name"`
	State             activity.State    `json:"state"`
	ExecutionLocation activity.Location `json:"execution_location"`
	SessionID         string            `json:"session_id"`
	// LaunchPublicID is a local successor's pending launch (lx_…), what
	// `steermi run` takes; empty for cloud runs or before its launch exists.
	LaunchPublicID string `json:"launch_public_id"`
}

// started reports a successor the server has started (ready or running);
// a suggested, waiting or finished successor has not started.
func (s runSuccessor) started() bool {
	return s.State == activity.Ready || s.State == activity.Running
}

// waitsLocally reports a local successor still waiting for a developer's
// `steermi run` (suggested, ready or waiting; not running or finished).
func (s runSuccessor) waitsLocally() bool {
	if s.ExecutionLocation != activity.Local {
		return false
	}
	switch s.State {
	case activity.Suggested, activity.Ready, activity.Waiting:
		return true
	}
	return false
}

// name is the successor's step as people see it.
func (s runSuccessor) name() string {
	if s.SkillName != "" {
		return s.SkillName
	}
	return s.Runner.Label()
}

// name is the run's skill as people see it.
func (v runView) name() string {
	if v.DisplayName != "" {
		return v.DisplayName
	}
	return v.SkillName()
}

func (v runView) paused() activity.AdvancePausedReason {
	if v.AutoPausedReason == nil {
		return ""
	}
	return *v.AutoPausedReason
}

// consentLine states what a flow loop will do before its agent starts:
// the loop Steermi reports in the claim (its steps, round limit and budget,
// checked when a round starts, so "no new round after", never a hard cap)
// and whether it pauses after each step (the loop asks, or the run's step
// mode, recorded at claim, is ask). Empty outside a loop.
func consentLine(l *api.FlowLoop, mode activity.StepMode) string {
	if l == nil || len(l.Steps) == 0 {
		return ""
	}
	line := "Zoe will run " + strings.Join(l.Steps, " → ")
	if mode == activity.StepModeAsk || l.Advance != activity.LoopAdvanceAuto {
		line += ", pausing after each step"
	} else {
		line += " automatically"
	}
	if l.ReviewStep != "" {
		line += " until " + l.ReviewStep + " is clean"
	}
	if l.MaxIterations > 0 {
		line += " · max " + strconv.Itoa(l.MaxIterations) + " rounds"
	}
	if l.BudgetUSD > 0 {
		line += " · no new round after " + dollars(l.BudgetUSD)
	}
	return line
}

func dollars(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("$%d", int64(f))
	}
	return fmt.Sprintf("$%.2f", f)
}

// nextStepHint explains what follows a submission when the run cannot be
// read: the delivery flow's next step is offered on the work item.
const nextStepHint = "Next: the work item's delivery flow offers the next step (for example Review). Start it from the work item, in the cloud or on this machine (Steermi gives you its `steermi run` command)."

// Polling for an automatic next step that could not start (tests shorten it).
var (
	pausePollFor   = 5 * time.Second
	pausePollEvery = time.Second
)

// nextStep reads the submitted run and words what its pinned flow hop does
// next. An automatic hop is watched for up to pausePollFor (done waits that
// long at most): the flow advancer either starts it in the cloud or records
// why it paused.
func nextStep(ctx context.Context, a *Active) string {
	read := func() (runView, bool) {
		var v runView
		if a.State.ActivityRunID == "" || a.Do(ctx, http.MethodGet, "/ai/activities/"+url.PathEscape(a.State.ActivityRunID), nil, &v) != nil {
			return v, false
		}
		return v, true
	}
	v, ok := read()
	if !ok {
		return nextStepHint
	}
	deadline := time.Now().Add(pausePollFor)
	for time.Now().Before(deadline) && v.awaitsAdvancer() {
		select {
		case <-ctx.Done():
			return describeNext(v, a.linkFor(v))
		case <-time.After(pausePollEvery):
		}
		if again, ok := read(); ok {
			v = again
		}
	}
	return describeNext(v, a.linkFor(v))
}

// awaitsAdvancer reports an automatic hop the flow advancer has neither
// started nor paused yet.
func (v runView) awaitsAdvancer() bool {
	_, auto := v.AutoHop()
	return auto && v.paused() == "" && v.Successor == nil
}

// linkFor is where to follow the run's next step: its successor's session
// in Steermi when it has one, else the work item.
func (a *Active) linkFor(v runView) string {
	if s := v.Successor; s != nil && s.started() && s.SessionID != "" && s.ExecutionLocation == activity.Cloud && v.ProjectID != "" && a.Client != nil {
		return strings.TrimRight(a.Client.URL, "/") + "/projects/" + url.PathEscape(v.ProjectID) + "/chat/" + url.PathEscape(s.SessionID)
	}
	return a.workItemLink()
}

// workItemLink opens the launch's work item in Steermi by its key.
func (a *Active) workItemLink() string {
	if a.Client == nil || a.State.Identifier == "" {
		return ""
	}
	return strings.TrimRight(a.Client.URL, "/") + "/work-items/key/" + url.PathEscape(a.State.Identifier)
}

// pausedReasons words every reason an automatic next step did not start
// (one entry per activity.AdvancePausedReason; a test keeps them in step).
var pausedReasons = map[activity.AdvancePausedReason]string{
	activity.PausedNoProviderKey:   "no AI key for Steermi runs",
	activity.PausedRouteNotAllowed: "the organization's AI policy does not allow the model for Steermi runs",
	activity.PausedOwnerInactive:   "the run's owner is no longer an active member",
	activity.PausedAutoNotAllowed:  "automatic steps are turned off for this organization or plan",
	activity.PausedProjectMoved:    "the project moved to another team",
}

// pausedWording explains why an automatic next step did not start; a
// reason newer than this CLI is spelled out from its value.
func pausedWording(reason activity.AdvancePausedReason) string {
	if w, ok := pausedReasons[reason]; ok {
		return "Paused: " + w
	}
	return "Paused: " + strings.ReplaceAll(string(reason), "_", " ")
}

// nextFacts is what describeNext words: the run's next hop and how to
// reach it.
type nextFacts struct {
	v        runView
	hop      activity.FlowHop
	stop     activity.Blocker
	step     string // the next step's name
	link     string // where to follow it ("" when unknown)
	at       string // " (link)" or ""
	start    string // how a person starts it
	statuses string // " The item moves to …." or ""
	hopKnown bool   // the run's pinned flow names its next hop
}

// inSteermi words a step that runs in Steermi: "X starts in Steermi: link"
// or "X started in Steermi." without one.
func inSteermi(step, verb, link string) string {
	if link == "" {
		return fmt.Sprintf("%s %s in Steermi.", step, verb)
	}
	return fmt.Sprintf("%s %s in Steermi: %s", step, verb, link)
}

// nextCases are describeNext's wordings, first match wins: a stopped loop
// (its successor, if any, waits for that decision), a local successor
// waiting for `steermi run`, a successor the server started, a suggested
// successor, then (only when the hop is known) an approval, the flow's
// end, a paused automatic step, an automatic cloud step, a local step, a
// step paused by the run's step mode, else a step a person starts. A
// successor in another state (cancelled, failed, waiting in the cloud)
// says nothing about what follows, so the hop decides.
var nextCases = []struct {
	hop  bool // needs a known hop
	when func(f nextFacts) bool
	say  func(f nextFacts) string
}{
	{true, func(f nextFacts) bool { return f.stop != "" },
		func(f nextFacts) string {
			return fmt.Sprintf("Next: the quality loop stopped (%s); a person decides on the work item%s.", strings.ReplaceAll(string(f.stop), "_", " "), f.at)
		}},
	{false, func(f nextFacts) bool { return f.v.Successor != nil && f.v.Successor.waitsLocally() },
		func(f nextFacts) string {
			s := f.v.Successor
			if s.LaunchPublicID != "" {
				return fmt.Sprintf("Next: %s waits for you on this machine: run `steermi run %s`.", s.name(), s.LaunchPublicID)
			}
			return fmt.Sprintf("Next: %s waits for you on this machine: its `steermi run` command is on the work item%s.", s.name(), f.at)
		}},
	{false, func(f nextFacts) bool { return f.v.Successor != nil && f.v.Successor.started() },
		func(f nextFacts) string { return inSteermi(f.v.Successor.name(), "started", f.link) }},
	{false, func(f nextFacts) bool { return f.v.Successor != nil && f.v.Successor.State == activity.Suggested },
		func(f nextFacts) string {
			return fmt.Sprintf("Next: %s waits for a person to confirm it on the work item%s.", f.v.Successor.name(), f.at)
		}},
	{true, func(f nextFacts) bool { return f.hop.Runner == "" && f.hop.Approval },
		func(f nextFacts) string { return "Next: a person decides on the work item" + f.at + "." }},
	{true, func(f nextFacts) bool { return f.hop.Runner == "" },
		func(nextFacts) string { return "Next: the delivery flow ends here." }},
	{true, func(f nextFacts) bool { return f.v.paused() != "" },
		func(f nextFacts) string {
			return fmt.Sprintf("%s. Next: %s — %s.", pausedWording(f.v.paused()), f.step, f.start)
		}},
	{true, func(f nextFacts) bool { _, auto := f.v.AutoHop(); return auto },
		func(f nextFacts) string { return inSteermi(f.step, "starts", f.link) }},
	{true, func(f nextFacts) bool { return f.hop.Location == activity.Local },
		func(f nextFacts) string {
			return fmt.Sprintf("Next: %s runs on this machine: start it from the work item%s for its `steermi run` command.", f.step, f.at)
		}},
	{true, func(f nextFacts) bool { return f.hop.Auto && f.v.StepMode == activity.StepModeAsk },
		func(f nextFacts) string {
			return fmt.Sprintf("Paused after this step (pause after each step is on). Next: %s — %s.", f.step, f.start)
		}},
	{true, func(nextFacts) bool { return true },
		func(f nextFacts) string { return fmt.Sprintf("Next: %s — %s.", f.step, f.start) }},
}

// describeNext words a run's flow hop: an automatic cloud step started in
// Steermi, a step that paused (and why), or a step a person starts. A step a
// person starts (or one waiting on a developer's machine) is followed from
// the work item, which continues this run's flow (its loop, its step's
// skill) and gives the `steermi run` command for this machine; the CLI never
// starts a new run beside it.
func describeNext(v runView, link string) string {
	hop, stop, ok := v.FlowNext()
	if !ok && v.Successor == nil {
		return nextStepHint
	}
	f := nextFacts{v: v, hop: hop, stop: stop, step: hop.Name, link: link, hopKnown: ok}
	if f.step == "" && hop.Runner != "" {
		f.step = hop.Runner.Label()
	}
	if link != "" {
		f.at = " (" + link + ")"
	}
	f.start = "start it from the work item" + f.at + ", in the cloud or on this machine"
	if len(hop.Statuses) > 0 {
		names := make([]string, len(hop.Statuses))
		for i, s := range hop.Statuses {
			names[i] = strings.ReplaceAll(s.Category, "_", " ")
		}
		f.statuses = " The item moves to " + strings.Join(names, ", then ") + "."
	}
	for _, c := range nextCases {
		if (!c.hop || f.hopKnown) && c.when(f) {
			return c.say(f) + f.statuses
		}
	}
	return nextStepHint
}
