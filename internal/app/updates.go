package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"chuanyun/internal/singbox"
	"chuanyun/internal/update"
)

// Updater 访问发布目录。proxy 为空时直连。
type Updater interface {
	Manifest(ctx context.Context, base, proxy string) (update.Manifest, error)
	Download(ctx context.Context, base, proxy string, m update.Manifest, dst string) error
	Nodes(ctx context.Context, base, proxy string, key []byte) ([]byte, error)
}

type readyUpdate struct {
	Version string
	Notes   string
	Path    string
}

// UpdateInterval 是后台检查更新的间隔。
const UpdateInterval = 3 * time.Hour

// subscriptionFrom 从导入的 JSON 里取出发布地址和节点密钥，没有或无效时返回空串。
func subscriptionFrom(text string) (base, key string) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") {
		return "", ""
	}
	var s struct {
		Update    string `json:"update"`
		UpdateKey string `json:"update_key"`
	}
	if json.Unmarshal([]byte(t), &s) != nil {
		return "", ""
	}
	if b, err := update.NormalizeBase(s.Update); err == nil {
		base = b
	}
	if _, err := update.ParseNodesKey(s.UpdateKey); err == nil {
		key = s.UpdateKey
	}
	return base, key
}

func (c *Controller) updateLineLocked() string {
	switch {
	case c.opts.Updater == nil:
		return "这个版本没有内置发布公钥，自动更新未启用"
	case c.settings.UpdateURL == "":
		return "未设置更新地址。导入发布工具生成的节点文件后自动开启"
	case c.updBusy:
		return "正在检查更新…"
	case c.updLine != "":
		return c.updLine
	default:
		return "已开启自动更新，每 3 小时检查一次"
	}
}

// StartUpdateLoop 在启动 30 秒后检查一次，之后每 UpdateInterval 检查一次。
func (c *Controller) StartUpdateLoop() {
	go func() {
		t := time.NewTimer(30 * time.Second)
		for range t.C {
			c.checkUpdates(false)
			t.Reset(UpdateInterval)
		}
	}()
}

// CheckUpdates 是「检查更新」按钮。
func (c *Controller) CheckUpdates() {
	go c.checkUpdates(true)
}

func (c *Controller) checkUpdatesIfStale(age time.Duration) {
	c.mu.Lock()
	stale := c.opts.Now().Sub(c.updChecked) >= age
	c.mu.Unlock()
	if stale {
		c.checkUpdates(false)
	}
}

func (c *Controller) checkUpdates(manual bool) {
	c.mu.Lock()
	if c.updBusy || c.opts.Updater == nil || c.settings.UpdateURL == "" {
		c.mu.Unlock()
		if manual {
			c.emit()
		}
		return
	}
	c.updBusy = true
	c.updChecked = c.opts.Now()
	base := c.settings.UpdateURL
	key := c.settings.NodesKey
	proxy := ""
	if c.phase == PhaseConnected {
		proxy = fmt.Sprintf("http://127.0.0.1:%d", singbox.MixedPort)
	}
	c.mu.Unlock()
	c.emit()

	// 先查程序：同步节点可能触发重连，期间代理不可用。
	parts := []string{c.syncApp(base, proxy)}
	if msg := c.syncNodes(base, proxy, key); msg != "" {
		parts = append(parts, msg)
	}

	c.mu.Lock()
	c.updBusy = false
	c.updLine = strings.Join(parts, "；") + "（" + c.opts.Now().Format("15:04") + " 检查）"
	c.mu.Unlock()
	c.emit()
}

// viaTunnelFirst 先走代理（不暴露更新流量），失败再直连。
func viaTunnelFirst(proxy string, fn func(proxy string) error) error {
	err := fn(proxy)
	if err != nil && proxy != "" {
		if fn("") == nil {
			return nil
		}
	}
	return err
}

func (c *Controller) syncNodes(base, proxy, key string) string {
	if key == "" {
		return ""
	}
	k, err := update.ParseNodesKey(key)
	if err != nil {
		return "节点密钥无效"
	}
	var data []byte
	err = viaTunnelFirst(proxy, func(p string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var e error
		data, e = c.opts.Updater.Nodes(ctx, base, p, k)
		return e
	})
	if err != nil {
		c.log("同步节点失败：" + err.Error())
		return "节点同步失败"
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	c.mu.Lock()
	same := hash == c.settings.NodesHash
	c.mu.Unlock()
	if same {
		return "节点已是最新"
	}
	if err := c.importText(string(data), true, true); err != nil {
		c.log("服务器上的节点无法导入：" + err.Error())
		return "节点同步失败"
	}
	c.mu.Lock()
	c.settings.NodesHash = hash
	c.mu.Unlock()
	c.saveSettings()
	c.log("已从服务器同步节点")
	if c.opts.OS != nil {
		c.opts.OS.Notify("穿云", "节点已从服务器更新")
	}
	return "节点已同步"
}

func (c *Controller) syncApp(base, proxy string) string {
	var m update.Manifest
	err := viaTunnelFirst(proxy, func(p string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var e error
		m, e = c.opts.Updater.Manifest(ctx, base, p)
		return e
	})
	if err != nil {
		c.log("检查新版本失败：" + err.Error())
		return "检查新版本失败"
	}
	dir := filepath.Join(c.opts.DataDir, "update")
	if !update.Newer(m.Version, c.opts.Version) {
		_ = os.RemoveAll(dir)
		return "已是最新版本 " + c.opts.Version
	}
	_ = os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, m.File)
	if !update.FileMatches(dst, m) {
		c.log("发现新版本 " + m.Version + "，正在下载…")
		err = viaTunnelFirst(proxy, func(p string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			return c.opts.Updater.Download(ctx, base, p, m, dst)
		})
		if err != nil {
			c.log("下载新版本失败：" + err.Error())
			return "下载新版本失败"
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Name() != m.File {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	c.mu.Lock()
	c.updReady = &readyUpdate{Version: m.Version, Notes: m.Notes, Path: dst}
	notify := c.updNotified != m.Version
	c.updNotified = m.Version
	c.mu.Unlock()
	if notify {
		c.log("新版本 " + m.Version + " 已下载，点「重启更新」即可升级")
		if c.opts.OS != nil {
			c.opts.OS.Notify("穿云 "+m.Version+" 已就绪", "打开穿云，点「重启更新」即可升级。")
		}
	}
	return "新版本 " + m.Version + " 已就绪"
}

// ApplyUpdate 断开、换成已下载的新版本并重启。新版本会自动恢复连接。
func (c *Controller) ApplyUpdate() {
	c.mu.Lock()
	r := c.updReady
	was := c.phase != PhaseIdle
	c.mu.Unlock()
	if r == nil || c.opts.LaunchUpdate == nil {
		return
	}
	c.log("正在更新到 " + r.Version + "…")
	if was {
		c.Disconnect()
	}
	args := []string{"--updated"}
	if was {
		args = append(args, "--connect")
	}
	if c.opts.BeforeRelaunch != nil {
		c.opts.BeforeRelaunch()
	}
	if err := c.opts.LaunchUpdate(r.Path, args); err != nil {
		if c.opts.AfterRelaunchFailed != nil {
			c.opts.AfterRelaunchFailed()
		}
		c.mu.Lock()
		c.updLine = "更新失败：" + err.Error()
		c.mu.Unlock()
		c.log("更新失败，已回到当前版本：" + err.Error())
		c.emit()
		if was {
			go c.Connect()
		}
		return
	}
	if c.opts.OnExit != nil {
		c.opts.OnExit()
	}
}
