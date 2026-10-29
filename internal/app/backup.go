package app

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DBName is the database file name inside a data directory and a backup.
const DBName = "docsreader.db"

// Snapshotter writes a consistent copy of the database.
type Snapshotter interface {
	Snapshot(ctx context.Context, dst string) error
}

// Backup packs the database and all book files into a zip archive.
type Backup struct {
	DB       Snapshotter
	BooksDir string
	TempDir  string
}

// WriteZip streams the archive to w.
func (b *Backup) WriteZip(ctx context.Context, w io.Writer) error {
	tmp, err := os.MkdirTemp(b.TempDir, "backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, DBName)
	if err := b.DB.Snapshot(ctx, snap); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	zw := zip.NewWriter(w)
	if err := addFile(zw, snap, DBName); err != nil {
		return err
	}
	entries, err := os.ReadDir(b.BooksDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		if err := addFile(zw, filepath.Join(b.BooksDir, e.Name()), "books/"+e.Name()); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// Restore unpacks a backup into dataDir. An existing database and books are
// moved aside to <name>.before-restore rather than overwritten, so a wrong
// restore can be undone. The app must not be running.
func Restore(zipPath, dataDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	hasDB := false
	for _, f := range zr.File {
		hasDB = hasDB || f.Name == DBName
	}
	if !hasDB {
		return fmt.Errorf("%s is not a docsreader backup (no %s inside)", zipPath, DBName)
	}
	for _, name := range []string{DBName, DBName + "-wal", DBName + "-shm", "books"} {
		p := filepath.Join(dataDir, name)
		if _, err := os.Stat(p); err == nil {
			if err := os.Rename(p, p+".before-restore"); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "books"), 0o700); err != nil {
		return err
	}
	for _, f := range zr.File {
		// Only the database and flat files under books/ are accepted; this also
		// rules out path traversal ("../") in a hostile archive.
		var dst string
		switch {
		case f.Name == DBName:
			dst = filepath.Join(dataDir, DBName)
		case strings.HasPrefix(f.Name, "books/") && !strings.Contains(f.Name[len("books/"):], "/") && !strings.Contains(f.Name, "\\") && f.Name != "books/":
			dst = filepath.Join(dataDir, filepath.FromSlash(f.Name))
		default:
			continue
		}
		if err := extract(f, dst); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
