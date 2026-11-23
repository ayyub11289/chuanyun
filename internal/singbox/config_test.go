package singbox

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"chuanyun/internal/nodes"
)

func sampleReality() nodes.Node {
	return nodes.Node{
		Name: "Reality-443", Type: "vless", Server: "203.0.113.10", Port: 443,
		UUID: "b7e2c1a0-1234-4abc-8def-0123456789ab", Flow: "xtls-rprx-vision",
		SNI: "www.microsoft.com", PublicKey: "Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI",
		ShortID: "a1b2c3d4", Fingerprint: "chrome",
	}
}

func sampleHy2() nodes.Node {
	return nodes.Node{
		Name: "Hysteria2", Type: "hysteria2", Server: "203.0.113.10", Port: 36712,
		Password: "secret-password", SNI: "www.microsoft.com", Insecure: true,
	}
}

func TestBuildRealityTUNHasSplitAndMixed(t *testing.T) {
	raw, err := Build(Input{
		Node: sampleReality(), Mode: ModeTUN, MixedPort: 2080,
		GeoSitePath: "/tmp/geosite-cn.srs", GeoIPPath: "/tmp/geoip-cn.srs",
		ExePath: `C:\Chuanyun\Chuanyun.exe`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	inb := cfg["inbounds"].([]any)
	if len(inb) != 2 {
		t.Fatalf("inbounds=%d", len(inb))
	}
	if inb[0].(map[string]any)["type"] != "mixed" || inb[1].(map[string]any)["type"] != "tun" {
		t.Fatalf("inbounds=%v", inb)
	}
	text := string(raw)
	for _, want := range []string{"geosite-cn", "geoip-cn", "xtls-rprx-vision", "hijack-dns", "Chuanyun.exe", "223.5.5.5", "1.1.1.1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s\n%s", want, text)
		}
	}
}

func TestBuildSystemSkipsTUN(t *testing.T) {
	raw, err := Build(Input{Node: sampleHy2(), Mode: ModeSystem})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"type": "tun"`) {
		t.Fatal("system mode should not include tun")
	}
	if !strings.Contains(string(raw), "hysteria2") || !strings.Contains(string(raw), ".cn") {
		t.Fatalf("%s", raw)
	}
}

func selfDirectRules(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var cfg struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range cfg.Route.Rules {
		if _, ok := r["process_name"]; ok {
			out = append(out, r)
		}
		if _, ok := r["process_path"]; ok {
			out = append(out, r)
		}
	}
	return out
}

func TestSelfDirectOnlyAppliesToTUNInbound(t *testing.T) {
	raw, err := Build(Input{Node: sampleReality(), Mode: ModeTUN, ExePath: `C:\Chuanyun\Chuanyun.exe`})
	if err != nil {
		t.Fatal(err)
	}
	rules := selfDirectRules(t, raw)
	if len(rules) != 2 {
		t.Fatalf("rules=%v", rules)
	}
	for _, r := range rules {
		inb, _ := r["inbound"].([]any)
		if len(inb) != 1 || inb[0] != "tun-in" {
			t.Fatalf("self-direct rule must be limited to tun-in, got %v", r)
		}
	}
	raw, err = Build(Input{Node: sampleReality(), Mode: ModeSystem, ExePath: `C:\Chuanyun\Chuanyun.exe`})
	if err != nil {
		t.Fatal(err)
	}
	if rules := selfDirectRules(t, raw); len(rules) != 0 {
		t.Fatalf("system mode must not route own process direct: %v", rules)
	}
}

func TestDirectAppsMatchFileNameCaseInsensitive(t *testing.T) {
	p := directAppPatterns([]string{"WeChat.exe", "a+b (x86).exe"})
	if len(p) != 2 {
		t.Fatalf("patterns=%v", p)
	}
	re := regexp.MustCompile(p[0])
	for path, want := range map[string]bool{
		`C:\Program Files\Tencent\WeChat\wechat.exe`: true,
		`WECHAT.EXE`:          true,
		`C:\x\NotWeChat.exe`:  false,
		`C:\x\WeChat.exe.bak`: false,
	} {
		if re.MatchString(path) != want {
			t.Errorf("%s: want %v", path, want)
		}
	}
	if !regexp.MustCompile(p[1]).MatchString(`D:\g\A+B (X86).exe`) {
		t.Error("special characters must be escaped")
	}

	raw, err := Build(Input{Node: sampleReality(), Mode: ModeSystem, DirectApps: []string{"QQ.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range cfg.Route.Rules {
		if _, ok := r["process_path_regex"]; ok {
			found = true
			if r["outbound"] != "direct" || r["inbound"] != nil {
				t.Fatalf("direct app rule must apply to every inbound: %v", r)
			}
		}
	}
	if !found {
		t.Fatal("missing direct app rule in system mode")
	}
}

func TestBuildRejectsBadNode(t *testing.T) {
	_, err := Build(Input{Node: nodes.Node{Type: "vless", Server: "203.0.113.10", Port: 443}})
	if err == nil {
		t.Fatal("expected error")
	}
}
