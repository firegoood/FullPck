package cmd

import (
	"testing"

	"github.com/backpack/backpack/config"
)

func TestResourceLimitsBoundDangerousValues(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			ChannelSize: 1 << 20, MuxSession: 1000, MuxCon: 1000,
			MaxFrameSize: 1 << 20, MaxReceiveBuffer: 1 << 30, MaxStreamBuffer: 1 << 30,
			SO_RCVBUF: 1 << 30, SO_SNDBUF: 1 << 30,
		},
		Client: config.ClientConfig{
			Transport: config.WSMUX, ConnectionPool: 1000, MuxSession: 1000,
			MaxFrameSize: 1 << 20, MaxReceiveBuffer: 1 << 30, MaxStreamBuffer: 1 << 30,
			SO_RCVBUF: 1 << 30, SO_SNDBUF: 1 << 30,
		},
		Direct: config.DirectConfig{
			Sessions: 1000, MaxFrameSize: 1 << 30, MaxReceiveBuffer: 1 << 30, MaxStreamBuffer: 1 << 30,
		},
		L3: config.L3Config{SockBuf: 1 << 30, SpoofConfig: config.SpoofConfig{SpoofSockBuf: 1 << 30}},
	}
	if warnings := enforceResourceLimits(cfg); len(warnings) == 0 {
		t.Fatal("dangerous configuration was silently accepted")
	}
	if cfg.Server.ChannelSize != maxChannelSize || cfg.Server.MuxCon != maxMuxConcurrency || cfg.Client.MaxReceiveBuffer != maxMuxReceiveBuffer {
		t.Fatalf("reverse limits not applied: server=%+v client=%+v", cfg.Server, cfg.Client)
	}
	if cfg.Direct.MaxReceiveBuffer != maxMuxReceiveBuffer || cfg.Direct.MaxStreamBuffer != maxMuxReceiveBuffer {
		t.Fatalf("direct limits not applied: %+v", cfg.Direct)
	}
	if cfg.L3.SockBuf != maxSocketBuffer || cfg.L3.SpoofSockBuf != maxSocketBuffer {
		t.Fatalf("l3 socket limits not applied: %+v", cfg.L3)
	}
	if cfg.Client.ConnectionPool*cfg.Client.MaxReceiveBuffer > muxClientMemoryBudget {
		t.Fatalf("client SMUX budget exceeded: pool=%d receive=%d", cfg.Client.ConnectionPool, cfg.Client.MaxReceiveBuffer)
	}
}

func TestResourceLimitsPreserveEstablishedPresetValues(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{ChannelSize: 8192, MuxSession: 16, MuxCon: 16, MaxFrameSize: 65535, MaxReceiveBuffer: 32 << 20, MaxStreamBuffer: 16 << 20, SO_RCVBUF: 32 << 20, SO_SNDBUF: 32 << 20},
		Client: config.ClientConfig{Transport: config.WSMUX, ConnectionPool: 16, MuxSession: 16, MaxFrameSize: 65535, MaxReceiveBuffer: 32 << 20, MaxStreamBuffer: 16 << 20, SO_RCVBUF: 32 << 20, SO_SNDBUF: 32 << 20},
	}
	if warnings := enforceResourceLimits(cfg); len(warnings) != 0 {
		t.Fatalf("established preset values produced warnings: %v", warnings)
	}
	if cfg.Server.ChannelSize != 8192 || cfg.Client.ConnectionPool != 16 || cfg.Client.MaxReceiveBuffer != 32<<20 {
		t.Fatalf("safe preset values changed: %+v", cfg)
	}
}

func TestResourceLimitsPreserveThroughputPreset(t *testing.T) {
	cfg := &config.Config{Client: config.ClientConfig{
		Transport: config.KCP, ConnectionPool: 16,
		MaxFrameSize: 65535, MaxReceiveBuffer: 64 << 20, MaxStreamBuffer: 32 << 20,
	}}
	if warnings := enforceResourceLimits(cfg); len(warnings) != 0 {
		t.Fatalf("throughput preset changed: %v", warnings)
	}
	if cfg.Client.ConnectionPool != 16 {
		t.Fatalf("pool = %d, want 16", cfg.Client.ConnectionPool)
	}
}
