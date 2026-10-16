package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	tools "github.com/subaud-tools/tools-common"
)

// State holds the user's machine selection.
type State struct {
	RepoDir string `json:"repo_dir"`
	RepoURL string `json:"repo_url,omitempty"`
	// RepoKind is "git" (default/empty) or "tarball"; it tells `update` whether
	// to git-pull the repo or re-fetch and re-extract the tarball at RepoURL.
	RepoKind       string   `json:"repo_kind,omitempty"`
	Profile        string   `json:"profile,omitempty"`
	Packages       []string `json:"packages"`
	AutoApplyFiles bool     `json:"auto_apply_files"`
	// Layers are the machine's chosen layers, in apply order: user layers,
	// then project layers, each in the order added.
	Layers []Layer `json:"layers,omitempty"`
}

// Layer is one layer in the machine's selection.
type Layer struct {
	Name     string      `json:"name"`
	Scope    string      `json:"scope"` // "user" | "project"
	Source   LayerSource `json:"source"`
	Packages []string    `json:"packages"`
	// Project is the checkout a layer added with -project belongs to. Such a
	// layer is held when its file no longer matches Approved (sha256 of the
	// file as last applied).
	Project  string `json:"project,omitempty"`
	Approved string `json:"approved,omitempty"`
}

// LayerSource locates a layer file: File (default kempt.toml) inside Dir. A
// "git" source is a checkout of URL that update pulls; a "path" source is only
// read.
type LayerSource struct {
	Kind string `json:"kind"` // "git" | "path"
	URL  string `json:"url,omitempty"`
	Dir  string `json:"dir"`
	File string `json:"file,omitempty"`
}

// Status holds the cached result of the last refresh check.
type Status struct {
	CheckedAt       time.Time `json:"checked_at"`
	Behind          int       `json:"behind"`
	FileChanges     int       `json:"file_changes"`
	SoftwareChanges int       `json:"software_changes"`
	Blocked         int       `json:"blocked"`
}

// Store is the persistence layer; state.json and status.json live in Dir.
type Store struct{ Dir string }

// DefaultStore resolves XDG_DATA_HOME (or ~/.local/share) joined with "kempt".
func DefaultStore() (*Store, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return &Store{Dir: filepath.Join(base, "kempt")}, nil
}

// Load reads state.json. Missing file returns (&State{}, false, nil).
func (s *Store) Load() (*State, bool, error) {
	data, existed, err := readFile(filepath.Join(s.Dir, "state.json"))
	if err != nil || !existed {
		return &State{}, false, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

// Save writes state.json atomically (tools.WriteFileAtomic), creating Dir as needed.
func (s *Store) Save(st *State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicWrite(s.Dir, "state.json", data)
}

// LoadStatus reads status.json. Missing file returns (&Status{}, false, nil).
func (s *Store) LoadStatus() (*Status, bool, error) {
	data, existed, err := readFile(filepath.Join(s.Dir, "status.json"))
	if err != nil || !existed {
		return &Status{}, false, err
	}
	var st Status
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

// SaveStatus writes status.json atomically, creating Dir as needed.
func (s *Store) SaveStatus(st *Status) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicWrite(s.Dir, "status.json", data)
}

// readFile reads a file; returns (nil, false, nil) if it doesn't exist.
func readFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// atomicWrite writes data to dir/name durably via tools.WriteFileAtomic
// (unique temp, fsync, rename), creating dir as needed.
func atomicWrite(dir, name string, data []byte) error {
	return tools.WriteFileAtomic(filepath.Join(dir, name), data, 0o644)
}
