//go:build linux

package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTelegramIPCRelaysBoundedByteStreamAndClosesWithAgent(t *testing.T) {
	dir := t.TempDir()
	oldStore, oldSocket, oldHub, oldDial := StorePath, TelegramSocketPath, DefaultHub, dialTelegramEndpoint
	StorePath = filepath.Join(dir, "nodes.json")
	TelegramSocketPath = filepath.Join(dir, "control.sock")
	DefaultHub = NewHub()
	dialTelegramEndpoint = func(context.Context) (net.Conn, error) {
		local, remote := net.Pipe()
		go func() { defer remote.Close(); _, _ = io.Copy(remote, remote) }()
		return local, nil
	}
	t.Cleanup(func() {
		DefaultHub.Close()
		StorePath, TelegramSocketPath, DefaultHub, dialTelegramEndpoint = oldStore, oldSocket, oldHub, oldDial
	})
	cred, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddManaged("telegram-node", "http://controller:7777", "telegram-id", cred); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(DefaultHub.ServeHTTP))
	defer ts.Close()
	agentCtx, stopAgent := context.WithCancel(context.Background())
	defer stopAgent()
	done := make(chan struct{})
	go func() {
		RunAgent(agentCtx, AgentConfig{NodeID: "telegram-id", Name: "telegram-node",
			ControllerURL: ts.URL, Credential: cred})
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !DefaultHub.IsOnline("telegram-id") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !DefaultHub.IsOnline("telegram-id") {
		t.Fatal("Agent did not connect")
	}
	ipcCtx, stopIPC := context.WithCancel(context.Background())
	closeIPC, err := StartTelegramIPC(ipcCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopIPC(); closeIPC() }()
	if !TelegramRelayAvailable(context.Background()) {
		t.Fatal("connected Agent not offered to monitor")
	}
	conn, err := DialTelegramIPC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	want := bytes.Repeat([]byte("opaque TLS bytes"), 3000) // multiple stream frames
	readDone := make(chan []byte, 1)
	go func() { got := make([]byte, len(want)); _, _ = io.ReadFull(conn, got); readDone <- got }()
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-readDone:
		if !bytes.Equal(got, want) {
			t.Fatal("stream changed opaque bytes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stalled")
	}
	stopAgent()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent did not stop")
	}
	var one [1]byte
	if _, err := conn.Read(one[:]); err == nil {
		t.Fatal("stream survived Agent disconnect")
	}
}

func TestTelegramIPCTriesAnotherAgentWhenFirstCannotReachTelegram(t *testing.T) {
	dir := t.TempDir()
	oldStore, oldSocket, oldHub, oldDial := StorePath, TelegramSocketPath, DefaultHub, dialTelegramEndpoint
	StorePath = filepath.Join(dir, "nodes.json")
	TelegramSocketPath = filepath.Join(dir, "control.sock")
	DefaultHub = NewHub()
	var attempts atomic.Int32
	dialTelegramEndpoint = func(context.Context) (net.Conn, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("Telegram unreachable from this Agent")
		}
		local, remote := net.Pipe()
		go func() { defer remote.Close(); _, _ = io.Copy(remote, remote) }()
		return local, nil
	}
	t.Cleanup(func() {
		DefaultHub.Close()
		StorePath, TelegramSocketPath, DefaultHub, dialTelegramEndpoint = oldStore, oldSocket, oldHub, oldDial
	})
	server := httptest.NewServer(http.HandlerFunc(DefaultHub.ServeHTTP))
	defer server.Close()
	ctx, stopAgents := context.WithCancel(context.Background())
	defer stopAgents()
	for _, name := range []string{"a", "b"} {
		cred, err := GenerateCredential()
		if err != nil {
			t.Fatal(err)
		}
		id := "telegram-agent-" + name
		if _, err := AddManaged(name, server.URL, id, cred); err != nil {
			t.Fatal(err)
		}
		go RunAgent(ctx, AgentConfig{NodeID: id, Name: name, ControllerURL: server.URL, Credential: cred})
	}
	deadline := time.Now().Add(10 * time.Second)
	for !(DefaultHub.IsOnline("telegram-agent-a") && DefaultHub.IsOnline("telegram-agent-b")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !DefaultHub.IsOnline("telegram-agent-a") || !DefaultHub.IsOnline("telegram-agent-b") {
		t.Fatal("both Agents did not connect")
	}
	ipcCtx, stopIPC := context.WithCancel(context.Background())
	closeIPC, err := StartTelegramIPC(ipcCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopIPC(); closeIPC() }()
	conn, err := DialTelegramIPC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("opaque tls")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("opaque tls"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "opaque tls" {
		t.Fatalf("second Agent did not relay bytes: %q, %v", got, err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected two egress attempts, got %d", attempts.Load())
	}
}
