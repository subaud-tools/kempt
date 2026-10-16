package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(serviceHandlerImpl{}) }

// expandTilde expands a leading ~ to home but never repo-joins bare values.
// Env values are NOT repo-relative paths, so only tilde substitution applies.
func expandTilde(home, v string) string {
	if v == "~" {
		return home
	}
	if strings.HasPrefix(v, "~/") {
		return filepath.Join(home, v[2:])
	}
	return v // verbatim — never repo-join
}

// xmlEscaper escapes the XML metacharacters that can appear in program args,
// env values, and log paths.
var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// renderPlist produces the exact launchd plist for a service step. Keys are
// emitted in a fixed alphabetical order for determinism. Optional keys are
// only rendered when set; a minimal step (label+program only) renders exactly
// as the phase-1b template did. ctx is needed to ~-expand env values and log
// paths.
func renderPlist(ctx *machine.Context, st manifest.ServiceStep) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")

	// EnvironmentVariables — keys sorted for determinism.
	if len(st.Env) > 0 {
		keys := make([]string, 0, len(st.Env))
		for k := range st.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\t<key>EnvironmentVariables</key>\n")
		b.WriteString("\t<dict>\n")
		for _, k := range keys {
			b.WriteString("\t\t<key>")
			b.WriteString(xmlEscaper.Replace(k))
			b.WriteString("</key>\n")
			b.WriteString("\t\t<string>")
			b.WriteString(xmlEscaper.Replace(expandTilde(ctx.Home, st.Env[k])))
			b.WriteString("</string>\n")
		}
		b.WriteString("\t</dict>\n")
	}

	// KeepAlive — default true; explicit false renders <false/>.
	b.WriteString("\t<key>KeepAlive</key>\n")
	if st.KeepAlive != nil && !*st.KeepAlive {
		b.WriteString("\t<false/>\n")
	} else {
		b.WriteString("\t<true/>\n")
	}

	// Label.
	b.WriteString("\t<key>Label</key>\n")
	b.WriteString("\t<string>")
	b.WriteString(xmlEscaper.Replace(st.Label))
	b.WriteString("</string>\n")

	// LimitLoadToSessionType.
	if st.SessionType != "" {
		b.WriteString("\t<key>LimitLoadToSessionType</key>\n")
		b.WriteString("\t<string>")
		b.WriteString(xmlEscaper.Replace(st.SessionType))
		b.WriteString("</string>\n")
	}

	// ProcessType.
	if st.ProcessType != "" {
		b.WriteString("\t<key>ProcessType</key>\n")
		b.WriteString("\t<string>")
		b.WriteString(xmlEscaper.Replace(st.ProcessType))
		b.WriteString("</string>\n")
	}

	// ProgramArguments.
	b.WriteString("\t<key>ProgramArguments</key>\n")
	b.WriteString("\t<array>\n")
	for _, a := range st.Program {
		b.WriteString("\t\t<string>")
		b.WriteString(xmlEscaper.Replace(a))
		b.WriteString("</string>\n")
	}
	b.WriteString("\t</array>\n")

	// RunAtLoad — default true; explicit false renders <false/>.
	b.WriteString("\t<key>RunAtLoad</key>\n")
	if st.RunAtLoad != nil && !*st.RunAtLoad {
		b.WriteString("\t<false/>\n")
	} else {
		b.WriteString("\t<true/>\n")
	}

	// StandardErrorPath.
	if st.Stderr != "" {
		b.WriteString("\t<key>StandardErrorPath</key>\n")
		b.WriteString("\t<string>")
		b.WriteString(xmlEscaper.Replace(ctx.Expand(st.Stderr)))
		b.WriteString("</string>\n")
	}

	// StandardOutPath.
	if st.Stdout != "" {
		b.WriteString("\t<key>StandardOutPath</key>\n")
		b.WriteString("\t<string>")
		b.WriteString(xmlEscaper.Replace(ctx.Expand(st.Stdout)))
		b.WriteString("</string>\n")
	}

	// StartInterval — a periodic job; validation requires keep-alive = false.
	if st.StartInterval != nil {
		b.WriteString("\t<key>StartInterval</key>\n")
		fmt.Fprintf(&b, "\t<integer>%d</integer>\n", *st.StartInterval)
	}

	// ThrottleInterval.
	if st.ThrottleInterval != nil {
		b.WriteString("\t<key>ThrottleInterval</key>\n")
		fmt.Fprintf(&b, "\t<integer>%d</integer>\n", *st.ThrottleInterval)
	}

	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

// serviceHandlerImpl manages a launchd user agent (darwin-only backend).
type serviceHandlerImpl struct{}

func (serviceHandlerImpl) Kind() string { return "service" }

func plistPathFor(ctx *machine.Context, label string) string {
	return filepath.Join(ctx.Home, "Library", "LaunchAgents", label+".plist")
}

func (serviceHandlerImpl) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.ServiceStep)
	base := fmt.Sprintf("service %s", st.Label)

	if ctx.OS != "darwin" {
		return engine.Delta{Op: engine.OpBlocked, Detail: base + " (launchd backend requires darwin)"}, nil
	}

	want := renderPlist(ctx, st)
	path := plistPathFor(ctx, st.Label)
	cur, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return engine.Delta{Op: engine.OpChange, Detail: base + " (install agent)"}, nil
		}
		return engine.Delta{}, err
	}
	if string(cur) != want {
		return engine.Delta{Op: engine.OpChange, Detail: base + " (update agent)"}, nil
	}
	return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
}

func (serviceHandlerImpl) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.ServiceStep)
	want := renderPlist(ctx, st)
	path := plistPathFor(ctx, st.Label)

	// Determine whether the on-disk content already matches. When it does,
	// applying is a no-op: no write, no reload.
	cur, err := os.ReadFile(path)
	switch {
	case err == nil:
		if string(cur) == want {
			return nil
		}
	case !os.IsNotExist(err):
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return err
	}

	// Reload the agent: bootout (ignore error — it may not be loaded) then
	// bootstrap (error propagates).
	domain := fmt.Sprintf("gui/%d", ctx.UID)
	_, _ = ctx.Runner.Run("launchctl", "bootout", domain, path)
	if _, err := ctx.Runner.Run("launchctl", "bootstrap", domain, path); err != nil {
		return err
	}
	return nil
}
