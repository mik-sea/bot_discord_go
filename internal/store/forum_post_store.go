package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/miksea/bot_discord_go/internal/clock"
)

// SetForumPost records the Discord forum thread (and its starter message)
// created for one GitHub issue, so later events (edited, closed, reopened)
// can update that same post instead of creating a new one each time.
func (s *Store) SetForumPost(ctx context.Context, repoFullName string, issueNumber int, forumChannelID, threadID, messageID string) error {
	now := clock.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO forum_posts (repo_full_name, issue_number, forum_channel_id, thread_id, message_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo_full_name, issue_number) DO UPDATE SET
			forum_channel_id = excluded.forum_channel_id,
			thread_id = excluded.thread_id,
			message_id = excluded.message_id,
			updated_at = excluded.updated_at
	`, repoFullName, issueNumber, forumChannelID, threadID, messageID, now, now)
	if err != nil {
		return fmt.Errorf("set forum post %s#%d: %w", repoFullName, issueNumber, err)
	}
	return nil
}

// ForumPost is the thread + starter message tracked for one issue's forum
// post.
type ForumPost struct {
	ThreadID  string
	MessageID string
}

// GetForumPost returns the thread/message IDs of the forum post previously
// created for this issue in this forum channel, if any.
func (s *Store) GetForumPost(ctx context.Context, repoFullName string, issueNumber int, forumChannelID string) (ForumPost, bool, error) {
	var post ForumPost
	err := s.db.QueryRowContext(ctx, `
		SELECT thread_id, message_id FROM forum_posts
		WHERE repo_full_name = ? AND issue_number = ? AND forum_channel_id = ?
	`, repoFullName, issueNumber, forumChannelID).Scan(&post.ThreadID, &post.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return ForumPost{}, false, nil
	}
	if err != nil {
		return ForumPost{}, false, fmt.Errorf("get forum post %s#%d: %w", repoFullName, issueNumber, err)
	}
	return post, true, nil
}

// DeleteForumPost removes the tracked thread mapping for an issue, e.g. when
// the thread has been deleted on Discord's side and a fresh one needs to be
// created on the next event.
func (s *Store) DeleteForumPost(ctx context.Context, repoFullName string, issueNumber int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM forum_posts WHERE repo_full_name = ? AND issue_number = ?`, repoFullName, issueNumber)
	if err != nil {
		return fmt.Errorf("delete forum post %s#%d: %w", repoFullName, issueNumber, err)
	}
	return nil
}
