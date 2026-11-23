//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// KillSwitch 用 Windows 筛选平台（WFP）阻断除隧道以外的出站连接。
// 规则挂在动态会话上：Release 或本进程退出时由系统自动删除，不会把电脑永久锁在断网状态。
type KillSwitch struct {
	mu     sync.Mutex
	engine uintptr
}

var (
	fwpuclnt                      = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0           = fwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0          = fwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmSubLayerAdd0          = fwpuclnt.NewProc("FwpmSubLayerAdd0")
	procFwpmFilterAdd0            = fwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmTransactionBegin0     = fwpuclnt.NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0    = fwpuclnt.NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0     = fwpuclnt.NewProc("FwpmTransactionAbort0")
	procFwpmGetAppIdFromFileName0 = fwpuclnt.NewProc("FwpmGetAppIdFromFileName0")
	procFwpmFreeMemory0           = fwpuclnt.NewProc("FwpmFreeMemory0")
)

// 以下常量和 GUID 来自 fwpmu.h / fwptypes.h。
const (
	fwpEmpty       = 0
	fwpUint8       = 1
	fwpUint32      = 3
	fwpByteBlob    = 12
	fwpV4AddrMask  = 0x100
	fwpV6AddrMask  = 0x101
	fwpMatchEqual  = 0
	fwpMatchAllSet = 6

	fwpActionBlock  = 0x1001
	fwpActionPermit = 0x1002

	fwpmSessionFlagDynamic       = 1
	fwpmFilterFlagClearActionRgt = 8
	fwpConditionFlagIsLoopback   = 1
	rpcCAuthnWinNT               = 10
)

var (
	layerConnectV4 = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	layerConnectV6 = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	condAppID      = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
	condFlags      = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	condLocalAddr  = windows.GUID{Data1: 0xd9ee00de, Data2: 0xc1ef, Data3: 0x4617, Data4: [8]byte{0xbf, 0xe3, 0xff, 0xd8, 0xf5, 0xa0, 0x89, 0x57}}
	condRemoteAddr = windows.GUID{Data1: 0xb235ae9a, Data2: 0x1d64, Data3: 0x49b8, Data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45}}
)

type fwpDisplayData struct {
	name        *uint16
	description *uint16
}

type fwpBlob struct {
	size uint32
	data *byte
}

type fwpValue struct {
	typ   uint32
	value uintptr
}

type fwpCondition struct {
	fieldKey  windows.GUID
	matchType uint32
	value     fwpValue
}

type fwpAction struct {
	typ        uint32
	filterType windows.GUID
}

type fwpFilter struct {
	filterKey           windows.GUID
	displayData         fwpDisplayData
	flags               uint32
	providerKey         *windows.GUID
	providerData        fwpBlob
	layerKey            windows.GUID
	subLayerKey         windows.GUID
	weight              fwpValue
	numFilterConditions uint32
	filterCondition     *fwpCondition
	action              fwpAction
	_                   [4]byte
	providerContextKey  windows.GUID
	reserved            *windows.GUID
	filterID            uint64
	effectiveWeight     fwpValue
}

type fwpSession struct {
	sessionKey           windows.GUID
	displayData          fwpDisplayData
	flags                uint32
	txnWaitTimeoutInMSec uint32
	processID            uint32
	sid                  *windows.SID
	username             *uint16
	kernelMode           int32
}

type fwpSublayer struct {
	subLayerKey  windows.GUID
	displayData  fwpDisplayData
	flags        uint32
	providerKey  *windows.GUID
	providerData fwpBlob
	weight       uint16
}

type v4Mask struct {
	addr uint32
	mask uint32
}

type v6Mask struct {
	addr   [16]byte
	prefix uint8
}

func fwpErr(op string, r uintptr) error {
	if r == 0 {
		return nil
	}
	return fmt.Errorf("%s: 0x%08x", op, uint32(r))
}

// Engaged 报告阻断规则是否在生效。
func (k *KillSwitch) Engaged() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.engine != 0
}

// Engage 只放行 allow 里的程序、本机回环、局域网，以及本地地址在 tunnel 网段内（即经过隧道网卡）的连接，其余出站全部阻断。
// 已生效时什么也不做。
func (k *KillSwitch) Engage(allow []string, tunnel []netip.Prefix) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.engine != 0 {
		return nil
	}
	if err := fwpuclnt.Load(); err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("穿云防泄漏")
	session := fwpSession{
		displayData:          fwpDisplayData{name: name},
		flags:                fwpmSessionFlagDynamic,
		txnWaitTimeoutInMSec: windows.INFINITE,
	}
	var engine uintptr
	if err := fwpErr("FwpmEngineOpen0", call(procFwpmEngineOpen0, 0, rpcCAuthnWinNT, 0, uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&engine)))); err != nil {
		return err
	}
	if err := install(engine, name, allow, tunnel); err != nil {
		procFwpmEngineClose0.Call(engine)
		return err
	}
	k.engine = engine
	return nil
}

