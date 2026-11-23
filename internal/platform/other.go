//go:build !windows

package platform

import (
	"fmt"
	"net/netip"
)

// Elevated 在非 Windows 上恒为 false。
func Elevated() bool { return false }

// RelaunchElevated 在非 Windows 上不可用。
func RelaunchElevated([]string) error { return fmt.Errorf("仅 Windows 支持提权") }

// SetSystemProxy 在非 Windows 上不可用。
func SetSystemProxy(int) error { return fmt.Errorf("仅 Windows 支持系统代理") }

// ClearSystemProxy 在非 Windows 上不可用。
func ClearSystemProxy() error { return fmt.Errorf("仅 Windows 支持系统代理") }

// ClearStaleProxy 在非 Windows 上什么也不做。
func ClearStaleProxy(int) bool { return false }

// InstallAutostart 在非 Windows 上不可用。
func InstallAutostart(string) error { return fmt.Errorf("仅 Windows 支持计划任务") }

// RemoveAutostart 在非 Windows 上不可用。
func RemoveAutostart() error { return nil }

// Notify 在非 Windows 上不弹系统通知。
func Notify(string, string) {}

// WatchShowRequests 在非 Windows 上什么也不做。
func WatchShowRequests(func()) {}

// RequestShow 在非 Windows 上不可用。
func RequestShow() bool { return false }

// App 是直连程序选择器里的一项。
type App struct {
	Name    string
	Exe     string
	Path    string
	Running bool
	Icon    []byte
}

// ListApps 在非 Windows 上返回空。
func ListApps() []App { return nil }

// KillSwitch 在非 Windows 上不可用。
type KillSwitch struct{}

// Engage 在非 Windows 上不可用。
func (*KillSwitch) Engage([]string, []netip.Prefix) error {
	return fmt.Errorf("仅 Windows 支持防泄漏")
}

// Release 在非 Windows 上什么也不做。
func (*KillSwitch) Release() {}

// Engaged 在非 Windows 上恒为 false。
func (*KillSwitch) Engaged() bool { return false }
