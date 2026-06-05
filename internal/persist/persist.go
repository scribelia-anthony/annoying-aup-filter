// Package persist holds the on-disk JSON helpers used by rules, fallback
// and intercept to survive restarts. Writes are atomic (temp + rename).
package persist

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteJSON marshals v with indentation and writes it to path atomically.
// The parent directory is created if missing.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".annoying-aup-filter-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ReadJSON unmarshals path into v. Returns os.ErrNotExist if the file is
// absent so callers can branch on errors.Is(err, os.ErrNotExist).
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
