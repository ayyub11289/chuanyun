package app

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"chuanyun/internal/failover"
	"chuanyun/internal/singbox"
)

type fakeFirewall struct {
	mu      sync.Mutex
	on      bool
	engages int
	allow   []string
	record  func(string)
}

func (f *fakeFirewall) Engage(allow []string, _ []netip.Prefix) error {
	f.mu.Lock()
	first := !f.on
	if first {
		f.engages++
		f.allow = allow
	}
	f.on = true
	f.mu.Unlock()
	if first {
		f.record("engage")
	}
	return nil
}

func (f *fakeFirewall) Release() {
	f.mu.Lock()
	f.on = false
	f.mu.Unlock()
}

func (f *fakeFirewall) Engaged() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on
}

type ksRig struct {
	c     *Controller
	fw    *fakeFirewall
	order []string
	exits []func(error)
	mu    sync.Mutex
}

func newKSRig(t *testing.T, mode string, failStart bool) *ksRig {
	t.Helper()
	r := &ksRig{}
	r.fw = &fakeFirewall{record: func(s string) {
		r.mu.Lock()
		r.order = append(r.order, s)
		r.mu.Unlock()
	}}
	r.c = New(Options{
		DataDir: t.TempDir(), ExePath: `C:\cy\Chuanyun.exe`, Interval: -1, AllowExampleNodes: true,
		OS:       &fakeOS{elevated: true},
		Firewall: r.fw,
		Now:      func() time.Time { return time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC) },
		Probes: Probes{
			Generate204: func(string) bool { return true },
			TCP:         func(string, int) bool { return true },
			Domestic:    func() failover.Domestic { return failover.Domestic{Checked: true, BaiduOK: true} },
		},
		FindSingBox: func() (string, error) { return `C:\cy\bin\sing-box.exe`, nil },
		StartCore: func(_, _ string, _ func(string), onExit func(error)) (func(), error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.order = append(r.order, "start")
			if failStart {
				return nil, fmt.Errorf("boom")
			}
			r.exits = append(r.exits, onExit)
			return func() {}, nil
		},
	})
	t.Cleanup(r.c.Close)
	if err := r.c.ImportText(bundle, true); err != nil {
		t.Fatal(err)
	}
	r.c.SetMode(mode)
	r.c.SetKillSwitch(true)
	return r
}

func (r *ksRig) starts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.exits)
}

func (r *ksRig) crash() {
	r.mu.Lock()
	exit := r.exits[len(r.exits)-1]
	r.mu.Unlock()
	exit(fmt.Errorf("killed"))
}

func TestKillSwitchEngagesBeforeTunnelAndHoldsAfterCrash(t *testing.T) {
	r := newKSRig(t, singbox.ModeTUN, false)
	r.c.connect()
	if r.order[0] != "engage" || r.order[1] != "start" {
		t.Fatalf("firewall must be up before the tunnel: %v", r.order)
	}
	if len(r.fw.allow) != 2 || r.fw.allow[0] != `C:\cy\bin\sing-box.exe` || r.fw.allow[1] != `C:\cy\Chuanyun.exe` {
		t.Fatalf("allow=%v", r.fw.allow)
	}

	r.crash()
	waitFor(t, func() bool { return r.starts() == 2 })
	if !r.fw.Engaged() || r.fw.engages != 1 {
		t.Fatalf("restart must keep the same block, engages=%d", r.fw.engages)
	}
	r.crash()
	waitFor(t, func() bool { return r.c.Snapshot().Phase == PhaseIdle })
	if s := r.c.Snapshot(); !s.Blocking || !r.fw.Engaged() {
		t.Fatalf("after giving up the network must stay blocked: %+v", s.Blocking)
	}

	r.c.ReleaseBlock()
	if r.fw.Engaged() || r.c.Snapshot().Blocking {
		t.Fatal("release should restore the network")
	}
}

func TestKillSwitchReleasedOnDisconnectAndFirstFailure(t *testing.T) {
	r := newKSRig(t, singbox.ModeTUN, false)
	r.c.connect()
	r.c.Disconnect()
	if r.fw.Engaged() {
		t.Fatal("disconnect must release")
	}

	f := newKSRig(t, singbox.ModeTUN, true)
	f.c.connect()
	if f.fw.Engaged() || f.c.Snapshot().Blocking {
		t.Fatal("a first connect that never worked must not leave the network blocked")
	}
}

func TestKillSwitchOnlyInTUN(t *testing.T) {
	r := newKSRig(t, singbox.ModeSystem, false)
	r.c.connect()
	if r.fw.engages != 0 {
		t.Fatal("system proxy mode must not touch the firewall")
	}
}

func TestKillSwitchToggleWhileConnected(t *testing.T) {
	r := newKSRig(t, singbox.ModeTUN, false)
	r.c.SetKillSwitch(false)
	r.c.connect()
	if r.fw.Engaged() {
		t.Fatal("off means off")
	}
	r.c.SetKillSwitch(true)
	if !r.fw.Engaged() {
		t.Fatal("turning on while connected should engage immediately")
	}
	r.c.SetKillSwitch(false)
	if r.fw.Engaged() {
		t.Fatal("turning off should release immediately")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout")
}
