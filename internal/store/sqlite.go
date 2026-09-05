// Package store owns durable metadata. Terminal output and credentials are not
// metadata and are never stored here implicitly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store interface {
	Load(context.Context) ([]byte, error)
	Save(context.Context, []byte) error
	Close() error
}
type SQLite struct{ db *sql.DB }

func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	_ = f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=2000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; CREATE TABLE IF NOT EXISTS metadata (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL, data BLOB NOT NULL)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize metadata store: %w", err)
	}
	return &SQLite{db: db}, nil
}
func (s *SQLite) Load(ctx context.Context) ([]byte, error) {
	var b []byte
	var version int
	err := s.db.QueryRowContext(ctx, "SELECT version,data FROM metadata WHERE id=1").Scan(&version, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported metadata version %d", version)
	}
	return b, nil
}
func (s *SQLite) Save(ctx context.Context, b []byte) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO metadata(id,version,data) VALUES(1,1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", b)
	return err
}
func (s *SQLite) Close() error { return s.db.Close() }
