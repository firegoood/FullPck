package node

// Telegram byte streams share the authenticated Agent session with typed
// management RPCs. A stream has one fixed destination, bounded frame and
// receive queues, and no request field that could become a generic CONNECT.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	telegramEndpoint = "api.telegram.org:443"
	streamChunkSize  = 16 << 10
	streamQueueSize  = 16
	streamLimit      = 16
)

var dialTelegramEndpoint = func(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{Timeout: 12 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", telegramEndpoint)
}

type telegramStream struct {
	id     string
	mu     sync.Mutex
	peer   net.Conn
	recv   chan []byte
	opened chan error
	done   chan struct{}
	once   sync.Once
}

func newTelegramStream(id string) *telegramStream {
	return &telegramStream{id: id, recv: make(chan []byte, streamQueueSize),
		opened: make(chan error, 1), done: make(chan struct{})}
}

func (st *telegramStream) setPeer(conn net.Conn) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	select {
	case <-st.done:
		_ = conn.Close()
		return false
	default:
	}
	st.peer = conn
	return true
}

func (st *telegramStream) close() {
	st.once.Do(func() {
		close(st.done)
		st.mu.Lock()
		if st.peer != nil {
			_ = st.peer.Close()
		}
		st.mu.Unlock()
		select {
		case st.opened <- ErrAgentOffline:
		default:
		}
	})
}

func (s *agentSession) addStream(st *telegramStream) error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.isClosed() {
		return ErrAgentOffline
	}
	if len(s.streams) >= streamLimit {
		return ErrAgentBackpress
	}
	if _, exists := s.streams[st.id]; exists {
		return ErrAgentProtocol
	}
	s.streams[st.id] = st
	return nil
}

func (s *agentSession) stream(id string) *telegramStream {
	s.streamMu.Lock()
	st := s.streams[id]
	s.streamMu.Unlock()
	return st
}

func (s *agentSession) closeStream(id string, tellPeer bool) {
	s.streamMu.Lock()
	st := s.streams[id]
	delete(s.streams, id)
	s.streamMu.Unlock()
	if st == nil {
		return
	}
	st.close()
	if tellPeer {
		_ = s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_close", ID: id})
	}
}

func (s *agentSession) closeStreams() {
	s.streamMu.Lock()
	streams := s.streams
	s.streams = make(map[string]*telegramStream)
	s.streamMu.Unlock()
	for _, st := range streams {
		st.close()
	}
}

func (s *agentSession) dispatchStream(env agentEnvelope) {
	if env.ID == "" || len(env.ID) > 64 || len(env.Data) > streamChunkSize {
		s.closeWith(ErrAgentProtocol)
		return
	}
	if env.Type == "stream_open" {
		if !s.isClient || len(env.Data) != 0 || env.Op != "telegram" {
			s.closeWith(ErrAgentProtocol)
			return
		}
		st := newTelegramStream(env.ID)
		if err := s.addStream(st); err != nil {
			_ = s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_error", ID: env.ID, Error: "stream capacity reached"})
			return
		}
		go s.connectTelegram(st)
		return
	}
	st := s.stream(env.ID)
	if st == nil {
		return
	} // A frame may have been in flight during close.
	switch env.Type {
	case "stream_opened":
		if s.isClient {
			s.closeWith(ErrAgentProtocol)
			return
		}
		select {
		case st.opened <- nil:
		default:
		}
	case "stream_error":
		select {
		case st.opened <- errors.New(env.Error):
		default:
		}
		s.closeStream(env.ID, false)
	case "stream_data":
		if len(env.Data) == 0 {
			return
		}
		select {
		case st.recv <- env.Data:
		case <-st.done:
		default:
			s.closeStream(env.ID, true)
		}
	case "stream_close":
		s.closeStream(env.ID, false)
	}
}

func (s *agentSession) connectTelegram(st *telegramStream) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	conn, err := dialTelegramEndpoint(ctx)
	if err != nil {
		_ = s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_error", ID: st.id, Error: "Telegram egress is unavailable"})
		s.closeStream(st.id, false)
		return
	}
	if !st.setPeer(conn) {
		return
	}
	if err := s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_opened", ID: st.id}); err != nil {
		s.closeStream(st.id, false)
		return
	}
	s.pumpStream(st, conn)
}

// openTelegram attaches the already accepted local Unix connection only after
// the foreign Agent has confirmed its fixed outbound TCP dial.
func (s *agentSession) openTelegram(ctx context.Context, local net.Conn) error {
	st := newTelegramStream(newRequestID())
	if err := s.addStream(st); err != nil {
		return err
	}
	if err := s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_open", ID: st.id, Op: "telegram"}); err != nil {
		s.closeStream(st.id, false)
		return err
	}
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case err := <-st.opened:
		if err != nil {
			s.closeStream(st.id, true)
			return err
		}
	case <-openCtx.Done():
		s.closeStream(st.id, true)
		return openCtx.Err()
	case <-s.done:
		return ErrAgentOffline
	}
	if !st.setPeer(local) {
		return ErrAgentOffline
	}
	s.pumpStream(st, local)
	return nil
}

func (s *agentSession) pumpStream(st *telegramStream, conn net.Conn) {
	go func() {
		for {
			select {
			case data := <-st.recv:
				for len(data) > 0 {
					n, err := conn.Write(data)
					if err != nil || n == 0 {
						s.closeStream(st.id, true)
						return
					}
					data = data[n:]
				}
			case <-st.done:
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, streamChunkSize)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				if e := s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "stream_data", ID: st.id, Data: data}); e != nil {
					s.closeStream(st.id, true)
					return
				}
			}
			if err != nil {
				s.closeStream(st.id, true)
				return
			}
		}
	}()
}

func (h *Hub) telegramSessions() []*agentSession {
	var sessions []*agentSession
	for _, n := range List() {
		if n.Revoked || n.ID == "" {
			continue
		}
		if s, ok := h.SessionFor(n.ID); ok {
			sessions = append(sessions, s)
		}
	}
	return sessions
}

func (h *Hub) telegramSession() (*agentSession, error) {
	if sessions := h.telegramSessions(); len(sessions) > 0 {
		return sessions[0], nil
	}
	return nil, fmt.Errorf("%w: no connected Agent for Telegram", ErrAgentOffline)
}
