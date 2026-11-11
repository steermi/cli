// Package profile holds the developer's agent profiles
// (~/.config/steermi/profiles.json) and resolves which agent and model a
// launch runs with. A profile is an agent plus a model per tier; Steermi's
// suggestion (tier + effort) picks the tier, the profile names the model.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/steermi/agent-contracts/hint"

	"github.com/steermi/cli/internal/config"
)

// Follow modes for Steermi's model suggestion.
const (
	// FollowAsk applies the suggestion and lets the developer change it in
	// the launch card (default).
	FollowAsk = "ask"
	// FollowAuto applies it without asking.
	FollowAuto = "auto"
	// FollowNever ignores it: the profile's default model always runs.
	FollowNever = "never"
)

// Profile is one agent configuration.
type Profile struct {
	Agent string `json:"agent"`
	// Model is the default model ("" = the agent's own default).
	Model string `json:"model,omitempty"`
	// Models maps tiers (fast, standard, deep) to model ids.
	Models map[hint.Tier]string `json:"models,omitempty"`
}

// File is profiles.json.
type File struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
	// Runners picks a profile per runner: what the run's skill hands back
	// (answer, code, findings).
	Runners map[activity.Runner]string `json:"runners,omitempty"`
	// FollowHints is ask (default), auto or never.
	FollowHints string `json:"follow_hints,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidName reports whether n can name a profile.
func ValidName(n string) bool { return nameRe.MatchString(n) }

// Path is the profiles file.
func Path() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles.json"), nil
}

// Load reads profiles.json; a missing file is an empty configuration.
func Load() (*File, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f := &File{Profiles: map[string]Profile{}}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return f, f.Validate(nil)
}

// Save writes profiles.json (0600).
func (f *File) Save() error {
	if err := f.Validate(nil); err != nil {
		return err
	}
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Validate checks names, agents (known reports whether an agent exists; nil
// skips that check), tiers and references.
func (f *File) Validate(known func(string) bool) error {
	for name, p := range f.Profiles {
		if !ValidName(name) {
			return fmt.Errorf("profile name %q: use lowercase letters, digits, - or _", name)
		}
		if p.Agent == "" || (known != nil && !known(p.Agent)) {
			return fmt.Errorf("profile %q: unknown agent %q", name, p.Agent)
		}
		for t := range p.Models {
			if !t.Valid() {
				return fmt.Errorf("profile %q: unknown tier %q (fast, standard, deep)", name, t)
			}
		}
	}
	if f.Default != "" {
		if _, ok := f.Profiles[f.Default]; !ok {
			return fmt.Errorf("default profile %q does not exist", f.Default)
		}
	}
	for runner, name := range f.Runners {
		if _, ok := f.Profiles[name]; !ok {
			return fmt.Errorf("runners.%s: profile %q does not exist", runner, name)
		}
	}
	switch f.FollowHints {
	case "", FollowAsk, FollowAuto, FollowNever:
	default:
		return fmt.Errorf("follow_hints must be ask, auto or never")
	}
	return nil
}

// Follow is the effective follow mode.
func (f *File) Follow() string {
	if f.FollowHints == "" {
		return FollowAsk
	}
	return f.FollowHints
}

// Names lists profile names sorted.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Request is what a launch asks for.
type Request struct {
	// Flags (highest precedence).
	Profile, Agent, Model, Effort string
	// RepoProfile is runner.yaml's `profile:`.
	RepoProfile string
	Runner      activity.Runner
	Hint        *hint.Hint
	// Installed lists the agents found on PATH, in preference order.
	Installed []string
	// TakesEffort reports whether an agent has an effort control; effort is
	// dropped for the others (nil: none has).
	TakesEffort func(agent string) bool
}

// Choice is the resolved launch configuration.
type Choice struct {
	// Profile is "" when an agent runs without a profile.
	Profile string
	Agent   string
	Model   string
	Effort  string
	// Suggested: the model or the effort was taken from Steermi's hint.
	Suggested bool
	// Hint is the suggestion that was considered (nil = none).
	Hint *hint.Hint
	// Source says why this profile was chosen (for the launch card).
	Source string
}

// Resolve picks the profile, agent and model. Profile: --profile → --agent
// (its profile of the same name, else the bare agent) → runner.yaml →
// runners[runner] → default → the first installed agent. Model: --model → the
// hint's tier through the profile's ladder (unless follow_hints=never) →
// the profile's model → the agent's default.
func (f *File) Resolve(r Request) (Choice, error) {
	c := Choice{Hint: r.Hint}
	pick := func(name, source string) error {
		p, ok := f.Profiles[name]
		if !ok {
			return fmt.Errorf("profile %q does not exist — see `steermi profile list`", name)
		}
		c.Profile, c.Agent, c.Source = name, p.Agent, source
		return nil
	}
	switch {
	case r.Profile != "":
		if err := pick(r.Profile, "--profile"); err != nil {
			return c, err
		}
	case r.Agent != "":
		if p, ok := f.Profiles[r.Agent]; ok && p.Agent == r.Agent {
			c.Profile = r.Agent
		}
		c.Agent, c.Source = r.Agent, "--agent"
	case r.RepoProfile != "" && f.Profiles[r.RepoProfile].Agent != "":
		_ = pick(r.RepoProfile, "repository runner.yaml")
	case f.Runners[r.Runner] != "":
		_ = pick(f.Runners[r.Runner], "your profile for "+string(r.Runner)+" skills")
	case f.Default != "":
		_ = pick(f.Default, "your default profile")
	default:
		if len(r.Installed) == 0 {
			return c, errors.New("no coding agent found on PATH — install Claude Code, Codex CLI, Copilot CLI or opencode, then run `steermi doctor`")
		}
		c.Agent, c.Source = r.Installed[0], "first installed agent"
		if p, ok := f.Profiles[c.Agent]; ok && p.Agent == c.Agent {
			c.Profile = c.Agent
		}
	}
	p := f.Profiles[c.Profile]
	follow := r.Hint != nil && f.Follow() != FollowNever
	c.Model = r.Model
	if c.Model == "" && follow {
		if m := Ladder(p, r.Hint.Tier); m != "" {
			c.Model, c.Suggested = m, true
		}
	}
	if c.Model == "" {
		c.Model = p.Model
	}
	if r.TakesEffort != nil && r.TakesEffort(c.Agent) {
		c.Effort = r.Effort
		if c.Effort == "" && follow {
			c.Effort, c.Suggested = string(r.Hint.Effort), true
		}
	}
	return c, nil
}

// Ladder maps a tier onto the profile's models: the tier itself, else the
// nearest tier toward standard, else any configured one ("" when none).
func Ladder(p Profile, t hint.Tier) string {
	order := map[hint.Tier][]hint.Tier{
		hint.Fast:     {hint.Fast, hint.Standard, hint.Deep},
		hint.Standard: {hint.Standard, hint.Deep, hint.Fast},
		hint.Deep:     {hint.Deep, hint.Standard, hint.Fast},
	}[t]
	for _, tier := range order {
		if m := strings.TrimSpace(p.Models[tier]); m != "" {
			return m
		}
	}
	return ""
}
