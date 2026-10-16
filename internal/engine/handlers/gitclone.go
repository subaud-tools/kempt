package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(gitCloneHandlerImpl{}) }

// gitCloneHandlerImpl realises a pinned git checkout at ctx.Expand(To). It does
// not fetch or pull existing clones in this phase (update semantics land in 1c);
// it only verifies the clone exists and points at the expected origin.
type gitCloneHandlerImpl struct{}

func (gitCloneHandlerImpl) Kind() string { return "git-clone" }

func (gitCloneHandlerImpl) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.GitCloneStep)
	to := ctx.Expand(st.To)
	base := fmt.Sprintf("git-clone %s", to)

	gitDir := filepath.Join(to, ".git")
	if fi, err := os.Stat(gitDir); err == nil && fi.IsDir() {
		// Existing clone: confirm origin is the desired repo, over any transport.
		out, err := ctx.Runner.Run("git", "-C", to, "remote", "get-url", "origin")
		if err != nil {
			return engine.Delta{}, err
		}
		origin := strings.TrimSpace(out)
		if SameRemote(origin, st.Repo) {
			return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
		}
		return engine.Delta{Op: engine.OpBlocked, Detail: base + fmt.Sprintf(" (origin is %s)", origin)}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return engine.Delta{}, err
	}

	// No .git dir. Distinguish a missing target from an occupied one.
	if _, err := os.Stat(to); err == nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: base + " (exists, not a git repo)"}, nil
	} else if !os.IsNotExist(err) {
		return engine.Delta{}, err
	}

	ref := "default"
	if st.Ref != "" {
		ref = "ref " + st.Ref
	}
	return engine.Delta{
		Op:     engine.OpChange,
		Detail: base + fmt.Sprintf(" (git clone %s, %s)", st.Repo, ref),
	}, nil
}

func (gitCloneHandlerImpl) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.GitCloneStep)
	to := ctx.Expand(st.To)

	if _, err := ctx.Runner.Run("git", "clone", st.Repo, to); err != nil {
		return err
	}
	if st.Ref != "" {
		if _, err := ctx.Runner.Run("git", "-C", to, "checkout", st.Ref); err != nil {
			return err
		}
	}
	return nil
}
