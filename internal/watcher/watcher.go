package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/queue"
)

// FileWatcher watches a directory for new JSON files and enqueues the issues inside.
// After processing, each file is moved to a "processed/" subfolder to avoid reprocessing.
type FileWatcher struct {
	dir    string
	queue  *queue.Queue
	logger *slog.Logger
}

// New creates a new FileWatcher.
func New(dir string, q *queue.Queue, logger *slog.Logger) *FileWatcher {
	return &FileWatcher{
		dir:    dir,
		queue:  q,
		logger: logger,
	}
}

// Start begins watching the directory on the given poll interval.
// It returns when ctx is cancelled.
func (fw *FileWatcher) Start(ctx context.Context, interval time.Duration) error {
	if err := fw.ensureDirs(); err != nil {
		return fmt.Errorf("ensure watcher dirs: %w", err)
	}

	fw.logger.Info("file watcher started", "dir", fw.dir, "interval", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fw.logger.Info("file watcher stopped")
			return nil
		case <-ticker.C:
			fw.scan()
		}
	}
}

// scan reads all .json files in the watch directory and processes them.
func (fw *FileWatcher) scan() {
	entries, err := os.ReadDir(fw.dir)
	if err != nil {
		fw.logger.Error("failed to read watch dir", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fw.processFile(filepath.Join(fw.dir, entry.Name()))
	}
}

// processFile reads and enqueues issues from a JSON file, then archives it.
func (fw *FileWatcher) processFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fw.logger.Error("failed to read file", "path", path, "error", err)
		return
	}

	issues, err := parseIssues(data)
	if err != nil {
		fw.logger.Error("failed to parse issues", "path", path, "error", err)
		fw.archiveFile(path, "failed")
		return
	}

	queued := 0
	for _, issue := range issues {
		if fw.queue.Push(issue) {
			queued++
		}
	}

	fw.logger.Info("file processed", "path", path, "total", len(issues), "queued", queued)
	fw.archiveFile(path, "processed")
}

// archiveFile moves a file into a named subdirectory (processed/ or failed/).
func (fw *FileWatcher) archiveFile(src, subDir string) {
	dst := filepath.Join(fw.dir, subDir, filepath.Base(src))
	if err := os.Rename(src, dst); err != nil {
		fw.logger.Error("failed to archive file", "src", src, "dst", dst, "error", err)
	}
}

// ensureDirs creates necessary subdirectories if they don't exist.
func (fw *FileWatcher) ensureDirs() error {
	for _, sub := range []string{"", "processed", "failed"} {
		dir := filepath.Join(fw.dir, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// parseIssues decodes a JSON body as a list, wrapped payload, or single issue.
func parseIssues(data []byte) ([]model.Issue, error) {
	var payload model.N8NPayload
	if err := json.Unmarshal(data, &payload); err == nil && len(payload.Issues) > 0 {
		return payload.Issues, nil
	}

	var issues []model.Issue
	if err := json.Unmarshal(data, &issues); err == nil && len(issues) > 0 {
		return issues, nil
	}

	var single model.Issue
	if err := json.Unmarshal(data, &single); err == nil && single.Number != 0 {
		return []model.Issue{single}, nil
	}

	return nil, fmt.Errorf("unrecognized JSON format")
}
