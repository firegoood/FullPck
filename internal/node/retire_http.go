package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// RetireHTTPController removes only the explicitly selected HTTP enrollment.
// A separately enrolled, pinned HTTPS entry on the same listener must already
// exist. No credential is migrated or changed, and the monitor reconciles the
// reduced configuration without restarting the remaining Agent workers.
// The returned backup path is also available if the final config write fails.
func RetireHTTPController(rawURL string) (string, error) {
	return retireHTTPController(rawURL, writePrivateFile)
}

func retireHTTPController(rawURL string, write func(string, []byte) error) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("retire-http requires an HTTP Controller origin without a path, credentials or query")
	}
	key, err := controllerIdentity(rawURL)
	if err != nil {
		return "", err
	}
	unlock, err := lockJoin()
	if err != nil {
		return "", err
	}
	defer unlock()
	agentConfigMu.Lock()
	defer agentConfigMu.Unlock()
	original, err := os.ReadFile(AgentConfigPath)
	if err != nil {
		return "", err
	}
	configs, err := decodeAgentConfigs(original)
	if err != nil {
		return "", err
	}
	removeIndex := -1
	hasReplacement := false
	for i, cfg := range configs {
		cfgKey, _ := controllerIdentity(cfg.ControllerURL)
		if cfgKey == key {
			removeIndex = i
		}
		if cfgKey == "https"+strings.TrimPrefix(key, "http") {
			host, _ := url.Parse(cfg.ControllerURL)
			_, pinErr := pinnedTLSConfig(cfg.TLSPinSHA256, host.Hostname())
			hasReplacement = pinErr == nil
		}
	}
	if removeIndex < 0 {
		return "", errors.New("selected HTTP Controller is not configured")
	}
	if !hasReplacement {
		return "", errors.New("a separately enrolled HTTPS Controller with a valid certificate pin on the same host and port is required")
	}
	data, err := removeControllerJSON(original, removeIndex)
	if err != nil {
		return "", err
	}
	// Reserve a unique name with 0600 permissions, then use the same durable
	// atomic writer as the credential file. Abort before removal on any failure.
	backup, err := os.CreateTemp(filepath.Dir(AgentConfigPath), filepath.Base(AgentConfigPath)+".retired-http-*.bak")
	if err != nil {
		return "", fmt.Errorf("could not create private Controller backup: %w", err)
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return "", err
	}
	if err := write(backupPath, original); err != nil {
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("could not save private Controller backup: %w", err)
	}
	if err := write(AgentConfigPath, data); err != nil {
		return backupPath, fmt.Errorf("could not save reduced Controller configuration; retain the private backup: %w", err)
	}
	return backupPath, nil
}

// Preserve additional JSON fields alongside the known credential fields. The
// configuration has already passed decodeAgentConfigs validation under the lock.
func removeControllerJSON(original []byte, index int) ([]byte, error) {
	var file map[string]json.RawMessage
	if err := json.Unmarshal(original, &file); err != nil {
		return nil, err
	}
	var additional []json.RawMessage
	if err := json.Unmarshal(file["additional_controllers"], &additional); err != nil {
		return nil, err
	}
	if index == 0 {
		var primary map[string]json.RawMessage
		if err := json.Unmarshal(additional[0], &primary); err != nil {
			return nil, err
		}
		for _, key := range []string{"node_id", "name", "controller_url", "credential", "tls_pin_sha256"} {
			delete(file, key)
		}
		for key, value := range primary {
			file[key] = value
		}
		additional = additional[1:]
	} else {
		additional = append(additional[:index-1:index-1], additional[index:]...)
	}
	delete(file, "additional_controllers")
	if len(additional) != 0 {
		file["additional_controllers"], _ = json.Marshal(additional)
	}
	return json.MarshalIndent(file, "", "  ")
}
