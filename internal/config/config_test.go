package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "steermi")
	prev := Dir
	Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { Dir = prev })
	return dir
}

func TestCredentialsRoundTripAndPermissions(t *testing.T) {
	dir := tempDir(t)
	_, err := LoadCredentials()
	assert.ErrorIs(t, err, ErrNotLoggedIn)

	require.NoError(t, SaveCredentials(&Credentials{URL: "https://steermi.com", RefreshToken: "std_x", DeviceID: "d1", AccessExpiry: time.Now()}))
	c, err := LoadCredentials()
	require.NoError(t, err)
	assert.Equal(t, "std_x", c.RefreshToken)

	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	require.NoError(t, DeleteCredentials())
	_, err = LoadCredentials()
	assert.ErrorIs(t, err, ErrNotLoggedIn)
	require.NoError(t, DeleteCredentials(), "deleting twice is fine")
}

func TestLaunchStateRejectsPathTricks(t *testing.T) {
	tempDir(t)
	require.NoError(t, SaveLaunch(&LaunchState{LaunchID: "abc", LaunchToken: "t"}))
	got, err := LoadLaunch("abc")
	require.NoError(t, err)
	assert.Equal(t, "t", got.LaunchToken)
	for _, bad := range []string{"", "../x", "a/b", "a.b"} {
		assert.Error(t, SaveLaunch(&LaunchState{LaunchID: bad}), bad)
	}
}

func TestRepoLinks(t *testing.T) {
	tempDir(t)
	links, err := LoadRepoLinks()
	require.NoError(t, err)
	assert.Empty(t, links)
	links["gitlab.com/a/b"] = "/src/b"
	require.NoError(t, SaveRepoLinks(links))
	got, err := LoadRepoLinks()
	require.NoError(t, err)
	assert.Equal(t, "/src/b", got["gitlab.com/a/b"])
}
