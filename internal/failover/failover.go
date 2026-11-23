// Package failover 实现与看门狗一致的纯决策：
// 每轮探测看 generate_204；连续失败满 3 次后先看国内网络，
// 国内不通则不归咎服务器；否则用同一轮对所有 TCP 端口的直连结果判断原因：
// 只有当前端口不通是「端口被封」，同一服务器所有 TCP 端口都不通是「IP 被墙」，
// 端口能连但代理不通是「被干扰」。切换时跳过已知不通的端口。
package failover

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// FailureThreshold 是触发切换前需要的连续失败次数。
	FailureThreshold = 3
	// IPWallCooldown 是同一服务器「IP 疑似被墙」通知的最短间隔。
	IPWallCooldown = 30 * time.Minute
	// ToastIPWall 是全部端口 TCP 失败时的通知原文。
	ToastIPWall = "服务器 IP 疑似被墙，请更换 IP"
	// LogLocalDown 是国内网络不通时写入日志的说明。
	LogLocalDown = "本地网络不可用，暂不切换节点"
)

// Action 是一次探测之后要做的事。
type Action int

const (
	// ActionHealthy 表示代理可用，失败计数已清零。
	ActionHealthy Action = iota
	// ActionFailing 表示本次失败，但还没到切换阈值。
	ActionFailing
	// ActionLocalDown 表示失败已达阈值，但国内网络也不通，不切换。
	ActionLocalDown
	// ActionSwitch 表示应切换到 Decision.NewIndex。
	ActionSwitch
	// ActionRetry 表示没有可用的备用节点，只能重启当前节点。
	ActionRetry
)

// Cause 是当前节点被判定不可用的原因。
type Cause int

const (
	CauseNone Cause = iota
	// CausePortBlocked：当前端口 TCP 不通，同服务器还有别的端口通。
	CausePortBlocked
	// CauseInterfered：当前端口 TCP 能连，但经代理访问失败。
	CauseInterfered
	// CauseIPWall：同一服务器所有 TCP 端口都不通。
	CauseIPWall
	// CauseUDP：Hysteria2 失败。UDP 没法用 TCP 直连判断。
	CauseUDP
)

// NodeRef 是决策所需的最小节点信息。调用方应已按 Reality 在前、Hysteria2 在后排好序。
// Label（如「线路 2」）和 Place（如「日本 · 东京」）非空时，通知和诊断用它们代替端口和 IP。
type NodeRef struct {
	Server   string
	Port     int
	Protocol string
	Label    string
	Place    string
}

func (n NodeRef) line() string {
	if n.Label != "" {
		return n.Label
	}
	if n.UDP() {
		return fmt.Sprintf("Hysteria2（UDP %d）", n.Port)
	}
	return fmt.Sprintf("端口 %d", n.Port)
}

func (n NodeRef) place() string {
	if n.Place != "" {
		return n.Place
	}
	return n.Server
}

// UDP 为真时这个节点走 UDP，TCP 探测结果对它没有意义。
func (n NodeRef) UDP() bool { return n.Protocol == "hysteria2" }

// TCPResult 是一次诊断里各节点的 TCP 直连结果，键是节点下标。UDP 节点不放进来。
type TCPResult map[int]bool

// Probe 是一轮探测结果。健康只看 Generate204OK。
// TCP 只在失败达到阈值且国内网络正常时才需要填写。
type Probe struct {
	Generate204OK bool
	TCP           TCPResult
}

// Domestic 是国内连通性。Baidu 或 223.5.5.5 任一成功即视为国内网络可用。
type Domestic struct {
	Checked  bool
	BaiduOK  bool
	AliDNSOK bool
}

// Up 在已检查且百度或阿里 DNS 可达时为真。
func (d Domestic) Up() bool {
	return d.Checked && (d.BaiduOK || d.AliDNSOK)
}

// State 是看门狗在两次探测之间记住的状态。
type State struct {
	Index               int
	ConsecutiveFailures int
	// LastIPWall 按服务器地址记录上次发出「IP 被墙」通知的时间。
	LastIPWall map[string]time.Time
}

// Decision 是 Step 给出的结论。Toast 非空时应弹出 Windows 通知；IPWall 为真时再发一条 ToastIPWall。
type Decision struct {
	Action       Action
	NewIndex     int
	Failures     int
	Cause        Cause
	Toast        string
	IPWall       bool
	WalledServer string
	Log          string
}

