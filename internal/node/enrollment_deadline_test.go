package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Each valid phase arrives within its own I/O budget, while two phases together
// take longer than that budget. A deadline set just once at upgrade incorrectly
// disconnects this progressing, authenticated enrollment.
func TestEnrollmentClientDeadlineFollowsProgress(t *testing.T) {
	isolateEnrollment(t)
	ready := make(chan enrollmentCode, 1)
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- delayedEnrollmentController(w, r, <-ready)
	}))
	defer srv.Close()
	code, err := CreateEnrollment("slow-controller", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	ready <- e
	cfg, joinErr := JoinWithEnrollment(code, nil)
	if peerErr := <-done; joinErr != nil || peerErr != nil {
		t.Fatalf("progressing enrollment failed: join=%v peer=%v", joinErr, peerErr)
	}
	if cfg.Credential == "" || findCredential(e.NodeID) != cfg.Credential {
		t.Fatal("progressing enrollment did not preserve the same durable credential")
	}
}

func delayedEnrollmentController(w http.ResponseWriter, r *http.Request, e enrollmentCode) error {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(time.Minute))
	hs, err := handshakeConfigWithPrologue(false, HashCredential(e.Token), "fullpack-node-enrollment-v1")
	if err != nil {
		return err
	}
	_, first, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	if _, _, _, err = hs.ReadMessage(nil, first); err != nil {
		return err
	}
	second, recv, send, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return err
	}
	time.Sleep(agentHandshakeTimeout/2 + 500*time.Millisecond)
	if err = ws.WriteMessage(websocket.BinaryMessage, second); err != nil {
		return err
	}
	_, ciphertext, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	plain, err := recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return err
	}
	var request enrollmentMessage
	if err = json.Unmarshal(plain, &request); err != nil {
		return err
	}
	if _, err = completeEnrollmentRecord(e.NodeID, HashCredential(e.Token), request.Credential); err != nil {
		return err
	}
	answer := marshalBody(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID})
	sealed, err := send.Encrypt(nil, nil, answer)
	if err != nil {
		return err
	}
	time.Sleep(agentHandshakeTimeout/2 + 500*time.Millisecond)
	if err = ws.WriteMessage(websocket.BinaryMessage, sealed); err != nil {
		return err
	}
	_, ciphertext, err = ws.ReadMessage()
	if err != nil {
		return err
	}
	plain, err = recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return err
	}
	var ack enrollmentMessage
	if json.Unmarshal(plain, &ack) != nil || !ack.Ack || ack.NodeID != e.NodeID {
		return fmt.Errorf("invalid enrollment acknowledgement")
	}
	if err = finishEnrollment(e.NodeID, request.Credential); err != nil {
		return err
	}
	sealed, err = send.Encrypt(nil, nil, marshalBody(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID, Ack: true}))
	if err != nil {
		return err
	}
	return ws.WriteMessage(websocket.BinaryMessage, sealed)
}

func TestEnrollmentControllerDeadlineFollowsProgress(t *testing.T) {
	isolateEnrollment(t)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		HandleEnrollmentHTTP(w, r)
	}))
	defer srv.Close()
	code, err := CreateEnrollment("slow-node", srv.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := parseEnrollment(code)
	wsURL := "ws" + srv.URL[len("http"):] + EnrollmentPath + "?id=" + e.NodeID
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ws.Close(); <-done }()
	_ = ws.SetReadDeadline(time.Now().Add(time.Minute))
	hs, err := handshakeConfigWithPrologue(true, HashCredential(e.Token), "fullpack-node-enrollment-v1")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _, err := hs.WriteMessage(nil, marshalBody(agentHello{Version: agentProtocolVersion, NodeID: e.NodeID}))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(agentHandshakeTimeout/2 + 500*time.Millisecond)
	if err = ws.WriteMessage(websocket.BinaryMessage, first); err != nil {
		t.Fatal(err)
	}
	_, second, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	_, send, recv, err := hs.ReadMessage(nil, second)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := GenerateCredential()
	sealed, err := send.Encrypt(nil, nil, marshalBody(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID, Credential: credential}))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(agentHandshakeTimeout/2 + 500*time.Millisecond)
	if err = ws.WriteMessage(websocket.BinaryMessage, sealed); err != nil {
		t.Fatal(err)
	}
	_, ciphertext, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("controller disconnected a progressing enrollment: %v", err)
	}
	plain, err := recv.Decrypt(nil, nil, ciphertext)
	var answer enrollmentMessage
	if err != nil || json.Unmarshal(plain, &answer) != nil || answer.Error != "" || answer.NodeID != e.NodeID {
		t.Fatal("controller did not confirm the durable credential", err)
	}
	sealed, err = send.Encrypt(nil, nil, marshalBody(enrollmentMessage{Version: EnrollmentVersion, NodeID: e.NodeID, Ack: true}))
	if err != nil {
		t.Fatal(err)
	}
	if err = ws.WriteMessage(websocket.BinaryMessage, sealed); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ws.ReadMessage(); err != nil {
		t.Fatal(err)
	}
}
