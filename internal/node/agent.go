package node

// The reverse persistent Agent control plane.
//
// A managed node dials the controller's existing WebUI listener and keeps one
// authenticated WebSocket open. The controller never opens a management
// connection to the node. Noise protects the WebSocket payloads and the
// typed Request/Response protocol remains the only operation surface.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firegoood/FullPck/internal/app"
	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/hkdf"
)

const (
	// AgentPath is deliberately outside the browser base-path wrapper. It is
	// still on the exact same configured WebUI http.Server and port.
	AgentPath = "/_bp/node"

	agentProtocolVersion  = 1
	agentPrologue         = "fullpack-node-agent-v1"
	agentMaxMessage       = 1 << 20
	agentQueueSize        = 64
	agentMaxConcurrent    = 8
	agentPingInterval     = 25 * time.Second
	agentPongTimeout      = 75 * time.Second
	agentHandshakeTimeout = 15 * time.Second
)

var (
	ErrAgentOffline   = errors.New("managed node is offline")
	ErrAgentRevoked   = errors.New("managed node is revoked")
	ErrAgentBackpress = errors.New("managed node request queue is full")
	ErrAgentProtocol  = errors.New("invalid node Agent protocol message")
)

type agentHello struct {
	Version int    `json:"version"`
	NodeID  string `json:"node_id"`
	Name    string `json:"name,omitempty"`
}

type agentEnvelope struct {
	Version int             `json:"version"`
	Type    string          `json:"type"` // request, response, heartbeat
	ID      string          `json:"id,omitempty"`
	Op      string          `json:"op,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	Data    []byte          `json:"data,omitempty"` // bounded opaque stream chunk
	OK      bool            `json:"ok,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Hub owns the controller's live Agent sessions. It is safe for concurrent
// HTTP handlers and for a reconnect racing with the previous session closing.
type Hub struct {
	mu         sync.RWMutex
	sessions   map[string]*agentSession
	generation map[string]uint64
	changed    chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		sessions:   make(map[string]*agentSession),
		generation: make(map[string]uint64),
		changed:    make(chan struct{}),
	}
}

// DefaultHub is the single controller registry used by the WebUI and Fleet.
var DefaultHub = NewHub()

func (h *Hub) credentialFor(id string) (string, bool, bool) {
	if n, ok := findByID(id); ok {
		return n.Credential, !n.Revoked, true
	}
	return "", false, false
}

// ServeHTTP upgrades /_bp/node on the existing WebUI listener. CheckOrigin is
// intentionally permissive because this is machine-to-machine traffic: browser
// Origin is not authentication. Noise PSK authentication is authoritative.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != AgentPath {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	credential, allowed, known := h.credentialFor(id)
	if !known {
		http.Error(w, "unknown node", http.StatusUnauthorized)
		return
	}
	if !allowed {
		http.Error(w, ErrAgentRevoked.Error(), http.StatusForbidden)
		return
	}
	up := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(*http.Request) bool {
			return true
		},
	}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s, err := serverSession(ws, id, credential)
	if err != nil {
		log.Printf("Agent Node %s: authenticate failed (%s)", agentNodeLabel(id), agentFailureReason(err))
		_ = ws.Close()
		return
	}
	stage := "readiness"
	defer func() {
		log.Printf("Agent Node %s: %s ended (%s; %s)", agentNodeLabel(id), stage,
			agentFailureReason(s.closeErr), s.activitySummary())
	}()
	s.start()
	// Prove the authenticated operation channel before showing the Node online.
	// Optional host metadata must not delay or disconnect a working session.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	resp, err := s.call(ctx, Request{Op: OpPing})
	cancel()
	if err != nil || !resp.OK {
		if err == nil {
			err = ErrAgentProtocol
		}
		s.closeWith(err)
		return
	}
	if n, ok := findByID(id); !ok || n.Revoked {
		s.closeWith(ErrAgentRevoked)
		return
	}
	if !h.replace(id, s) {
		s.closeWith(ErrAgentRevoked)
		return
	}
	stage = "session"
	log.Printf("Agent Node %s: operation channel ready", agentNodeLabel(id))
	// A saved Agent config proves possession of the permanent credential. This
	// closes the enrollment if its final acknowledgement was lost in transit.
	_ = finishEnrollment(id, credential)
	noteConnection(id, r.RemoteAddr, "", true)
	go h.refreshSessionInfo(r.Context(), id, s)
	<-s.done
	h.remove(id, s)
}

