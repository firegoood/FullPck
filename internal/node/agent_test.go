package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHasAgentConfigTreatsPresenceAsManagedNodeMarker(t *testing.T) {
	dir := t.TempDir()
	old := AgentConfigPath
	AgentConfigPath = filepath.Join(dir, "node-agent.json")
	t.Cleanup(func() { AgentConfigPath = old })

	if HasAgentConfig() {
		t.Fatal("a missing Agent config marked the installation as managed")
	}
	if err := os.WriteFile(AgentConfigPath, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if !HasAgentConfig() {
		t.Fatal("a present Agent config did not mark the installation as managed")
	}
}

func TestIsManagedForeignHonorsTheInstallerRoleMarker(t *testing.T) {
	dir := t.TempDir()
	oldAgent, oldRole := AgentConfigPath, roleFilePath
	AgentConfigPath = filepath.Join(dir, "node-agent.json")
	roleFilePath = filepath.Join(dir, "role")
	t.Cleanup(func() { AgentConfigPath, roleFilePath = oldAgent, oldRole })

	if IsManagedForeign() {
		t.Fatal("an unconfigured installation was marked as foreign")
	}
	if err := os.WriteFile(roleFilePath, []byte("kharej\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !IsManagedForeign() {
		t.Fatal("the kharej role marker was ignored")
	}
	if err := os.WriteFile(roleFilePath, []byte("iran\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if IsManagedForeign() {
		t.Fatal("the Iran role marker was treated as foreign")
	}
	if err := os.WriteFile(AgentConfigPath, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if !IsManagedForeign() {
		t.Fatal("the Agent config did not override the Iran role marker")
	}
}

func TestEnrollmentPinsSelfSignedControllerTLS(t *testing.T) {
	dir := t.TempDir()
	oldStore, oldEnroll, oldAgent := StorePath, EnrollmentStorePath, AgentConfigPath
	StorePath = filepath.Join(dir, "nodes.json")
	EnrollmentStorePath = filepath.Join(dir, "enrollment.json")
	AgentConfigPath = filepath.Join(dir, "node-agent.json")
	t.Cleanup(func() { StorePath, EnrollmentStorePath, AgentConfigPath = oldStore, oldEnroll, oldAgent })
	srv := httptest.NewTLSServer(http.HandlerFunc(HandleEnrollmentHTTP))
	defer srv.Close()
	pin := sha256.Sum256(srv.Certificate().Raw)
	code, err := CreateEnrollmentPinned("tls-node", srv.URL,
		base64.RawURLEncoding.EncodeToString(pin[:]), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := JoinWithEnrollment(code, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSPinSHA256 == "" {
		t.Fatal("the Agent lost its TLS pin")
	}
	AgentConfigPath = filepath.Join(dir, "wrong-pin-agent.json")
	wrong := sha256.Sum256([]byte("different certificate"))
	badCode, err := CreateEnrollmentPinned("wrong-pin", srv.URL,
		base64.RawURLEncoding.EncodeToString(wrong[:]), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := JoinWithEnrollment(badCode, nil); err == nil {
		t.Fatal("enrollment accepted a different certificate")
	}
}

func TestEnrollmentIsOneTimeAndCreatesDistinctPermanentCredential(t *testing.T) {
	dir := t.TempDir()
	oldStore, oldEnroll, oldAgent := StorePath, EnrollmentStorePath, AgentConfigPath
	StorePath = filepath.Join(dir, "nodes.json")
	EnrollmentStorePath = filepath.Join(dir, "enrollment.json")
	AgentConfigPath = filepath.Join(dir, "node-agent.json")
	t.Cleanup(func() { StorePath, EnrollmentStorePath, AgentConfigPath = oldStore, oldEnroll, oldAgent })

	srv := httptest.NewServer(http.HandlerFunc(HandleEnrollmentHTTP))
	defer srv.Close()
	code, err := CreateEnrollment("kharej-1", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := JoinWithEnrollment(code, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Credential == "" || cfg.Credential == code {
		t.Fatal("enrollment token was reused as permanent credential")
	}
	if _, ok := findByID(cfg.NodeID); !ok {
		t.Fatal("controller did not persist enrolled node")
	}
	if _, err := JoinWithEnrollment(code, srv.Client()); err == nil {
		t.Fatal("enrollment code was accepted twice")
	}
}

func TestReverseAgentSessionAndTypedRPC(t *testing.T) {
	dir := t.TempDir()
	oldStore := StorePath
	StorePath = filepath.Join(dir, "nodes.json")
	t.Cleanup(func() { StorePath = oldStore })

	cred, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddManaged("kharej-1", "http://controller:7777", "node-1", cred); err != nil {
		t.Fatal(err)
	}
	hub := NewHub()
	t.Cleanup(func() { hub.Close() })
	ts := httptest.NewServer(http.HandlerFunc(hub.ServeHTTP))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunAgent(ctx, AgentConfig{NodeID: "node-1", Name: "kharej-1", ControllerURL: ts.URL, Credential: cred})
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !hub.IsOnline("node-1") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !hub.IsOnline("node-1") {
		t.Fatal("Agent did not connect")
	}
	var info Info
	if err := hub.Call(context.Background(), "node-1", OpHello, nil, &info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" {
		t.Fatal("typed hello response was empty")
	}
	if err := Revoke("kharej-1"); err != nil {
		t.Fatal(err)
	}
	if err := hub.Call(context.Background(), "node-1", OpHello, nil, &info); err != ErrAgentRevoked {
		t.Fatalf("revoked credential still allows RPC: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent did not stop")
	}
}
