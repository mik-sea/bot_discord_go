// Package store persists local bookkeeping data for the bot in sqlite.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/miksea/bot_discord_go/internal/clock"
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

CREATE TABLE IF NOT EXISTS notification_channels (
	channel_id        TEXT PRIMARY KEY,
	added_by          TEXT NOT NULL,
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
	// Menambah kolom "email" untuk database lama yang dibuat sebelum fitur
	// undangan by-email ada. sqlite tidak punya "ADD COLUMN IF NOT EXISTS",
	// jadi error "duplicate column" untuk database yang sudah punya kolom ini
	// diabaikan.
	if _, err := db.Exec(`ALTER TABLE invites ADD COLUMN email TEXT`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return nil, fmt.Errorf("migrate email column: %w", err)
	}
	if err := dropNotNullOnDiscordColumns(db); err != nil {
		return nil, fmt.Errorf("migrate discord columns nullability: %w", err)
	}

	return &InviteStore{db: db}, nil
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
func (s *InviteStore) Close() error {
	return s.db.Close()
}

// CreateForUser inserts a new invite row for a tagged Discord user and
// returns its generated ID. email is the derived (non-functional) address
// sent to the plan API — stored alongside for record-keeping only, nothing
// is actually sent to it. Callers should only call this after the plan API
// has confirmed the invite, so only successful invites end up in the table.
func (s *InviteStore) CreateForUser(ctx context.Context, discordUserID, discordUsername, email, projectKey, tokenID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (discord_user_id, discord_username, email, project_key, token_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		discordUserID, discordUsername, email, projectKey, tokenID, clock.Now(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert invite: %w", err)
	}
	return res.LastInsertId()
}

// CreateForEmail inserts a new invite row for a plain email address (no
// Discord account involved) and returns its generated ID. Callers should
// only call this after the plan API has confirmed the invite, so only
// successful invites end up in the table.
func (s *InviteStore) CreateForEmail(ctx context.Context, email, projectKey, tokenID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (email, project_key, token_id, created_at) VALUES (?, ?, ?, ?)`,
		email, projectKey, tokenID, clock.Now(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert invite: %w", err)
	}
	return res.LastInsertId()
}

// AddNotificationChannel stores a Discord channel as a global notification
// target. Re-adding the same channel is harmless.
func (s *InviteStore) AddNotificationChannel(ctx context.Context, channelID, addedBy string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_channels (channel_id, added_by, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET added_by = excluded.added_by`,
		channelID, addedBy, clock.Now(),
	)
	if err != nil {
		return fmt.Errorf("upsert notification channel: %w", err)
	}
	return nil
}

// RemoveNotificationChannel removes a Discord channel from the extra global
// notification targets. It returns true when a row was removed.
func (s *InviteStore) RemoveNotificationChannel(ctx context.Context, channelID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE channel_id = ?`, channelID)
	if err != nil {
		return false, fmt.Errorf("delete notification channel: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect deleted notification channel: %w", err)
	}
	return affected > 0, nil
}

// ListNotificationChannels returns the extra global notification channels.
func (s *InviteStore) ListNotificationChannels(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM notification_channels ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		channels = append(channels, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification channels: %w", err)
	}
	return channels, nil
}
