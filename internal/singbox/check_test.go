package singbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSingBoxCheckRealityAndHysteria(t *testing.T) {
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		bin = "/tmp/chuanyun-tools/sing-box-1.14.4-linux-amd64/sing-box"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("找不到 sing-box（设置 SINGBOX_BIN）：%v", err)
	}
	rules := os.Getenv("CHUANYUN_RULESET_DIR")
	if rules == "" {
		rules = "/tmp/chuanyun-tools"
	}
	site := filepath.Join(rules, "geosite-cn.srs")
	ip := filepath.Join(rules, "geoip-cn.srs")
	for _, p := range []string{site, ip} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("找不到规则集 %s：%v", p, err)
		}
	}

	dir := t.TempDir()
	cases := []struct {
		name string
		in   Input
	}{
		{"reality-tun", Input{Node: sampleReality(), Mode: ModeTUN, GeoSitePath: site, GeoIPPath: ip, ExePath: `C:\Chuanyun\Chuanyun.exe`, ClashSecret: "offline-check"}},
		{"hysteria-system", Input{Node: sampleHy2(), Mode: ModeSystem, GeoSitePath: site, GeoIPPath: ip}},
		{"reality-fallback", Input{Node: sampleReality(), Mode: ModeTUN}},
		{"direct-apps", Input{Node: sampleReality(), Mode: ModeTUN, GeoSitePath: site, GeoIPPath: ip, DirectApps: []string{"WeChat.exe", "a+b (x86).exe"}}},
		{"direct-apps-system", Input{Node: sampleHy2(), Mode: ModeSystem, DirectApps: []string{"QQ.exe"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Build(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, tc.name+".json")
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "check", "-c", path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sing-box check 失败：%v\n%s\n配置：\n%s", err, out, raw)
			}
		})
	}
}
