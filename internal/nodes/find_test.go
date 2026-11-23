package nodes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindSkipsExamplesAndRanksSubscriptionFirst(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	real := `{"nodes":[{"type":"vless","name":"x","server":"8.8.8.8","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"},
{"type":"hysteria2","server":"8.8.8.8","port":36712,"password":"p"}]}`
	sub := `{"update":"https://h/","update_key":"k","nodes":[{"type":"hysteria2","server":"1.1.1.1","port":1,"password":"p"}]}`
	example := `{"nodes":[{"type":"hysteria2","server":"203.0.113.10","port":1,"password":"p"}]}`
	write := func(dir, name, text string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		_ = os.Chtimes(p, ts, ts)
	}
	write(a, "chuanyun-nodes.json", real, 0)
	write(a, "示例-proxy-nodes.json", example, 0)
	write(a, "notes.txt", "hello", 0)
	write(b, "copy.json", real, time.Hour)
	write(b, "sub.json", sub, 48*time.Hour)

	got := Find([]string{a, b, a, filepath.Join(a, "missing")})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].Subscription || filepath.Base(got[0].Path) != "sub.json" {
		t.Fatalf("subscription should be first: %+v", got)
	}
	if filepath.Base(got[1].Path) != "chuanyun-nodes.json" || got[1].Nodes != 2 || got[1].Servers != 1 {
		t.Fatalf("second=%+v", got[1])
	}
}
