package gitrepo_test

import (
	"errors"
	"testing"

	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/run"
)

func newFake(responses map[string]run.Response) *run.FakeRunner {
	return &run.FakeRunner{Responses: responses}
}

func TestFetch(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /repo fetch --quiet": {Stdout: ""},
	})
	if err := gitrepo.Fetch(f, "/repo"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(f.Calls) != 1 || f.Calls[0] != "git -C /repo fetch --quiet" {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestFetchPropagatesError(t *testing.T) {
	want := errors.New("network")
	f := newFake(map[string]run.Response{
		"git -C /repo fetch --quiet": {Err: want},
	})
	if err := gitrepo.Fetch(f, "/repo"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestBehind(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /r rev-list --count HEAD..@{u}": {Stdout: "3\n"},
	})
	got, err := gitrepo.Behind(f, "/r")
	if err != nil {
		t.Fatalf("Behind: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
	if len(f.Calls) != 1 || f.Calls[0] != "git -C /r rev-list --count HEAD..@{u}" {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestBehindZero(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /r rev-list --count HEAD..@{u}": {Stdout: "0\n"},
	})
	got, err := gitrepo.Behind(f, "/r")
	if err != nil {
		t.Fatalf("Behind: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestBehindNonNumericError(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /r rev-list --count HEAD..@{u}": {Stdout: "not-a-number\n"},
	})
	_, err := gitrepo.Behind(f, "/r")
	if err == nil {
		t.Fatal("expected error for non-numeric stdout")
	}
}

func TestBehindPropagatesRunError(t *testing.T) {
	want := errors.New("fatal")
	f := newFake(map[string]run.Response{
		"git -C /r rev-list --count HEAD..@{u}": {Err: want},
	})
	_, err := gitrepo.Behind(f, "/r")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestPull(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /repo pull --rebase --autostash": {Stdout: ""},
	})
	if err := gitrepo.Pull(f, "/repo"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(f.Calls) != 1 || f.Calls[0] != "git -C /repo pull --rebase --autostash" {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestPullPropagatesError(t *testing.T) {
	want := errors.New("conflict")
	f := newFake(map[string]run.Response{
		"git -C /repo pull --rebase --autostash": {Err: want},
	})
	if err := gitrepo.Pull(f, "/repo"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestClone(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git clone https://example.com/r /dest": {Stdout: ""},
	})
	if err := gitrepo.Clone(f, "https://example.com/r", "/dest"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if len(f.Calls) != 1 || f.Calls[0] != "git clone https://example.com/r /dest" {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestClonePropagatesError(t *testing.T) {
	want := errors.New("auth")
	f := newFake(map[string]run.Response{
		"git clone https://example.com/r /dest": {Err: want},
	})
	if err := gitrepo.Clone(f, "https://example.com/r", "/dest"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestRemoteURL(t *testing.T) {
	f := newFake(map[string]run.Response{
		"git -C /repo remote get-url origin": {Stdout: "https://github.com/x/y.git\n"},
	})
	got, err := gitrepo.RemoteURL(f, "/repo")
	if err != nil {
		t.Fatalf("RemoteURL: %v", err)
	}
	if got != "https://github.com/x/y.git" {
		t.Fatalf("got %q, want trimmed URL", got)
	}
	if len(f.Calls) != 1 || f.Calls[0] != "git -C /repo remote get-url origin" {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestRemoteURLPropagatesError(t *testing.T) {
	want := errors.New("no remote")
	f := newFake(map[string]run.Response{
		"git -C /repo remote get-url origin": {Err: want},
	})
	_, err := gitrepo.RemoteURL(f, "/repo")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
