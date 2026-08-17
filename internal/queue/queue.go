package queue

import (
	"context"
	"log/slog"
	"sync"

	"github.com/miksea/bot_discord_go/internal/model"
)

// Processor is a function that handles a single issue notification.
type Processor func(ctx context.Context, issue model.Issue) error

// Queue is a thread-safe, buffered FIFO queue for issue notifications.
// It guarantees ordered delivery even when many issues arrive simultaneously.
type Queue struct {
	ch        chan model.Issue
	processor Processor
	logger    *slog.Logger
	wg        sync.WaitGroup
}

// New creates a new Queue with the given buffer capacity and processor function.
func New(capacity int, processor Processor, logger *slog.Logger) *Queue {
	return &Queue{
		ch:        make(chan model.Issue, capacity),
		processor: processor,
		logger:    logger,
	}
}

// Push adds an issue to the queue.
// Returns false if the queue is full (non-blocking).
func (q *Queue) Push(issue model.Issue) bool {
	select {
	case q.ch <- issue:
		q.logger.Info("issue enqueued", "issue_number", issue.Number, "title", issue.Title)
		return true
	default:
		q.logger.Warn("queue full, dropping issue", "issue_number", issue.Number)
		return false
	}
}

// Start begins consuming the queue in the background.
// It spawns workerCount goroutines. Call Stop to wait for all workers to finish.
func (q *Queue) Start(ctx context.Context, workerCount int) {
	for i := range workerCount {
		q.wg.Add(1)
		go q.work(ctx, i)
	}
}

// Stop drains remaining items and waits for all workers to exit.
func (q *Queue) Stop() {
	close(q.ch)
	q.wg.Wait()
}

func (q *Queue) work(ctx context.Context, workerID int) {
	defer q.wg.Done()
	for issue := range q.ch {
		if err := q.processor(ctx, issue); err != nil {
			q.logger.Error("failed to process issue",
				"worker", workerID,
				"issue_number", issue.Number,
				"error", err,
			)
		}
	}
}
