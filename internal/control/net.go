package control

import (
	"context"
	"sync"
	"time"

	"github.com/firegoood/FullPck/internal/node"
)

// Measure round trips over the Node's existing reverse Agent session. Never
// open a Controller-to-Node probe connection to its public address. This is an
// application round trip, not an ICMP or raw packet-loss measurement.
const (
	// probeEvery is how often the whole fleet is measured. The report asked for
	// five seconds; the measurement itself takes about one, so this is a
	// quarter of the time spent pinging and the rest idle.
	probeEvery = 5 * time.Second
)

// NetHealth describes a measured Agent session round trip. Measured is false
// when no authenticated session answered; transport packet loss is not inferred.
type NetHealth struct {
	Measured bool    `json:"measured"`
	LossPct  float64 `json:"lossPct"`
	RTTms    float64 `json:"rttMs"`
	JitterMs float64 `json:"jitterMs"`
	At       int64   `json:"at,omitempty"` // unix seconds
}

// Net holds the last measurement for every managed server.
//
// It was a package-level variable in internal/webui, sitting next to the HTTP
// handlers that read it. That is exactly what this package exists to stop: the
// panel is a reader of the fleet's state, not its owner. Build one with NewNet
// and hand it to whoever needs it.
type Net struct {
	mu      sync.Mutex
	seen    map[string]NetHealth
	started bool
}

// Health returns what is known about the path to one server. A server never
// measured comes back zeroed with Measured false, which is what a card that
// has not been told anything yet should draw.
func (p *Net) Health(name string) NetHealth {
	// A nil probe is one nobody has built — a panel assembled for a test, or a
	// reader that does not measure. It has told the card nothing, which is
	// exactly what an unmeasured reading means, so it answers rather than
	// panicking on a page nobody was looking at.
	if p == nil {
		return NetHealth{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[name]
}

// start begins measuring, once per process. Starting twice would double the
// probe rate for no extra information.
//
// The context is what stops it. The loop was `for range t.C` with nothing else
// in the select, so it could not be stopped by anything short of ending the
// process — which is fine for the singleton the panel starts and wrong for
// everything else: a test that started one left it running for the rest of the
// suite, dialling every server in whatever fleet happened to be on the machine,
// and a panel that has shut its listener down went on probing.
func (p *Net) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()

	go p.loop(ctx)
}

func (p *Net) loop(ctx context.Context) {
	// A first pass immediately, so a panel that has just started has something
	// to show on the first fleet page somebody opens rather than a dash for the
	// first five seconds.
	p.pass(ctx)

	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pass(ctx)
		}
	}
}

// pass measures every server in the fleet.
//
// The servers are measured together rather than one after another: done in
// sequence a fleet of six would take six seconds to get through a round that is
// supposed to happen every five, and the last card would always be showing a
// reading older than the first.
func (p *Net) pass(ctx context.Context) {
	list := node.List()
	if len(list) == 0 {
		return
	}

	type result struct {
		name string
		h    NetHealth
	}
	out := make([]result, len(list))

	var wg sync.WaitGroup
	for i, n := range list {
		wg.Add(1)
		go func(i int, n node.Node) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			start := time.Now()
			err := node.DefaultHub.Call(probeCtx, n.ID, node.OpPing, nil, nil)
			out[i] = result{name: n.Name, h: NetHealth{
				Measured: err == nil,
				RTTms:    float64(time.Since(start).Microseconds()) / 1000,
				At:       time.Now().Unix(),
			}}
		}(i, n)
	}
	wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	// Rebuilt rather than merged, so a server that has left the fleet stops
	// being remembered here too.
	fresh := make(map[string]NetHealth, len(out))
	for _, r := range out {
		fresh[r.name] = r.h
	}
	p.seen = fresh
}

// NewNet builds a probe. It measures nothing until Start is called.
func NewNet() *Net { return &Net{seen: map[string]NetHealth{}} }
