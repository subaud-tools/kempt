package state

import (
	"encoding/json"
	"path/filepath"
)

// ManagedFile is the ownership record of one release-owned file: the tool
// whose setup bundle supplied it, its destination, and the sha256 (hex) of the
// content kempt last wrote or adopted there.
type ManagedFile struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// managedName is the file in Dir that holds the ManagedFile records.
const managedName = "managed-files.json"

// ManagedFile returns the record for destination path, if there is one.
func (s *Store) ManagedFile(path string) (ManagedFile, bool, error) {
	files, err := s.loadManaged()
	if err != nil {
		return ManagedFile{}, false, err
	}
	for _, f := range files {
		if f.Path == path {
			return f, true, nil
		}
	}
	return ManagedFile{}, false, nil
}

// RecordManagedFile saves f, replacing any record for the same destination,
// atomically. Call it only after the file's content is in place.
func (s *Store) RecordManagedFile(f ManagedFile) error {
	files, err := s.loadManaged()
	if err != nil {
		return err
	}
	replaced := false
	for i := range files {
		if files[i].Path == f.Path {
			files[i], replaced = f, true
		}
	}
	if !replaced {
		files = append(files, f)
	}
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	return atomicWrite(s.Dir, managedName, data)
}

func (s *Store) loadManaged() ([]ManagedFile, error) {
	data, existed, err := readFile(filepath.Join(s.Dir, managedName))
	if err != nil || !existed {
		return nil, err
	}
	var files []ManagedFile
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, err
	}
	return files, nil
}
