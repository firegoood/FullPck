package node

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type agentLogBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *agentLogBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

func (b *agentLogBuffer) text() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func TestControllerReportsRejectedReadinessWithoutLoggingPeerSecrets(t *testing.T) {
	isolateEnrollment(t)
	var output agentLogBuffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	hub := NewHub()
	defer hub.Close()
	exited := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		hub.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cred, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddManaged("rejected", srv.URL, "rejected-id", cred); err != nil {
		t.Fatal(err)
	}
	cfg := AgentConfig{NodeID: "rejected-id", Name: "rejected", ControllerURL: srv.URL, Credential: cred}
	ws, _, err := dialController(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := clientSession(ws, cfg)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	defer client.closeWith(ErrAgentOffline)
	const secret = "peer-supplied-password-do-not-log"
	client.setHandler(func(env agentEnvelope) agentEnvelope {
		return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: env.ID, Error: secret}
	})
	client.start()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("rejected readiness did not finish")
	}
	if hub.IsOnline(cfg.NodeID) {
		t.Fatal("a rejected operation channel was published Online")
	}
	text := output.text()
	for _, want := range []string{"readiness ended", "sent=1 received=1", "last_authenticated="} {
		if !strings.Contains(text, want) {
			t.Fatalf("Controller journal lost session evidence %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{secret, cred, cfg.NodeID, cfg.ControllerURL} {
		if strings.Contains(text, forbidden) {
			t.Fatal("Controller journal exposed peer text or private configuration")
		}
	}
}

func TestReadyDistinguishesConnectedAgentThatDoesNotAnswerPing(t *testing.T) {
	isolateEnrollment(t)
	hub := NewHub()
	defer hub.Close()
	exited := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		hub.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cred, _ := GenerateCredential()
	if _, err := AddManaged("silent", srv.URL, "silent-id", cred); err != nil {
		t.Fatal(err)
	}
	cfg := AgentConfig{NodeID: "silent-id", ControllerURL: srv.URL, Credential: cred}
	ws, _, err := dialController(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	client, err := clientSession(ws, cfg)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	var pings atomic.Int32
	blocked := make(chan struct{})
	client.setHandler(func(env agentEnvelope) agentEnvelope {
		if env.Op == OpPing && pings.Add(1) > 1 {
			<-blocked
		}
		return agentEnvelope{Version: agentProtocolVersion, Type: "response", ID: env.ID, OK: true}
	})
	client.start()
	defer func() {
		close(blocked)
		client.closeWith(ErrAgentOffline)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			t.Error("Controller did not finish the silent session")
		}
	}()
	waitAgentCondition(t, func() bool { return hub.IsOnline(cfg.NodeID) })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = NewAgentRunner(hub).Ready(ctx, "silent")
	if err == nil || !strings.Contains(err.Error(), "connected but did not answer Ping") {
		t.Fatalf("an unanswered Ping did not report the operation channel failure: %v", err)
	}
	if !hub.IsOnline(cfg.NodeID) {
		t.Fatal("diagnostic Ping timeout changed the authenticated session")
	}
	if got := (&agentSession{}).activitySummary(); got != "sent=0 received=0 last_authenticated=never" {
		t.Fatalf("a session without traffic reported authenticated activity: %s", got)
	}
}
