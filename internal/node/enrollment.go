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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// A retry must reuse the credential that was offered to the controller. This
// intent is written on the node before the first network request, so losing a
// response cannot cause a new credential to be offered for the same code.
type pendingEnrollment struct {
	CodeHash   string `json:"code_hash"`
	NodeID     string `json:"node_id"`
	Credential string `json:"credential"`
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
	var err error
	controllerURL, err = ValidateControllerURL(controllerURL)
	if err != nil {
		return "", err
	}
	if err := validName(name); err != nil {
		return "", err
	}
	u, _ := url.Parse(controllerURL)
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
		if item.Used || (item.Expires <= time.Now().Unix() && !enrollmentProvisioned(item.NodeID)) {
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

// ValidateControllerURL accepts only an origin with an explicit port. Agent
// paths are fixed and browser path prefixes must never enter this endpoint.
func ValidateControllerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", errors.New("controller URL must be an http(s) origin with an explicit port")
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return "", errors.New("controller URL needs a valid host and port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("controller URL has an invalid port")
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 || strings.Contains(host, "..") {
			return "", errors.New("controller URL has an invalid host")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("controller URL has an invalid host")
			}
			for _, c := range label {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
					(c >= '0' && c <= '9') || c == '-') {
					return "", errors.New("controller URL has an invalid host")
				}
			}
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, portText), nil
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
	_, urlErr := ValidateControllerURL(out.ControllerURL)
	if idErr != nil || len(id) != 16 || tokenErr != nil || len(token) != 32 ||
		urlErr != nil {
		return enrollmentCode{}, errors.New("invalid enrollment code")
	}
	u, _ := url.Parse(out.ControllerURL)
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

// CompleteEnrollment is the direct completion helper. The WebSocket join uses
// the provision/ack steps separately because the node must save its config
// before the controller consumes the bootstrap credential.
func CompleteEnrollment(code, credential string) (AgentConfig, error) {
	e, err := parseEnrollment(code)
	if err != nil {
		return AgentConfig{}, err
	}
	cfg, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	if err == nil {
		err = finishEnrollment(e.NodeID, credential)
	}
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
	if found == nil || found.Used ||
		subtle.ConstantTimeCompare([]byte(found.TokenHash), []byte(tokenHash)) != 1 {
		return AgentConfig{}, errors.New("enrollment code is invalid, expired or already used")
	}
	// The registry's atomic write is the Provisioned commit. If it already
	// contains this identity, a lost response (or a crash after the commit)
	// returns the same result. A different credential is never allowed to rotate
	// that identity through a retry.
	if existing, ok := findByID(nodeID); ok {
		if existing.Revoked || existing.Name != found.Name || existing.ControllerURL != found.ControllerURL ||
			subtle.ConstantTimeCompare([]byte(existing.Credential), []byte(credential)) != 1 {
			return AgentConfig{}, errors.New("enrollment already provisioned with a different credential")
		}
		return AgentConfig{NodeID: found.NodeID, Name: found.Name, ControllerURL: found.ControllerURL, Credential: credential}, nil
	}
	if found.Expires <= time.Now().Unix() {
		return AgentConfig{}, errors.New("enrollment code has expired")
	}
	// An unsuccessful registry write leaves the token pending. The only writer
	// seals credentials and atomically replaces nodes.json.
	if _, err := AddManaged(found.Name, found.ControllerURL, found.NodeID, credential); err != nil {
		return AgentConfig{}, err
	}
	return AgentConfig{NodeID: found.NodeID, Name: found.Name, ControllerURL: found.ControllerURL, Credential: credential}, nil
}

func enrollmentProvisioned(id string) bool {
	n, ok := findByID(id)
	return ok && !n.Revoked && n.Credential != ""
}

// Completed is recorded only after the node has saved its config, or after it
// proves the permanent credential on the ordinary Agent channel. A failed
// completion write leaves the same Provisioned identity safely retryable.
func finishEnrollment(nodeID, credential string) error {
	enrollmentMu.Lock()
	defer enrollmentMu.Unlock()
	n, ok := findByID(nodeID)
	if !ok || n.Revoked || subtle.ConstantTimeCompare([]byte(n.Credential), []byte(credential)) != 1 {
		return errors.New("enrollment identity is unavailable")
	}
	items, err := loadEnrollments()
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].NodeID == nodeID {
			if items[i].Used {
				return nil
			}
			items[i].Used = true
			return saveEnrollments(items)
		}
	}
	return nil
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
	return writeEnrollmentFile(EnrollmentStorePath, b)
}

