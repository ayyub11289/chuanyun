package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"chuanyun/internal/failover"
	"chuanyun/internal/geo"
	"chuanyun/internal/nodes"
)

// ServerView 是节点列表的一行：一台服务器，下面的多个端口和协议合成「线路」，界面上不显示端口。
type ServerView struct {
	Server string
	// Place 是「日本 · 东京」；位置还没查到时退回节点备注或「服务器 N」。
	Place string
	// Code 是两位国家代码，未知时为空。
	Code        string
	Name        string
	Lines       int
	Current     bool
	CurrentLine string
	Health      HealthKind
	Latency     string
}

var portSuffix = regexp.MustCompile(`[-_ ·]*\d{2,5}$`)

// baseName 去掉「穿云-443」这类名字末尾的端口；只剩协议名时返回空。
func baseName(n nodes.Node) string {
	s := strings.TrimSpace(portSuffix.ReplaceAllString(n.Name, ""))
	switch strings.ToLower(s) {
	case "", "reality", "vless", "hysteria2", "hy2":
		return ""
	}
	return s
}

// serverOrderLocked 返回服务器在列表里第一次出现的顺序。
func (c *Controller) serverOrderLocked() []string {
	var order []string
	seen := map[string]bool{}
	for _, n := range c.nodes {
		if !seen[n.Server] {
			seen[n.Server] = true
			order = append(order, n.Server)
		}
	}
	return order
}

// lineLabelLocked 是第 i 个节点在所属服务器里的编号，如「线路 2」。
func (c *Controller) lineLabelLocked(i int) string {
	if i < 0 || i >= len(c.nodes) {
		return ""
	}
	k := 0
	for j := 0; j <= i; j++ {
		if c.nodes[j].Server == c.nodes[i].Server {
			k++
		}
	}
	return fmt.Sprintf("线路 %d", k)
}

func (c *Controller) placeLocked(server string) string {
	if info, ok := c.geo[server]; ok && info.Place() != "" {
		return info.Place()
	}
	for _, n := range c.nodes {
		if n.Server == server {
			if b := baseName(n); b != "" {
				return b
			}
		}
	}
	for k, s := range c.serverOrderLocked() {
		if s == server {
			return fmt.Sprintf("服务器 %d", k+1)
		}
	}
	return server
}

// labelledRefs 给看门狗用：带上线路编号和地名，通知里就不出现端口和 IP。调用时不能持有 c.mu。
func (c *Controller) labelledRefs(list []nodes.Node) []failover.NodeRef {
	refs := refsOf(list)
	c.mu.Lock()
	defer c.mu.Unlock()
	count := map[string]int{}
	for i, n := range list {
		count[n.Server]++
		refs[i].Label = fmt.Sprintf("线路 %d", count[n.Server])
		refs[i].Place = c.placeLocked(n.Server)
	}
	return refs
}

func healthRank(h HealthKind) int {
	switch h {
	case HealthOK:
		return 0
	case HealthReachable:
		return 1
	case HealthUnknown:
		return 2
	case HealthUDP:
		return 3
	case HealthInterfered:
		return 4
	case HealthBlocked:
		return 5
	case HealthFailed:
		return 6
	default:
		return 7
	}
}

// serversLocked 把 Snapshot.Nodes 按服务器合并。
func (c *Controller) serversLocked(views []NodeView) []ServerView {
	idx := map[string]int{}
	var out []ServerView
	for i, v := range views {
		k, ok := idx[v.Server]
		if !ok {
			k = len(out)
			idx[v.Server] = k
			sv := ServerView{Server: v.Server, Place: c.placeLocked(v.Server), Health: v.Health}
			if info, ok := c.geo[v.Server]; ok {
				sv.Code = info.Code
			}
			sv.Name = baseName(c.nodes[i])
			out = append(out, sv)
		}
		sv := &out[k]
		sv.Lines++
		if healthRank(v.Health) < healthRank(sv.Health) {
			sv.Health = v.Health
		}
		if v.Current {
			sv.Current = true
			sv.CurrentLine = c.lineLabelLocked(i)
			if v.Latency != "" {
				sv.Latency = v.Latency
			}
		} else if sv.Latency == "" {
			sv.Latency = v.Latency
		}
	}
	return out
}

// SelectServer 切到某台服务器：优先选没有被判定为不通的线路。
// 多台服务器时手动选择会关闭自动选择。
func (c *Controller) SelectServer(server string) {
	c.mu.Lock()
	manual := len(c.serverOrderLocked()) > 1 && !c.settings.ManualPick
	if manual {
		c.settings.ManualPick = true
	}
	if c.index >= 0 && c.index < len(c.nodes) && c.nodes[c.index].Server == server {
		c.mu.Unlock()
		if manual {
			c.saveSettings()
			c.emit()
		}
		return
	}
	pick := -1
	for i, n := range c.nodes {
		if n.Server != server {
			continue
		}
		if pick < 0 {
			pick = i
		}
		if !c.health[nodeKey(n)].Bad() {
			pick = i
			break
		}
	}
	c.mu.Unlock()
	if pick >= 0 {
		c.Select(pick)
	} else if manual {
		c.saveSettings()
	}
}

func (c *Controller) loadGeo() {
	c.geo = map[string]geo.Info{}
	if b, err := os.ReadFile(filepath.Join(c.opts.DataDir, "geo.json")); err == nil {
		_ = json.Unmarshal(b, &c.geo)
	}
}

// RefreshGeo 在后台查还不知道位置的服务器。
func (c *Controller) RefreshGeo() {
	go c.refreshGeo()
}

// refreshGeo 只在已连接时经隧道查询：查询是明文 HTTP，直连会让墙看到本机在查服务器 IP。
func (c *Controller) refreshGeo() {
	c.mu.Lock()
	if c.geoBusy || c.opts.Locate == nil || c.phase != PhaseConnected {
		c.mu.Unlock()
		return
	}
	var todo []string
	for _, s := range c.serverOrderLocked() {
		if _, ok := c.geo[s]; !ok {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		c.mu.Unlock()
		return
	}
	c.geoBusy = true
	c.mu.Unlock()

	found := map[string]geo.Info{}
	for _, s := range todo {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		info, err := c.opts.Locate(ctx, s, proxyURL())
		cancel()
		if err == nil {
			found[s] = info
		}
	}

	c.mu.Lock()
	c.geoBusy = false
	for s, info := range found {
		c.geo[s] = info
	}
	b, _ := json.MarshalIndent(c.geo, "", "  ")
	c.mu.Unlock()
	if len(found) > 0 {
		_ = os.WriteFile(filepath.Join(c.opts.DataDir, "geo.json"), b, 0o600)
		c.emit()
	}
}
