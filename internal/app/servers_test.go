package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"chuanyun/internal/geo"
)

const twoServers = `{"nodes":[
{"type":"vless","name":"穿云-443","server":"203.0.113.10","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"},
{"type":"vless","name":"穿云-8443","server":"203.0.113.10","port":8443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"},
{"type":"hysteria2","name":"Hysteria2-36712","server":"203.0.113.10","port":36712,"password":"p"},
{"type":"vless","name":"备用-443","server":"203.0.113.20","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"}
]}`

func TestServersGroupLinesAndShowCountry(t *testing.T) {
	c := New(Options{
		DataDir: t.TempDir(), Interval: -1, AllowExampleNodes: true,
		Locate: func(_ context.Context, host, _ string) (geo.Info, error) {
			if host == "203.0.113.10" {
				return geo.Info{Code: "JP", Country: "日本", City: "东京"}, nil
			}
			return geo.Info{}, fmt.Errorf("unknown")
		},
	})
	defer c.Close()
	if err := c.importText(twoServers, true, true); err != nil {
		t.Fatal(err)
	}
	c.refreshGeo()
	if s := c.Snapshot().Servers; s[0].Code != "" || s[0].Place != "穿云" {
		t.Fatalf("must not look up before the tunnel is up: %+v", s[0])
	}
	c.mu.Lock()
	c.phase = PhaseConnected
	c.mu.Unlock()
	c.refreshGeo()

	s := c.Snapshot().Servers
	if len(s) != 2 {
		t.Fatalf("servers=%+v", s)
	}
	if s[0].Place != "日本 · 东京" || s[0].Code != "JP" || s[0].Lines != 3 || !s[0].Current || s[0].CurrentLine != "线路 1" {
		t.Fatalf("first=%+v", s[0])
	}
	if s[1].Place != "备用" || s[1].Lines != 1 {
		t.Fatalf("second server falls back to remark name: %+v", s[1])
	}
	if d := c.currentDetailLocked(); d != "日本 · 东京 · 线路 1" || strings.Contains(d, "443") {
		t.Fatalf("detail=%q", d)
	}

	c.SelectServer("203.0.113.20")
	if got := c.Snapshot().Servers; !got[1].Current || got[0].Current {
		t.Fatalf("select server failed: %+v", got)
	}
}

func TestSelectServerSkipsBlockedLine(t *testing.T) {
	c := New(Options{DataDir: t.TempDir(), Interval: -1, AllowExampleNodes: true})
	defer c.Close()
	if err := c.importText(twoServers, true, true); err != nil {
		t.Fatal(err)
	}
	c.SelectServer("203.0.113.20")
	c.mu.Lock()
	c.health[nodeKey(c.nodes[0])] = HealthBlocked
	c.mu.Unlock()
	c.SelectServer("203.0.113.10")
	if p := c.Snapshot().Port; p != 8443 {
		t.Fatalf("should pick the first line not marked blocked, port=%d", p)
	}
}
