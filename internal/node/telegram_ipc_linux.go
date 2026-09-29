//go:build linux

package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// TelegramSocketPath is a root-only Unix socket shared by the WebUI controller
// and the separate monitor process. Tests may point it at a temporary path.
var TelegramSocketPath = "/run/fullpack/control.sock"

// StartTelegramIPC owns only a local Unix socket. Remote Agent sessions remain
// on the existing WebUI listener; this adds no public management port.
func StartTelegramIPC(ctx context.Context) (func(), error) {
	path := TelegramSocketPath
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control socket path is not a socket: %s", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, errors.New("another controller owns the control socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	done := make(chan struct{})
	stop := func() { _ = ln.Close(); <-done }
	go func() {
		defer close(done)
		defer os.Remove(path)
		limit := make(chan struct{}, 32)
		go func() { <-ctx.Done(); _ = ln.Close() }()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case limit <- struct{}{}:
				go func() { defer func() { <-limit }(); serveTelegramLocal(ctx, conn) }()
			default:
				_ = conn.Close()
			}
		}
	}()
	return stop, nil
}

func serveTelegramLocal(ctx context.Context, conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var command [1]byte
	if _, err := io.ReadFull(conn, command[:]); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if command[0] == 'S' {
		_, err := DefaultHub.telegramSession()
		if err == nil {
			_, _ = conn.Write([]byte{1})
		} else {
			_, _ = conn.Write([]byte{0})
		}
		_ = conn.Close()
		return
	}
	if command[0] != 'T' {
		_ = conn.Close()
		return
	}
	opened := false
	for _, s := range DefaultHub.telegramSessions() {
		if err := s.openTelegram(ctx, conn); err == nil {
			opened = true
			break
		}
	}
	if !opened {
		_ = conn.Close()
		return
	}
	// The byte is a local success acknowledgement, never part of TLS to Telegram.
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte{1}); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetWriteDeadline(time.Time{})
	// The stream pumps now own conn until close or Agent disconnect.
}

func dialControl(ctx context.Context, command byte) (net.Conn, byte, error) {
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", TelegramSocketPath)
	if err != nil {
		return nil, 0, err
	}
	_ = conn.SetDeadline(time.Now().Add(18 * time.Second))
	if _, err := conn.Write([]byte{command}); err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, answer[0], nil
}

// DialTelegramIPC returns a local connection carrying opaque TLS bytes to the
// fixed Telegram API endpoint through an authenticated Agent stream.
func DialTelegramIPC(ctx context.Context) (net.Conn, error) {
	conn, answer, err := dialControl(ctx, 'T')
	if err != nil {
		return nil, err
	}
	if answer != 1 {
		_ = conn.Close()
		return nil, ErrAgentOffline
	}
	return conn, nil
}

func TelegramRelayAvailable(ctx context.Context) bool {
	conn, answer, err := dialControl(ctx, 'S')
	if conn != nil {
		_ = conn.Close()
	}
	return err == nil && answer == 1
}
