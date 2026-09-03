// Package store persists local bookkeeping data for the bot in sqlite:
// project-plan invites (see invite_store.go) and runtime-editable settings
// such as the default notification channel (see settings_store.go).
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// discord_user_id/discord_username are empty for email-based invites (no
// Discord account involved); email is empty for Discord-tag invites.
const schema = `
CREATE TABLE IF NOT EXISTS invites (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	discord_user_id   TEXT,
	discord_username  TEXT,
	email             TEXT,
	project_key       TEXT NOT NULL,
	token_id          TEXT,
	created_at        DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS notify_channels (
	channel_id TEXT PRIMARY KEY,
	added_by   TEXT,
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS forum_repo_channels (
	repo_full_name   TEXT PRIMARY KEY,
	repo_url         TEXT NOT NULL,
	forum_channel_id TEXT NOT NULL,
	added_by         TEXT,
	created_at       DATETIME NOT NULL,
	updated_at       DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS github_user_links (
	github_login    TEXT PRIMARY KEY,
	discord_user_id TEXT NOT NULL,
	added_by         TEXT,
	created_at       DATETIME NOT NULL,
	updated_at       DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS forum_posts (
	repo_full_name   TEXT NOT NULL,
	issue_number     INTEGER NOT NULL,
	forum_channel_id TEXT NOT NULL,
	thread_id        TEXT NOT NULL,
	message_id       TEXT NOT NULL DEFAULT '',
	created_at       DATETIME NOT NULL,
	updated_at       DATETIME NOT NULL,
	PRIMARY KEY (repo_full_name, issue_number)
);`

// Store wraps the sqlite connection backing invites and runtime settings.
type Store struct {
	db *sql.DB
}

// Open connects to the sqlite database at path (creating the file and schema
// if needed).
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory %q: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %q: %w", path, err)
	}
	// Driver sqlite ini tidak aman dipakai dengan banyak writer bersamaan.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	// Menambah kolom "email" untuk database lama yang dibuat sebelum fitur
	// undangan by-email ada. sqlite tidak punya "ADD COLUMN IF NOT EXISTS",
	// jadi error "duplicate column" untuk database yang sudah punya kolom ini
	// diabaikan.
	if _, err := db.Exec(`ALTER TABLE invites ADD COLUMN email TEXT`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return nil, fmt.Errorf("migrate email column: %w", err)
	}
	// Menambah kolom "message_id" untuk database yang dibuat sebelum forum post
	// diedit-di-tempat alih-alih dibuat ulang setiap event; baris lama tanpa
	// message_id otomatis membuat post baru sekali lagi (lihat notifier.go),
	// lalu ke depannya sudah terisi.
	if _, err := db.Exec(`ALTER TABLE forum_posts ADD COLUMN message_id TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return nil, fmt.Errorf("migrate forum_posts message_id column: %w", err)
	}
	if err := dropNotNullOnDiscordColumns(db); err != nil {
		return nil, fmt.Errorf("migrate discord columns nullability: %w", err)
	}

	return &Store{db: db}, nil
}

// dropNotNullOnDiscordColumns fixes databases created before email-based
// invites existed, where discord_user_id/discord_username were NOT NULL —
// which made CreateForEmail's inserts (which leave them unset) fail.
// sqlite has no ALTER COLUMN, so the table is rebuilt with the current schema
// and the existing rows are copied over.
func dropNotNullOnDiscordColumns(db *sql.DB) error {
	needsFix, err := columnIsNotNull(db, "discord_user_id")
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if !needsFix {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE invites RENAME TO invites_old`); err != nil {
		return fmt.Errorf("rename old table: %w", err)
	}
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("recreate table: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO invites (id, discord_user_id, discord_username, email, project_key, token_id, created_at)
		SELECT id, discord_user_id, discord_username, email, project_key, token_id, created_at FROM invites_old
	`); err != nil {
		return fmt.Errorf("copy rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE invites_old`); err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}

	return tx.Commit()
}

// columnIsNotNull reports whether the given column on the invites table
// currently has a NOT NULL constraint.
func columnIsNotNull(db *sql.DB, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(invites)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return notNull == 1, nil
		}
	}
	return false, rows.Err()
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
