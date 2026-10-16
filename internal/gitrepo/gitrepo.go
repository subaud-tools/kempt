// Package gitrepo provides thin wrappers around git commands via a run.Runner.
package gitrepo

import (
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
