package handlers

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/inventory"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(installHandler{}) }

// installHandler realises software installation for an install step.
//
// Backends fall into two categories:
//
//   - OS-EXCLUSIVE software backend (brew/winget/apt): exactly one applies per
//     OS, first-match wins. darwin/linux with brew content → brew; matching-OS
//     winget (windows) or apt (linux) with no brew → OpBlocked "(backend <name>
//     not implemented in this phase)"; nothing matched → no OS backend. On
//     linux with BOTH brew and apt content, brew is selected and apt ignored.
//   - ADDITIVE cross-platform sources (npm/pi): each applies independently on
//     any OS whenever its list is non-empty. A single install step may carry
//     brew AND npm AND pi, and every present source is evaluated and applied.
//
// Inspect evaluates every present backend, joins their Detail segments with
// "; ", and combines their ops with this precedence:
//
//  1. OpChange if ANY backend reports changes (Apply must run);
//  2. else OpBlocked if ANY required backend tool is missing (that backend
//     cannot converge, so the step is blocked);
//  3. else OpNoop.
//
// If no backend applies at all, the step is OpSkip "(no backend for <os>)".
type installHandler struct{}

// inventory command keys, memoized via ctx.Cache.
const (
	brewFormulaCmd = "brew list --formula -1"
	brewCaskCmd    = "brew list --cask -1"
	brewTapCmd     = "brew tap"
)

func (installHandler) Kind() string { return "install" }

func (installHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.InstallStep)

	var subs []engine.Delta

	// OS-exclusive software backend (one wins per OS).
	if useBrew(ctx, st) {
		d, err := brewInspect(ctx, st.Brew)
		if err != nil {
			return engine.Delta{}, err
		}
		subs = append(subs, d)
	} else if name, ok := unimplementedBackend(ctx, st); ok {
		subs = append(subs, engine.Delta{
			Op:     engine.OpBlocked,
			Detail: fmt.Sprintf("install (backend %s not implemented in this phase)", name),
		})
	}

	// Additive cross-platform sources.
	if len(st.Npm) > 0 {
		d, err := npmInspect(ctx, st.Npm)
		if err != nil {
			return engine.Delta{}, err
		}
		subs = append(subs, d)
	}
	if len(st.Pi) > 0 {
		d, err := piInspect(ctx, st.Pi)
		if err != nil {
			return engine.Delta{}, err
		}
		subs = append(subs, d)
	}

	if len(subs) == 0 {
		return engine.Delta{
			Op:     engine.OpSkip,
			Detail: fmt.Sprintf("install (no backend for %s)", ctx.OS),
		}, nil
	}
	return combineDeltas(subs), nil
}

// combineDeltas joins sub-backend deltas: Detail joins with "; " in evaluation
// order; Op = OpChange if any changes, else OpBlocked if any is blocked, else
// OpNoop.
func combineDeltas(subs []engine.Delta) engine.Delta {
	details := make([]string, 0, len(subs))
	op := engine.OpNoop
	blocked := false
	for _, d := range subs {
		details = append(details, d.Detail)
		switch d.Op {
		case engine.OpChange:
			op = engine.OpChange
		case engine.OpBlocked:
			blocked = true
		}
	}
	if op != engine.OpChange && blocked {
		op = engine.OpBlocked
	}
	return engine.Delta{Op: op, Detail: strings.Join(details, "; ")}
}

func (installHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.InstallStep)
	// Run every actionable backend in evaluation order. Each applier recomputes
	// its own missing set and no-ops when nothing is due.
	if useBrew(ctx, st) {
		if err := brewApply(ctx, st.Brew); err != nil {
			return err
		}
	}
	if len(st.Npm) > 0 {
		if err := npmApply(ctx, st.Npm); err != nil {
			return err
		}
	}
	if len(st.Pi) > 0 {
		if err := piApply(ctx, st.Pi); err != nil {
			return err
		}
	}
	return nil
}

// npmInspect probes global npm packages (read-only) and reports the delta.
func npmInspect(ctx *machine.Context, desired []string) (engine.Delta, error) {
	if _, err := ctx.Runner.LookPath("npm"); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: "install (npm not found)"}, nil //nolint:nilerr // a missing tool is a blocked step, not an error
	}
	installed, err := inventory.Npm(ctx)
	if err != nil {
		return engine.Delta{}, err
	}
	need := needing(desired, installed)
	if len(need) == 0 {
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("npm: %d present", len(desired))}, nil
	}
	return engine.Delta{Op: engine.OpChange, Detail: "npm install: " + strings.Join(need, " ")}, nil
}

