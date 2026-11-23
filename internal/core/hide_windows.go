//go:build windows

package core

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

// bindToJob 把 sing-box 放进一个「句柄关闭即结束」的作业对象。
// 句柄只由本进程持有，穿云崩溃或被结束时系统会顺带结束 sing-box，不留占着 2080 端口的孤儿进程。
func bindToJob(pid int) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(h)
			return
		}
		jobHandle = h
	})
	if jobHandle == 0 {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	_ = windows.AssignProcessToJobObject(jobHandle, p)
}

// KillStray 结束路径与 bin 相同、但不归本进程管的 sing-box（上次崩溃留下的）。返回结束的个数。
func KillStray(bin string) int {
	want, err := filepath.Abs(bin)
	if err != nil {
		return 0
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	killed := 0
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if !strings.EqualFold(name, filepath.Base(want)) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, pe.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil &&
			strings.EqualFold(windows.UTF16ToString(buf[:n]), want) {
			if windows.TerminateProcess(h, 1) == nil {
				killed++
			}
		}
		_ = windows.CloseHandle(h)
	}
	return killed
}
