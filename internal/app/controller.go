// Package app 是与界面无关的客户端：导入节点、启停 sing-box、按看门狗结果切换端口。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chuanyun/internal/failover"
	"chuanyun/internal/geo"
	"chuanyun/internal/nodes"
	"chuanyun/internal/singbox"
	"chuanyun/internal/speed"
)

// Phase 是连接状态。
type Phase string

const (
	PhaseIdle       Phase = "idle"
	PhaseConnecting Phase = "connecting"
	PhaseConnected  Phase = "connected"
)

// 状态文字。界面按这些值着色。
const (
	StatusIdle       = "未连接"
	StatusConnecting = "正在连接…"
	StatusConnected  = "已连接"
	StatusSwitched   = "已切换"
	StatusFailed     = "连接失败"
	StatusDegraded   = "连接异常"
	StatusLocalDown  = "本地网络异常"
)

const (
	logFileLimit = 2 << 20
	coreTailSize = 8
)

// ErrExampleNodes 表示导入的是示例文件里的占位节点。
var ErrExampleNodes = errors.New("这是示例节点：地址是文档专用的测试网段（如 203.0.113.x），永远连不通。请导入服务器生成的 chuanyun-nodes.json（在 VPS 的 /root/ 下）")

// Settings 保存在数据目录。
type Settings struct {
	Mode        string `json:"mode"`
	Autostart   bool   `json:"autostart"`
	AutoConnect bool   `json:"auto_connect"`
	Selected    int    `json:"selected"`
	// DirectApps 是不走代理的程序文件名。
	DirectApps []string `json:"direct_apps,omitempty"`
	// UpdateURL 是发布目录，NodesKey 解密服务器上的节点，NodesHash 记录上次同步的内容。
	UpdateURL string `json:"update_url,omitempty"`
	NodesKey  string `json:"nodes_key,omitempty"`
	NodesHash string `json:"nodes_hash,omitempty"`
	// ManualPick 为真表示用户手动选了服务器，连接时不再按速度自动选。
	ManualPick bool `json:"manual_pick,omitempty"`
	// KillSwitch 为真时 TUN 连接期间用防火墙阻断隧道以外的出站。
	KillSwitch bool `json:"kill_switch,omitempty"`
}

// OS 是平台相关能力，测试里用假实现。
type OS interface {
	Elevated() bool
	RelaunchElevated(args []string) error
	SetSystemProxy(port int) error
	ClearSystemProxy() error
	InstallAutostart(exe string) error
	RemoveAutostart() error
	Notify(title, body string)
}

// Probes 是可替换的探测函数。
type Probes struct {
	Generate204 func(proxyURL string) bool
	TCP         func(host string, port int) bool
	Domestic    func() failover.Domestic
}

// Options 组装控制器。
type Options struct {
	DataDir string
	ExePath string
	// Interval 是正常时的探测间隔；FastInterval 是失败后的复查间隔；Grace 是连接或切换后的第一次探测延迟。
	Interval     time.Duration
	FastInterval time.Duration
	Grace        time.Duration
	OS           OS
	Probes       Probes
	FindSingBox  func() (string, error)
	// EnsureRuleSets 返回规则集路径，必要时下载。
	EnsureRuleSets func() (geoSite, geoIP string, err error)
	// StartCore 启动 sing-box。进程不是被 stop 结束时必须调用 onExit。
	StartCore func(singboxPath, configPath string, onLine func(string), onExit func(error)) (stop func(), err error)
	// CoreReady 报告混合端口是否已在监听。nil 时启动后不等待。
	CoreReady func() bool
	// AllowExampleNodes 只给测试用：允许导入文档测试地址的节点。
	AllowExampleNodes   bool
	OnExit              func()
	BeforeRelaunch      func()
	AfterRelaunchFailed func()
	Now                 func() time.Time
	// Download 经当前代理下载，必须自己停在 maxBytes。nil 表示测速不可用。
	Download func(ctx context.Context, proxyURL string, maxBytes int64, onProgress func(int64)) (int64, error)
	// TunnelRT 是经代理的短请求。nil 表示延迟不可用。不要把它实现成 TCP 握手。
	TunnelRT func(ctx context.Context, proxyURL string) error
	// TunnelTraffic 返回本隧道累计上下行字节。nil 或出错时界面显示不可用。
	TunnelTraffic func(ctx context.Context, baseURL, secret string) (upload, download int64, err error)
	// Version 是当前程序版本。
	Version string
	// Updater 访问发布目录；nil 表示这个版本没有内置发布公钥。
	Updater Updater
	// LaunchUpdate 用新 exe 替换自己并启动它；出错时必须已经回滚。
	LaunchUpdate func(newExe string, args []string) error
	// Locate 查询服务器位置。nil 时节点列表只显示备注名。
	Locate func(ctx context.Context, host, proxy string) (geo.Info, error)
	// Firewall 实现防泄漏。nil 时不支持。
	Firewall Firewall
}

// HealthKind 是某个节点最近一次的检测结论。
type HealthKind string

const (
	HealthUnknown    HealthKind = ""
	HealthOK         HealthKind = "ok"
	HealthReachable  HealthKind = "reachable"
	HealthBlocked    HealthKind = "blocked"
	HealthWalled     HealthKind = "walled"
	HealthInterfered HealthKind = "interfered"
	HealthFailed     HealthKind = "failed"
	HealthUDP        HealthKind = "udp"
)

// Text 是节点列表里的短标签。
func (h HealthKind) Text() string {
	switch h {
	case HealthOK:
		return "正常"
	case HealthReachable:
		return "可用"
	case HealthBlocked:
		return "被封"
	case HealthWalled:
		return "不通"
	case HealthInterfered:
		return "被干扰"
	case HealthFailed:
		return "代理失败"
	case HealthUDP:
		return "待连接"
	default:
		return "未检测"
	}
}

