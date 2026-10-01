package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/firegoood/FullPck/internal/node"
)

type disconnectDuringHello struct{ *fakeRunner }

type unreadyAgent struct{ *fakeRunner }

func (r *unreadyAgent) Ready(context.Context, string) error { return node.ErrAgentOffline }

func TestManagedPairRequiresPingReadinessBeforeWritingLocalTunnel(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	f := newFake()
	f.up["TR"] = true
	s.nodes.Use(&unreadyAgent{f})
	w := httptest.NewRecorder()
	s.handleNodePair(w, httptest.NewRequest(http.MethodPost, "/api/node/pair",
		strings.NewReader(`{"node":"TR","kind":"reverse"}`)))
	if w.Code != http.StatusBadGateway || w.Header().Get(fixHeader) != "managed-node" {
		t.Fatalf("unready session reached local creation: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "nothing was created") || len(f.calls) != 0 {
		t.Fatal("offline preflight touched tunnel state")
	}
}

func (r *disconnectDuringHello) Call(name, op string, body, out any) error {
	err := r.fakeRunner.Call(name, op, body, out)
	if op == node.OpHello {
		r.up[name] = false
	}
	return err
}

func TestNodeListingDoesNotPublishOnlineAfterMetadataDisconnects(t *testing.T) {
	isolateFleet(t)
	cred, err := node.GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.AddManaged("TR", "http://controller:9876", "metadata-id", cred); err != nil {
		t.Fatal(err)
	}
	s := newFleetServer()
	f := newFake()
	f.up["TR"] = true
	f.answers[node.OpHello] = node.Info{Version: "test"}
	s.nodes.Use(&disconnectDuringHello{f})
	w := httptest.NewRecorder()
	s.handleNodes(w, httptest.NewRequest(http.MethodGet, "/api/nodes", nil))
	var state struct {
		Nodes []nodeView `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != 1 || state.Nodes[0].Online || state.Nodes[0].Why == "" {
		t.Fatalf("disconnected Agent was returned as Online: %s", w.Body)
	}
}