func (h *Hub) refreshSessionInfo(parent context.Context, id string, s *agentSession) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	resp, err := s.call(ctx, Request{Op: OpHello})
	var info Info
	if err != nil || !resp.OK || json.Unmarshal(resp.Body, &info) != nil {
		return
	}
	current, online := h.SessionFor(id)
	if current != s || !online {
		return
	}
	if n, ok := findByID(id); ok && !n.Revoked {
		_ = NoteInfo(n.Name, info)
	}
}

// CloseNode tears down the live connection immediately after revocation or
// removal. The node's tunnel processes remain independent and untouched.
func (h *Hub) CloseNode(id string) {
	h.mu.Lock()
	s := h.sessions[id]
	if s != nil {
		delete(h.sessions, id)
	}
	h.notifyLocked()
	h.mu.Unlock()
	if s != nil {
		s.closeWith(ErrAgentRevoked)
		noteConnection(id, "", "Node credential revoked", false)
	}
}

// Close releases controller sessions when its WebUI lifetime ends. Tunnel
// processes and the independently running monitor are not touched.
func (h *Hub) Close() {
	h.mu.Lock()
	sessions := h.sessions
	h.sessions = make(map[string]*agentSession)
	h.notifyLocked()
	h.mu.Unlock()
	for id, s := range sessions {
		s.closeWith(ErrAgentOffline)
		noteConnection(id, "", "Controller stopped", false)
	}
}

func (h *Hub) replace(id string, s *agentSession) bool {
	h.mu.Lock()
	// A revoke can win after hello but before publication. Recheck while
	// holding the session lock so CloseNode can always find a published peer.
	n, ok := findByID(id)
	if !ok || n.Revoked {
		h.mu.Unlock()
		return false
	}
	old := h.sessions[id]
	h.generation[id]++
	s.generation = h.generation[id]
	h.sessions[id] = s
	h.notifyLocked()
	h.mu.Unlock()
	if old != nil {
		old.closeWith(ErrAgentOffline)
	}
	return true
}

func (h *Hub) remove(id string, s *agentSession) {
	h.mu.Lock()
	if h.sessions[id] == s {
		delete(h.sessions, id)
		h.notifyLocked()
		noteConnection(id, "", agentFailureReason(s.closeErr), false)
	}
	h.mu.Unlock()
}

// notifyLocked wakes readiness checks after publication, replacement or loss.
// The registry mutex protects both the session snapshot and its notification.
func (h *Hub) notifyLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

