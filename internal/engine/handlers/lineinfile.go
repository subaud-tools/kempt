package handlers

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(lineInFileHandler{}) }

// lineInFileHandler realises the desired state: ctx.Expand(File) contains Line
// as an exact full line. It never reorders or removes existing content.
type lineInFileHandler struct{}

func (lineInFileHandler) Kind() string { return "line-in-file" }

func (lineInFileHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.LineInFileStep)
	file := ctx.Expand(st.File)
	base := "line-in-file " + file

	b, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return engine.Delta{Op: engine.OpChange, Detail: base + " (create file)"}, nil
		}
		return engine.Delta{}, err
	}
	if hasLine(string(b), st.Line) {
		return engine.Delta{Op: engine.OpNoop, Detail: base}, nil
	}
	return engine.Delta{Op: engine.OpChange, Detail: base + " (append line)"}, nil
}

func (lineInFileHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.LineInFileStep)
	file := ctx.Expand(st.File)

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}

	b, err := os.ReadFile(file)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		b = nil
	}
	if hasLine(string(b), st.Line) {
		return nil
	}

	out := string(b)
	if len(out) > 0 && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += st.Line + "\n"
	return os.WriteFile(file, []byte(out), 0o644)
}

// hasLine reports whether line appears as an exact full line in content.
func hasLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if l == line {
			return true
		}
	}
	return false
}
