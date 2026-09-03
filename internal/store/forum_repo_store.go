package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// ForumRepoChannel maps one GitHub repository to one Discord forum channel.
type ForumRepoChannel struct {
	RepoFullName   string
	RepoURL        string
	ForumChannelID string
}

// SetForumRepoChannel stores the forum channel that should receive posts for
// issues from repoFullName.
func (s *Store) SetForumRepoChannel(ctx context.Context, repoFullName, repoURL, forumChannelID, addedBy string) error {
	now := clock.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO forum_repo_channels (repo_full_name, repo_url, forum_channel_id, added_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo_full_name) DO UPDATE SET
			repo_url = excluded.repo_url,
			forum_channel_id = excluded.forum_channel_id,
			added_by = excluded.added_by,
			updated_at = excluded.updated_at
	`, repoFullName, repoURL, forumChannelID, addedBy, now, now)
	if err != nil {
		return fmt.Errorf("set forum repo channel %q: %w", repoFullName, err)
	}
	return nil
}

// GetForumChannelByRepo returns the mapped Discord forum channel for a repo.
func (s *Store) GetForumChannelByRepo(ctx context.Context, repoFullName string) (string, bool, error) {
	var channelID string
	err := s.db.QueryRowContext(ctx, `
		SELECT forum_channel_id FROM forum_repo_channels WHERE repo_full_name = ?
	`, repoFullName).Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get forum repo channel %q: %w", repoFullName, err)
	}
	return channelID, true, nil
}

// RemoveForumRepoChannel removes a repo-to-forum mapping.
func (s *Store) RemoveForumRepoChannel(ctx context.Context, repoFullName string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM forum_repo_channels WHERE repo_full_name = ?`, repoFullName)
	if err != nil {
		return false, fmt.Errorf("remove forum repo channel %q: %w", repoFullName, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove forum repo channel %q: %w", repoFullName, err)
	}
	return affected > 0, nil
}

// ListForumRepoChannels returns all repo-to-forum mappings.
func (s *Store) ListForumRepoChannels(ctx context.Context) ([]ForumRepoChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT repo_full_name, repo_url, forum_channel_id
		FROM forum_repo_channels
		ORDER BY repo_full_name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list forum repo channels: %w", err)
	}
	defer rows.Close()

	var mappings []ForumRepoChannel
	for rows.Next() {
		var mapping ForumRepoChannel
		if err := rows.Scan(&mapping.RepoFullName, &mapping.RepoURL, &mapping.ForumChannelID); err != nil {
			return nil, fmt.Errorf("list forum repo channels: %w", err)
		}
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}
