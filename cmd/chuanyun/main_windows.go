//go:build windows

package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"

	"chuanyun/internal/app"
	"chuanyun/internal/core"
	"chuanyun/internal/geo"
	"chuanyun/internal/platform"
	"chuanyun/internal/probe"
	"chuanyun/internal/singbox"
	"chuanyun/internal/speed"
	"chuanyun/internal/update"
	"chuanyun/internal/version"
)

const (
	labelTUN    = "TUN 全局"
	labelSystem = "系统代理"
)

func main() {
	autostart := flag.Bool("autostart", false, "由计划任务启动")
	connectFlag := flag.Bool("connect", false, "启动后立即连接")
	updatedFlag := flag.Bool("updated", false, "由自动更新重启")
	flag.Parse()

	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	exe, _ = filepath.Abs(exe)
	exeDir := filepath.Dir(exe)
	dataDir := platform.DataDir(exeDir)

	holder := &struct{ rel func() }{}
	var already bool
	holder.rel, already = platform.AcquireSingleInstance()
	if already {
		if !platform.RequestShow() {
			platform.AlreadyRunning()
		}
		return
	}
	release := func() {
		if holder.rel != nil {
			holder.rel()
			holder.rel = nil
		}
	}
	defer release()

	// 上次没正常退出时，残留的 sing-box 会占着端口，系统代理也还指向它。
	stray := 0
	if bin, err := core.LocateSingBox(exeDir, dataDir, false); err == nil {
		stray = core.KillStray(bin)
	}
	staleProxy := platform.ClearStaleProxy(singbox.MixedPort)
	go update.CleanupOld(exe)

	var updater app.Updater
	if key, err := update.Key(); err == nil {
		updater = releaseSite{key: key}
	}

	var ctrl *app.Controller
	ctrl = app.New(app.Options{
		DataDir: dataDir,
		ExePath: exe,
		OS:      host{},
		Probes: app.Probes{
			Generate204: probe.Generate204,
			TCP:         probe.TCP,
			Domestic:    probe.Domestic,
		},
		FindSingBox: func() (string, error) {
			return core.LocateSingBox(exeDir, dataDir, true)
		},
		EnsureRuleSets: func() (string, string, error) {
			return core.EnsureRuleSets(exeDir, dataDir, true)
		},
		StartCore: core.Start,
		CoreReady: func() bool { return probe.TCP("127.0.0.1", singbox.MixedPort) },
		Download: func(ctx context.Context, proxyURL string, maxBytes int64, onProgress func(int64)) (int64, error) {
			return speed.ReadThroughProxy(ctx, proxyURL, speed.DownloadURL, maxBytes, onProgress)
		},
		TunnelRT:      speed.TunnelRoundTrip,
		TunnelTraffic: speed.FetchTunnelBytes,
		Version:       version.Version,
		Updater:       updater,
		Locate:        geo.Lookup,
		Firewall:      &platform.KillSwitch{},
		LaunchUpdate: func(newExe string, args []string) error {
			rollback, err := update.Replace(exe, newExe)
			if err != nil {
				return err
			}
			cmd := exec.Command(exe, args...)
			cmd.Dir = exeDir
			if err := cmd.Start(); err != nil {
				rollback()
				return err
			}
			_ = cmd.Process.Release()
			return nil
		},
		BeforeRelaunch: func() { release() },
		AfterRelaunchFailed: func() {
			var again bool
			holder.rel, again = platform.AcquireSingleInstance()
			if again && holder.rel != nil {
				holder.rel()
				holder.rel = nil
			}
		},
	})
	defer ctrl.Close()
	ctrl.Note("穿云 v" + version.Version + " 已启动。数据目录：" + dataDir)
	if stray > 0 {
		ctrl.Note(fmt.Sprintf("已结束上次残留的 sing-box 进程 %d 个", stray))
	}
	if staleProxy {
		ctrl.Note("上次没有正常断开，已关闭残留的系统代理")
	}
	if *updatedFlag {
		ctrl.Note("已自动更新到 v" + version.Version)
	}
	ctrl.StartUpdateLoop()
	ctrl.RefreshGeo()

	a := fyneapp.NewWithID("xyz.chuanyun.desktop")
	a.Settings().SetTheme(newTheme())
	a.SetIcon(appIcon())
	w := a.NewWindow("穿云")
	// 内容整体可滚动，窗口高度按 150% 缩放的 1080p 工作区留余量。
	w.Resize(fyne.NewSize(440, 700))
	w.SetMaster()
	w.CenterOnScreen()

	ui := newUI(ctrl, w, dataDir)
	w.SetContent(ui.root)
	ui.refresh()
	ctrl.Subscribe(func() {
		fyne.Do(func() { ui.refresh() })
	})

	quit := func() {
		if ctrl.Snapshot().Phase != app.PhaseIdle {
			ctrl.Disconnect()
		}
		a.Quit()
	}
	showWin := func() {
		w.Show()
		w.RequestFocus()
	}
	tray := setupTray(a, ctrl, showWin, quit)
	if tray {
		platform.WatchShowRequests(func() { fyne.Do(showWin) })
	}
	hintedTray := false
	w.SetCloseIntercept(func() {
		if !tray {
			quit()
			return
		}
		w.Hide()
		if !hintedTray {
			hintedTray = true
			go platform.Notify("穿云仍在运行", "已最小化到右下角托盘。要彻底退出，请右键托盘图标选「退出」。")
		}
	})
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		var chunks []string
		for _, u := range uris {
			b, err := readURI(u)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			chunks = append(chunks, string(b))
		}
		if err := ctrl.ImportText(strings.Join(chunks, "\n"), true); err != nil {
			dialog.ShowError(err, w)
		}
	})

	ctrl.SetOnExit(func() {
		fyne.Do(func() { a.Quit() })
	})

	if platform.Elevated() {
		_ = ctrl.SyncAutostart()
	}
	if *autostart && ctrl.AutoConnect() {
		ctrl.Connect()
	} else if *connectFlag {
		ctrl.Connect()
	}

	// 开机自启时只放在托盘里，不弹窗口。
	if *autostart && tray {
		a.Run()
		return
	}
	w.ShowAndRun()
}

