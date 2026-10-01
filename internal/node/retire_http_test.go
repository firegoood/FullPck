package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func retirementConfig(id, controllerURL string) AgentConfig {
	credential, _ := GenerateCredential()
	cfg := AgentConfig{NodeID: id, Name: id, ControllerURL: controllerURL, Credential: credential}
	if strings.HasPrefix(controllerURL, "https://") {
		pin := sha256.Sum256([]byte(id))
		cfg.TLSPinSHA256 = base64.RawURLEncoding.EncodeToString(pin[:])
	}
	return cfg
}

func TestRetireHTTPPreservesCredentialsAndPrivateBackup(t *testing.T) {
	for _, primaryHTTP := range []bool{true, false} {
		t.Run(map[bool]string{true: "HTTP-primary", false: "HTTPS-primary"}[primaryHTTP], func(t *testing.T) {
			isolateEnrollment(t)
			old := retirementConfig("old-http", "http://controller.example:9443/browser/")
			replacement := retirementConfig("new-https", "https://controller.example:9443/new-prefix/")
			other := retirementConfig("other-iran", "https://other.example:8443")
			configs := []AgentConfig{old, replacement, other}
			if !primaryHTTP {
				configs[0], configs[1] = replacement, old
			}
			for _, cfg := range configs {
				if err := SaveAgentConfig(cfg); err != nil {
					t.Fatal(err)
				}
			}
			// A future or operator-owned field must not disappear on retirement.
			raw, _ := os.ReadFile(AgentConfigPath)
			var extended map[string]json.RawMessage
			_ = json.Unmarshal(raw, &extended)
			extended["operator_metadata"] = json.RawMessage(`{"retained":true}`)
			raw, _ = json.Marshal(extended)
			if err := writePrivateFile(AgentConfigPath, raw); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(AgentConfigPath)
			backup, err := RetireHTTPController("http://CONTROLLER.example:9443")
			if err != nil {
				t.Fatal(err)
			}
			got, err := LoadAgentConfigs()
			if err != nil || !reflect.DeepEqual(got, []AgentConfig{replacement, other}) {
				t.Fatalf("remaining identities, credentials or pins changed: %v", err)
			}
			raw, _ = os.ReadFile(AgentConfigPath)
			var after map[string]json.RawMessage
			_ = json.Unmarshal(raw, &after)
			var metadata map[string]bool
			if json.Unmarshal(after["operator_metadata"], &metadata) != nil || !metadata["retained"] {
				t.Fatal("retirement removed operator-owned configuration fields")
			}
			backupBytes, err := os.ReadFile(backup)
			if err != nil || !bytes.Equal(backupBytes, original) || filepath.Dir(backup) != filepath.Dir(AgentConfigPath) {
				t.Fatal("backup did not preserve the exact original private configuration")
			}
			for _, path := range []string{backup, AgentConfigPath} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("Controller credentials or backup are not private")
				}
			}
		})
	}
}

func TestRetireHTTPRejectsUnsafeRemoval(t *testing.T) {
	tests := []struct {
		name, target, replacement string
		badPin                    bool
	}{
		{"no-replacement", "http://controller.example:9443", "", false},
		{"wrong-host", "http://controller.example:9443", "https://other.example:9443", false},
		{"wrong-port", "http://controller.example:9443", "https://controller.example:8443", false},
		{"bad-pin", "http://controller.example:9443", "https://controller.example:9443", true},
		{"cannot-remove-HTTPS", "https://controller.example:9443", "https://controller.example:9443", false},
		{"missing-target", "http://missing.example:9443", "https://controller.example:9443", false},
		{"credentials-in-target", "http://user:password@controller.example:9443", "https://controller.example:9443", false},
		{"path-in-target", "http://controller.example:9443/browser", "https://controller.example:9443", false},
		{"query-in-target", "http://controller.example:9443/?token=secret", "https://controller.example:9443", false},
		{"empty-query-in-target", "http://controller.example:9443/?", "https://controller.example:9443", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateEnrollment(t)
			if err := SaveAgentConfig(retirementConfig("old", "http://controller.example:9443")); err != nil {
				t.Fatal(err)
			}
			if tt.replacement != "" {
				cfg := retirementConfig("replacement", tt.replacement)
				if tt.badPin {
					cfg.TLSPinSHA256 = "not-a-pin"
				}
				if err := SaveAgentConfig(cfg); err != nil {
					t.Fatal(err)
				}
			}
			original, _ := os.ReadFile(AgentConfigPath)
			backup, err := RetireHTTPController(tt.target)
			if err == nil || backup != "" {
				t.Fatal("unsafe retirement succeeded or wrote a backup")
			}
			if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
				t.Fatal("rejected URL leaked secrets into diagnostics")
			}
			after, _ := os.ReadFile(AgentConfigPath)
			backups, _ := filepath.Glob(AgentConfigPath + ".retired-http-*.bak")
			if !bytes.Equal(after, original) || len(backups) != 0 {
				t.Fatal("rejected retirement changed Controller state")
			}
		})
	}
}

