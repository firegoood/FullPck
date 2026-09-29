package node

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/backpack/backpack/internal/app"
)

// The Controller seals each permanent Agent credential before storing it.
// The key stays beside the registry but is excluded from backup archives, so
// restoring a backup on another machine requires importing the separately
// exported key or enrolling Nodes again. Root on this machine can read both.

// sealKeyName is the key file, beside the registry it seals.
//
// Derived from StorePath rather than kept as a second variable, so anything
// that points the registry somewhere else — a test, and there are several —
// takes the key with it. Two knobs meant every caller had to know about the
// second one, and the ones that did not tried to write the key into /etc while
// their registry was safely in a temp directory.
const sealKeyName = "node.key"

func keyPath() string { return filepath.Join(filepath.Dir(StorePath), sealKeyName) }

// sealPrefix marks a sealed value.
const sealPrefix = "enc:v1:"

// sealingKey returns the key, creating it on first use.
func sealingKey() ([]byte, error) {
	if b, err := os.ReadFile(keyPath()); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating the fleet key: %w", err)
	}
	// 0600 and never in the archive — see the note above and backup.go, which
	// skips it by name.
	if err := app.WriteFileAtomic(keyPath(), key, 0o600); err != nil {
		return nil, fmt.Errorf("writing the fleet key: %w", err)
	}
	return key, nil
}

// seal encrypts a credential for storage. Empty input seals to nothing.
func seal(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	gcm, err := sealingAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(password), nil)
	return sealPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// unseal reverses seal. A value without the prefix is returned as it is.
//
// A sealed value that cannot be opened returns empty and no error. That is the
// registry having been restored onto a machine whose key is a different one,
// and it is not a failure to report to the caller: the fleet still lists its
// servers, but an Agent cannot reconnect until the key is imported or it is
// enrolled again.
func unseal(stored string) string {
	if stored == "" || !strings.HasPrefix(stored, sealPrefix) {
		return stored
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealPrefix))
	if err != nil {
		return ""
	}
	// Reading does not mint a key.
	//
	// It used to: unseal went through sealingAEAD, which creates one on first
	// use. That is right for sealing — you cannot write without a key — and it
	// is a trap on the read path, and precisely on the path that matters. A
	// registry restored onto a fresh machine has sealed values and no key; the
	// fleet screen reads them, a key is minted as a side effect of failing to
	// decrypt them, and ImportSealKey then refuses to install the real one
	// because "this machine already has a fleet key". The operator is locked
	// out of their own recovery by the act of looking at it.
	//
	// So a missing key here is what it has always meant one step later: this
	// value cannot be read, while the fleet still lists its servers.
	gcm, err := existingSealingAEAD()
	if err != nil {
		return ""
	}
	if len(raw) < gcm.NonceSize() {
		return ""
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// existingSealingAEAD is sealingAEAD for the read path: it uses the key that is
// there and refuses rather than creating one. See unseal.
func existingSealingAEAD() (cipher.AEAD, error) {
	key, err := os.ReadFile(keyPath())
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("the fleet key is %d bytes rather than 32", len(key))
	}
	return aeadFor(key)
}

func sealingAEAD() (cipher.AEAD, error) {
	key, err := sealingKey()
	if err != nil {
		return nil, err
	}
	return aeadFor(key)
}

func aeadFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Taking the key deliberately.
//
// The seal is doing exactly what it was designed to do, and the design has a
// consequence nobody has hit yet: restoring a backup onto a different machine
// brings the fleet list back without the credentials to use it. That is correct
// for a backup that was emailed or left on a laptop. It is the wrong answer for
// the one case that matters most — the panel machine is gone and this is the
// recovery.
//
// So the key can be taken out and put back, on purpose, by somebody with a
// shell on the machine. Deliberately not through the panel and deliberately not
// in the archive: the whole protection is that the key and the data travel
// separately, and a button that put them back together would be the protection
// removed with a nicer name. Two things, two places, the operator's choice to
// bring them together.

// ExportSealKey returns the fleet key as a base64 string, for an operator who
// is keeping it somewhere other than this machine.
//
// It does not create one. A key that does not exist yet is a fleet with no
// sealed Agent credentials in it, and handing out a freshly minted key would file
// something that protects nothing.
func ExportSealKey() (string, error) {
	b, err := os.ReadFile(keyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("this machine has no fleet key yet — one is created the " +
				"first time a managed Agent credential is stored, so there is nothing to keep")
		}
		return "", fmt.Errorf("reading the fleet key: %w", err)
	}
	if len(b) != 32 {
		return "", fmt.Errorf("the fleet key is %d bytes rather than 32; it is damaged", len(b))
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ImportSealKey puts a previously exported key back, so a restored registry
// becomes readable again.
//
// It refuses when a key is already present. Overwriting one would leave every
// credential currently sealed on this machine unreadable, which is a worse
// outcome than the one being recovered from and is not undoable — the operator
// has to move the existing key out of the way themselves, having decided that
// is what they mean.
func ImportSealKey(encoded string) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return fmt.Errorf("that is not a fleet key: %w", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("a fleet key is 32 bytes; this one is %d", len(key))
	}
	if b, err := os.ReadFile(keyPath()); err == nil && len(b) > 0 {
		if string(b) == string(key) {
			return nil // already the key we were given; nothing to do
		}
		return fmt.Errorf("this machine already has a fleet key, and replacing it would make "+
			"every Agent credential currently sealed here unreadable. Move %s aside first if that "+
			"is what you mean", keyPath())
	}
	if err := os.MkdirAll(filepath.Dir(keyPath()), 0o700); err != nil {
		return fmt.Errorf("preparing the key directory: %w", err)
	}
	if err := app.WriteFileAtomic(keyPath(), key, 0o600); err != nil {
		return fmt.Errorf("writing the fleet key: %w", err)
	}
	return nil
}

// HasSealedCredentials reports whether stored Agent credentials require the key.
// Read the file directly: List intentionally removes secrets from its copies.
func HasSealedCredentials() bool {
	data, err := os.ReadFile(StorePath)
	if err != nil {
		return false
	}
	var s Store
	if json.Unmarshal(data, &s) != nil {
		return false
	}
	for _, n := range s.Nodes {
		if strings.HasPrefix(n.CredentialSealed, sealPrefix) {
			return true
		}
	}
	return false
}
