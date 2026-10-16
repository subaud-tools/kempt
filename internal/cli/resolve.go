package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/subaud-tools/kempt/internal/state"
)

// isManifestURL reports whether ref is an http(s) URL to fetch rather than a
// local path.
func isManifestURL(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

// fetchManifestURL GETs a manifest over http(s). It is a package var so tests
// can stub the network.
var fetchManifestURL = func(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// loadManifestSource reads manifest bytes from a local path, an http(s) URL, or
// "-" (stdin). It also returns the repo dir that repo-relative steps
// (symlink/git-clone `from`) resolve against and a display name for messages.
// A URL or stdin carries no repo tree, so repoDir defaults to the current
// working directory — self-contained manifests (software + merges + downloads)
// need no tree at all.
func loadManifestSource(ref string, stdin io.Reader) (src []byte, repoDir, name string, err error) {
	switch {
	case ref == "-":
		b, e := io.ReadAll(stdin)
		if e != nil {
			return nil, "", "", fmt.Errorf("reading manifest from stdin: %w", e)
		}
		cwd, _ := os.Getwd()
		return b, cwd, "<stdin>", nil
	case isManifestURL(ref):
		b, e := fetchManifestURL(ref)
		if e != nil {
			return nil, "", "", e
		}
		cwd, _ := os.Getwd()
		return b, cwd, ref, nil
	default:
		b, e := os.ReadFile(ref)
		if e != nil {
			return nil, "", "", fmt.Errorf("cannot read %s: %w", ref, e)
		}
		return b, filepath.Dir(ref), ref, nil
	}
}

// splitPackages parses a comma-separated -packages flag value into a slice,
// trimming whitespace and dropping empties. Returns nil for an empty value.
func splitPackages(v string) []string {
	var packages []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			packages = append(packages, p)
		}
	}
	return packages
}

// loadState returns the default store's saved state. It is a package var so
// tests can inject a state.Store backed by a tempdir.
var loadState = func() (*state.State, bool, error) {
	s, err := state.DefaultStore()
	if err != nil {
		return nil, false, err
	}
	return s.Load()
}

// saveState persists state to the default store. It is a package var so tests
// can redirect writes to a tempdir store.
var saveState = func(st *state.State) error {
	s, err := state.DefaultStore()
	if err != nil {
		return err
	}
	return s.Save(st)
}

// resolveManifest picks the manifest path. An explicit flag (non-empty) always
// wins. Otherwise, when saved state exists, the manifest lives at
// <RepoDir>/kempt.toml. With no flag and no state, fall back to "kempt.toml".
func resolveManifest(flagVal string, st *state.State, existed bool) string {
	if flagVal != "" {
		return flagVal
	}
	if existed && st != nil && st.RepoDir != "" {
		return filepath.Join(st.RepoDir, "kempt.toml")
	}
	return "kempt.toml"
}

// resolveSelection picks the profile/packages selection. Explicit flags win
// (profile XOR packages, validated downstream by engine.Select). Otherwise the
// saved selection is used. With neither, returns ("", nil) meaning all packages.
func resolveSelection(profileFlag string, packagesFlag []string, st *state.State, existed bool) (profile string, packages []string) {
	if profileFlag != "" || len(packagesFlag) > 0 {
		return profileFlag, packagesFlag
	}
	if existed && st != nil {
		// init expands the chosen profile into a concrete package list stored in
		// st.Packages. State.Profile is informational only and must NOT be passed
		// to engine.Select alongside the package list (that would trigger the
		// "use --profile or --packages, not both" error).
		return "", st.Packages
	}
	return "", nil
}
