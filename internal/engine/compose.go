package engine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// composed is the selection after layer composition, plus what the plan should
// say about it: the contributors and overrides of each folded merge step, keyed
// by package index then step index.
type composed struct {
	pkgs      []*manifest.Package
	notes     map[int]map[int]string
	overrides map[int][]string // package index -> pin overrides it made
}

// Compose returns the selected packages as BuildPlan converges them after the
// cross-layer rules (see compose), for readers that inspect steps without
// planning, such as doctor. Composing an already composed selection changes
// nothing.
func Compose(ctx *machine.Context, pkgs []*manifest.Package) ([]*manifest.Package, error) {
	c, err := compose(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	return c.pkgs, nil
}

// layerLabel names a package's layer in plan output.
func layerLabel(p *manifest.Package) string {
	if p.Layer == "" {
		return "base"
	}
	return p.Layer
}

// compose applies the cross-layer rules of the layers spec to the selected
// packages, in order: a later layer's pin of a pi/npm entry replaces an
// earlier layer's; two layers claiming one exclusive target is an error unless
// the definitions are identical (one copy is kept); json-merge and toml-merge
// steps into one file from two or more layers fold into one step. Packages from
// a single layer are returned unchanged, so a base-only selection plans
// exactly as it did before layers. The input packages are never mutated.
func compose(ctx *machine.Context, in []*manifest.Package) (*composed, error) {
	pkgs := make([]*manifest.Package, len(in))
	for i, p := range in {
		c := *p
		c.Steps = append([]manifest.Step(nil), p.Steps...)
		pkgs[i] = &c
	}
	c := &composed{pkgs: pkgs, notes: map[int]map[int]string{}, overrides: map[int][]string{}}
	if !multiLayer(pkgs) {
		return c, nil
	}
	c.overridePins()
	if err := resolveExclusive(ctx, pkgs); err != nil {
		return nil, err
	}
	c.foldMerges(ctx)
	return c, nil
}

func multiLayer(pkgs []*manifest.Package) bool {
	for _, p := range pkgs {
		if p.Layer != pkgs[0].Layer {
			return true
		}
	}
	return false
}

// active reports whether step s of package p applies on this machine: a step
// its only-clause skips takes no part in composition and renders as skipped.
func active(ctx *machine.Context, p *manifest.Package, s manifest.Step) bool {
	if _, skip := skipReason(ctx, p.Only); skip {
		return false
	}
	_, skip := skipReason(ctx, stepOnly(s))
	return !skip
}

// ---- pins ------------------------------------------------------------------

type pinRef struct {
	pkg, step int
	pi        bool
	entry     string
}

// overridePins drops an earlier layer's pi/npm install entry when a later layer
// lists the same package (by SpecIdentity) with a different spec, and notes the
// override on the later package.
func (c *composed) overridePins() {
	pkgs := c.pkgs
	seen := map[string]pinRef{}
	for pi, p := range pkgs {
		for si, s := range p.Steps {
			is, ok := s.(manifest.InstallStep)
			if !ok {
				continue
			}
			visit := func(entries []string, isPi bool) {
				for _, e := range entries {
					key := fmt.Sprintf("%v|%s", isPi, jsonutil.SpecIdentity(e))
					prev, ok := seen[key]
					if ok && pkgs[prev.pkg].Layer != p.Layer && prev.entry != e {
						dropEntry(pkgs[prev.pkg], prev.step, prev.pi, prev.entry)
						c.overrides[pi] = append(c.overrides[pi], fmt.Sprintf("%s (%s overrides %s: %s)", e, layerLabel(p), layerLabel(pkgs[prev.pkg]), prev.entry))
					}
					seen[key] = pinRef{pi, si, isPi, e}
				}
			}
			visit(is.Pi, true)
			visit(is.Npm, false)
		}
	}
}

func dropEntry(p *manifest.Package, step int, pi bool, entry string) {
	is := p.Steps[step].(manifest.InstallStep)
	list := &is.Npm
	if pi {
		list = &is.Pi
	}
	var kept []string
	for _, e := range *list {
		if e != entry {
			kept = append(kept, e)
		}
	}
	*list = kept
	p.Steps[step] = is
}

// ---- exclusive targets -----------------------------------------------------

// exclusiveKey names the one thing a step owns outright, and a comparable form
// of the step for the identical-definition check; ok is false for steps that
// own nothing exclusively.
func exclusiveKey(ctx *machine.Context, s manifest.Step) (key string, norm any, ok bool) {
	switch st := s.(type) {
	case manifest.SymlinkStep:
		// The source resolves against the declaring layer's root, so two layers
		// naming the same relative from are different links.
		st.From = ctx.Expand(st.From)
		return "symlink " + st.To, st, true
	case manifest.GitCloneStep:
		return "git-clone " + st.To, st, true
	case manifest.DownloadStep:
		bin := st.Bin
		if bin == "" {
			bin = st.Tool
		}
		return "binary " + bin, st, true
	case manifest.GithubReleaseStep:
		return "binary " + st.Bin, st, true
	case manifest.ServiceStep:
		return "service " + st.Label, st, true
	}
	return "", nil, false
}

type claim struct {
	pkg  int
	norm any
}

// resolveExclusive rejects two layers claiming one exclusive target with
// different definitions, and drops the later copy of an identical one. Claims
// within one layer are left alone, as before layers.
func resolveExclusive(ctx *machine.Context, pkgs []*manifest.Package) error {
	claims := map[string]claim{}
	for pi, p := range pkgs {
		pctx := ContextFor(ctx, p.Root)
		var kept []manifest.Step
		for _, s := range p.Steps {
			key, norm, ok := exclusiveKey(pctx, s)
			if !ok || !active(ctx, p, s) {
				kept = append(kept, s)
				continue
			}
			prev, seen := claims[key]
			if !seen || pkgs[prev.pkg].Layer == p.Layer {
				claims[key] = claim{pi, norm}
				kept = append(kept, s)
				continue
			}
			if !reflect.DeepEqual(prev.norm, norm) {
				q := pkgs[prev.pkg]
				return fmt.Errorf("conflict: %s is declared by %s (%s) and %s (%s)", key, layerLabel(q), q.Name, layerLabel(p), p.Name)
			}
		}
		p.Steps = kept
	}
	return nil
}

// ---- merge folding ---------------------------------------------------------

type mergeRef struct{ pkg, step int }

type mergeGroup struct {
	kind, file, mode string
	refs             []mergeRef
}

// foldMerges folds, for every file that merges from two or more layers, its
// json-merge steps (one fold per arrays mode; remove paths per pruneRemoved
// and placeRemoves) and its toml-merge steps into a single step each, at the
// first contributor's position. When such a file has
// both an append and a replace fold, an array the replace fold owns absorbs the
// append fold's elements for it (so a layer appending to a base's replace list
// keeps its entry and converging is idempotent), and when anything is left in
// the append fold the replace fold moves after it. Files one layer merges alone keep
// their steps exactly as written.
func (c *composed) foldMerges(ctx *machine.Context) {
	pkgs := c.pkgs
	groups := map[string]*mergeGroup{}
	var order []string
	for pi, p := range pkgs {
		pctx := ContextFor(ctx, p.Root)
		for si, s := range p.Steps {
			var kind, file, mode string
			switch st := s.(type) {
			case manifest.JSONMergeStep:
				kind, file, mode = "json-merge", pctx.Expand(st.File), st.Arrays
				if mode == "" {
					mode = "append"
				}
			case manifest.TomlMergeStep:
				kind, file = "toml-merge", pctx.Expand(st.File)
			default:
				continue
			}
			if !active(ctx, p, s) {
				continue
			}
			key := kind + "|" + file + "|" + mode
			g, ok := groups[key]
			if !ok {
				g = &mergeGroup{kind: kind, file: file, mode: mode}
				groups[key] = g
				order = append(order, key)
			}
			g.refs = append(g.refs, mergeRef{pi, si})
		}
	}

	fileLayers := map[string]map[string]bool{}
	for _, g := range groups {
		if fileLayers[g.file] == nil {
			fileLayers[g.file] = map[string]bool{}
		}
		for _, r := range g.refs {
			fileLayers[g.file][pkgs[r.pkg].Layer] = true
		}
	}

	fileRemoves := c.pruneRemoved(groups, order, fileLayers)

	remove := map[mergeRef]bool{}
	replaceAt := map[string]mergeRef{} // file -> the replace fold's position
	appendAt := map[string]mergeRef{}  // file -> the append fold's position
	contributorsAt := map[mergeRef][]string{}
	for _, key := range order {
		g := groups[key]
		if len(fileLayers[g.file]) < 2 {
			continue
		}
		if g.mode == "append" && g.kind == "json-merge" {
			appendAt[g.file] = g.refs[0]
		}
		merged, contributors, overrides := foldGroup(pkgs, g)
		first := g.refs[0]
		switch g.kind {
		case "json-merge":
			arrays := g.mode
			if arrays == "append" {
				arrays = ""
			}
			pkgs[first.pkg].Steps[first.step] = manifest.JSONMergeStep{File: g.file, Merge: merged, Arrays: arrays}
			if g.mode == "replace" {
				replaceAt[g.file] = first
			}
		case "toml-merge":
			pkgs[first.pkg].Steps[first.step] = manifest.TomlMergeStep{File: g.file, Merge: merged}
		}
		contributorsAt[first] = contributors
		if len(overrides) > 0 {
			c.setNote(first, "; "+strings.Join(overrides, "; "))
		}
		for _, r := range g.refs[1:] {
			remove[r] = true
		}
	}

	// An array the replace fold owns absorbs the append fold's elements for it.
	for file, rAt := range replaceAt {
		aAt, ok := appendAt[file]
		if !ok {
			continue
		}
		rs := pkgs[rAt.pkg].Steps[rAt.step].(manifest.JSONMergeStep)
		as := pkgs[aAt.pkg].Steps[aAt.step].(manifest.JSONMergeStep)
		rest, owned, moved := absorb(deepCopy(as.Merge), deepCopy(rs.Merge))
		if !moved {
			continue
		}
		rs.Merge = owned
		pkgs[rAt.pkg].Steps[rAt.step] = rs
		contributorsAt[rAt] = unionStrings(contributorsAt[rAt], contributorsAt[aAt])
		if len(rest) == 0 {
			remove[aAt] = true
			delete(appendAt, file)
		} else {
			as.Merge = rest
			pkgs[aAt.pkg].Steps[aAt.step] = as
		}
	}
	for file, paths := range fileRemoves {
		placeRemoves(pkgs, file, paths, appendAt, replaceAt)
	}
	for at, contributors := range contributorsAt {
		if len(contributors) < 2 {
			continue
		}
		c.setNote(at, "(from "+strings.Join(contributors, ", ")+")"+c.notes[at.pkg][at.step])
	}

	// A replace fold must apply after the file's append fold, which now holds
	// every append contribution at the first contributor's position.
	move := map[mergeRef]mergeRef{} // replace fold -> insert after
	for file, at := range replaceAt {
		if after, ok := appendAt[file]; ok && less(at, after) {
			move[at] = after
		}
	}
	c.rebuild(remove, move)
}

// pruneRemoved applies, for every json-merge file folded from two or more
// layers, each contributor's remove paths to the merge documents of the
// contributors before it (in either arrays mode), so a later layer's removal
// wins over an earlier layer's merge. It returns each such file's remove paths
// in contributor order; the folded steps carry none until placeRemoves.
func (c *composed) pruneRemoved(groups map[string]*mergeGroup, order []string, fileLayers map[string]map[string]bool) map[string][]string {
	byFile := map[string][]mergeRef{}
	for _, key := range order {
		g := groups[key]
		if g.kind == "json-merge" && len(fileLayers[g.file]) >= 2 {
			byFile[g.file] = append(byFile[g.file], g.refs...)
		}
	}
	out := map[string][]string{}
	for file, refs := range byFile {
		sort.Slice(refs, func(i, j int) bool { return less(refs[i], refs[j]) })
		for j, rj := range refs {
			removes := c.pkgs[rj.pkg].Steps[rj.step].(manifest.JSONMergeStep).Remove
			if len(removes) == 0 {
				continue
			}
			out[file] = unionStrings(out[file], removes)
			for _, ri := range refs[:j] {
				st := c.pkgs[ri.pkg].Steps[ri.step].(manifest.JSONMergeStep)
				st.Merge, _ = jsonutil.RemovePaths(st.Merge, removes).(map[string]any)
				c.pkgs[ri.pkg].Steps[ri.step] = st
			}
		}
	}
	return out
}

// placeRemoves puts each of a folded file's remove paths on the fold whose
// merge sets that path again (the replace fold first, as it applies last), so
// removing and re-setting happen in one step and both folds converge; that
// fold takes over the append fold's part of the removed subtree. A path no fold
// sets goes on the fold that applies first: the append fold when there is one.
func placeRemoves(pkgs []*manifest.Package, file string, paths []string, appendAt, replaceAt map[string]mergeRef) {
	var ats []mergeRef
	if at, ok := replaceAt[file]; ok {
		ats = append(ats, at)
	}
	if at, ok := appendAt[file]; ok {
		ats = append(ats, at)
	}
	if len(ats) == 0 {
		return
	}
	for _, p := range paths {
		if coveredBy(p, paths) {
			continue // an ancestor's removal already deletes it
		}
		target := ats[len(ats)-1]
		for _, at := range ats {
			if jsonutil.HasPath(pkgs[at.pkg].Steps[at.step].(manifest.JSONMergeStep).Merge, strings.Split(p, ".")) {
				target = at
				break
			}
		}
		st := pkgs[target.pkg].Steps[target.step].(manifest.JSONMergeStep)
		st.Remove = append(st.Remove, p)
		if aAt, ok := appendAt[file]; ok && target != aAt {
			// The fold that removes a path owns its whole subtree: the append
			// fold's part of it moves over, or it would be removed again.
			as := pkgs[aAt.pkg].Steps[aAt.step].(manifest.JSONMergeStep)
			path := strings.Split(p, ".")
			if sub, ok := getPath(as.Merge, path); ok {
				as.Merge, _ = jsonutil.RemovePaths(as.Merge, []string{p}).(map[string]any)
				pkgs[aAt.pkg].Steps[aAt.step] = as
				own, _ := getPath(st.Merge, path)
				st.Merge = setPath(deepCopy(st.Merge), path, combine(sub, own, "", "", map[string]string{}, new([]string)))
			}
		}
		pkgs[target.pkg].Steps[target.step] = st
	}
}

// coveredBy reports whether another of paths is a strict ancestor of p.
func coveredBy(p string, paths []string) bool {
	for _, q := range paths {
		if strings.HasPrefix(p, q+".") {
			return true
		}
	}
	return false
}

func getPath(m map[string]any, path []string) (any, bool) {
	var v any = m
	for _, k := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = mm[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// setPath sets path in m to v, creating maps along it, and returns m.
func setPath(m map[string]any, path []string, v any) map[string]any {
	if len(path) == 1 {
		m[path[0]] = v
		return m
	}
	child, _ := m[path[0]].(map[string]any)
	if child == nil {
		child = map[string]any{}
	}
	m[path[0]] = setPath(child, path[1:], v)
	return m
}

// absorb moves every array in app that owned sets at the same path into owned
// (as an ordered union), pruning emptied maps from app. It returns what is left
// of app, the grown owned, and whether anything moved.
func absorb(app, owned map[string]any) (map[string]any, map[string]any, bool) {
	moved := false
	for k, av := range app {
		switch a := av.(type) {
		case []any:
			if o, ok := owned[k].([]any); ok {
				owned[k] = combine(o, a, "", "", map[string]string{}, new([]string))
				delete(app, k)
				moved = true
			}
		case map[string]any:
			if o, ok := owned[k].(map[string]any); ok {
				rest, grown, m := absorb(a, o)
				owned[k] = grown
				if m {
					moved = true
				}
				if len(rest) == 0 {
					delete(app, k)
				} else {
					app[k] = rest
				}
			}
		}
	}
	return app, owned, moved
}

func deepCopy(m map[string]any) map[string]any {
	out, _ := copyValue(m).(map[string]any)
	return out
}

func copyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = copyValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = copyValue(e)
		}
		return out
	}
	return v
}

func unionStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, s := range b {
		found := false
		for _, have := range out {
			found = found || have == s
		}
		if !found {
			out = append(out, s)
		}
	}
	return out
}

func less(a, b mergeRef) bool {
	return a.pkg < b.pkg || (a.pkg == b.pkg && a.step < b.step)
}

func (c *composed) setNote(r mergeRef, note string) {
	if c.notes[r.pkg] == nil {
		c.notes[r.pkg] = map[int]string{}
	}
	c.notes[r.pkg][r.step] = note
}

// rebuild drops the folded-away steps and moves each replace fold after its
// append anchor, re-keying the notes to the steps' final positions.
func (c *composed) rebuild(remove map[mergeRef]bool, move map[mergeRef]mergeRef) {
	type moved struct {
		step manifest.Step
		note string
	}
	pending := map[mergeRef][]moved{}
	for from, after := range move {
		pending[after] = append(pending[after], moved{c.pkgs[from.pkg].Steps[from.step], c.notes[from.pkg][from.step]})
		remove[from] = true
	}
	notes := map[int]map[int]string{}
	for pi, p := range c.pkgs {
		var steps []manifest.Step
		add := func(s manifest.Step, note string) {
			if note != "" {
				if notes[pi] == nil {
					notes[pi] = map[int]string{}
				}
				notes[pi][len(steps)] = note
			}
			steps = append(steps, s)
		}
		for si, s := range p.Steps {
			r := mergeRef{pi, si}
			if !remove[r] {
				add(s, c.notes[pi][si])
			}
			for _, m := range pending[r] {
				add(m.step, m.note)
			}
		}
		p.Steps = steps
	}
	c.notes = notes
}

