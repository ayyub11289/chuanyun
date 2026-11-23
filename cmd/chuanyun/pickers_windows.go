//go:build windows

package main

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"chuanyun/internal/app"
	"chuanyun/internal/nodes"
	"chuanyun/internal/platform"
)

// choiceRow 是可点击的一行：左边图标，中间标题和说明，右边可放按钮或勾选标记。
type choiceRow struct {
	widget.BaseWidget
	bg       *canvas.Rectangle
	lead     fyne.CanvasObject
	title    *widget.Label
	sub      *widget.Label
	tail     fyne.CanvasObject
	onTap    func()
	selected bool
	hovered  bool
}

func newChoiceRow(lead fyne.CanvasObject, title, sub string, tail fyne.CanvasObject, onTap func()) *choiceRow {
	r := &choiceRow{lead: lead, tail: tail, onTap: onTap}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 10
	r.title = widget.NewLabel(title)
	r.title.TextStyle = fyne.TextStyle{Bold: true}
	r.title.Truncation = fyne.TextTruncateEllipsis
	r.sub = widget.NewLabel(sub)
	r.sub.SizeName = theme.SizeNameCaptionText
	r.sub.Importance = widget.LowImportance
	r.sub.Truncation = fyne.TextTruncateEllipsis
	r.ExtendBaseWidget(r)
	return r
}

func (r *choiceRow) CreateRenderer() fyne.WidgetRenderer {
	text := container.New(layout.NewCustomPaddedVBoxLayout(-14), r.title, r.sub)
	var left, right fyne.CanvasObject
	if r.lead != nil {
		left = container.NewCenter(r.lead)
	}
	if r.tail != nil {
		right = container.NewCenter(r.tail)
	}
	body := container.NewBorder(nil, nil, left, right, text)
	r.paint()
	return widget.NewSimpleRenderer(container.NewStack(r.bg, container.New(layout.NewCustomPaddedLayout(0, 0, 8, 6), body)))
}

func (r *choiceRow) setSelected(on bool) {
	r.selected = on
	r.paint()
}

func (r *choiceRow) paint() {
	switch {
	case r.selected:
		r.bg.FillColor = colSelected
		r.bg.StrokeColor = withAlpha(colPrimary, 150)
		r.bg.StrokeWidth = 1
	case r.hovered && r.onTap != nil:
		r.bg.FillColor = colCardRaised
		r.bg.StrokeWidth = 0
	default:
		r.bg.FillColor = color.Transparent
		r.bg.StrokeWidth = 0
	}
	r.bg.Refresh()
}

