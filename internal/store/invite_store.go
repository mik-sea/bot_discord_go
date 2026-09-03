package store

import (
	"context"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// CreateForUser inserts a new invite row for a tagged Discord user and
// returns its generated ID. email is the derived (non-functional) address
// sent to the plan API — stored alongside for record-keeping only, nothing
// is actually sent to it. Callers should only call this after the plan API
// has confirmed the invite, so only successful invites end up in the table.
func (s *Store) CreateForUser(ctx context.Context, discordUserID, discordUsername, email, projectKey, tokenID string) (int64, error) {
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
func (s *Store) CreateForEmail(ctx context.Context, email, projectKey, tokenID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (email, project_key, token_id, created_at) VALUES (?, ?, ?, ?)`,
		email, projectKey, tokenID, clock.Now(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert invite: %w", err)
	}
	return res.LastInsertId()
}
