package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(jsonMergeHandler{}) }

// jsonMergeHandler realises the desired state: ctx.Expand(File) is JSON that is
// a deep superset of Merge. Maps recurse, arrays gain missing elements, scalars
// overwrite. Remove paths are deleted before the merge, so a path that Merge
// sets again is replaced rather than left alone.
type jsonMergeHandler struct{}

func (jsonMergeHandler) Kind() string { return "json-merge" }

func (jsonMergeHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.JSONMergeStep)
	file := ctx.Expand(st.File)
	base := "json-merge " + file

	b, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return engine.Delta{Op: engine.OpChange, Detail: base + " (create file)"}, nil
		}
		return engine.Delta{}, err
	}

	var current any
	if err := json.Unmarshal(b, &current); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: base + " (existing file is not valid JSON)"}, nil //nolint:nilerr // an unparseable target is a blocked step, not an error
	}

	desired := desiredDoc(ctx, st)
	replace := st.Arrays == "replace"
	pruned := jsonutil.RemovePaths(current, st.Remove)
	next := merge(desired, pruned, replace)
	if len(st.Remove) == 0 && isSubset(desired, current, replace) || len(st.Remove) > 0 && reflect.DeepEqual(next, current) {
		return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
	}

	var parts []string
	var gone []string
	for _, r := range st.Remove {
		path := strings.Split(r, ".")
		if jsonutil.HasPath(current, path) && !jsonutil.HasPath(next, path) {
			gone = append(gone, r)
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		parts = append(parts, "remove: "+strings.Join(gone, ", "))
	}
	if keys := mergeKeys(desired.(map[string]any), pruned, replace); len(keys) > 0 {
		parts = append(parts, "merge keys: "+strings.Join(keys, ", "))
	}
	return engine.Delta{Op: engine.OpChange, Detail: base + " (" + strings.Join(parts, "; ") + ")"}, nil
}

// desiredDoc is the step's merge document with ${HOME} expanded; a step that
// only removes merges an empty object.
func desiredDoc(ctx *machine.Context, st manifest.JSONMergeStep) any {
	m := st.Merge
	if m == nil {
		m = map[string]any{}
	}
	return jsonutil.ExpandHome(jsonutil.ToAny(m), ctx.Home)
}

func (jsonMergeHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.JSONMergeStep)
	file := ctx.Expand(st.File)

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}

	var current any
	b, err := os.ReadFile(file)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		current = map[string]any{}
	} else if err := json.Unmarshal(b, &current); err != nil {
		return fmt.Errorf("existing file is not valid JSON: %s", file)
	}

	merged := merge(desiredDoc(ctx, st), jsonutil.RemovePaths(current, st.Remove), st.Arrays == "replace")
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(out, '\n'), 0o644)
}

// mergeKeys returns the sorted top-level desired keys whose values are not
// already a deep subset of current.
func mergeKeys(desired map[string]any, current any, replaceArrays bool) []string {
	curMap, _ := current.(map[string]any)
	var keys []string
	for k, dv := range desired {
		cv, ok := curMap[k]
		if !ok || !isSubset(dv, cv, replaceArrays) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// isSubset reports whether desired is deeply contained in current: maps recurse
// per key, scalars must be equal. Array handling depends on replaceArrays: when
// false (append mode) every desired element must be present (deep equality);
// when true (replace mode) the desired array must deep-equal current exactly, so
// any extra, reordered, or differing element drives a change.
func isSubset(desired, current any, replaceArrays bool) bool {
	switch d := desired.(type) {
	case map[string]any:
		c, ok := current.(map[string]any)
		if !ok {
			return false
		}
		for k, dv := range d {
			cv, ok := c[k]
			if !ok || !isSubset(dv, cv, replaceArrays) {
				return false
			}
		}
		return true
	case []any:
		c, ok := current.([]any)
		if !ok {
			return false
		}
		if replaceArrays {
			return reflect.DeepEqual(d, c)
		}
		for _, de := range d {
			if !containsElem(c, de) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(desired, current)
	}
}

// merge returns the deep merge of desired into current: maps recurse, scalars
// and mismatched types take the desired value. Array handling depends on
// replaceArrays: when false (append mode) desired elements missing from current
// are appended (order preserved); when true (replace mode) the desired array
// replaces the current array wholesale.
func merge(desired, current any, replaceArrays bool) any {
	switch d := desired.(type) {
	case map[string]any:
		c, ok := current.(map[string]any)
		if !ok {
			c = map[string]any{}
		}
		out := map[string]any{}
		for k, v := range c {
			out[k] = v
		}
		for k, dv := range d {
			if cv, ok := out[k]; ok {
				out[k] = merge(dv, cv, replaceArrays)
			} else {
				out[k] = dv
			}
		}
		return out
	case []any:
		if replaceArrays {
			return append([]any{}, d...)
		}
		c, ok := current.([]any)
		if !ok {
			return append([]any{}, d...)
		}
		out := append([]any{}, c...)
		for _, de := range d {
			if !containsElem(out, de) {
				out = append(out, de)
			}
		}
		return out
	default:
		return desired
	}
}

func containsElem(list []any, elem any) bool {
	for _, e := range list {
		if reflect.DeepEqual(e, elem) {
			return true
		}
	}
	return false
}
