// Package files provides bounded local text editing with conflict detection.
package files

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const MaxSize = 1024 * 1024

var ErrConflict = errors.New("file changed on disk; reopen it before saving")

type Document struct {
	Path     string
	Text     string
	original [32]byte
	exists   bool
	mode     os.FileMode
}

func Resolve(root, name string) (string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(path))
		if e != nil {
			return "", e
		}
		resolved = filepath.Join(parent, filepath.Base(path))
	} else if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("file must be inside workspace")
	}
	return resolved, nil
}
func read(path string) ([]byte, os.FileMode, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > MaxSize || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return nil, 0, fmt.Errorf("editor supports UTF-8 text files up to 1 MiB")
	}
	return b, info.Mode().Perm(), nil
}
func Open(root, name string) (*Document, error) {
	path, err := Resolve(root, name)
	if err != nil {
		return nil, err
	}
	b, mode, err := read(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !exists {
		mode = 0644
	}
	return &Document{Path: path, Text: string(b), original: sha256.Sum256(b), exists: exists, mode: mode}, nil
}
func (d *Document) Save(text string) error {
	if len(text) > MaxSize || !utf8.ValidString(text) || bytes.IndexByte([]byte(text), 0) >= 0 {
		return fmt.Errorf("editor supports UTF-8 text files up to 1 MiB")
	}
	// Recheck the actual path, including replacement with a symlink.
	parent, err := filepath.EvalSymlinks(filepath.Dir(d.Path))
	if err != nil || parent != filepath.Dir(d.Path) {
		return ErrConflict
	}
	info, err := os.Lstat(d.Path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ErrConflict
	}
	b, _, err := read(d.Path)
	if d.exists {
		if err != nil || sha256.Sum256(b) != d.original {
			return ErrConflict
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	f, err := os.CreateTemp(filepath.Dir(d.Path), ".orkestar-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(d.mode); err == nil {
		_, err = f.WriteString(text)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	latest, _, readErr := read(d.Path)
	if d.exists && (readErr != nil || sha256.Sum256(latest) != d.original) || !d.exists && !errors.Is(readErr, os.ErrNotExist) {
		return ErrConflict
	}
	if err = os.Rename(f.Name(), d.Path); err != nil {
		return err
	}
	d.original = sha256.Sum256([]byte(text))
	d.Text = text
	d.exists = true
	return nil
}
