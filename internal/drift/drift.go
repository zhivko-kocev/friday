// Package drift tracks SHA256 checksums of files friday has written, so push
// and pull can detect external modifications (drift) before overwriting.
package drift

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/textnorm"
)

// Store is the drift tracker, persisted to $UserCacheDir/friday/state.json.
type Store struct {
	path   string
	hashes map[string]string // "adapter:absPath" → sha256hex
}

// DefaultPath returns a project-independent location for the drift store —
// $UserCacheDir/friday/state.json. This works equally well for user-level
// pushes (which always use the same home-rooted targets) and transient
// project pushes (which target absolute paths the user might re-push later).
func DefaultPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "friday", "state.json"), nil
}

func Load(path string) (*Store, error) {
	s := &Store{path: path, hashes: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	return s, json.Unmarshal(data, &s.hashes)
}

// Save writes the store atomically (via temp + rename) after vacuuming entries
// whose target file no longer exists. Keeps the store from growing unboundedly
// as adapters are added and removed over time.
func (s *Store) Save() error {
	s.vacuum()
	data, err := json.MarshalIndent(s.hashes, "", "  ")
	if err != nil {
		return err
	}
	return atomicio.WriteFile(s.path, data, 0o644)
}

// vacuum drops entries whose absPath portion no longer exists on disk. The
// adapter prefix is stripped using the first colon as the separator —
// Windows drive letters (which contain ":") sit safely past that boundary.
func (s *Store) vacuum() {
	for k := range s.hashes {
		i := strings.IndexByte(k, ':')
		if i < 0 {
			continue
		}
		absPath := k[i+1:]
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			delete(s.hashes, k)
		}
	}
}

func (s *Store) key(adapter, absPath string) string { return adapter + ":" + absPath }

func (s *Store) Set(adapter, absPath string, content []byte) {
	s.hashes[s.key(adapter, absPath)] = hash(content)
}

// Check reports whether an on-disk file has drifted from what friday last wrote.
// Returns (drifted=false, exists=false) if the file doesn't exist yet. CRLF
// differences are ignored so a Windows checkout of a LF-authored file won't
// be flagged as drift on every run.
func (s *Store) Check(adapter, absPath string) (drifted, exists bool) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return false, false
	}
	stored, known := s.hashes[s.key(adapter, absPath)]
	if !known {
		// File exists but friday didn't write it — treat as drift so the
		// user gets a chance to inspect before overwriting.
		return true, true
	}
	return hash(data) != stored, true
}

// hash returns the SHA256 of the content after newline normalization, so
// two files that differ only in line endings hash identically.
func hash(data []byte) string {
	h := sha256.Sum256(textnorm.Newlines(data))
	return hex.EncodeToString(h[:])
}
