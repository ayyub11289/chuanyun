//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const appID = "Chuanyun.Desktop"

// Elevated 判断当前进程是否已通过 UAC 提升。
func Elevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

// RelaunchElevated 用 runas 重新启动自己。
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	arg, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, arg, cwd, windows.SW_SHOWNORMAL)
}

const (
	inetKey   = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	backupKey = `Software\Chuanyun\PreviousProxy`
)

// SetSystemProxy 写入当前用户的 WinINET 代理。原有设置先备份到注册表，断开或崩溃后启动时还原。
func SetSystemProxy(port int) error {
	ours := fmt.Sprintf("127.0.0.1:%d", port)
	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	backupProxy(k, ours)
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", ours); err != nil {
		return err
	}
	_ = k.SetStringValue("ProxyOverride", "localhost;127.0.0.1;<local>")
	notifyProxy()
	return nil
}

// ClearSystemProxy 关闭本程序设置的系统代理，有备份时还原用户原来的设置。
func ClearSystemProxy() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	restored := restoreProxy(k)
	if !restored {
		if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
			return err
		}
	}
	notifyProxy()
	return nil
}

// ClearStaleProxy 在启动时调用：系统代理仍指向本程序端口（上次没正常断开）时关掉它。
func ClearStaleProxy(port int) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	enabled, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")
	k.Close()
	if enabled != 1 || server != fmt.Sprintf("127.0.0.1:%d", port) {
		return false
	}
	return ClearSystemProxy() == nil
}

func backupProxy(k registry.Key, ours string) {
	server, _, _ := k.GetStringValue("ProxyServer")
	if server == ours {
		return
	}
	enabled, _, _ := k.GetIntegerValue("ProxyEnable")
	override, _, _ := k.GetStringValue("ProxyOverride")
	b, _, err := registry.CreateKey(registry.CURRENT_USER, backupKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer b.Close()
	_ = b.SetDWordValue("ProxyEnable", uint32(enabled))
	_ = b.SetStringValue("ProxyServer", server)
	_ = b.SetStringValue("ProxyOverride", override)
}

func restoreProxy(k registry.Key) bool {
	b, err := registry.OpenKey(registry.CURRENT_USER, backupKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	enabled, _, _ := b.GetIntegerValue("ProxyEnable")
	server, _, _ := b.GetStringValue("ProxyServer")
	override, _, _ := b.GetStringValue("ProxyOverride")
	b.Close()
	_ = k.SetDWordValue("ProxyEnable", uint32(enabled))
	_ = k.SetStringValue("ProxyServer", server)
	_ = k.SetStringValue("ProxyOverride", override)
	_ = registry.DeleteKey(registry.CURRENT_USER, backupKey)
	return true
}

func notifyProxy() {
	wininet := windows.NewLazySystemDLL("wininet.dll")
	proc := wininet.NewProc("InternetSetOptionW")
	const internetOptionSettingsChanged = 39
	const internetOptionRefresh = 37
	_, _, _ = proc.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = proc.Call(0, internetOptionRefresh, 0, 0)
}

// InstallAutostart 创建登录时以最高权限运行的计划任务，通知仍在交互会话里弹出。
func InstallAutostart(exe string) error {
	tr := fmt.Sprintf(`"%s" --autostart`, exe)
	cmd := exec.Command("schtasks", "/Create", "/F", "/TN", "Chuanyun", "/SC", "ONLOGON", "/RL", "HIGHEST", "/IT", "/TR", tr)
	hide(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveAutostart 删除计划任务。任务不存在时视为成功。
func RemoveAutostart() error {
	cmd := exec.Command("schtasks", "/Delete", "/F", "/TN", "Chuanyun")
	hide(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "cannot find") || strings.Contains(msg, "not found") || strings.Contains(string(out), "不存在") {
			return nil
		}
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Notify 弹出 Windows 通知。
func Notify(title, body string) {
	ps := filepath.Join(os.TempDir(), "chuanyun-toast.ps1")
	if err := os.WriteFile(ps, []byte(toastScript), 0o644); err != nil {
		return
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", ps)
	cmd.Env = append(os.Environ(),
		"CHUANYUN_APPID="+appID,
		"CHUANYUN_TITLE="+title,
		"CHUANYUN_BODY="+body,
	)
	hide(cmd)
	_ = cmd.Run()
}

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// AcquireSingleInstance 保证只开一个窗口。already 为真时调用方应退出。
func AcquireSingleInstance() (release func(), already bool) {
	name, _ := windows.UTF16PtrFromString(`Local\Chuanyun`)
	h, err := windows.CreateMutex(nil, false, name)
	// 已运行的是提权实例时，普通权限打开它的互斥量会被拒绝访问，同样说明已在运行。
	if err == windows.ERROR_ALREADY_EXISTS || err == windows.ERROR_ACCESS_DENIED {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		return func() {}, true
	}
	return func() {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}, false
}

const showEvent = `Local\ChuanyunShow`

// WatchShowRequests 在另一个实例请求唤出窗口时调用 fn。
// 提权实例和普通实例完整性级别不同，事件要放开 DACL 并降到低完整性，双方才都打得开。
func WatchShowRequests(fn func()) {
	var sa *windows.SecurityAttributes
	if sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)"); err == nil {
		sa = &windows.SecurityAttributes{SecurityDescriptor: sd}
		sa.Length = uint32(unsafe.Sizeof(*sa))
	}
	name, _ := windows.UTF16PtrFromString(showEvent)
	h, _ := windows.CreateEvent(sa, 0, 0, name)
	if h == 0 {
		return
	}
	go func() {
		for {
			r, err := windows.WaitForSingleObject(h, windows.INFINITE)
			if err != nil || r != windows.WAIT_OBJECT_0 {
				return
			}
			fn()
		}
	}()
}

// RequestShow 让已运行的实例把窗口调到前台；对方不支持时返回 false。
func RequestShow() bool {
	name, _ := windows.UTF16PtrFromString(showEvent)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h) == nil
}

// AlreadyRunning 用系统对话框说明程序已在运行。
func AlreadyRunning() {
	title, _ := windows.UTF16PtrFromString("穿云")
	text, _ := windows.UTF16PtrFromString("穿云已经在运行。")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONINFORMATION)
}

const toastScript = `
$ErrorActionPreference = 'Stop'
$appId = $env:CHUANYUN_APPID
if (-not $appId) { $appId = 'Chuanyun.Desktop' }
$title = $env:CHUANYUN_TITLE
$body = $env:CHUANYUN_BODY
$reg = "HKCU:\Software\Classes\AppUserModelId\$appId"
New-Item -Path $reg -Force | Out-Null
New-ItemProperty -Path $reg -Name DisplayName -Value '穿云' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $reg -Name ShowInSettings -Value 1 -PropertyType DWord -Force | Out-Null
$note = "HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Notifications\Settings\$appId"
New-Item -Path $note -Force | Out-Null
New-ItemProperty -Path $note -Name ShowInActionCenter -Value 1 -PropertyType DWord -Force | Out-Null
function Esc([string]$s) {
  if (-not $s) { return '' }
  return [System.Security.SecurityElement]::Escape($s)
}
$xmlText = @"
<toast>
  <visual>
    <binding template="ToastGeneric">
      <text>$(Esc $title)</text>
      <text>$(Esc $body)</text>
    </binding>
  </visual>
</toast>
"@
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($xmlText)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)
`
