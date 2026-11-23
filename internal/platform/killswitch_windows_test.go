//go:build windows

package platform

import (
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// 偏移量对照 fwpmtypes.h 在 x64 下的布局，错一个字节 WFP 就会读到垃圾。
func TestWFPStructLayout(t *testing.T) {
	var f fwpFilter
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"FWPM_FILTER0", unsafe.Sizeof(f), 200},
		{"filter.flags", unsafe.Offsetof(f.flags), 32},
		{"filter.layerKey", unsafe.Offsetof(f.layerKey), 64},
		{"filter.weight", unsafe.Offsetof(f.weight), 96},
		{"filter.numFilterConditions", unsafe.Offsetof(f.numFilterConditions), 112},
		{"filter.filterCondition", unsafe.Offsetof(f.filterCondition), 120},
		{"filter.action", unsafe.Offsetof(f.action), 128},
		{"filter.providerContextKey", unsafe.Offsetof(f.providerContextKey), 152},
		{"filter.filterID", unsafe.Offsetof(f.filterID), 176},
		{"filter.effectiveWeight", unsafe.Offsetof(f.effectiveWeight), 184},
		{"FWPM_FILTER_CONDITION0", unsafe.Sizeof(fwpCondition{}), 40},
		{"condition.value", unsafe.Offsetof(fwpCondition{}.value), 24},
		{"FWP_VALUE0", unsafe.Sizeof(fwpValue{}), 16},
		{"FWPM_SESSION0", unsafe.Sizeof(fwpSession{}), 72},
		{"session.processId", unsafe.Offsetof(fwpSession{}.processID), 40},
		{"session.kernelMode", unsafe.Offsetof(fwpSession{}.kernelMode), 64},
		{"FWPM_SUBLAYER0", unsafe.Sizeof(fwpSublayer{}), 72},
		{"sublayer.weight", unsafe.Offsetof(fwpSublayer{}.weight), 64},
		{"FWP_V4_ADDR_AND_MASK", unsafe.Sizeof(v4Mask{}), 8},
		{"FWP_V6_ADDR_AND_MASK", unsafe.Sizeof(v6Mask{}), 17},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func childCanDial(t *testing.T, addr string) bool {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	script := "$c=New-Object Net.Sockets.TcpClient; try { $c.ConnectAsync('" + host + "'," + port + ").Wait(3000) } catch { $false }"
	out, _ := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	return strings.Contains(string(out), "True")
}

// 需要管理员。会在几秒内阻断本机除本测试进程以外的新连接，结束后自动恢复。
func TestKillSwitchBlocksOthersButNotAllowed(t *testing.T) {
	if !Elevated() || os.Getenv("CY_KILLSWITCH_E2E") == "" {
		t.Skip("set CY_KILLSWITCH_E2E=1 and run elevated")
	}
	const remote = "1.1.1.1:443"
	if !childCanDial(t, remote) {
		t.Skip("no internet before test")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	self, _ := os.Executable()
	var k KillSwitch
	if err := k.Engage([]string{self}, []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")}); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			k.Release()
		}
	}()
	if !k.Engaged() {
		t.Fatal("not engaged")
	}
	if childCanDial(t, remote) {
		t.Error("other programs must be blocked")
	}
	if !childCanDial(t, ln.Addr().String()) {
		t.Error("loopback must stay open")
	}
	if c, err := net.DialTimeout("tcp", remote, 3*time.Second); err != nil {
		t.Errorf("allowed program blocked: %v", err)
	} else {
		c.Close()
	}

	k.Release()
	released = true
	if !childCanDial(t, remote) {
		t.Error("network not restored after release")
	}
}
