package store

import (
	"context"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// AddNotificationChannel registers channelID as an extra recipient of GitHub
// issue notifications (in addition to the default channel), set via the
// /notify-channel add command. Adding a channel that's already registered is
// a no-op.
func (s *Store) AddNotificationChannel(ctx context.Context, channelID, addedBy string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notify_channels (channel_id, added_by, created_at) VALUES (?, ?, ?)
		ON CONFLICT(channel_id) DO NOTHING
	`, channelID, addedBy, clock.Now())
	if err != nil {
		return fmt.Errorf("add notification channel %q: %w", channelID, err)
	}
	return nil
}

// RemoveNotificationChannel deletes channelID from the extra recipients. It
// reports whether a row was actually removed.
func (s *Store) RemoveNotificationChannel(ctx context.Context, channelID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notify_channels WHERE channel_id = ?`, channelID)
	if err != nil {
		return false, fmt.Errorf("remove notification channel %q: %w", channelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove notification channel %q: %w", channelID, err)
	}
	return affected > 0, nil
}

// ListNotificationChannels returns all extra channel IDs currently
// registered to receive GitHub issue notifications.
func (s *Store) ListNotificationChannels(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM notify_channels ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, fmt.Errorf("list notification channels: %w", err)
		}
		channels = append(channels, channelID)
	}
	return channels, rows.Err()
}
