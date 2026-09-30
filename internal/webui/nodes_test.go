package webui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firegoood/FullPck/internal/manage"
	"github.com/firegoood/FullPck/internal/node"
)

// The fleet endpoints.
//
// What is worth holding still here is not that they return JSON. It is that a
// panel with the feature turned off cannot be talked into acting on a node, and
// that the one thing this feature is for — an edit reaching both ends — is
// reported honestly when only one of them took it.

// isolateFleet points the fleet's two state files at a temp directory, so a
// test never reads or writes the machine it runs on.
func isolateFleet(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	oldStore, oldPairs, oldEnrollment, oldAgent := node.StorePath, manage.NodePairPath, node.EnrollmentStorePath, node.AgentConfigPath
	node.StorePath = filepath.Join(dir, "nodes.json")
	manage.NodePairPath = filepath.Join(dir, "node-pairs.json")
	node.EnrollmentStorePath = filepath.Join(dir, "node-enrollment.json")
	node.AgentConfigPath = filepath.Join(dir, "node-agent.json")
	t.Cleanup(func() {
		node.StorePath, manage.NodePairPath = oldStore, oldPairs
		node.EnrollmentStorePath, node.AgentConfigPath = oldEnrollment, oldAgent
	})
}

func newFleetServer() *server { return newServer() }

// fakeRunner stands in for a fleet of real machines.
//
// The transport is an authenticated outbound Agent session, so a test that wanted a live node would need a
// second computer. What the panel's own behaviour depends on is narrower than
// that: whether a server answers, and what it says — so that is what is
// substituted, and every path through the handlers is exercised for real.
type fakeRunner struct {
	up      map[string]bool
	answers map[string]any   // op -> what it returns
	fail    map[string]error // op -> what it refuses with
	calls   []string
	forgot  []string
}

func newFake() *fakeRunner {
	return &fakeRunner{up: map[string]bool{}, answers: map[string]any{}, fail: map[string]error{}}
}

func (f *fakeRunner) Call(name, op string, body, out any) error {
	f.calls = append(f.calls, name+":"+op)
	if !f.up[name] {
		return node.ErrOffline{Name: name, Why: "no route to host"}
	}
	if err := f.fail[op]; err != nil {
		return err
	}
	if v, ok := f.answers[op]; ok && out != nil {
		b, _ := json.Marshal(v)
		return json.Unmarshal(b, out)
	}
	return nil
}

func (f *fakeRunner) IsOnline(name string) bool { return f.up[name] }

func (f *fakeRunner) Reachable(name string) (bool, string) {
	if f.up[name] {
		return true, ""
	}
	return false, "no route to host"
}

func (f *fakeRunner) Forget(name string) { f.forgot = append(f.forgot, name) }

// withFleet puts a stand-in behind the panel's fleet.
func withFleet(s *server, f *fakeRunner) { s.nodes.Use(f) }

func post(t *testing.T, s *server, form string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/nodes", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Signed in, as the operator on the fleet page is: adding a server and
	// changing its credentials are admin actions (nodeActionScope).
	r.RemoteAddr = "192.0.2.10:1"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.sessions.create("192.0.2.10")})
	w := httptest.NewRecorder()
	s.handleNodes(w, r)
	return w
}

// A panel that manages nothing says so, and cannot be talked into acting on a
// server that is not there.
//
// What used to be here also checked that the feature was off until switched on.
// There is no switch: it guarded listeners the panel had to open, and the panel
// dials out now — an empty fleet is already the off state.
func TestAnEmptyFleetIsEmptyAndActsOnNothing(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)

	r := httptest.NewRequest("GET", "/api/nodes", nil)
	w := httptest.NewRecorder()
	s.handleNodes(w, r)
	var state struct {
		Nodes []any `json:"nodes"`
	}
	json.Unmarshal(w.Body.Bytes(), &state)
	if len(state.Nodes) != 0 {
		t.Errorf("a panel that has never used the feature reports %d servers", len(state.Nodes))
	}

	// And nothing can be pushed to a server that does not exist.
	body, _ := json.Marshal(map[string]any{"node": "kharej", "kind": "reverse"})
	rq := httptest.NewRequest("POST", "/api/node/pair", strings.NewReader(string(body)))
	wr := httptest.NewRecorder()
	s.handleNodePair(wr, rq)
	if wr.Code == http.StatusOK {
		t.Error("a tunnel was paired with a server that is not in the fleet")
	}
}

