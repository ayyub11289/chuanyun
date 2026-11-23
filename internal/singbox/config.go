// Package singbox 生成 sing-box 1.14 客户端配置：混合入站始终开启，
// TUN 与系统代理二选一，中国域名/IP 直连，其余走当前节点。
package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"

	"chuanyun/internal/nodes"
)

// ModeTUN 是全设备 TUN。ModeSystem 只开混合入站并交给系统代理。
const (
	ModeTUN    = "tun"
	ModeSystem = "system"
	MixedPort  = 2080
	// ClashPort 是本机 clash API，只给本进程读隧道流量，不对外。
	ClashPort = 9097
)

// TunPrefixes 是 TUN 网卡的本机地址。防泄漏按「本地地址在这些网段内」放行经过隧道的连接。
var TunPrefixes = []netip.Prefix{
	netip.MustParsePrefix("172.19.0.1/30"),
	netip.MustParsePrefix("fdfe:dcba:9876::1/126"),
}

// Input 是生成一份配置所需的参数。规则集路径为空时退回内置的 .cn 直连规则。
type Input struct {
	Node        nodes.Node
	MixedPort   int
	Mode        string
	GeoSitePath string
	GeoIPPath   string
	ExePath     string
	// ClashSecret 非空时打开 127.0.0.1 上的 clash API，用于统计 proxy 出站流量。
	ClashSecret string
	// DirectApps 是始终直连的程序文件名（如 WeChat.exe），不区分大小写。
	DirectApps []string
}

// directAppPatterns 把程序名转成不区分大小写、匹配路径末段的正则。
func directAppPatterns(apps []string) []string {
	var out []string
	for _, a := range apps {
		if a == "" {
			continue
		}
		out = append(out, `(?i)(^|[\\/])`+regexp.QuoteMeta(a)+`$`)
	}
	return out
}