// Good 为真时界面用成功色。
func (h HealthKind) Good() bool { return h == HealthOK || h == HealthReachable }

// Bad 为真时界面用错误色。
func (h HealthKind) Bad() bool {
	return h == HealthBlocked || h == HealthWalled || h == HealthInterfered || h == HealthFailed
}

// NodeView 是节点列表的一行。
type NodeView struct {
	Label   string
	Name    string
	Proto   string
	Server  string
	Port    int
	Current bool
	Health  HealthKind
	Latency string
}

// Snapshot 是界面读取的一份状态。
type Snapshot struct {
	Phase       Phase
	Status      string
	Detail      string
	Mode        string
	DirectApps  []string
	Elevated    bool
	Autostart   bool
	AutoConnect bool
	Logs        string
	Labels      []string
	Nodes       []NodeView
	Servers     []ServerView
	AutoPick    bool
	// KillSwitch 是用户设置；Blocking 表示隧道已断开但防泄漏仍在阻断上网。
	KillSwitch          bool
	KillSwitchAvailable bool
	Blocking            bool
	Selected            int
	Server              string
	Port                int
	Proto               string
	Busy                bool
	HasNodes            bool
	ExampleNodes        bool
	Alert               string
	DiagBusy            bool
	DiagLines           []string
	LatencyLines        []string
	LatencyShort        string
	DownloadLine        string
	ThroughputLine      string
	ThroughputShort     string
	SpeedBusy           bool
	LatencyBusy         bool
	UpdateLine          string
	UpdateBusy          bool
	// UpdateReady 是已下载、等待重启的新版本号。
	UpdateReady string
	UpdateNotes string
}

// Controller 驱动连接与看门狗。
type Controller struct {
	opts           Options
	mu             sync.Mutex
	coreMu         sync.Mutex
	probeMu        sync.Mutex
	settings       Settings
	nodes          []nodes.Node
	index          int
	phase          Phase
	status         string
	detail         string
	alert          string
	logs           []string
	fail           failover.State
	stop           func()
	cancel         func()
	quickCheck     bool
	logFile        *os.File
	listen         []func()
	holdWatchdog   bool
	latency        map[string]speed.Latency
	health         map[string]HealthKind
	latencyBusy    bool
	diagBusy       bool
	diagLines      []string
	speedCancel    context.CancelFunc
	speedBusy      bool
	downloadLine   string
	throughputLine string
	throughShort   string
	trafficCancel  context.CancelFunc
	clashSecret    string
	coreGen        int
	coreStarting   bool
	coreTail       []string
	lastCrash      time.Time
	updBusy        bool
	updLine        string
	updReady       *readyUpdate
	updChecked     time.Time
	updNotified    string
	geo            map[string]geo.Info
	geoBusy        bool
}

const idleDownloadLine = "未开始。下载测速只在点击后运行，经当前代理，不会自动开始。"

// New 加载已保存的节点和设置。
func New(opts Options) *Controller {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval == 0 {
		opts.Interval = 60 * time.Second
	}
	if opts.FastInterval == 0 {
		opts.FastInterval = 15 * time.Second
	}
	if opts.Grace == 0 {
		opts.Grace = 15 * time.Second
	}
	_ = os.MkdirAll(opts.DataDir, 0o755)
	c := &Controller{
		opts:           opts,
		phase:          PhaseIdle,
		status:         StatusIdle,
		latency:        map[string]speed.Latency{},
		health:         map[string]HealthKind{},
		downloadLine:   idleDownloadLine,
		throughputLine: "不可用",
		clashSecret:    newSecret(),
		settings: Settings{
			Mode:        singbox.ModeTUN,
			AutoConnect: true,
		},
	}
	c.load()
	c.loadGeo()
	logPath := filepath.Join(opts.DataDir, "chuanyun.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > logFileLimit {
		_ = os.Rename(logPath, logPath+".1")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		c.logFile = f
	}
	return c
}

// SetOnExit 在成功请求提权后调用，用来关掉当前窗口。
func (c *Controller) SetOnExit(fn func()) {
	c.mu.Lock()
	c.opts.OnExit = fn
	c.mu.Unlock()
}

// Note 写一条用户可见的日志。
func (c *Controller) Note(line string) {
	c.log(line)
	c.emit()
}

// Close 释放日志文件。
func (c *Controller) Close() {
	if c.logFile != nil {
		_ = c.logFile.Close()
	}
}

// Subscribe 在状态变化时被调用。
func (c *Controller) Subscribe(fn func()) {
	c.mu.Lock()
	c.listen = append(c.listen, fn)
	c.mu.Unlock()
}