// npmApply installs the missing global packages in one command, then
// invalidates the npm inventory cache key.
func npmApply(ctx *machine.Context, desired []string) error {
	if _, err := ctx.Runner.LookPath("npm"); err != nil {
		return nil //nolint:nilerr // absent: Inspect already reported this step blocked
	}
	installed, err := inventory.Npm(ctx)
	if err != nil {
		return err
	}
	need := needing(desired, installed)
	if len(need) == 0 {
		return nil
	}
	// Each entry is installed verbatim; a pinned entry carries its @version so
	// npm installs exactly that version.
	if _, err := ctx.Runner.Run("npm", append([]string{"install", "-g"}, need...)...); err != nil {
		return err
	}
	delete(ctx.Cache, inventory.NpmCmd)
	return nil
}

// piInspect probes registered pi packages (read-only) and reports the delta.
func piInspect(ctx *machine.Context, desired []string) (engine.Delta, error) {
	if _, err := ctx.Runner.LookPath("pi"); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: "install (pi not found)"}, nil //nolint:nilerr // a missing tool is a blocked step, not an error
	}
	present, err := inventory.Pi(ctx)
	if err != nil {
		return engine.Delta{}, err
	}
	need := needing(desired, present)
	if len(need) == 0 {
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("pi: %d present", len(desired))}, nil
	}
	return engine.Delta{Op: engine.OpChange, Detail: "pi install: " + strings.Join(need, " ")}, nil
}

// piApply installs each missing pi package with its own command, then
// invalidates the pi inventory cache key.
func piApply(ctx *machine.Context, desired []string) error {
	if _, err := ctx.Runner.LookPath("pi"); err != nil {
		return nil //nolint:nilerr // absent: Inspect already reported this step blocked
	}
	present, err := inventory.Pi(ctx)
	if err != nil {
		return err
	}
	need := needing(desired, present)
	if len(need) == 0 {
		return nil
	}
	// Install each entry verbatim; a pinned entry (e.g. npm:name@ver) carries
	// its version so pi converges to exactly that version. A failing entry must
	// not block the ones after it, so every entry is tried and the failures are
	// reported together.
	var errs []error
	for _, p := range need {
		if _, err := ctx.Runner.Run("pi", "install", p); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	delete(ctx.Cache, inventory.PiCmd)
	return errors.Join(errs...)
}

// splitNameVersion splits an entry into (name, version) at a trailing @version,
// preserving an `npm:` prefix and never treating a leading @scope as a version
// separator. It generalizes the old reducePiEntry helper.
//
// Examples:
//
//	"npm:pi-tmux-bridge@0.1.1"            → ("npm:pi-tmux-bridge", "0.1.1")
//	"npm:pi-tmux-bridge"                  → ("npm:pi-tmux-bridge", "")
//	"@earendil-works/pi-coding-agent"     → ("@earendil-works/pi-coding-agent", "")
//	"@earendil-works/pi-coding-agent@1.2.3" → ("@earendil-works/pi-coding-agent", "1.2.3")
//	"/local/path"                         → ("/local/path", "")
func splitNameVersion(entry string) (name, version string) {
	const prefix = "npm:"
	work := entry
	pre := ""
	if strings.HasPrefix(entry, prefix) {
		pre = prefix
		work = entry[len(prefix):]
	}
	// at > 0 guard so a leading "@scope" is not treated as a version separator.
	if at := strings.LastIndex(work, "@"); at > 0 {
		return pre + work[:at], work[at+1:]
	}
	return entry, ""
}

// useBrew reports whether the brew backend applies: darwin/linux with brew
// content.
func useBrew(ctx *machine.Context, st manifest.InstallStep) bool {
	if ctx.OS != "darwin" && ctx.OS != "linux" {
		return false
	}
	return st.Brew != nil && hasBrewContent(st.Brew)
}

func hasBrewContent(b *manifest.BrewSpec) bool {
	return len(b.Formulas) > 0 || len(b.Casks) > 0 || len(b.Taps) > 0
}

// unimplementedBackend names a matching-OS non-brew backend that has content
// but is not implemented in this phase.
func unimplementedBackend(ctx *machine.Context, st manifest.InstallStep) (string, bool) {
	switch ctx.OS {
	case "windows":
		if len(st.Winget) > 0 {
			return "winget", true
		}
	case "linux":
		if len(st.Apt) > 0 {
			return "apt", true
		}
	}
	return "", false
}

// brewInspect probes brew inventory (read-only) and reports the delta. It runs
// only the three inventory commands, memoized via ctx.Cache.
func brewInspect(ctx *machine.Context, spec *manifest.BrewSpec) (engine.Delta, error) {
	if _, err := ctx.Runner.LookPath("brew"); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: "install (brew not found)"}, nil //nolint:nilerr // a missing tool is a blocked step, not an error
	}

	installedF, err := brewInventory(ctx, brewFormulaCmd)
	if err != nil {
		return engine.Delta{}, err
	}
	installedC, err := brewInventory(ctx, brewCaskCmd)
	if err != nil {
		return engine.Delta{}, err
	}
	installedT, err := brewInventory(ctx, brewTapCmd)
	if err != nil {
		return engine.Delta{}, err
	}

	missF := missing(spec.Formulas, installedF)
	missC := missing(spec.Casks, installedC)
	missT := missing(spec.Taps, installedT)

	if len(missF) == 0 && len(missC) == 0 && len(missT) == 0 {
		total := len(spec.Formulas) + len(spec.Casks) + len(spec.Taps)
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("brew: %d present", total)}, nil
	}

	// Build labeled segments; formulas carry no label, casks/taps do. Join
	// only non-empty segments so casks-only yields "brew install: casks: X"
	// rather than "brew install: ; casks: X".
	var segments []string
	if len(missF) > 0 {
		segments = append(segments, strings.Join(missF, " "))
	}
	if len(missC) > 0 {
		segments = append(segments, "casks: "+strings.Join(missC, " "))
	}
	if len(missT) > 0 {
		segments = append(segments, "taps: "+strings.Join(missT, " "))
	}
	return engine.Delta{Op: engine.OpChange, Detail: "brew install: " + strings.Join(segments, "; ")}, nil
}

