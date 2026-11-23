package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chuanyun/internal/failover"
	"chuanyun/internal/singbox"
	"chuanyun/internal/speed"
)

type fakeOS struct {
	mu       sync.Mutex
	elevated bool
	proxy    int
	cleared  int
	toasts   []string
	relaunch []string
	tasks    int
}

func (f *fakeOS) Elevated() bool { return f.elevated }
func (f *fakeOS) RelaunchElevated(args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relaunch = append(f.relaunch, args...)
	return nil
}
func (f *fakeOS) SetSystemProxy(port int) error {
	f.mu.Lock()
	f.proxy = port
	f.mu.Unlock()
	return nil
}
func (f *fakeOS) ClearSystemProxy() error {
	f.mu.Lock()
	f.cleared++
	f.mu.Unlock()
	return nil
}
func (f *fakeOS) InstallAutostart(string) error {
	f.mu.Lock()
	f.tasks++
	f.mu.Unlock()
	return nil
}
func (f *fakeOS) RemoveAutostart() error { return nil }
func (f *fakeOS) Notify(_, body string) {
	f.mu.Lock()
	f.toasts = append(f.toasts, body)
	f.mu.Unlock()
}
func (f *fakeOS) toastList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.toasts...)
}

func noCore(string, string, func(string), func(error)) (func(), error) { return func() {}, nil }

const bundle = `{"server":"203.0.113.10","uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k","short_id":"ab","ports":[443,8443,2053],"hysteria2_port":36712,"hysteria2_password":"pw"}`

func TestConnectSystemProxyAndImport(t *testing.T) {
	dir := t.TempDir()
	osFake := &fakeOS{elevated: true}
	var configs []string
	c := New(Options{
		DataDir:           dir,
		ExePath:           filepath.Join(dir, "Chuanyun.exe"),
		Interval:          -1,
		OS:                osFake,
		AllowExampleNodes: true,
		Probes:            Probes{Generate204: func(string) bool { return true }, TCP: func(string, int) bool { return true }, Domestic: func() failover.Domestic { return failover.Domestic{Checked: true, BaiduOK: true} }},
		FindSingBox:       func() (string, error) { return "sing-box", nil },
		EnsureRuleSets:    func() (string, string, error) { return "/rs/geosite-cn.srs", "/rs/geoip-cn.srs", nil },
		StartCore: func(_, cfg string, _ func(string), _ func(error)) (func(), error) {
			b, _ := os.ReadFile(cfg)
			configs = append(configs, string(b))
			return func() {}, nil
		},
	})
	defer c.Close()
	if err := c.ImportText(`{"server":"203.0.113.10","uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","publicKey":"Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI","shortId":"a1b2c3d4","ports":[443,8443],"hysteria2":{"port":36712,"password":"pw","insecure":true}}`, true); err != nil {
		t.Fatal(err)
	}
	c.SetMode(singbox.ModeSystem)
	c.connect()
	snap := c.Snapshot()
	if snap.Phase != PhaseConnected || snap.Port != 443 {
		t.Fatalf("snap=%+v", snap)
	}
	if osFake.proxy != singbox.MixedPort {
		t.Fatalf("proxy=%d", osFake.proxy)
	}
	if len(configs) != 1 || !strings.Contains(configs[0], "geosite-cn") {
		t.Fatalf("config missing ruleset: %s", configs)
	}
	c.Disconnect()
	if osFake.cleared != 1 {
		t.Fatalf("cleared=%d", osFake.cleared)
	}
}

func TestRejectsExampleNodes(t *testing.T) {
	c := New(Options{DataDir: t.TempDir(), Interval: -1})
	defer c.Close()
	if err := c.ImportText(bundle, true); !errors.Is(err, ErrExampleNodes) {
		t.Fatalf("err=%v", err)
	}
	if c.Snapshot().HasNodes {
		t.Fatal("example nodes must not be saved")
	}
}

