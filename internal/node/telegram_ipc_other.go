//go:build !linux

package node

import (
	"context"
	"net"
)

var TelegramSocketPath = ""

func StartTelegramIPC(context.Context) (func(), error)  { return func() {}, nil }
func DialTelegramIPC(context.Context) (net.Conn, error) { return nil, ErrAgentOffline }
func TelegramRelayAvailable(context.Context) bool       { return false }
