package handlers

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(tomlMergeHandler{}) }

// tomlMergeHandler realises the desired state: ctx.Expand(File) is TOML that is
// a deep superset of Merge. Maps recurse, arrays gain missing elements, scalars
// overwrite. It mirrors jsonMergeHandler's default (append) semantics but reads
// and writes TOML via BurntSushi/toml.
type tomlMergeHandler struct{}

func (tomlMergeHandler) Kind() string { return "toml-merge" }

func (tomlMergeHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.TomlMergeStep)
	file := ctx.Expand(st.File)
	base := "toml-merge " + file

	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return engine.Delta{Op: engine.OpChange, Detail: base + " (create file)"}, nil
		}
		return engine.Delta{}, err
	}

	var current any
	if _, err := toml.DecodeFile(file, &current); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: base + " (existing file is not valid TOML)"}, nil //nolint:nilerr // an unparseable target is a blocked step, not an error
	}

	desired := jsonutil.ExpandHome(toAnyTOML(st.Merge), ctx.Home)
	if isSubset(desired, current, false) {
		return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
	}

	keys := mergeKeys(desired.(map[string]any), current, false)
	return engine.Delta{Op: engine.OpChange, Detail: base + fmt.Sprintf(" (merge keys: %s)", strings.Join(keys, ", "))}, nil
}

func (tomlMergeHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.TomlMergeStep)
	file := ctx.Expand(st.File)

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}

	var current any
	if _, err := os.Stat(file); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		current = map[string]any{}
	} else if _, err := toml.DecodeFile(file, &current); err != nil {
		return fmt.Errorf("existing file is not valid TOML: %s", file)
	}

	merged := merge(jsonutil.ExpandHome(toAnyTOML(st.Merge), ctx.Home), current, false)
	m, ok := merged.(map[string]any)
	if !ok {
		return fmt.Errorf("merge result is not a table: %s", file)
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	return os.WriteFile(file, buf.Bytes(), 0o644)
}

// toAnyTOML round-trips a map through TOML so its values (which may come from a
// manifest or a test) share the same dynamic types as the current state decoded
// from disk (int64, etc.), making deep comparison reliable. On any failure it
// returns the input unchanged.
func toAnyTOML(m map[string]any) any {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return m
	}
	var v map[string]any
	if _, err := toml.Decode(buf.String(), &v); err != nil {
		return m
	}
	return any(v)
}