func TestRetireHTTPWriteFailuresKeepOriginalAndReportDurableBackup(t *testing.T) {
	for _, failBackup := range []bool{true, false} {
		t.Run(map[bool]string{true: "backup-write", false: "config-write"}[failBackup], func(t *testing.T) {
			isolateEnrollment(t)
			for _, rawURL := range []string{"http://controller.example:9443", "https://controller.example:9443"} {
				if err := SaveAgentConfig(retirementConfig(rawURL, rawURL)); err != nil {
					t.Fatal(err)
				}
			}
			original, _ := os.ReadFile(AgentConfigPath)
			injected := errors.New("injected write failure")
			backup, err := retireHTTPController("http://controller.example:9443", func(path string, data []byte) error {
				if (path != AgentConfigPath) == failBackup {
					return injected
				}
				return writePrivateFile(path, data)
			})
			if !errors.Is(err, injected) {
				t.Fatal("retirement lost a persistence error")
			}
			after, _ := os.ReadFile(AgentConfigPath)
			if !bytes.Equal(after, original) {
				t.Fatal("failed retirement changed the original configuration")
			}
			if failBackup {
				backups, _ := filepath.Glob(AgentConfigPath + ".retired-http-*.bak")
				if backup != "" || len(backups) != 0 {
					t.Fatal("failed backup was reported as durable or left behind")
				}
			} else {
				backupBytes, readErr := os.ReadFile(backup)
				if readErr != nil || !bytes.Equal(backupBytes, original) {
					t.Fatal("config write failure did not report the saved backup")
				}
			}
		})
	}
}

func TestRetireHTTPDoesNotRestartHealthyPinnedAgent(t *testing.T) {
	isolateEnrollment(t)
	hub := NewHub()
	mux := http.NewServeMux()
	mux.Handle(AgentPath, hub)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	defer hub.Close()
	pin := sha256.Sum256(srv.Certificate().Raw)
	current := retirementConfig("healthy-agent", srv.URL)
	current.TLSPinSHA256 = base64.RawURLEncoding.EncodeToString(pin[:])
	if _, err := AddManaged(current.Name, current.ControllerURL, current.NodeID, current.Credential); err != nil {
		t.Fatal(err)
	}
	oldURL := "http" + strings.TrimPrefix(srv.URL, "https")
	old := retirementConfig("obsolete-agent", oldURL)
	for _, cfg := range []AgentConfig{old, current} {
		if err := SaveAgentConfig(cfg); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, oldStopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runConfiguredAgents(ctx, func(ctx context.Context, cfg AgentConfig) {
			if cfg == old {
				<-ctx.Done()
				close(oldStopped)
				return
			}
			RunAgent(ctx, cfg)
		}, 10*time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Agent supervisor did not stop")
		}
	})
	waitAgentCondition(t, func() bool { return hub.IsOnline(current.NodeID) })
	before, _ := hub.SessionFor(current.NodeID)
	if _, err := RetireHTTPController(oldURL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldStopped:
	case <-time.After(time.Second):
		t.Fatal("obsolete HTTP worker was not retired")
	}
	after, online := hub.SessionFor(current.NodeID)
	if !online || after != before {
		t.Fatal("retirement restarted the healthy HTTPS session")
	}
	if err := hub.Call(ctx, current.NodeID, OpPing, nil, nil); err != nil {
		t.Fatal("remaining authenticated HTTPS session stopped working", err)
	}
}