// Snapshot 复制当前界面状态。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{
		Phase:           c.phase,
		Status:          c.status,
		Detail:          c.detail,
		Mode:            c.settings.Mode,
		DirectApps:      append([]string(nil), c.settings.DirectApps...),
		UpdateLine:      c.updateLineLocked(),
		UpdateBusy:      c.updBusy,
		Elevated:        c.opts.OS != nil && c.opts.OS.Elevated(),
		Autostart:       c.settings.Autostart,
		AutoConnect:     c.settings.AutoConnect,
		Logs:            strings.Join(c.logs, "\n"),
		Selected:        c.index,
		Busy:            c.phase == PhaseConnecting,
		HasNodes:        len(c.nodes) > 0,
		ExampleNodes:    nodes.AllExamples(c.nodes),
		Alert:           c.alert,
		DiagBusy:        c.diagBusy,
		DiagLines:       append([]string(nil), c.diagLines...),
		DownloadLine:    c.downloadLine,
		ThroughputLine:  c.throughputLine,
		ThroughputShort: c.throughShort,
		SpeedBusy:       c.speedBusy,
		LatencyBusy:     c.latencyBusy,
	}
	healthy := c.phase == PhaseConnected && (c.status == StatusConnected || c.status == StatusSwitched)
	for i, n := range c.nodes {
		key := nodeKey(n)
		lat := c.latency[key]
		s.Labels = append(s.Labels, n.Label())
		s.LatencyLines = append(s.LatencyLines, speed.FormatLatency(c.placeLocked(n.Server)+" · "+c.lineLabelLocked(i), lat))
		h := c.health[key]
		if h == HealthUnknown && n.Type == "hysteria2" {
			h = HealthUDP
		}
		cur := i == c.index
		if cur && healthy && !h.Bad() {
			h = HealthOK
		}
		s.Nodes = append(s.Nodes, NodeView{
			Label: n.Label(), Name: n.Name, Proto: protoName(n), Server: n.Server, Port: n.Port,
			Current: cur, Health: h, Latency: shortLatency(lat),
		})
	}
	if len(c.nodes) > 0 && c.index >= 0 && c.index < len(c.nodes) {
		n := c.nodes[c.index]
		s.Server = n.Server
		s.Port = n.Port
		s.Proto = n.Type
		s.LatencyShort = shortLatency(c.latency[nodeKey(n)])
	}
	s.Servers = c.serversLocked(s.Nodes)
	s.AutoPick = !c.settings.ManualPick
	s.KillSwitch = c.settings.KillSwitch
	s.KillSwitchAvailable = c.opts.Firewall != nil
	s.Blocking = c.blockingLocked()
	if c.updReady != nil {
		s.UpdateReady = c.updReady.Version
		s.UpdateNotes = c.updReady.Notes
	}
	return s
}

// SetMode 切换 TUN / 系统代理。已连接时会按新模式重连。
func (c *Controller) SetMode(mode string) {
	if mode != singbox.ModeSystem {
		mode = singbox.ModeTUN
	}
	c.mu.Lock()
	if c.settings.Mode == mode {
		c.mu.Unlock()
		return
	}
	c.settings.Mode = mode
	connected := c.phase == PhaseConnected
	c.mu.Unlock()
	c.saveSettings()
	c.log("代理模式已改为 " + modeLabel(mode))
	if connected {
		c.Disconnect()
		c.Connect()
	} else {
		c.ReleaseBlock()
	}
	c.emit()
}

// SetAutostart 保存偏好，并由调用方在有权限时同步计划任务。
func (c *Controller) SetAutostart(on bool) error {
	c.mu.Lock()
	c.settings.Autostart = on
	exe := c.opts.ExePath
	c.mu.Unlock()
	c.saveSettings()
	if c.opts.OS == nil {
		c.emit()
		return nil
	}
	var err error
	if on {
		err = c.opts.OS.InstallAutostart(exe)
	} else {
		err = c.opts.OS.RemoveAutostart()
	}
	if err != nil {
		c.mu.Lock()
		c.settings.Autostart = !on
		c.mu.Unlock()
		c.saveSettings()
		c.log("开机自启设置失败：" + err.Error())
	} else if on {
		c.log("已创建开机自启计划任务（最高权限）")
	} else {
		c.log("已取消开机自启")
	}
	c.emit()
	return err
}

// SetAutoConnect 记住启动后是否自动连接。
func (c *Controller) SetAutoConnect(on bool) {
	c.mu.Lock()
	c.settings.AutoConnect = on
	c.mu.Unlock()
	c.saveSettings()
	c.emit()
}

// NormalizeApps 把用户输入整理成去重的程序文件名：允许整条路径、可省略 .exe，忽略空行。
func NormalizeApps(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		l = strings.Trim(strings.TrimSpace(l), `"`)
		if i := strings.LastIndexAny(l, `\/`); i >= 0 {
			l = l[i+1:]
		}
		if l == "" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(l), ".exe") {
			l += ".exe"
		}
		if strings.EqualFold(l, "Chuanyun.exe") || strings.EqualFold(l, "sing-box.exe") {
			continue
		}
		key := strings.ToLower(l)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	return out
}

// SetDirectApps 保存直连程序列表。已连接时立即重连生效。
func (c *Controller) SetDirectApps(lines []string) {
	apps := NormalizeApps(lines)
	c.mu.Lock()
	if strings.EqualFold(strings.Join(apps, "\n"), strings.Join(c.settings.DirectApps, "\n")) {
		c.mu.Unlock()
		return
	}
	c.settings.DirectApps = apps
	connected := c.phase == PhaseConnected
	c.mu.Unlock()
	c.saveSettings()
	if len(apps) == 0 {
		c.log("已清空直连程序")
	} else {
		c.log("直连程序：" + strings.Join(apps, "、"))
	}
	c.emit()
	if connected {
		go c.restart("直连程序已更新，已重新连接", StatusConnected)
	}
}

// SyncAutostart 按已保存的设置安装或删除计划任务。
func (c *Controller) SyncAutostart() error {
	c.mu.Lock()
	on := c.settings.Autostart
	exe := c.opts.ExePath
	c.mu.Unlock()
	if c.opts.OS == nil {
		return nil
	}
	if on {
		return c.opts.OS.InstallAutostart(exe)
	}
	return c.opts.OS.RemoveAutostart()
}

// AutoConnect 是否在开机启动时连接。
func (c *Controller) AutoConnect() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.AutoConnect && len(c.nodes) > 0
}

// ImportText 导入 JSON 或分享链接。replace 为真时覆盖原有节点。已连接时用新列表重连。
// JSON 里带 update / update_key 时同时开启自动更新。
func (c *Controller) ImportText(text string, replace bool) error {
	return c.importText(text, replace, false)
}

