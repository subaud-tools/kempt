package handlers

import (
	"fmt"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// apt command keys, memoized via ctx.Cache (FakeRunner key format).
const (
	// dpkgFormat is passed to dpkg-query as is; dpkg-query expands the \n.
	dpkgFormat     = `${Package} ${Status}\n`
	dpkgQueryCmd   = "dpkg-query -W -f " + dpkgFormat
	aptUpdateCmd   = "sudo -n apt-get update"
	aptReadyKey    = "apt ready"
	aptSudoRefused = "install (apt needs passwordless sudo: sudo -n refused)"
)

// useApt reports whether the apt backend applies: linux with apt content. On
// linux apt wins over brew, since a Linux host has no Homebrew.
func useApt(ctx *machine.Context, st manifest.InstallStep) bool {
	return ctx.OS == "linux" && len(st.Apt) > 0
}

// aptInspect reads the dpkg inventory (read-only) and reports the delta. With
// packages missing it also checks that sudo runs without a password, since
// Apply needs it; a refusal blocks the step.
func aptInspect(ctx *machine.Context, desired []string) (engine.Delta, error) {
	if _, err := ctx.Runner.LookPath("apt-get"); err != nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: "install (apt-get not found)"}, nil //nolint:nilerr // a missing tool is a blocked step, not an error
	}
	installed, err := aptInventory(ctx)
	if err != nil {
		return engine.Delta{}, err
	}
	need := missing(desired, installed)
	if len(need) == 0 {
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("apt: %d present", len(desired))}, nil
	}
	if !aptSudoOK(ctx) {
		return engine.Delta{Op: engine.OpBlocked, Detail: aptSudoRefused}, nil
	}
	return engine.Delta{Op: engine.OpChange, Detail: "apt install: " + strings.Join(need, " ")}, nil
}

// aptApply refreshes the package lists once per run and installs the missing
// packages in one command, then invalidates the inventory cache key.
func aptApply(ctx *machine.Context, desired []string) error {
	if _, ok := ctx.Cache[aptReadyKey]; !ok {
		if _, err := ctx.Runner.LookPath("apt-get"); err != nil {
			return nil //nolint:nilerr // absent: Inspect already reported this step blocked
		}
	}
	installed, err := aptInventory(ctx)
	if err != nil {
		return err
	}
	need := missing(desired, installed)
	if len(need) == 0 {
		return nil
	}
	if !aptSudoOK(ctx) {
		return nil // refused: Inspect already reported this step blocked
	}
	if _, done := ctx.Cache[aptUpdateCmd]; !done {
		if _, err := ctx.Runner.Run("sudo", "-n", "apt-get", "update"); err != nil {
			return err
		}
		if ctx.Cache != nil {
			ctx.Cache[aptUpdateCmd] = ""
		}
	}
	args := append([]string{"-n", "apt-get", "install", "-y", "--no-install-recommends"}, need...)
	if _, err := ctx.Runner.Run("sudo", args...); err != nil {
		return err
	}
	delete(ctx.Cache, dpkgQueryCmd)
	return nil
}

// aptInventory reads the installed packages once per run, memoized on
// ctx.Cache. A package counts as installed only when its status says so; one
// removed with its config files left is missing.
func aptInventory(ctx *machine.Context) (map[string]struct{}, error) {
	out, ok := ctx.Cache[dpkgQueryCmd]
	if !ok {
		var err error
		out, err = ctx.Runner.Run("dpkg-query", "-W", "-f", dpkgFormat)
		if err != nil {
			return nil, err
		}
		if ctx.Cache != nil {
			ctx.Cache[dpkgQueryCmd] = out
		}
	}
	set := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == "installed" {
			set[fields[0]] = struct{}{}
		}
	}
	return set, nil
}

// aptSudoOK reports whether sudo runs without a password, asked once per run
// and memoized on ctx.Cache. apt-get found on PATH is recorded with it, so an
// Apply after Inspect issues only its install commands.
func aptSudoOK(ctx *machine.Context) bool {
	if v, ok := ctx.Cache[aptReadyKey]; ok {
		return v == ""
	}
	v := ""
	if _, err := ctx.Runner.Run("sudo", "-n", "true"); err != nil {
		v = aptSudoRefused
	}
	if ctx.Cache != nil {
		ctx.Cache[aptReadyKey] = v
	}
	return v == ""
}
