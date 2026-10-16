// Package layers loads a machine's chosen layers and composes them with the
// base manifest into one manifest and one selection (see
// docs/superpowers/specs/2026-09-29-layers-design.md). Everything downstream
// (plan, apply, update, doctor, ...) then works on the composed manifest; the
// cross-layer step rules live in engine.BuildPlan.
package layers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

// DefaultFile is a layer's file name inside its source directory.
const DefaultFile = "kempt.toml"

// Loaded is a layer whose file was read and validated.
type Loaded struct {
	Layer    state.Layer
	Manifest *manifest.Manifest
	File     string // absolute path of the layer file
	Root     string // relative paths in the layer resolve here
	Hash     string // sha256 of the file as read
}

// Status names a layer left out of this run and why.
type Status struct{ Name, Reason string }

// Result is what Load found for each chosen layer.
type Result struct {
	Loaded   []Loaded
	Skipped  []Status                      // source missing: reported, the rest converges
	Held     []Status                      // project-added layer changed since applied
	Findings map[string][]manifest.Finding // per layer: parse, lint, or scope errors
}

// ExpandHome expands a leading "~" against home ("" means os.UserHomeDir).
func ExpandHome(p, home string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}

// FilePath is the absolute path of a layer's file.
func FilePath(l state.Layer, home string) string {
	file := l.Source.File
	if file == "" {
		file = DefaultFile
	}
	return filepath.Join(ExpandHome(l.Source.Dir, home), file)
}

// FileHash is the "sha256:<hex>" of a file, the value a project layer's
// Approved records.
func FileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Load reads each chosen layer. A missing source is skipped, a project-added
// layer whose file changed since it was applied is held, and a layer that
// fails to parse, lint, or whose file scope differs from the scope it was
// added with has findings; none of these stop the other layers loading.
func Load(chosen []state.Layer, home string) Result {
	res := Result{Findings: map[string][]manifest.Finding{}}
	for _, l := range chosen {
		file := FilePath(l, home)
		src, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			res.Skipped = append(res.Skipped, Status{l.Name, file + " not found"})
			continue
		}
		if err != nil {
			res.Skipped = append(res.Skipped, Status{l.Name, err.Error()})
			continue
		}
		sum := sha256.Sum256(src)
		hash := "sha256:" + hex.EncodeToString(sum[:])
		// An empty Approved means the file was never accepted: held, too.
		if l.Project != "" && hash != l.Approved {
			res.Held = append(res.Held, Status{l.Name, "layer changed since you applied it"})
			continue
		}
		m, findings := manifest.Parse(src)
		if m != nil {
			findings = append(findings, manifest.Validate(m)...)
			if m.Layer != l.Scope {
				findings = append(findings, manifest.Finding{Path: "kempt.layer",
					Msg: fmt.Sprintf("file scope %q does not match the layer's scope %q", m.Layer, l.Scope)})
			}
		}
		if len(findings) > 0 {
			res.Findings[l.Name] = findings
			continue
		}
		root := filepath.Dir(file)
		if l.Scope == manifest.LayerProject {
			root = ExpandHome(l.Project, home)
		}
		res.Loaded = append(res.Loaded, Loaded{Layer: l, Manifest: m, File: file, Root: root, Hash: hash})
	}
	return res
}

// Qualified is a layer package's name in the composed manifest.
func Qualified(layer, pkg string) string { return layer + "/" + pkg }

// Compose returns one manifest holding the base's packages and every loaded
// layer's, namespaced as "<layer>/<pkg>" with Layer and Root set, and the
// selection: basePkgs (every base package when empty) followed by each layer's
// chosen packages (every package in the layer when none were chosen). A layer's
// unqualified need names its own package first, then the base's; a qualified
// one names an earlier layer's.
func Compose(base *manifest.Manifest, basePkgs []string, loaded []Loaded) (*manifest.Manifest, []string, error) {
	m := &manifest.Manifest{
		Spec:     base.Spec,
		Packages: map[string]*manifest.Package{},
		Profiles: base.Profiles,
		Doctor:   base.Doctor,
	}
	for name, p := range base.Packages {
		m.Packages[name] = p
	}
	sel := append([]string(nil), basePkgs...)
	if len(sel) == 0 {
		for name := range base.Packages {
			sel = append(sel, name)
		}
		sort.Strings(sel)
	}
	for _, l := range loaded {
		name := l.Layer.Name
		for pkgName, p := range l.Manifest.Packages {
			c := *p
			c.Name = Qualified(name, pkgName)
			c.Layer = name
			c.Root = l.Root
			c.Needs = nil
			for _, need := range p.Needs {
				switch {
				case strings.Contains(need, "/"):
					c.Needs = append(c.Needs, need)
				case l.Manifest.Packages[need] != nil:
					c.Needs = append(c.Needs, Qualified(name, need))
				default:
					c.Needs = append(c.Needs, need)
				}
			}
			for _, need := range c.Needs {
				if _, ok := m.Packages[need]; !ok && !strings.HasPrefix(need, name+"/") {
					return nil, nil, fmt.Errorf("layer %s: package %s needs unknown package %q", name, pkgName, need)
				}
			}
			m.Packages[c.Name] = &c
		}
		chosen := l.Layer.Packages
		if len(chosen) == 0 {
			for pkgName := range l.Manifest.Packages {
				chosen = append(chosen, pkgName)
			}
			sort.Strings(chosen)
		}
		for _, pkgName := range chosen {
			q := Qualified(name, pkgName)
			if m.Packages[q] == nil {
				return nil, nil, fmt.Errorf("layer %s has no package %q (%s)", name, pkgName, q)
			}
			sel = append(sel, q)
		}
	}
	return m, sel, nil
}

// Order stable-sorts selected packages by layer rank (the base, then each
// loaded layer in order), keeping Select's topological order within a rank.
// A layer needs only the base or earlier layers, so needs still come first.
func Order(selected []*manifest.Package, loaded []Loaded) []*manifest.Package {
	rank := map[string]int{"": 0}
	for i, l := range loaded {
		rank[l.Layer.Name] = i + 1
	}
	out := append([]*manifest.Package(nil), selected...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Layer] < rank[out[j].Layer] })
	return out
}