func (c *Controller) importText(text string, replace, fromServer bool) error {
	list, err := nodes.Parse(text)
	if err != nil {
		return err
	}
	skipped := 0
	if !c.opts.AllowExampleNodes {
		if nodes.AllExamples(list) {
			return ErrExampleNodes
		}
		kept := list[:0:0]
		for _, n := range list {
			if nodes.IsExample(n) {
				skipped++
				continue
			}
			kept = append(kept, n)
		}
		list = kept
	}
	c.mu.Lock()
	if replace || len(c.nodes) == 0 {
		c.nodes = list
		c.index = 0
		c.health = map[string]HealthKind{}
		c.diagLines = nil
		c.alert = ""
	} else {
		cur := ""
		if c.index >= 0 && c.index < len(c.nodes) {
			cur = nodeKey(c.nodes[c.index])
		}
		c.nodes = nodes.Normalize(append(append([]nodes.Node{}, c.nodes...), list...))
		c.index = 0
		for i, n := range c.nodes {
			if nodeKey(n) == cur {
				c.index = i
			}
		}
	}
	c.settings.Selected = c.index
	c.fail = failover.State{Index: c.index, LastIPWall: c.fail.LastIPWall}
	saved := append([]nodes.Node(nil), c.nodes...)
	total := len(saved)
	connected := c.phase == PhaseConnected
	base, key := subscriptionFrom(text)
	subChanged := false
	if base != "" && base != c.settings.UpdateURL {
		c.settings.UpdateURL = base
		subChanged = true
	}
	if key != "" && key != c.settings.NodesKey {
		c.settings.NodesKey = key
		subChanged = true
	}
	if !fromServer {
		// 手动导入后让下一次同步重新以服务器为准。
		c.settings.NodesHash = ""
	}
	c.mu.Unlock()
	c.saveNodes(saved)
	c.saveSettings()
	msg := fmt.Sprintf("已导入 %d 个节点，当前共 %d 个", len(list), total)
	if skipped > 0 {
		msg += fmt.Sprintf("（跳过 %d 个示例节点）", skipped)
	}
	c.log(msg)
	go c.refreshGeo()
	if subChanged {
		c.log("已开启自动更新：" + base)
		if !fromServer {
			go c.checkUpdates(false)
		}
	}
	c.emit()
	if connected {
		go c.restart("节点已更新，已按新列表重新连接", StatusConnected)
	}
	return nil
}

// Select 选择当前节点。连接中会重启到该节点。
func (c *Controller) Select(i int) {
	c.mu.Lock()
	if i < 0 || i >= len(c.nodes) || i == c.index {
		c.mu.Unlock()
		return
	}
	c.index = i
	c.settings.Selected = i
	c.fail = failover.State{Index: i, LastIPWall: c.fail.LastIPWall}
	label := c.nodes[i].Label()
	connected := c.phase == PhaseConnected
	c.mu.Unlock()
	c.saveSettings()
	c.emit()
	if connected {
		go c.restart("已切换到 "+label, StatusConnected)
	}
}

// Connect 在后台连接。TUN 且未提权时会拉起管理员进程并请求退出。
func (c *Controller) Connect() {
	go c.connect()
}

func (c *Controller) connect() {
	c.mu.Lock()
	if c.phase == PhaseConnected || c.phase == PhaseConnecting {
		c.mu.Unlock()
		return
	}
	if len(c.nodes) == 0 {
		c.status = StatusIdle
		c.detail = "请先导入节点"
		c.mu.Unlock()
		c.log("尚未导入节点")
		c.emit()
		return
	}
	mode := c.settings.Mode
	c.phase = PhaseConnecting
	c.status = StatusConnecting
	c.detail = ""
	c.mu.Unlock()
	c.emit()

	if mode == singbox.ModeTUN && c.opts.OS != nil && !c.opts.OS.Elevated() {
		c.log("TUN 模式需要管理员权限，正在请求提升…")
		if c.opts.BeforeRelaunch != nil {
			c.opts.BeforeRelaunch()
		}
		if err := c.opts.OS.RelaunchElevated([]string{"--connect"}); err != nil {
			if c.opts.AfterRelaunchFailed != nil {
				c.opts.AfterRelaunchFailed()
			}
			c.failConnect("未能获取管理员权限")
			return
		}
		c.log("请在管理员窗口中继续")
		if c.opts.OnExit != nil {
			c.opts.OnExit()
		}
		return
	}

	c.pickLine()
	c.coreMu.Lock()
	err := c.startCoreLocked()
	if err != nil {
		// 第一次就没连上时不阻断：用户从没进入过受保护状态，断网只会让人困惑。
		c.releaseFirewall()
	}
	c.coreMu.Unlock()
	if err != nil {
		c.failConnect(err.Error())
		return
	}
	c.mu.Lock()
	c.phase = PhaseConnected
	c.fail = failover.State{Index: c.index, LastIPWall: c.fail.LastIPWall}
	c.status = StatusConnected
	c.detail = c.currentDetailLocked()
	c.lastCrash = time.Time{}
	detail := c.detail
	c.mu.Unlock()
	c.log("已连接 " + detail)
	c.startLoop()
	c.startTraffic()
	c.emit()
	go c.refreshGeo()
}

// Disconnect 停止 sing-box，并在系统代理模式下取消代理。
func (c *Controller) Disconnect() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	mode := c.settings.Mode
	c.phase = PhaseIdle
	c.status = StatusIdle
	c.detail = ""
	c.mu.Unlock()

	c.coreMu.Lock()
	c.stopCore()
	c.releaseFirewall()
	c.coreMu.Unlock()
	if mode == singbox.ModeSystem && c.opts.OS != nil {
		_ = c.opts.OS.ClearSystemProxy()
	}
	c.stopExtras()
	c.mu.Lock()
	c.throughputLine = "不可用"
	c.throughShort = ""
	if !c.speedBusy {
		c.downloadLine = idleDownloadLine
	}
	c.mu.Unlock()
	c.log("已断开")
	c.emit()
}