func (r *choiceRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *choiceRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.paint() }
func (r *choiceRow) MouseMoved(*desktop.MouseEvent) {}
func (r *choiceRow) MouseOut()                      { r.hovered = false; r.paint() }
func (r *choiceRow) Cursor() desktop.Cursor {
	if r.onTap == nil {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func iconBox(res fyne.Resource, size float32) fyne.CanvasObject {
	if res == nil {
		res = theme.ComputerIcon()
	}
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	return container.NewGridWrap(fyne.NewSize(size, size), img)
}

// importDirs 是导入时自动查找的目录和它们在界面上的名字。
func importDirs() ([]string, map[string]string) {
	names := map[string]string{}
	var dirs []string
	add := func(dir, name string) {
		if dir == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if _, ok := names[key]; ok {
			return
		}
		names[key] = name
		dirs = append(dirs, dir)
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Dir(exe), "程序目录")
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, "Desktop"), "桌面")
		add(filepath.Join(home, "OneDrive", "Desktop"), "桌面")
		add(filepath.Join(home, "OneDrive", "桌面"), "桌面")
		add(filepath.Join(home, "Downloads"), "下载")
		add(filepath.Join(home, "Documents"), "文档")
		add(filepath.Join(home, "OneDrive", "Documents"), "文档")
	}
	return dirs, names
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// openImport 自动列出桌面、下载、文档和程序目录里的节点文件，以及剪贴板里的链接；点一下就导入。
func (d *desktopUI) openImport() {
	list := container.NewVBox()
	status := widget.NewLabel("正在查找节点文件…")
	status.Wrapping = fyne.TextWrapWord
	var dlg *dialog.CustomDialog
	done := func(text string, replace bool) {
		if err := d.ctrl.ImportText(text, replace); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		dlg.Hide()
	}

	if clip := strings.TrimSpace(fyne.CurrentApp().Clipboard().Content()); clip != "" {
		if got, err := nodes.Parse(clip); err == nil && len(got) > 0 && !nodes.AllExamples(got) {
			replace := strings.HasPrefix(clip, "{") || strings.HasPrefix(clip, "[")
			sub := "点一下导入"
			if !replace {
				sub = "点一下加入现有节点"
			}
			list.Add(newChoiceRow(iconBox(theme.ContentPasteIcon(), 28),
				fmt.Sprintf("剪贴板里的 %d 个节点", len(got)), sub, nil,
				func() { done(clip, replace) }))
		}
	}

	paste := widget.NewButtonWithIcon("粘贴链接", theme.ContentPasteIcon(), func() {
		dlg.Hide()
		d.openPaste()
	})
	browse := widget.NewButtonWithIcon("其他位置", theme.FolderOpenIcon(), func() {
		dlg.Hide()
		d.browseImport()
	})
	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(420, 260))
	content := container.NewBorder(status, container.NewGridWithColumns(2, paste, browse), nil, nil, scroll)
	dlg = dialog.NewCustom("导入节点", "关闭", content, d.win)
	dlg.Resize(fyne.NewSize(480, 440))
	dlg.Show()

	go func() {
		dirs, where := importDirs()
		found := nodes.Find(dirs)
		fyne.Do(func() {
			for _, f := range found {
				f := f
				lead := theme.DocumentIcon()
				parts := []string{fmt.Sprintf("%d 个节点", f.Nodes)}
				if f.Servers > 1 {
					parts = append(parts, fmt.Sprintf("%d 台服务器", f.Servers))
				}
				if f.Subscription {
					lead = theme.ViewRefreshIcon()
					parts = append(parts, "含自动更新")
				}
				parts = append(parts, where[strings.ToLower(filepath.Dir(f.Path))], ago(f.Modified))
				list.Add(newChoiceRow(iconBox(lead, 28), filepath.Base(f.Path), strings.Join(parts, " · "), nil, func() {
					b, err := os.ReadFile(f.Path)
					if err != nil {
						dialog.ShowError(err, d.win)
						return
					}
					done(string(b), true)
				}))
			}
			switch {
			case len(list.Objects) == 0:
				status.SetText("没在桌面、下载、文档和程序目录里找到节点文件。可以把文件拖进主窗口，或复制节点链接后再点「导入」。")
			case len(found) == 0:
				status.SetText("没找到节点文件，但剪贴板里有节点。")
			default:
				status.SetText("点一下即可导入，会替换当前节点。")
			}
			list.Refresh()
		})
	}()
}

// browseImport 是找不到文件时的后备：手动选文件。
func (d *desktopUI) browseImport() {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		if rc == nil {
			return
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		if err := d.ctrl.ImportText(string(b), true); err != nil {
			dialog.ShowError(err, d.win)
		}
	}, d.win)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".json", ".txt"}))
	fd.Resize(fyne.NewSize(720, 480))
	fd.Show()
}

// loadApps 在后台读取程序列表和图标，结果缓存起来供选择器和直连列表使用。
func (d *desktopUI) loadApps(then func()) {
	go func() {
		apps := platform.ListApps()
		icons := map[string]fyne.Resource{}
		names := map[string]string{}
		for _, a := range apps {
			key := strings.ToLower(a.Exe)
			names[key] = a.Name
			if len(a.Icon) > 0 {
				icons[key] = fyne.NewStaticResource(a.Exe+".png", a.Icon)
			}
		}
		fyne.Do(func() {
			d.appList = apps
			d.appIcons = icons
			d.appNames = names
			d.appsKey = ""
			d.refreshDirectApps(d.ctrl.Snapshot())
			if then != nil {
				then()
			}
		})
	}()
}