// foldGroup combines a group's merge documents in order: maps deep-merge,
// arrays take the ordered union, and a scalar set differently by a later
// layer wins and is reported as an override.
func foldGroup(pkgs []*manifest.Package, g *mergeGroup) (map[string]any, []string, []string) {
	var acc any = map[string]any{}
	setBy := map[string]string{}
	var contributors, overrides []string
	seen := map[string]bool{}
	for _, r := range g.refs {
		p := pkgs[r.pkg]
		label := layerLabel(p)
		if !seen[label] {
			seen[label] = true
			contributors = append(contributors, label)
		}
		var doc map[string]any
		switch st := p.Steps[r.step].(type) {
		case manifest.JSONMergeStep:
			doc = st.Merge
		case manifest.TomlMergeStep:
			doc = st.Merge
		}
		if doc != nil {
			acc = combine(acc, jsonutil.ToAny(doc), "", label, setBy, &overrides)
		}
	}
	sort.Strings(overrides)
	out, _ := acc.(map[string]any)
	return out, contributors, overrides
}

func combine(acc, next any, path, layer string, setBy map[string]string, overrides *[]string) any {
	switch n := next.(type) {
	case map[string]any:
		a, ok := acc.(map[string]any)
		if !ok {
			a = map[string]any{}
		}
		out := map[string]any{}
		for k, v := range a {
			out[k] = v
		}
		for k, v := range n {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			if cur, ok := out[k]; ok {
				out[k] = combine(cur, v, sub, layer, setBy, overrides)
			} else {
				out[k] = v
				setBy[sub] = layer
			}
		}
		return out
	case []any:
		a, _ := acc.([]any)
		out := append([]any(nil), a...)
		for _, e := range n {
			dup := false
			for _, have := range out {
				if reflect.DeepEqual(have, e) {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, e)
			}
		}
		setBy[path] = layer
		return out
	default:
		if acc != nil && !reflect.DeepEqual(acc, next) && setBy[path] != layer {
			*overrides = append(*overrides, fmt.Sprintf("%s: %s overrides %s", path, layer, setBy[path]))
		}
		setBy[path] = layer
		return next
	}
}
