package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps sqlx.DB
type DB struct {
	*sqlx.DB
}

// New creates a new database connection
func New(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Connect with WAL mode and foreign keys enabled
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sqlx.Connect("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// SQLite allows one writer at a time: a single connection avoids
	// "database is locked" errors between concurrent goroutines
	db.SetMaxOpenConns(1)

	return &DB{db}, nil
}

// Migrate runs database migrations
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Columns added after the first release
	for _, col := range addedColumns {
		exists, err := db.columnExists(ctx, col.table, col.name)
		if err != nil {
			return fmt.Errorf("failed to inspect %s: %w", col.table, err)
		}
		if exists {
			continue
		}
		query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.name, col.definition)
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to add column %s.%s: %w", col.table, col.name, err)
		}
	}
	return nil
}

func (db *DB) columnExists(ctx context.Context, table, column string) (bool, error) {
	var count int
	err := db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column)
	return count > 0, err
}
