package node

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateStore(t *testing.T) {
	t.Helper()
	old := StorePath
	StorePath = filepath.Join(t.TempDir(), "nodes.json")
	t.Cleanup(func() { StorePath = old })
}

func TestAgentCredentialIsSealedAndBlanked(t *testing.T) {
	isolateStore(t)
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if err := SaveStore(Store{Nodes: []Node{{Name: "a", ID: "id-a", Credential: secret}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || !HasSealedCredentials() {
		t.Fatal("Agent credential was not sealed on disk")
	}
	if got := LoadStore().Nodes[0].Credential; got != secret {
		t.Fatal("sealed credential did not read back")
	}
	public := List()[0]
	data, _ := json.Marshal(public)
	if public.Credential != "" || public.CredentialSealed != "" || strings.Contains(string(data), secret) {
		t.Fatal("fleet listing exposed the credential")
	}
}

func TestMissingFleetKeyKeepsIdentityWithoutMintingKey(t *testing.T) {
	isolateStore(t)
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if err := SaveStore(Store{Nodes: []Node{{Name: "a", ID: "id-a", Credential: secret}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath()); err != nil {
		t.Fatal(err)
	}
	got := LoadStore()
	if len(got.Nodes) != 1 || got.Nodes[0].Name != "a" || got.Nodes[0].Credential != "" {
		t.Fatalf("restored identity or secret is wrong: %+v", got.Nodes)
	}
	if _, err := os.Stat(keyPath()); !os.IsNotExist(err) {
		t.Fatal("reading a restored registry minted a replacement fleet key")
	}
	if err := SaveStore(got); err != nil {
		t.Fatal(err)
	}
	if !HasSealedCredentials() {
		t.Fatal("saving restored fleet erased unrecoverable ciphertext")
	}
}

func TestTheSealingKeyIsRootOnly(t *testing.T) {
	isolateStore(t)
	if _, err := seal("x"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the fleet key is %#o", st.Mode().Perm())
	}
}
