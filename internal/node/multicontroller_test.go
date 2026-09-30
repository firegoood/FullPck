package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func agentTestController(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	mux := http.NewServeMux()
	mux.Handle(AgentPath, hub)
	mux.HandleFunc(EnrollmentPath, HandleEnrollmentHTTP)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { hub.Close(); srv.Close() })
	return hub, srv
}

func waitAgentCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatal("Agent condition was not satisfied")
	}
}

func TestOneForeignNodeKeepsTwoControllerSessions(t *testing.T) {
	isolateEnrollment(t)
	hub1, srv1 := agentTestController(t)
	hub2, srv2 := agentTestController(t)
	code1, err := CreateEnrollment("iran-one-node", srv1.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := JoinWithEnrollment(code1, srv1.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runConfiguredAgents(ctx, RunAgent, 20*time.Millisecond); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Agent workers did not stop")
		}
	})
	waitAgentCondition(t, func() bool { return hub1.IsOnline(first.NodeID) })
	oldSession, _ := hub1.SessionFor(first.NodeID)
	code2, err := CreateEnrollment("iran-two-node", srv2.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := JoinWithEnrollment(code2, srv2.Client())
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeID == second.NodeID || first.Credential == second.Credential {
		t.Fatal("Controllers share identity or permanent credential")
	}
	waitAgentCondition(t, func() bool { return hub2.IsOnline(second.NodeID) })
	current, online := hub1.SessionFor(first.NodeID)
	if !online || current != oldSession {
		t.Fatal("second join restarted the first Controller session")
	}
	if hub1.IsOnline(second.NodeID) || hub2.IsOnline(first.NodeID) {
		t.Fatal("a session appeared on the wrong Controller")
	}
	configs, err := LoadAgentConfigs()
	if err != nil || len(configs) != 2 || configs[0] != first || configs[1] != second {
		t.Fatalf("first configuration was lost: %v (%d entries)", err, len(configs))
	}
	info, err := os.Stat(AgentConfigPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("multi-Controller credentials are not private")
	}
	// Losing one control connection must not take down the other.
	hub1.CloseNode(first.NodeID)
	if !hub2.IsOnline(second.NodeID) {
		t.Fatal("closing one Controller disconnected the other")
	}
	var ignored any
	if err := hub2.Call(ctx, second.NodeID, OpPing, nil, &ignored); err != nil {
		t.Fatal(err)
	}
}

func TestPendingJoinForOneControllerDoesNotBlockAnother(t *testing.T) {
	isolateEnrollment(t)
	_, srv1 := agentTestController(t)
	_, srv2 := agentTestController(t)
	code1, _ := CreateEnrollment("pending-one", srv1.URL, time.Minute)
	saveAgentConfigForJoin = func(AgentConfig) error { return errors.New("injected disk failure") }
	if _, err := JoinWithEnrollment(code1, srv1.Client()); err == nil {
		t.Fatal("injected write failure was ignored")
	}
	saveAgentConfigForJoin = SaveAgentConfig
	code2, _ := CreateEnrollment("pending-two", srv2.URL, time.Minute)
	second, err := JoinWithEnrollment(code2, srv2.Client())
	if err != nil {
		t.Fatalf("another Controller's pending intent blocked enrollment: %v", err)
	}
	first, err := JoinWithEnrollment(code1, srv1.Client())
	if err != nil {
		t.Fatalf("original interrupted enrollment could not resume: %v", err)
	}
	configs, err := LoadAgentConfigs()
	if err != nil || len(configs) != 2 || configs[0] != second || configs[1] != first {
		t.Fatal("retry overwrote an existing Controller")
	}
	if _, err := JoinWithEnrollment(code2, srv2.Client()); err == nil {
		t.Fatal("completed enrollment was accepted again")
	}
}

func TestAgentOnlineDoesNotWaitForOptionalHello(t *testing.T) {
	isolateEnrollment(t)
	hub, srv := agentTestController(t)
	cred, _ := GenerateCredential()
	if _, err := AddManaged("slow-hello", srv.URL, "slow-id", cred); err != nil {
		t.Fatal(err)
	}
	cfg := AgentConfig{NodeID: "slow-id", Name: "slow-hello", ControllerURL: srv.URL, Credential: cred}
	ws, _, err := dialController(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	session, err := clientSession(ws, cfg)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	session.setHandler(func(e agentEnvelope) agentEnvelope {
		if e.Op == OpHello {
			entered <- struct{}{}
			<-gate
		}
		return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: e.ID, OK: true}
	})
	session.start()
	t.Cleanup(func() { close(gate); session.closeWith(ErrAgentOffline) })
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata request never arrived")
	}
	if !hub.IsOnline(cfg.NodeID) {
		t.Fatal("metadata blocked authenticated Online state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Call(ctx, cfg.NodeID, OpPing, nil, nil); err != nil {
		t.Fatal("slow metadata blocked independent RPC", err)
	}
}

func TestAgentMustAnswerAuthenticatedPingBeforeOnline(t *testing.T) {
	isolateEnrollment(t)
	hub, srv := agentTestController(t)
	cred, _ := GenerateCredential()
	AddManaged("no-ping", srv.URL, "no-ping-id", cred)
	cfg := AgentConfig{NodeID: "no-ping-id", ControllerURL: srv.URL, Credential: cred}
	ws, _, err := dialController(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	session, err := clientSession(ws, cfg)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	session.setHandler(func(e agentEnvelope) agentEnvelope {
		return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: e.ID, OK: false}
	})
	session.start()
	defer session.closeWith(ErrAgentOffline)
	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		t.Fatal("rejected readiness ping did not close session")
	}
	if hub.IsOnline(cfg.NodeID) {
		t.Fatal("unready session was registered as Online")
	}
}

func TestAgentRapidSessionDropsUseReconnectBackoff(t *testing.T) {
	cred, _ := GenerateCredential()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s, err := serverSession(ws, "drop-id", cred)
		if err != nil {
			ws.Close()
			return
		}
		attempts.Add(1)
		s.start()
		s.closeWith(ErrAgentOffline)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	RunAgent(ctx, AgentConfig{NodeID: "drop-id", ControllerURL: srv.URL, Credential: cred})
	if n := attempts.Load(); n < 1 || n > 2 {
		t.Fatalf("rapid drops caused %d reconnects in 1.8s", n)
	}
}
