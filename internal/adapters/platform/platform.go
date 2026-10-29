// Package platform holds OS-specific helpers.
package platform

import (
	"io"
	"os"
	"path/filepath"
)

// DataDir returns (creating it) the per-user data directory.
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "docsreader")
	return dir, os.MkdirAll(filepath.Join(dir, "books"), 0o700)
}

// FileStore implements app.FileStore on the local disk.
type FileStore struct{ Dir string }

func (s FileStore) Put(id, ext string, r io.Reader) (string, error) {
	p := filepath.Join(s.Dir, id+ext)
	tmp := p + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

func (s FileStore) Remove(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s FileStore) Rename(p, id, ext string) (string, error) {
	dst := filepath.Join(s.Dir, id+ext)
	return dst, os.Rename(p, dst)
}
