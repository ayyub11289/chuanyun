//go:build windows

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// toggleRow 是科技风开关行：大轨道 + 滑块，开/关一眼能分清。
type toggleRow struct {
	widget.BaseWidget
	bg      *canvas.Rectangle
	track   *canvas.Rectangle
	knob    *canvas.Circle
	title   *canvas.Text
	sub     *canvas.Text
	on      bool
	hovered bool
	changed func(bool)
}

func newToggleRow(title, sub string, on bool, changed func(bool)) *toggleRow {
	r := &toggleRow{on: on, changed: changed}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 12
	r.track = canvas.NewRectangle(colCheckFill)
	r.track.CornerRadius = 13
	r.track.StrokeWidth = 1.5
	r.knob = canvas.NewCircle(colForeground)
	r.title = canvas.NewText(title, colForeground)
	r.title.TextSize = 14
	r.title.TextStyle = fyne.TextStyle{Bold: true}
	r.sub = mutedText(sub, 11)
	r.ExtendBaseWidget(r)
	r.paint()
	return r
}

type toggleRenderer struct {
	r    *toggleRow
	objs []fyne.CanvasObject
}

func (r *toggleRow) CreateRenderer() fyne.WidgetRenderer {
	return &toggleRenderer{r: r, objs: []fyne.CanvasObject{r.bg, r.title, r.sub, r.track, r.knob}}
}

func (t *toggleRenderer) Layout(size fyne.Size) {
	r := t.r
	r.bg.Resize(size)
	pad := float32(12)
	r.title.Move(fyne.NewPos(pad, 10))
	r.title.Resize(r.title.MinSize())
	r.sub.Move(fyne.NewPos(pad, 30))
	r.sub.Resize(fyne.NewSize(size.Width-pad*2-56, r.sub.MinSize().Height))

	trackW, trackH := float32(48), float32(26)
	tx := size.Width - pad - trackW
	ty := (size.Height - trackH) / 2
	r.track.Resize(fyne.NewSize(trackW, trackH))
	r.track.Move(fyne.NewPos(tx, ty))

	knob := float32(20)
	kx := tx + 3
	if r.on {
		kx = tx + trackW - knob - 3
	}
	r.knob.Resize(fyne.NewSize(knob, knob))
	r.knob.Move(fyne.NewPos(kx, ty+(trackH-knob)/2))
}

func (t *toggleRenderer) MinSize() fyne.Size { return fyne.NewSize(280, 56) }

func (t *toggleRenderer) Refresh() {
	t.r.paint()
	t.Layout(t.r.Size())
	for _, o := range t.objs {
		o.Refresh()
	}
}

func (t *toggleRenderer) Objects() []fyne.CanvasObject { return t.objs }
func (t *toggleRenderer) Destroy()                     {}

func (r *toggleRow) SetOn(on bool) {
	if r.on == on {
		return
	}
	r.on = on
	r.Refresh()
}

func (r *toggleRow) paint() {
	if r.on {
		r.track.FillColor = withAlpha(colPrimary, 60)
		r.track.StrokeColor = colPrimary
		r.knob.FillColor = colPrimary
	} else {
		r.track.FillColor = colCheckFill
		r.track.StrokeColor = colCheckEdge
		r.knob.FillColor = colForeground
	}
	if r.hovered {
		r.bg.FillColor = colCardRaised
	} else {
		r.bg.FillColor = colCard
	}
	r.bg.StrokeColor = withAlpha(colBorder, 160)
	r.bg.StrokeWidth = 1
}

func (r *toggleRow) Tapped(*fyne.PointEvent) {
	r.on = !r.on
	r.Refresh()
	if r.changed != nil {
		r.changed(r.on)
	}
}

func (r *toggleRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.Refresh() }
func (r *toggleRow) MouseMoved(*desktop.MouseEvent) {}
func (r *toggleRow) MouseOut()                      { r.hovered = false; r.Refresh() }
func (r *toggleRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }

// modeChip 是 TUN / 系统代理的分段按钮。
type modeChip struct {
	widget.BaseWidget
	bg      *canvas.Rectangle
	label   *canvas.Text
	active  bool
	hovered bool
	onTap   func()
}

func newModeChip(text string, onTap func()) *modeChip {
	c := &modeChip{onTap: onTap}
	c.bg = canvas.NewRectangle(colCard)
	c.bg.CornerRadius = 10
	c.label = canvas.NewText(text, colMuted)
	c.label.TextSize = 13
	c.label.TextStyle = fyne.TextStyle{Bold: true}
	c.label.Alignment = fyne.TextAlignCenter
	c.ExtendBaseWidget(c)
	c.paint()
	return c
}

func (c *modeChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.bg, container.NewCenter(c.label)))
}

func (c *modeChip) MinSize() fyne.Size { return fyne.NewSize(120, 36) }

func (c *modeChip) setActive(on bool) {
	c.active = on
	c.paint()
}

func (c *modeChip) paint() {
	switch {
	case c.active:
		c.bg.FillColor = withAlpha(colPrimary, 40)
		c.bg.StrokeColor = colPrimary
		c.bg.StrokeWidth = 1.5
		c.label.Color = colPrimary
	case c.hovered:
		c.bg.FillColor = colCardRaised
		c.bg.StrokeColor = colBorder
		c.bg.StrokeWidth = 1
		c.label.Color = colForeground
	default:
		c.bg.FillColor = colCard
		c.bg.StrokeColor = withAlpha(colBorder, 140)
		c.bg.StrokeWidth = 1
		c.label.Color = colMuted
	}
	c.bg.Refresh()
	c.label.Refresh()
}

func (c *modeChip) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}
func (c *modeChip) MouseIn(*desktop.MouseEvent)    { c.hovered = true; c.paint() }
func (c *modeChip) MouseMoved(*desktop.MouseEvent) {}
func (c *modeChip) MouseOut()                      { c.hovered = false; c.paint() }
func (c *modeChip) Cursor() desktop.Cursor         { return desktop.PointerCursor }

// brandHeader 顶部品牌条：logo + 穿云大字。
func brandHeader(ver string) fyne.CanvasObject {
	logo := canvas.NewImageFromResource(appIcon())
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(42, 42))
	title := canvas.NewText("穿云", colForeground)
	title.TextSize = 26
	title.TextStyle = fyne.TextStyle{Bold: true}
	sub := mutedText("CHUANYUN", 10)
	left := container.NewHBox(container.NewGridWrap(fyne.NewSize(42, 42), logo), container.NewVBox(title, sub))
	return container.NewBorder(nil, nil, left, container.NewCenter(mutedText(ver, 12)))
}
