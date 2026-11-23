package nodes

import (
	"strings"
	"testing"
)

const vlessLink = "vless://b7e2c1a0-1234-4abc-8def-0123456789ab@203.0.113.10:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI&sid=a1b2c3d4&type=tcp#Reality-443"

const hy2Link = "hysteria2://secret-password@203.0.113.10:36712?sni=www.microsoft.com&insecure=1#Hysteria2"

func TestParseLinksAndOrder(t *testing.T) {
	list, err := Parse(hy2Link + "\n" + vlessLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].Type != "vless" || list[0].Port != 443 || list[0].PublicKey == "" || list[0].UUID == "" {
		t.Fatalf("reality=%+v", list[0])
	}
	if list[0].Flow != "xtls-rprx-vision" || list[0].SNI != "www.microsoft.com" || list[0].ShortID != "a1b2c3d4" {
		t.Fatalf("reality fields=%+v", list[0])
	}
	if list[1].Type != "hysteria2" || list[1].Password != "secret-password" || !list[1].Insecure {
		t.Fatalf("hy2=%+v", list[1])
	}
}

func TestParseBundle(t *testing.T) {
	raw := `{
	  "server": "203.0.113.10",
	  "remark": "穿云",
	  "uuid": "b7e2c1a0-1234-4abc-8def-0123456789ab",
	  "sni": "www.microsoft.com",
	  "publicKey": "Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI",
	  "shortId": "a1b2c3d4",
	  "ports": [8443, 443],
	  "hysteria2": {"port": 36712, "password": "secret-password", "insecure": true}
	}`
	list, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d %+v", len(list), list)
	}
	if list[0].Port != 8443 || list[1].Port != 443 || list[2].Type != "hysteria2" {
		t.Fatalf("order=%s %s %s", list[0].Label(), list[1].Label(), list[2].Label())
	}
	if !strings.Contains(list[0].Name, "穿云") || list[2].Password != "secret-password" || !list[2].Insecure {
		t.Fatalf("%+v %+v", list[0], list[2])
	}
}

func TestParseSingBoxOutbound(t *testing.T) {
	raw := `{
	  "outbounds": [
	    {"type": "direct", "tag": "direct"},
	    {
	      "type": "vless",
	      "tag": "r1",
	      "server": "203.0.113.10",
	      "server_port": 2053,
	      "uuid": "b7e2c1a0-1234-4abc-8def-0123456789ab",
	      "tls": {
	        "server_name": "www.microsoft.com",
	        "utls": {"fingerprint": "chrome"},
	        "reality": {"enabled": true, "public_key": "Ez3LJ3ZuN1IyKZoFX_CIgABck8VD5DQQk5a0bSot2kI", "short_id": "a1b2c3d4"}
	      }
	    }
	  ]
	}`
	list, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Port != 2053 || list[0].PublicKey == "" || list[0].Flow != "xtls-rprx-vision" {
		t.Fatalf("%+v", list)
	}
}

func TestParseNodeArrayAndHy2Scheme(t *testing.T) {
	raw := `[{"protocol":"hysteria2","address":"203.0.113.8","port":443,"password":"p","sni":"a.com"}]`
	list, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Server != "203.0.113.8" || list[0].Type != "hysteria2" {
		t.Fatalf("%+v", list[0])
	}
	n, err := ParseLink("hy2://pw@[2001:db8::1]:8443?insecure=1&obfs=salamander&obfs-password=op")
	if err != nil {
		t.Fatal(err)
	}
	if n.Server != "2001:db8::1" || n.Port != 8443 || n.Obfs != "salamander" || n.ObfsPassword != "op" {
		t.Fatalf("%+v", n)
	}
}

func TestParseServerProxyNodesFile(t *testing.T) {
	// install-chuanyun-server.sh 写出的 /root/proxy-nodes.json 格式。
	raw := `{"version":1,"server":"198.18.0.10","generated":"2026-11-18T06:40:03Z","nodes":[
	  {"name":"Reality-443","type":"vless-reality","server":"198.18.0.10","port":443,"transport":"tcp",
	   "uuid":"11111111-1111-4111-8111-111111111111","flow":"xtls-rprx-vision","sni":"www.apple.com",
	   "public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"aabbccdd","fingerprint":"chrome"},
	  {"name":"Hysteria2-36712","type":"hysteria2","server":"198.18.0.10","port":36712,"transport":"udp",
	   "password":"pw","obfs":"salamander","obfs_password":"op","sni":"www.bing.com","insecure":true}]}`
	list, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Type != "vless" || list[0].PublicKey == "" {
		t.Fatalf("%+v", list)
	}
	if list[1].Obfs != "salamander" || list[1].ObfsPassword != "op" || !list[1].Insecure {
		t.Fatalf("hy2=%+v", list[1])
	}
	if AllExamples(list) {
		t.Fatal("real server must not be treated as example")
	}
}

func TestDetectsExampleNodes(t *testing.T) {
	list, err := Parse(vlessLink + "\n" + hy2Link)
	if err != nil {
		t.Fatal(err)
	}
	if !AllExamples(list) {
		t.Fatal("203.0.113.0/24 is a documentation range")
	}
	if !IsExample(Node{Server: "198.18.0.10", Password: "请换成安装器给出的密码"}) {
		t.Fatal("placeholder password")
	}
	if IsExample(Node{Server: "198.18.0.10", Password: "x"}) {
		t.Fatal("real address")
	}
}

func TestRejectsEmptyAndBad(t *testing.T) {
	if _, err := Parse("   "); err == nil {
		t.Fatal("empty")
	}
	if _, err := Parse("vmess://abc"); err == nil {
		t.Fatal("vmess")
	}
	if _, err := Parse(`{"server":"1.1.1.1","ports":[443]}`); err == nil {
		t.Fatal("missing uuid")
	}
}
