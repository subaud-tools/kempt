package cli

import (
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/state"
)

func TestResolveManifest(t *testing.T) {
	st := &state.State{RepoDir: "/repo"}
	tests := []struct {
		name    string
		flagVal string
		st      *state.State
		existed bool
		want    string
	}{
		{"flag wins", "custom.toml", st, true, "custom.toml"},
		{"empty + state", "", st, true, filepath.Join("/repo", "kempt.toml")},
		{"empty + no state", "", &state.State{}, false, "kempt.toml"},
		{"flag wins over no state", "custom.toml", &state.State{}, false, "custom.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveManifest(tt.flagVal, tt.st, tt.existed); got != tt.want {
				t.Fatalf("resolveManifest = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveSelection(t *testing.T) {
	saved := &state.State{Profile: "dev", Packages: []string{"a", "b"}}
	tests := []struct {
		name         string
		profileFlag  string
		packagesFlag []string
		st           *state.State
		existed      bool
		wantProfile  string
		wantPackages []string
	}{
		{"profile flag wins", "prod", nil, saved, true, "prod", nil},
		{"packages flag wins", "", []string{"x"}, saved, true, "", []string{"x"}},
		// init expands the profile into a concrete package list; State.Profile is
		// informational only. The resolver must return ("", st.Packages) so that
		// engine.Select never sees both non-empty, which would be rejected.
		{"saved selection", "", nil, saved, true, "", []string{"a", "b"}},
		{"empty no state", "", nil, &state.State{}, false, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, pkgs := resolveSelection(tt.profileFlag, tt.packagesFlag, tt.st, tt.existed)
			if p != tt.wantProfile {
				t.Fatalf("profile = %q, want %q", p, tt.wantProfile)
			}
			if len(pkgs) != len(tt.wantPackages) {
				t.Fatalf("packages = %v, want %v", pkgs, tt.wantPackages)
			}
			for i := range pkgs {
				if pkgs[i] != tt.wantPackages[i] {
					t.Fatalf("packages = %v, want %v", pkgs, tt.wantPackages)
				}
			}
		})
	}
}
