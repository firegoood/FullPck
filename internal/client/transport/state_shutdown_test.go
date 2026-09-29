package transport

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestClientStateStopWaitClosesAndJoinsWorkers(t *testing.T) {
	var state clientState
	ctx, cancel := context.WithCancel(context.Background())
	state.Reset(ctx, cancel, nil)
	a, b := net.Pipe()
	defer b.Close()
	if !state.SetConn(a) {
		t.Fatal("control connection rejected")
	}
	workerDone := make(chan struct{})
	if !state.Go(func() {
		defer close(workerDone)
		buf := make([]byte, 1)
		a.Read(buf)
	}) {
		t.Fatal("worker rejected")
	}
	done := make(chan struct{})
	go func() { state.StopAndWait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join blocked worker")
	}
	select {
	case <-workerDone:
	default:
		t.Fatal("worker remained active")
	}
	if state.Go(func() {}) {
		t.Fatal("new worker accepted after stop")
	}
	c, d := net.Pipe()
	defer d.Close()
	if _, ok := state.Track(c); ok {
		t.Fatal("new connection accepted after stop")
	}
}
