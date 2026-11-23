//go:build windows

package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"chuanyun/internal/app"
)

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func card(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 16
	bg.StrokeColor = withAlpha(colBorder, 150)
	bg.StrokeWidth = 1
	glow := canvas.NewRectangle(withAlpha(colPrimary, 8))
	glow.CornerRadius = 16
	return container.NewStack(bg, glow, container.New(layout.NewCustomPaddedLayout(12, 12, 14, 14), content))
}

func mutedText(s string, size float32) *canvas.Text {
	t := canvas.NewText(s, colMuted)
	t.TextSize = size
	return t
}

// motion 是连接按钮的动画状态。
type motion int

const (
	motionNone       motion = iota
	motionConnecting        // 彗星绕圈
	motionConnected         // 光晕向外扩散
)

const (
	ringSize = float32(136)
	haloGrow = float32(26)
)

// powerButton 是主界面中间的圆形连接按钮。
type powerButton struct {
	widget.BaseWidget
	halos    [2]*canvas.Circle
	ring     *canvas.Circle
	disc     *canvas.Circle
	comets   [4]*canvas.Circle
	label    *canvas.Text
	onTap    func()
	tone     color.NRGBA
	disabled bool
	hovered  bool

	motion motion
	phase  float32
	anim   *fyne.Animation
}

func newPowerButton(onTap func()) *powerButton {
	b := &powerButton{onTap: onTap, tone: colPrimary}
	for i := range b.halos {
		b.halos[i] = canvas.NewCircle(color.Transparent)
		b.halos[i].StrokeWidth = 2
	}
	b.ring = canvas.NewCircle(color.Transparent)
	b.ring.StrokeWidth = 3
	b.disc = canvas.NewCircle(colCardRaised)
	for i := range b.comets {
		b.comets[i] = canvas.NewCircle(color.Transparent)
	}
	b.label = canvas.NewText("连接", colForeground)
	b.label.TextSize = 22
	b.label.TextStyle = fyne.TextStyle{Bold: true}
	b.label.Alignment = fyne.TextAlignCenter
	b.ExtendBaseWidget(b)
	b.apply()
	return b
}

type powerRenderer struct {
	b    *powerButton
	objs []fyne.CanvasObject
}

func (b *powerButton) CreateRenderer() fyne.WidgetRenderer {
	r := &powerRenderer{b: b}
	r.objs = append(r.objs, b.halos[0], b.halos[1], b.ring, b.disc)
	for _, c := range b.comets {
		r.objs = append(r.objs, c)
	}
	r.objs = append(r.objs, b.label)
	return r
}

func (r *powerRenderer) Layout(size fyne.Size) {
	b := r.b
	cx, cy := size.Width/2, size.Height/2
	place := func(o fyne.CanvasObject, d float32) {
		o.Resize(fyne.NewSize(d, d))
		o.Move(fyne.NewPos(cx-d/2, cy-d/2))
	}
	place(b.ring, ringSize)
	place(b.disc, ringSize-18)

	for i, h := range b.halos {
		p := b.phase + float32(i)*0.5
		if p >= 1 {
			p--
		}
		if b.motion != motionConnected {
			h.StrokeColor = color.Transparent
			h.FillColor = color.Transparent
			place(h, ringSize)
			continue
		}
		place(h, ringSize+2*haloGrow*p)
		h.StrokeColor = withAlpha(b.tone, uint8(150*(1-p)))
		h.FillColor = withAlpha(b.tone, uint8(26*(1-p)))
	}

	radius := ringSize/2 - 1.5
	for i, c := range b.comets {
		if b.motion != motionConnecting {
			c.FillColor = color.Transparent
			place(c, 0)
			continue
		}
		a := float64(b.phase)*2*math.Pi - float64(i)*0.32 - math.Pi/2
		d := float32(11 - 2*i)
		c.Resize(fyne.NewSize(d, d))
		c.Move(fyne.NewPos(cx+radius*float32(math.Cos(a))-d/2, cy+radius*float32(math.Sin(a))-d/2))
		c.FillColor = withAlpha(colForeground, uint8(235-55*i))
	}

	ls := b.label.MinSize()
	b.label.Resize(ls)
	b.label.Move(fyne.NewPos(cx-ls.Width/2, cy-ls.Height/2))
}

func (r *powerRenderer) MinSize() fyne.Size {
	d := ringSize + 2*haloGrow
	return fyne.NewSize(d, d)
}

func (r *powerRenderer) Refresh() {
	r.Layout(r.b.Size())
	for _, o := range r.objs {
		o.Refresh()
	}
}

func (r *powerRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *powerRenderer) Destroy()                     {}

// setMotion 切换动画；同一状态重复调用不会重启动画。
func (b *powerButton) setMotion(m motion) {
	if m == b.motion {
		return
	}
	b.motion = m
	if b.anim != nil {
		b.anim.Stop()
		b.anim = nil
	}
	b.phase = 0
	if m != motionNone {
		period := 1200 * time.Millisecond
		if m == motionConnected {
			period = 3200 * time.Millisecond
		}
		b.anim = fyne.NewAnimation(period, func(p float32) {
			b.phase = p
			b.Refresh()
		})
		b.anim.Curve = fyne.AnimationLinear
		b.anim.RepeatCount = fyne.AnimationRepeatForever
		b.anim.Start()
	}
	b.Refresh()
}

// inRing 判断点是否落在圆环内，光晕区域不算按钮。
func (b *powerButton) inRing(p fyne.Position) bool {
	s := b.Size()
	dx, dy := p.X-s.Width/2, p.Y-s.Height/2
	return dx*dx+dy*dy <= (ringSize/2)*(ringSize/2)
}

