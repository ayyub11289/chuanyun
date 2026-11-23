package app

import (
	"net/netip"

	"chuanyun/internal/singbox"
)

// Firewall 是防泄漏用的系统防火墙。Engage 已生效时不重复安装。
type Firewall interface {
	Engage(allow []string, tunnel []netip.Prefix) error
	Release()
	Engaged() bool
}

// engageLocked 在 TUN 模式下启动核心前调用（持有 coreMu）：先阻断再起隧道，中间不留空档。
// 系统代理模式或关闭防泄漏时解除。
func (c *Controller) engageLocked(mode, singBox string) {
	fw := c.opts.Firewall
	if fw == nil {
		return
	}
	c.mu.Lock()
	want := c.settings.KillSwitch && mode == singbox.ModeTUN
	c.mu.Unlock()
	if !want {
		c.releaseFirewall()
		return
	}
	if fw.Engaged() {
		return
	}
	if c.opts.OS != nil && !c.opts.OS.Elevated() {
		return
	}
	if err := fw.Engage([]string{singBox, c.opts.ExePath}, singbox.TunPrefixes); err != nil {
		c.log("防泄漏未能启用：" + err.Error())
		return
	}
	c.log("防泄漏已启用：隧道断开时阻断上网，不会漏到本地网络")
}

func (c *Controller) releaseFirewall() {
	fw := c.opts.Firewall
	if fw == nil || !fw.Engaged() {
		return
	}
	fw.Release()
	c.log("防泄漏已解除，恢复正常上网")
}

// SetKillSwitch 打开或关闭防泄漏。已用 TUN 连接时立即生效。
func (c *Controller) SetKillSwitch(on bool) {
	c.mu.Lock()
	if c.settings.KillSwitch == on {
		c.mu.Unlock()
		return
	}
	c.settings.KillSwitch = on
	connected := c.phase == PhaseConnected
	mode := c.settings.Mode
	c.mu.Unlock()
	c.saveSettings()
	switch {
	case !on:
		c.releaseFirewall()
	case connected && mode == singbox.ModeTUN && c.opts.FindSingBox != nil:
		if bin, err := c.opts.FindSingBox(); err == nil {
			c.coreMu.Lock()
			c.engageLocked(mode, bin)
			c.coreMu.Unlock()
		}
	}
	c.emit()
}

// ReleaseBlock 在隧道已断开、防泄漏仍在阻断时手动恢复上网。
func (c *Controller) ReleaseBlock() {
	c.mu.Lock()
	idle := c.phase == PhaseIdle
	c.mu.Unlock()
	if idle {
		c.releaseFirewall()
		c.emit()
	}
}

func (c *Controller) blockingLocked() bool {
	return c.opts.Firewall != nil && c.phase == PhaseIdle && c.opts.Firewall.Engaged()
}
