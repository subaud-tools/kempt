package engine

import (
	"fmt"
	"io"

	"github.com/subaud-tools/kempt/internal/manifest"
)

var markers = map[Op]string{
	OpNoop:    "✓",
	OpChange:  "+",
	OpSkip:    "-",
	OpBlocked: "!",
}

// Render writes a human-readable plan to w. A footer always summarises counts
// and, when there are changes, breaks them down by safety class.
//
// verbose controls the per-step body. Quiet (the default) prints only the steps
// that are NOT a clean ✓ — changes, skips, blocks — under their package header,
// and omits a package entirely when it is fully converged: a converged machine
// then prints just the summary line instead of dozens of ✓ lines burying the
// one thing that changed. Verbose restores the full listing: every package
// header and every step, ✓ included. The footer counts are identical in both
// modes — quiet hides lines, it never changes the totals.
func Render(p *Plan, w io.Writer, verbose bool) {
	var changes, ok, skipped, blocked int
	var software, files int

	for _, pp := range p.Packages {
		if pp.Skipped {
			_, _ = fmt.Fprintf(w, "package %s (skipped: %s)\n", pp.Name, pp.Detail)
			continue
		}
		// The header prints lazily in quiet mode: not until a noteworthy step
		// is about to be written, so a fully-✓ package emits nothing at all.
		headerPrinted := false
		header := func() {
			if !headerPrinted {
				_, _ = fmt.Fprintf(w, "package %s\n", pp.Name)
				headerPrinted = true
			}
		}
		if verbose {
			header()
		}
		for _, o := range pp.Overrides {
			header()
			_, _ = fmt.Fprintf(w, "  ^ %s\n", o)
		}
		for _, sr := range pp.Steps {
			switch sr.Delta.Op {
			case OpNoop:
				ok++
			case OpChange:
				changes++
				switch sr.Step.Class() {
				case manifest.ClassSoftware:
					software++
				case manifest.ClassFiles:
					files++
				}
			case OpSkip:
				skipped++
			case OpBlocked:
				blocked++
			}
			// Print the step in verbose mode, or in quiet mode when it is not a
			// clean ✓ (the header is materialised on demand for the latter).
			if verbose {
				_, _ = fmt.Fprintf(w, "  %s %s\n", markers[sr.Delta.Op], sr.Delta.Detail)
			} else if sr.Delta.Op != OpNoop {
				header()
				_, _ = fmt.Fprintf(w, "  %s %s\n", markers[sr.Delta.Op], sr.Delta.Detail)
			}
		}
	}

	_, _ = fmt.Fprintf(w, "%d changes, %d ok, %d skipped, %d blocked\n", changes, ok, skipped, blocked)
	if changes > 0 {
		_, _ = fmt.Fprintf(w, "software changes: %d, file changes: %d\n", software, files)
	}

	// Manual follow-ups are static, repeat-every-run reminders (codex login,
	// SwiftBar setup, ...). They are noise on a converged machine, so quiet mode
	// omits them; -v surfaces the full list.
	if !verbose {
		return
	}
	var allNotes []string
	for _, pp := range p.Packages {
		allNotes = append(allNotes, pp.Notes...)
	}
	if len(allNotes) > 0 {
		_, _ = fmt.Fprintf(w, "manual follow-ups:\n")
		for _, note := range allNotes {
			_, _ = fmt.Fprintf(w, "  - %s\n", note)
		}
	}
}