// refreshDirectApps 用图标 + 名字显示已选的直连程序，每项可单独移除。
func (d *desktopUI) refreshDirectApps(snap app.Snapshot) {
	key := strings.Join(snap.DirectApps, "\n")
	if key == d.appsKey {
		return
	}
	d.appsKey = key
	d.appsBox.Objects = nil
	if len(snap.DirectApps) == 0 {
		empty := widget.NewLabel("还没有直连程序")
		empty.Importance = widget.LowImportance
		d.appsBox.Add(empty)
	}
	for _, exe := range snap.DirectApps {
		exe := exe
		k := strings.ToLower(exe)
		name := d.appNames[k]
		if name == "" {
			name = strings.TrimSuffix(exe, filepath.Ext(exe))
		}
		remove := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
			var keep []string
			for _, a := range d.ctrl.Snapshot().DirectApps {
				if !strings.EqualFold(a, exe) {
					keep = append(keep, a)
				}
			}
			d.ctrl.SetDirectApps(keep)
			d.refresh()
		})
		remove.Importance = widget.LowImportance
		d.appsBox.Add(newChoiceRow(iconBox(d.appIcons[k], 26), name, exe, remove, nil))
	}
	d.appsBox.Refresh()
}

// openAppPicker 用图标列出运行中和已安装的程序，勾选后点「完成」保存。
func (d *desktopUI) openAppPicker() {
	chosen := map[string]bool{}
	order := append([]string(nil), d.ctrl.Snapshot().DirectApps...)
	for _, a := range order {
		chosen[strings.ToLower(a)] = true
	}
	count := widget.NewLabel("")
	setCount := func() { count.SetText(fmt.Sprintf("已选 %d 个", len(chosen))) }
	setCount()

	type item struct {
		row  *choiceRow
		text string
	}
	var items []item
	list := container.NewVBox()
	search := widget.NewEntry()
	search.SetPlaceHolder("搜索程序，例如 微信、QQ、Steam")
	filter := func(q string) {
		q = strings.ToLower(strings.TrimSpace(q))
		for _, it := range items {
			if q == "" || strings.Contains(it.text, q) {
				it.row.Show()
			} else {
				it.row.Hide()
			}
		}
		list.Refresh()
	}
	search.OnChanged = filter
	toggle := func(exe string, row *choiceRow) {
		k := strings.ToLower(exe)
		if chosen[k] {
			delete(chosen, k)
			for i, a := range order {
				if strings.EqualFold(a, exe) {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
		} else {
			chosen[k] = true
			order = append(order, exe)
		}
		if row != nil {
			row.setSelected(chosen[k])
		}
		setCount()
	}
	fill := func() {
		list.Objects = nil
		items = nil
		for _, a := range d.appList {
			a := a
			sub := a.Exe
			if a.Running {
				sub += " · 运行中"
			}
			var row *choiceRow
			row = newChoiceRow(iconBox(d.appIcons[strings.ToLower(a.Exe)], 28), a.Name, sub, nil, func() { toggle(a.Exe, row) })
			row.setSelected(chosen[strings.ToLower(a.Exe)])
			items = append(items, item{row, strings.ToLower(a.Name + " " + a.Exe)})
			list.Add(row)
		}
		if len(items) == 0 {
			list.Add(widget.NewLabel("没有读到程序列表，可以在下面手动输入程序名。"))
		}
		filter(search.Text)
	}
	if d.appList == nil {
		list.Add(widget.NewLabel("正在读取电脑上的程序…"))
		d.loadApps(fill)
	} else {
		fill()
	}

	manual := widget.NewEntry()
	manual.SetPlaceHolder("列表里没有？输入程序名，如 Steam.exe")
	addManual := func() {
		for _, n := range app.NormalizeApps([]string{manual.Text}) {
			if !chosen[strings.ToLower(n)] {
				toggle(n, nil)
			}
		}
		manual.SetText("")
	}
	manual.OnSubmitted = func(string) { addManual() }
	addBtn := widget.NewButtonWithIcon("添加", theme.ContentAddIcon(), addManual)

	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(420, 320))
	bottom := container.NewVBox(container.NewBorder(nil, nil, nil, addBtn, manual), count)
	content := container.NewBorder(search, bottom, nil, nil, scroll)
	dlg := dialog.NewCustomConfirm("选择直连程序", "完成", "取消", content, func(ok bool) {
		if !ok {
			return
		}
		d.ctrl.SetDirectApps(order)
		d.refresh()
	}, d.win)
	dlg.Resize(fyne.NewSize(500, 560))
	dlg.Show()
	d.win.Canvas().Focus(search)
}
