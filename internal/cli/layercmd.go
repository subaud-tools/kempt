package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/layers"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

func init() {
	Register(Command{Name: "layer", Summary: "add, list, remove or re-apply the machine's layers",
		Synopsis: "layer add <name> <git-url | path> | layer add -project <dir> | layer list | layer remove <name> | layer apply <name>",
		Help: "Layers are manifests applied on top of the base, per machine: work, personal, " +
			"machine-only, or a project's own. `add` shows the combined plan and applies it; " +
			"`remove` takes a layer out of the selection and uninstalls nothing; `apply` re-applies " +
			"a layer and, for one added with -project, accepts its current file.",
		Run: runLayer})
}

func runLayer(args []string, out, errw io.Writer) error {
	if len(args) == 0 {
		return UsageError{Msg: "usage: kempt layer add|list|remove|apply"}
	}
	switch args[0] {
	case "add":
		return runLayerAdd(args[1:], out, errw)
	case "list":
		return runLayerList(args[1:], out)
	case "remove":
		return runLayerRemove(args[1:], out)
	case "apply":
		return runLayerApply(args[1:], out, errw)
	}
	return UsageError{Msg: fmt.Sprintf("unknown layer subcommand %q (want add, list, remove or apply)", args[0])}
}

// parseInterleaved parses flags that may come before, between or after the
// positional arguments, and returns the positionals in order.
func parseInterleaved(fs *flag.FlagSet, args []string, out io.Writer) ([]string, error) {
	var pos []string
	rest := args
	for {
		if err := ParseFlags(fs, rest, out); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

func isGitURL(s string) bool {
	for _, p := range []string{"https://", "http://", "ssh://", "git@"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return strings.HasSuffix(s, ".git")
}

// defaultLayerCheckout is where a git layer is cloned when -dir is not given.
func defaultLayerCheckout(name string) (string, error) {
	store, err := state.DefaultStore()
	if err != nil {
		return "", err
	}
	return filepath.Join(store.Dir, "layers", name), nil
}

func runLayerAdd(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("layer add", flag.ContinueOnError)
	file := fs.String("file", "", "layer file inside the source (default kempt.toml; .kempt/kempt.toml with -project)")
	dir := fs.String("dir", "", "checkout for a git source (default ~/.local/share/kempt/layers/<name>)")
	project := fs.String("project", "", "add the layer in this project checkout's .kempt/kempt.toml")
	pkgs := fs.String("packages", "", "comma-separated layer packages (default: all)")
	profile := fs.String("profile", "", "a profile defined in the layer")
	yes := fs.Bool("yes", false, "apply without prompting")
	fs.BoolVar(yes, "y", false, "shorthand for -yes")
	pos, err := parseInterleaved(fs, args, out)
	if err != nil {
		return err
	}
	if *pkgs != "" && *profile != "" {
		return UsageError{Msg: "use -packages or -profile, not both"}
	}
	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed {
		return UsageError{Msg: "no saved selection; run kempt init first"}
	}
	ctx, err := newContext(st.RepoDir)
	if err != nil {
		return err
	}

	var l state.Layer
	switch {
	case *project != "":
		if len(pos) > 1 {
			return UsageError{Msg: "usage: kempt layer add -project <dir> [name]"}
		}
		proj, err := filepath.Abs(layers.ExpandHome(*project, ctx.Home))
		if err != nil {
			return err
		}
		name := filepath.Base(proj)
		if len(pos) == 1 {
			name = pos[0]
		}
		f := *file
		if f == "" {
			f = filepath.Join(".kempt", layers.DefaultFile)
		}
		kind := "path"
		if _, err := ctx.Runner.Run("git", "-C", proj, "ls-files", "--error-unmatch", f); err == nil {
			kind = "git"
		}
		l = state.Layer{Name: name, Project: proj, Source: state.LayerSource{Kind: kind, Dir: proj, File: f}}
	default:
		if len(pos) != 2 {
			return UsageError{Msg: "usage: kempt layer add <name> <git-url | path>"}
		}
		name, src := pos[0], pos[1]
		l = state.Layer{Name: name, Source: state.LayerSource{File: *file}}
		if isGitURL(src) {
			d := layers.ExpandHome(*dir, ctx.Home)
			if d == "" {
				if d, err = defaultLayerCheckout(name); err != nil {
					return err
				}
			}
			if _, err := os.Stat(d); err == nil {
				origin, err := gitrepo.RemoteURL(ctx.Runner, d)
				if err != nil {
					return UsageError{Msg: fmt.Sprintf("%s exists but is not a git checkout: %v", d, err)}
				}
				if !handlers.SameRemote(origin, src) {
					return UsageError{Msg: fmt.Sprintf("%s already has origin %q, not %q", d, origin, src)}
				}
			} else if err := gitrepo.Clone(ctx.Runner, src, d); err != nil {
				return fmt.Errorf("clone %s: %w", src, err)
			}
			l.Source.Kind, l.Source.URL, l.Source.Dir = "git", src, d
		} else {
			d, err := filepath.Abs(layers.ExpandHome(src, ctx.Home))
			if err != nil {
				return err
			}
			l.Source.Kind, l.Source.Dir = "path", d
		}
	}
	if l.Name == "" || strings.ContainsAny(l.Name, "/ ") {
		return UsageError{Msg: fmt.Sprintf("invalid layer name %q (no slashes or spaces)", l.Name)}
	}
	for _, have := range st.Layers {
		if have.Name == l.Name {
			return UsageError{Msg: fmt.Sprintf("layer %q already exists; kempt layer remove %s first", l.Name, l.Name)}
		}
	}

	path := layers.FilePath(l, ctx.Home)
	src, err := os.ReadFile(path)
	if err != nil {
		return UsageError{Msg: fmt.Sprintf("cannot read layer file: %v", err)}
	}
	m, findings := manifest.Parse(src)
	if m == nil || len(findings) > 0 {
		return UsageError{Msg: fmt.Sprintf("%s does not parse; run kempt lint on it", path)}
	}
	switch {
	case m.Layer == "":
		return UsageError{Msg: fmt.Sprintf("%s is not a layer: its [kempt] has no layer = \"user\" or \"project\"", path)}
	case m.Layer == manifest.LayerProject && l.Project == "":
		return UsageError{Msg: fmt.Sprintf("%s is a project layer; add it with kempt layer add -project <checkout>", path)}
	}
	l.Scope = m.Layer

	switch {
	case *profile != "":
		pr, ok := m.Profiles[*profile]
		if !ok {
			return UsageError{Msg: fmt.Sprintf("layer has no profile %q", *profile)}
		}
		l.Packages = append([]string(nil), pr.Packages...)
	case *pkgs != "":
		l.Packages = splitPackages(*pkgs)
	default:
		for name := range m.Packages {
			l.Packages = append(l.Packages, name)
		}
	}
	sort.Strings(l.Packages)
	for _, p := range l.Packages {
		if m.Packages[p] == nil {
			return UsageError{Msg: fmt.Sprintf("layer has no package %q", p)}
		}
	}
	if l.Project != "" {
		if l.Approved, err = layers.FileHash(path); err != nil {
			return err
		}
	}

	next := *st
	next.Layers = insertLayer(st.Layers, l)
	if err := applyWithState(st, &next, *yes, out, errw); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "added layer %s (%s, %s)\n", l.Name, l.Scope, l.Source.Kind)
	return nil
}

// insertLayer keeps apply order: user layers, then project layers, each in the
// order added.
func insertLayer(ls []state.Layer, l state.Layer) []state.Layer {
	out := append([]state.Layer(nil), ls...)
	if l.Scope == manifest.LayerProject {
		return append(out, l)
	}
	at := len(out)
	for i, have := range out {
		if have.Scope == manifest.LayerProject {
			at = i
			break
		}
	}
	return append(out[:at], append([]state.Layer{l}, out[at:]...)...)
}

// applyWithState saves next and applies the saved selection. When the apply is
// declined or fails, prev is saved back, so a rejected change leaves the
// machine's layers as they were.
func applyWithState(prev, next *state.State, yes bool, out, errw io.Writer) error {
	if err := saveState(next); err != nil {
		return err
	}
	var applyArgs []string
	if yes {
		applyArgs = append(applyArgs, "-yes")
	}
	if err := runApply(applyArgs, out, errw); err != nil {
		if rerr := saveState(prev); rerr != nil {
			return fmt.Errorf("%w (and restoring the previous layers failed: %w)", err, rerr)
		}
		return err
	}
	return nil
}

func findLayer(st *state.State, name string) (int, error) {
	for i, l := range st.Layers {
		if l.Name == name {
			return i, nil
		}
	}
	return -1, UsageError{Msg: fmt.Sprintf("no layer %q (kempt layer list)", name)}
}

func runLayerList(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("layer list", flag.ContinueOnError)
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}
	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed || len(st.Layers) == 0 {
		_, _ = fmt.Fprintln(out, "no layers (kempt layer add <name> <git-url | path>)")
		return nil
	}
	home, _ := os.UserHomeDir()
	for _, l := range st.Layers {
		status := "ok"
		if _, err := os.Stat(layers.FilePath(l, home)); err != nil {
			status = "missing"
		}
		src := l.Source.Dir
		if l.Source.URL != "" {
			src = l.Source.URL
		}
		_, _ = fmt.Fprintf(out, "%s  %s  %s %s  %s  %s\n", l.Name, l.Scope, l.Source.Kind, src, strings.Join(l.Packages, ","), status)
	}
	return nil
}

func runLayerRemove(args []string, out io.Writer) error {
	name, err := parseOnePositional("layer remove", args, out)
	if err != nil {
		return err
	}
	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed {
		return UsageError{Msg: "no saved selection; run kempt init first"}
	}
	i, err := findLayer(st, name)
	if err != nil {
		return err
	}
	st.Layers = append(st.Layers[:i], st.Layers[i+1:]...)
	if err := saveState(st); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "removed layer %s; nothing was uninstalled\n", name)
	return nil
}

