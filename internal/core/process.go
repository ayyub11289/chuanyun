package core

import (
	"bufio"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Start 启动 `sing-box run -c config`。stop 会结束进程。
// 进程不是由 stop 结束时（崩溃、配置错误、端口被占用），会调用 onExit。
func Start(path, configPath string, onLine func(string), onExit func(error)) (stop func(), err error) {
	cmd := exec.Command(path, "run", "-c", configPath)
	cmd.Dir = ""
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	bindToJob(cmd.Process.Pid)
	var stopping atomic.Bool
	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
	}
	wg.Add(2)
	go pump(stdout)
	go pump(stderr)
	go func() {
		wg.Wait()
		werr := cmd.Wait()
		if !stopping.Load() && onExit != nil {
			onExit(werr)
		}
	}()
	stop = func() {
		stopping.Store(true)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	return stop, nil
}
