package node

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolateEnrollment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldStore, oldEnroll, oldAgent, oldSave, oldWrite, oldNow := StorePath, EnrollmentStorePath, AgentConfigPath,
		saveAgentConfigForJoin, writeEnrollmentFile, enrollmentNow
	StorePath = filepath.Join(dir, "nodes.json")
	EnrollmentStorePath = filepath.Join(dir, "enrollments.json")
	AgentConfigPath = filepath.Join(dir, "agent.json")
	t.Cleanup(func() {
		StorePath, EnrollmentStorePath, AgentConfigPath = oldStore, oldEnroll, oldAgent
		saveAgentConfigForJoin = oldSave
		writeEnrollmentFile = oldWrite
		enrollmentNow = oldNow
	})
	return dir
}

func TestInitialEnrollmentExpiry(t *testing.T) {
	isolateEnrollment(t)
	now := time.Now().Truncate(time.Second)
	enrollmentNow = func() time.Time { return now }
	before, err := CreateEnrollment("before", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := parseEnrollment(before)
	credential, _ := GenerateCredential()
	now = now.Add(59 * time.Second)
	if _, err := completeEnrollmentRecord(first.NodeID, HashCredential(first.Token), credential); err != nil {
		t.Fatalf("unused code expired too early: %v", err)
	}
	after, err := CreateEnrollment("after", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := parseEnrollment(after)
	now = now.Add(time.Minute)
	if _, err := completeEnrollmentRecord(second.NodeID, HashCredential(second.Token), credential); err == nil {
		t.Fatal("unused code outlived its initial expiry")
	}
}

func TestProvisionedRecoveryWindowPersistsAndDoesNotRevokeAgent(t *testing.T) {
	isolateEnrollment(t)
	now := time.Now().Truncate(time.Second)
	enrollmentNow = func() time.Time { return now }
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	first, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	if err != nil {
		t.Fatal(err)
	}
	// Read the persisted record again, as a new Controller process would.
	stored, err := os.ReadFile(EnrollmentStorePath)
	if err != nil || strings.Contains(string(stored), credential) {
		t.Fatal("Controller enrollment store exposed the permanent credential")
	}
	var items []enrollmentRecord
	if err := json.Unmarshal(stored, &items); err != nil || len(items) != 1 || items[0].ProvisionedAt != now.Unix() {
		t.Fatalf("provisioning timestamp did not survive persisted reload: %+v, %v", items, err)
	}
	other, _ := GenerateCredential()
	now = now.Add(2 * time.Minute) // Initial validity ended; recovery has not.
	second, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	if err != nil || second != first || len(List()) != 1 || findCredential(e.NodeID) != credential {
		t.Fatalf("exact lost-response retry did not converge: %+v, %v", second, err)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), other); err == nil {
		t.Fatal("different permanent credential was accepted inside recovery window")
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential("wrong"), credential); err == nil {
		t.Fatal("wrong bootstrap secret was accepted")
	}
	now = time.Unix(items[0].ProvisionedAt, 0).Add(provisionedRecoveryWindow)
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("bootstrap recovery remained open at its deadline")
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), other); err == nil {
		t.Fatal("different permanent credential was accepted after recovery expiry")
	}
	if len(List()) != 1 || findCredential(e.NodeID) != credential {
		t.Fatal("bootstrap recovery expiry deleted or rotated the Managed Node")
	}
	w := httptest.NewRecorder()
	HandleEnrollmentHTTP(w, httptest.NewRequest(http.MethodGet,
		EnrollmentPath+"?id="+url.QueryEscape(e.NodeID), nil))
	if w.Code != http.StatusGone {
		t.Fatalf("expired provisioned enrollment returned HTTP %d", w.Code)
	}
	if _, err := CreateEnrollment("other", "http://controller.example:9876", time.Minute); err != nil {
		t.Fatal(err)
	}
	items, err = loadEnrollments()
	if err != nil || len(items) != 1 || items[0].NodeID == e.NodeID ||
		len(List()) != 1 || findCredential(e.NodeID) != credential {
		t.Fatalf("lazy enrollment cleanup changed the permanent Node: %+v, %v", items, err)
	}
}

