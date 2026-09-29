package node

// One-time enrollment for reverse Agent Nodes.
//
// Enrollment credentials are deliberately a different class of secret from
// the permanent Agent credential. The controller stores only a hash and an
// expiry; after a successful join the token is consumed and can never open a
// normal Agent session.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/firegoood/FullPck/internal/app"
	"github.com/gorilla/websocket"
)

const (
	EnrollmentVersion = "BPENROLL1"
	EnrollmentPath    = "/_bp/node/enroll"
	defaultEnrollTTL  = 15 * time.Minute
)

type enrollmentCode struct {
	Version       string `json:"version"`
	NodeID        string `json:"node_id"`
	Name          string `json:"name"`
	ControllerURL string `json:"controller_url"`
	Token         string `json:"token"`
	TLSPinSHA256  string `json:"tls_pin_sha256,omitempty"`
}

type enrollmentRecord struct {
	NodeID        string `json:"node_id"`
	Name          string `json:"name"`
	ControllerURL string `json:"controller_url"`
	TokenHash     string `json:"token_hash"`
	Expires       int64  `json:"expires"`
	Used          bool   `json:"used,omitempty"`
}

var enrollmentMu sync.Mutex

// EnrollmentStorePath can be overridden by package tests.
var EnrollmentStorePath = app.NodeEnrollmentConfig

// CreateEnrollment makes a short-lived, one-time code. The plaintext token is
// returned once to the operator and is never written to disk or logs.
func CreateEnrollment(name, controllerURL string, ttl time.Duration) (string, error) {
	return CreateEnrollmentPinned(name, controllerURL, "", ttl)
}

