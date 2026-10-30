package manifest

import (
	"cmp"
	"github.com/subaud-tools/kempt/internal/state"
)

// Class is the safety class of a primitive step. It is a code constant derived
// from the step kind, never a manifest field.
type Class int

const (
	ClassSoftware Class = iota
	ClassFiles
	ClassReadOnly
)

type Manifest struct {
	Spec int
	// Layer is the [kempt] layer scope: "" for a base manifest, "user" or
	// "project" for a layer (see docs/superpowers/specs/2026-09-29-layers-design.md).
	Layer    string
	Packages map[string]*Package
	Profiles map[string]*Profile
	Doctor   *DoctorConfig
}

// DoctorConfig is the optional top-level [doctor] block tuning read-only health
// checks. CheckNpmOrphans opts in to reporting orphaned global npm packages;
// Ignore suppresses specific installed identities (exact, npm:-prefixed, or
// glob: patterns).
type DoctorConfig struct {
	CheckNpmOrphans bool     `toml:"checkNpmOrphans"`
	Ignore          []string `toml:"ignore"`
}

type Package struct {
	Resolved    *state.ResolvedRelease
	SetupParent string
	Name        string
	Description string
	Needs       []string
	Only        *Only
	Steps       []Step // document order
	Notes       []string
	// Layer is the layer that declared this package ("" for the base) and
	// Root the directory its relative paths resolve against ("" for the
	// base: the context's repo). Both are set by layer composition.
	Layer string
	Root  string
}

type Profile struct {
	Name        string
	Description string
	Packages    []string
}

type Only struct {
	OS   string `toml:"os"`
	Arch string `toml:"arch"`
}

type Step interface {
	Kind() string // "install", "symlink", ...
	Class() Class
}

type Finding struct {
	// Path uses per-kind step indices: packages.core.symlink[1] is the second
	// [[packages.core.symlink]] table, regardless of other kinds interleaved.
	Path string
	Msg  string
}

type InstallStep struct {
	Brew   *BrewSpec `toml:"brew"`
	Winget []string  `toml:"winget"`
	Apt    []string  `toml:"apt"`
	// Npm and Pi are ADDITIVE cross-platform package sources: unlike the
	// OS-exclusive brew/winget/apt choice (one wins per OS), a single install
	// step may carry brew AND npm AND pi, and every present source applies.
	Npm  []string `toml:"npm"`
	Pi   []string `toml:"pi"`
	Only *Only    `toml:"only"`
}

type BrewSpec struct {
	Formulas []string `toml:"formulas"`
	Casks    []string `toml:"casks"`
	Taps     []string `toml:"taps"`
}

type GithubReleaseStep struct {
	Repo  string `toml:"repo"`
	Asset string `toml:"asset"`
	Bin   string `toml:"bin"`
	Only  *Only  `toml:"only"`
}

type DownloadStep struct {
	// CandidateVersion is upgrade's exact reviewed target, not a manifest pin.
	CandidateVersion string                 `toml:"-" json:"-"`
	Held             string                 `toml:"-" json:"-"`
	Resolved         *state.ResolvedRelease `toml:"-" json:"-"`
	Asset, Checksums []byte                 `toml:"-" json:"-"`
	ResolutionError  string                 `toml:"-" json:"-"`
	Site             string                 `toml:"site"`    // e.g. "tackle.tools"
	Tool             string                 `toml:"tool"`    // e.g. "proj"
	Product          string                 `toml:"product"` // what /links is asked for on a gated site; "" → Tool
	Version          string                 `toml:"version"` // "latest" (default when empty) or pinned "1.4.0"
	Bin              string                 `toml:"bin"`     // binary inside tarball + installed name
	// Setup (spec 2) opts in to the release's setup bundle: its
	// [packages.integration] fragment joins the plan at this version.
	Setup bool  `toml:"setup"`
	Only  *Only `toml:"only"`
}

// ProductName is what the access service is asked for: Product, else Tool.
func (s DownloadStep) ProductName() string { return cmp.Or(s.Product, s.Tool) }

type GitCloneStep struct {
	Repo string `toml:"repo"`
	To   string `toml:"to"`
	Ref  string `toml:"ref"`
	Only *Only  `toml:"only"`
}