// Build 返回缩进后的 sing-box JSON。
func Build(in Input) ([]byte, error) {
	if err := validate(in.Node); err != nil {
		return nil, err
	}
	if in.MixedPort == 0 {
		in.MixedPort = MixedPort
	}
	if in.Mode == "" {
		in.Mode = ModeTUN
	}

	cfg := map[string]any{
		"log":      map[string]any{"level": "warn", "timestamp": true},
		"dns":      dnsSection(in),
		"inbounds": inbounds(in),
		"outbounds": []any{
			outbound(in.Node),
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": routeSection(in),
	}
	if in.ClashSecret != "" {
		cfg["experimental"] = map[string]any{
			"clash_api": map[string]any{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", ClashPort),
				"secret":              in.ClashSecret,
			},
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func validate(n nodes.Node) error {
	if n.Server == "" || n.Port <= 0 || n.Port > 65535 {
		return fmt.Errorf("节点地址或端口无效")
	}
	switch n.Type {
	case "vless":
		if n.UUID == "" {
			return fmt.Errorf("vless 节点缺少 UUID")
		}
	case "hysteria2":
		if n.Password == "" {
			return fmt.Errorf("hysteria2 节点缺少密码")
		}
	default:
		return fmt.Errorf("不支持的协议：%s", n.Type)
	}
	return nil
}

func dnsSection(in Input) map[string]any {
	rules := []any{}
	// 直连程序要拿国内解析结果，否则会按国外 CDN 地址直连过去。
	// 走系统 DNS 客户端（svchost）的查询认不出发起程序，只对自带解析的程序生效。
	if p := directAppPatterns(in.DirectApps); len(p) > 0 {
		rules = append(rules, map[string]any{
			"process_path_regex": p,
			"action":             "route",
			"server":             "local-dns",
		})
	}
	if in.GeoSitePath != "" {
		rules = append(rules, map[string]any{
			"rule_set": "geosite-cn",
			"action":   "route",
			"server":   "local-dns",
		})
	} else {
		rules = append(rules, map[string]any{
			"domain_suffix": []string{".cn"},
			"action":        "route",
			"server":        "local-dns",
		})
	}
	return map[string]any{
		"servers": []any{
			map[string]any{"type": "https", "tag": "remote-dns", "server": "1.1.1.1", "detour": "proxy"},
			map[string]any{"type": "udp", "tag": "local-dns", "server": "223.5.5.5", "server_port": 53},
		},
		"rules":    rules,
		"final":    "remote-dns",
		"strategy": "ipv4_only",
	}
}

func inbounds(in Input) []any {
	mixed := map[string]any{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      "127.0.0.1",
		"listen_port": in.MixedPort,
	}
	if in.Mode != ModeTUN {
		return []any{mixed}
	}
	tun := map[string]any{
		"type":         "tun",
		"tag":          "tun-in",
		"address":      []string{TunPrefixes[0].String(), TunPrefixes[1].String()},
		"auto_route":   true,
		"strict_route": true,
		"stack":        "mixed",
		"mtu":          1500,
	}
	return []any{mixed, tun}
}

func outbound(n nodes.Node) map[string]any {
	switch n.Type {
	case "hysteria2":
		tls := map[string]any{
			"enabled":     true,
			"server_name": sniOr(n),
		}
		if n.Insecure {
			tls["insecure"] = true
		}
		if len(n.ALPN) > 0 {
			tls["alpn"] = n.ALPN
		} else {
			tls["alpn"] = []string{"h3"}
		}
		o := map[string]any{
			"type":        "hysteria2",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"password":    n.Password,
			"tls":         tls,
		}
		if n.Obfs != "" {
			o["obfs"] = map[string]any{"type": n.Obfs, "password": n.ObfsPassword}
		}
		return o
	default:
		fp := n.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		tls := map[string]any{
			"enabled":     true,
			"server_name": sniOr(n),
			"utls":        map[string]any{"enabled": true, "fingerprint": fp},
		}
		if n.PublicKey != "" {
			reality := map[string]any{"enabled": true, "public_key": n.PublicKey}
			if n.ShortID != "" {
				reality["short_id"] = n.ShortID
			}
			tls["reality"] = reality
		}
		flow := n.Flow
		if flow == "" && n.PublicKey != "" {
			flow = "xtls-rprx-vision"
		}
		o := map[string]any{
			"type":        "vless",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"uuid":        n.UUID,
			"tls":         tls,
		}
		if flow != "" {
			o["flow"] = flow
		}
		return o
	}
}

func sniOr(n nodes.Node) string {
	if n.SNI != "" {
		return n.SNI
	}
	return "www.microsoft.com"
}

func routeSection(in Input) map[string]any {
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"port": 53, "action": "hijack-dns"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	// 本程序的国内探测和端口 TCP 探测会被 TUN 捕获，必须直连；
	// 但经混合端口发出的 generate_204、延迟和测速必须走代理，所以限定 tun-in。
	if in.Mode == ModeTUN {
		if in.ExePath != "" {
			rules = append(rules, map[string]any{
				"inbound":      []string{"tun-in"},
				"process_path": []string{in.ExePath},
				"action":       "route",
				"outbound":     "direct",
			})
		}
		rules = append(rules, map[string]any{
			"inbound":      []string{"tun-in"},
			"process_name": []string{"Chuanyun.exe"},
			"action":       "route",
			"outbound":     "direct",
		})
	}
	// 用户指定的直连程序：TUN 和混合端口进来的都放行，出口是本机网络。
	if p := directAppPatterns(in.DirectApps); len(p) > 0 {
		rules = append(rules, map[string]any{
			"process_path_regex": p,
			"action":             "route",
			"outbound":           "direct",
		})
	}
	rules = append(rules, map[string]any{
		"ip_is_private": true,
		"action":        "route",
		"outbound":      "direct",
	})

	route := map[string]any{
		"rules":                   rules,
		"final":                   "proxy",
		"auto_detect_interface":   true,
		"default_domain_resolver": "local-dns",
	}
	if in.GeoSitePath != "" && in.GeoIPPath != "" {
		route["rule_set"] = []any{
			map[string]any{"type": "local", "tag": "geosite-cn", "format": "binary", "path": in.GeoSitePath},
			map[string]any{"type": "local", "tag": "geoip-cn", "format": "binary", "path": in.GeoIPPath},
		}
		rules = append(rules, map[string]any{
			"rule_set": []string{"geosite-cn", "geoip-cn"},
			"action":   "route",
			"outbound": "direct",
		})
		route["rules"] = rules
		return route
	}
	rules = append(rules, map[string]any{
		"domain_suffix": []string{".cn"},
		"action":        "route",
		"outbound":      "direct",
	})
	rules = append(rules, map[string]any{
		"domain_keyword": []string{"baidu", "qq", "taobao", "alicdn", "aliyun", "bilibili", "weibo", "zhihu", "douyin", "jd", "tmall", "weixin", "163.com", "126.com"},
		"action":         "route",
		"outbound":       "direct",
	})
	route["rules"] = rules
	return route
}