func runLayerApply(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("layer apply", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "apply without prompting")
	fs.BoolVar(yes, "y", false, "shorthand for -yes")
	pos, err := parseInterleaved(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return UsageError{Msg: "usage: kempt layer apply <name>"}
	}
	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed {
		return UsageError{Msg: "no saved selection; run kempt init first"}
	}
	i, err := findLayer(st, pos[0])
	if err != nil {
		return err
	}
	next := *st
	next.Layers = append([]state.Layer(nil), st.Layers...)
	if l := next.Layers[i]; l.Project != "" {
		home, _ := os.UserHomeDir()
		if next.Layers[i].Approved, err = layers.FileHash(layers.FilePath(l, home)); err != nil {
			return UsageError{Msg: fmt.Sprintf("cannot read layer file: %v", err)}
		}
	}
	return applyWithState(st, &next, *yes, out, errw)
}

// adoptInLayer and dropInLayer edit one layer's package list.
func adoptInLayer(layer, pkg string, out io.Writer) error {
	st, i, m, err := loadLayerForEdit(layer)
	if err != nil {
		return err
	}
	if m.Packages[pkg] == nil {
		return UsageError{Msg: fmt.Sprintf("layer %s has no package %q", layer, pkg)}
	}
	set := map[string]bool{pkg: true}
	for _, p := range st.Layers[i].Packages {
		set[p] = true
	}
	st.Layers[i].Packages = sortedKeys(set)
	if err := saveState(st); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "adopted %s\nrun kempt apply to converge\n", layers.Qualified(layer, pkg))
	return nil
}

