package app

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

type tcpFake struct {
	mu    sync.Mutex
	ok    map[string]bool
	delay map[string]time.Duration
	calls []string
}

func (f *tcpFake) probe(host string, port int) bool {
	key := host + ":" + strconv.Itoa(port)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	ok, d := f.ok[key], f.delay[host]
	f.mu.Unlock()
	time.Sleep(d)
	return ok
}

func pickController(t *testing.T, f *tcpFake) *Controller {
	t.Helper()
	c := New(Options{DataDir: t.TempDir(), Interval: -1, AllowExampleNodes: true, Probes: Probes{TCP: f.probe}})
	t.Cleanup(c.Close)
	if err := c.importText(twoServers, true, true); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAutoPickChoosesFasterServer(t *testing.T) {
	f := &tcpFake{
		ok:    map[string]bool{"203.0.113.10:443": true, "203.0.113.20:443": true},
		delay: map[string]time.Duration{"203.0.113.10": 80 * time.Millisecond, "203.0.113.20": 5 * time.Millisecond},
	}
	c := pickController(t, f)
	c.pickLine()
	if s := c.Snapshot(); s.Server != "203.0.113.20" || !s.AutoPick {
		t.Fatalf("should pick the faster server, got %s auto=%v", s.Server, s.AutoPick)
	}
	if len(f.calls) != 2 {
		t.Fatalf("one handshake per server, calls=%v", f.calls)
	}
}

func TestManualPickKeepsServerButSkipsDeadLine(t *testing.T) {
	f := &tcpFake{ok: map[string]bool{"203.0.113.10:8443": true, "203.0.113.20:443": true}}
	c := pickController(t, f)
	c.SelectServer("203.0.113.20")
	c.SelectServer("203.0.113.10")
	if c.Snapshot().AutoPick {
		t.Fatal("selecting a server by hand must turn auto off")
	}
	f.calls = nil
	c.pickLine()
	s := c.Snapshot()
	if s.Server != "203.0.113.10" || s.Port != 8443 {
		t.Fatalf("got %s:%d", s.Server, s.Port)
	}
	if s.Nodes[0].Health != HealthBlocked || s.Nodes[1].Health != HealthReachable {
		t.Fatalf("health=%v %v", s.Nodes[0].Health, s.Nodes[1].Health)
	}
	for _, call := range f.calls {
		if call == "203.0.113.10:36712" || call == "203.0.113.20:443" {
			t.Fatalf("must not probe UDP line or other server: %v", f.calls)
		}
	}
}

func TestPickLineKeepsCurrentWhenNothingAnswers(t *testing.T) {
	f := &tcpFake{ok: map[string]bool{}}
	c := pickController(t, f)
	c.pickLine()
	s := c.Snapshot()
	if s.Server != "203.0.113.10" || s.Port != 443 {
		t.Fatalf("got %s:%d", s.Server, s.Port)
	}
	for _, n := range s.Nodes {
		if n.Health == HealthBlocked {
			t.Fatalf("all failing may be the local network, nothing should be marked blocked: %+v", s.Nodes)
		}
	}
}

func TestSetAutoPickTurnsBackOn(t *testing.T) {
	c := pickController(t, &tcpFake{})
	c.SelectServer("203.0.113.20")
	c.SetAutoPick(true)
	if !c.Snapshot().AutoPick {
		t.Fatal("auto should be on")
	}
}
