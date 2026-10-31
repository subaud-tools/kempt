package manifest

import (
	"strings"
	"testing"
)

func TestReconciliationValidation(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		valid     bool
	}{
		{"remove only", `[[packages.muster.json-merge]]
file="~/settings.json"
remove-hooks=[{event="Stop",matcher="",commands=["muster hook Stop claude"]}]`, true},
		{"missing event", `[[packages.muster.json-merge]]
file="~/settings.json"
merge={a=true}
remove-hooks=[{commands=["muster hook Stop claude"]}]`, false},
		{"missing commands", `[[packages.muster.json-merge]]
file="~/settings.json"
merge={a=true}
remove-hooks=[{event="Stop"}]`, false},
		{"markers not skill", `[[packages.muster.managed-file]]
from="wrapper"
to="~/wrapper"
mode="0644"
adopt-markers={muda-owned="muda"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f := Parse([]byte("[kempt]\nspec=2\n" + tc.src))
			if len(f) == 0 {
				f = Validate(m)
			}
			if (len(f) == 0) != tc.valid {
				t.Fatalf("valid=%v findings=%v", tc.valid, f)
			}
			if tc.valid {
				m, f = Parse([]byte("[kempt]\nspec=1\n" + tc.src))
				if len(f) == 0 {
					f = Validate(m)
				}
				if len(f) == 0 || !strings.Contains(f[0].Msg, "spec 2") {
					t.Fatalf("spec1: %v", f)
				}
			}
		})
	}
}
