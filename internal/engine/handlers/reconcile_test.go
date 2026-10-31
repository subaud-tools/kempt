package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func TestDeclaredHookRemovalConverges(t *testing.T) {
	ctx := testCtx(t)
	m, findings := manifest.Parse([]byte(`[kempt]
spec = 2
[[packages.muster.json-merge]]
file = "~/settings.json"
remove-hooks = [{event="Stop", matcher="", commands=["muster hook Stop claude", "~/.local/bin/muster hook Stop claude"]}]
merge = {hooks={Stop=[{hooks=[{type="command", command="~/.local/bin/muster hook Stop claude"}]}]}}
`))
	if len(findings) > 0 {
		t.Fatal(findings)
	}
	if findings = manifest.Validate(m); len(findings) > 0 {
		t.Fatal(findings)
	}
	s := m.Packages["muster"].Steps[0]
	file := filepath.Join(ctx.Home, "settings.json")
	before := `{"hooks":{"Stop":[{"extra":"keep","hooks":[{"type":"command","command":"muster hook Stop claude"},{"type":"command","command":"unrelated"}]},{"hooks":[{"type":"command","command":"~/.local/bin/muster hook Stop claude"}]},{"matcher":"different","hooks":[{"type":"command","command":"muster hook Stop claude"}]}]}}`
	if err := os.WriteFile(file, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	h := jsonHandler(t)
	for i := 0; i < 2; i++ {
		d, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		want := engine.OpChange
		if i > 0 {
			want = engine.OpNoop
		}
		if d.Op != want {
			t.Fatalf("iteration %d: %+v", i, d)
		}
		if err := h.Apply(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	var want any
	if err := json.Unmarshal([]byte(`{"hooks":{"Stop":[{"extra":"keep","hooks":[{"type":"command","command":"unrelated"}]},{"matcher":"different","hooks":[{"type":"command","command":"muster hook Stop claude"}]},{"hooks":[{"type":"command","command":"~/.local/bin/muster hook Stop claude"}]}]}}`), &want); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, file); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestDeclaredMarkerAdoption(t *testing.T) {
	for _, tc := range []struct {
		name, cur, want string
		blocked         bool
	}{
		{"owned", "---\nname: muda\nmuda-owned: muda\n---\nold", "---\nname: muda\nmuda-owned: muda\n---\nnew", false},
		{"foreign current", "---\nname: muda\n---\nold", "---\nname: muda\nmuda-owned: muda\n---\nnew", true},
		{"identical foreign", "---\nname: muda\n---\nforeign", "---\nname: muda\n---\nforeign", true},
		{"foreign desired", "---\nname: muda\nmuda-owned: muda\n---\nold", "---\nname: muda\n---\nnew", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := managedCtx(t)
			m, f := manifest.Parse([]byte(`[kempt]
spec=2
[[packages.integration.managed-file]]
from="muda/SKILL.md"
to="~/.claude/skills/muda/SKILL.md"
mode="0644"
adopt-markers={muda-owned="muda"}
`))
			if len(f) > 0 {
				t.Fatal(f)
			}
			if f = manifest.Validate(m); len(f) > 0 {
				t.Fatal(f)
			}
			if f = manifest.BindSetup(m, "muda", "1.0.0", map[string][]byte{"muda/SKILL.md": []byte(tc.want)}); len(f) > 0 {
				t.Fatal(f)
			}
			s := m.Packages["integration"].Steps[0]
			dest := filepath.Join(ctx.Home, ".claude/skills/muda/SKILL.md")
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte(tc.cur), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.blocked {
				if d := inspect(t, ctx, s); d.Op != engine.OpBlocked {
					t.Fatal(d)
				}
				if readString(t, dest) != tc.cur {
					t.Fatal("foreign content changed")
				}
			} else {
				converge(t, ctx, s)
			}
		})
	}
}
