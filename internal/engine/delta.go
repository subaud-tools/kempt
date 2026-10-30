// Package engine builds and renders plans of primitive steps and dispatches
// them to per-kind handlers. It is the structural core consumed by every
// handler and by the CLI plan/apply commands.
package engine

import (
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

// Op is the outcome of inspecting a step against the current machine state.
type Op int

const (
	OpNoop    Op = iota // current == desired
	OpChange            // apply would act; Detail says what
	OpSkip              // filtered out by only (Detail names the condition)
	OpBlocked           // cannot inspect/apply (Detail says why)
)

// Delta is the result of inspecting a single step.
type Delta struct {
	Op     Op
	Detail string // one human line, no trailing newline
}

// Handler inspects and applies a single kind of primitive step. Inspect must
// be strictly read-only.
type Handler interface {
	Kind() string
	Inspect(ctx *machine.Context, s manifest.Step) (Delta, error)
	Apply(ctx *machine.Context, s manifest.Step) error
}

var handlers = map[string]Handler{}

// RegisterHandler adds a handler to the package-level registry, keyed by Kind.
// Handlers register themselves from their file's init().
func RegisterHandler(h Handler) { handlers[h.Kind()] = h }

// HandlerFor returns the registered handler for a kind, if any.
func HandlerFor(kind string) (Handler, bool) {
	h, ok := handlers[kind]
	return h, ok
}

// StepResult pairs a step with its inspected Delta and any apply-time error.
type StepResult struct {
	Step    manifest.Step
	Delta   Delta
	Err     error // apply-time error, nil during plan
	Applied bool  // set by Execute when Apply ran without error
}

// PackagePlan is the plan for one package.
type PackagePlan struct {
	Needs       []string
	Resolved    *state.ResolvedRelease
	SetupParent string
	Name        string
	Skipped     bool   // whole package filtered by only
	Detail      string // skip reason when Skipped (e.g. "os != darwin")
	Steps       []StepResult
	Notes       []string
	Layer       string // declaring layer, "" for the base
	// Overrides lists the entries this package's layer took over from an
	// earlier layer; always rendered, since they change what converges.
	Overrides []string
	Root      string // relative-path root, "" for the context's repo
}

// Plan is the ordered set of package plans.
type Plan struct{ Packages []PackagePlan }