// setupTray 建托盘菜单并随连接状态更新文字；系统不支持托盘时返回 false。
func setupTray(a fyne.App, ctrl *app.Controller, showWin, quit func()) bool {
	desk, ok := a.(desktop.App)
	if !ok {
		return false
	}
	toggle := fyne.NewMenuItem("连接", func() {
		if ctrl.Snapshot().Phase == app.PhaseConnected {
			ctrl.Disconnect()
		} else {
			ctrl.Connect()
		}
	})
	exit := fyne.NewMenuItem("退出", quit)
	exit.IsQuit = true
	menu := fyne.NewMenu("穿云",
		fyne.NewMenuItem("打开穿云", showWin),
		toggle,
		fyne.NewMenuItemSeparator(),
		exit,
	)
	desk.SetSystemTrayMenu(menu)

	lastLabel, lastTip := "", ""
	update := func() {
		snap := ctrl.Snapshot()
		label := "连接"
		switch {
		case snap.Phase == app.PhaseConnected:
			label = "断开"
		case snap.Busy:
			label = "连接中…"
		}
		if label != lastLabel {
			lastLabel = label
			toggle.Label = label
			toggle.Disabled = snap.Busy && snap.Phase != app.PhaseConnected
			menu.Refresh()
		}
		tip := "穿云 · " + snap.Status
		if snap.Phase == app.PhaseConnected && snap.HasNodes && snap.Selected < len(snap.Labels) {
			tip += "\n" + snap.Labels[snap.Selected]
		}
		if tip != lastTip {
			lastTip = tip
			systray.SetTooltip(tip)
		}
	}
	update()
	ctrl.Subscribe(func() { fyne.Do(update) })
	return true
}

// releaseSite 用内置公钥访问发布目录。
type releaseSite struct{ key ed25519.PublicKey }

func (r releaseSite) client(base, proxy string) update.Client {
	return update.Client{Base: base, Proxy: proxy, Key: r.key}
}

func (r releaseSite) Manifest(ctx context.Context, base, proxy string) (update.Manifest, error) {
	return r.client(base, proxy).Manifest(ctx)
}

func (r releaseSite) Download(ctx context.Context, base, proxy string, m update.Manifest, dst string) error {
	return r.client(base, proxy).Download(ctx, m, dst)
}

func (r releaseSite) Nodes(ctx context.Context, base, proxy string, key []byte) ([]byte, error) {
	return r.client(base, proxy).Nodes(ctx, key)
}

type host struct{}

func (host) Elevated() bool                       { return platform.Elevated() }
func (host) RelaunchElevated(args []string) error { return platform.RelaunchElevated(args) }
func (host) SetSystemProxy(port int) error        { return platform.SetSystemProxy(port) }
func (host) ClearSystemProxy() error              { return platform.ClearSystemProxy() }
func (host) InstallAutostart(exe string) error    { return platform.InstallAutostart(exe) }
func (host) RemoveAutostart() error               { return platform.RemoveAutostart() }
func (host) Notify(title, body string)            { platform.Notify(title, body) }