func proxyURL() string { return fmt.Sprintf("http://127.0.0.1:%d", singbox.MixedPort) }

// Tick 执行一轮看门狗。未连接时什么也不做。
func (c *Controller) Tick() {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	c.mu.Lock()
	if c.holdWatchdog || c.phase != PhaseConnected || len(c.nodes) == 0 || c.opts.Probes.Generate204 == nil {
		c.mu.Unlock()
		return
	}
	st := c.fail
	st.Index = c.index
	list := append([]nodes.Node(nil), c.nodes...)
	gen := c.coreGen
	c.mu.Unlock()

	start := time.Now()
	probe := failover.Probe{Generate204OK: c.opts.Probes.Generate204(proxyURL())}
	rtt := time.Since(start)
	var domestic failover.Domestic
	if !probe.Generate204OK && st.ConsecutiveFailures+1 >= failover.FailureThreshold && c.opts.Probes.Domestic != nil {
		domestic = c.opts.Probes.Domestic()
		if domestic.Up() {
			probe.TCP = c.probeTCP(list)
		}
	}
	newSt, dec := failover.Step(c.opts.Now(), c.labelledRefs(list), st, probe, domestic)

	c.mu.Lock()
	if c.phase != PhaseConnected || c.coreGen != gen {
		c.mu.Unlock()
		return
	}
	cur := list[st.Index]
	c.fail = newSt
	c.index = newSt.Index
	c.settings.Selected = newSt.Index
	if probe.TCP != nil {
		c.applyTCPLocked(list, probe.TCP)
	}
	switch dec.Action {
	case failover.ActionHealthy:
		c.latency[nodeKey(cur)] = speed.Latency{Status: speed.LatencyOK, RTT: rtt, At: c.opts.Now()}
		c.health[nodeKey(cur)] = HealthOK
		c.alert = ""
		c.status = StatusConnected
		c.detail = c.currentDetailLocked()
	case failover.ActionFailing:
		c.status = StatusDegraded
		c.detail = dec.Log
	case failover.ActionLocalDown:
		c.status = StatusLocalDown
		c.detail = dec.Log
	case failover.ActionSwitch, failover.ActionRetry:
		c.health[nodeKey(cur)] = causeHealth(dec.Cause)
		c.quickCheck = true
		if dec.WalledServer != "" {
			c.alert = fmt.Sprintf("%s 的所有线路都连不上，服务器 IP 疑似被墙。请在 VPS 控制台更换 IP，再推送新节点。", c.placeLocked(dec.WalledServer))
		}
	}
	healthy := dec.Action == failover.ActionHealthy
	c.mu.Unlock()

	if !healthy || dec.Log != "generate_204 正常" {
		c.log(dec.Log)
	}
	switch dec.Action {
	case failover.ActionSwitch:
		c.saveSettings()
		c.restart(dec.Log, StatusSwitched)
		if dec.Toast != "" && c.opts.OS != nil {
			c.opts.OS.Notify("穿云", dec.Toast)
		}
	case failover.ActionRetry:
		c.restart("重启当前节点重试", StatusDegraded)
	}
	if dec.IPWall {
		if c.opts.OS != nil {
			c.opts.OS.Notify("穿云", failover.ToastIPWall)
		}
		c.log(failover.ToastIPWall)
		// 服务器可能已经换了 IP，节点同步能拿到新地址。
		go c.checkUpdatesIfStale(10 * time.Minute)
	}
	c.emit()
}

