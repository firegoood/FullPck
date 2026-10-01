package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Working encrypted RPC traffic must establish liveness even when an
// intermediary does not pass WebSocket control pongs. Use the actual WebUI
// HTTP deadlines and wait beyond the production Agent liveness deadline.
func TestAuthenticatedTrafficKeepsAgentSessionWithoutControlPongs(t *testing.T) {
	hub, cfg := trafficOnlyAgent(t)
	original, _ := hub.SessionFor(cfg.NodeID)
	runner := NewAgentRunner(hub)
	deadline := time.Now().Add(agentPongTimeout + time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := hub.Call(ctx, cfg.NodeID, OpPing, nil, nil)
		cancel()
		if err != nil {
			t.Fatalf("authenticated RPC traffic lost its healthy session: %v", err)
		}
		current, online := hub.SessionFor(cfg.NodeID)
		if !online || current != original || !runner.IsOnline("TR") {
			t.Fatal("healthy Agent was disconnected or replaced")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestIdleAgentEncryptedHeartbeatsKeepSessionWithoutControlPongs(t *testing.T) {
	hub, cfg := trafficOnlyAgent(t)
	original, _ := hub.SessionFor(cfg.NodeID)
	deadline := time.Now().Add(agentPongTimeout + time.Second)
	for time.Now().Before(deadline) {
		current, online := hub.SessionFor(cfg.NodeID)
		if !online || current != original {
			t.Fatal("idle authenticated Agent lost its heartbeat session")
		}
		time.Sleep(500 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := NewAgentRunner(hub).Ready(ctx, "TR"); err != nil {
		t.Fatal("idle session no longer answers a typed Ping", err)
	}
}

func trafficOnlyAgent(t *testing.T) (*Hub, AgentConfig) {
	t.Helper()
	isolateEnrollment(t)
	hub := NewHub()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(hub.ServeHTTP))
	srv.Config.ReadTimeout = 15 * time.Second
	srv.Config.WriteTimeout = 30 * time.Second
	srv.Start()
	t.Cleanup(func() { hub.Close(); srv.Close() })
	cred, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddManaged("TR", srv.URL, "traffic-id", cred); err != nil {
		t.Fatal(err)
	}
	cfg := AgentConfig{NodeID: "traffic-id", Name: "TR", ControllerURL: srv.URL, Credential: cred}
	ws, _, err := dialController(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := clientSession(ws, cfg)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	ws.SetPingHandler(func(string) error { return nil })
	client.setHandler(func(e agentEnvelope) agentEnvelope {
		return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: e.ID, OK: true}
	})
	client.start()
	t.Cleanup(func() { client.closeWith(ErrAgentOffline) })
	waitAgentCondition(t, func() bool { return hub.IsOnline(cfg.NodeID) })
	return hub, cfg
}

func TestReadyWaitsForAuthenticatedReconnectAndHonorsCancellation(t *testing.T) {
	isolateEnrollment(t)
	hub, srv := agentTestController(t)
	cred, _ := GenerateCredential()
	AddManaged("TR", srv.URL, "reconnect-id", cred)
	runner := NewAgentRunner(hub)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- runner.Ready(ctx, "TR") }()
	select {
	case err := <-ready:
		t.Fatalf("readiness did not wait for reconnect: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	agentCtx, stopAgent := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		RunAgent(agentCtx, AgentConfig{NodeID: "reconnect-id", Name: "TR", ControllerURL: srv.URL, Credential: cred})
		close(stopped)
	}()
	t.Cleanup(func() { stopAgent(); <-stopped })
	if err := <-ready; err != nil {
		t.Fatal("authenticated reconnect did not satisfy readiness", err)
	}
	hub.CloseNode("reconnect-id")
	stopAgent()
	<-stopped
	short, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()
	if err := runner.Ready(short, "TR"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("offline readiness ignored deadline: %v", err)
	}
	if err := Revoke("TR"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Ready(context.Background(), "TR"); err != ErrAgentRevoked {
		t.Fatalf("revoked Node passed readiness: %v", err)
	}
}

func TestAgentFailureReasonDoesNotExposeRawPeerOrConnectionErrors(t *testing.T) {
	const secret = "private-credential-do-not-log"
	for _, err := range []error{errors.New(secret), fmt.Errorf("https://user:%s@controller: %w", secret, context.DeadlineExceeded),
		&websocket.CloseError{Code: 1008, Text: secret}} {
		if strings.Contains(agentFailureReason(err), secret) {
			t.Fatal("raw connection error leaked a credential")
		}
	}
}
