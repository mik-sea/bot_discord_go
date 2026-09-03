package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// GitHubUserLink maps a GitHub username to a Discord user ID.
type GitHubUserLink struct {
	GitHubLogin   string
	DiscordUserID string
}

// SetGitHubUserLink stores or updates one GitHub-login to Discord-user link.
func (s *Store) SetGitHubUserLink(ctx context.Context, githubLogin, discordUserID, addedBy string) error {
	now := clock.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO github_user_links (github_login, discord_user_id, added_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(github_login) DO UPDATE SET
			discord_user_id = excluded.discord_user_id,
			added_by = excluded.added_by,
			updated_at = excluded.updated_at
	`, githubLogin, discordUserID, addedBy, now, now)
	if err != nil {
		return fmt.Errorf("set github user link %q: %w", githubLogin, err)
	}
	return nil
}

// GetDiscordUserByGitHubLogin returns the Discord user mapped to a GitHub login.
func (s *Store) GetDiscordUserByGitHubLogin(ctx context.Context, githubLogin string) (string, bool, error) {
	var discordUserID string
	err := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id FROM github_user_links WHERE github_login = ?
	`, githubLogin).Scan(&discordUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get github user link %q: %w", githubLogin, err)
	}
	return discordUserID, true, nil
}

// RemoveGitHubUserLink removes one GitHub-login to Discord-user link.
func (s *Store) RemoveGitHubUserLink(ctx context.Context, githubLogin string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM github_user_links WHERE github_login = ?`, githubLogin)
	if err != nil {
		return false, fmt.Errorf("remove github user link %q: %w", githubLogin, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove github user link %q: %w", githubLogin, err)
	}
	return affected > 0, nil
}

// ListGitHubUserLinks returns all GitHub-login to Discord-user links.
func (s *Store) ListGitHubUserLinks(ctx context.Context) ([]GitHubUserLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT github_login, discord_user_id
		FROM github_user_links
		ORDER BY github_login ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list github user links: %w", err)
	}
	defer rows.Close()

	var links []GitHubUserLink
	for rows.Next() {
		var link GitHubUserLink
		if err := rows.Scan(&link.GitHubLogin, &link.DiscordUserID); err != nil {
			return nil, fmt.Errorf("list github user links: %w", err)
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
