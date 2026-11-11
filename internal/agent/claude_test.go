package agent

import (
	"github.com/steermi/agent-contracts/activity"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgsInteractiveNewSession(t *testing.T) {
	args := Args(Launch{Mode: Interactive, Identifier: "STE-123", Title: "Add retry", SystemPrompt: "sys", SessionID: "uuid-1", PluginDir: "/p", Runner: "code"})
	assert.Equal(t, []string{
		"--session-id", "uuid-1", "--name", "STE-123 · Add retry", "--append-system-prompt", "sys", "--plugin-dir", "/p",
		InitialPrompt("STE-123", false),
	}, args)
	assert.NotContains(t, args, "-p", "interactive runs in the developer's terminal")
	assert.NotContains(t, args, "--dangerously-skip-permissions")
}

func TestArgsInteractiveResume(t *testing.T) {
	args := Args(Launch{Mode: Interactive, Identifier: "STE-1", SessionID: "uuid-1", Resume: true, Runner: activity.Code})
	assert.Equal(t, []string{"--resume", "uuid-1", "--name", "STE-1"}, args, "resume reopens the conversation without a new prompt")
}

func TestArgsBackground(t *testing.T) {
	args := Args(Launch{Mode: Background, Identifier: "STE-1", SessionID: "u", AllowedTools: []string{"Bash(make test)", "Bash(go test *)"}, Runner: "findings"})
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "-p --output-format stream-json --verbose --permission-mode default")
	assert.Contains(t, joined, "--allowedTools Bash(make test),Bash(go test *)")
	assert.NotContains(t, joined, "--name")
	assert.NotContains(t, joined, "dangerously")
	assert.Equal(t, InitialPrompt("STE-1", true), args[len(args)-1])
}

func TestInitialPromptNamesTheBoundaryNotTheWork(t *testing.T) {
	edit, readOnly := InitialPrompt("X-1", false), InitialPrompt("X-1", true)
	assert.Contains(t, edit, "run the tests")
	assert.Contains(t, readOnly, "do not edit files")
	for _, p := range []string{edit, readOnly} {
		assert.Contains(t, p, "follow the run's skill")
		assert.NotContains(t, p, "Plan")
		assert.NotContains(t, p, "Review")
	}
}

func TestSessionNameTruncates(t *testing.T) {
	name := sessionName("STE-1", strings.Repeat("long ", 30))
	assert.LessOrEqual(t, len([]rune(name)), 60)
	assert.True(t, strings.HasSuffix(name, "…"))
	assert.Equal(t, "STE-1", sessionName("STE-1", ""))
}

func TestActivityReadOnlyLaunchDoesNotAutoApproveEdits(t *testing.T) {
	for _, runner := range []activity.Runner{activity.Answer, activity.Findings} {
		args := Args(Launch{Mode: Background, Runner: runner, AllowedTools: []string{"Edit", "Write", "Bash"}})
		joined := strings.Join(args, " ")
		require.NotContains(t, joined, "acceptEdits")
		require.Contains(t, joined, "--disallowedTools")
		require.Contains(t, joined, "Edit,Write,NotebookEdit")
	}
}