var writeEnrollmentFile = writePrivateFile

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
	Ack        bool   `json:"ack,omitempty"`
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
	unlock, err := lockJoin()
	if err != nil {
		return AgentConfig{}, err
	}
	defer unlock()
	// A pending intent lets an interrupted join resume with the exact same
	// secret. Check the config separately: a write can report failure after
	// rename, leaving a complete file that still needs confirmation.
	_, configErr := os.Stat(AgentConfigPath)
	configExists := configErr == nil
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return AgentConfig{}, configErr
	}
	pendingPath := AgentConfigPath + ".pending"
	var pending pendingEnrollment
	if b, readErr := os.ReadFile(pendingPath); readErr == nil {
		if json.Unmarshal(b, &pending) != nil || pending.CodeHash != HashCredential(code) || pending.NodeID != e.NodeID {
			return AgentConfig{}, errors.New("a different enrollment is pending on this node")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return AgentConfig{}, readErr
	} else {
		if configExists {
			return AgentConfig{}, errors.New("this node already has an Agent configuration")
		}
		credential, genErr := GenerateCredential()
		if genErr != nil {
			return AgentConfig{}, genErr
		}
		pending = pendingEnrollment{CodeHash: HashCredential(code), NodeID: e.NodeID, Credential: credential}
		b, _ := json.Marshal(pending)
		if err := writePrivateFile(pendingPath, b); err != nil {
			return AgentConfig{}, fmt.Errorf("saving retryable enrollment intent: %w", err)
		}
	}
	credential := pending.Credential
	if raw, decodeErr := base64.RawURLEncoding.DecodeString(credential); decodeErr != nil || len(raw) != 32 {
		return AgentConfig{}, errors.New("pending enrollment credential is invalid")
	}
	if configExists {
		cfg := AgentConfig{NodeID: e.NodeID, Name: e.Name, ControllerURL: e.ControllerURL,
			Credential: credential, TLSPinSHA256: e.TLSPinSHA256}
		current, err := LoadAgentConfig()
		if err != nil || current != cfg {
			return AgentConfig{}, errors.New("existing Agent configuration conflicts with pending enrollment")
		}
		if err := saveAgentConfigForJoin(cfg); err != nil {
			return AgentConfig{}, fmt.Errorf("saving Agent configuration (retry node join with the same code): %w", err)
		}
		_ = os.Remove(pendingPath)
		return cfg, nil
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
	ws, response, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		if response != nil {
			if response.Body != nil {
				defer response.Body.Close()
			}
			switch response.StatusCode {
			case http.StatusConflict:
				return AgentConfig{}, errors.New("enrollment code was already completed")
			case http.StatusGone:
				return AgentConfig{}, errors.New("enrollment code has expired")
			case http.StatusForbidden:
				return AgentConfig{}, errors.New("enrollment was revoked")
			case http.StatusUnauthorized:
				return AgentConfig{}, errors.New("enrollment code is invalid")
			}
		}
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
	if err := saveAgentConfigForJoin(cfg); err != nil {
		return AgentConfig{}, fmt.Errorf("saving Agent configuration (retry node join with the same code): %w", err)
	}
	// The permanent config now owns the credential. The retry intent has served
	// its purpose even if the final acknowledgement is lost; the ordinary Agent
	// connection will complete the Controller record in that case.
	_ = os.Remove(pendingPath)
	// The config is durable before acknowledgement. If the acknowledgement is
	// lost, the normal authenticated Agent connection also completes enrollment.
	ack, _ := json.Marshal(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID, Ack: true})
	if sealedAck, sealErr := send.Encrypt(nil, nil, ack); sealErr == nil {
		if ws.WriteMessage(websocket.BinaryMessage, sealedAck) == nil {
			// Give the Controller a chance to commit completion before returning.
			// Its response can be lost; the durable Agent config remains usable.
			_, _, _ = ws.ReadMessage()
		}
	}
	return cfg, nil
}

// The CLI can be invoked twice in separate processes. Keep the local retry
// intent and Agent config a single transaction even in that case. flock is
// released by the kernel if the process crashes; the small lock file can stay.
func lockJoin() (func(), error) {
	path := AgentConfigPath + ".join.lock"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another node join is already in progress")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// The join's final write is injectable so a disk failure after controller
// provisioning can be tested without weakening production persistence.
var saveAgentConfigForJoin = SaveAgentConfig

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
	if stored.TokenHash == "" {
		http.Error(w, "invalid enrollment", http.StatusUnauthorized)
		return
	}
	if n, ok := findByID(id); ok && n.Revoked {
		http.Error(w, "revoked enrollment", http.StatusForbidden)
		return
	}
	if stored.Used {
		http.Error(w, "completed enrollment", http.StatusConflict)
		return
	}
	if stored.Expires <= time.Now().Unix() && !enrollmentProvisioned(id) {
		http.Error(w, "expired enrollment", http.StatusGone)
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
		answer.Error = err.Error()
	}
	b, _ := json.Marshal(answer)
	sealed, sealErr := cs1.Encrypt(nil, nil, b)
	if sealErr == nil {
		_ = ws.WriteMessage(websocket.BinaryMessage, sealed)
	}
	if err != nil || sealErr != nil {
		return
	}
	_, encryptedAck, err := ws.ReadMessage()
	if err != nil {
		return
	}
	plainAck, err := cs0.Decrypt(nil, nil, encryptedAck)
	if err != nil {
		return
	}
	var ack enrollmentMessage
	if json.Unmarshal(plainAck, &ack) != nil || !ack.Ack || ack.Version != EnrollmentVersion || ack.NodeID != id {
		return
	}
	answer = enrollmentMessage{Version: EnrollmentVersion, NodeID: id, Ack: true}
	if finishEnrollment(id, request.Credential) != nil {
		answer.Error = "enrollment confirmation could not be saved"
	}
	b, _ = json.Marshal(answer)
	if reply, sealErr := cs1.Encrypt(nil, nil, b); sealErr == nil {
		_ = ws.WriteMessage(websocket.BinaryMessage, reply)
	}
}
