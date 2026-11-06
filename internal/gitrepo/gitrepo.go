// Package gitrepo provides thin wrappers around git commands via a run.Runner.
package gitrepo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/subaud-tools/kempt/internal/run"
)

// Fetch runs `git -C <dir> fetch --quiet`.
func Fetch(r run.Runner, dir string) error {
	_, err := r.Run("git", "-C", dir, "fetch", "--quiet")
	return err
}

// Behind runs `git -C <dir> rev-list --count HEAD..@{u}` and parses the
// trimmed stdout as an integer. Non-numeric output returns an error.
func Behind(r run.Runner, dir string) (int, error) {
	out, err := r.Run("git", "-C", dir, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("gitrepo: Behind: unexpected output %q: %w", out, err)
	}
	return n, nil
}

// Pull runs `git -C <dir> pull --rebase --autostash`.
func Pull(r run.Runner, dir string) error {
	_, err := r.Run("git", "-C", dir, "pull", "--rebase", "--autostash")
	return err
}

// ErrNotFastForward is wrapped by FastForward when the checkout cannot be
// brought up to its upstream without losing or rewriting local work.
var ErrNotFastForward = errors.New("cannot fast-forward")

// FastForward brings dir's branch up to its upstream (`git merge --ff-only
// --no-overwrite-ignore @{u}`, after a Fetch). It changes nothing and returns an error wrapping
// ErrNotFastForward when the work tree has uncommitted changes or the branch
// has diverged from its upstream.
func FastForward(r run.Runner, dir string) error {
	status, err := r.Run("git", "-C", dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("%w: it has uncommitted changes", ErrNotFastForward)
	}
	behind, err := Behind(r, dir)
	if err != nil || behind == 0 {
		return err
	}
	out, err := r.Run("git", "-C", dir, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return err
	}
	if ahead := strings.TrimSpace(out); ahead != "0" {
		return fmt.Errorf("%w: it has diverged from its upstream (%s local, %d upstream commits)", ErrNotFastForward, ahead, behind)
	}
	// --no-overwrite-ignore: a file the remote starts tracking must not
	// replace a local file this clone ignores (a secret, a local override).
	_, err = r.Run("git", "-C", dir, "merge", "--ff-only", "--no-overwrite-ignore", "--quiet", "@{u}")
	return err
}

// Clone runs `git clone <url> <dir>`.
func Clone(r run.Runner, url, dir string) error {
	_, err := r.Run("git", "clone", url, dir)
	return err
}

// RemoteURL runs `git -C <dir> remote get-url origin` and returns the trimmed
// stdout.
func RemoteURL(r run.Runner, dir string) (string, error) {
	out, err := r.Run("git", "-C", dir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
