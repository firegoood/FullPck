package webui

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/firegoood/FullPck/internal/manage"
)

// Managed pairing derives the far end; Manual remains a separate local path.
// The mirror below still derives the peer config for Agent pairing.

func TestThePanelKeepsManualAndManagedCreationPaths(t *testing.T) {
	loadPanel()

	api, err := fs.ReadFile(panelRoot, "js/api.js")
	if err != nil {
		t.Fatalf("cannot read api.js: %v", err)
	}
	if strings.Contains(string(api), "sharelink") {
		t.Error("the panel still calls the share-link endpoint, which is gone — the " +
			"call would 404 and the wizard would offer a link it cannot build")
	}

	add, err := fs.ReadFile(panelRoot, "js/views/add.js")
	if err != nil {
		t.Fatalf("cannot read add.js: %v", err)
	}
	src := string(add)
	for _, gone := range []string{"shareLinkDecode", "paintHandoff", "applyPastedLink"} {
		if strings.Contains(src, gone) {
			t.Errorf("add.js still has %s, so the second-pass path is still on screen", gone)
		}
	}
	mode, err := fs.ReadFile(panelRoot, "js/lib/addmode.js")
	if err != nil {
		t.Fatalf("cannot read addmode.js: %v", err)
	}
	for _, path := range []string{"api.tunnelCreate(payload)", "api.directCreate(payload)", "api.nodePair("} {
		if !strings.Contains(string(mode), path) {
			t.Errorf("creation router lost %s", path)
		}
	}
	if !strings.Contains(src, "createTunnelForMode(api, creationMode") ||
		!strings.Contains(src, "let creationMode = 'manual'") {
		t.Error("the Add Tunnel view no longer routes by an explicit Manual default")
	}
	if strings.Contains(src, "noFleet(") {
		t.Error("the wizard blocks manual creation when no managed Node is online")
	}
}

// The mirroring is what the push depends on, so it stays, and stays exercised.
func TestTheMirrorStillDerivesTheFarEnd(t *testing.T) {
	link, err := manage.ShareLink{
		V: 1, Kind: "reverse", From: "iran", Name: "fr-relay",
		Tr: "tcpmux", Host: "203.0.113.9", Port: "8443", Tok: "s3cret",
	}.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := manage.DecodeShareLink(link)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	form := manage.MirrorForPeer(parsed)
	if form.Kind != "reverse" {
		t.Errorf("the mirror changed the kind to %q", form.Kind)
	}
	t2 := form.ToNewTunnel()
	if t2.Role != "client" {
		t.Errorf("the far end of a server is %q, not a client", t2.Role)
	}
	if t2.Token != "s3cret" {
		t.Error("the token did not survive the mirror, so the two ends would not agree")
	}
	if t2.Transport != "tcpmux" {
		t.Errorf("the transport changed to %q", t2.Transport)
	}
}
