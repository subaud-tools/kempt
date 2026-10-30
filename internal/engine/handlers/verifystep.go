package handlers

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(verifyStepHandler{}) }

// verifyStepHandler evaluates read-only checks. Inspect returns OpNoop when all
// checks pass and OpBlocked when any fail; it never returns OpChange, so the
// engine's Execute (which walks OpChange only) never applies a verify step.
// verify degrades rather than hard-erroring: resolver and runner failures
// become OpBlocked details, keeping `kempt verify` usable offline.
//
// version-current Command field: the value is split on whitespace
// (strings.Fields) where the first token is the binary and the remainder are
// arguments. Quoted arguments and shell metacharacters are NOT supported;
// use a wrapper script for complex invocations.
type verifyStepHandler struct{}

func (verifyStepHandler) Kind() string { return "verify" }

func (verifyStepHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.VerifyStep)
	if st.Offline && (st.HTTPOk != "" || st.VersionCurrent != nil) {
		return engine.Delta{Op: engine.OpBlocked, Detail: "verify: release setup network check unresolved offline; run kempt apply"}, nil
	}

	var details []string
	blocked := false
	fail := func(detail string) { details = append(details, detail); blocked = true }
	pass := func(detail string) { details = append(details, detail) }

	if st.CommandExists != "" {
		if _, err := ctx.Runner.LookPath(st.CommandExists); err == nil {
			pass(fmt.Sprintf("verify: command %s ✓", st.CommandExists))
		} else {
			fail(fmt.Sprintf("verify: command %s MISSING", st.CommandExists))
		}
	}

	if st.SymlinkTarget != nil {
		link := ctx.Expand(st.SymlinkTarget.Link)
		target := ctx.Expand(st.SymlinkTarget.Target)
		cur, err := os.Readlink(link)
		if err == nil && cur == target {
			pass(fmt.Sprintf("verify: symlink %s -> %s ✓", link, target))
		} else {
			got := cur
			if err != nil {
				got = err.Error()
			}
			fail(fmt.Sprintf("verify: symlink %s -> %s MISMATCH (got %s)", link, target, got))
		}
	}

	if len(st.CommandExistsAny) > 0 {
		names := st.CommandExistsAny
		label := "[" + strings.Join(names, " ") + "]"
		found := ""
		for _, name := range names {
			if _, err := ctx.Runner.LookPath(name); err == nil {
				found = name
				break
			}
		}
		if found != "" {
			pass(fmt.Sprintf("verify: one of %s ✓ (%s)", label, found))
		} else {
			fail(fmt.Sprintf("verify: none of %s found", label))
		}
	}

	if st.HTTPOk != "" {
		_, err := ctx.Releases.Download(st.HTTPOk)
		if err == nil {
			pass(fmt.Sprintf("verify: %s ok", st.HTTPOk))
		} else {
			fail(fmt.Sprintf("verify: %s unreachable (%v)", st.HTTPOk, err))
		}
	}

	if st.VersionCurrent != nil {
		vc := st.VersionCurrent
		tag, err := ctx.Releases.LatestTag(vc.Repo)
		if err != nil {
			fail(fmt.Sprintf("verify: %s (cannot resolve latest: %v)", vc.Repo, err))
		} else {
			fields := strings.Fields(vc.Command)
			var out string
			var runErr error
			if len(fields) == 0 {
				runErr = errors.New("empty version command")
			} else {
				out, runErr = ctx.Runner.Run(fields[0], fields[1:]...)
			}
			switch {
			case runErr != nil:
				fail(fmt.Sprintf("verify: %s (version command failed: %v)", vc.Repo, runErr))
			case strings.Contains(out, strings.TrimPrefix(tag, "v")):
				pass(fmt.Sprintf("verify: %s current (%s)", vc.Repo, tag))
			default:
				fail(fmt.Sprintf("verify: %s behind (latest %s)", vc.Repo, tag))
			}
		}
	}

	op := engine.OpNoop
	if blocked {
		op = engine.OpBlocked
	}
	return engine.Delta{Op: op, Detail: strings.Join(details, "; ")}, nil
}

func (verifyStepHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	return errors.New("verify steps are never applied")
}