// probeTCP 并行直连所有 TCP 节点的端口。UDP 节点不探测。
func (c *Controller) probeTCP(list []nodes.Node) failover.TCPResult {
	out := failover.TCPResult{}
	if c.opts.Probes.TCP == nil {
		return out
	}
	type target struct {
		host string
		port int
	}
	results := map[target]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range list {
		if n.Type == "hysteria2" {
			continue
		}
		t := target{n.Server, n.Port}
		mu.Lock()
		_, dup := results[t]
		results[t] = false
		mu.Unlock()
		if dup {
			continue
		}
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			ok := c.opts.Probes.TCP(t.host, t.port)
			mu.Lock()
			results[t] = ok
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	for i, n := range list {
		if n.Type == "hysteria2" {
			continue
		}
		out[i] = results[target{n.Server, n.Port}]
	}
	return out
}

func (c *Controller) applyTCPLocked(list []nodes.Node, tcp failover.TCPResult) {
	walled := failover.Walled(refsOf(list), tcp)
	for i, n := range list {
		key := nodeKey(n)
		if n.Type == "hysteria2" {
			if c.health[key] == HealthUnknown {
				c.health[key] = HealthUDP
			}
			continue
		}
		ok, known := tcp[i]
		switch {
		case !known:
		case walled[n.Server]:
			c.health[key] = HealthWalled
		case !ok:
			c.health[key] = HealthBlocked
		case c.health[key] != HealthOK && c.health[key] != HealthInterfered:
			c.health[key] = HealthReachable
		}
	}
}

func causeHealth(cause failover.Cause) HealthKind {
	switch cause {
	case failover.CausePortBlocked:
		return HealthBlocked
	case failover.CauseIPWall:
		return HealthWalled
	case failover.CauseInterfered:
		return HealthInterfered
	default:
		return HealthFailed
	}
}

// Diagnose 手动检测：国内网络、所有 TCP 端口直连，已连接时再经代理访问一次。
func (c *Controller) Diagnose() {
	go c.diagnose()
}

func (c *Controller) diagnose() {
	c.mu.Lock()
	if c.diagBusy || len(c.nodes) == 0 {
		c.mu.Unlock()
		return
	}
	c.diagBusy = true
	c.diagLines = []string{"正在检测所有端口…"}
	list := append([]nodes.Node(nil), c.nodes...)
	c.mu.Unlock()
	c.emit()

	c.probeMu.Lock()
	var lines []string
	var tcp failover.TCPResult
	domestic := failover.Domestic{Checked: true, BaiduOK: true}
	if c.opts.Probes.Domestic != nil {
		domestic = c.opts.Probes.Domestic()
	}
	if !domestic.Up() {
		lines = append(lines, "本机网络不通（百度和 223.5.5.5 都连不上），先检查本地网络，无法判断服务器状态")
	} else {
		tcp = c.probeTCP(list)
		lines = append(lines, failover.Summarize(c.labelledRefs(list), tcp)...)
	}
	c.mu.Lock()
	connected := c.phase == PhaseConnected
	c.mu.Unlock()
	if connected && domestic.Up() && c.opts.Probes.Generate204 != nil {
		if c.opts.Probes.Generate204(proxyURL()) {
			lines = append(lines, "当前节点经代理访问正常")
		} else {
			lines = append(lines, "当前节点经代理访问失败")
		}
	}
	c.probeMu.Unlock()

	c.mu.Lock()
	if tcp != nil {
		c.applyTCPLocked(list, tcp)
		for s := range failover.Walled(refsOf(list), tcp) {
			c.alert = fmt.Sprintf("%s 的所有线路都连不上，服务器 IP 疑似被墙。请在 VPS 控制台更换 IP，再推送新节点。", c.placeLocked(s))
		}
	}
	c.diagLines = lines
	c.diagBusy = false
	c.mu.Unlock()
	c.log("检测结果：" + strings.Join(lines, "；"))
	c.emit()
}

func (c *Controller) restart(reason, status string) {
	c.coreMu.Lock()
	c.mu.Lock()
	active := c.phase == PhaseConnected
	c.mu.Unlock()
	if !active {
		c.coreMu.Unlock()
		return
	}
	c.stopCore()
	err := c.startCoreLocked()
	if err == nil {
		c.mu.Lock()
		active = c.phase == PhaseConnected
		c.mu.Unlock()
		if !active {
			c.stopCore()
		}
	}
	c.coreMu.Unlock()
	if err != nil {
		c.failConnect(err.Error())
		return
	}
	if !active {
		return
	}
	c.mu.Lock()
	c.status = status
	c.detail = c.currentDetailLocked()
	c.mu.Unlock()
	c.log(reason)
	c.emit()
}

func (c *Controller) startCoreLocked() error {
	c.mu.Lock()
	if len(c.nodes) == 0 {
		c.mu.Unlock()
		return fmt.Errorf("没有节点")
	}
	if c.index < 0 || c.index >= len(c.nodes) {
		c.index = 0
	}
	node := c.nodes[c.index]
	mode := c.settings.Mode
	directApps := append([]string(nil), c.settings.DirectApps...)
	c.mu.Unlock()

	if c.opts.FindSingBox == nil || c.opts.StartCore == nil {
		return fmt.Errorf("sing-box 未配置")
	}
	bin, err := c.opts.FindSingBox()
	if err != nil {
		return err
	}
	c.engageLocked(mode, bin)
	var site, ip string
	if c.opts.EnsureRuleSets != nil {
		site, ip, err = c.opts.EnsureRuleSets()
		if err != nil {
			c.log("规则集不可用，改用内置中国域名直连：" + err.Error())
			site, ip = "", ""
		}
	}
	raw, err := singbox.Build(singbox.Input{
		Node:        node,
		MixedPort:   singbox.MixedPort,
		Mode:        mode,
		GeoSitePath: site,
		GeoIPPath:   ip,
		ExePath:     c.opts.ExePath,
		ClashSecret: c.clashSecret,
		DirectApps:  directApps,
	})
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(c.opts.DataDir, "sing-box.json")
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		return err
	}

	c.mu.Lock()
	c.coreGen++
	gen := c.coreGen
	c.coreStarting = true
	c.coreTail = nil
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.coreStarting = false
		c.mu.Unlock()
	}()
	exited := make(chan struct{})
	var exitOnce sync.Once
	stop, err := c.opts.StartCore(bin, cfgPath, c.coreLine, func(err error) {
		exitOnce.Do(func() { close(exited) })
		go c.coreExited(gen, err)
	})
	if err != nil {
		return fmt.Errorf("启动 sing-box 失败：%w", err)
	}
	if c.opts.CoreReady != nil {
		deadline := time.Now().Add(8 * time.Second)
		for !c.opts.CoreReady() {
			select {
			case <-exited:
				stop()
				return fmt.Errorf("sing-box 启动后立即退出：%s", c.tail())
			case <-time.After(150 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				stop()
				return fmt.Errorf("sing-box 8 秒内没有开始监听 127.0.0.1:%d（端口可能被占用）：%s", singbox.MixedPort, c.tail())
			}
		}
	}
	c.mu.Lock()
	c.stop = stop
	c.mu.Unlock()
	if mode == singbox.ModeSystem && c.opts.OS != nil {
		if err := c.opts.OS.SetSystemProxy(singbox.MixedPort); err != nil {
			c.log("设置系统代理失败：" + err.Error())
		} else {
			c.log(fmt.Sprintf("系统代理已设为 127.0.0.1:%d", singbox.MixedPort))
		}
	}
	return nil
}

// coreLine 只写文件，不进界面日志；最近几行留着用于说明启动失败或崩溃的原因。
func (c *Controller) coreLine(line string) {
	c.mu.Lock()
	c.coreTail = append(c.coreTail, line)
	if len(c.coreTail) > coreTailSize {
		c.coreTail = c.coreTail[len(c.coreTail)-coreTailSize:]
	}
	c.mu.Unlock()
	c.writeFile("sing-box " + line)
}