// Ready proves the typed operation path. Only the idempotent Ping is retried;
// apply/start/delete must never be replayed after an uncertain response.
func (h *Hub) Ready(ctx context.Context, id string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n, ok := findByID(id); !ok || n.Revoked {
			return ErrAgentRevoked
		}
		h.mu.RLock()
		s, changed := h.sessions[id], h.changed
		h.mu.RUnlock()
		if s != nil && !s.isClosed() {
			resp, err := s.call(ctx, Request{Op: OpPing})
			if err == nil {
				if !resp.OK {
					return ErrAgentProtocol
				}
				current, online := h.SessionFor(id)
				if current == s && online {
					if n, ok := findByID(id); ok && !n.Revoked {
						return nil
					}
					return ErrAgentRevoked
				}
			} else if !s.isClosed() {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// SessionFor returns the current session for a stable node identity.
func (h *Hub) SessionFor(id string) (*agentSession, bool) {
	h.mu.RLock()
	s, ok := h.sessions[id]
	h.mu.RUnlock()
	return s, ok && !s.isClosed()
}

func (h *Hub) IsOnline(id string) bool {
	_, ok := h.SessionFor(id)
	return ok
}

// Call routes one typed operation to the one authoritative live session.
func (h *Hub) Call(ctx context.Context, id, op string, body, out any) error {
	if n, ok := findByID(id); !ok || n.Revoked {
		return ErrAgentRevoked
	}
	s, ok := h.SessionFor(id)
	if !ok {
		return ErrAgentOffline
	}
	resp, err := s.call(ctx, Request{Op: op, Body: marshalBody(body)})
	if err != nil {
		return err
	}
	if !resp.OK {
		if resp.Err == "" {
			return ErrAgentProtocol
		}
		return errors.New(resp.Err)
	}
	if out != nil && len(resp.Body) > 0 {
		return json.Unmarshal(resp.Body, out)
	}
	return nil
}

// agentSession serialises all WebSocket writes through writeQ and keeps a
// bounded pending RPC map. A slow or non-reading peer cannot grow memory
// without limit.
type agentSession struct {
	ws         *websocket.Conn
	nodeID     string
	name       string
	send       *noise.CipherState
	recv       *noise.CipherState
	writeQ     chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error // assigned before done closes; readers wait for done
	writeMu    sync.Mutex
	readMu     sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan agentEnvelope
	requestSem chan struct{}
	handler    func(agentEnvelope) agentEnvelope
	generation uint64
	isClient   bool
	sent       atomic.Uint64
	received   atomic.Uint64
	lastRecv   atomic.Int64
	streamMu   sync.Mutex
	streams    map[string]*telegramStream
}

func (s *agentSession) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *agentSession) start() {
	go s.runGuarded(s.writeLoop)
	go s.runGuarded(s.readLoop)
}

// A panic in a session worker must not terminate the monitor or Controller.
// Closing the session lets the Agent reconnect through its normal backoff.
func (s *agentSession) runGuarded(fn func()) {
	defer func() {
		if recover() != nil {
			s.closeWith(ErrAgentProtocol)
		}
	}()
	fn()
}

func (s *agentSession) closeWith(err error) {
	s.closeOnce.Do(func() {
		s.closeErr = err
		close(s.done)
		_ = s.ws.Close()
		s.pendingMu.Lock()
		for id, ch := range s.pending {
			select {
			case ch <- agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: id, Error: err.Error()}:
			default:
			}
			delete(s.pending, id)
		}
		s.pendingMu.Unlock()
		s.closeStreams()
	})
}

func (s *agentSession) writeLoop() {
	t := time.NewTicker(agentPingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case plain := <-s.writeQ:
			if err := s.writeEncrypted(plain); err != nil {
				s.closeWith(err)
				return
			}
		case <-t.C:
			// Encrypted heartbeats also survive intermediaries that filter
			// WebSocket control frames. Older Agents already accept this type.
			if err := s.writeEncrypted(marshalBody(agentEnvelope{Version: agentProtocolVersion, Type: "heartbeat"})); err != nil {
				s.closeWith(err)
				return
			}
			if err := s.ws.WriteControl(websocket.PingMessage, []byte("bp"), time.Now().Add(5*time.Second)); err != nil {
				s.closeWith(err)
				return
			}
		}
	}
}

func (s *agentSession) writeEncrypted(plain []byte) error {
	_ = s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	frame, err := s.encrypt(plain)
	if err != nil {
		return err
	}
	if err := s.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return err
	}
	s.sent.Add(1)
	return nil
}

func (s *agentSession) readLoop() {
	s.ws.SetReadLimit(agentMaxMessage)
	_ = s.ws.SetReadDeadline(time.Now().Add(agentPongTimeout))
	s.ws.SetPongHandler(func(string) error {
		return s.ws.SetReadDeadline(time.Now().Add(agentPongTimeout))
	})
	for {
		typ, raw, err := s.ws.ReadMessage()
		if err != nil {
			s.closeWith(err)
			return
		}
		if typ != websocket.BinaryMessage {
			continue
		}
		plain, err := s.decrypt(raw)
		if err != nil || len(plain) > agentMaxMessage {
			s.closeWith(ErrAgentProtocol)
			return
		}
		var env agentEnvelope
		if err := json.Unmarshal(plain, &env); err != nil || env.Version != agentProtocolVersion {
			s.closeWith(ErrAgentProtocol)
			return
		}
		// Authenticated, valid traffic also proves liveness; malformed
		// ciphertext must never refresh the deadline.
		_ = s.ws.SetReadDeadline(time.Now().Add(agentPongTimeout))
		s.received.Add(1)
		s.lastRecv.Store(time.Now().UnixNano())
		s.dispatch(env)
	}
}

