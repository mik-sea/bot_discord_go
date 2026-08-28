// Package store persists local bookkeeping data for the bot in sqlite.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS invites (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	discord_user_id   TEXT NOT NULL,
	discord_username  TEXT NOT NULL,
	project_key       TEXT NOT NULL,
	token_id          TEXT,
	created_at        DATETIME NOT NULL
);`

// InviteStore persists project-plan invites created via the /invite command.
type InviteStore struct {
	db *sql.DB
}

// Open connects to the sqlite database at path (creating the file and schema
// if needed).
func Open(path string) (*InviteStore, error) {
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

	return &InviteStore{db: db}, nil
}

// Close releases the underlying database connection.
func (s *InviteStore) Close() error {
	return s.db.Close()
}

// Create inserts a new invite row and returns its generated ID.
func (s *InviteStore) Create(ctx context.Context, discordUserID, discordUsername, projectKey string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (discord_user_id, discord_username, project_key, created_at) VALUES (?, ?, ?, ?)`,
		discordUserID, discordUsername, projectKey, time.Now().UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert invite: %w", err)
	}
	return res.LastInsertId()
}

// SetTokenID stores the plan API's invite token against an existing invite row.
func (s *InviteStore) SetTokenID(ctx context.Context, id int64, tokenID string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE invites SET token_id = ? WHERE id = ?`, tokenID, id); err != nil {
		return fmt.Errorf("update invite token_id: %w", err)
	}
	return nil
}
