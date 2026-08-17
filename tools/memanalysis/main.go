//go:build ignore

// RAM Analysis Tool for bot_discord_go
// Measures memory usage in three scenarios:
//   1. Baseline (process only)
//   2. Idle (all components initialized, queue empty, no Discord connection)
//   3. Max load (queue burst + concurrent workers processing mock issues)
//
// Run: go run ./tools/memanalysis/main.go

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/miksea/bot_discord_go/internal/model"
	"github.com/miksea/bot_discord_go/internal/queue"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func snapshot(label string) memStats {
	runtime.GC()
	debug.FreeOSMemory()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return memStats{
		Label:     label,
		HeapAlloc: ms.HeapAlloc,
		HeapSys:   ms.HeapSys,
		HeapInuse: ms.HeapInuse,
		StackSys:  ms.StackSys,
		TotalSys:  ms.Sys,
		NumGC:     ms.NumGC,
		Goroutines: uint64(runtime.NumGoroutine()),
	}
}

type memStats struct {
	Label      string
	HeapAlloc  uint64
	HeapSys    uint64
	HeapInuse  uint64
	StackSys   uint64
	TotalSys   uint64
	NumGC      uint32
	Goroutines uint64
}

func kb(b uint64) string { return fmt.Sprintf("%.2f KB", float64(b)/1024) }
func mb(b uint64) string { return fmt.Sprintf("%.3f MB", float64(b)/1024/1024) }

// ─── Mock Issue Builder ───────────────────────────────────────────────────────

func mockIssue(n int) model.Issue {
	return model.Issue{
		Number:  n,
		Title:   fmt.Sprintf("Issue #%d: fix critical bug in authentication module", n),
		Body:    strings.Repeat("This is a detailed description of the issue. ", 10),
		HTMLURL: fmt.Sprintf("https://github.com/org/repo/issues/%d", n),
		State:   "open",
		Labels:  []model.Label{{Name: "bug"}, {Name: "urgent"}},
		Assignees: []model.User{
			{Login: "alice", AvatarURL: "https://avatars.githubusercontent.com/u/1"},
			{Login: "bob", AvatarURL: "https://avatars.githubusercontent.com/u/2"},
		},
		Repository: model.Repo{FullName: "org/repo", HTMLURL: "https://github.com/org/repo"},
		CreatedAt:  time.Now().Format(time.RFC3339),
	}
}

// ─── Scenarios ────────────────────────────────────────────────────────────────

// scenario1Baseline: process just started, nothing initialized.
func scenario1Baseline() memStats {
	return snapshot("1. Baseline (empty process)")
}

// scenario2Idle: queue + workers initialized but queue is empty.
func scenario2Idle() (memStats, func()) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	var processed atomic.Int64
	processor := func(ctx context.Context, issue model.Issue) error {
		processed.Add(1)
		return nil
	}

	q := queue.New(500, processor, logger)
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx, 3) // 3 workers like production

	// Let goroutines settle
	time.Sleep(50 * time.Millisecond)

	stats := snapshot("2. Idle (queue+3 workers, empty queue)")

	return stats, func() {
		cancel()
		q.Stop()
	}
}

// scenario3MaxLoad: burst 500 issues (full queue capacity) processed by 3 workers.
func scenario3MaxLoad() memStats {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	var processed atomic.Int64
	var wg sync.WaitGroup

	processor := func(ctx context.Context, issue model.Issue) error {
		// Simulate message formatting work (no actual Discord call)
		data, _ := json.Marshal(issue)
		_ = fmt.Sprintf("[%s] #%d %s\nBody: %s\nURL: %s",
			issue.Repository.FullName, issue.Number, issue.Title,
			issue.Body, string(data))
		time.Sleep(1 * time.Millisecond) // simulate discord API latency
		processed.Add(1)
		wg.Done()
		return nil
	}

	q := queue.New(500, processor, logger)
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx, 3)

	// Burst: fill the entire queue
	const burstSize = 500
	wg.Add(burstSize)
	for i := range burstSize {
		q.Push(mockIssue(i + 1))
	}

	// Capture memory WHILE queue is draining (peak load)
	time.Sleep(10 * time.Millisecond)
	stats := snapshot("3. Max load (500 issues burst, queue full)")

	// Wait for all to finish
	wg.Wait()
	cancel()
	q.Stop()

	fmt.Printf("   └─ processed: %d issues\n", processed.Load())
	return stats
}

