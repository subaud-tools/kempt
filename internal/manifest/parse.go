package manifest

import (
	"github.com/BurntSushi/toml"
)

type rawManifest struct {
	Kempt struct {
		Spec  int    `toml:"spec"`
		Layer string `toml:"layer"`
	} `toml:"kempt"`
	Packages map[string]rawPackage `toml:"packages"`
	Profiles map[string]rawProfile `toml:"profiles"`
	Doctor   *DoctorConfig         `toml:"doctor"`
}

type rawProfile struct {
	Description string   `toml:"description"`
	Packages    []string `toml:"packages"`
}

type rawPackage struct {
	Description   string              `toml:"description"`
	Needs         []string            `toml:"needs"`
	Only          *Only               `toml:"only"`
	Notes         []string            `toml:"notes"`
	Install       []InstallStep       `toml:"install"`
	GithubRelease []GithubReleaseStep `toml:"github-release"`
	Download      []DownloadStep      `toml:"download"`
	GitClone      []GitCloneStep      `toml:"git-clone"`
	Service       []ServiceStep       `toml:"service"`
	Symlink       []SymlinkStep       `toml:"symlink"`
	ManagedFile   []ManagedFileStep   `toml:"managed-file"`
	JSONMerge     []JSONMergeStep     `toml:"json-merge"`
	TomlMerge     []TomlMergeStep     `toml:"toml-merge"`
	LineInFile    []LineInFileStep    `toml:"line-in-file"`
	Verify        []VerifyStep        `toml:"verify"`
}

// isFreeform reports whether an undecoded key falls within the free-form
// `merge` subtree of a json-merge or toml-merge step. The key must start with
// "packages" and contain adjacent segments `json-merge`/`toml-merge` then
// `merge`, so unrelated paths like a top-level `json-merge.merge` are not
// suppressed.
func isFreeform(key toml.Key) bool {
	if len(key) == 0 || key[0] != "packages" {
		return false
	}
	for i := 0; i+1 < len(key); i++ {
		if (key[i] == "json-merge" || key[i] == "toml-merge") && key[i+1] == "merge" {
			return true
		}
	}
	return false
}

func Parse(src []byte) (*Manifest, []Finding) {
	var raw rawManifest
	md, err := toml.Decode(string(src), &raw)
	if err != nil {
		return nil, []Finding{{Path: "kempt.toml", Msg: err.Error()}}
	}
	var findings []Finding
	for _, key := range md.Undecoded() {
		if isFreeform(key) {
			// The `merge` field of a json-merge step is intentionally
			// free-form (map[string]any). BurntSushi reports its nested
			// keys as undecoded, so they must not be treated as unknown.
			continue
		}
		findings = append(findings, Finding{Path: key.String(), Msg: "unknown key"})
	}
	m := &Manifest{
		Spec:     raw.Kempt.Spec,
		Layer:    raw.Kempt.Layer,
		Packages: map[string]*Package{},
		Profiles: map[string]*Profile{},
		Doctor:   raw.Doctor,
	}
	for name, rp := range raw.Packages {
		m.Packages[name] = &Package{
			Name:        name,
			Description: rp.Description,
			Needs:       rp.Needs,
			Only:        rp.Only,
			Steps:       interleave(md, name, rp),
			Notes:       rp.Notes,
		}
	}
	for name, pr := range raw.Profiles {
		m.Profiles[name] = &Profile{Name: name, Description: pr.Description, Packages: pr.Packages}
	}
	return m, findings
}

// interleave returns the package's steps in source order. md.Keys() lists
// every defined key in document order; each [[packages.<pkg>.<kind>]] table
// re-lists the key "packages.<pkg>.<kind>" once per element, in order. Walk
// those occurrences and pop from the corresponding typed slice.
//
// Bounds invariant: the index i inside pop is always < len(slice). BurntSushi
// records exactly one md.Keys() entry per decoded array-of-tables element in
// the same decode pass, so the occurrence count for each kind equals the
// length of the corresponding typed slice in rawPackage. No additional bounds
// guard is needed.
func interleave(md toml.MetaData, pkg string, rp rawPackage) []Step {
	idx := map[string]int{}
	pop := func(kind string) Step {
		i := idx[kind]
		idx[kind] = i + 1
		switch kind {
		case "install":
			return rp.Install[i]
		case "github-release":
			return rp.GithubRelease[i]
		case "download":
			return rp.Download[i]
		case "git-clone":
			return rp.GitClone[i]
		case "service":
			return rp.Service[i]
		case "symlink":
			return rp.Symlink[i]
		case "managed-file":
			return rp.ManagedFile[i]
		case "json-merge":
			return rp.JSONMerge[i]
		case "toml-merge":
			return rp.TomlMerge[i]
		case "line-in-file":
			return rp.LineInFile[i]
		case "verify":
			return rp.Verify[i]
		}
		return nil
	}
	kinds := map[string]bool{"install": true, "github-release": true, "download": true,
		"git-clone": true, "service": true, "symlink": true, "managed-file": true, "json-merge": true,
		"toml-merge": true, "line-in-file": true, "verify": true}
	var steps []Step
	for _, key := range md.Keys() {
		parts := []string(key)
		// BurntSushi emits the array key once per [[element]], but it also emits
		// child keys (e.g. packages.x.symlink.from) — the len==3 guard filters those.
		if len(parts) != 3 || parts[0] != "packages" || parts[1] != pkg || !kinds[parts[2]] {
			continue
		}
		steps = append(steps, pop(parts[2]))
	}
	return steps
}
