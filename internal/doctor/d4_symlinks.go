package doctor

import (
	"fmt"
	"os"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func CheckSymlinks(ctx *machine.Context, pkgs []*manifest.Package) []Finding {
	var out []Finding
	for _, pkg := range pkgs {
		pctx := engine.ContextFor(ctx, pkg.Root)
		for _, step := range pkg.Steps {
			sl, ok := step.(manifest.SymlinkStep)
			if !ok {
				continue
			}
			to := pctx.Expand(sl.To)
			fi, err := os.Lstat(to)
			if err != nil {
				continue // absent → plan will create it
			}
			// Match the symlink handler's own target basis exactly: it links to
			// pctx.Expand(From), which resolves ~, absolute, and repo-relative From
			// alike. filepath.Join(RepoDir, From) would only agree for repo-relative
			// From and would false-positive on a ~/ or absolute From.
			want := pctx.Expand(sl.From)
			if fi.Mode()&os.ModeSymlink == 0 {
				out = append(out, Finding{
					Check: "symlink", Package: pkg.Name, Severity: Warn,
					Detail:      fmt.Sprintf("%s is a real %s where a managed symlink is expected", to, kindOf(fi)),
					Remediation: "run `kempt apply` (it will back up and relink)",
				})
				continue
			}
			target, _ := os.Readlink(to)
			if _, err := os.Stat(to); err != nil {
				out = append(out, Finding{
					Check: "symlink", Package: pkg.Name, Severity: Error,
					Detail:      fmt.Sprintf("%s is a broken symlink → %s", to, target),
					Remediation: "run `kempt apply` to relink, or remove the dangling link",
				})
				continue
			}
			if target != want {
				out = append(out, Finding{
					Check: "symlink", Package: pkg.Name, Severity: Warn,
					Detail:      fmt.Sprintf("%s points to %s, expected %s", to, target, want),
					Remediation: "run `kempt apply` to repoint it",
				})
			}
		}
	}
	return out
}

func kindOf(fi os.FileInfo) string {
	if fi.IsDir() {
		return "directory"
	}
	return "file"
}
