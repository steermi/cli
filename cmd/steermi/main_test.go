package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/cli/internal/fixture"
)

// sandbox isolates config, HOME and PATH: a fake `claude` and `git` keep
// doctor deterministic and nothing touches the developer's real setup.
func sandbox(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are shell scripts")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	write := func(name, script string) {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755))
	}
	write("claude", `echo "2.1.0 (Claude Code)"`+"\n")
	write("git", `[ "$1" = "--version" ] && { echo "git version 2.47.0"; exit 0; }; exit 128`+"\n")
	t.Setenv("PATH", bin)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("STEERMI_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("STEERMI_URL", "")
}

func steermi(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, strings.NewReader(stdin), &out, &errOut)
	return out.String() + errOut.String(), err
}

// TestFixtureWalkthrough runs the contributor walkthrough from
// docs/development.md against the fixture gateway.
func TestFixtureWalkthrough(t *testing.T) {
	sandbox(t)
	gw := fixture.New()
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)

	out, err := steermi(t, "wrong-code\n", "login", "--url", srv.URL, "--no-browser")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pairing failed")
	assert.Contains(t, err.Error(), "PAIRING_CODE_INVALID")

	out, err = steermi(t, fixture.PairingCode+"\n", "login", "--url", srv.URL, "--no-browser", "--name", "ci")
	require.NoError(t, err, out)
	assert.Contains(t, out, srv.URL+"/device?", "prints the approval link")
	assert.Contains(t, out, `Paired "fixture-laptop"`)

	info, err := os.Stat(filepath.Join(os.Getenv("STEERMI_CONFIG_DIR"), "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "credentials are private to the user")

	out, err = steermi(t, "", "whoami")
	require.NoError(t, err, out)
	assert.Contains(t, out, "fixture-laptop (dev-fixture)")
	assert.Contains(t, out, "server  "+srv.URL)

	out, err = steermi(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "fixture-laptop")
	assert.Contains(t, out, "claude 2.1.0", "the status home lists installed agents")
	assert.Contains(t, out, "DEMO-1", "and the next work")
	assert.Contains(t, out, "steermi profile add")

	out, err = steermi(t, "", "next", "--json")
	require.NoError(t, err, out)
	var list struct {
		Items []struct{ Identifier string } `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &list))
	require.Len(t, list.Items, 3)
	assert.Equal(t, "DEMO-1", list.Items[0].Identifier)

	out, err = steermi(t, "", "next", "--pick", "3", "--no-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEMO-7 was just taken by someone else")

	gw.ExpireAccessToken()
	out, err = steermi(t, "", "whoami")
	require.NoError(t, err, "an expired access token is refreshed transparently: %s", out)
	refreshes := 0
	for _, c := range gw.Calls() {
		if c.Path == "/v1/ai/devices/token" {
			refreshes++
		}
	}
	assert.Equal(t, 1, refreshes)

	out, err = steermi(t, "", "doctor")
	require.NoError(t, err, out)
	for _, want := range []string{"✓ steermi", "✓ config", "✓ claude       Claude Code 2.1.0", "✓ git          git version 2.47.0", "✓ login        fixture-laptop on " + srv.URL} {
		assert.Contains(t, out, want)
	}

	gw.Revoke()
	gw.ExpireAccessToken()
	out, err = steermi(t, "", "whoami")
	require.Error(t, err, out)
	assert.Contains(t, err.Error(), "DEVICE_REVOKED")
	assert.Contains(t, err.Error(), "run `steermi login` again")

	out, err = steermi(t, "", "doctor")
	require.Error(t, err)
	assert.Equal(t, "doctor found 1 problem(s)", err.Error())
	assert.Contains(t, out, "✗ login")

	out, err = steermi(t, "", "logout")
	require.NoError(t, err)
	_, err = steermi(t, "", "whoami")
	assert.ErrorContains(t, err, "not logged in")
}

func TestDoctorFlagsWorldReadableCredentials(t *testing.T) {
	sandbox(t)
	dir := os.Getenv("STEERMI_CONFIG_DIR")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"url":"http://127.0.0.1:1"}`), 0o644))
	out, err := steermi(t, "", "doctor")
	require.Error(t, err)
	assert.Contains(t, out, "is readable by other users")
}

func TestCommandLine(t *testing.T) {
	sandbox(t)

	out, err := steermi(t, "")
	require.NoError(t, err)
	assert.Contains(t, out, "Not logged in")

	out, err = steermi(t, "", "help")
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "--profile NAME")

	out, err = steermi(t, "", "version")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "steermi "), out)

	out, err = steermi(t, "", "version", "--json")
	require.NoError(t, err)
	var v map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.NotEmpty(t, v["version"])

	for _, cmd := range []string{"work", "next", "run", "login", "done", "handoff"} {
		out, err = steermi(t, "", cmd, "--help")
		assert.ErrorIs(t, err, flag.ErrHelp, "%s --help exits successfully", cmd)
		assert.Contains(t, out, "Usage of "+cmd)
	}

	_, err = steermi(t, "", "bogus")
	assert.EqualError(t, err, "unknown command \"bogus\" (see `steermi help`)")

	_, err = steermi(t, "", "work")
	assert.ErrorContains(t, err, "usage: steermi work KEY")

	_, err = steermi(t, "", "open", "https://evil.example/launch/lx_7Q4XK2M9ab")
	assert.Error(t, err, "only steermi:// launch links are accepted")
}

func TestProfileCommands(t *testing.T) {
	sandbox(t)
	out, err := steermi(t, "", "profile")
	require.NoError(t, err)
	assert.Contains(t, out, "No profiles yet")

	_, err = steermi(t, "", "profile", "add", "fast", "--agent", "vim")
	assert.ErrorContains(t, err, `unknown agent "vim"`)

	out, err = steermi(t, "", "profile", "add", "cc", "--agent", "claude", "--fast", "haiku", "--deep", "opus")
	require.NoError(t, err, out)
	assert.Contains(t, out, "Saved profile cc (claude), the default")
	out, err = steermi(t, "", "profile", "add", "cx", "--agent", "codex", "--standard", "gpt-x")
	require.NoError(t, err, out)
	out, err = steermi(t, "", "profile", "use", "cx", "--runner", "findings")
	require.NoError(t, err, out)
	assert.Contains(t, out, "Skills that hand back findings run with cx")
	_, err = steermi(t, "", "profile", "use", "nope")
	assert.Error(t, err)
	_, err = steermi(t, "", "profile", "follow", "sometimes")
	assert.Error(t, err)
	_, err = steermi(t, "", "profile", "follow", "auto")
	require.NoError(t, err)

	out, err = steermi(t, "", "profile", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "haiku / – / opus")
	assert.Contains(t, out, "findings")
	assert.Contains(t, out, "Model suggestions: auto")

	out, err = steermi(t, "", "profile", "rm", "cx")
	require.NoError(t, err, out)
	out, _ = steermi(t, "", "profile", "list")
	assert.NotContains(t, out, "cx ")

	out, err = steermi(t, "", "doctor")
	assert.Contains(t, out, "profiles     1 (default cc, suggestions: auto)")
	assert.Contains(t, out, "codex        not installed")
}