// Enrollment publishes a short-lived code. The node generates its own permanent
// credential, joins through the existing WebUI listener, and only then enters
// the fleet.
func TestAddingAServerUsesOneTimeAgentEnrollment(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc(node.EnrollmentPath, node.HandleEnrollmentHTTP)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/nodes",
		strings.NewReader("action=add&name=kharej&controller_url="+url.QueryEscape(srv.URL)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enrollment: %s", resp.Status)
	}
	var issued struct {
		EnrollmentCode string `json:"enrollmentCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.EnrollmentCode, node.EnrollmentVersion+":") {
		t.Fatal("the server did not issue a versioned code")
	}
	if len(node.List()) != 0 {
		t.Fatal("pending enrollment appeared online as a Node")
	}

	cfg, err := node.JoinWithEnrollment(issued.EnrollmentCode, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	list := node.List()
	if len(list) != 1 || list[0].ID != cfg.NodeID || list[0].Credential != "" {
		t.Fatalf("fleet did not keep only the enrolled identity: %+v", list)
	}
	if _, err := node.JoinWithEnrollment(issued.EnrollmentCode, srv.Client()); err == nil {
		t.Fatal("one-time code enrolled another Agent")
	}
	front := getNodes(t, s).Body.String()
	if strings.Contains(front, cfg.Credential) || strings.Contains(front, issued.EnrollmentCode) {
		t.Fatal("the browser fleet state exposed enrollment or permanent secrets")
	}
	if w := post(t, s, "action=add&name=kha%2Frej"); w.Code == http.StatusOK {
		t.Fatal("invalid Node name was accepted")
	}
	if w := post(t, s, "action=add&name=kharej"); w.Code == http.StatusOK {
		t.Fatal("an already enrolled name was reused")
	}
}

func TestEnrollmentEndpointRejectsUntrustedHostAndMalformedControllerURL(t *testing.T) {
	isolateFleet(t)
	useConfigFile(t, Config{})
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)
	for _, form := range []string{
		"action=add&name=kharej",
		"action=add&name=kharej&controller_url=http%3A%2F%2Fevil.example%3A8443%2Fother",
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(form))
		r.Host = "injected.example:9999"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.handleNodes(w, r)
		if w.Code != http.StatusBadRequest || len(node.List()) != 0 {
			t.Fatalf("untrusted Controller endpoint accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

// Old SSH fields must never reappear as a way to enroll a managed Node.
func TestLegacySSHRejectedAndRevocationRemovesAgentAuthority(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)
	for _, form := range []string{
		"action=add&name=kharej&host=203.0.113.9&user=root&password=secret",
		"action=credentials&name=kharej&password=secret",
	} {
		if w := post(t, s, form); w.Code != http.StatusGone {
			t.Fatalf("legacy credentials accepted: %d %s", w.Code, w.Body.String())
		}
	}
	cred, err := node.GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.AddManaged("kharej", "http://controller:7777", "node-1", cred); err != nil {
		t.Fatal(err)
	}
	if w := post(t, s, "action=revoke&name=kharej"); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	n, ok := node.Find("kharej")
	if !ok || !n.Revoked || n.Credential != "" {
		t.Fatalf("revoked node state: %+v", n)
	}
	front := getNodes(t, s).Body.String()
	if strings.Contains(front, cred) || !strings.Contains(front, "\"revoked\":true") {
		t.Fatal("the browser state leaked a credential or lost revocation")
	}
}

// The point of the feature: an edit that cannot reach the other end says so,
// names the server, and does not report plain success.
func TestAnEditThatCannotReachTheNodeSaysSo(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()

	if err := manage.NoteNodePair("fr-relay", "kharej-de", "fr-relay-kharej"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	r := httptest.NewRequest("POST", "/api/tunnel/edit", nil)
	out := s.afterEdit("fr-relay", r)

	if out["status"] != "partial" {
		t.Errorf("an edit that never reached the node reported %v", out["status"])
	}
	if out["node"] != "kharej-de" {
		t.Errorf("the reply does not name the server: %v", out["node"])
	}
	for _, k := range []string{"peerError", "peerHint"} {
		if v, _ := out[k].(string); !strings.Contains(v, "kharej-de") {
			t.Errorf("%s does not tell the operator which end is behind: %q", k, v)
		}
	}

	// An unpaired tunnel is the ordinary case and gains nothing.
	plain := s.afterEdit("some-other-tunnel", r)
	if plain["status"] != "ok" || plain["node"] != nil {
		t.Errorf("an unpaired edit was reported as %v", plain)
	}
}

// A pairing is remembered until one end of it goes, and removing the server
// forgets every tunnel on it — otherwise a later edit reports a node that is no
// longer in the fleet.
func TestPairingsAreForgottenWithTheirServer(t *testing.T) {
	isolateFleet(t)

	manage.NoteNodePair("fr-relay", "kharej-de", "fr-relay-kharej")
	manage.NoteNodePair("de-edge", "kharej-de", "de-edge-kharej")
	manage.NoteNodePair("nl-ws", "kharej-nl", "nl-ws-kharej")

	if got := manage.TunnelsOnNode("kharej-de"); len(got) != 2 {
		t.Fatalf("tunnels on kharej-de: %v", got)
	}
	if err := manage.ForgetNodePairs("kharej-de"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if got := manage.TunnelsOnNode("kharej-de"); len(got) != 0 {
		t.Errorf("removing the server left %v behind", got)
	}
	if _, ok := manage.NodeFor("nl-ws"); !ok {
		t.Error("removing one server forgot another server's tunnel")
	}

	manage.ForgetNodePair("nl-ws")
	if _, ok := manage.NodeFor("nl-ws"); ok {
		t.Error("a deleted tunnel is still paired")
	}
}

// The picker and the setup-link box both apply to a reverse tunnel and a direct
// one, so neither may sit inside the half of the form that only reverse sees.
//
// This is a guard rather than a style note. Both of them did sit there, and
// nothing failed: the form rendered, the tests passed, and the feature was
// simply absent for every direct tunnel — which is half of what this project
// builds. A rule about where two ids live is cheap; discovering this from a bug
// report is not.
func TestTheNodePickerReachesBothKindsOfTunnel(t *testing.T) {
	loadPanel()

	raw, err := fs.ReadFile(panelRoot, "views/add.html")
	if err != nil {
		t.Fatalf("reading add.html: %v", err)
	}
	html := string(raw)

	// The box that pasted a setup link from a second panel used to be checked
	// beside this one. It is gone: the panel writes the far end itself, so
	// there is no second panel and nothing to paste.
	if strings.Contains(html, `id="apaste"`) {
		t.Error("add.html still has the paste-a-setup-link box, which is the two-pass " +
			"flow the fleet exists to remove")
	}

	rev := strings.Index(html, `<div class="step3rev">`)
	if rev < 0 {
		t.Fatal("add.html has no .step3rev — this guard needs updating")
	}
	at := strings.Index(html, `id="nodeGrp"`)
	if at < 0 {
		t.Fatal(`id="nodeGrp" is not in add.html any more, so there is no way to pick ` +
			"the server the far end is written on")
	}
	if at > rev {
		t.Error(`id="nodeGrp" sits inside .step3rev, so it is hidden for every direct ` +
			"tunnel — move it above the reverse/direct split")
	}
}

// The line the panel hands out has to be a line the installer understands.
//
// Enrollment is the only fleet CLI entry point on a managed server.
func TestTheFarSideUsesEnrollmentWithoutRemoteExec(t *testing.T) {
	cli, err := os.ReadFile(filepath.Join("..", "..", "nodecmd.go"))
	if err != nil {
		t.Fatalf("reading nodecmd.go: %v", err)
	}
	src := string(cli)
	if !strings.Contains(src, `case "join":`) {
		t.Fatal("`fullpack node join` is missing")
	}
	for _, gone := range []string{`case "exec":`, `case "setup":`, `case "run":`, `case "remove":`} {
		if strings.Contains(src, gone) {
			t.Errorf("nodecmd.go still has obsolete fleet command %s", gone)
		}
	}

	sh, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	if strings.Contains(string(sh), "node setup") {
		t.Error("install.sh still ends in `fullpack node setup`, which no longer exists")
	}
	// The installer supports noninteractive deployment before enrollment.
	if !strings.Contains(string(sh), "if [ -t 0 ]") {
		t.Error("install.sh no longer checks for a terminal")
	}
}

func TestStartStopAndRestartReachTheOtherEnd(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()

	if err := manage.NoteNodePair("fr-relay", "kharej-de", "fr-relay-kharej"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	for _, action := range []string{"start", "stop", "restart"} {
		out := s.alsoOnNode("fr-relay", action)
		if out["status"] != "partial" {
			t.Errorf("%s: an action that never reached the node reported %v", action, out["status"])
		}
		if out["node"] != "kharej-de" {
			t.Errorf("%s: the reply does not name the server: %v", action, out["node"])
		}
		if v, _ := out["peerError"].(string); !strings.Contains(v, "kharej-de") {
			t.Errorf("%s: %q does not say which end is behind", action, v)
		}
	}

	// Deleting is deliberately not one of them: there is no operation that
	// removes a tunnel on a node, and a delete here is not consent to one there.
	if out := s.alsoOnNode("fr-relay", "delete"); out["status"] != "ok" || out["node"] != nil {
		t.Errorf("delete reached across: %v", out)
	}
	// And an unpaired tunnel is the ordinary case, unchanged.
	if out := s.alsoOnNode("some-other", "restart"); out["status"] != "ok" || out["node"] != nil {
		t.Errorf("an unpaired restart was reported as %v", out)
	}
}

// The far end's own name is remembered, because every operation that reaches
// across has to name it and it is not the same name as this end's.
func TestThePeerNameIsRemembered(t *testing.T) {
	isolateFleet(t)
	manage.NoteNodePair("fr-relay", "kharej-de", "fr-relay-kharej")
	p, ok := manage.PairFor("fr-relay")
	if !ok || p.Node != "kharej-de" || p.PeerName != "fr-relay-kharej" {
		t.Fatalf("PairFor = %+v, ok=%v", p, ok)
	}
	if _, ok := manage.PairFor("nothing"); ok {
		t.Error("an unpaired tunnel reported a pair")
	}
}

// An edit rebuilds the far end, so it first asks the far end what it already
// has. A node that cannot answer must not turn that into a failed edit: the
// carry-forward is an improvement on the rebuild, not a precondition for it.
func TestTheCarryForwardNeverBlocksAnEdit(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()

	if got := peerConnOnNode(s.nodes.Runner(), "kharej-de", "fr-relay-kharej"); got != nil {
		t.Errorf("an unreachable node produced settings out of nowhere: %+v", got)
	}
}

// The Agent protocol has no remote installer or shell operation. Updates are
// performed with the verified local updater on the managed machine.
func TestRemoteUpgradeIsUnavailableOverAgent(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)
	cred, err := node.GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.AddManaged("germany", "http://controller:7777", "node-upgrade", cred); err != nil {
		t.Fatal(err)
	}
	w := post(t, s, "action=upgrade&name=germany")
	if w.Code == http.StatusOK {
		t.Fatalf("remote upgrade was offered without a restricted Agent operation: %s", w.Body.String())
	}
}

// The fleet page keeps a server's own account of itself current.
//
// What a managed server says about itself — its version, its uptime, and now
// its processor and memory — was written down when it was added and rewritten
// only when somebody upgraded or refreshed it by hand. Half of that is a
// reading rather than a fact: a card showing 4% processor from an hour ago,
// labelled as now, is worse than one showing nothing.
func TestTheFleetPageRefreshesWhatEachServerReports(t *testing.T) {
	isolateFleet(t)
	s := newFleetServer()
	t.Cleanup(s.nodes.Stop)

	f := newFake()
	f.up["germany"] = true
	f.answers[node.OpHello] = node.Info{Version: "v1.7.7.5", CPUPercent: 41, MemPercent: 62}
	withFleet(s, f)

	cred, err := node.GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.AddManaged("germany", "http://controller:7777", "node-refresh", cred); err != nil {
		t.Fatal(err)
	}
	if err := node.NoteInfo("germany", f.answers[node.OpHello].(node.Info)); err != nil {
		t.Fatal(err)
	}

	// Fresh: the add just asked, so a listing straight afterwards must not ask
	// again — a page that re-read every card on every poll would put a round
	// trip per server into every few seconds.
	before := countCalls(f, "germany:"+node.OpHello)
	if w := getNodes(t, s); w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if got := countCalls(f, "germany:"+node.OpHello); got != before {
		t.Errorf("a listing right after the add asked the server again (%d → %d)", before, got)
	}

	// Stale: age the record past the window and it has to ask.
	agePastInfoTTL(t, "germany")
	if w := getNodes(t, s); w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if got := countCalls(f, "germany:"+node.OpHello); got <= before {
		t.Error("a stale record was served as current — the card would show a " +
			"processor reading from whenever the server was added")
	}
}

func getNodes(t *testing.T, s *server) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleNodes(w, httptest.NewRequest("GET", "/api/nodes", nil))
	return w
}

func countCalls(f *fakeRunner, want string) int {
	n := 0
	for _, c := range f.calls {
		if c == want {
			n++
		}
	}
	return n
}

// agePastInfoTTL rewinds when a server last reported, so the next listing
// treats what is stored as old.
func agePastInfoTTL(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(node.StorePath)
	if err != nil {
		t.Fatalf("reading the fleet: %v", err)
	}
	var store struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		t.Fatalf("fleet file: %v", err)
	}
	for _, n := range store.Nodes {
		if s, _ := n["name"].(string); strings.EqualFold(s, name) {
			n["lastSeen"] = time.Now().Add(-time.Hour).Unix()
		}
	}
	out, _ := json.Marshal(store)
	if err := os.WriteFile(node.StorePath, out, 0600); err != nil {
		t.Fatalf("writing the fleet: %v", err)
	}
}
