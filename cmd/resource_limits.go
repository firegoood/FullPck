package cmd

import (
	"fmt"

	"github.com/backpack/backpack/config"
)

// These ceilings bound values which directly size channels, socket buffers,
// goroutine bursts, or SMUX windows. They are deliberately high enough to
// preserve the existing performance presets (including Direct/L3) while
// preventing a typo from turning one tunnel into an unbounded allocation.
const (
	maxChannelSize        = 8 * 1024
	maxConnectionPool     = 64
	maxMuxSessions        = 64
	maxMuxConcurrency     = 256
	maxMuxFrameSize       = 65_535
	maxMuxReceiveBuffer   = 64 << 20
	maxSocketBuffer       = 32 << 20
	muxClientMemoryBudget = 1024 << 20
)

// enforceResourceLimits normalises dangerous resource inputs after ordinary
// defaults have been applied. It returns warnings so callers can make every
// correction visible in the tunnel journal. Valid existing values are kept.
// Direct and L3 are included here because they use the same SMUX/socket
// settings but live outside [server]/[client].
func enforceResourceLimits(cfg *config.Config) []string {
	var warnings []string
	capValue := func(name string, value *int, maximum int) {
		if *value > maximum {
			warnings = append(warnings, fmt.Sprintf("%s=%d exceeds the safe limit; using %d", name, *value, maximum))
			*value = maximum
		}
	}

	capValue("server.channel_size", &cfg.Server.ChannelSize, maxChannelSize)
	capValue("client.connection_pool", &cfg.Client.ConnectionPool, maxConnectionPool)
	capValue("server.mux_session", &cfg.Server.MuxSession, maxMuxSessions)
	capValue("client.mux_session", &cfg.Client.MuxSession, maxMuxSessions)
	capValue("server.mux_con", &cfg.Server.MuxCon, maxMuxConcurrency)
	capValue("server.mux_framesize", &cfg.Server.MaxFrameSize, maxMuxFrameSize)
	capValue("client.mux_framesize", &cfg.Client.MaxFrameSize, maxMuxFrameSize)
	capValue("server.mux_recievebuffer", &cfg.Server.MaxReceiveBuffer, maxMuxReceiveBuffer)
	capValue("client.mux_recievebuffer", &cfg.Client.MaxReceiveBuffer, maxMuxReceiveBuffer)
	capValue("server.mux_streambuffer", &cfg.Server.MaxStreamBuffer, maxMuxReceiveBuffer)
	capValue("client.mux_streambuffer", &cfg.Client.MaxStreamBuffer, maxMuxReceiveBuffer)
	capValue("server.so_rcvbuf", &cfg.Server.SO_RCVBUF, maxSocketBuffer)
	capValue("server.so_sndbuf", &cfg.Server.SO_SNDBUF, maxSocketBuffer)
	capValue("client.so_rcvbuf", &cfg.Client.SO_RCVBUF, maxSocketBuffer)
	capValue("client.so_sndbuf", &cfg.Client.SO_SNDBUF, maxSocketBuffer)

	if cfg.Server.MaxStreamBuffer > cfg.Server.MaxReceiveBuffer && cfg.Server.MaxReceiveBuffer > 0 {
		warnings = append(warnings, "server.mux_streambuffer exceeds mux_recievebuffer; using the receive-buffer limit")
		cfg.Server.MaxStreamBuffer = cfg.Server.MaxReceiveBuffer
	}
	if cfg.Client.MaxStreamBuffer > cfg.Client.MaxReceiveBuffer && cfg.Client.MaxReceiveBuffer > 0 {
		warnings = append(warnings, "client.mux_streambuffer exceeds mux_recievebuffer; using the receive-buffer limit")
		cfg.Client.MaxStreamBuffer = cfg.Client.MaxReceiveBuffer
	}

	// A large receive window is useful, but multiplying it by an arbitrary
	// connection pool is the dangerous part. This still honours an explicit
	// pool up to the established budget and lets the runtime pool guard handle
	// automatic growth separately.
	if clientUsesSMUX(cfg.Client.Transport) && cfg.Client.MaxReceiveBuffer > 0 {
		maxPoolForBudget := muxClientMemoryBudget / cfg.Client.MaxReceiveBuffer
		if maxPoolForBudget < 1 {
			maxPoolForBudget = 1
		}
		if cfg.Client.ConnectionPool > maxPoolForBudget {
			warnings = append(warnings, fmt.Sprintf(
				"client connection_pool × mux_recievebuffer exceeds %d MiB; reducing connection_pool from %d to %d",
				muxClientMemoryBudget>>20, cfg.Client.ConnectionPool, maxPoolForBudget))
			cfg.Client.ConnectionPool = maxPoolForBudget
		}
	}

	// Direct's edge owns the session fan-out. Keep the same aggregate budget
	// there, without changing the preset values that are already documented.
	capValue("direct.sessions", &cfg.Direct.Sessions, maxMuxSessions)
	capValue("direct.mux_framesize", &cfg.Direct.MaxFrameSize, maxMuxFrameSize)
	capValue("direct.mux_recievebuffer", &cfg.Direct.MaxReceiveBuffer, maxMuxReceiveBuffer)
	capValue("direct.mux_streambuffer", &cfg.Direct.MaxStreamBuffer, maxMuxReceiveBuffer)
	if cfg.Direct.MaxStreamBuffer > cfg.Direct.MaxReceiveBuffer && cfg.Direct.MaxReceiveBuffer > 0 {
		warnings = append(warnings, "direct.mux_streambuffer exceeds mux_recievebuffer; using the receive-buffer limit")
		cfg.Direct.MaxStreamBuffer = cfg.Direct.MaxReceiveBuffer
	}
	if cfg.Direct.Sessions > 0 && cfg.Direct.MaxReceiveBuffer > 0 {
		maxSessionsForBudget := muxClientMemoryBudget / cfg.Direct.MaxReceiveBuffer
		if maxSessionsForBudget < 1 {
			maxSessionsForBudget = 1
		}
		if cfg.Direct.Sessions > maxSessionsForBudget {
			warnings = append(warnings, fmt.Sprintf(
				"direct sessions × mux_recievebuffer exceeds %d MiB; reducing sessions from %d to %d",
				muxClientMemoryBudget>>20, cfg.Direct.Sessions, maxSessionsForBudget))
			cfg.Direct.Sessions = maxSessionsForBudget
		}
	}

	capValue("l3.sockbuf", &cfg.L3.SockBuf, maxSocketBuffer)
	capValue("l3.spoof_sockbuf", &cfg.L3.SpoofSockBuf, maxSocketBuffer)
	return warnings
}

func clientUsesSMUX(transport config.TransportType) bool {
	switch transport {
	case config.TCPMUX, config.KCP, config.XDI, config.PCK, config.WSMUX, config.WSSMUX, config.SPOOF:
		return true
	default:
		return false
	}
}
