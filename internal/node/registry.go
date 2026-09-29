package node

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/firegoood/FullPck/internal/app"
)

// StorePath holds enrolled Node identities and sealed credentials.
var StorePath = app.ConfigDir + "/nodes.json"

// Node is one managed server.
type Node struct {
	// Name is what the operator called it and how every other part of the
	// panel refers to it. It is fixed when the server is added: renaming would
	// strand the tunnels already pointing at it.
	Name string `json:"name"`

	// ID is the stable identity used by the reverse Agent session. It is
	// independent of the observed address and therefore remains valid when the
	// node changes IP or reconnects through another route.
	ID string `json:"id,omitempty"`

	// ControllerURL is the configured WebUI origin the node dials outward to.
	// The Agent always appends /_bp/node and never assumes a default port.
	ControllerURL string `json:"controller_url,omitempty"`

	// Credential is the permanent per-node secret. It is unsealed on load only
	// inside this package and is never returned by List or Find.
	Credential       string `json:"credential,omitempty"`
	CredentialSealed string `json:"credential_sealed,omitempty"`

	// Revoked prevents a removed or explicitly revoked node from reconnecting
	// with a credential that may still exist on its disk.
	Revoked bool `json:"revoked,omitempty"`

	Added            int64  `json:"added"`              // unix seconds
	LastSeen         int64  `json:"lastSeen,omitempty"` // unix seconds
	LastConnected    int64  `json:"last_connected,omitempty"`
	LastDisconnected int64  `json:"last_disconnected,omitempty"`
	ObservedAddress  string `json:"observed_address,omitempty"`
	DisconnectReason string `json:"disconnect_reason,omitempty"`
	ProtocolVersion  int    `json:"protocol_version,omitempty"`

	// Info is what the server last said about itself. It is stored rather than
	// asked for on demand so the fleet screen can draw a server that is down.
	Info Info `json:"info,omitempty"`
}

// Store is the whole persisted state.
type Store struct {
	Nodes []Node `json:"nodes,omitempty"`
}

// storeMu serialises read-modify-write cycles. Two writes landing together
// would otherwise each read the file, make their change, and write back — and
// whichever wrote second would silently drop the other.
var storeMu sync.Mutex

// LoadStore reads the persisted state. A missing file is an empty fleet.
func LoadStore() Store {
	var s Store
	if data, err := os.ReadFile(StorePath); err == nil {
		json.Unmarshal(data, &s)
	}
	for i := range s.Nodes {
		if s.Nodes[i].CredentialSealed != "" {
			s.Nodes[i].Credential = unseal(s.Nodes[i].CredentialSealed)
		}
	}
	return s
}

// SaveStore persists the state, root-only and with each Agent secret sealed.
//
// Sealing here rather than at each call site is what makes it unconditional:
// this is the only function that writes the file, so there is no path by which
// an Agent credential reaches the disk in the clear.
func SaveStore(s Store) error {
	out := Store{Nodes: append([]Node(nil), s.Nodes...)}
	for i := range out.Nodes {
		if out.Nodes[i].Credential != "" {
			sealed, err := seal(out.Nodes[i].Credential)
			if err != nil {
				return err
			}
			out.Nodes[i].CredentialSealed = sealed
		}
		// A restored registry may lack its separate key. Keep the original
		// ciphertext so a later key import can recover the Agent credential.
		out.Nodes[i].Credential = ""
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return app.WriteFileAtomic(StorePath, data, 0600)
}

// update runs fn against the store under the lock and saves the result.
func update(fn func(*Store) error) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	s := LoadStore()
	if err := fn(&s); err != nil {
		return err
	}
	return SaveStore(s)
}

// nameRx is deliberately strict. A node name reaches a systemd unit name and a
// config path on the far machine, so anything that could be read as a path
// separator or a shell metacharacter is refused at the door rather than escaped
// at each of the places it is later used.
func validName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("the server needs a name")
	}
	if len(name) > 40 {
		return fmt.Errorf("that name is longer than 40 characters")
	}
	for _, r := range name {
		ok := r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("a server name can hold letters, digits, - and _ only")
		}
	}
	return nil
}

// blank returns a copy without the Agent credential.
func blank(n Node) Node {
	n.Credential = ""
	n.CredentialSealed = ""
	return n
}

