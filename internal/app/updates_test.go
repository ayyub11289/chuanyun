package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"chuanyun/internal/update"
)

type fakeUpdater struct {
	mu        sync.Mutex
	manifest  update.Manifest
	nodes     string
	downloads int
	proxies   []string
	failProxy bool
}

func (f *fakeUpdater) note(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proxies = append(f.proxies, p)
	if f.failProxy && p != "" {
		return fmt.Errorf("tunnel down")
	}
	return nil
}

func (f *fakeUpdater) Manifest(_ context.Context, _, p string) (update.Manifest, error) {
	if err := f.note(p); err != nil {
		return update.Manifest{}, err
	}
	return f.manifest, nil
}

func (f *fakeUpdater) Download(_ context.Context, _, p string, m update.Manifest, dst string) error {
	if err := f.note(p); err != nil {
		return err
	}
	f.mu.Lock()
	f.downloads++
	f.mu.Unlock()
	return os.WriteFile(dst, []byte("MZ "+m.Version), 0o644)
}

func (f *fakeUpdater) Nodes(_ context.Context, _, p string, _ []byte) ([]byte, error) {
	if err := f.note(p); err != nil {
		return nil, err
	}
	return []byte(f.nodes), nil
}

var testNodesKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func subscribed(server, url string) string {
	return fmt.Sprintf(`{"server":%q,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","publicKey":"k","ports":[443],"update":%q,"update_key":%q}`, server, url, testNodesKey)
}

func updaterController(t *testing.T, up *fakeUpdater) (*Controller, *fakeOS) {
	t.Helper()
	osFake := &fakeOS{elevated: true}
	c := New(Options{
		DataDir: t.TempDir(), ExePath: "Chuanyun.exe", Interval: -1, OS: osFake, AllowExampleNodes: true,
		Version: "1.4.0", Updater: up,
	})
	t.Cleanup(c.Close)
	return c, osFake
}

func TestImportEnablesUpdatesAndSyncsNodes(t *testing.T) {
	up := &fakeUpdater{manifest: update.Manifest{Version: "1.4.0", File: "Chuanyun-1.4.0.exe"}}
	up.nodes = subscribed("203.0.113.77", "http://198.18.0.9:8088/new/")
	c, _ := updaterController(t, up)
	if err := c.importText(subscribed("203.0.113.10", "http://198.18.0.1:8088/abc"), true, true); err != nil {
		t.Fatal(err)
	}
	if c.settings.UpdateURL != "http://198.18.0.1:8088/abc/" || c.settings.NodesKey != testNodesKey {
		t.Fatalf("settings=%+v", c.settings)
	}

	c.checkUpdates(true)
	if got := c.Snapshot().Server; got != "203.0.113.77" {
		t.Fatalf("nodes not synced, server=%s", got)
	}
	if c.settings.UpdateURL != "http://198.18.0.9:8088/new/" {
		t.Fatalf("server payload must be able to move the update URL, got %s", c.settings.UpdateURL)
	}
	if !strings.Contains(c.Snapshot().UpdateLine, "已是最新版本") {
		t.Fatalf("line=%s", c.Snapshot().UpdateLine)
	}

	hash := c.settings.NodesHash
	c.Select(0)
	c.checkUpdates(true)
	if c.settings.NodesHash != hash || !strings.Contains(c.Snapshot().UpdateLine, "节点已是最新") {
		t.Fatalf("unchanged payload must not reimport: %s", c.Snapshot().UpdateLine)
	}
}

func TestNewVersionDownloadsOnceAndApplies(t *testing.T) {
	up := &fakeUpdater{manifest: update.Manifest{Version: "1.5.0", File: "Chuanyun-1.5.0.exe", Notes: "修复"}}
	c, osFake := updaterController(t, up)
	if err := c.importText(subscribed("203.0.113.10", "http://198.18.0.1:8088/abc/"), true, true); err != nil {
		t.Fatal(err)
	}
	c.checkUpdates(true)
	snap := c.Snapshot()
	if snap.UpdateReady != "1.5.0" || snap.UpdateNotes != "修复" {
		t.Fatalf("snap=%+v", snap)
	}
	if len(osFake.toastList()) == 0 {
		t.Fatal("ready update must notify")
	}

	var launched string
	var args []string
	exited := false
	c.opts.LaunchUpdate = func(p string, a []string) error { launched, args = p, a; return nil }
	c.SetOnExit(func() { exited = true })
	c.ApplyUpdate()
	if !strings.HasSuffix(launched, "Chuanyun-1.5.0.exe") || !exited {
		t.Fatalf("launched=%q exited=%v", launched, exited)
	}
	if strings.Join(args, " ") != "--updated" {
		t.Fatalf("idle update must not auto-connect, args=%v", args)
	}
}

func TestApplyUpdateFailureKeepsRunning(t *testing.T) {
	up := &fakeUpdater{manifest: update.Manifest{Version: "1.5.0", File: "Chuanyun-1.5.0.exe"}}
	c, _ := updaterController(t, up)
	_ = c.importText(subscribed("203.0.113.10", "http://198.18.0.1:8088/abc/"), true, true)
	c.checkUpdates(true)
	exited := false
	c.SetOnExit(func() { exited = true })
	c.opts.LaunchUpdate = func(string, []string) error { return fmt.Errorf("目录不可写") }
	c.ApplyUpdate()
	if exited || !strings.Contains(c.Snapshot().UpdateLine, "目录不可写") {
		t.Fatalf("exited=%v line=%s", exited, c.Snapshot().UpdateLine)
	}
}

func TestOlderVersionIgnored(t *testing.T) {
	up := &fakeUpdater{manifest: update.Manifest{Version: "1.3.0", File: "Chuanyun-1.3.0.exe"}}
	c, _ := updaterController(t, up)
	_ = c.importText(subscribed("203.0.113.10", "http://198.18.0.1:8088/abc/"), true, true)
	c.checkUpdates(true)
	if up.downloads != 0 || c.Snapshot().UpdateReady != "" {
		t.Fatalf("downloads=%d", up.downloads)
	}
}

func TestUpdatesFallBackToDirect(t *testing.T) {
	err := viaTunnelFirst("http://127.0.0.1:2080", func(p string) error {
		if p != "" {
			return fmt.Errorf("down")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoUpdaterExplains(t *testing.T) {
	c := New(Options{DataDir: t.TempDir(), Interval: -1})
	defer c.Close()
	if !strings.Contains(c.Snapshot().UpdateLine, "没有内置发布公钥") {
		t.Fatal(c.Snapshot().UpdateLine)
	}
}
