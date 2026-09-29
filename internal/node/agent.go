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
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
}

func NewHub() *Hub {
	return &Hub{
		sessions:   make(map[string]*agentSession),
		generation: make(map[string]uint64),
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
		_ = ws.Close()
		return
	}
	s.start()
	// An authenticated handshake is necessary but not sufficient to show the
	// node Online. Prove the typed operation channel works and cache its hello
	// before replacing the old authoritative session.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	var info Info
	resp, err := s.call(ctx, Request{Op: OpHello})
	cancel()
	if err != nil || !resp.OK || json.Unmarshal(resp.Body, &info) != nil {
		if err == nil {
			err = ErrAgentProtocol
		}
		s.closeWith(err)
		return
	}
	if n, ok := findByID(id); ok && !n.Revoked {
		_ = NoteInfo(n.Name, info)
	} else {
		s.closeWith(ErrAgentRevoked)
		return
	}
	if !h.replace(id, s) {
		s.closeWith(ErrAgentRevoked)
		return
	}
	noteConnection(id, r.RemoteAddr, "", true)
	<-s.done
	h.remove(id, s)
}

// CloseNode tears down the live connection immediately after revocation or
// removal. The node's tunnel processes remain independent and untouched.
func (h *Hub) CloseNode(id string) {
	h.mu.Lock()
	s := h.sessions[id]
	if s != nil {
		delete(h.sessions, id)
	}
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
		noteConnection(id, "", "Agent connection closed", false)
	}
	h.mu.Unlock()
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
	writeMu    sync.Mutex
	readMu     sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan agentEnvelope
	requestSem chan struct{}
	handler    func(agentEnvelope) agentEnvelope
	generation uint64
	isClient   bool
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
			_ = s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			frame, err := s.encrypt(plain)
			if err != nil {
				s.closeWith(err)
				return
			}
			if err := s.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				s.closeWith(err)
				return
			}
		case <-t.C:
			if err := s.ws.WriteControl(websocket.PingMessage, []byte("bp"), time.Now().Add(5*time.Second)); err != nil {
				s.closeWith(err)
				return
			}
		}
	}
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
		// WebSocket Ping/Pong handles liveness; application heartbeats are
		// accepted for forward compatibility and intentionally carry no action.
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

func LoadAgentConfig() (AgentConfig, error) {
	var c AgentConfig
	b, err := os.ReadFile(AgentConfigPath)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.NodeID == "" || c.ControllerURL == "" || c.Credential == "" {
		return c, errors.New("node Agent configuration is incomplete")
	}
	return c, nil
}

func SaveAgentConfig(c AgentConfig) error {
	if c.NodeID == "" || c.ControllerURL == "" || c.Credential == "" {
		return errors.New("node Agent configuration is incomplete")
	}
	b, err := json.MarshalIndent(c, "", "  ")
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
	cfg, err := LoadAgentConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			<-ctx.Done()
		}
		return
	}
	RunAgent(ctx, cfg)
}

func RunAgent(ctx context.Context, cfg AgentConfig) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		ws, _, err := dialController(ctx, cfg)
		if err == nil {
			s, serr := clientSession(ws, cfg)
			if serr == nil {
				s.start()
				backoff = time.Second
				select {
				case <-ctx.Done():
					s.closeWith(ctx.Err())
					return
				case <-s.done:
				}
				continue
			}
			_ = ws.Close()
		}
		d := backoff + time.Duration(mrand.Int64N(int64(backoff/2)+1))
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
