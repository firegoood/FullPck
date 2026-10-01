package node

import (
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestEnrollmentClientReportsPhaseAndRetainsPrivateRetry(t *testing.T) {
	for _, phase := range []string{"receiving Noise reply", "receiving provisioning response"} {
		t.Run(phase, func(t *testing.T) {
			isolateEnrollment(t)
			const peerSecret = "close-reason-with-a-password-must-not-print"
			ready := make(chan enrollmentCode, 1)
			done := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				done <- failEnrollmentPeer(w, r, <-ready, phase, peerSecret)
			}))
			defer srv.Close()
			code, err := CreateEnrollment("interrupted", srv.URL, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := parseEnrollment(code)
			ready <- e
			_, err = JoinWithEnrollment(code, nil)
			if peerErr := <-done; peerErr != nil {
				t.Fatal(peerErr)
			}
			if err == nil || !strings.Contains(err.Error(), "enrollment "+phase+" failed") {
				t.Fatalf("join did not identify the failed phase: %v", err)
			}
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseGoingAway {
				t.Fatal("phase annotation lost the original error type")
			}
			for _, secret := range []string{peerSecret, code, e.Token} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("phase error exposed a secret or peer-supplied close reason")
				}
			}
			if len(List()) != 0 || HasAgentConfig() {
				t.Fatal("interrupted enrollment committed a permanent identity")
			}
			info, statErr := os.Stat(AgentConfigPath + ".pending")
			if statErr != nil || info.Mode().Perm() != 0600 {
				t.Fatal("interrupted enrollment lost its private recovery intent", statErr)
			}
		})
	}
}

func failEnrollmentPeer(w http.ResponseWriter, r *http.Request, e enrollmentCode, phase, text string) error {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	_, first, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	if phase == "receiving provisioning response" {
		hs, hsErr := handshakeConfigWithPrologue(false, HashCredential(e.Token), "fullpack-node-enrollment-v1")
		if hsErr != nil {
			return hsErr
		}
		if _, _, _, err = hs.ReadMessage(nil, first); err != nil {
			return err
		}
		second, _, _, hsErr := hs.WriteMessage(nil, nil)
		if hsErr != nil {
			return hsErr
		}
		if err = ws.WriteMessage(websocket.BinaryMessage, second); err != nil {
			return err
		}
		if _, _, err = ws.ReadMessage(); err != nil {
			return err
		}
	}
	return ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, text), time.Now().Add(time.Second))
}

func TestEnrollmentControllerLogsPhaseWithoutPeerMaterial(t *testing.T) {
	isolateEnrollment(t)
	var output agentLogBuffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		HandleEnrollmentHTTP(w, r)
	}))
	defer srv.Close()
	code, err := CreateEnrollment("bad-peer", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):]+EnrollmentPath+"?id="+e.NodeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	const peerSecret = "invalid-handshake-with-a-password-must-not-log"
	if err = ws.WriteMessage(websocket.BinaryMessage, []byte(peerSecret)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("invalid enrollment handler did not finish")
	}
	text := output.text()
	if !strings.Contains(text, "authenticating Noise hello failed") {
		t.Fatal("Controller did not identify the enrollment failure phase")
	}
	for _, secret := range []string{peerSecret, code, e.Token, e.NodeID, HashCredential(e.Token)} {
		if strings.Contains(text, secret) {
			t.Fatal("Controller enrollment log exposed private or peer-supplied material")
		}
	}
	if len(List()) != 0 {
		t.Fatal("an invalid Noise handshake provisioned a Node")
	}
}
