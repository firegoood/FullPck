package transport

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/firegoood/FullPck/internal/web"
	"github.com/gorilla/websocket"
)

// A client transport rebuilds its state on every reconnect: Restart() swaps the
// context, the control channel, the usage monitor and the counters while the
// previous generation's goroutines are still winding down. Those goroutines are
// reading the very fields being replaced, which is a data race — and the values
// involved are pointers and channels, where an unsynchronised read is not
// merely stale but undefined.
//
// clientState puts that generation-scoped state behind one lock so a reader
// always sees a complete, consistent generation.

// clientState holds everything Restart() replaces.
type clientState struct {
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	conn         net.Conn        // control channel for the byte-stream transports
	wsConn       *websocket.Conn // control channel for the websocket transports
	usageMonitor *web.Usage
	workers      sync.WaitGroup
	stopping     bool
	closers      map[uint64]io.Closer
	nextID       uint64
}

// Reset publishes a whole new generation at once, so no reader can observe a
// half-swapped mixture of old and new.
func (s *clientState) Reset(ctx context.Context, cancel context.CancelFunc, usage *web.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.cancel = cancel
	s.usageMonitor = usage
	s.conn = nil
	s.wsConn = nil
	s.stopping = false
	s.closers = make(map[uint64]io.Closer)
	s.nextID = 0
}

func (s *clientState) Ctx() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}

func (s *clientState) Cancel() context.CancelFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cancel
}

func (s *clientState) Usage() *web.Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usageMonitor
}

func (s *clientState) Conn() net.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

func (s *clientState) SetConn(c net.Conn) bool {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		c.Close()
		return false
	}
	s.conn = c
	s.addCloserLocked(c)
	s.mu.Unlock()
	return true
}

func (s *clientState) WSConn() *websocket.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wsConn
}

func (s *clientState) SetWSConn(c *websocket.Conn) bool {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		c.Close()
		return false
	}
	s.wsConn = c
	s.addCloserLocked(c)
	s.mu.Unlock()
	return true
}

// Go registers a worker before starting it. Stop closes registration before
// Wait starts, so the previous generation has fully ended before Reset.
func (s *clientState) Go(fn func()) bool {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return false
	}
	s.workers.Add(1)
	s.mu.Unlock()
	go func() { defer s.workers.Done(); fn() }()
	return true
}

// Track makes a blocking data connection close when its generation stops.
func (s *clientState) Track(c io.Closer) (func(), bool) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		c.Close()
		return func() {}, false
	}
	id := s.addCloserLocked(c)
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); delete(s.closers, id); s.mu.Unlock() }) }, true
}

func (s *clientState) addCloserLocked(c io.Closer) uint64 {
	if s.closers == nil {
		s.closers = make(map[uint64]io.Closer)
	}
	s.nextID++
	s.closers[s.nextID] = c
	return s.nextID
}

func (s *clientState) Stop() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	cancel := s.cancel
	closers := make([]io.Closer, 0, len(s.closers))
	for _, c := range s.closers {
		closers = append(closers, c)
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, c := range closers {
		_ = c.Close()
	}
}

func (s *clientState) Wait()        { s.workers.Wait() }
func (s *clientState) StopAndWait() { s.Stop(); s.Wait() }

// CloseConn closes whichever control channel is held, if any.
func (s *clientState) CloseConn() {
	if c := s.Conn(); c != nil {
		c.Close()
	}
	if c := s.WSConn(); c != nil {
		c.Close()
	}
}

// drain empties a buffered signal channel without replacing it. Restart used to
// allocate a new channel, which races with the goroutines selecting on the old
// one; emptying the existing channel achieves the same thing safely.
func drain(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// tunnelStatus is the one-line state a transport publishes for the panel.
// Written as a run starts and again when its control channel comes up, and
// cleared by Restart — three writers across two generations that overlap, on
// what was a plain string field. clientState above exists for exactly this
// reason; the status was simply left out of it.
type tunnelStatus struct {
	mu sync.RWMutex
	s  string
}

func (t *tunnelStatus) set(v string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s = v
}

func (t *tunnelStatus) get() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.s
}
