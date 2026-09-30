package transport

import (
	"sync"
	"testing"
)

func TestMuxPoolLimit(t *testing.T) {
	for _, tc := range []struct{ configured, receive, want int }{
		{8, 4 << 20, 32},
		{16, 32 << 20, 32},
		{16, 64 << 20, 16},
		{4, 0, 16},
		{0, 64 << 20, 0},
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