func (s *agentSession) dispatch(env agentEnvelope) {
	switch env.Type {
	case "response":
		s.pendingMu.Lock()
		ch := s.pending[env.ID]
		if ch != nil {
			delete(s.pending, env.ID)
		}
		s.pendingMu.Unlock()
		if ch != nil {
			ch <- env
		}
	case "request":
		if s.handler == nil {
			s.closeWith(ErrAgentProtocol)
			return
		}
		select {
		case s.requestSem <- struct{}{}:
		case <-s.done:
			return
		default:
			_ = s.enqueue(agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: env.ID, Error: "node request capacity is full"})
			return
		}
		go s.runGuarded(func() {
			defer func() { <-s.requestSem }()
			_ = s.enqueue(s.handler(env))
		})
	case "heartbeat":
		// The encrypted heartbeat proves liveness and carries no operation.
	case "stream_open", "stream_opened", "stream_data", "stream_close", "stream_error":
		s.dispatchStream(env)
	default:
		s.closeWith(ErrAgentProtocol)
	}
}

func (s *agentSession) call(ctx context.Context, req Request) (Response, error) {
	id := newRequestID()
	env := agentEnvelope{Version: agentProtocolVersion, Type: "request", ID: id, Op: req.Op, Body: req.Body}
	ch := make(chan agentEnvelope, 1)
	s.pendingMu.Lock()
	if s.isClosed() {
		s.pendingMu.Unlock()
		return Response{}, ErrAgentOffline
	}
	if len(s.pending) >= agentQueueSize {
		s.pendingMu.Unlock()
		return Response{}, ErrAgentBackpress
	}
	s.pending[id] = ch
	s.pendingMu.Unlock()
	if err := s.enqueue(env); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return Response{}, err
	}
	select {
	case out := <-ch:
		if out.Error != "" {
			return Response{}, errors.New(out.Error)
		}
		return Response{OK: out.OK, Err: out.Error, Body: out.Body}, nil
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return Response{}, ctx.Err()
	case <-s.done:
		return Response{}, ErrAgentOffline
	}
}

func (s *agentSession) enqueue(env agentEnvelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(raw) > agentMaxMessage-32 {
		return ErrAgentProtocol
	}
	select {
	case s.writeQ <- raw:
		return nil
	case <-s.done:
		return ErrAgentOffline
	default:
		return ErrAgentBackpress
	}
}

func (s *agentSession) encrypt(plain []byte) ([]byte, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.send.Encrypt(nil, nil, plain)
}

func (s *agentSession) decrypt(ciphertext []byte) ([]byte, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	return s.recv.Decrypt(nil, nil, ciphertext)
}

func (s *agentSession) setHandler(fn func(agentEnvelope) agentEnvelope) { s.handler = fn }

func (s *agentSession) requestHandler(env agentEnvelope) agentEnvelope {
	resp := Execute(Request{Op: env.Op, Body: env.Body})
	return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: env.ID,
		OK: resp.OK, Error: resp.Err, Body: resp.Body}
}

func marshalBody(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func credentialPSK(credential string) ([]byte, error) {
	if strings.TrimSpace(credential) == "" {
		return nil, errors.New("empty Agent credential")
	}
	key := hkdf.New(sha256.New, []byte(credential), []byte("fullpack-node-agent-psk-v1"), nil)
	psk := make([]byte, 32)
	_, err := io.ReadFull(key, psk)
	return psk, err
}

func handshakeConfig(initiator bool, credential string) (*noise.HandshakeState, error) {
	return handshakeConfigWithPrologue(initiator, credential, agentPrologue)
}

func handshakeConfigWithPrologue(initiator bool, credential, prologue string) (*noise.HandshakeState, error) {
	psk, err := credentialPSK(credential)
	if err != nil {
		return nil, err
	}
	return noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s),
		Pattern:     noise.HandshakeNN, Initiator: initiator,
		Prologue: []byte(prologue), PresharedKey: psk, PresharedKeyPlacement: 0,
	})
}