// scenario4ModelAlloc: cost of allocating 500 Issue structs in memory.
func scenario4ModelAlloc() memStats {
	issues := make([]model.Issue, 500)
	for i := range issues {
		issues[i] = mockIssue(i + 1)
	}
	stats := snapshot("4. 500 Issue structs in heap")
	_ = issues
	return stats
}

// ─── Reporter ─────────────────────────────────────────────────────────────────

func printReport(stats []memStats) {
	sep := strings.Repeat("─", 70)

	fmt.Println()
	fmt.Println(sep)
	fmt.Println("  📊  MEMORY ANALYSIS — bot_discord_go")
	fmt.Println(sep)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  Scenario\tHeapAlloc\tHeapInuse\tHeapSys\tStackSys\tTotalSys\tGoroutines\tGC runs")
	fmt.Fprintln(w, "  ────────\t─────────\t─────────\t───────\t────────\t────────\t──────────\t───────")

	for _, s := range stats {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n",
			s.Label,
			mb(s.HeapAlloc),
			mb(s.HeapInuse),
			mb(s.HeapSys),
			kb(s.StackSys),
			mb(s.TotalSys),
			s.Goroutines,
			s.NumGC,
		)
	}
	w.Flush()

	// Delta analysis
	if len(stats) >= 3 {
		idle := stats[1]
		load := stats[2]

		fmt.Println()
		fmt.Println(sep)
		fmt.Println("  📈  DELTA ANALYSIS (Idle → Max Load)")
		fmt.Println(sep)
		fmt.Printf("  HeapAlloc  delta : %s\n", mb(load.HeapAlloc-idle.HeapAlloc))
		fmt.Printf("  HeapInuse  delta : %s\n", mb(load.HeapInuse-idle.HeapInuse))
		fmt.Printf("  Goroutines delta : +%d\n", load.Goroutines-idle.Goroutines)
	}

	// Per-issue cost
	if len(stats) >= 4 {
		base := stats[0]
		model500 := stats[3]
		delta := model500.HeapAlloc - base.HeapAlloc
		perIssue := float64(delta) / 500
		fmt.Println()
		fmt.Println(sep)
		fmt.Println("  🧮  PER-ISSUE COST (500 structs)")
		fmt.Println(sep)
		fmt.Printf("  500 Issue structs total : %s\n", kb(delta))
		fmt.Printf("  Cost per Issue struct   : %.2f bytes (%.3f KB)\n", perIssue, perIssue/1024)
	}

	// Recommendations
	fmt.Println()
	fmt.Println(sep)
	fmt.Println("  💡  RECOMMENDATIONS")
	fmt.Println(sep)
	fmt.Println("  Queue capacity 500  → max queue memory ≈ 500 × cost-per-issue")
	fmt.Println("  Worker count 3      → goroutine overhead minimal")
	fmt.Println("  No CGO / net-http   → clean heap, no hidden C allocations")
	fmt.Println("  GC pressure         → use runtime.GC() after burst if needed")
	fmt.Println(sep)
	fmt.Println()
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("⏳ Running memory analysis... (this may take a few seconds)")

	var results []memStats

	// Scenario 1: baseline
	results = append(results, scenario1Baseline())

	// Scenario 2: idle
	idleStats, cleanup := scenario2Idle()
	results = append(results, idleStats)
	cleanup()

	// Scenario 3: max load
	fmt.Println("⏳ Bursting 500 issues into queue...")
	results = append(results, scenario3MaxLoad())

	// Scenario 4: model allocation cost
	results = append(results, scenario4ModelAlloc())

	printReport(results)
}
