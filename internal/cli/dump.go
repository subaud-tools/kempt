package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/machine"
)

func init() {
	Register(Command{
		Name:     "dump",
		Summary:  "suggest a manifest from the current machine (read-only)",
		Synopsis: "dump [flags]",
		Help:     "Prints a manifest inferred from the current machine (read-only).",
		NewFlags: func() *flag.FlagSet { fs, _ := newDumpFlags(); return fs },
		Run:      runDump,
	})
}

// dumpFlags holds the parsed flag values for runDump.
type dumpFlags struct {
	repo *string
}

// newDumpFlags constructs dump's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runDump's body.
func newDumpFlags() (*flag.FlagSet, *dumpFlags) {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	v := &dumpFlags{
		repo: fs.String("repo", "", "repo directory for symlink detection"),
	}
	return fs, v
}

func runDump(args []string, out, errw io.Writer) error {
	fs, v := newDumpFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	ctx, err := newContext(".")
	if err != nil {
		return err
	}

	// Header comments.
	_, _ = fmt.Fprintln(out, "# kempt dump — suggestions; review before committing")
	_, _ = fmt.Fprintln(out, "# note: symlink detection is shallow (depth-1 scan of ~ and ~/.config).")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "[kempt]")
	_, _ = fmt.Fprintln(out, "spec = 1")

	dumpBrew(ctx, out)

	if *v.repo != "" {
		dumpSymlinks(ctx, *v.repo, out)
	}

	return nil
}

// dumpBrew emits an install suggestion from `brew leaves` and
// `brew list --cask -1`. If brew is absent, it emits a comment and returns.
func dumpBrew(ctx *machine.Context, out io.Writer) {
	if _, err := ctx.Runner.LookPath("brew"); err != nil {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "# brew not found; skipping install suggestions")
		return
	}

	formulas := brewList(ctx, "leaves")
	casks := brewList(ctx, "list", "--cask", "-1")

	if len(formulas) == 0 && len(casks) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "[packages.brew]")
	_, _ = fmt.Fprintln(out, "  [[packages.brew.install]]")

	var parts []string
	if len(formulas) > 0 {
		parts = append(parts, "formulas = "+tomlStringArray(formulas))
	}
	if len(casks) > 0 {
		parts = append(parts, "casks = "+tomlStringArray(casks))
	}
	_, _ = fmt.Fprintf(out, "  brew = { %s }\n", strings.Join(parts, ", "))
}

// brewList runs a brew inventory command and returns sorted, trimmed,
// non-empty lines. Errors yield an empty slice (best-effort suggestions).
func brewList(ctx *machine.Context, args ...string) []string {
	stdout, err := ctx.Runner.Run("brew", args...)
	if err != nil {
		return nil
	}
	var items []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			items = append(items, line)
		}
	}
	sort.Strings(items)
	return items
}

// symlinkSuggestion is one discovered dotfile symlink pointing into the repo.
type symlinkSuggestion struct {
	from string // target relative to repoAbs
	to   string // link path with Home prefix replaced by "~"
}

func dumpSymlinks(ctx *machine.Context, repo string, out io.Writer) {
	repoAbs := ctx.Expand(repo)
	if abs, err := filepath.Abs(repoAbs); err == nil {
		repoAbs = abs
	}
	// Resolve symlinks in repoAbs so target comparison matches EvalSymlinks'd
	// link targets (e.g. macOS /var → /private/var).
	if resolved, err := filepath.EvalSymlinks(repoAbs); err == nil {
		repoAbs = resolved
	}

	var found []symlinkSuggestion
	found = append(found, scanSymlinks(ctx, ctx.Home, repoAbs)...)
	found = append(found, scanSymlinks(ctx, filepath.Join(ctx.Home, ".config"), repoAbs)...)

	if len(found) == 0 {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "# no repo-linked dotfiles found in ~ or ~/.config")
		return
	}

	sort.Slice(found, func(i, j int) bool { return found[i].to < found[j].to })

	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "[packages.dotfiles]")
	for _, s := range found {
		_, _ = fmt.Fprintln(out, "  [[packages.dotfiles.symlink]]")
		_, _ = fmt.Fprintf(out, "  from = %q\n", s.from)
		_, _ = fmt.Fprintf(out, "  to = %q\n", s.to)
	}
}

// scanSymlinks scans dir (depth 1) for symlinks whose resolved target is inside
// repoAbs, returning a suggestion for each.
func scanSymlinks(ctx *machine.Context, dir, repoAbs string) []symlinkSuggestion {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []symlinkSuggestion
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(dir, e.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		targetAbs := target
		if !filepath.IsAbs(targetAbs) {
			targetAbs = filepath.Join(dir, target)
		}
		if resolved, err := filepath.EvalSymlinks(link); err == nil {
			targetAbs = resolved
		}
		targetAbs = filepath.Clean(targetAbs)

		rel, err := filepath.Rel(repoAbs, targetAbs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		to := link
		if strings.HasPrefix(link, ctx.Home) {
			to = "~" + link[len(ctx.Home):]
		}
		out = append(out, symlinkSuggestion{from: rel, to: to})
	}
	return out
}

// tomlStringArray renders a slice as a TOML inline array of quoted strings.
func tomlStringArray(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
