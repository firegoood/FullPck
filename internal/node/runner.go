package node

import (
	"context"
	"fmt"
	"time"
)

// Runner is the typed operation surface used by the Fleet controller. The
// production runner sends calls over authenticated reverse Agent sessions.
type Runner interface {
	Call(name, op string, body, out any) error
	IsOnline(name string) bool
	Reachable(name string) (bool, string)
	Forget(name string)
}

// ErrOffline preserves a machine name and cause for the operator.
type ErrOffline struct {
	Name string
	Why  string
	Err  error
}

func (e ErrOffline) Error() string {
	if e.Why == "" {
		return fmt.Sprintf("%s is offline", e.Name)
	}
	return fmt.Sprintf("%s could not be reached: %s", e.Name, e.Why)
}
func (e ErrOffline) Unwrap() error { return e.Err }

// AgentRunner never dials a managed Node. The Node maintains the outbound
// Agent session on the controller's existing WebUI listener.
type AgentRunner struct{ hub *Hub }

var _ Runner = (*AgentRunner)(nil)

func NewAgentRunner(hub *Hub) *AgentRunner {
	if hub == nil {
		hub = DefaultHub
	}
	return &AgentRunner{hub: hub}
}

func (r *AgentRunner) Call(name, op string, body, out any) error {
	n, ok := Find(name)
	if !ok {
		return fmt.Errorf("no server called %q", name)
	}
	if n.Revoked {
		return ErrAgentRevoked
	}
	if n.ID == "" {
		return ErrOffline{Name: name, Why: "the Node has not enrolled with the reverse Agent", Err: ErrAgentOffline}
	}
	var hello Info
	target := out
	if op == OpHello && target == nil {
		target = &hello
	}
	timeout := 60 * time.Second
	if op == OpHello {
		// Status metadata must not exhaust the WebUI's HTTP write deadline.
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := r.hub.Call(ctx, n.ID, op, body, target); err != nil {
		return ErrOffline{Name: name, Why: err.Error(), Err: err}
	}
	if op == OpHello && out == nil {
		_ = NoteInfo(name, hello)
	}
	return nil
}

func (r *AgentRunner) IsOnline(name string) bool {
	n, ok := Find(name)
	return ok && !n.Revoked && n.ID != "" && r.hub.IsOnline(n.ID)
}

// Ready tolerates a brief reconnect and requires an authenticated Ping reply.
func (r *AgentRunner) Ready(ctx context.Context, name string) error {
	n, ok := Find(name)
	if !ok {
		return fmt.Errorf("no server called %q", name)
	}
	if n.Revoked {
		return ErrAgentRevoked
	}
	if n.ID == "" {
		return ErrOffline{Name: name, Why: ErrAgentOffline.Error(), Err: ErrAgentOffline}
	}
	if err := r.hub.Ready(ctx, n.ID); err != nil {
		why := agentFailureReason(err)
		if !r.hub.IsOnline(n.ID) && ctx.Err() != nil {
			why = "managed node is offline; reconnect did not finish before the deadline"
		}
		return ErrOffline{Name: name, Why: why, Err: err}
	}
	return nil
}

func (r *AgentRunner) Reachable(name string) (bool, string) {
	n, ok := Find(name)
	if !ok {
		return false, "Node is not enrolled"
	}
	if n.Revoked {
		return false, ErrAgentRevoked.Error()
	}
	if n.ID == "" || !r.hub.IsOnline(n.ID) {
		return false, ErrAgentOffline.Error()
	}
	return true, ""
}

func (r *AgentRunner) Forget(string) {}
