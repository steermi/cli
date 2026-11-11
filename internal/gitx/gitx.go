// Package gitx wraps the git operations the steermi CLI needs. It shells out
// to the user's git so their credentials, signing and hooks apply.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// NormalizeRemote turns any git remote URL into "host/owner/repo" (lower
// case, no scheme, credentials, port or .git suffix) so remotes can be
// compared with the clone URLs Steermi stores.
func NormalizeRemote(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" {
		return ""
	}
	var host, p string
	if strings.Contains(r, "://") {
		u, err := url.Parse(r)
		if err != nil {
			return ""
		}
		host, p = u.Hostname(), u.Path
	} else if m := scpLike.FindStringSubmatch(r); m != nil {
		host, p = m[1], m[2]
	} else {
		return ""
	}
	p = strings.Trim(strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git"), "/")
	if host == "" || p == "" {
		return ""
	}
	return strings.ToLower(host + "/" + p)
}

// Toplevel returns the repository root containing dir.
func Toplevel(dir string) (string, error) { return Run(dir, "rev-parse", "--show-toplevel") }

// RemoteURL returns a remote's configured URL. It reads remote.<name>.url
// rather than `git remote get-url`, which applies url.*.insteadOf rewrites
// and would hide the address Steermi knows the repository by.
func RemoteURL(dir, remote string) (string, error) {
	return Run(dir, "config", "--get", "remote."+remote+".url")
}

// CurrentBranch returns the checked-out branch name.
func CurrentBranch(dir string) (string, error) { return Run(dir, "rev-parse", "--abbrev-ref", "HEAD") }

// HeadSHA returns the current commit.
func HeadSHA(dir string) (string, error) { return Run(dir, "rev-parse", "HEAD") }

// DefaultBranch returns origin's default branch, falling back to main.
func DefaultBranch(dir string) string {
	if ref, err := Run(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(ref, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := Run(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b); err == nil {
			return b
		}
	}
	return "main"
}

func refExists(dir, ref string) bool {
	_, err := Run(dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// EnsureWorktree makes path a worktree of repoDir on branch. An existing
// worktree at path is reused. A new branch starts from origin/<branch> when
// the remote has it (continuing work started elsewhere), otherwise from
// origin/<base>.
func EnsureWorktree(repoDir, path, branch, base string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		current, e := CurrentBranch(path)
		if e != nil {
			return e
		}
		if current != branch {
			return fmt.Errorf("worktree already uses a different branch")
		}
		if len(base) == 40 || len(base) == 64 {
			if _, e := Run(path, "merge-base", "--is-ancestor", base, "HEAD"); e != nil {
				return fmt.Errorf("worktree does not contain pinned input revision")
			}
		}
		return nil
	}
	_, _ = Run(repoDir, "fetch", "--quiet", "origin")
	if len(base) == 40 || len(base) == 64 {
		if !refExists(repoDir, base+"^{commit}") {
			if _, e := Run(repoDir, "fetch", "--quiet", "origin", base); e != nil {
				return e
			}
		}
		if refExists(repoDir, "refs/heads/"+branch) {
			if _, e := Run(repoDir, "merge-base", "--is-ancestor", base, branch); e != nil {
				return fmt.Errorf("existing branch does not contain pinned input revision")
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	switch {
	case refExists(repoDir, "refs/heads/"+branch):
		_, err := Run(repoDir, "worktree", "add", path, branch)
		return err
	case refExists(repoDir, "refs/remotes/origin/"+branch):
		_, err := Run(repoDir, "worktree", "add", "--track", "-b", branch, path, "origin/"+branch)
		return err
	}
	if base == "" {
		base = DefaultBranch(repoDir)
	}
	start := "origin/" + base
	if !refExists(repoDir, "refs/remotes/"+start) {
		start = base
		if !refExists(repoDir, start) {
			if len(base) == 40 || len(base) == 64 {
				if _, err := Run(repoDir, "fetch", "--quiet", "origin", base); err != nil {
					return fmt.Errorf("pinned revision unavailable: %w", err)
				}
				if !refExists(repoDir, base+"^{commit}") {
					return fmt.Errorf("pinned revision unavailable")
				}
			} else {
				start = "HEAD"
			}
		}
	}
	_, err := Run(repoDir, "worktree", "add", "--no-track", "-b", branch, path, start)
	return err
}

// CheckoutBranch switches dir to branch, creating it from HEAD if needed.
func CheckoutBranch(dir, branch string) error {
	if cur, err := CurrentBranch(dir); err == nil && cur == branch {
		return nil
	}
	if refExists(dir, "refs/heads/"+branch) {
		_, err := Run(dir, "checkout", branch)
		return err
	}
	_, err := Run(dir, "checkout", "-b", branch)
	return err
}

// ExcludeSteermi keeps .steermi/ out of commits via info/exclude (shared by
// all worktrees), without touching the project's .gitignore.
func ExcludeSteermi(dir string) error {
	common, err := Run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	exclude := filepath.Join(common, "info", "exclude")
	data, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ".steermi/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + "# steermi local launch state\n.steermi/\n")
	return err
}

// Dirty reports uncommitted changes.
func Dirty(dir string) (bool, error) {
	out, err := Run(dir, "status", "--porcelain")
	return out != "", err
}

// CommitAll commits every change with message.
func CommitAll(dir, message string) error {
	if _, err := Run(dir, "add", "-A"); err != nil {
		return err
	}
	_, err := Run(dir, "commit", "-m", message)
	return err
}

// Push pushes branch to origin and sets upstream.
func Push(dir, branch string) error {
	_, err := Run(dir, "push", "-u", "origin", branch)
	return err
}

// CommitsSince lists commit SHAs on HEAD that are not on origin/<base>.
func CommitsSince(dir, base string) ([]string, error) {
	start := "origin/" + base
	if !refExists(dir, "refs/remotes/"+start) {
		start = base
	}
	out, err := Run(dir, "rev-list", start+"..HEAD")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// CheckoutPinnedBranch creates a branch at the verified input revision. Existing
// branches may resume only when they descend from that revision.
func CheckoutPinnedBranch(dir, branch, pin string) error {
	if _, err := Run(dir, "rev-parse", "--verify", pin+"^{commit}"); err != nil {
		if _, e := Run(dir, "fetch", "--quiet", "origin", pin); e != nil {
			return e
		}
	}
	if refExists(dir, "refs/heads/"+branch) {
		if _, e := Run(dir, "merge-base", "--is-ancestor", pin, branch); e != nil {
			return fmt.Errorf("existing branch does not contain pinned input")
		}
		_, err := Run(dir, "checkout", branch)
		return err
	}
	_, err := Run(dir, "checkout", "-b", branch, pin)
	return err
}
