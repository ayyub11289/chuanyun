//go:build windows

package main

import (
	_ "embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

//go:embed fonts/NotoSansSC-Regular.otf
var fontBytes []byte

//go:embed icon.png
var iconBytes []byte

// 科技审美：近黑底 + 电青主色 + 高对比控件边，避免灰蓝糊成一片。
var (
	colBackground = color.NRGBA{R: 6, G: 10, B: 18, A: 255}
	colCard       = color.NRGBA{R: 14, G: 20, B: 32, A: 255}
	colCardRaised = color.NRGBA{R: 22, G: 32, B: 48, A: 255}
	colSelected   = color.NRGBA{R: 12, G: 42, B: 58, A: 255}
	colBorder     = color.NRGBA{R: 58, G: 86, B: 118, A: 255}
	colForeground = color.NRGBA{R: 236, G: 244, B: 252, A: 255}
	colMuted      = color.NRGBA{R: 120, G: 140, B: 164, A: 255}
	colPrimary    = color.NRGBA{R: 0, G: 212, B: 255, A: 255}
	colAccent     = color.NRGBA{R: 90, G: 140, B: 255, A: 255}
	colSuccess    = color.NRGBA{R: 34, G: 220, B: 150, A: 255}
	colError      = color.NRGBA{R: 255, G: 80, B: 110, A: 255}
	colWarning    = color.NRGBA{R: 255, G: 180, B: 64, A: 255}
	// 未勾选框边必须明显亮于背景，否则 Fyne Check 会「看不见」。
	colCheckEdge = color.NRGBA{R: 160, G: 190, B: 220, A: 255}
	colCheckFill = color.NRGBA{R: 28, G: 40, B: 58, A: 255}
)

func appIcon() fyne.Resource {
	return fyne.NewStaticResource("icon.png", iconBytes)
}

func appFont() fyne.Resource {
	return fyne.NewStaticResource("NotoSansSC-Regular.otf", fontBytes)
}

type chuanyunTheme struct {
	font fyne.Resource
}

func newTheme() fyne.Theme {
	return &chuanyunTheme{font: appFont()}
}

func (t *chuanyunTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameButton:
		return colCardRaised
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 86, G: 100, B: 120, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 18, G: 24, B: 34, A: 255}
	case theme.ColorNameError:
		return colError
	case theme.ColorNameFocus:
		return withAlpha(colPrimary, 70)
	case theme.ColorNameForeground:
		return colForeground
	case theme.ColorNameForegroundOnError:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 4, G: 12, B: 22, A: 255}
	case theme.ColorNameForegroundOnSuccess:
		return color.NRGBA{R: 4, G: 24, B: 18, A: 255}
	case theme.ColorNameHeaderBackground:
		return colCard
	case theme.ColorNameHover:
		return color.NRGBA{R: 36, G: 52, B: 74, A: 255}
	case theme.ColorNameInputBackground:
		return colCheckFill
	case theme.ColorNameInputBorder:
		return colCheckEdge
	case theme.ColorNameMenuBackground:
		return colCard
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 8, G: 12, B: 22, A: 245}
	case theme.ColorNamePlaceHolder:
		return colMuted
	case theme.ColorNamePressed:
		return color.NRGBA{R: 0, G: 160, B: 200, A: 255}
	case theme.ColorNamePrimary:
		return colPrimary
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 70, G: 100, B: 140, A: 255}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 40, G: 56, B: 78, A: 255}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 110}
	case theme.ColorNameSuccess:
		return colSuccess
	case theme.ColorNameWarning:
		return colWarning
	default:
		return theme.DefaultTheme().Color(n, theme.VariantDark)
	}
}

func (t *chuanyunTheme) Font(fyne.TextStyle) fyne.Resource { return t.font }

func (t *chuanyunTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (t *chuanyunTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNamePadding:
		return 7
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInlineIcon:
		return 20 // 勾选框加大，更容易点、更容易看见
	case theme.SizeNameInputBorder:
		return 2
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 10
	default:
		return theme.DefaultTheme().Size(n)
	}
}