// Release 删除全部规则，恢复正常上网。
func (k *KillSwitch) Release() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.engine != 0 {
		procFwpmEngineClose0.Call(k.engine)
		k.engine = 0
	}
}

func call(p *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func install(engine uintptr, name *uint16, allow []string, tunnel []netip.Prefix) (err error) {
	if err := fwpErr("FwpmTransactionBegin0", call(procFwpmTransactionBegin0, engine, 0)); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			procFwpmTransactionAbort0.Call(engine)
		}
	}()

	key, err := windows.GenerateGUID()
	if err != nil {
		return err
	}
	sub := fwpSublayer{subLayerKey: key, displayData: fwpDisplayData{name: name}, weight: 0xffff}
	if err := fwpErr("FwpmSubLayerAdd0", call(procFwpmSubLayerAdd0, engine, uintptr(unsafe.Pointer(&sub)), 0)); err != nil {
		return err
	}

	// keep 防止只以 uintptr 形式存在的条件值在调用前被回收。
	var keep []any
	add := func(layer windows.GUID, weight uint8, action uint32, flags uint32, conds ...fwpCondition) error {
		f := fwpFilter{
			displayData: fwpDisplayData{name: name},
			flags:       flags,
			layerKey:    layer,
			subLayerKey: key,
			weight:      fwpValue{typ: fwpUint8, value: uintptr(weight)},
			action:      fwpAction{typ: action},
		}
		if len(conds) > 0 {
			f.numFilterConditions = uint32(len(conds))
			f.filterCondition = &conds[0]
		}
		var id uint64
		r := call(procFwpmFilterAdd0, engine, uintptr(unsafe.Pointer(&f)), 0, uintptr(unsafe.Pointer(&id)))
		runtime.KeepAlive(conds)
		runtime.KeepAlive(keep)
		return fwpErr("FwpmFilterAdd0", r)
	}
	layers := []windows.GUID{layerConnectV4, layerConnectV6}

	for _, exe := range allow {
		p, err := windows.UTF16PtrFromString(exe)
		if err != nil {
			return err
		}
		var blob *fwpBlob
		if err := fwpErr("FwpmGetAppIdFromFileName0 "+exe, call(procFwpmGetAppIdFromFileName0, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&blob)))); err != nil {
			return err
		}
		cond := fwpCondition{fieldKey: condAppID, matchType: fwpMatchEqual, value: fwpValue{typ: fwpByteBlob, value: uintptr(unsafe.Pointer(blob))}}
		for _, l := range layers {
			if err := add(l, 15, fwpActionPermit, 0, cond); err != nil {
				procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&blob)))
				return err
			}
		}
		procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&blob)))
	}

	loop := fwpCondition{fieldKey: condFlags, matchType: fwpMatchAllSet, value: fwpValue{typ: fwpUint32, value: fwpConditionFlagIsLoopback}}
	for _, l := range layers {
		if err := add(l, 15, fwpActionPermit, 0, loop); err != nil {
			return err
		}
	}

	prefixCond := func(field windows.GUID, p netip.Prefix) (windows.GUID, fwpCondition) {
		if p.Addr().Is4() {
			a := p.Addr().As4()
			m := &v4Mask{addr: binary.BigEndian.Uint32(a[:]), mask: ^uint32(0) << (32 - p.Bits())}
			if p.Bits() == 0 {
				m.mask = 0
			}
			keep = append(keep, m)
			return layerConnectV4, fwpCondition{fieldKey: field, matchType: fwpMatchEqual, value: fwpValue{typ: fwpV4AddrMask, value: uintptr(unsafe.Pointer(m))}}
		}
		m := &v6Mask{addr: p.Addr().As16(), prefix: uint8(p.Bits())}
		keep = append(keep, m)
		return layerConnectV6, fwpCondition{fieldKey: field, matchType: fwpMatchEqual, value: fwpValue{typ: fwpV6AddrMask, value: uintptr(unsafe.Pointer(m))}}
	}
	for _, p := range tunnel {
		l, c := prefixCond(condLocalAddr, p.Masked())
		if err := add(l, 14, fwpActionPermit, 0, c); err != nil {
			return err
		}
	}
	for _, s := range lanPrefixes {
		l, c := prefixCond(condRemoteAddr, netip.MustParsePrefix(s))
		if err := add(l, 13, fwpActionPermit, 0, c); err != nil {
			return err
		}
	}
	for _, l := range layers {
		if err := add(l, 0, fwpActionBlock, fwpmFilterFlagClearActionRgt); err != nil {
			return err
		}
	}
	return fwpErr("FwpmTransactionCommit0", call(procFwpmTransactionCommit0, engine))
}

// lanPrefixes 是始终放行的局域网、链路本地和组播地址，保证打印机、NAS 和 DHCP 正常。
var lanPrefixes = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "224.0.0.0/4", "255.255.255.255/32",
	"fe80::/10", "ff00::/8",
}
