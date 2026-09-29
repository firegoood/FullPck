package transport

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueuedLocalExpiresAndReleasesOnce(t *testing.T) {
	lim := newLimiter(Limits{MaxConnections: 1})
	if !lim.acquire() {
		t.Fatal("slot unavailable")
	}
	a, b := net.Pipe()
	defer b.Close()
	var released atomic.Int32
	local := newLocalTCPConnWithTimeout(a, "127.0.0.1:80", lim, 20*time.Millisecond, func() { released.Add(1) })
	select {
	case <-local.expiry():
	case <-time.After(time.Second):
		t.Fatal("queued connection did not expire")
	}
	if local.claim() {
		t.Fatal("expired connection was claimed")
	}
	local.closeAndRelease(lim)
	if released.Load() != 1 || lim.active.Load() != 0 {
		t.Fatalf("release count %d, active %d", released.Load(), lim.active.Load())
	}
	if !lim.acquire() {
		t.Fatal("slot not returned")
	}
	lim.release()
}

func TestClaimedLocalOutlivesQueueDeadline(t *testing.T) {
	lim := newLimiter(Limits{MaxConnections: 1})
	lim.acquire()
	a, b := net.Pipe()
	defer b.Close()
	local := newLocalTCPConnWithTimeout(a, "127.0.0.1:80", lim, 20*time.Millisecond, nil)
	if !local.claim() {
		t.Fatal("fresh connection could not be claimed")
	}
	time.Sleep(40 * time.Millisecond)
	select {
	case <-local.expiry():
		t.Fatal("claimed connection expired")
	default:
	}
	if lim.active.Load() != 1 {
		t.Fatal("slot released while relay owns it")
	}
	local.closeAndRelease(lim)
	if lim.active.Load() != 0 {
		t.Fatal("slot not released after relay")
	}
}
