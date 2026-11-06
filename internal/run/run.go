package run

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// Runner abstracts command execution and binary lookup.
type Runner interface {
	Run(name string, args ...string) (stdout string, err error) // err wraps stderr text on failure
	LookPath(name string) (string, error)
}

// RealRunner is an os/exec-backed Runner.
type RealRunner struct{}

// Run executes the named command with the given args. It captures stdout and
// returns it on success. On non-zero exit it returns an error of the form
// "<name>: <exitError>: <stderr>".
//
// stdout goes to a temporary file, not a pipe: Node writes to a pipe
// asynchronously on macOS, so a CLI that calls process.exit() right after
// printing (pi's package commands) drops the rest of its output and still
// exits 0. Writes to a file are synchronous.
func (r RealRunner) Run(name string, args ...string) (string, error) {
	f, err := os.CreateTemp("", "kempt-stdout-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	var stderrBuf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = f
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, stderrBuf.String())
	}
	out, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// LookPath reports the path of the named binary using exec.LookPath.
func (r RealRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}