// Walled 返回所有 TCP 端口都探测失败的服务器。没有 TCP 节点或没有探测结果的服务器不算。
func Walled(nodes []NodeRef, tcp TCPResult) map[string]bool {
	seen := map[string]bool{}
	anyOK := map[string]bool{}
	for i, n := range nodes {
		if n.UDP() {
			continue
		}
		ok, known := tcp[i]
		if !known {
			continue
		}
		seen[n.Server] = true
		if ok {
			anyOK[n.Server] = true
		}
	}
	out := map[string]bool{}
	for s := range seen {
		if !anyOK[s] {
			out[s] = true
		}
	}
	return out
}

// Step 根据当前状态和本轮探测计算下一步。now 只用于 IP 被墙通知的限频。
// 函数不修改入参里的 map。
func Step(now time.Time, nodes []NodeRef, st State, probe Probe, domestic Domestic) (State, Decision) {
	st.LastIPWall = cloneTimes(st.LastIPWall)
	if len(nodes) == 0 {
		return st, Decision{Action: ActionHealthy, Log: "没有可用节点"}
	}
	if st.Index < 0 || st.Index >= len(nodes) {
		st.Index = 0
	}
	if probe.Generate204OK {
		st.ConsecutiveFailures = 0
		return st, Decision{Action: ActionHealthy, NewIndex: st.Index, Log: "generate_204 正常"}
	}

	st.ConsecutiveFailures++
	if st.ConsecutiveFailures < FailureThreshold {
		return st, Decision{
			Action:   ActionFailing,
			NewIndex: st.Index,
			Failures: st.ConsecutiveFailures,
			Log:      fmt.Sprintf("代理探测失败 %d/%d", st.ConsecutiveFailures, FailureThreshold),
		}
	}

	if !domestic.Up() {
		st.ConsecutiveFailures = 0
		return st, Decision{Action: ActionLocalDown, NewIndex: st.Index, Log: LogLocalDown}
	}
	st.ConsecutiveFailures = 0

	cur := nodes[st.Index]
	walled := Walled(nodes, probe.TCP)
	cause := classify(st.Index, cur, probe.TCP, walled)
	dec := Decision{Cause: cause, NewIndex: st.Index}
	if walled[cur.Server] {
		dec.WalledServer = cur.Server
		last := st.LastIPWall[cur.Server]
		if last.IsZero() || now.Sub(last) >= IPWallCooldown {
			dec.IPWall = true
			st.LastIPWall[cur.Server] = now
		}
	}

	next := pickNext(nodes, st.Index, probe.TCP, walled)
	if next < 0 {
		dec.Action = ActionRetry
		dec.Log = describe(cur, cause) + "，没有可用的备用节点，重启当前节点重试"
		return st, dec
	}
	st.Index = next
	dec.Action = ActionSwitch
	dec.NewIndex = next
	dec.Toast = switchToast(cur, nodes[next], cause)
	dec.Log = dec.Toast
	return st, dec
}

func classify(idx int, cur NodeRef, tcp TCPResult, walled map[string]bool) Cause {
	if walled[cur.Server] {
		return CauseIPWall
	}
	if cur.UDP() {
		return CauseUDP
	}
	if ok, known := tcp[idx]; known && !ok {
		return CausePortBlocked
	}
	return CauseInterfered
}

// pickNext 从当前节点之后按顺序找第一个可能可用的节点：
// 已知 TCP 不通的端口、被墙服务器上的 TCP 端口都跳过；UDP 节点没有反证，总是候选。
func pickNext(nodes []NodeRef, idx int, tcp TCPResult, walled map[string]bool) int {
	cur := nodes[idx]
	for k := 1; k < len(nodes); k++ {
		j := (idx + k) % len(nodes)
		n := nodes[j]
		if n == cur {
			continue
		}
		if !n.UDP() {
			if walled[n.Server] {
				continue
			}
			if ok, known := tcp[j]; known && !ok {
				continue
			}
		}
		return j
	}
	return -1
}

