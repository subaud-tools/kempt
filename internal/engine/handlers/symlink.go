// Package handlers holds the per-kind step handlers. Each file registers its
// handler in init(); import the package for its side effects to make the
// handlers available in the engine registry.
package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(symlinkHandler{}) }

// symlinkHandler realises the desired state: ctx.Expand(To) is a symlink
// pointing at ctx.Expand(From).
type symlinkHandler struct{}

func (symlinkHandler) Kind() string { return "symlink" }

func (symlinkHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.SymlinkStep)
	to := ctx.Expand(st.To)
	from := ctx.Expand(st.From)
	base := fmt.Sprintf("symlink %s -> %s", to, from)

	fi, err := os.Lstat(to)
	if err != nil {
		if os.IsNotExist(err) {
			return engine.Delta{Op: engine.OpChange, Detail: base + " (create)"}, nil
		}
		return engine.Delta{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		cur, err := os.Readlink(to)
		if err != nil {
			return engine.Delta{}, err
		}
		if cur == from {
			return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
		}
		return engine.Delta{Op: engine.OpChange, Detail: base + fmt.Sprintf(" (retarget from %s)", cur)}, nil
	}
	// A real file or directory occupies `to`.
	if st.Backup {
		return engine.Delta{Op: engine.OpChange, Detail: base + " (backup existing, then link)"}, nil
	}
	return engine.Delta{Op: engine.OpBlocked, Detail: base + " (real path exists, backup = false)"}, nil
}

func (symlinkHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.SymlinkStep)
	to := ctx.Expand(st.To)
	from := ctx.Expand(st.From)

	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}

	fi, err := os.Lstat(to)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		// Remove an existing (wrong-target) symlink before relinking.
		if err := os.Remove(to); err != nil {
			return err
		}
	case err == nil:
		// Back up the real file/dir; refuse to clobber an existing backup.
		bak := to + ".bak"
		if _, berr := os.Lstat(bak); berr == nil {
			return fmt.Errorf("backup target already exists: %s", bak)
		} else if !os.IsNotExist(berr) {
			return berr
		}
		if err := os.Rename(to, bak); err != nil {
			return err
		}
	case !os.IsNotExist(err):
		return err
	}

	return os.Symlink(from, to)
}
