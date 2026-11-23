// Package nodes 解析 proxy-nodes.json、sing-box 出站，以及 vless:// 与 hysteria2:// 链接。
package nodes

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Node 是客户端内部使用的一个代理节点。Type 为 vless 或 hysteria2。
type Node struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Server       string   `json:"server"`
	Port         int      `json:"port"`
	UUID         string   `json:"uuid,omitempty"`
	Flow         string   `json:"flow,omitempty"`
	SNI          string   `json:"sni,omitempty"`
	PublicKey    string   `json:"public_key,omitempty"`
	ShortID      string   `json:"short_id,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	Password     string   `json:"password,omitempty"`
	Insecure     bool     `json:"insecure,omitempty"`
	ALPN         []string `json:"alpn,omitempty"`
	Obfs         string   `json:"obfs,omitempty"`
	ObfsPassword string   `json:"obfs_password,omitempty"`
}

// Label 是界面上显示的节点名。
func (n Node) Label() string {
	proto := "Reality"
	if n.Type == "hysteria2" {
		proto = "Hysteria2"
	}
	name := n.Name
	if name == "" {
		name = proto
	}
	return fmt.Sprintf("%s · %s · %d", name, proto, n.Port)
}

// docNets 是 RFC 5737 / 3849 的文档测试网段，这些地址永远连不通。
var docNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// IsExample 判断节点是不是示例文件里的占位节点：文档测试地址、example 域名或「请换成…」的占位密码。
func IsExample(n Node) bool {
	if strings.Contains(n.Password, "请换成") || strings.Contains(n.UUID, "请换成") {
		return true
	}
	host := strings.ToLower(strings.TrimSuffix(n.Server, "."))
	if host == "example.com" || strings.HasSuffix(host, ".example.com") || strings.HasSuffix(host, ".example") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, d := range docNets {
			if d.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// AllExamples 在列表非空且每个节点都是示例时为真。
func AllExamples(list []Node) bool {
	if len(list) == 0 {
		return false
	}
	for _, n := range list {
		if !IsExample(n) {
			return false
		}
	}
	return true
}

// Normalize 去重并按 Reality、Hysteria2 的故障切换顺序排列。
func Normalize(in []Node) []Node {
	return Order(dedupe(in))
}

// Order 把 Reality / VLESS 放在前面，Hysteria2 放在后面，组内保持原顺序。
func Order(in []Node) []Node {
	var reality, hy2 []Node
	for _, n := range in {
		if n.Type == "hysteria2" {
			hy2 = append(hy2, n)
		} else {
			reality = append(reality, n)
		}
	}
	return append(reality, hy2...)
}

// Parse 接受 JSON 文本，或一行一个的分享链接。
func Parse(text string) ([]Node, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("内容为空")
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return nil, fmt.Errorf("JSON 无法解析：%w", err)
		}
		list, err := parseValue(v)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("没有解析到节点")
		}
		return Order(dedupe(list)), nil
	}
	var list []Node
	var errs []string
	for _, line := range strings.Fields(text) {
		n, err := ParseLink(line)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		list = append(list, n)
	}
	if len(list) == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("%s", errs[0])
		}
		return nil, fmt.Errorf("没有解析到链接")
	}
	return Order(dedupe(list)), nil
}

// ParseLink 解析 vless://、hysteria2:// 或 hy2://。
func ParseLink(raw string) (Node, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, fmt.Errorf("链接无效：%v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return Node{}, fmt.Errorf("链接缺少主机或端口")
	}
	q := u.Query()
	name := u.Fragment
	if name == "" {
		name = first(q, "remark", "name")
	}
	switch scheme {
	case "vless":
		n := Node{
			Name:        name,
			Type:        "vless",
			Server:      host,
			Port:        port,
			UUID:        u.User.Username(),
			Flow:        first(q, "flow"),
			SNI:         first(q, "sni", "serverName", "peer"),
			PublicKey:   first(q, "pbk", "publicKey", "public_key"),
			ShortID:     first(q, "sid", "shortId", "short_id"),
			Fingerprint: first(q, "fp", "fingerprint"),
		}
		if n.UUID == "" {
			return Node{}, fmt.Errorf("vless 链接缺少 UUID")
		}
		if n.Name == "" {
			n.Name = fmt.Sprintf("Reality-%d", n.Port)
		}
		if n.Flow == "" && n.PublicKey != "" {
			n.Flow = "xtls-rprx-vision"
		}
		if n.Fingerprint == "" {
			n.Fingerprint = "chrome"
		}
		return n, nil
	case "hysteria2", "hy2":
		pass := ""
		if u.User != nil {
			if p, ok := u.User.Password(); ok {
				pass = u.User.Username() + ":" + p
			} else {
				pass = u.User.Username()
			}
		}
		n := Node{
			Name:         name,
			Type:         "hysteria2",
			Server:       host,
			Port:         port,
			Password:     pass,
			SNI:          first(q, "sni", "serverName", "peer"),
			Insecure:     truthy(first(q, "insecure", "allowInsecure", "skip-cert-verify")),
			Obfs:         first(q, "obfs"),
			ObfsPassword: first(q, "obfs-password", "obfsPassword"),
		}
		if alpn := first(q, "alpn"); alpn != "" {
			n.ALPN = splitComma(alpn)
		}
		if n.Password == "" {
			return Node{}, fmt.Errorf("hysteria2 链接缺少密码")
		}
		if n.Name == "" {
			n.Name = fmt.Sprintf("Hysteria2-%d", n.Port)
		}
		return n, nil
	default:
		return Node{}, fmt.Errorf("不支持的链接类型：%s", scheme)
	}
}

func parseValue(v any) ([]Node, error) {
	switch t := v.(type) {
	case []any:
		var out []Node
		for _, item := range t {
			switch x := item.(type) {
			case string:
				n, err := ParseLink(x)
				if err != nil {
					return nil, err
				}
				out = append(out, n)
			case map[string]any:
				list, err := parseObject(x)
				if err != nil {
					return nil, err
				}
				out = append(out, list...)
			default:
				return nil, fmt.Errorf("节点格式无法识别")
			}
		}
		return out, nil
	case map[string]any:
		return parseObject(t)
	default:
		return nil, fmt.Errorf("JSON 须为对象或数组")
	}
}

func parseObject(m map[string]any) ([]Node, error) {
	if v, ok := m["nodes"]; ok {
		return parseValue(v)
	}
	if v, ok := m["outbounds"]; ok {
		return parseValue(v)
	}
	if v, ok := m["links"]; ok {
		return parseValue(v)
	}
	if isBundle(m) {
		return parseBundle(m)
	}
	n, err := parseNode(m)
	if err != nil {
		return nil, err
	}
	if n.Type == "direct" || n.Type == "block" || n.Type == "dns" || n.Type == "selector" || n.Type == "urltest" {
		return nil, nil
	}
	return []Node{n}, nil
}

func isBundle(m map[string]any) bool {
	if has(m, "type", "protocol") {
		return false
	}
	if has(m, "reality_ports", "realityPorts", "ports") {
		return true
	}
	if _, ok := m["hysteria2"].(map[string]any); ok {
		return true
	}
	if _, ok := m["hy2"].(map[string]any); ok {
		return true
	}
	if has(m, "hysteria2_port", "hy2_port", "hysteria2Port") {
		return true
	}
	return false
}

func parseBundle(m map[string]any) ([]Node, error) {
	server := str(m, "server", "address", "host", "ip")
	if server == "" {
		return nil, fmt.Errorf("缺少服务器地址")
	}
	uuid := str(m, "uuid", "id", "uid")
	flow := str(m, "flow")
	sni := str(m, "sni", "server_name", "serverName")
	pub := str(m, "public_key", "publicKey", "pbk")
	sid := str(m, "short_id", "shortId", "sid")
	fp := str(m, "fingerprint", "fp")
	if fp == "" {
		fp = "chrome"
	}
	remark := str(m, "remark", "name")
	applyTLSMap(m, &sni, &pub, &sid, &fp)

	var out []Node
	ports := ints(m, "reality_ports", "realityPorts", "ports")
	if len(ports) > 0 {
		if uuid == "" {
			return nil, fmt.Errorf("Reality 节点缺少 UUID")
		}
		if flow == "" {
			flow = "xtls-rprx-vision"
		}
		for _, p := range ports {
			name := fmt.Sprintf("Reality-%d", p)
			if remark != "" {
				name = fmt.Sprintf("%s-%d", remark, p)
			}
			out = append(out, Node{
				Name: name, Type: "vless", Server: server, Port: p, UUID: uuid,
				Flow: flow, SNI: sni, PublicKey: pub, ShortID: sid, Fingerprint: fp,
			})
		}
	}

	if hy, ok := m["hysteria2"].(map[string]any); ok {
		n, err := hyNode(server, remark, hy)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	} else if hy, ok := m["hy2"].(map[string]any); ok {
		n, err := hyNode(server, remark, hy)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	} else if p := integer(m, "hysteria2_port", "hy2_port", "hysteria2Port"); p > 0 {
		pass := str(m, "hysteria2_password", "hy2_password", "hysteria2Password")
		if pass == "" {
			return nil, fmt.Errorf("Hysteria2 缺少密码")
		}
		hsni := str(m, "hysteria2_sni", "hy2_sni")
		if hsni == "" {
			hsni = sni
		}
		insecure, _ := boolean(m, "hysteria2_insecure", "hy2_insecure")
		name := fmt.Sprintf("Hysteria2-%d", p)
		if remark != "" {
			name = remark + "-hy2"
		}
		out = append(out, Node{
			Name: name, Type: "hysteria2", Server: server, Port: p, Password: pass, SNI: hsni, Insecure: insecure,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有解析到节点")
	}
	return out, nil
}

func hyNode(server, remark string, hy map[string]any) (Node, error) {
	port := integer(hy, "port", "server_port")
	pass := str(hy, "password", "pwd", "auth")
	if port == 0 || pass == "" {
		return Node{}, fmt.Errorf("Hysteria2 缺少端口或密码")
	}
	sni := str(hy, "sni", "server_name", "serverName")
	insecure, ok := boolean(hy, "insecure", "skip_cert_verify", "allowInsecure")
	if !ok {
		insecure = false
	}
	name := str(hy, "name", "remark")
	if name == "" {
		name = fmt.Sprintf("Hysteria2-%d", port)
		if remark != "" {
			name = remark + "-hy2"
		}
	}
	n := Node{
		Name: name, Type: "hysteria2", Server: server, Port: port, Password: pass, SNI: sni, Insecure: insecure,
		Obfs: str(hy, "obfs"), ObfsPassword: str(hy, "obfs_password", "obfs-password", "obfsPassword"),
	}
	if a := str(hy, "alpn"); a != "" {
		n.ALPN = splitComma(a)
	}
	return n, nil
}

func parseNode(m map[string]any) (Node, error) {
	typ := strings.ToLower(str(m, "type", "protocol"))
	switch typ {
	case "reality", "vless-reality", "vless_reality", "vless+reality":
		typ = "vless"
	case "hy2":
		typ = "hysteria2"
	}
	if typ == "direct" || typ == "block" || typ == "dns" || typ == "selector" || typ == "urltest" {
		return Node{Type: typ}, nil
	}
	server := str(m, "server", "address", "host")
	port := integer(m, "port", "server_port")
	if server == "" || port == 0 {
		return Node{}, fmt.Errorf("节点缺少服务器或端口")
	}
	n := Node{
		Name:         str(m, "name", "remark", "tag", "ps"),
		Type:         typ,
		Server:       server,
		Port:         port,
		UUID:         str(m, "uuid", "id", "uid"),
		Flow:         str(m, "flow"),
		SNI:          str(m, "sni", "server_name", "serverName"),
		PublicKey:    str(m, "public_key", "publicKey", "pbk"),
		ShortID:      str(m, "short_id", "shortId", "sid"),
		Fingerprint:  str(m, "fingerprint", "fp"),
		Password:     str(m, "password", "pwd", "auth"),
		Obfs:         str(m, "obfs"),
		ObfsPassword: str(m, "obfs_password", "obfs-password", "obfsPassword"),
	}
	if b, ok := boolean(m, "insecure", "skip_cert_verify", "allowInsecure"); ok {
		n.Insecure = b
	}
	applyTLSMap(m, &n.SNI, &n.PublicKey, &n.ShortID, &n.Fingerprint)
	if tls, ok := m["tls"].(map[string]any); ok {
		if b, ok := boolean(tls, "insecure"); ok {
			n.Insecure = b
		}
		if alpn, ok := tls["alpn"].([]any); ok {
			for _, a := range alpn {
				if s, ok := a.(string); ok && s != "" {
					n.ALPN = append(n.ALPN, s)
				}
			}
		}
	}
	if ob, ok := m["obfs"].(map[string]any); ok {
		n.Obfs = str(ob, "type")
		n.ObfsPassword = str(ob, "password")
	}
	if n.Type != "vless" && n.Type != "hysteria2" {
		return Node{}, fmt.Errorf("不支持的协议：%s", typ)
	}
	if n.Type == "vless" && n.UUID == "" {
		return Node{}, fmt.Errorf("vless 节点缺少 UUID")
	}
	if n.Type == "hysteria2" && n.Password == "" {
		return Node{}, fmt.Errorf("hysteria2 节点缺少密码")
	}
	if n.Type == "vless" && n.PublicKey != "" && n.Flow == "" {
		n.Flow = "xtls-rprx-vision"
	}
	if n.Type == "vless" && n.Fingerprint == "" {
		n.Fingerprint = "chrome"
	}
	if n.Name == "" {
		if n.Type == "hysteria2" {
			n.Name = fmt.Sprintf("Hysteria2-%d", n.Port)
		} else {
			n.Name = fmt.Sprintf("Reality-%d", n.Port)
		}
	}
	return n, nil
}

func applyTLSMap(m map[string]any, sni, pub, sid, fp *string) {
	tls, ok := m["tls"].(map[string]any)
	if !ok {
		return
	}
	if *sni == "" {
		*sni = str(tls, "server_name", "serverName", "sni")
	}
	if utls, ok := tls["utls"].(map[string]any); ok && *fp == "" {
		*fp = str(utls, "fingerprint", "fp")
	}
	if reality, ok := tls["reality"].(map[string]any); ok {
		if *pub == "" {
			*pub = str(reality, "public_key", "publicKey")
		}
		if *sid == "" {
			*sid = str(reality, "short_id", "shortId")
		}
	}
}

func dedupe(in []Node) []Node {
	seen := map[string]bool{}
	var out []Node
	for _, n := range in {
		key := strings.Join([]string{n.Type, n.Server, strconv.Itoa(n.Port), n.UUID, n.Password}, "|")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	return out
}

func has(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		}
	}
	return ""
}

func integer(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if n, ok := asInt(m[k]); ok {
			return n
		}
	}
	return 0
}

func ints(m map[string]any, keys ...string) []int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case []any:
			var out []int
			for _, item := range t {
				if n, ok := asInt(item); ok && n > 0 {
					out = append(out, n)
				}
			}
			if len(out) > 0 {
				return out
			}
		case string:
			var out []int
			for _, part := range splitComma(t) {
				if n, err := strconv.Atoi(part); err == nil && n > 0 {
					out = append(out, n)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func boolean(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case bool:
			return t, true
		case string:
			return truthy(t), true
		case float64:
			return t != 0, true
		}
	}
	return false, false
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case json.Number:
		i, err := t.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		return i, err == nil
	default:
		return 0, false
	}
}

func first(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