func target(n NodeRef) string {
	if n.Label != "" {
		if n.Place != "" {
			return n.Place + " " + n.Label
		}
		return n.Label
	}
	if n.UDP() {
		return fmt.Sprintf("Hysteria2（UDP %d）", n.Port)
	}
	return strconv.Itoa(n.Port)
}

func describe(cur NodeRef, cause Cause) string {
	switch cause {
	case CausePortBlocked:
		return cur.line() + " 疑似被封"
	case CauseInterfered:
		return cur.line() + " 能连通但代理不通，疑似被干扰"
	case CauseIPWall:
		return fmt.Sprintf("服务器 %s 所有 TCP 线路都不通，IP 疑似被墙", cur.place())
	case CauseUDP:
		return cur.line() + " 连续失败"
	default:
		return cur.line() + " 连续失败"
	}
}

func switchToast(cur, next NodeRef, cause Cause) string {
	to := target(next)
	if next.Label != "" && next.Server == cur.Server {
		to = next.Label
	}
	switch cause {
	case CausePortBlocked:
		return fmt.Sprintf("%s 疑似被封，已自动切换到 %s", cur.line(), to)
	case CauseInterfered:
		return fmt.Sprintf("%s 能连通但代理不通（疑似被干扰），已切换到 %s", cur.line(), to)
	case CauseIPWall:
		return fmt.Sprintf("服务器所有 TCP 线路都不通，已切换到 %s", to)
	case CauseUDP:
		return fmt.Sprintf("%s 不可用，已切换到 %s", cur.line(), to)
	default:
		return fmt.Sprintf("%s 不可用，已切换到 %s", cur.line(), to)
	}
}

// Summarize 把一次手动诊断写成几行中文，按服务器分组。
func Summarize(nodes []NodeRef, tcp TCPResult) []string {
	type group struct {
		ok, bad   []int
		udp       []int
		okL, badL []string
		udpL      []string
		place     string
	}
	groups := map[string]*group{}
	var order []string
	labelled := true
	for i, n := range nodes {
		if n.Label == "" {
			labelled = false
		}
		g := groups[n.Server]
		if g == nil {
			g = &group{place: n.place()}
			groups[n.Server] = g
			order = append(order, n.Server)
		}
		if n.UDP() {
			g.udp = append(g.udp, n.Port)
			g.udpL = append(g.udpL, n.Label)
			continue
		}
		ok, known := tcp[i]
		if !known {
			continue
		}
		if ok {
			g.ok = append(g.ok, n.Port)
			g.okL = append(g.okL, n.Label)
		} else {
			g.bad = append(g.bad, n.Port)
			g.badL = append(g.badL, n.Label)
		}
	}
	var out []string
	for _, s := range order {
		g := groups[s]
		prefix := ""
		if len(order) > 1 {
			prefix = g.place + "："
		}
		if labelled {
			switch {
			case len(g.ok) == 0 && len(g.bad) > 0:
				out = append(out, prefix+"所有线路都不通，服务器 IP 疑似被墙（或服务器已关机）")
			case len(g.bad) > 0:
				out = append(out, prefix+strings.Join(g.badL, "、")+" 疑似被封；"+strings.Join(g.okL, "、")+" 正常")
			case len(g.ok) > 0:
				out = append(out, prefix+"线路全部正常")
			}
			if len(g.udp) > 0 {
				out = append(out, prefix+strings.Join(g.udpL, "、")+" 是 UDP 线路，连上后看延迟")
			}
			continue
		}
		switch {
		case len(g.ok) == 0 && len(g.bad) > 0:
			out = append(out, prefix+"所有 TCP 端口都不通，服务器 IP 疑似被墙（或服务器已关机）")
		case len(g.bad) > 0:
			out = append(out, prefix+"端口 "+joinPorts(g.bad)+" 不通，疑似被封；"+joinPorts(g.ok)+" 可达")
		case len(g.ok) > 0:
			out = append(out, prefix+"TCP 端口 "+joinPorts(g.ok)+" 全部可达")
		}
		if len(g.udp) > 0 {
			out = append(out, prefix+"Hysteria2（UDP "+joinPorts(g.udp)+"）无法直接探测，连接后看延迟")
		}
	}
	return out
}

func joinPorts(ps []int) string {
	ps = append([]int(nil), ps...)
	sort.Ints(ps)
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, "、")
}

func cloneTimes(in map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
