package state

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ServiceActivation records only an activation kempt has begun but not
// successfully bootstrapped. It is not a claim about live service health.
type ServiceActivation struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	UID    int    `json:"uid"`
	Digest string `json:"digest"`
}

func (s *Store) serviceActivationPath(a ServiceActivation) string {
	key := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", a.UID, a.Label, a.Path)))
	return filepath.Join(s.Dir, "pending-services", fmt.Sprintf("%x.json", key))
}

// PendingServiceActivation reports whether this exact desired activation is pending.
func (s *Store) PendingServiceActivation(a ServiceActivation) (bool, error) {
	data, exists, err := readFile(s.serviceActivationPath(a))
	if err != nil || !exists {
		return false, err
	}
	var pending ServiceActivation
	if err := json.Unmarshal(data, &pending); err != nil {
		return false, err
	}
	return pending == a, nil
}

// RecordServiceActivation must succeed before writing the plist or activating it.
func (s *Store) RecordServiceActivation(a ServiceActivation) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	path := s.serviceActivationPath(a)
	return atomicWrite(filepath.Dir(path), filepath.Base(path), data)
}

// ClearServiceActivation is called only after bootstrap succeeds.
func (s *Store) ClearServiceActivation(a ServiceActivation) error {
	err := os.Remove(s.serviceActivationPath(a))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
