package store

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database connection and provides persistence APIs.
type Store struct {
	db *sql.DB
}

// NewStore opens the SQLite database at the specified path and runs schema migrations.
func NewStore(dbPath string) (*Store, error) {
	// Ensure the parent directory exists
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, err
	}

	connStr := dbPath + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	// Optimize SQLite performance and enforce foreign keys
	pragmaQuery := `
	PRAGMA journal_mode=WAL;
	PRAGMA foreign_keys=ON;
	PRAGMA busy_timeout=30000;
	`
	if _, err := db.Exec(pragmaQuery); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
