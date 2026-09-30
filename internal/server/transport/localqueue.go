package transport

import (
	"net"
	"sync"
	"time"
)

// A lease starts on accept, so a connection expires even while every handler
// is busy and it remains in a buffered queue. It owns exactly one limit slot.
type localTCPLease struct {
	mu        sync.Mutex
	conn      net.Conn
	limits    *limiter
	timer     *time.Timer
	deadline  time.Time
	expired   chan struct{}
	onRelease func()
	state     uint8 // 0 queued, 1 claimed by relay, 2 released
}

func newLocalTCPConn(conn net.Conn, addr string, limits *limiter) LocalTCPConn {
	return newCountedLocalTCPConn(conn, addr, limits, nil)
}

func newCountedLocalTCPConn(conn net.Conn, addr string, limits *limiter, onRelease func()) LocalTCPConn {
	return newLocalTCPConnWithTimeout(conn, addr, limits, pairingTimeout, onRelease)
}

func newLocalTCPConnWithTimeout(conn net.Conn, addr string, limits *limiter, timeout time.Duration, onRelease func()) LocalTCPConn {
	l := &localTCPLease{conn: conn, limits: limits, expired: make(chan struct{}), onRelease: onRelease,
		deadline: time.Now().Add(timeout)}
	l.timer = time.AfterFunc(timeout, l.expire)
	return LocalTCPConn{conn: conn, remoteAddr: addr, timeCreated: nowMillis(), lease: l}
}

func (l *localTCPLease) expire() {
	l.mu.Lock()
	if l.state != 0 {
		l.mu.Unlock()
		return
	}
	l.state = 2
	l.mu.Unlock()
	l.release()
}

func (l *localTCPLease) release() {
	l.conn.Close()
	if l.limits != nil {
		l.limits.release()
	}
	if l.onRelease != nil {
		l.onRelease()
	}
	close(l.expired)
}

func (c LocalTCPConn) claim() bool {
	if c.lease == nil {
		return true
	}
	l := c.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != 0 {
		return false
	}
	l.state = 1
	l.timer.Stop()
	return true
}

// A failed stream may put a claimed connection back into the queue. Resume its
// original deadline instead of granting another full waiting period.
func (c LocalTCPConn) requeue() {
	if c.lease == nil {
		return
	}
	l := c.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != 1 {
		return
	}
	l.state = 0
	l.timer.Reset(time.Until(l.deadline))
}

func (c LocalTCPConn) expiry() <-chan struct{} {
	if c.lease == nil {
		return nil
	}
	return c.lease.expired
}

// closeAndRelease is idempotent for leased connections. A fallback limit is
// used by legacy literals in focused tests and older call paths.
func (c LocalTCPConn) closeAndRelease(fallback *limiter) {
	if c.lease == nil {
		if c.conn != nil {
			c.conn.Close()
		}
		if fallback != nil {
			fallback.release()
		}
		return
	}
	l := c.lease
	l.mu.Lock()
	if l.state == 2 {
		l.mu.Unlock()
		return
	}
	l.state = 2
	l.timer.Stop()
	l.mu.Unlock()
	l.release()
}