func serverSession(ws *websocket.Conn, expectedID, credential string) (*agentSession, error) {
	if err := ws.SetReadDeadline(time.Now().Add(agentHandshakeTimeout)); err != nil {
		return nil, err
	}
	hs, err := handshakeConfig(false, credential)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(agentMaxMessage)
	typ, msg, err := ws.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage {
		return nil, ErrAgentProtocol
	}
	payload, _, _, err := hs.ReadMessage(nil, msg)
	if err != nil {
		return nil, ErrAgentProtocol
	}
	var hello agentHello
	if json.Unmarshal(payload, &hello) != nil || hello.Version != agentProtocolVersion || hello.NodeID != expectedID {
		return nil, ErrAgentProtocol
	}
	reply, cs0, cs1, err := hs.WriteMessage(nil, []byte(`{"version":1}`))
	if err != nil {
		return nil, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, reply); err != nil {
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	// Noise assigns the responder's receive state to cs0 and send state to
	// cs1, the opposite of the initiator.
	return newSession(ws, expectedID, hello.Name, cs1, cs0, false), nil
}

func clientSession(ws *websocket.Conn, cfg AgentConfig) (*agentSession, error) {
	if err := ws.SetReadDeadline(time.Now().Add(agentHandshakeTimeout)); err != nil {
		return nil, err
	}
	hs, err := handshakeConfig(true, cfg.Credential)
	if err != nil {
		return nil, err
	}
	payload, _, _, err := hs.WriteMessage(nil, marshalBody(agentHello{Version: agentProtocolVersion, NodeID: cfg.NodeID, Name: cfg.Name}))
	if err != nil {
		return nil, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		return nil, err
	}
	typ, msg, err := ws.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage {
		return nil, ErrAgentProtocol
	}
	peer, send, recv, err := hs.ReadMessage(nil, msg)
	if err != nil || len(peer) == 0 {
		return nil, ErrAgentProtocol
	}
	_ = ws.SetReadDeadline(time.Time{})
	return newSession(ws, cfg.NodeID, cfg.Name, send, recv, true), nil
}

func newSession(ws *websocket.Conn, id, name string, send, recv *noise.CipherState, client bool) *agentSession {
	s := &agentSession{ws: ws, nodeID: id, name: name, send: send, recv: recv,
		writeQ: make(chan []byte, agentQueueSize), done: make(chan struct{}),
		pending: make(map[string]chan agentEnvelope), requestSem: make(chan struct{}, agentMaxConcurrent),
		isClient: client, streams: make(map[string]*telegramStream)}
	if client {
		s.setHandler(s.requestHandler)
	}
	return s
}

// AgentConfig is the complete node-side configuration. It is written with
// mode 0600 and never returned by controller APIs after enrollment.
type AgentConfig struct {
	NodeID        string `json:"node_id"`
	Name          string `json:"name"`
	ControllerURL string `json:"controller_url"`
	Credential    string `json:"credential"`
	TLSPinSHA256  string `json:"tls_pin_sha256,omitempty"`
}

// AgentConfigPath can be redirected by tests; production uses the root-only
// /etc/fullpack path.
var AgentConfigPath = app.NodeAgentConfig

// roleFilePath is only overridden by tests; production reads the installer's
// role marker from the root-only configuration directory.
var roleFilePath = app.RoleFile

// HasAgentConfig reports whether this installation has been enrolled as a
// managed foreign node. Presence is enough here: even a damaged or partial
// file must not make the CLI silently expose a WebUI listener on that node.
func HasAgentConfig() bool {
	_, err := os.Stat(AgentConfigPath)
	return err == nil
}

// IsManagedForeign reports whether this installation is intended to be a
// managed foreign node. The role marker covers the period between installation
// and enrollment; the Agent config keeps the decision after enrollment.
func IsManagedForeign() bool {
	if HasAgentConfig() {
		return true
	}
	b, err := os.ReadFile(roleFilePath)
	return err == nil && strings.EqualFold(strings.TrimSpace(string(b)), "kharej")
}

// The primary entry retains the existing file shape. Additional Controllers
// have independent Node IDs, credentials and TLS pins in the same private file.
type agentConfigFile struct {
	AgentConfig
	Additional []AgentConfig `json:"additional_controllers,omitempty"`
}

const maxAgentControllers = 8

var agentConfigMu sync.Mutex

// controllerIdentity ignores the browser prefix: machine gateways use the
// root of the configured listener. Different listeners remain independent.
func controllerIdentity(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid Controller URL")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}

func LoadAgentConfigs() ([]AgentConfig, error) {
	b, err := os.ReadFile(AgentConfigPath)
	if err != nil {
		return nil, err
	}
	return decodeAgentConfigs(b)
}

func decodeAgentConfigs(b []byte) ([]AgentConfig, error) {
	var file agentConfigFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, err
	}
	configs := append([]AgentConfig{file.AgentConfig}, file.Additional...)
	if len(configs) > maxAgentControllers {
		return nil, errors.New("too many Agent Controllers")
	}
	seen := make(map[string]bool)
	for _, c := range configs {
		key, err := controllerIdentity(c.ControllerURL)
		if err != nil || c.NodeID == "" || c.Credential == "" {
			return nil, errors.New("node Agent configuration is incomplete")
		}
		if seen[key] {
			return nil, errors.New("duplicate Agent Controller")
		}
		seen[key] = true
	}
	return configs, nil
}

func LoadAgentConfig() (AgentConfig, error) {
	configs, err := LoadAgentConfigs()
	if err != nil {
		return AgentConfig{}, err
	}
	return configs[0], nil
}

func SaveAgentConfig(c AgentConfig) error {
	key, err := controllerIdentity(c.ControllerURL)
	if err != nil || c.NodeID == "" || c.Credential == "" {
		return errors.New("node Agent configuration is incomplete")
	}
	agentConfigMu.Lock()
	defer agentConfigMu.Unlock()
	configs, err := LoadAgentConfigs()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	updated := false
	for i, old := range configs {
		oldKey, _ := controllerIdentity(old.ControllerURL)
		if oldKey == key {
			configs[i], updated = c, true
			break
		}
	}
	if !updated {
		if len(configs) >= maxAgentControllers {
			return errors.New("too many Agent Controllers")
		}
		configs = append(configs, c)
	}
	b, err := json.MarshalIndent(agentConfigFile{AgentConfig: configs[0], Additional: configs[1:]}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(AgentConfigPath, b)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return app.WriteFileAtomic(path, data, 0600)
}

// RunConfiguredAgent is the monitor job. A controller installation without a
// node config stays idle; a foreign node reconnects with capped exponential
// backoff and jitter instead of using panic supervision as reconnect logic.
func RunConfiguredAgent(ctx context.Context) {
	runConfiguredAgents(ctx, RunAgent, 3*time.Second)
}

// Only changed entries are restarted. Joining a second Controller does not
// tear down the first session or restart the monitor's other jobs.
func runConfiguredAgents(ctx context.Context, run func(context.Context, AgentConfig), interval time.Duration) {
	type worker struct {
		cfg    AgentConfig
		cancel context.CancelFunc
	}
	workers := make(map[string]worker)
	var wg sync.WaitGroup
	reload := func() {
		configs, err := LoadAgentConfigs()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// A malformed/temporarily unreadable file must not drop healthy sessions.
			return
		}
		keep := make(map[string]bool)
		for _, cfg := range configs {
			key, _ := controllerIdentity(cfg.ControllerURL)
			keep[key] = true
			if old, ok := workers[key]; ok {
				if old.cfg == cfg {
					continue
				}
				old.cancel()
			}
			workerCtx, cancel := context.WithCancel(ctx)
			workers[key] = worker{cfg: cfg, cancel: cancel}
			wg.Add(1)
			go func(cfg AgentConfig) {
				defer wg.Done()
				for workerCtx.Err() == nil {
					func() {
						defer func() {
							if recover() != nil {
								log.Print("node Agent job failed; reconnecting")
							}
						}()
						run(workerCtx, cfg)
					}()
					select {
					case <-workerCtx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}(cfg)
		}
		for key, old := range workers {
			if !keep[key] {
				old.cancel()
				delete(workers, key)
			}
		}
	}
	reload()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, w := range workers {
				w.cancel()
			}
			wg.Wait()
			return
		case <-ticker.C:
			reload()
		}
	}
}

func RunAgent(ctx context.Context, cfg AgentConfig) {
	var active *agentSession
	defer func() {
		if active != nil {
			active.closeWith(ErrAgentOffline)
		}
	}()
	backoff := time.Second
	controller, _ := controllerIdentity(cfg.ControllerURL)
	for {
		if ctx.Err() != nil {
			return
		}
		stage := "connect"
		ws, response, err := dialController(ctx, cfg)
		if err == nil {
			s, serr := clientSession(ws, cfg)
			stage, err = "authenticate", serr
			if serr == nil {
				active = s
				s.start()
				connectedAt := time.Now()
				log.Printf("Agent Controller %s: authenticated connection established", controller)
				select {
				case <-ctx.Done():
					s.closeWith(ctx.Err())
					return
				case <-s.done:
				}
				stage, err = "session", s.closeErr
				// A rapidly dropped authenticated connection needs the same
				// jittered backoff as a failed dial. Reset after a stable session.
				if time.Since(connectedAt) >= agentPongTimeout {
					backoff = time.Second
				}
			}
			_ = ws.Close()
		}
		d := backoff + time.Duration(mrand.Int64N(int64(backoff/2)+1))
		if ctx.Err() != nil {
			return
		}
		reason := agentFailureReason(err)
		if stage == "session" && active != nil {
			reason += "; " + active.activitySummary()
		}
		if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
			reason = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		log.Printf("Agent Controller %s: %s failed (%s); reconnecting in %s", controller, stage, reason, d.Round(10*time.Millisecond))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

// Only counters and a fixed-format age are logged. Never include envelope
// bodies, node credentials, raw network errors or peer-provided close text.
func (s *agentSession) activitySummary() string {
	last := "never"
	if at := s.lastRecv.Load(); at != 0 {
		last = time.Since(time.Unix(0, at)).Round(time.Second).String() + " ago"
	}
	return fmt.Sprintf("sent=%d received=%d last_authenticated=%s", s.sent.Load(), s.received.Load(), last)
}

// A short opaque identifier correlates Controller sessions without exposing
// the full identity or a name supplied in a request to the public gateway.
func agentNodeLabel(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", sum[:6])
}

// Do not log raw dial/TLS/peer errors: they can contain URL credentials or
// peer-controlled strings. Fixed reasons still identify the failing stage.
func agentFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrAgentRevoked):
		return "Node credential revoked"
	case errors.Is(err, ErrAgentProtocol):
		return "Agent authentication or protocol failed"
	case errors.Is(err, ErrAgentBackpress):
		return "Agent queue is full"
	case errors.Is(err, context.Canceled):
		return "Agent stopped"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return "Agent connection closed"
	}
	var closed *websocket.CloseError
	if errors.As(err, &closed) {
		return fmt.Sprintf("WebSocket closed (code %d)", closed.Code)
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return "Agent connection timed out"
	}
	return "Agent connection failed"
}