// CreateEnrollmentPinned includes a leaf certificate fingerprint for a panel
// that may serve its own self-signed fallback certificate. Ordinary trusted
// certificates still use normal chain and hostname validation.
func CreateEnrollmentPinned(name, controllerURL, tlsPin string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = defaultEnrollTTL
	}
	name = strings.TrimSpace(name)
	controllerURL = strings.TrimRight(strings.TrimSpace(controllerURL), "/")
	if err := validName(name); err != nil {
		return "", err
	}
	u, err := url.Parse(controllerURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the controller URL must start with http:// or https://")
	}
	if tlsPin != "" {
		pin, pinErr := base64.RawURLEncoding.DecodeString(tlsPin)
		if u.Scheme != "https" || pinErr != nil || len(pin) != sha256.Size {
			return "", errors.New("invalid TLS certificate pin")
		}
	}
	if _, exists := Find(name); exists {
		return "", fmt.Errorf("a server called %q is already in the fleet", name)
	}
	nodeID, err := randomToken(16)
	if err != nil {
		return "", err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	rec := enrollmentRecord{NodeID: nodeID, Name: name, ControllerURL: controllerURL,
		TokenHash: HashCredential(token), Expires: time.Now().Add(ttl).Unix()}
	enrollmentMu.Lock()
	defer enrollmentMu.Unlock()
	items, err := loadEnrollments()
	if err != nil {
		return "", err
	}
	// Expired bootstrap material is not useful and should not accumulate in
	// the controller store. An outstanding code reserves the chosen name.
	active := items[:0]
	for _, item := range items {
		if item.Used || item.Expires <= time.Now().Unix() {
			continue
		}
		if strings.EqualFold(item.Name, name) {
			return "", fmt.Errorf("an enrollment code for %q is already active", name)
		}
		active = append(active, item)
	}
	items = active
	items = append(items, rec)
	if err := saveEnrollments(items); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(enrollmentCode{Version: EnrollmentVersion, NodeID: nodeID,
		Name: name, ControllerURL: controllerURL, Token: token, TLSPinSHA256: tlsPin})
	return EnrollmentVersion + ":" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func parseEnrollment(code string) (enrollmentCode, error) {
	var out enrollmentCode
	code = strings.TrimSpace(code)
	if len(code) > 4096 {
		return out, errors.New("enrollment code is too long")
	}
	prefix, payload, ok := strings.Cut(code, ":")
	if !ok || prefix != EnrollmentVersion {
		return out, errors.New("invalid or unsupported enrollment code")
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(b, &out) != nil || out.Version != EnrollmentVersion ||
		out.NodeID == "" || out.Token == "" || out.ControllerURL == "" {
		return out, errors.New("invalid enrollment code")
	}
	id, idErr := base64.RawURLEncoding.DecodeString(out.NodeID)
	token, tokenErr := base64.RawURLEncoding.DecodeString(out.Token)
	u, urlErr := url.Parse(out.ControllerURL)
	if idErr != nil || len(id) != 16 || tokenErr != nil || len(token) != 32 ||
		urlErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return enrollmentCode{}, errors.New("invalid enrollment code")
	}
	if out.TLSPinSHA256 != "" {
		pin, err := base64.RawURLEncoding.DecodeString(out.TLSPinSHA256)
		if err != nil || len(pin) != sha256.Size || u.Scheme != "https" {
			return enrollmentCode{}, errors.New("invalid TLS certificate pin")
		}
	}
	return out, nil
}

// CertificatePin reads the public leaf certificate, never its key.
func CertificatePin(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid panel certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// CompleteEnrollment validates and consumes a code, stores the permanent
// credential on the controller, and returns the node's stable identity.
func CompleteEnrollment(code, credential string) (AgentConfig, error) {
	e, err := parseEnrollment(code)
	if err != nil {
		return AgentConfig{}, err
	}
	cfg, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	cfg.TLSPinSHA256 = e.TLSPinSHA256
	return cfg, err
}

func completeEnrollmentRecord(nodeID, tokenHash, credential string) (AgentConfig, error) {
	if strings.TrimSpace(credential) == "" {
		return AgentConfig{}, errors.New("permanent Agent credential is required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(credential)
	if err != nil || len(decoded) != 32 || subtle.ConstantTimeCompare([]byte(HashCredential(credential)), []byte(tokenHash)) == 1 {
		return AgentConfig{}, errors.New("permanent Agent credential must be a distinct 32-byte secret")
	}
	enrollmentMu.Lock()
	defer enrollmentMu.Unlock()
	items, err := loadEnrollments()
	if err != nil {
		return AgentConfig{}, err
	}
	var found *enrollmentRecord
	for i := range items {
		if items[i].NodeID == nodeID {
			found = &items[i]
			break
		}
	}
	if found == nil || found.Used || found.Expires <= time.Now().Unix() ||
		subtle.ConstantTimeCompare([]byte(found.TokenHash), []byte(tokenHash)) != 1 {
		return AgentConfig{}, errors.New("enrollment code is invalid, expired or already used")
	}
	// AddManaged seals the permanent credential through the controller's normal
	// registry writer. The enrollment token itself never enters that registry.
	found.Used = true
	if err := saveEnrollments(items); err != nil {
		return AgentConfig{}, err
	}
	if _, err := AddManaged(found.Name, found.ControllerURL, found.NodeID, credential); err != nil {
		return AgentConfig{}, err
	}
	return AgentConfig{NodeID: found.NodeID, Name: found.Name, ControllerURL: found.ControllerURL, Credential: credential}, nil
}

func loadEnrollments() ([]enrollmentRecord, error) {
	var items []enrollmentRecord
	b, err := osReadFile(EnrollmentStorePath)
	if err != nil {
		if errors.Is(err, errFileNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, fmt.Errorf("read enrollment store: %w", err)
	}
	return items, nil
}

func saveEnrollments(items []enrollmentRecord) error {
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(EnrollmentStorePath, b)
}

// Small indirections keep enrollment tests independent from the host OS and
// let the normal implementation stay with the standard library.
var (
	osReadFile      = func(path string) ([]byte, error) { return os.ReadFile(path) }
	errFileNotFound = os.ErrNotExist
)

type enrollmentMessage struct {
	Version    string `json:"version"`
	NodeID     string `json:"node_id"`
	Credential string `json:"credential"`
	Error      string `json:"error,omitempty"`
}

// JoinWithEnrollment completes a join from the operator-provided code. The
// permanent credential is generated locally, sent inside a Noise-authenticated
// WebSocket session, and saved after the controller confirms success. The
// one-time token never travels in a URL, HTTP body, or process argument.
func JoinWithEnrollment(code string, client *http.Client) (AgentConfig, error) {
	e, err := parseEnrollment(code)
	if err != nil {
		return AgentConfig{}, err
	}
	credential, err := GenerateCredential()
	if err != nil {
		return AgentConfig{}, err
	}
	u, err := url.Parse(e.ControllerURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return AgentConfig{}, errors.New("invalid controller URL in enrollment code")
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path = EnrollmentPath
	u.RawQuery = "id=" + url.QueryEscape(e.NodeID)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = agentHandshakeTimeout
	if e.TLSPinSHA256 != "" {
		dialer.TLSClientConfig, err = pinnedTLSConfig(e.TLSPinSHA256, u.Hostname())
		if err != nil {
			return AgentConfig{}, err
		}
	} else if client != nil && client.Transport != nil {
		// Tests and explicit operator trust settings can provide a TLS policy.
		// The default dialer keeps normal certificate/hostname verification.
		if tr, ok := client.Transport.(*http.Transport); ok {
			dialer.TLSClientConfig = tr.TLSClientConfig
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ws, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("controller enrollment failed: %w", err)
	}
	defer ws.Close()
	ws.SetReadLimit(16 << 10)
	_ = ws.SetReadDeadline(time.Now().Add(agentHandshakeTimeout))
	_ = ws.SetWriteDeadline(time.Now().Add(agentHandshakeTimeout))
	hs, err := handshakeConfigWithPrologue(true, HashCredential(e.Token), "fullpack-node-enrollment-v1")
	if err != nil {
		return AgentConfig{}, err
	}
	first, _, _, err := hs.WriteMessage(nil, marshalBody(agentHello{Version: agentProtocolVersion, NodeID: e.NodeID}))
	if err != nil {
		return AgentConfig{}, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, first); err != nil {
		return AgentConfig{}, err
	}
	_, second, err := ws.ReadMessage()
	if err != nil {
		return AgentConfig{}, err
	}
	_, send, recv, err := hs.ReadMessage(nil, second)
	if err != nil {
		return AgentConfig{}, errors.New("controller rejected enrollment authentication")
	}
	request, _ := json.Marshal(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID, Credential: credential})
	sealed, err := send.Encrypt(nil, nil, request)
	if err != nil {
		return AgentConfig{}, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, sealed); err != nil {
		return AgentConfig{}, err
	}
	_, ciphertext, err := ws.ReadMessage()
	if err != nil {
		return AgentConfig{}, err
	}
	plain, err := recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return AgentConfig{}, ErrAgentProtocol
	}
	var answer enrollmentMessage
	if json.Unmarshal(plain, &answer) != nil || answer.Version != EnrollmentVersion || answer.NodeID != e.NodeID {
		return AgentConfig{}, ErrAgentProtocol
	}
	if answer.Error != "" {
		return AgentConfig{}, fmt.Errorf("controller enrollment rejected: %s", answer.Error)
	}
	cfg := AgentConfig{NodeID: e.NodeID, Name: e.Name, ControllerURL: e.ControllerURL,
		Credential: credential, TLSPinSHA256: e.TLSPinSHA256}
	if err := SaveAgentConfig(cfg); err != nil {
		return AgentConfig{}, err
	}
	return cfg, nil
}

// HandleEnrollmentHTTP is mounted on the same WebUI listener as the Agent
// gateway. It accepts only the narrow Noise-encrypted enrollment exchange,
// never commands or arbitrary paths.
func HandleEnrollmentHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != EnrollmentPath {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "invalid enrollment", http.StatusBadRequest)
		return
	}
	enrollmentMu.Lock()
	items, err := loadEnrollments()
	enrollmentMu.Unlock()
	if err != nil {
		http.Error(w, "enrollment unavailable", http.StatusServiceUnavailable)
		return
	}
	var stored enrollmentRecord
	for _, item := range items {
		if item.NodeID == id {
			stored = item
			break
		}
	}
	if stored.Expires <= time.Now().Unix() || stored.Used || stored.TokenHash == "" {
		http.Error(w, "enrollment unavailable", http.StatusUnauthorized)
		return
	}
	// Machine clients have no meaningful browser Origin. Noise PSK0 is the
	// authentication boundary; the bootstrap PSK is distinct from the Agent PSK.
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(16 << 10)
	_ = ws.SetReadDeadline(time.Now().Add(agentHandshakeTimeout))
	hs, err := handshakeConfigWithPrologue(false, stored.TokenHash, "fullpack-node-enrollment-v1")
	if err != nil {
		return
	}
	_, first, err := ws.ReadMessage()
	if err != nil {
		return
	}
	payload, _, _, err := hs.ReadMessage(nil, first)
	if err != nil {
		return
	}
	var hello agentHello
	if json.Unmarshal(payload, &hello) != nil || hello.Version != agentProtocolVersion || hello.NodeID != id {
		return
	}
	second, cs0, cs1, err := hs.WriteMessage(nil, nil)
	if err != nil || ws.WriteMessage(websocket.BinaryMessage, second) != nil {
		return
	}
	_, ciphertext, err := ws.ReadMessage()
	if err != nil {
		return
	}
	plain, err := cs0.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return
	}
	var request enrollmentMessage
	if json.Unmarshal(plain, &request) != nil || request.Version != EnrollmentVersion || request.NodeID != id {
		return
	}
	_, err = completeEnrollmentRecord(id, stored.TokenHash, request.Credential)
	answer := enrollmentMessage{Version: EnrollmentVersion, NodeID: id}
	if err != nil {
		answer.Error = "invalid, expired or used enrollment"
	}
	b, _ := json.Marshal(answer)
	sealed, sealErr := cs1.Encrypt(nil, nil, b)
	if sealErr == nil {
		_ = ws.WriteMessage(websocket.BinaryMessage, sealed)
	}
}
