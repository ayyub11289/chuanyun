//go:build windows

package platform

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// App 是直连程序选择器里的一项。
type App struct {
	Name    string
	Exe     string
	Path    string
	Running bool
	// Icon 是 32×32 PNG，取不到时为空。
	Icon []byte
}

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	gdi32             = windows.NewLazySystemDLL("gdi32.dll")
	procExtractIconEx = shell32.NewProc("ExtractIconExW")
	procGetIconInfo   = user32.NewProc("GetIconInfo")
	procDestroyIcon   = user32.NewProc("DestroyIcon")
	procGetDC         = user32.NewProc("GetDC")
	procReleaseDC     = user32.NewProc("ReleaseDC")
	procGetObject     = gdi32.NewProc("GetObjectW")
	procGetDIBits     = gdi32.NewProc("GetDIBits")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
)

// ListApps 列出正在运行和已安装的桌面程序（不含系统目录里的），运行中的排在前面。
func ListApps() []App {
	byExe := map[string]*App{}
	add := func(path, name string, running bool) {
		path = cleanExePath(path)
		if path == "" || skipApp(path) {
			return
		}
		key := strings.ToLower(filepath.Base(path))
		if a, ok := byExe[key]; ok {
			a.Running = a.Running || running
			if a.Name == "" {
				a.Name = name
			}
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		byExe[key] = &App{Name: name, Exe: filepath.Base(path), Path: path, Running: running}
	}
	for _, p := range runningExes() {
		add(p, "", true)
	}
	for _, it := range installedExes() {
		add(it[0], it[1], false)
	}

	out := make([]App, 0, len(byExe))
	for _, a := range byExe {
		if a.Name == "" {
			a.Name = fileDescription(a.Path)
		}
		if a.Name == "" {
			a.Name = strings.TrimSuffix(a.Exe, filepath.Ext(a.Exe))
		}
		// 没有图标的多是后台服务和命令行工具，列表里不放，需要时可手动输入。
		if a.Icon = exeIcon(a.Path); len(a.Icon) == 0 {
			continue
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func cleanExePath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, `"`) {
		if end := strings.Index(p[1:], `"`); end >= 0 {
			p = p[1 : end+1]
		}
	}
	if i := strings.LastIndex(strings.ToLower(p), ".exe"); i >= 0 {
		p = p[:i+4]
	} else {
		return ""
	}
	if s, err := registry.ExpandString(p); err == nil {
		p = s
	}
	return filepath.Clean(p)
}

func skipApp(path string) bool {
	lower := strings.ToLower(path)
	if win := os.Getenv("SystemRoot"); win != "" && strings.HasPrefix(lower, strings.ToLower(win)+`\`) {
		return true
	}
	if strings.Contains(lower, `\package cache\`) || strings.Contains(lower, `\installer\`) {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	for _, s := range []string{"unins", "uninst", "setup", "installer", "update", "crashpad", "helper", "elevation", "redist", "webview2"} {
		if strings.Contains(base, s) {
			return true
		}
	}
	return base == "chuanyun.exe" || base == "sing-box.exe"
}

func runningExes() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []string
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
			out = append(out, windows.UTF16ToString(buf[:n]))
		}
		windows.CloseHandle(h)
	}
	return out
}

// installedExes 返回 [路径, 显示名]，来自卸载信息的 DisplayIcon 和 App Paths。
func installedExes() [][2]string {
	var out [][2]string
	uninstall := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	for _, u := range uninstall {
		eachSubkey(u.root, u.path, func(k registry.Key) {
			icon, _, _ := k.GetStringValue("DisplayIcon")
			name, _, _ := k.GetStringValue("DisplayName")
			if icon != "" {
				out = append(out, [2]string{icon, strings.TrimSpace(name)})
			}
		})
	}
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		eachSubkey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths`, func(k registry.Key) {
			if p, _, err := k.GetStringValue(""); err == nil {
				out = append(out, [2]string{p, ""})
			}
		})
	}
	return out
}

func eachSubkey(root registry.Key, path string, fn func(registry.Key)) {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		fn(sk)
		sk.Close()
	}
}

func fileDescription(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])) != nil {
		return ""
	}
	var p unsafe.Pointer
	var n uint32
	if windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&p), &n) != nil || n < 4 {
		return ""
	}
	pair := unsafe.Slice((*uint16)(p), 2)
	sub := fmt.Sprintf(`\StringFileInfo\%04x%04x\FileDescription`, pair[0], pair[1])
	if windows.VerQueryValue(unsafe.Pointer(&buf[0]), sub, unsafe.Pointer(&p), &n) != nil || n == 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(unsafe.Slice((*uint16)(p), n)))
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

type bitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

type bitmapInfo struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
	colors          [4]uint32
}

func exeIcon(path string) []byte {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var large windows.Handle
	if n, _, _ := procExtractIconEx.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&large)), 0, 1); n == 0 || large == 0 {
		return nil
	}
	defer procDestroyIcon.Call(uintptr(large))

	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(large), uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil
	}
	defer procDeleteObject.Call(uintptr(ii.hbmMask))
	if ii.hbmColor == 0 {
		return nil
	}
	defer procDeleteObject.Call(uintptr(ii.hbmColor))

	var bm bitmap
	if r, _, _ := procGetObject.Call(uintptr(ii.hbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w <= 0 || h <= 0 || w > 256 || h > 256 {
		return nil
	}
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer procReleaseDC.Call(0, dc)

	read := func(hbm windows.Handle) []byte {
		bi := bitmapInfo{biWidth: int32(w), biHeight: -int32(h), biPlanes: 1, biBitCount: 32}
		bi.biSize = uint32(unsafe.Offsetof(bi.colors))
		px := make([]byte, w*h*4)
		if r, _, _ := procGetDIBits.Call(dc, uintptr(hbm), 0, uintptr(h), uintptr(unsafe.Pointer(&px[0])), uintptr(unsafe.Pointer(&bi)), 0); int(r) != h {
			return nil
		}
		return px
	}
	color := read(ii.hbmColor)
	if color == nil {
		return nil
	}
	hasAlpha := false
	for i := 3; i < len(color); i += 4 {
		if color[i] != 0 {
			hasAlpha = true
			break
		}
	}
	var mask []byte
	if !hasAlpha {
		mask = read(ii.hbmMask)
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		o := i * 4
		img.Pix[o], img.Pix[o+1], img.Pix[o+2] = color[o+2], color[o+1], color[o]
		switch {
		case hasAlpha:
			img.Pix[o+3] = color[o+3]
		case mask != nil && mask[o] != 0:
			img.Pix[o+3] = 0
		default:
			img.Pix[o+3] = 255
		}
	}
	var out bytes.Buffer
	if png.Encode(&out, img) != nil {
		return nil
	}
	return out.Bytes()
}