func (c *Controller) tail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.coreTail) == 0 {
		return "没有输出"
	}
	return strings.Join(c.coreTail, " | ")
}

func (c *Controller) coreExited(gen int, err error) {
	c.mu.Lock()
	if gen != c.coreGen || c.phase != PhaseConnected || c.coreStarting {
		c.mu.Unlock()
		return
	}
	now := c.opts.Now()
	again := !c.lastCrash.IsZero() && now.Sub(c.lastCrash) < time.Minute
	c.lastCrash = now
	c.stop = nil
	c.mu.Unlock()
	reason := "进程结束"
	if err != nil {
		reason = err.Error()
	}
	c.log("sing-box 意外退出（" + reason + "）：" + c.tail())
	if again {
		c.failConnect("sing-box 一分钟内反复退出，已停止连接。详见日志")
		return
	}
	c.restart("sing-box 已自动重启", StatusConnected)
}

func (c *Controller) stopCore() {
	c.mu.Lock()
	stop := c.stop
	c.stop = nil
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (c *Controller) startLoop() {
	if c.opts.Interval <= 0 {
		return
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	stopped := make(chan struct{})
	c.cancel = func() { close(stopped) }
	grace := c.opts.Grace
	c.mu.Unlock()
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-timer.C:
				c.Tick()
				timer.Reset(c.nextDelay())
			}
		}
	}()
}

// nextDelay：刚切换过用宽限期复查，失败中用快速间隔，正常时用常规间隔。
func (c *Controller) nextDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quickCheck {
		c.quickCheck = false
		return c.opts.Grace
	}
	if c.fail.ConsecutiveFailures > 0 {
		return c.opts.FastInterval
	}
	return c.opts.Interval
}

func (c *Controller) failConnect(msg string) {
	c.coreMu.Lock()
	c.stopCore()
	c.coreMu.Unlock()
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	mode := c.settings.Mode
	c.phase = PhaseIdle
	c.status = StatusFailed
	c.detail = msg
	c.throughputLine = "不可用"
	c.throughShort = ""
	c.mu.Unlock()
	if mode == singbox.ModeSystem && c.opts.OS != nil {
		_ = c.opts.OS.ClearSystemProxy()
	}
	c.stopExtras()
	c.log(msg)
	c.emit()
}

func (c *Controller) currentDetailLocked() string {
	if c.index < 0 || c.index >= len(c.nodes) {
		return ""
	}
	return c.placeLocked(c.nodes[c.index].Server) + " · " + c.lineLabelLocked(c.index)
}

func protoName(n nodes.Node) string {
	if n.Type == "hysteria2" {
		return "Hysteria2"
	}
	return "Reality"
}

func (c *Controller) log(line string) {
	full := c.opts.Now().Format("15:04:05") + "  " + line
	c.mu.Lock()
	c.logs = append(c.logs, full)
	if len(c.logs) > 200 {
		c.logs = c.logs[len(c.logs)-200:]
	}
	c.mu.Unlock()
	c.writeFile(line)
}

func (c *Controller) writeFile(line string) {
	c.mu.Lock()
	f := c.logFile
	c.mu.Unlock()
	if f != nil {
		_, _ = fmt.Fprintln(f, c.opts.Now().Format("15:04:05")+"  "+line)
	}
}

func (c *Controller) emit() {
	c.mu.Lock()
	ls := append([]func(){}, c.listen...)
	c.mu.Unlock()
	for _, fn := range ls {
		go fn()
	}
}

func (c *Controller) load() {
	if b, err := os.ReadFile(filepath.Join(c.opts.DataDir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &c.settings)
	}
	if c.settings.Mode != singbox.ModeSystem {
		c.settings.Mode = singbox.ModeTUN
	}
	var nf struct {
		Nodes []nodes.Node `json:"nodes"`
	}
	if b, err := os.ReadFile(filepath.Join(c.opts.DataDir, "nodes.json")); err == nil {
		_ = json.Unmarshal(b, &nf)
		c.nodes = nodes.Normalize(nf.Nodes)
	}
	if c.settings.Selected >= 0 && c.settings.Selected < len(c.nodes) {
		c.index = c.settings.Selected
	}
}

func (c *Controller) saveSettings() {
	c.mu.Lock()
	b, err := json.MarshalIndent(c.settings, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.opts.DataDir, "settings.json"), b, 0o600)
}

func (c *Controller) saveNodes(list []nodes.Node) {
	body, err := json.MarshalIndent(map[string]any{"version": 1, "nodes": list}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.opts.DataDir, "nodes.json"), body, 0o600)
}

// MeasureLatency 只测当前已连接节点的隧道往返，不会去连其他节点，也不使用 TCP 握手时间。
func (c *Controller) MeasureLatency() {
	go c.measureLatency()
}

func (c *Controller) measureLatency() {
	c.mu.Lock()
	if c.latencyBusy {
		c.mu.Unlock()
		return
	}
	if c.phase != PhaseConnected || c.index < 0 || c.index >= len(c.nodes) {
		c.mu.Unlock()
		c.log("未连接，不能测量隧道延迟")
		c.emit()
		return
	}
	n := c.nodes[c.index]
	key := nodeKey(n)
	if c.opts.TunnelRT == nil {
		c.latency[key] = speed.Latency{Status: speed.LatencyUnavailable, Detail: "未配置", At: c.opts.Now()}
		c.mu.Unlock()
		c.emit()
		return
	}
	c.latencyBusy = true
	c.holdWatchdog = true
	c.latency[key] = speed.Latency{Status: speed.LatencyMeasuring, At: c.opts.Now()}
	c.mu.Unlock()
	c.emit()

	result := speed.MeasureTunnel(context.Background(), 8*time.Second, func(ctx context.Context) error {
		return c.opts.TunnelRT(ctx, proxyURL())
	}, c.opts.Now)
	c.mu.Lock()
	c.latency[key] = result
	c.latencyBusy = false
	c.holdWatchdog = c.speedBusy
	c.mu.Unlock()
	c.log(speed.FormatLatency(n.Label(), result))
	c.emit()
}