func dialController(ctx context.Context, cfg AgentConfig) (*websocket.Conn, *http.Response, error) {
	u, err := url.Parse(strings.TrimRight(cfg.ControllerURL, "/"))
	if err != nil || u.Host == "" {
		return nil, nil, fmt.Errorf("invalid controller URL")
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, nil, fmt.Errorf("controller URL must use http(s) or ws(s)")
	}
	u.Path = AgentPath
	if cfg.TLSPinSHA256 != "" && u.Scheme != "wss" {
		return nil, nil, errors.New("TLS certificate pin requires a wss controller URL")
	}
	q := u.Query()
	q.Set("id", cfg.NodeID)
	u.RawQuery = q.Encode()
	d := *websocket.DefaultDialer
	d.HandshakeTimeout = agentHandshakeTimeout
	if cfg.TLSPinSHA256 != "" {
		d.TLSClientConfig, err = pinnedTLSConfig(cfg.TLSPinSHA256, u.Hostname())
		if err != nil {
			return nil, nil, err
		}
	}
	return d.DialContext(ctx, u.String(), nil)
}

// pinnedTLSConfig keeps normal CA/hostname verification and also permits the
// exact certificate embedded in a trusted enrollment code. The scoped
// InsecureSkipVerify only delegates verification to VerifyConnection; it never
// accepts a certificate without a valid chain or an exact pin.
func pinnedTLSConfig(encodedPin, host string) (*tls.Config, error) {
	want, err := base64.RawURLEncoding.DecodeString(encodedPin)
	if err != nil || len(want) != sha256.Size {
		return nil, errors.New("invalid TLS certificate pin")
	}
	roots, _ := x509.SystemCertPool()
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("controller presented no TLS certificate")
			}
			leaf := cs.PeerCertificates[0]
			if err := leaf.VerifyHostname(host); err != nil {
				return err
			}
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots,
				Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
				return nil
			}
			sum := sha256.Sum256(leaf.Raw)
			if subtle.ConstantTimeCompare(want, sum[:]) != 1 {
				return errors.New("controller TLS certificate does not match the enrollment pin")
			}
			now := time.Now()
			if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return errors.New("controller TLS certificate is expired or not yet valid")
			}
			return nil
		},
	}, nil
}

// GenerateCredential returns an opaque permanent credential suitable for a
// setup link. The node ID is separate so rotating one does not silently change
// the identity shown in the controller.
func GenerateCredential() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashCredential is used for one-time enrollment records; the permanent
// credential itself is never written to the enrollment file.
func HashCredential(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
