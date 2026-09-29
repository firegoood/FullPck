package transport

import (
	"sync"
	"testing"
)

func TestMuxPoolLimit(t *testing.T) {
	for _, tc := range []struct{ configured, receive, want int }{
		{8, 4 << 20, 32}, {16, 32 << 20, 32},
		{16, 64 << 20, 16}, {4, 0, 16}, {0, 64 << 20, 0},
	} {
		if got := muxPoolLimit(tc.configured, tc.receive); got != tc.want {
			t.Errorf("muxPoolLimit(%d, %d) = %d, want %d", tc.configured, tc.receive, got, tc.want)
		}
	}
}

func TestSessionSlotsConcurrent(t *testing.T) {
	slots := newSessionSlots(8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	result := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; result <- slots.tryAcquire() }()
	}
	close(start)
	wg.Wait()
	close(result)
	acquired := 0
	for ok := range result {
		if ok {
			acquired++
		}
	}
	if acquired != 8 {
		t.Fatalf("acquired %d slots, want 8", acquired)
	}
	for i := 0; i < acquired; i++ {
		slots.release()
	}
	if !slots.tryAcquire() {
		t.Fatal("released slot unavailable")
	}
	slots.release()
}

// The blind spot this exists to close: a few long-lived streams saturate the
// connections they already have without ever asking for a new one, so the
// request-rate trigger stays silent while the pipe is full.
func TestThroughputTriggerGrowsWhenConnectionsAreLoaded(t *testing.T) {
	var p poolLoad
	// 8 live connections carrying 200 Mbit/s = 25 Mbit/s each: working hard.
	if !p.wantsMore(200, 8, 8, 8) {
		t.Fatal("a loaded pool should be allowed to grow")
	}
}

// The same total throughput spread over enough connections is not a reason to
// add more — each one is barely working.
func TestThroughputTriggerIgnoresLightlyLoadedPool(t *testing.T) {
	var p poolLoad
	// 32 connections sharing 200 Mbit/s ≈ 6 Mbit/s each.
	if p.wantsMore(200, 32, 32, 8) {
		t.Fatal("a lightly loaded pool must not grow")
	}
}

// An idle tunnel must never grow the pool.
func TestThroughputTriggerIgnoresIdleTunnel(t *testing.T) {
	var p poolLoad
	if p.wantsMore(0, 8, 8, 8) {
		t.Fatal("no traffic must not grow the pool")
	}
}

// Growth is bounded, so neither trigger can add connections forever.
func TestPoolGrowthIsBounded(t *testing.T) {
	var p poolLoad
	max := 8 * poolGrowthLimit
	if p.wantsMore(10000, 8, max, 8) {
		t.Fatalf("pool must not grow past %d connections", max)
	}
	if !poolCanGrow(max-1, 8) {
		t.Fatal("pool should still grow just below the limit")
	}
	if poolCanGrow(max, 8) {
		t.Fatal("pool must stop at the limit")
	}
}

// A first reading has nothing to compare against, and counters that go
// backwards (a restored metrics file) are not a throughput measurement — both
// must read as "no opinion" rather than as a huge or negative rate.
func TestFirstReadingAndCounterResetAreNotMeasurements(t *testing.T) {
	var p poolLoad
	if got := p.mbps(); got != 0 {
		t.Fatalf("first reading = %d, want 0", got)
	}
	// Simulate the counters having been higher before (a restore).
	p.lastIn, p.lastOut = 1<<40, 1<<40
	if got := p.mbps(); got != 0 {
		t.Fatalf("counter reset = %d, want 0", got)
	}
}

// A pool with no configured size (an unset config) must not be grown.
func TestUnconfiguredPoolDoesNotGrow(t *testing.T) {
	if poolCanGrow(1, 0) {
		t.Fatal("an unconfigured pool size must not grow")
	}
}