type desktopUI struct {
	ctrl     *app.Controller
	win      fyne.Window
	root     fyne.CanvasObject
	updating bool

	power  *powerButton
	status *canvas.Text
	detail *widget.Label
	stats  *canvas.Text

	modeTUN    *modeChip
	modeSystem *modeChip
	killSwitch *toggleRow
	unblockBtn *widget.Button

	banner    *widget.Label
	bannerBox fyne.CanvasObject

	nodeBox   *fyne.Container
	rows      []*nodeRow
	rowsKey   string
	autoRow   *choiceRow
	emptyNode *widget.Label
	diag      *widget.Label
	diagBtn   *widget.Button

	latency    *widget.Label
	measureBtn *widget.Button
	through    *widget.Label
	download   *widget.Label
	speedBtn   *widget.Button
	cancelBtn  *widget.Button

	logs      *widget.Label
	logScroll *container.Scroll
	lastLogs  string

	appsBox  *fyne.Container
	appsKey  string
	appList  []platform.App
	appIcons map[string]fyne.Resource
	appNames map[string]string

	updBox   fyne.CanvasObject
	updReady *widget.Label
	updLine  *widget.Label
	checkBtn *widget.Button

	autoStart *toggleRow
	autoConn  *toggleRow
	admin     *widget.Label
	more      *widget.Accordion
}

func sectionLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