// StartDownloadTest 手动开始一次经代理的下载测速。未连接或已在测速时不会另起一次。
func (c *Controller) StartDownloadTest() {
	c.mu.Lock()
	if c.speedBusy {
		c.mu.Unlock()
		return
	}
	if c.phase != PhaseConnected {
		c.downloadLine = "不可用：未连接，不能测代理下载"
		c.mu.Unlock()
		c.emit()
		return
	}
	if c.opts.Download == nil {
		c.downloadLine = "不可用：未配置经代理的下载测速"
		c.mu.Unlock()
		c.emit()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.speedCancel = cancel
	c.speedBusy = true
	c.holdWatchdog = true
	c.downloadLine = "正在测速…已接收 " + speed.FormatBytes(0)
	c.mu.Unlock()
	c.log("开始手动下载测速（经当前代理）。测速期间不自动切换节点。")
	c.emit()
	go c.finishDownload(ctx)
}

// CancelDownloadTest 取消正在进行的手动测速。
func (c *Controller) CancelDownloadTest() {
	c.mu.Lock()
	cancel := c.speedCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Controller) finishDownload(ctx context.Context) {
	res := speed.RunDownload(ctx, speed.DefaultMaxDuration, speed.DefaultMaxBytes, func(ctx context.Context, maxBytes int64) (int64, error) {
		return c.opts.Download(ctx, proxyURL(), maxBytes, func(n int64) {
			c.mu.Lock()
			c.downloadLine = "正在测速…已接收 " + speed.FormatBytes(n)
			c.mu.Unlock()
			c.emit()
		})
	})
	line := formatDownload(res)
	c.mu.Lock()
	c.downloadLine = line
	c.speedBusy = false
	c.speedCancel = nil
	c.holdWatchdog = c.latencyBusy
	c.mu.Unlock()
	c.log(line)
	c.emit()
}

func (c *Controller) startTraffic() {
	c.stopTraffic()
	if c.opts.TunnelTraffic == nil {
		c.mu.Lock()
		c.throughputLine = "不可用（未配置核心流量接口）"
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.trafficCancel = cancel
	c.throughputLine = "等待采样"
	c.throughShort = ""
	secret := c.clashSecret
	c.mu.Unlock()
	base := fmt.Sprintf("http://127.0.0.1:%d", singbox.ClashPort)
	go func() {
		var counter speed.Counter
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				up, down, err := c.opts.TunnelTraffic(ctx, base, secret)
				c.mu.Lock()
				if err != nil {
					c.throughputLine = "不可用（核心未返回本隧道流量）"
					c.throughShort = ""
				} else if u, d, ok := counter.Observe(up, down, c.opts.Now()); !ok {
					c.throughputLine = "等待采样"
					c.throughShort = ""
				} else {
					c.throughputLine = speed.FormatThroughput(u, d)
					if u.OK && d.OK {
						c.throughShort = "↓ " + speed.FormatMbps(d.BitsPerSecond) + "   ↑ " + speed.FormatMbps(u.BitsPerSecond)
					}
				}
				c.mu.Unlock()
				c.emit()
			}
		}
	}()
}

func (c *Controller) stopTraffic() {
	c.mu.Lock()
	cancel := c.trafficCancel
	c.trafficCancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Controller) stopExtras() {
	c.CancelDownloadTest()
	c.stopTraffic()
}

func formatDownload(res speed.DownloadResult) string {
	base := "已接收 " + speed.FormatBytes(res.Bytes)
	if res.Elapsed > 0 {
		base += "，用时 " + res.Elapsed.Round(time.Millisecond).String()
	}
	rate := speed.FormatPair(res.Rate)
	switch res.Outcome {
	case speed.OutcomeByteCap:
		return "达到流量上限。" + base + "，约 " + rate + "。这是经代理的近似值。"
	case speed.OutcomeTimeCap:
		return "达到时间上限。" + base + "，约 " + rate + "。这是经代理的近似值。"
	case speed.OutcomeCancelled:
		return "已取消。" + base + "，约 " + rate + "。"
	case speed.OutcomeError:
		msg := "未知错误"
		if res.Err != nil {
			msg = res.Err.Error()
		}
		return "测速失败：" + msg + "。" + base + "。"
	case speed.OutcomeOK:
		return "测速结束。" + base + "，约 " + rate + "。这是经代理的近似值。"
	default:
		return "不可用"
	}
}

func shortLatency(v speed.Latency) string {
	switch v.Status {
	case speed.LatencyOK:
		return fmt.Sprintf("%d ms", v.RTT.Milliseconds())
	case speed.LatencyMeasuring:
		return "测量中"
	case speed.LatencyTimeout:
		return "超时"
	case speed.LatencyError:
		return "失败"
	default:
		return ""
	}
}

func nodeKey(n nodes.Node) string {
	return fmt.Sprintf("%s|%s|%d", n.Type, n.Server, n.Port)
}

func newSecret() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "chuanyun-local"
	}
	return hex.EncodeToString(buf)
}

func refsOf(list []nodes.Node) []failover.NodeRef {
	out := make([]failover.NodeRef, len(list))
	for i, n := range list {
		out[i] = failover.NodeRef{Server: n.Server, Port: n.Port, Protocol: n.Type}
	}
	return out
}

func modeLabel(mode string) string {
	if mode == singbox.ModeSystem {
		return "系统代理"
	}
	return "TUN 全局"
}
