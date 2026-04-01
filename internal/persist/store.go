package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store provides JSON file persistence with atomic writes.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New creates a Store rooted at the given directory.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("persist: create dir %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Save atomically writes v as JSON to filename within the store directory.
func (s *Store) Save(filename string, v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SaveFile(filepath.Join(s.dir, filename), v)
}

// Load reads JSON from filename into v. Returns os.ErrNotExist if missing.
func (s *Store) Load(filename string, v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return LoadFile(filepath.Join(s.dir, filename), v)
}

// SaveFile atomically writes v as JSON to path.
func SaveFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadFile reads JSON from path into v.
func LoadFile(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
