package failover

import (
	"strings"
	"testing"
	"time"
)

const srv = "192.0.2.1"

func nodes() []NodeRef {
	return []NodeRef{
		{Server: srv, Port: 443, Protocol: "vless"},
		{Server: srv, Port: 8443, Protocol: "vless"},
		{Server: srv, Port: 2053, Protocol: "vless"},
		{Server: srv, Port: 36712, Protocol: "hysteria2"},
	}
}

func domesticUp() Domestic { return Domestic{Checked: true, BaiduOK: true} }
func domesticDown() Domestic {
	return Domestic{Checked: true, BaiduOK: false, AliDNSOK: false}
}

func tcp(ok ...bool) TCPResult {
	out := TCPResult{}
	for i, v := range ok {
		out[i] = v
	}
	return out
}

func failThrice(st State, list []NodeRef, p Probe, d Domestic) (State, Decision) {
	var dec Decision
	for i := 0; i < FailureThreshold; i++ {
		st, dec = Step(time.Now(), list, st, p, d)
	}
	return st, dec
}

func TestHealthyResetsStreak(t *testing.T) {
	st := State{Index: 1, ConsecutiveFailures: 2}
	got, dec := Step(time.Now(), nodes(), st, Probe{Generate204OK: true}, domesticUp())
	if dec.Action != ActionHealthy || got.ConsecutiveFailures != 0 || got.Index != 1 {
		t.Fatalf("got=%+v dec=%+v", got, dec)
	}
}

func TestTwoFailuresDoNotSwitch(t *testing.T) {
	st := State{}
	var dec Decision
	for i := 1; i <= 2; i++ {
		st, dec = Step(time.Now(), nodes(), st, Probe{}, domesticUp())
		if dec.Action != ActionFailing || dec.Failures != i {
			t.Fatalf("tick %d: %+v", i, dec)
		}
	}
	if st.Index != 0 {
		t.Fatalf("index=%d", st.Index)
	}
}

func TestLocalDownDoesNotBlameServer(t *testing.T) {
	got, dec := Step(time.Now(), nodes(), State{ConsecutiveFailures: 2}, Probe{TCP: tcp(false, false, false)}, domesticDown())
	if dec.Action != ActionLocalDown || dec.Log != LogLocalDown || got.Index != 0 || dec.IPWall || dec.Toast != "" {
		t.Fatalf("got=%+v dec=%+v", got, dec)
	}
	_, dec = Step(time.Now(), nodes(), State{ConsecutiveFailures: 2}, Probe{}, Domestic{})
	if dec.Action != ActionLocalDown {
		t.Fatalf("unchecked domestic must not switch: %+v", dec)
	}
}

func TestBlockedPortSkipsOtherDeadPorts(t *testing.T) {
	// 443 与 8443 不通，2053 通：直接跳到 2053，不在 8443 上浪费一轮。
	st, dec := failThrice(State{}, nodes(), Probe{TCP: tcp(false, false, true)}, domesticUp())
	if dec.Action != ActionSwitch || dec.NewIndex != 2 || dec.Cause != CausePortBlocked {
		t.Fatalf("dec=%+v", dec)
	}
	if dec.Toast != "端口 443 疑似被封，已自动切换到 2053" || dec.IPWall {
		t.Fatalf("dec=%+v", dec)
	}
	if st.Index != 2 {
		t.Fatalf("index=%d", st.Index)
	}
}

func TestInterferedWhenTCPOK(t *testing.T) {
	_, dec := failThrice(State{}, nodes(), Probe{TCP: tcp(true, true, true)}, domesticUp())
	if dec.Cause != CauseInterfered || dec.NewIndex != 1 || !strings.Contains(dec.Toast, "疑似被干扰") {
		t.Fatalf("dec=%+v", dec)
	}
}

