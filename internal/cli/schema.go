package cli

import (
	"io"

	"github.com/subaud-tools/kempt/internal/schema"
)

func init() {
	Register(Command{
		Name:     "schema",
		Summary:  "print the JSON Schema for kempt.toml",
		Synopsis: "schema",
		Help:     "Prints the JSON Schema for kempt.toml.",
		Run:      runSchema,
	})
}

func runSchema(args []string, out, errw io.Writer) error {
	if len(args) > 0 {
		return UsageError{Msg: "usage: kempt schema"}
	}
	_, _ = out.Write(schema.JSON())
	_, _ = io.WriteString(out, "\n")
	return nil
}