func TestTUNRequestsElevation(t *testing.T) {
	dir := t.TempDir()
	osFake := &fakeOS{}
	exited := false
	c := New(Options{
		DataDir: dir, ExePath: "Chuanyun.exe", Interval: -1, OS: osFake, AllowExampleNodes: true,
		OnExit:      func() { exited = true },
		FindSingBox: func() (string, error) { return "sb", nil },
		StartCore: func(string, string, func(string), func(error)) (func(), error) {
			t.Fatal("should not start core")
			return nil, nil
		},
	})
	defer c.Close()
	if err := c.ImportText("vless://b7e2c1a0-1234-4abc-8def-0123456789ab@203.0.113.10:443?security=reality&pbk=Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI&sid=ab&sni=www.microsoft.com#R", true); err != nil {
		t.Fatal(err)
	}
	c.connect()
	if len(osFake.relaunch) != 1 || osFake.relaunch[0] != "--connect" || !exited {
		t.Fatalf("relaunch=%v exited=%v", osFake.relaunch, exited)
	}
}

// watchdog 构造一个已连接的控制器。tcp 决定每个端口直连是否成功。
func watchdog(t *testing.T, tcp map[int]bool, starts *int32) (*Controller, *fakeOS) {
	t.Helper()
	osFake := &fakeOS{elevated: true}
	var mu sync.Mutex
	// 连接时的选线握手全部放行，让这些用例从 443 开始考验看门狗。
	var connected int32
	c := New(Options{
		DataDir: t.TempDir(), ExePath: "Chuanyun.exe", Interval: -1, OS: osFake, AllowExampleNodes: true,
		Now: func() time.Time { return time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC) },
		Probes: Probes{
			Generate204: func(string) bool { return false },
			TCP: func(_ string, port int) bool {
				if atomic.LoadInt32(&connected) == 0 {
					return true
				}
				mu.Lock()
				defer mu.Unlock()
				return tcp[port]
			},
			Domestic: func() failover.Domestic { return failover.Domestic{Checked: true, BaiduOK: true} },
		},
		FindSingBox:    func() (string, error) { return "sb", nil },
		EnsureRuleSets: func() (string, string, error) { return "", "", fmt.Errorf("offline") },
		StartCore: func(string, string, func(string), func(error)) (func(), error) {
			atomic.AddInt32(starts, 1)
			return func() {}, nil
		},
	})
	t.Cleanup(c.Close)
	if err := c.ImportText(bundle, true); err != nil {
		t.Fatal(err)
	}
	c.connect()
	atomic.StoreInt32(&connected, 1)
	return c, osFake
}

func TestWatchdogSkipsBlockedPorts(t *testing.T) {
	var starts int32
	c, osFake := watchdog(t, map[int]bool{443: false, 8443: false, 2053: true}, &starts)
	for i := 0; i < failover.FailureThreshold; i++ {
		c.Tick()
	}
	if c.Snapshot().Port != 2053 || atomic.LoadInt32(&starts) != 2 {
		t.Fatalf("port=%d starts=%d", c.Snapshot().Port, atomic.LoadInt32(&starts))
	}
	toasts := osFake.toastList()
	if len(toasts) != 1 || toasts[0] != "线路 1 疑似被封，已自动切换到 线路 3" {
		t.Fatalf("toasts=%v", toasts)
	}
	snap := c.Snapshot()
	if snap.Nodes[0].Health != HealthBlocked || snap.Nodes[1].Health != HealthBlocked {
		t.Fatalf("nodes=%+v", snap.Nodes)
	}
}

func TestWatchdogIPWallFallsBackToHysteria(t *testing.T) {
	var starts int32
	c, osFake := watchdog(t, map[int]bool{}, &starts)
	for i := 0; i < failover.FailureThreshold; i++ {
		c.Tick()
	}
	snap := c.Snapshot()
	if snap.Port != 36712 {
		t.Fatalf("port=%d", snap.Port)
	}
	found := false
	for _, m := range osFake.toastList() {
		if m == failover.ToastIPWall {
			found = true
		}
	}
	if !found || !strings.Contains(snap.Alert, "IP 疑似被墙") {
		t.Fatalf("toasts=%v alert=%q", osFake.toastList(), snap.Alert)
	}
	if snap.Nodes[0].Health != HealthWalled {
		t.Fatalf("health=%v", snap.Nodes[0].Health)
	}
}

func TestDiagnoseReportsPorts(t *testing.T) {
	var starts int32
	c, _ := watchdog(t, map[int]bool{443: false, 8443: true, 2053: true}, &starts)
	c.diagnose()
	snap := c.Snapshot()
	if len(snap.DiagLines) == 0 || !strings.Contains(snap.DiagLines[0], "线路 1 疑似被封") || strings.Contains(strings.Join(snap.DiagLines, ""), "443") {
		t.Fatalf("diag=%q", snap.DiagLines)
	}
	if snap.Nodes[0].Health != HealthBlocked || snap.Nodes[1].Health != HealthReachable {
		t.Fatalf("nodes=%+v", snap.Nodes)
	}
}