func TestProvisioningTimestampWriteFailureDoesNotCommitNode(t *testing.T) {
	isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	writeEnrollmentFile = func(string, []byte) error { return errors.New("injected enrollment store failure") }
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("Managed Node committed without a durable recovery deadline")
	}
	if len(List()) != 0 {
		t.Fatal("Managed Node was saved despite enrollment timestamp failure")
	}
	writeEnrollmentFile = writePrivateFile
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatalf("retry after timestamp storage recovery failed: %v", err)
	}
}

func TestPermanentAgentCompletesEnrollmentAfterRecoveryExpiry(t *testing.T) {
	isolateEnrollment(t)
	now := time.Now().Truncate(time.Second)
	enrollmentNow = func() time.Time { return now }
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatal(err)
	}
	now = now.Add(provisionedRecoveryWindow + time.Second)
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("expired bootstrap recovery was accepted")
	}
	hub := NewHub()
	t.Cleanup(hub.Close)
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeHTTP))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunAgent(ctx, AgentConfig{NodeID: e.NodeID, Name: e.Name,
			ControllerURL: srv.URL, Credential: credential})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done; srv.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for !hub.IsOnline(e.NodeID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !hub.IsOnline(e.NodeID) {
		t.Fatal("permanent Agent credential failed after bootstrap recovery expiry")
	}
	var info Info
	if err := hub.Call(context.Background(), e.NodeID, OpHello, nil, &info); err != nil {
		t.Fatalf("Agent was online but not manageable: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	var items []enrollmentRecord
	var loadErr error
	for time.Now().Before(deadline) {
		items, loadErr = loadEnrollments()
		if loadErr == nil && len(items) == 1 && items[0].Used {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if loadErr != nil || len(items) != 1 || !items[0].Used {
		t.Fatalf("normal Agent authentication did not finalize enrollment: %+v, %v", items, loadErr)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("completed enrollment accepted bootstrap retry")
	}
	if len(List()) != 1 || findCredential(e.NodeID) != credential {
		t.Fatal("normal authentication changed the permanent Managed Node")
	}
}

func TestEnrollmentCommitFailureDoesNotConsumeCode(t *testing.T) {
	dir := isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	goodPath := StorePath
	StorePath = filepath.Join(dir, "blocked")
	if err := os.Mkdir(StorePath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("registry write failure was accepted")
	}
	StorePath = goodPath
	items, err := loadEnrollments()
	if err != nil || len(items) != 1 || items[0].Used {
		t.Fatalf("failed registry commit consumed the code: %+v, %v", items, err)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatalf("retry after storage recovery failed: %v", err)
	}
}

func TestEnrollmentLostResponseReloadKeepsOneCredential(t *testing.T) {
	isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	first, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	if err != nil {
		t.Fatal(err)
	}
	// No in-memory enrollment state is needed: the second call reloads both
	// durable files, just as it does after a controller process restart.
	second, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential)
	if err != nil || second != first {
		t.Fatalf("lost response did not converge: %+v, %v", second, err)
	}
	if same, err := AddManaged(e.Name, e.ControllerURL, e.NodeID, credential); err != nil || same.ID != e.NodeID {
		t.Fatalf("exact registry retry was not idempotent: %+v, %v", same, err)
	}
	if len(List()) != 1 || findCredential(e.NodeID) != credential {
		t.Fatal("retry duplicated or rotated the managed identity")
	}
	other, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), other); err == nil {
		t.Fatal("retry rotated the permanent credential")
	}
	if err := finishEnrollment(e.NodeID, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("completed code was reusable")
	}
}

func TestFailedCompletionWriteLeavesProvisionedIdentityRecoverable(t *testing.T) {
	isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatal(err)
	}
	writeEnrollmentFile = func(string, []byte) error { return errors.New("injected enrollment store failure") }
	if err := finishEnrollment(e.NodeID, credential); err == nil {
		t.Fatal("failed completion write was reported as successful")
	}
	writeEnrollmentFile = writePrivateFile
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatalf("failed completion write stranded the provisioned Node: %v", err)
	}
	if err := finishEnrollment(e.NodeID, credential); err != nil {
		t.Fatalf("completion did not recover: %v", err)
	}
}

func findCredential(id string) string {
	n, _ := findByID(id)
	return n.Credential
}

