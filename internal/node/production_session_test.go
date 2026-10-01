package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Exercise the real monitor client and Execute handler, including the initial
// Ping and Hello over pinned TLS. The other liveness tests suppress control
// pongs over HTTP; this covers the real client on the supported HTTPS path.
func TestRunAgentKeepsPinnedTLSSessionBeyondLivenessDeadline(t *testing.T) {
	isolateEnrollment(t)
	hub := NewHub()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(hub.ServeHTTP))
	srv.Config.ReadTimeout = 15 * time.Second
	srv.Config.WriteTimeout = 30 * time.Second
	srv.Config.ReadHeaderTimeout = 10 * time.Second
	srv.Config.IdleTimeout = 90 * time.Second
	var handlers sync.WaitGroup
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		hub.ServeHTTP(w, r)
	})
	srv.StartTLS()
	defer srv.Close()
	defer hub.Close()
	cred, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddManaged("production", srv.URL, "production-id", cred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pin := sha256.Sum256(srv.Certificate().Raw)
	stopped := make(chan struct{})
	go func() {
		RunAgent(ctx, AgentConfig{NodeID: "production-id", Name: "production", ControllerURL: srv.URL, Credential: cred,
			TLSPinSHA256: base64.RawURLEncoding.EncodeToString(pin[:])})
		close(stopped)
	}()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Error("production Agent did not stop")
		}
		hub.Close()
		finished := make(chan struct{})
		go func() { handlers.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Error("Controller did not finish the production session")
		}
	}()
	waitAgentCondition(t, func() bool { return hub.IsOnline("production-id") })
	original, _ := hub.SessionFor("production-id")
	deadline := time.Now().Add(agentPongTimeout + time.Second)
	for time.Now().Before(deadline) {
		current, online := hub.SessionFor("production-id")
		if !online || current != original {
			t.Fatal("production Agent disconnected or reconnected before the liveness deadline")
		}
		time.Sleep(500 * time.Millisecond)
	}
	ready, cancelReady := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReady()
	if err := NewAgentRunner(hub).Ready(ready, "production"); err != nil {
		t.Fatal("production Agent no longer answers Ping", err)
	}
}
