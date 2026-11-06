package run

import (
	"strings"
	"testing"
)

// A program that exits straight after printing (pi's package commands call
// process.exit) loses output still queued for a pipe on macOS. Run hands the
// command a file for stdout, so nothing can be left queued.
func TestRealRunnerStdoutIsAFile(t *testing.T) {
	out, err := RealRunner{}.Run("sh", "-c", "if [ -f /dev/stdout ]; then echo file; else echo pipe; fi")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "file" {
		t.Fatalf("stdout is a %s, want a file", got)
	}
}

func TestRealRunnerFailureWrapsStderr(t *testing.T) {
	_, err := RealRunner{}.Run("sh", "-c", "echo partial; echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to carry stderr", err)
	}
}
