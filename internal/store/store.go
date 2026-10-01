// Package store keeps API keys and usage rows in one SQLite file
// (modernc.org/sqlite, pure Go). It never stores plaintext keys or content.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed schema.sql
var schema string

const schemaVersion = 1

// ErrNotFound is returned when a key does not exist (or is not active, for
// KeyByHash).
var ErrNotFound = errors.New("store: not found")

// Store is the SQLite-backed key and usage store. It uses one connection
// (DECISIONS D4): at ≤ 30 users that is a handful of queries per second.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
// The file is created with mode 0600 so SQLite's -wal and -shm files, which
// copy the database file's mode, are 0600 too.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open file: %w", err)
	}
	_ = f.Close()

	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: read schema version: %w", err)
	}
	if version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("store: database schema version %d is newer than this binary (%d)", version, schemaVersion)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database; SQLite checkpoints the WAL on the last close.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

func nullMS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