func (b *powerButton) set(text string, tone color.NRGBA, disabled bool) {
	if b.label.Text == text && b.tone == tone && b.disabled == disabled {
		return
	}
	b.label.Text = text
	b.tone = tone
	b.disabled = disabled
	b.apply()
}

func (b *powerButton) apply() {
	alpha := uint8(46)
	if b.hovered && !b.disabled {
		alpha = 90
	}
	b.ring.StrokeColor = b.tone
	b.disc.FillColor = withAlpha(b.tone, alpha)
	b.label.Color = colForeground
	if b.disabled {
		b.ring.StrokeColor = withAlpha(b.tone, 120)
		b.label.Color = colMuted
	}
	b.ring.Refresh()
	b.disc.Refresh()
	b.label.Refresh()
}

func (b *powerButton) Tapped(e *fyne.PointEvent) {
	if !b.disabled && b.onTap != nil && b.inRing(e.Position) {
		b.onTap()
	}
}

func (b *powerButton) MouseIn(e *desktop.MouseEvent) { b.MouseMoved(e) }
func (b *powerButton) MouseMoved(e *desktop.MouseEvent) {
	if in := b.inRing(e.Position); in != b.hovered {
		b.hovered = in
		b.apply()
	}
}
func (b *powerButton) MouseOut() { b.hovered = false; b.apply() }
func (b *powerButton) Cursor() desktop.Cursor {
	if b.disabled || !b.hovered {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

// nodeRow 是节点列表的一行（一台服务器）：点一下就切换过去。
type nodeRow struct {
	widget.BaseWidget
	bg      *canvas.Rectangle
	flag    *canvas.Rectangle
	code    *canvas.Text
	name    *canvas.Text
	sub     *canvas.Text
	badge   *canvas.Text
	latency *canvas.Text
	onTap   func()
	current bool
	hovered bool
}

func newNodeRow(onTap func()) *nodeRow {
	r := &nodeRow{onTap: onTap}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 10
	r.flag = canvas.NewRectangle(withAlpha(colPrimary, 35))
	r.flag.CornerRadius = 8
	r.flag.StrokeColor = withAlpha(colPrimary, 140)
	r.flag.StrokeWidth = 1
	r.code = canvas.NewText("", colPrimary)
	r.code.TextSize = 13
	r.code.TextStyle = fyne.TextStyle{Bold: true}
	r.code.Alignment = fyne.TextAlignCenter
	r.name = canvas.NewText("", colForeground)
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.sub = mutedText("", 12)
	r.badge = canvas.NewText("", colMuted)
	r.badge.TextSize = 12
	r.badge.Alignment = fyne.TextAlignTrailing
	r.latency = mutedText("", 12)
	r.latency.Alignment = fyne.TextAlignTrailing
	r.ExtendBaseWidget(r)
	return r
}

func (r *nodeRow) CreateRenderer() fyne.WidgetRenderer {
	flag := container.NewCenter(container.NewGridWrap(fyne.NewSize(38, 30), container.NewStack(r.flag, container.NewCenter(r.code))))
	right := container.NewVBox(r.badge, r.latency)
	body := container.NewBorder(nil, nil, container.NewHBox(flag, layout.NewSpacer()), right, container.NewVBox(r.name, r.sub))
	return widget.NewSimpleRenderer(container.NewStack(r.bg, container.New(layout.NewCustomPaddedLayout(7, 7, 10, 10), body)))
}

func (r *nodeRow) update(v app.ServerView) {
	r.current = v.Current
	r.name.Text = v.Place
	r.code.Text = v.Code
	if r.code.Text == "" {
		if rs := []rune(v.Place); len(rs) > 0 {
			r.code.Text = string(rs[0])
		} else {
			r.code.Text = "·"
		}
	}
	var sub []string
	if v.Name != "" && v.Name != v.Place {
		sub = append(sub, v.Name)
	}
	if v.Lines > 1 {
		sub = append(sub, fmt.Sprintf("%d 条线路", v.Lines))
	}
	if v.Current && v.CurrentLine != "" && v.Lines > 1 {
		sub = append(sub, "正在用"+v.CurrentLine)
	}
	if len(sub) == 0 {
		sub = append(sub, "1 条线路")
	}
	r.sub.Text = strings.Join(sub, " · ")
	r.badge.Text = v.Health.Text()
	switch {
	case v.Health.Good():
		r.badge.Color = colSuccess
	case v.Health.Bad():
		r.badge.Color = colError
	default:
		r.badge.Color = colMuted
	}
	r.latency.Text = v.Latency
	r.paint()
}

func (r *nodeRow) paint() {
	switch {
	case r.current:
		r.bg.FillColor = colSelected
		r.bg.StrokeColor = withAlpha(colPrimary, 150)
		r.bg.StrokeWidth = 1
		r.flag.FillColor = withAlpha(colPrimary, 55)
		r.flag.StrokeColor = colPrimary
	case r.hovered:
		r.bg.FillColor = colCardRaised
		r.bg.StrokeWidth = 0
		r.flag.FillColor = withAlpha(colPrimary, 30)
		r.flag.StrokeColor = withAlpha(colPrimary, 120)
	default:
		r.bg.FillColor = color.Transparent
		r.bg.StrokeWidth = 0
		r.flag.FillColor = withAlpha(colPrimary, 22)
		r.flag.StrokeColor = withAlpha(colPrimary, 90)
	}
	r.Refresh()
}

func (r *nodeRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *nodeRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.paint() }
func (r *nodeRow) MouseMoved(*desktop.MouseEvent) {}
func (r *nodeRow) MouseOut()                      { r.hovered = false; r.paint() }
func (r *nodeRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
