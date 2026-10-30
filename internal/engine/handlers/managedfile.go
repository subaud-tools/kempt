package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	tools "github.com/subaud-tools/tools-common"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

func init() { engine.RegisterHandler(managedFileHandler{}) }

// managedFileHandler realises the desired state: ctx.Expand(To) holds the
// bytes BindSetup took from the verified setup bundle, with Mode. It writes a
// destination only when it is missing or holds what kempt last wrote there
// for the same tool (recorded under kempt's state directory); content already
// identical is adopted without a write. Anything else is shown blocked, never
// overwritten. It touches no other file, so a skill directory's other files
// stay as they are.
type managedFileHandler struct{}

func (managedFileHandler) Kind() string { return "managed-file" }

// managedAction is what converging one managed file takes.
type managedAction struct {
	delta  engine.Delta
	dest   string
	data   []byte
	mode   os.FileMode
	write  bool // replace the content
	chmod  bool // set the mode on content already in place
	record state.ManagedFile
}

func (managedFileHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	a, err := planManagedFile(ctx, s.(manifest.ManagedFileStep))
	return a.delta, err
}

func (managedFileHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	a, err := planManagedFile(ctx, s.(manifest.ManagedFileStep))
	switch {
	case err != nil:
		return err
	case a.delta.Op == engine.OpBlocked:
		return errors.New(a.delta.Detail)
	case a.delta.Op != engine.OpChange:
		return nil
	}
	if a.write {
		if err := tools.WriteFileAtomic(a.dest, a.data, a.mode); err != nil {
			return err
		}
		got, err := os.ReadFile(a.dest)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, a.data) {
			return fmt.Errorf("%s: content after write does not match the setup bundle", a.dest)
		}
	} else if a.chmod {
		if err := os.Chmod(a.dest, a.mode); err != nil {
			return err
		}
	}
	store, err := state.DefaultStore()
	if err != nil {
		return err
	}
	return store.RecordManagedFile(a.record)
}

// planManagedFile works out, read-only, what converging st takes.
func planManagedFile(ctx *machine.Context, st manifest.ManagedFileStep) (managedAction, error) {
	dest := ctx.Expand(st.To)
	a := managedAction{dest: dest}
	if st.Source == nil {
		a.delta = engine.Delta{Op: engine.OpBlocked, Detail: fmt.Sprintf("managed-file %s (no verified setup bundle supplies %s)", dest, st.From)}
		return a, nil
	}
	perm, err := strconv.ParseUint(st.Mode, 8, 32)
	if err != nil {
		return a, fmt.Errorf("managed-file %s: mode %q: %w", dest, st.Mode, err)
	}
	a.data, a.mode = st.Source.Data, os.FileMode(perm)
	a.record = state.ManagedFile{Tool: st.Source.Tool, Path: dest, Digest: digest(a.data)}
	say := func(op engine.Op, what string) (managedAction, error) {
		a.delta = engine.Delta{Op: op, Detail: fmt.Sprintf("managed-file %s (%s) from %s %s files/%s", dest, what, st.Source.Tool, st.Source.Version, st.From)}
		return a, nil
	}

	fi, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		a.write = true
		return say(engine.OpChange, "create, mode "+st.Mode)
	}
	if err != nil {
		return a, err
	}
	if !fi.Mode().IsRegular() {
		return say(engine.OpBlocked, "not a regular file: not kempt's to replace")
	}
	cur, err := os.ReadFile(dest)
	if err != nil {
		return a, err
	}
	store, err := state.DefaultStore()
	if err != nil {
		return a, err
	}
	rec, recorded, err := store.ManagedFile(dest)
	if err != nil {
		return a, err
	}
	if recorded && rec.Tool != st.Source.Tool {
		return say(engine.OpBlocked, fmt.Sprintf("kempt wrote it for %s: not %s's to replace", rec.Tool, st.Source.Tool))
	}
	owned := recorded && rec.Digest == digest(cur)
	sameMode := fi.Mode().Perm() == a.mode
	switch {
	case bytes.Equal(cur, a.data) && owned && sameMode:
		return say(engine.OpNoop, "mode "+st.Mode)
	case bytes.Equal(cur, a.data):
		a.chmod = !sameMode
		if owned {
			return say(engine.OpChange, fmt.Sprintf("mode %04o -> %s", fi.Mode().Perm(), st.Mode))
		}
		return say(engine.OpChange, "adopt identical content, mode "+st.Mode)
	case owned:
		a.write = true
		return say(engine.OpChange, "update content, mode "+st.Mode)
	case recorded:
		return say(engine.OpBlocked, "changed since kempt wrote it: not overwriting")
	}
	return say(engine.OpBlocked, "content is not kempt's: not overwriting")
}

// digest is the hex sha256 of b.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
