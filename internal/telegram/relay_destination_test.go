package telegram

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTelegramRelaysRejectOtherDestinations(t *testing.T) {
	clients := []*http.Client{
		tunnelledClient(12345, time.Second),
		agentPreferredClient(Config{ViaTunnel: AutoRelay}, time.Second),
	}
	for _, client := range clients {
		tr := client.Transport.(*http.Transport)
		if conn, err := tr.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
			conn.Close()
			t.Fatal("Telegram relay accepted a caller-chosen host")
		}
	}
}