func TestImportWhileConnectedRestarts(t *testing.T) {
	var starts int32
	c, _ := watchdog(t, map[int]bool{}, &starts)
	if err := c.ImportText(`{"type":"vless","server":"203.0.113.20","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"}`, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.Snapshot().Server != "203.0.113.20" {
		time.Sleep(10 * time.Millisecond)
	}
	for time.Now().Before(deadline) && atomic.LoadInt32(&starts) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&starts) != 2 {
		t.Fatalf("starts=%d", atomic.LoadInt32(&starts))
	}
}

func TestNormalizeApps(t *testing.T) {
	got := NormalizeApps([]string{
		` "C:\Program Files\Tencent\WeChat\WeChat.exe" `,
		"wechat.exe",
		"QQ",
		"",
		"Chuanyun.exe",
		"D:/games/LeagueClient.EXE",
	})
	want := []string{"WeChat.exe", "QQ.exe", "LeagueClient.EXE"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDirectAppsReachConfigAndRestart(t *testing.T) {
	var starts int32
	c, _ := watchdog(t, map[int]bool{}, &starts)
	c.SetDirectApps([]string{"WeChat"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&starts) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&starts) != 2 {
		t.Fatalf("starts=%d", atomic.LoadInt32(&starts))
	}
	raw, err := os.ReadFile(filepath.Join(c.opts.DataDir, "sing-box.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `WeChat\\.exe`) {
		t.Fatalf("config missing direct app:\n%s", raw)
	}
	if got := c.Snapshot().DirectApps; len(got) != 1 || got[0] != "WeChat.exe" {
		t.Fatalf("snapshot=%v", got)
	}
	c.SetDirectApps([]string{"wechat.exe"})
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&starts) != 2 {
		t.Fatalf("unchanged list (case only) must not restart, starts=%d", atomic.LoadInt32(&starts))
	}
}

func TestCoreCrashRestartsOnceThenStops(t *testing.T) {
	var mu sync.Mutex
	var exits []func(error)
	c := New(Options{
		DataDir: t.TempDir(), ExePath: "Chuanyun.exe", Interval: -1, OS: &fakeOS{elevated: true}, AllowExampleNodes: true,
		FindSingBox: func() (string, error) { return "sb", nil },
		StartCore: func(_, _ string, _ func(string), onExit func(error)) (func(), error) {
			mu.Lock()
			exits = append(exits, onExit)
			mu.Unlock()
			return func() {}, nil
		},
	})
	defer c.Close()
	if err := c.ImportText(bundle, true); err != nil {
		t.Fatal(err)
	}
	c.connect()
	crash := func(n int) {
		mu.Lock()
		fn := exits[len(exits)-1]
		mu.Unlock()
		fn(errors.New("exit status 1"))
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			got := len(exits)
			mu.Unlock()
			if got >= n || c.Snapshot().Phase != PhaseConnected {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	crash(2)
	if c.Snapshot().Phase != PhaseConnected {
		t.Fatalf("first crash should restart, snap=%+v", c.Snapshot())
	}
	crash(3)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.Snapshot().Phase == PhaseConnected {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Snapshot().Status != StatusFailed {
		t.Fatalf("second crash within a minute should stop, status=%s", c.Snapshot().Status)
	}
}

func TestLocalOutageDoesNotSwitch(t *testing.T) {
	dir := t.TempDir()
	osFake := &fakeOS{elevated: true}
	c := New(Options{
		DataDir: dir, ExePath: "Chuanyun.exe", Interval: -1, OS: osFake, AllowExampleNodes: true,
		Probes: Probes{
			Generate204: func(string) bool { return false },
			TCP:         func(string, int) bool { return false },
			Domestic:    func() failover.Domestic { return failover.Domestic{Checked: true} },
		},
		FindSingBox: func() (string, error) { return "sb", nil },
		StartCore:   noCore,
	})
	defer c.Close()
	if err := c.ImportText(`{"type":"vless","server":"203.0.113.10","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"}`, true); err != nil {
		t.Fatal(err)
	}
	if err := c.ImportText("hysteria2://pw@203.0.113.10:8443?insecure=1", false); err != nil {
		t.Fatal(err)
	}
	c.connect()
	for i := 0; i < 3; i++ {
		c.Tick()
	}
	if c.Snapshot().Port != 443 {
		t.Fatalf("should stay, port=%d status=%s", c.Snapshot().Port, c.Snapshot().Status)
	}
	if c.Snapshot().Status != StatusLocalDown {
		t.Fatalf("status=%s", c.Snapshot().Status)
	}
	if len(osFake.toastList()) != 0 {
		t.Fatalf("toasts=%v", osFake.toastList())
	}
}

func TestDownloadIsManualBoundedAndPausesFailover(t *testing.T) {
	dir := t.TempDir()
	osFake := &fakeOS{elevated: true}
	c := New(Options{
		DataDir: dir, ExePath: "Chuanyun.exe", Interval: -1, OS: osFake, AllowExampleNodes: true,
		Probes: Probes{
			Generate204: func(string) bool { return false },
			TCP:         func(string, int) bool { return false },
			Domestic:    func() failover.Domestic { return failover.Domestic{Checked: true, BaiduOK: true} },
		},
		FindSingBox: func() (string, error) { return "sb", nil },
		StartCore:   noCore,
	})
	defer c.Close()
	if err := c.ImportText(`{"type":"vless","server":"203.0.113.10","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"}`, true); err != nil {
		t.Fatal(err)
	}
	c.StartDownloadTest()
	if !strings.Contains(c.Snapshot().DownloadLine, "不可用") {
		t.Fatalf("not connected: %s", c.Snapshot().DownloadLine)
	}
	started := make(chan struct{})
	c.opts.Download = func(ctx context.Context, proxy string, max int64, on func(int64)) (int64, error) {
		if proxy != "http://127.0.0.1:2080" || max != speed.DefaultMaxBytes {
			t.Errorf("proxy=%s max=%d", proxy, max)
		}
		on(1000)
		close(started)
		<-ctx.Done()
		return 1000, ctx.Err()
	}
	c.connect()
	c.StartDownloadTest()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("download did not start")
	}
	for i := 0; i < 3; i++ {
		c.Tick()
	}
	if c.Snapshot().Port != 443 || len(osFake.toastList()) != 0 {
		t.Fatalf("watchdog moved during speed test port=%d toasts=%v", c.Snapshot().Port, osFake.toastList())
	}
	c.CancelDownloadTest()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := c.Snapshot()
		if !snap.SpeedBusy && strings.Contains(snap.DownloadLine, "已取消") {
			if !strings.Contains(snap.DownloadLine, "1000 字节") {
				t.Fatalf("line=%s", snap.DownloadLine)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not cancelled: %s", c.Snapshot().DownloadLine)
}

func TestLatencyUsesTunnelRoundTripNotTCP(t *testing.T) {
	dir := t.TempDir()
	c := New(Options{
		DataDir: dir, ExePath: "Chuanyun.exe", Interval: -1, OS: &fakeOS{elevated: true}, AllowExampleNodes: true,
		FindSingBox: func() (string, error) { return "sb", nil },
		StartCore:   noCore,
		TunnelRT: func(ctx context.Context, proxy string) error {
			if proxy != "http://127.0.0.1:2080" {
				t.Errorf("proxy=%s", proxy)
			}
			return nil
		},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	defer c.Close()
	if err := c.ImportText(`{"type":"vless","server":"203.0.113.10","port":443,"uuid":"b7e2c1a0-1234-4abc-8def-0123456789ab","public_key":"k"}`, true); err != nil {
		t.Fatal(err)
	}
	if err := c.ImportText("hysteria2://pw@203.0.113.10:8443?insecure=1", false); err != nil {
		t.Fatal(err)
	}
	c.connect()
	c.MeasureLatency()
	deadline := time.Now().Add(2 * time.Second)
	var snap Snapshot
	for time.Now().Before(deadline) {
		snap = c.Snapshot()
		if !snap.LatencyBusy && len(snap.LatencyLines) == 2 && strings.Contains(snap.LatencyLines[0], "不是 TCP 握手") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(snap.LatencyLines[0], "不是 TCP 握手") {
		t.Fatalf("lines=%v", snap.LatencyLines)
	}
	if !strings.Contains(snap.LatencyLines[1], "未测量") {
		t.Fatalf("other node must stay unmeasured: %v", snap.LatencyLines)
	}
}
