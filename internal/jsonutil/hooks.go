package jsonutil

import (
	"strings"

	"github.com/subaud-tools/tools-common/hookconfig"
)

// HookRemovals expands declaration tokens, never strings from the live document.
func HookRemovals(rules []hookconfig.Removal, home string) []hookconfig.Removal {
	out := make([]hookconfig.Removal, len(rules))
	for i, r := range rules {
		out[i] = hookconfig.Removal{Event: strings.ReplaceAll(r.Event, "${HOME}", home), Matcher: strings.ReplaceAll(r.Matcher, "${HOME}", home)}
		for _, c := range r.Commands {
			out[i].Commands = append(out[i].Commands, strings.ReplaceAll(c, "${HOME}", home))
		}
	}
	return out
}

// RemoveHooks copies before calling the shared mutating predicate. Parsed and
// folded declarations can therefore be planned and applied repeatedly.
func RemoveHooks(v any, rules []hookconfig.Removal, home string) any {
	if len(rules) == 0 {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	copyDoc, _ := ToAny(m).(map[string]any)
	hookconfig.Remove(copyDoc, HookRemovals(rules, home))
	return copyDoc
}
