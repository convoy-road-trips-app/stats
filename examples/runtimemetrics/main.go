package main

import (
	"fmt"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

func main() {
	fmt.Println("=== Runtime Metrics Collector Example ===")
	fmt.Println()
	fmt.Println("This example demonstrates how to enable automatic Go runtime")
	fmt.Println("metric collection via stats.WithRuntimeMetrics().")
	fmt.Println()
	fmt.Println("The collector fires an immediate Collect() at Start(), so")
	fmt.Println("runtime metrics enter the pipeline before the first sleep.")
	fmt.Println()

	// Create a new stats client with runtime metrics enabled.
	// WithRuntimeMetrics() enables automatic collection of Go runtime
	// metrics (memory, GC, goroutines, CPU) using DefaultRuntimeMetricsConfig
	// which fires an immediate Collect() at startup, then every 10s.
	client, err := stats.NewClient(
		stats.WithServiceName("runtime-metrics-example"),
		stats.WithEnvironment("development"),
		stats.WithBufferSize(1024),
		stats.WithWorkers(2),
		stats.WithRuntimeMetrics(),
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create client: %v", err))
	}
	defer func() { _ = client.Close() }()

	fmt.Println("Stats client created with runtime metrics enabled.")
	fmt.Println()

	// Print the runtime metric names being collected.
	fmt.Println("Runtime metric names collected (prefix: runtime.go.):")
	fmt.Println()
	fmt.Println("  Memory:")
	fmt.Println("    runtime.go.memory.heap.alloc      - bytes allocated and in use on heap")
	fmt.Println("    runtime.go.memory.heap.inuse      - bytes in in-use spans")
	fmt.Println("    runtime.go.memory.heap.idle       - bytes in idle spans")
	fmt.Println("    runtime.go.memory.heap.released   - bytes returned to OS")
	fmt.Println("    runtime.go.memory.sys             - bytes obtained from OS total")
	fmt.Println("    runtime.go.memory.stack.inuse     - bytes in stack spans")
	fmt.Println()
	fmt.Println("  Heap Objects:")
	fmt.Println("    runtime.go.heap.allocs.bytes      - cumulative bytes allocated on heap")
	fmt.Println("    runtime.go.heap.frees.bytes       - cumulative bytes freed from heap")
	fmt.Println("    runtime.go.heap.allocs.objects    - cumulative heap object allocs")
	fmt.Println("    runtime.go.heap.frees.objects     - cumulative heap object frees")
	fmt.Println("    runtime.go.heap.objects.live      - live heap objects (allocs - frees)")
	fmt.Println("    runtime.go.heap.goal.bytes        - GC target heap size")
	fmt.Println()
	fmt.Println("  Garbage Collection:")
	fmt.Println("    runtime.go.gc.cycles.total        - number of completed GC cycles")
	fmt.Println("    runtime.go.gc.cpu.seconds         - cumulative CPU seconds spent in GC")
	fmt.Println()
	fmt.Println("  Scheduler:")
	fmt.Println("    runtime.go.goroutines             - current goroutine count")
	fmt.Println("    runtime.go.gomaxprocs             - GOMAXPROCS value")
	fmt.Println("    runtime.go.cgo.calls              - number of CGo calls made")
	fmt.Println()
	fmt.Println("  CPU:")
	fmt.Println("    runtime.go.cpu.total.seconds      - total CPU time used")
	fmt.Println("    runtime.go.cpu.user.seconds       - user-space CPU time")
	fmt.Println("    runtime.go.cpu.gc.seconds         - GC CPU time")
	fmt.Println("    runtime.go.cpu.idle.seconds       - idle CPU time")
	fmt.Println("    runtime.go.cpu.scavenge.seconds   - scavenger CPU time")
	fmt.Println()

	// Capture initial stats before giving workers time to process.
	initialStats := client.Stats()
	fmt.Printf("Initial Pipeline Stats (before processing):\n")
	fmt.Printf("  Processed: %d\n", initialStats.Pipeline.Processed)
	fmt.Printf("  Buffer Len: %d/%d\n", initialStats.Pipeline.BufferLen, initialStats.Pipeline.BufferCap)
	fmt.Println()

	// The collector fires an immediate Collect() at Start(). Allow
	// time for the worker pool to drain the buffered runtime metrics.
	fmt.Println("Waiting 500ms for worker pool to process runtime metrics...")
	time.Sleep(500 * time.Millisecond)

	// Final stats — Processed should be > 0 from the immediate collect.
	finalStats := client.Stats()
	fmt.Printf("\nFinal Pipeline Stats:\n")
	fmt.Printf("  Service: %s\n", finalStats.ServiceName)
	fmt.Printf("  Environment: %s\n", finalStats.Environment)
	fmt.Printf("  Buffer Len: %d/%d\n", finalStats.Pipeline.BufferLen, finalStats.Pipeline.BufferCap)
	fmt.Printf("  Processed: %d\n", finalStats.Pipeline.Processed)
	fmt.Printf("  Dropped: %d\n", finalStats.Pipeline.Dropped)
	fmt.Printf("  Errors: %d\n", finalStats.Pipeline.Errors)
	fmt.Printf("  Memory Usage: %d bytes\n", finalStats.Pipeline.MemoryUsage)

	if finalStats.Pipeline.Processed > 0 {
		fmt.Printf("\n✓ Runtime metrics are flowing! %d metric(s) processed.\n", finalStats.Pipeline.Processed)
	} else {
		fmt.Println("\n⚠ Processed count is 0 — workers may not have drained yet.")
	}

	fmt.Println("\nShutting down gracefully...")
}
