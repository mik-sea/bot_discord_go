package queue_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/queue"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

func mockIssue(n int) model.Issue {
	return model.Issue{
		Number:  n,
		Title:   fmt.Sprintf("Issue #%d: fix critical bug in authentication module", n),
		Body:    strings.Repeat("This is a detailed description. ", 10),
		HTMLURL: fmt.Sprintf("https://github.com/org/repo/issues/%d", n),
		State:   "open",
		Labels:  []model.Label{{Name: "bug"}, {Name: "urgent"}},
		Assignees: []model.User{
			{Login: "alice"},
			{Login: "bob"},
		},
		Repository: model.Repo{FullName: "org/repo"},
	}
}

// BenchmarkQueuePush measures the cost of pushing a single issue into the queue.
func BenchmarkQueuePush(b *testing.B) {
	nop := func(_ context.Context, _ model.Issue) error { return nil }
	q := queue.New(b.N+100, nop, nopLogger())
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx, 1)

	issue := mockIssue(1)
	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		q.Push(issue)
	}

	b.StopTimer()
	cancel()
	q.Stop()
}

// BenchmarkQueueThroughput measures end-to-end issue processing throughput with 3 workers.
func BenchmarkQueueThroughput(b *testing.B) {
	done := make(chan struct{}, b.N+1)
	processor := func(_ context.Context, _ model.Issue) error {
		done <- struct{}{}
		return nil
	}
	q := queue.New(b.N+100, processor, nopLogger())
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx, 3)

	issue := mockIssue(1)
	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		q.Push(issue)
	}
	for range b.N {
		<-done
	}

	b.StopTimer()
	cancel()
	q.Stop()
}

// BenchmarkIssueJSONMarshal measures JSON encode cost per issue (webhook parse path).
func BenchmarkIssueJSONMarshal(b *testing.B) {
	issue := mockIssue(1)
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, err := json.Marshal(issue); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIssueJSONUnmarshal measures JSON decode cost per issue (webhook parse path).
func BenchmarkIssueJSONUnmarshal(b *testing.B) {
	issue := mockIssue(1)
	data, _ := json.Marshal(issue)
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		var out model.Issue
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}