type ServiceStep struct {
	Label            string            `toml:"label"`
	Program          []string          `toml:"program"`
	Env              map[string]string `toml:"env"`          // EnvironmentVariables
	Stdout           string            `toml:"stdout"`       // StandardOutPath (~ expanded)
	Stderr           string            `toml:"stderr"`       // StandardErrorPath (~ expanded)
	KeepAlive        *bool             `toml:"keep-alive"`   // default true when nil
	RunAtLoad        *bool             `toml:"run-at-load"`  // default true when nil
	ProcessType      string            `toml:"process-type"` // e.g. "Interactive"
	ThrottleInterval *int              `toml:"throttle-interval"`
	StartInterval    *int              `toml:"start-interval"` // StartInterval seconds: a periodic job (requires keep-alive = false)
	SessionType      string            `toml:"session-type"`   // LimitLoadToSessionType, e.g. "Aqua"
	Only             *Only             `toml:"only"`
}

// ManagedFileStep (spec 2) writes one release-owned file: From is a regular
// file relative to the verified setup bundle's files/, To the destination
// (~ and relative paths expand as for other steps), Mode "0644" or "0755".
type ManagedFileStep struct {
	From string `toml:"from"`
	To   string `toml:"to"`
	Mode string `toml:"mode"`
	Only *Only  `toml:"only"`
	// Source is From's bytes in the verified setup bundle, set by BindSetup
	// and never by a manifest; nil means no verified bundle supplied it.
	Source *SetupSource `toml:"-" json:"-"`
}

// SetupSource is a managed file's content and the release that supplied it.
type SetupSource struct {
	Tool, Version string
	Data          []byte
}

type SymlinkStep struct {
	From   string `toml:"from"`
	To     string `toml:"to"`
	Backup bool   `toml:"backup"`
	Only   *Only  `toml:"only"`
}

type JSONMergeStep struct {
	File   string         `toml:"file"`
	Merge  map[string]any `toml:"merge"`
	Remove []string       `toml:"remove"` // dotted key paths deleted before the merge
	Arrays string         `toml:"arrays"` // ""|"append"|"replace"; empty == "append"
	Only   *Only          `toml:"only"`
}

type TomlMergeStep struct {
	File  string         `toml:"file"`
	Merge map[string]any `toml:"merge"`
	Only  *Only          `toml:"only"`
}

type LineInFileStep struct {
	File string `toml:"file"`
	Line string `toml:"line"`
	Only *Only  `toml:"only"`
}

type VerifyStep struct {
	// Offline marks a cached release check whose remote inputs cannot be read.
	Offline          bool                `toml:"-" json:"-"`
	CommandExists    string              `toml:"command-exists"`
	CommandExistsAny []string            `toml:"command-exists-any"`
	HTTPOk           string              `toml:"http-ok"`
	SymlinkTarget    *SymlinkTargetCheck `toml:"symlink-target"`
	VersionCurrent   *VersionCheck       `toml:"version-current"`
	Only             *Only               `toml:"only"`
}

type SymlinkTargetCheck struct {
	Link   string `toml:"link"`
	Target string `toml:"target"`
}

type VersionCheck struct {
	Repo    string `toml:"repo"`
	Command string `toml:"command"`
}

func (InstallStep) Kind() string { return "install" }
func (InstallStep) Class() Class { return ClassSoftware }

func (GithubReleaseStep) Kind() string { return "github-release" }
func (GithubReleaseStep) Class() Class { return ClassSoftware }

func (DownloadStep) Kind() string { return "download" }
func (DownloadStep) Class() Class { return ClassSoftware }

func (GitCloneStep) Kind() string { return "git-clone" }
func (GitCloneStep) Class() Class { return ClassSoftware }

func (ServiceStep) Kind() string { return "service" }
func (ServiceStep) Class() Class { return ClassSoftware }

func (SymlinkStep) Kind() string { return "symlink" }
func (SymlinkStep) Class() Class { return ClassFiles }

func (ManagedFileStep) Kind() string { return "managed-file" }
func (ManagedFileStep) Class() Class { return ClassFiles }

func (JSONMergeStep) Kind() string { return "json-merge" }
func (JSONMergeStep) Class() Class { return ClassFiles }

func (TomlMergeStep) Kind() string { return "toml-merge" }
func (TomlMergeStep) Class() Class { return ClassFiles }

func (LineInFileStep) Kind() string { return "line-in-file" }
func (LineInFileStep) Class() Class { return ClassFiles }

func (VerifyStep) Kind() string { return "verify" }
func (VerifyStep) Class() Class { return ClassReadOnly }