// brewApply installs the missing taps, formulas, and casks in that order,
// coalescing formulas and casks into a single command each, then invalidates
// the inventory cache keys.
func brewApply(ctx *machine.Context, spec *manifest.BrewSpec) error {
	installedF, err := brewInventory(ctx, brewFormulaCmd)
	if err != nil {
		return err
	}
	installedC, err := brewInventory(ctx, brewCaskCmd)
	if err != nil {
		return err
	}
	installedT, err := brewInventory(ctx, brewTapCmd)
	if err != nil {
		return err
	}

	missF := missing(spec.Formulas, installedF)
	missC := missing(spec.Casks, installedC)
	missT := missing(spec.Taps, installedT)

	// A failing tap must not block the taps after it.
	var tapErrs []error
	for _, t := range missT {
		if _, err := ctx.Runner.Run("brew", "tap", t); err != nil {
			tapErrs = append(tapErrs, fmt.Errorf("%s: %w", t, err))
		}
	}
	if err := errors.Join(tapErrs...); err != nil {
		delete(ctx.Cache, brewTapCmd)
		return err
	}
	if len(missF) > 0 {
		if _, err := ctx.Runner.Run("brew", append([]string{"install"}, missF...)...); err != nil {
			return err
		}
	}
	if len(missC) > 0 {
		if _, err := ctx.Runner.Run("brew", append([]string{"install", "--cask"}, missC...)...); err != nil {
			return err
		}
	}

	// Inventory changed; drop memoized results so any later probe re-reads.
	delete(ctx.Cache, brewFormulaCmd)
	delete(ctx.Cache, brewCaskCmd)
	delete(ctx.Cache, brewTapCmd)
	return nil
}

// brewInventory runs a read-only inventory command once per run, memoizing the
// stdout on ctx.Cache keyed by the FakeRunner key string.
func brewInventory(ctx *machine.Context, key string) (map[string]struct{}, error) {
	out, err := cachedRun(ctx, key)
	if err != nil {
		return nil, err
	}
	return toSet(out), nil
}

// cachedRun runs the command encoded in key (FakeRunner key format) once per
// run, memoizing stdout on ctx.Cache.
func cachedRun(ctx *machine.Context, key string) (string, error) {
	if out, ok := ctx.Cache[key]; ok {
		return out, nil
	}
	fields := strings.Fields(key)
	out, err := ctx.Runner.Run(fields[0], fields[1:]...)
	if err != nil {
		return "", err
	}
	if ctx.Cache != nil {
		ctx.Cache[key] = out
	}
	return out, nil
}

// toSet splits command output into a set of non-empty trimmed lines.
func toSet(out string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			set[line] = struct{}{}
		}
	}
	return set
}

// missing returns the desired items absent from installed, sorted.
func missing(desired []string, installed map[string]struct{}) []string {
	var out []string
	for _, d := range desired {
		if _, ok := installed[d]; !ok {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// needing returns the desired entries that are not satisfied by the installed
// name→version inventory, sorted. It supports both unversioned and pinned
// desired entries:
//
//   - UNVERSIONED ("name"): satisfied if the name is installed at ANY version
//     (presence-only — unchanged legacy behavior).
//   - PINNED ("name@version"): satisfied only if installed[name] == version;
//     otherwise it needs (re)install (covers absent AND wrong-version).
//
// The returned strings are the ORIGINAL desired entries (carrying any @version)
// so Apply installs them verbatim.
func needing(desired []string, installed map[string]string) []string {
	var out []string
	for _, d := range desired {
		name, ver := splitNameVersion(d)
		if ver == "" {
			if _, ok := installed[name]; !ok {
				out = append(out, d)
			}
			continue
		}
		if installed[name] != ver {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
