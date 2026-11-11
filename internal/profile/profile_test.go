package profile

import (
	"github.com/steermi/agent-contracts/activity"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/hint"
)

func sample() *File {
	return &File{
		Default: "claude",
		Profiles: map[string]Profile{
			"claude": {Agent: "claude", Models: map[hint.Tier]string{hint.Fast: "haiku", hint.Standard: "sonnet", hint.Deep: "opus"}},
			"codex":  {Agent: "codex", Model: "gpt-x", Models: map[hint.Tier]string{hint.Standard: "gpt-x"}},
		},
		Runners: map[activity.Runner]string{activity.Findings: "codex"},
	}
}

func TestResolve(t *testing.T) {
	deep := &hint.Hint{Tier: hint.Deep, Effort: hint.High, Reason: "loop round 2"}
	cases := []struct {
		name                          string
		req                           Request
		profile, agent, model, effort string
		suggested                     bool
	}{
		{"default + hint", Request{Runner: "code", Hint: deep}, "claude", "claude", "opus", "high", true},
		{"kind profile, ladder falls back", Request{Runner: "findings", Hint: deep}, "codex", "codex", "gpt-x", "high", true},
		{"flag model wins", Request{Runner: "code", Hint: deep, Model: "sonnet"}, "claude", "claude", "sonnet", "high", true},
		{"profile flag", Request{Profile: "codex", Runner: "code"}, "codex", "codex", "gpt-x", "", false},
		{"bare agent", Request{Agent: "opencode", Runner: "code"}, "", "opencode", "", "", false},
		{"no effort control drops effort", Request{Agent: "opencode", Runner: "code", Hint: deep, Effort: "low"}, "", "opencode", "", "", false},
		{"repo profile", Request{RepoProfile: "codex", Runner: "code"}, "codex", "codex", "gpt-x", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.req.TakesEffort = func(a string) bool { return a != "opencode" }
			got, err := sample().Resolve(c.req)
			require.NoError(t, err)
			assert.Equal(t, []string{c.profile, c.agent, c.model, c.effort}, []string{got.Profile, got.Agent, got.Model, got.Effort})
			assert.Equal(t, c.suggested, got.Suggested)
			assert.NotEmpty(t, got.Source)
		})
	}
}

func TestResolveNever(t *testing.T) {
	f := sample()
	f.FollowHints = FollowNever
	got, err := f.Resolve(Request{Runner: "code", Hint: &hint.Hint{Tier: hint.Deep, Effort: hint.High}})
	require.NoError(t, err)
	assert.Equal(t, "", got.Model)
	assert.False(t, got.Suggested)
}

func TestResolveWithoutProfiles(t *testing.T) {
	f := &File{Profiles: map[string]Profile{}}
	got, err := f.Resolve(Request{Runner: "code", Installed: []string{"codex"}, Hint: &hint.Hint{Tier: hint.Fast, Effort: hint.Low}, TakesEffort: func(string) bool { return true }})
	require.NoError(t, err)
	assert.Equal(t, "codex", got.Agent)
	assert.Equal(t, "", got.Model, "no ladder: the agent's default model")
	assert.Equal(t, "low", got.Effort)
	_, err = f.Resolve(Request{Runner: "code"})
	require.Error(t, err)
	_, err = sample().Resolve(Request{Profile: "nope"})
	require.Error(t, err)
}

func TestLadder(t *testing.T) {
	p := Profile{Models: map[hint.Tier]string{hint.Fast: "f", hint.Deep: "d"}}
	assert.Equal(t, "f", Ladder(p, hint.Fast))
	assert.Equal(t, "d", Ladder(p, hint.Standard))
	assert.Equal(t, "", Ladder(Profile{}, hint.Deep))
}

func TestSaveLoadValidate(t *testing.T) {
	t.Setenv("STEERMI_CONFIG_DIR", t.TempDir())
	f := sample()
	require.NoError(t, f.Save())
	p, _ := Path()
	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, f, got)

	bad := sample()
	bad.Runners["answer"] = "missing"
	require.Error(t, bad.Save())
	require.Error(t, (&File{Profiles: map[string]Profile{"Bad Name": {Agent: "claude"}}}).Validate(nil))
	require.Error(t, (&File{Profiles: map[string]Profile{"x": {Agent: "claude", Models: map[hint.Tier]string{"huge": "m"}}}}).Validate(nil))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(p), "profiles.json"), []byte("{"), 0o600))
	_, err = Load()
	require.Error(t, err)
}