func newUI(ctrl *app.Controller, win fyne.Window, dataDir string) *desktopUI {
	d := &desktopUI{ctrl: ctrl, win: win}

	header := brandHeader("v" + version.Version)

	d.power = newPowerButton(func() {
		if d.ctrl.Snapshot().Phase == app.PhaseConnected {
			d.ctrl.Disconnect()
			return
		}
		d.ctrl.Connect()
	})
	d.status = canvas.NewText(app.StatusIdle, colForeground)
	d.status.TextSize = 20
	d.status.TextStyle = fyne.TextStyle{Bold: true}
	d.status.Alignment = fyne.TextAlignCenter
	d.detail = widget.NewLabel("")
	d.detail.Alignment = fyne.TextAlignCenter
	d.detail.Wrapping = fyne.TextWrapWord
	d.stats = mutedText("", 13)
	d.stats.Alignment = fyne.TextAlignCenter
	hero := card(container.NewVBox(
		container.NewCenter(d.power),
		d.status,
		d.detail,
		d.stats,
	))

	d.modeTUN = newModeChip(labelTUN, func() {
		if !d.updating {
			d.ctrl.SetMode(singbox.ModeTUN)
		}
	})
	d.modeSystem = newModeChip(labelSystem, func() {
		if !d.updating {
			d.ctrl.SetMode(singbox.ModeSystem)
		}
	})
	modeBar := container.NewGridWithColumns(2, d.modeTUN, d.modeSystem)
	d.killSwitch = newToggleRow("防泄漏", "隧道断开时阻断上网，不泄漏到本地网络", false, func(on bool) {
		if !d.updating {
			d.ctrl.SetKillSwitch(on)
		}
	})

	d.banner = widget.NewLabel("")
	d.banner.Wrapping = fyne.TextWrapWord
	d.banner.Importance = widget.WarningImportance
	bannerBg := canvas.NewRectangle(withAlpha(colWarning, 28))
	bannerBg.CornerRadius = 10
	bannerBg.StrokeColor = withAlpha(colWarning, 120)
	bannerBg.StrokeWidth = 1
	d.unblockBtn = widget.NewButtonWithIcon("解除阻断", theme.CancelIcon(), func() { d.ctrl.ReleaseBlock() })
	d.bannerBox = container.NewStack(bannerBg, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6),
		container.NewBorder(nil, nil, nil, container.NewCenter(d.unblockBtn), d.banner)))

	d.updReady = widget.NewLabel("")
	d.updReady.Wrapping = fyne.TextWrapWord
	applyBtn := widget.NewButtonWithIcon("重启更新", theme.ViewRefreshIcon(), func() {
		msg := "将断开当前连接，换成新版本后自动重新打开。"
		if d.ctrl.Snapshot().Phase != app.PhaseIdle {
			msg = "将短暂断开连接，换成新版本后自动重新连接。"
		}
		dialog.ShowConfirm("更新穿云", msg, func(ok bool) {
			if ok {
				go d.ctrl.ApplyUpdate()
			}
		}, d.win)
	})
	applyBtn.Importance = widget.HighImportance
	updBg := canvas.NewRectangle(withAlpha(colPrimary, 28))
	updBg.CornerRadius = 10
	updBg.StrokeColor = withAlpha(colPrimary, 120)
	updBg.StrokeWidth = 1
	d.updBox = container.NewStack(updBg, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6),
		container.NewBorder(nil, nil, nil, container.NewCenter(applyBtn), d.updReady)))

	importBtn := widget.NewButtonWithIcon("导入", theme.ContentAddIcon(), d.openImport)
	d.diagBtn = widget.NewButtonWithIcon("检测", theme.SearchIcon(), func() { d.ctrl.Diagnose() })
	nodeHeader := container.NewBorder(nil, nil, sectionLabel("节点"), container.NewHBox(importBtn, d.diagBtn))
	d.nodeBox = container.NewVBox()
	d.emptyNode = widget.NewLabel("还没有节点。点「导入」会自动找到桌面、下载里的节点文件，也可以复制节点链接后点「导入」。")
	d.emptyNode.Wrapping = fyne.TextWrapWord
	d.diag = widget.NewLabel("")
	d.diag.Wrapping = fyne.TextWrapWord
	hint := mutedText("点一下切换服务器；线路被封会自动换到同一服务器的其他线路", 12)
	nodes := card(container.NewVBox(nodeHeader, d.nodeBox, d.emptyNode, d.diag, hint))

	d.latency = widget.NewLabel("")
	d.latency.Wrapping = fyne.TextWrapWord
	d.measureBtn = widget.NewButton("测量延迟", func() { d.ctrl.MeasureLatency() })
	d.through = widget.NewLabel("")
	d.through.Wrapping = fyne.TextWrapWord
	d.download = widget.NewLabel("")
	d.download.Wrapping = fyne.TextWrapWord
	d.speedBtn = widget.NewButton("开始下载测速", func() { d.ctrl.StartDownloadTest() })
	d.cancelBtn = widget.NewButton("取消", func() { d.ctrl.CancelDownloadTest() })
	speedBox := container.NewVBox(
		sectionLabel("延迟（经代理往返）"),
		d.latency,
		d.measureBtn,
		sectionLabel("实时吞吐（只算代理流量）"),
		d.through,
		sectionLabel("下载测速（最多 12 秒 / 8 MB）"),
		d.download,
		container.NewGridWithColumns(2, d.speedBtn, d.cancelBtn),
	)

	d.logs = widget.NewLabel("")
	d.logs.Wrapping = fyne.TextWrapWord
	d.logScroll = container.NewVScroll(d.logs)
	d.logScroll.SetMinSize(fyne.NewSize(200, 180))
	openDir := widget.NewButtonWithIcon("打开日志目录", theme.FolderIcon(), func() {
		_ = exec.Command("explorer", dataDir).Start()
	})
	logBox := container.NewBorder(nil, openDir, nil, nil, d.logScroll)

	d.autoStart = newToggleRow("开机自启", "用计划任务以最高权限启动", false, func(on bool) {
		if d.updating {
			return
		}
		if err := d.ctrl.SetAutostart(on); err != nil {
			dialog.ShowError(fmt.Errorf("设置开机自启失败：%w\n需要管理员权限。可先用 TUN 连接以提升权限，再打开。", err), d.win)
			d.refresh()
		}
	})
	d.autoConn = newToggleRow("启动后自动连接", "打开穿云后直接连上当前节点", false, func(on bool) {
		if !d.updating {
			d.ctrl.SetAutoConnect(on)
		}
	})
	d.admin = widget.NewLabel("")
	d.admin.Wrapping = fyne.TextWrapWord
	d.admin.Importance = widget.LowImportance
	d.updLine = widget.NewLabel("")
	d.updLine.Wrapping = fyne.TextWrapWord
	d.checkBtn = widget.NewButtonWithIcon("检查更新", theme.ViewRefreshIcon(), func() { d.ctrl.CheckUpdates() })
	d.checkBtn.Importance = widget.HighImportance
	footer := mutedText("sing-box "+version.SingBox+" · 穿云 "+version.Version, 12)
	settingsBox := container.NewVBox(d.autoStart, d.autoConn, d.admin,
		widget.NewSeparator(), sectionLabel("自动更新"), d.updLine, d.checkBtn, footer)

	d.appsBox = container.NewVBox()
	pickApp := widget.NewButtonWithIcon("添加程序", theme.ContentAddIcon(), d.openAppPicker)
	appsHint := widget.NewLabel("这些程序不走代理，用本机网络和国内 IP，适合银行、游戏、会检测海外 IP 的软件。TUN 模式下它们仍能看到虚拟网卡；最好再配合「系统代理」模式。")
	appsHint.Wrapping = fyne.TextWrapWord
	appsHint.Importance = widget.LowImportance
	appsBox := container.NewVBox(appsHint, d.appsBox, pickApp)
	d.refreshDirectApps(ctrl.Snapshot())
	if len(ctrl.Snapshot().DirectApps) > 0 {
		d.loadApps(nil)
	}

	d.more = widget.NewAccordion(
		widget.NewAccordionItem("直连程序", appsBox),
		widget.NewAccordionItem("网络测速", speedBox),
		widget.NewAccordionItem("日志", logBox),
		widget.NewAccordionItem("设置", settingsBox),
	)

	content := container.NewVBox(
		header,
		hero,
		modeBar,
		d.killSwitch,
		d.updBox,
		d.bannerBox,
		nodes,
		d.more,
	)
	d.root = container.NewVScroll(container.New(layout.NewCustomPaddedLayout(8, 14, 14, 14), content))
	return d
}