func TestEnrollmentExpiredAndInvalidCodes(t *testing.T) {
	isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential("wrong"), credential); err == nil {
		t.Fatal("invalid code was accepted")
	}
	items, _ := loadEnrollments()
	items[0].Expires = time.Now().Add(-time.Minute).Unix()
	if err := saveEnrollments(items); err != nil {
		t.Fatal(err)
	}
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err == nil {
		t.Fatal("expired unprovisioned code was accepted")
	}
}

func TestAgentConfigWriteFailureCanRetrySameEnrollment(t *testing.T) {
	isolateEnrollment(t)
	hub := NewHub()
	t.Cleanup(hub.Close)
	mux := http.NewServeMux()
	mux.HandleFunc(EnrollmentPath, HandleEnrollmentHTTP)
	mux.Handle(AgentPath, hub)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	code, err := CreateEnrollment("kharej", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	saveAgentConfigForJoin = func(AgentConfig) error { return errors.New("injected disk failure") }
	if _, err := JoinWithEnrollment(code, srv.Client()); err == nil || !strings.Contains(err.Error(), "retry node join") {
		t.Fatalf("Agent write failure was not recoverable: %v", err)
	}
	if len(List()) != 1 {
		t.Fatal("controller did not commit the provisioned identity")
	}
	first := findCredential(List()[0].ID)
	if info, err := os.Stat(AgentConfigPath + ".pending"); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("node lost its private retry intent: %v, %v", info, err)
	}
	saveAgentConfigForJoin = SaveAgentConfig
	cfg, err := JoinWithEnrollment(code, srv.Client())
	if err != nil || cfg.Credential != first || len(List()) != 1 {
		t.Fatalf("retry changed the identity: %+v, %v", cfg, err)
	}
	if info, err := os.Stat(AgentConfigPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("Agent config not private: %v, %v", info, err)
	}
	if _, err := os.Stat(AgentConfigPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed pending intent was not removed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunAgent(ctx, cfg); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !hub.IsOnline(cfg.NodeID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !hub.IsOnline(cfg.NodeID) {
		t.Fatal("recovered permanent credential could not authenticate the Agent")
	}
	e, _ := parseEnrollment(code)
	bad := AgentConfig{NodeID: cfg.NodeID, Name: cfg.Name, ControllerURL: srv.URL, Credential: e.Token}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer probeCancel()
	ws, _, err := dialController(probeCtx, bad)
	if err == nil {
		defer ws.Close()
		if _, err := clientSession(ws, bad); err == nil {
			t.Fatal("bootstrap token authenticated as a permanent Agent credential")
		}
	}
	cancel()
	<-done
}

func TestEnrollmentDoesNotContactControllerWhenRetryIntentCannotBeSaved(t *testing.T) {
	dir := isolateEnrollment(t)
	srv := httptest.NewServer(http.HandlerFunc(HandleEnrollmentHTTP))
	defer srv.Close()
	code, err := CreateEnrollment("kharej", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(block, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	AgentConfigPath = filepath.Join(block, "agent.json")
	if _, err := JoinWithEnrollment(code, srv.Client()); err == nil {
		t.Fatal("join proceeded without durable retry intent")
	}
	if len(List()) != 0 {
		t.Fatal("controller committed despite Node-side intent failure")
	}
}

func TestExpiredPendingIntentIsRemoved(t *testing.T) {
	isolateEnrollment(t)
	code, err := CreateEnrollment("kharej", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	credential, _ := GenerateCredential()
	b, _ := json.Marshal(pendingEnrollment{
		CodeHash: HashCredential(code), NodeID: e.NodeID, Credential: credential,
		Created: time.Now().Add(-25 * time.Hour).Unix(),
	})
	if err := writePrivateFile(AgentConfigPath+".pending", b); err != nil {
		t.Fatal(err)
	}
	if _, err := JoinWithEnrollment(code, nil); err == nil || !strings.Contains(err.Error(), "pending enrollment has expired") {
		t.Fatalf("stale pending intent was accepted: %v", err)
	}
	if _, err := os.Stat(AgentConfigPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale pending intent was not removed: %v", err)
	}
}

func TestRetryAfterAmbiguousAgentConfigWrite(t *testing.T) {
	isolateEnrollment(t)
	srv := httptest.NewServer(http.HandlerFunc(HandleEnrollmentHTTP))
	defer srv.Close()
	code, err := CreateEnrollment("kharej", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	saveAgentConfigForJoin = func(c AgentConfig) error {
		if err := SaveAgentConfig(c); err != nil {
			return err
		}
		return errors.New("injected error after rename")
	}
	if _, err := JoinWithEnrollment(code, srv.Client()); err == nil {
		t.Fatal("ambiguous config write was reported as success")
	}
	if len(List()) != 1 {
		t.Fatal("Controller did not provision the identity")
	}
	saveAgentConfigForJoin = SaveAgentConfig
	cfg, err := JoinWithEnrollment(code, srv.Client())
	if err != nil || cfg.Credential != findCredential(List()[0].ID) {
		t.Fatalf("ambiguous config write could not be recovered: %+v, %v", cfg, err)
	}
	if _, err := os.Stat(AgentConfigPath + ".pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the recovered intent was not removed")
	}
}

func TestControllerURLIsAnExplicitValidOrigin(t *testing.T) {
	for _, raw := range []string{
		"http://example.com", "ftp://example.com:7777", "http://evil.com:bad",
		"http://evil.com:7777/path", "http://evil.com:7777?x=1",
		"http://user@evil.com:7777", "http://evil.com:7777\r\nInjected: x",
		"http://bad..host:7777", "http://evil.com:0",
	} {
		if _, err := ValidateControllerURL(raw); err == nil {
			t.Errorf("accepted invalid Controller URL %q", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:7777", "https://controller.example:8443", "http://[::1]:12345"} {
		if got, err := ValidateControllerURL(raw); err != nil || got != raw {
			t.Errorf("rejected valid Controller URL %q: %q, %v", raw, got, err)
		}
	}
}

func TestEnrollmentReportsInvalidExpiredCompletedAndRevoked(t *testing.T) {
	isolateEnrollment(t)
	status := func(id string) int {
		w := httptest.NewRecorder()
		HandleEnrollmentHTTP(w, httptest.NewRequest(http.MethodGet,
			EnrollmentPath+"?id="+url.QueryEscape(id), nil))
		return w.Code
	}
	if got := status("missing"); got != http.StatusUnauthorized {
		t.Fatalf("invalid enrollment status = %d", got)
	}
	code, err := CreateEnrollment("expired", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	items, _ := loadEnrollments()
	items[0].Expires = time.Now().Add(-time.Minute).Unix()
	if err := saveEnrollments(items); err != nil {
		t.Fatal(err)
	}
	if got := status(e.NodeID); got != http.StatusGone {
		t.Fatalf("expired enrollment status = %d", got)
	}
	items[0].Expires = time.Now().Add(time.Minute).Unix()
	if err := saveEnrollments(items); err != nil {
		t.Fatal(err)
	}
	credential, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), credential); err != nil {
		t.Fatal(err)
	}
	if err := Revoke("expired"); err != nil {
		t.Fatal(err)
	}
	if got := status(e.NodeID); got != http.StatusForbidden {
		t.Fatalf("revoked enrollment status = %d", got)
	}
	// A different identity reaches Completed after the node acknowledges it.
	other, err := CreateEnrollment("completed", "http://controller.example:9876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := parseEnrollment(other)
	credential2, _ := GenerateCredential()
	if _, err := completeEnrollmentRecord(e2.NodeID, HashCredential(e2.Token), credential2); err != nil {
		t.Fatal(err)
	}
	if err := finishEnrollment(e2.NodeID, credential2); err != nil {
		t.Fatal(err)
	}
	if got := status(e2.NodeID); got != http.StatusConflict {
		t.Fatalf("completed enrollment status = %d", got)
	}
}

func TestConcurrentNodeJoinsCannotReplaceRetryIntent(t *testing.T) {
	isolateEnrollment(t)
	unlock, err := lockJoin()
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockJoin(); err == nil {
		second()
		unlock()
		t.Fatal("a second join acquired the same local intent")
	}
	unlock()
	third, err := lockJoin()
	if err != nil {
		t.Fatalf("crash-safe join lock was not reusable: %v", err)
	}
	third()
}
