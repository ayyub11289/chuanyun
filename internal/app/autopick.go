package app

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"chuanyun/internal/failover"
	"chuanyun/internal/nodes"
)

type probeResult struct {
	idx int
	ok  bool
	rtt time.Duration
}

func (c *Controller) probeLines(list []nodes.Node, idxs []int) []probeResult {
	out := make([]probeResult, len(idxs))
	var wg sync.WaitGroup
	for k, i := range idxs {
		wg.Add(1)
		go func(k, i int) {
			defer wg.Done()
			start := time.Now()
			ok := c.opts.Probes.TCP(list[i].Server, list[i].Port)
			out[k] = probeResult{idx: i, ok: ok, rtt: time.Since(start)}
		}(k, i)
	}
	wg.Wait()
	return out
}

// pickLine 在连接前选线路，探测只做 TCP 握手。
// 自动模式且有多台服务器时，每台服务器只握手一条线路，选最快的；
// 然后确认选中的线路能握手，不通就按顺序换同服务器的其他 TCP 线路，省掉看门狗约一分钟的判定。
func (c *Controller) pickLine() {
	c.mu.Lock()
	if c.opts.Probes.TCP == nil || len(c.nodes) == 0 {
		c.mu.Unlock()
		return
	}
	list := append([]nodes.Node(nil), c.nodes...)
	cur := c.index
	if cur < 0 || cur >= len(list) {
		cur = 0
	}
	bad := map[int]bool{}
	for i, n := range list {
		bad[i] = c.health[nodeKey(n)].Bad()
	}
	servers := c.serverOrderLocked()
	auto := !c.settings.ManualPick && len(servers) > 1
	if auto {
		c.detail = "正在选择最快的服务器…"
	}
	c.mu.Unlock()
	c.emit()

	firstTCP := func(server string) int {
		fallback := -1
		for i, n := range list {
			if n.Server != server || n.Type == "hysteria2" {
				continue
			}
			if fallback < 0 {
				fallback = i
			}
			if !bad[i] {
				return i
			}
		}
		return fallback
	}

	chosen, why := cur, ""
	tested := map[int]probeResult{}
	if auto {
		var idxs []int
		for _, s := range servers {
			i := firstTCP(s)
			if list[cur].Server == s && list[cur].Type != "hysteria2" && !bad[cur] {
				i = cur
			}
			if i >= 0 {
				idxs = append(idxs, i)
			}
		}
		res := c.probeLines(list, idxs)
		sort.SliceStable(res, func(a, b int) bool {
			if res[a].ok != res[b].ok {
				return res[a].ok
			}
			return res[a].rtt < res[b].rtt
		})
		for _, r := range res {
			tested[r.idx] = r
		}
		if len(res) > 0 && res[0].ok {
			chosen = res[0].idx
			why = fmt.Sprintf("（握手 %d ms）", res[0].rtt.Milliseconds())
		}
	}

	if list[chosen].Type != "hysteria2" {
		r, ok := tested[chosen]
		if !ok {
			r = c.probeLines(list, []int{chosen})[0]
			tested[chosen] = r
		}
		if !r.ok {
			var others []int
			for i, n := range list {
				if i != chosen && n.Server == list[chosen].Server && n.Type != "hysteria2" {
					if _, done := tested[i]; !done {
						others = append(others, i)
					}
				}
			}
			for _, r := range c.probeLines(list, others) {
				tested[r.idx] = r
			}
			for i := range list {
				if r, ok := tested[i]; ok && r.ok && list[i].Server == list[chosen].Server {
					chosen, why = i, "（原线路握手失败）"
					break
				}
			}
		}
	}

	c.mu.Lock()
	reachable := map[string]bool{}
	for i, r := range tested {
		if r.ok {
			reachable[list[i].Server] = true
		}
	}
	for i, r := range tested {
		key := nodeKey(list[i])
		switch {
		case r.ok:
			c.health[key] = HealthReachable
		case reachable[list[i].Server]:
			// 同服务器别的线路通，才把这条记为被封；全不通可能只是网络抖动。
			c.health[key] = HealthBlocked
		}
	}
	changed := chosen != c.index && chosen < len(c.nodes) && nodeKey(c.nodes[chosen]) == nodeKey(list[chosen])
	if changed {
		c.index = chosen
		c.settings.Selected = chosen
		c.fail = failover.State{Index: chosen, LastIPWall: c.fail.LastIPWall}
	}
	label := c.placeLocked(list[chosen].Server) + " · " + c.lineLabelLocked(chosen)
	c.detail = ""
	c.mu.Unlock()
	if changed {
		c.saveSettings()
		c.log("自动选择 " + label + why)
	}
	c.emit()
}

// AutoPick 报告是否按速度自动选服务器。
func (c *Controller) AutoPick() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.settings.ManualPick
}

// SetAutoPick 打开或关闭自动选服务器。打开且已连接时，立即重新选一次。
func (c *Controller) SetAutoPick(on bool) {
	c.mu.Lock()
	if c.settings.ManualPick == !on {
		c.mu.Unlock()
		return
	}
	c.settings.ManualPick = !on
	connected := c.phase == PhaseConnected
	before := c.index
	c.mu.Unlock()
	c.saveSettings()
	c.emit()
	if on && connected {
		go func() {
			c.pickLine()
			c.mu.Lock()
			moved := c.index != before
			c.mu.Unlock()
			if moved {
				c.restart("已自动切换到更快的服务器", StatusConnected)
			}
		}()
	}
}
