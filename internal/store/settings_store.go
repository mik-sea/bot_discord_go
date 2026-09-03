package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// SettingKeyDefaultChannel overrides config.DiscordConfig.DefaultChannel at
// runtime (set via the /set-channel command) so the notification target can
// be changed without editing .env and restarting the bot.
const SettingKeyDefaultChannel = "default_channel"

// GetSetting returns the stored value for key, and false if it has never
// been set.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting upserts the value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, clock.Now())
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