func TestAllTCPFailIsIPWallAndTriesHysteria(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	st := State{ConsecutiveFailures: 2}
	st, dec := Step(now, nodes(), st, Probe{TCP: tcp(false, false, false)}, domesticUp())
	if dec.Cause != CauseIPWall || !dec.IPWall || dec.WalledServer != srv {
		t.Fatalf("dec=%+v", dec)
	}
	if dec.NewIndex != 3 || !strings.Contains(dec.Toast, "Hysteria2（UDP 36712）") {
		t.Fatalf("should fall back to hysteria2: %+v", dec)
	}
	// 30 分钟内同一服务器不重复通知。
	st.Index = 0
	st.ConsecutiveFailures = 2
	st, dec = Step(now.Add(10*time.Minute), nodes(), st, Probe{TCP: tcp(false, false, false)}, domesticUp())
	if dec.IPWall {
		t.Fatal("ip wall not rate limited")
	}
	st.Index = 0
	st.ConsecutiveFailures = 2
	_, dec = Step(now.Add(IPWallCooldown+time.Minute), nodes(), st, Probe{TCP: tcp(false, false, false)}, domesticUp())
	if !dec.IPWall {
		t.Fatal("expected ip wall after cooldown")
	}
}

func TestHysteriaFailureGoesBackToReachableReality(t *testing.T) {
	st := State{Index: 3, ConsecutiveFailures: 2}
	_, dec := Step(time.Now(), nodes(), st, Probe{TCP: tcp(false, true, true)}, domesticUp())
	if dec.Cause != CauseUDP || dec.NewIndex != 1 {
		t.Fatalf("dec=%+v", dec)
	}
}

func TestSingleNodeRetries(t *testing.T) {
	one := []NodeRef{{Server: srv, Port: 443, Protocol: "vless"}}
	_, dec := failThrice(State{}, one, Probe{TCP: tcp(false)}, domesticUp())
	if dec.Action != ActionRetry || !dec.IPWall || dec.Toast != "" {
		t.Fatalf("dec=%+v", dec)
	}
}

func TestOtherServerStillCandidateWhenOneWalled(t *testing.T) {
	list := []NodeRef{
		{Server: srv, Port: 443, Protocol: "vless"},
		{Server: srv, Port: 8443, Protocol: "vless"},
		{Server: "192.0.2.2", Port: 443, Protocol: "vless"},
	}
	_, dec := failThrice(State{}, list, Probe{TCP: tcp(false, false, true)}, domesticUp())
	if dec.Cause != CauseIPWall || dec.NewIndex != 2 {
		t.Fatalf("dec=%+v", dec)
	}
}

func TestAliDNSAloneCountsAsDomesticUp(t *testing.T) {
	_, dec := Step(time.Now(), nodes(), State{ConsecutiveFailures: 2}, Probe{}, Domestic{Checked: true, AliDNSOK: true})
	if dec.Action != ActionSwitch {
		t.Fatalf("action=%v", dec.Action)
	}
}

func TestSummarize(t *testing.T) {
	lines := Summarize(nodes(), tcp(false, true, true))
	if len(lines) != 2 || lines[0] != "端口 443 不通，疑似被封；2053、8443 可达" {
		t.Fatalf("lines=%q", lines)
	}
	lines = Summarize(nodes(), tcp(false, false, false))
	if !strings.Contains(lines[0], "IP 疑似被墙") {
		t.Fatalf("lines=%q", lines)
	}
}

func labelled() []NodeRef {
	list := nodes()
	for i := range list {
		list[i].Label = []string{"线路 1", "线路 2", "线路 3", "线路 4"}[i]
		list[i].Place = "日本 · 东京"
	}
	return list
}

func TestLabelledMessagesHidePorts(t *testing.T) {
	_, dec := failThrice(State{}, labelled(), Probe{TCP: tcp(false, false, true)}, domesticUp())
	if dec.Toast != "线路 1 疑似被封，已自动切换到 线路 3" {
		t.Fatalf("toast=%q", dec.Toast)
	}
	lines := Summarize(labelled(), tcp(false, true, true))
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "443") || !strings.Contains(joined, "线路 1 疑似被封") {
		t.Fatalf("summary must not show ports: %v", lines)
	}
}