// AddManaged records a reverse-Agent node. SaveStore seals its credential.
func AddManaged(name, controllerURL, id, credential string) (Node, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return Node{}, err
	}
	var err error
	controllerURL, err = ValidateControllerURL(controllerURL)
	if err != nil {
		return Node{}, err
	}
	id = strings.TrimSpace(id)
	credential = strings.TrimSpace(credential)
	if id == "" || credential == "" {
		return Node{}, fmt.Errorf("managed nodes need an identity and permanent credential")
	}
	secret, err := base64.RawURLEncoding.DecodeString(credential)
	if err != nil || len(secret) != 32 {
		return Node{}, fmt.Errorf("managed Node credentials must hold 32 random bytes")
	}
	var out Node
	err = update(func(s *Store) error {
		for _, n := range s.Nodes {
			if strings.EqualFold(n.Name, name) {
				return fmt.Errorf("a server called %q is already in the fleet", name)
			}
			if n.ID != "" && n.ID == id {
				return fmt.Errorf("node identity %q is already enrolled", id)
			}
		}
		out = Node{Name: name, ID: id, ControllerURL: controllerURL,
			Credential: credential, Added: time.Now().Unix()}
		s.Nodes = append(s.Nodes, out)
		return nil
	})
	if err != nil {
		return Node{}, err
	}
	return out, nil
}

// findByID returns the secret only inside this package. Browser handlers must
// use List/Find, which blank credentials before serialisation.
func findByID(id string) (Node, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Node{}, false
	}
	for _, n := range LoadStore().Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Revoke permanently disables a node credential without deleting its history.
func Revoke(name string) error {
	var id string
	err := update(func(s *Store) error {
		for i := range s.Nodes {
			if strings.EqualFold(s.Nodes[i].Name, name) {
				id = s.Nodes[i].ID
				s.Nodes[i].Revoked = true
				return nil
			}
		}
		return fmt.Errorf("no server called %q", name)
	})
	if err == nil && id != "" {
		DefaultHub.CloseNode(id)
	}
	return err
}

// List returns the fleet, oldest first, without credentials.
func List() []Node {
	s := LoadStore()
	out := make([]Node, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		out = append(out, blank(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Added < out[j].Added })
	return out
}

// Find returns one server by name, without its credential.
func Find(name string) (Node, bool) {
	n, ok := findWithSecret(name)
	if !ok {
		return Node{}, false
	}
	return blank(n), true
}

// findWithSecret returns one server as stored, credential included. Unexported
// on purpose: everything outside this package wants Find.
func findWithSecret(name string) (Node, bool) {
	for _, n := range LoadStore().Nodes {
		if strings.EqualFold(n.Name, name) {
			return n, true
		}
	}
	return Node{}, false
}

// NoteInfo stores what a server last reported about itself.
func NoteInfo(name string, info Info) error {
	return update(func(s *Store) error {
		for i := range s.Nodes {
			if strings.EqualFold(s.Nodes[i].Name, name) {
				info.Name = s.Nodes[i].Name
				s.Nodes[i].Info = info
				s.Nodes[i].LastSeen = time.Now().Unix()
				return nil
			}
		}
		return fmt.Errorf("no server called %q", name)
	})
}

func noteConnection(id, observed, reason string, connected bool) {
	_ = update(func(s *Store) error {
		for i := range s.Nodes {
			if s.Nodes[i].ID != id {
				continue
			}
			if connected {
				s.Nodes[i].LastConnected = time.Now().Unix()
				s.Nodes[i].ObservedAddress = observed
				s.Nodes[i].ProtocolVersion = agentProtocolVersion
				s.Nodes[i].DisconnectReason = ""
			} else {
				s.Nodes[i].LastDisconnected = time.Now().Unix()
				s.Nodes[i].DisconnectReason = reason
			}
			return nil
		}
		return fmt.Errorf("unknown Node")
	})
}

// Remove takes a server out of the fleet. Its tunnels keep running there,
// because they are systemd services on that machine and have nothing to do with
// this panel being able to reach it.
func Remove(name string) error {
	var id string
	err := update(func(s *Store) error {
		kept := s.Nodes[:0]
		found := false
		for _, n := range s.Nodes {
			if strings.EqualFold(n.Name, name) {
				found = true
				id = n.ID
				continue
			}
			kept = append(kept, n)
		}
		s.Nodes = kept
		if !found {
			return fmt.Errorf("no server called %q", name)
		}
		return nil
	})
	if err == nil && id != "" {
		DefaultHub.CloseNode(id)
	}
	return err
}