func (d *desktopUI) openPaste() {
	entry := widget.NewMultiLineEntry()
	entry.SetPlaceHolder("vless://… 或 hysteria2://…，一行一个；也可粘贴整段 JSON")
	entry.SetMinRowsVisible(6)
	dlg := dialog.NewCustomConfirm("粘贴节点", "导入", "取消", entry, func(ok bool) {
		if !ok {
			return
		}
		text := strings.TrimSpace(entry.Text)
		replace := strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")
		if err := d.ctrl.ImportText(text, replace); err != nil {
			dialog.ShowError(err, d.win)
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(400, 280))
	dlg.Show()
}

func (d *desktopUI) refresh() {
	d.updating = true
	defer func() { d.updating = false }()
	snap := d.ctrl.Snapshot()

	switch {
	case snap.Phase == app.PhaseConnected && snap.Status == app.StatusDegraded:
		d.power.set("断开", colWarning, false)
		d.power.setMotion(motionConnecting)
	case snap.Phase == app.PhaseConnected:
		d.power.set("断开", colSuccess, false)
		d.power.setMotion(motionConnected)
	case snap.Busy:
		d.power.set("连接中", colPrimary, true)
		d.power.setMotion(motionConnecting)
	default:
		d.power.set("连接", colPrimary, false)
		d.power.setMotion(motionNone)
	}

	d.status.Text = snap.Status
	switch snap.Status {
	case app.StatusConnected, app.StatusSwitched:
		d.status.Color = colSuccess
	case app.StatusFailed, app.StatusLocalDown:
		d.status.Color = colError
	case app.StatusDegraded:
		d.status.Color = colWarning
	default:
		d.status.Color = colForeground
	}
	d.status.Refresh()
	switch {
	case snap.Detail != "":
		d.detail.SetText(snap.Detail)
	case snap.HasNodes:
		for _, s := range snap.Servers {
			if s.Current {
				d.detail.SetText(s.Place + " · " + s.CurrentLine)
			}
		}
	default:
		d.detail.SetText("导入节点后即可连接")
	}
	var stats []string
	if snap.Phase == app.PhaseConnected {
		if snap.LatencyShort != "" {
			stats = append(stats, "延迟 "+snap.LatencyShort)
		}
		if snap.ThroughputShort != "" {
			stats = append(stats, snap.ThroughputShort)
		}
	}
	d.stats.Text = strings.Join(stats, "    ")
	d.stats.Refresh()

	d.unblockBtn.Hide()
	switch {
	case snap.Blocking:
		d.banner.SetText("防泄漏正在阻断上网：隧道已断开，流量不会漏到本地网络。点「连接」恢复，或解除阻断临时直连。")
		d.unblockBtn.Show()
		d.bannerBox.Show()
	case snap.ExampleNodes:
		d.banner.SetText("当前是示例节点（203.0.113.x 是文档测试地址），永远连不通。请导入服务器上的 /root/chuanyun-nodes.json。")
		d.bannerBox.Show()
	case snap.Alert != "":
		d.banner.SetText(snap.Alert)
		d.bannerBox.Show()
	default:
		d.bannerBox.Hide()
	}

	d.refreshNodes(snap)
	d.refreshDirectApps(snap)
	if snap.DiagBusy {
		d.diagBtn.SetText("检测中")
		d.diagBtn.Disable()
	} else {
		d.diagBtn.SetText("检测")
		if snap.HasNodes {
			d.diagBtn.Enable()
		} else {
			d.diagBtn.Disable()
		}
	}
	if len(snap.DiagLines) == 0 {
		d.diag.Hide()
	} else {
		d.diag.SetText(strings.Join(snap.DiagLines, "\n"))
		d.diag.Show()
	}

	d.modeTUN.setActive(snap.Mode != singbox.ModeSystem)
	d.modeSystem.setActive(snap.Mode == singbox.ModeSystem)
	d.killSwitch.SetOn(snap.KillSwitch)
	if snap.KillSwitchAvailable && snap.Mode == singbox.ModeTUN {
		d.killSwitch.Show()
	} else {
		d.killSwitch.Hide()
	}

	if len(snap.LatencyLines) == 0 {
		d.latency.SetText("未导入节点")
	} else {
		d.latency.SetText(strings.Join(snap.LatencyLines, "\n"))
	}
	if snap.Phase == app.PhaseConnected && !snap.LatencyBusy {
		d.measureBtn.Enable()
	} else {
		d.measureBtn.Disable()
	}
	d.through.SetText(snap.ThroughputLine)
	d.download.SetText(snap.DownloadLine)
	if snap.Phase == app.PhaseConnected && !snap.SpeedBusy {
		d.speedBtn.Enable()
	} else {
		d.speedBtn.Disable()
	}
	if snap.SpeedBusy {
		d.cancelBtn.Enable()
	} else {
		d.cancelBtn.Disable()
	}

	if snap.Logs != d.lastLogs {
		d.lastLogs = snap.Logs
		lines := strings.Split(snap.Logs, "\n")
		if len(lines) > 80 {
			lines = lines[len(lines)-80:]
		}
		d.logs.SetText(strings.Join(lines, "\n"))
		d.logScroll.ScrollToBottom()
	}

	if snap.UpdateReady != "" {
		text := "新版本 v" + snap.UpdateReady + " 已下载"
		if snap.UpdateNotes != "" {
			text += "：" + snap.UpdateNotes
		}
		d.updReady.SetText(text)
		d.updBox.Show()
	} else {
		d.updBox.Hide()
	}
	d.updLine.SetText(snap.UpdateLine)
	if snap.UpdateBusy {
		d.checkBtn.Disable()
	} else {
		d.checkBtn.Enable()
	}

	d.autoStart.SetOn(snap.Autostart)
	d.autoConn.SetOn(snap.AutoConnect)
	if snap.Elevated {
		d.admin.SetText("当前已获得管理员权限")
	} else {
		d.admin.SetText("未提升。TUN 连接时会请求管理员权限")
	}
}

func (d *desktopUI) refreshNodes(snap app.Snapshot) {
	if len(snap.Servers) == 0 {
		d.emptyNode.Show()
	} else {
		d.emptyNode.Hide()
	}
	var keys []string
	for _, s := range snap.Servers {
		keys = append(keys, s.Server)
	}
	key := strings.Join(keys, "\n")
	if key != d.rowsKey {
		d.rowsKey = key
		d.rows = nil
		d.nodeBox.Objects = nil
		d.autoRow = nil
		if len(snap.Servers) > 1 {
			d.autoRow = newChoiceRow(iconBox(theme.ViewRefreshIcon(), 26), "自动选择", "连接时选握手最快的服务器", nil, func() { d.ctrl.SetAutoPick(true) })
			d.nodeBox.Add(d.autoRow)
		}
		for _, s := range snap.Servers {
			server := s.Server
			row := newNodeRow(func() { d.ctrl.SelectServer(server) })
			d.rows = append(d.rows, row)
			d.nodeBox.Add(row)
		}
		d.nodeBox.Refresh()
	}
	for i, v := range snap.Servers {
		if i < len(d.rows) {
			d.rows[i].update(v)
		}
	}
	if d.autoRow != nil {
		d.autoRow.setSelected(snap.AutoPick)
		sub := "点这里让穿云按速度自动选服务器"
		if snap.AutoPick {
			sub = "连接时选握手最快的服务器"
		}
		if d.autoRow.sub.Text != sub {
			d.autoRow.sub.SetText(sub)
		}
	}
}

func readURI(u fyne.URI) ([]byte, error) {
	r, err := storage.Reader(u)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
