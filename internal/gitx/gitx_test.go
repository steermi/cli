package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.com:acme/app.git":                 "gitlab.com/acme/app",
		"https://gitlab.com/acme/app":                 "gitlab.com/acme/app",
		"https://oauth2:tok@gitlab.com/Acme/App.git/": "gitlab.com/acme/app",
		"ssh://git@gitlab.com:22/acme/app.git":        "gitlab.com/acme/app",
		"git@github.com:org/sub/group/repo.git":       "github.com/org/sub/group/repo",
		"":                                            "",
		"not a url":                                   "",
	} {
		assert.Equal(t, want, NormalizeRemote(in), in)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(dir, args...)
	require.NoError(t, err)
	return out
}

// repoWithOrigin creates a bare origin with main and feature branches plus a
// clone of it.
func repoWithOrigin(t *testing.T) (clone string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "clone")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "init", "-b", "main", seed)
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		git(t, seed, "config", kv[0], kv[1])
	}
	require.NoError(t, os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a"), 0o644))
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "init")
	git(t, seed, "remote", "add", "origin", origin)
	git(t, seed, "push", "origin", "main")
	git(t, seed, "checkout", "-b", "feat/STE-1-cloud")
	require.NoError(t, os.WriteFile(filepath.Join(seed, "cloud.txt"), []byte("from cloud"), 0o644))
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "cloud work")
	git(t, seed, "push", "origin", "feat/STE-1-cloud")
	git(t, root, "clone", origin, clone)
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		git(t, clone, "config", kv[0], kv[1])
	}
	return clone
}

func TestEnsureWorktreeNewBranchFromBase(t *testing.T) {
	clone := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt", "STE-2")
	require.NoError(t, EnsureWorktree(clone, wt, "feat/STE-2-new", "main"))
	branch, err := CurrentBranch(wt)
	require.NoError(t, err)
	assert.Equal(t, "feat/STE-2-new", branch)
	assert.NoFileExists(t, filepath.Join(wt, "cloud.txt"))
	require.NoError(t, EnsureWorktree(clone, wt, "feat/STE-2-new", "main"), "existing worktree is reused")
	assert.Equal(t, "main", DefaultBranch(clone))
}

func TestEnsureWorktreeContinuesRemoteBranch(t *testing.T) {
	clone := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "STE-1")
	require.NoError(t, EnsureWorktree(clone, wt, "feat/STE-1-cloud", "main"))
	assert.FileExists(t, filepath.Join(wt, "cloud.txt"), "a branch the cloud run pushed is continued, not recreated")
}

func TestExcludeSteermiIsIdempotent(t *testing.T) {
	clone := repoWithOrigin(t)
	require.NoError(t, ExcludeSteermi(clone))
	require.NoError(t, ExcludeSteermi(clone))
	data, err := os.ReadFile(filepath.Join(clone, ".git", "info", "exclude"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), ".steermi/"))

	require.NoError(t, os.MkdirAll(filepath.Join(clone, ".steermi"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clone, ".steermi", "TASK.md"), []byte("x"), 0o644))
	dirty, err := Dirty(clone)
	require.NoError(t, err)
	assert.False(t, dirty, ".steermi/ never shows up as a change")
}

func TestCommitsSinceAndPush(t *testing.T) {
	clone := repoWithOrigin(t)
	require.NoError(t, CheckoutBranch(clone, "feat/STE-3"))
	require.NoError(t, os.WriteFile(filepath.Join(clone, "b.txt"), []byte("b"), 0o644))
	dirty, err := Dirty(clone)
	require.NoError(t, err)
	assert.True(t, dirty)
	require.NoError(t, CommitAll(clone, "feat: b (STE-3)"))
	commits, err := CommitsSince(clone, "main")
	require.NoError(t, err)
	assert.Len(t, commits, 1)
	require.NoError(t, Push(clone, "feat/STE-3"))
	remote := git(t, clone, "ls-remote", "--heads", "origin", "feat/STE-3")
	assert.Contains(t, remote, "refs/heads/feat/STE-3")
}

func TestPinnedWorktreeRejectsMissingCommitInsteadOfFallingBackToHEAD(t *testing.T) {
	clone := repoWithOrigin(t)
	require.Error(t, EnsureWorktree(clone, filepath.Join(t.TempDir(), "pinned"), "feat/pinned", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
}

func TestExistingWorktreeCannotSilentlyUseAnotherBranch(t *testing.T) {
	clone := repoWithOrigin(t)
	path := filepath.Join(t.TempDir(), "work")
	require.NoError(t, EnsureWorktree(clone, path, "feat/first", "main"))
	require.Error(t, EnsureWorktree(clone, path, "feat/second", "main"))
	current, e := CurrentBranch(path)
	require.NoError(t, e)
	require.Equal(t, "feat/first", current)
}
