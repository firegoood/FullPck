// Package node implements the managed Node Agent and Controller protocol.
//
// A Node enrolls with a short-lived, single-use code and stores a distinct
// permanent credential. The Agent then dials outward to the Controller's
// existing WebUI origin and maintains an authenticated, encrypted WebSocket
// session. The Controller sends only typed operations listed in protocol.go;
// no remote shell or arbitrary command execution is available.
//
// The Agent runs in the existing monitor service. It adds no inbound Node
// management listener and uses no fixed management port. Telegram can send
// opaque TLS bytes through a restricted stream to api.telegram.org:443; that
// port is a remote outbound destination, never a local listener.
package node