func dropInLayer(layer, pkg string, out io.Writer) error {
	st, i, _, err := loadLayerForEdit(layer)
	if err != nil {
		return err
	}
	var kept []string
	for _, p := range st.Layers[i].Packages {
		if p != pkg {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(st.Layers[i].Packages) {
		return UsageError{Msg: fmt.Sprintf("%s is not selected", layers.Qualified(layer, pkg))}
	}
	if len(kept) == 0 {
		return UsageError{Msg: fmt.Sprintf("that is layer %s's last package; kempt layer remove %s instead", layer, layer)}
	}
	st.Layers[i].Packages = kept
	if err := saveState(st); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "dropped %s\n", layers.Qualified(layer, pkg))
	return nil
}

func loadLayerForEdit(layer string) (*state.State, int, *manifest.Manifest, error) {
	st, existed, err := loadState()
	if err != nil {
		return nil, 0, nil, err
	}
	if !existed {
		return nil, 0, nil, UsageError{Msg: "no saved selection; run kempt init first"}
	}
	i, err := findLayer(st, layer)
	if err != nil {
		return nil, 0, nil, err
	}
	home, _ := os.UserHomeDir()
	src, err := os.ReadFile(layers.FilePath(st.Layers[i], home))
	if err != nil {
		return nil, 0, nil, UsageError{Msg: fmt.Sprintf("cannot read layer file: %v", err)}
	}
	m, findings := manifest.Parse(src)
	if m == nil || len(findings) > 0 {
		return nil, 0, nil, UsageError{Msg: "layer file does not parse; run kempt lint on it"}
	}
	return st, i, m, nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
