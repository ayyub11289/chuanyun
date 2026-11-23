// Package probe 做看门狗用的三类探测：经本地混合端口访问 generate_204、
// 直连服务器端口的 TCP，以及百度 / 223.5.5.5 的国内连通性。
package probe

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"chuanyun/internal/failover"
)

// Generate204 通过 HTTP 代理访问 gstatic generate_204，状态码 204 视为成功。
func Generate204(proxyURL string) bool {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return false
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(u),
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DisableKeepAlives:     true,
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	for _, target := range []string{
		"https://www.gstatic.com/generate_204",
		"http://www.gstatic.com/generate_204",
	} {
		if status204(client, target) {
			return true
		}
	}
	return false
}

func status204(client *http.Client, target string) bool {
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == 204
}

// TCP 直连服务器端口，超时 5 秒。
func TCP(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Domestic 检查百度与 223.5.5.5:53，任一成功即认为国内网络可用。
func Domestic() failover.Domestic {
	var baiduOK, aliOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		baiduOK = baidu()
	}()
	go func() {
		defer wg.Done()
		aliOK = TCP("223.5.5.5", 53)
	}()
	wg.Wait()
	return failover.Domestic{Checked: true, BaiduOK: baiduOK, AliDNSOK: aliOK}
}

func baidu() bool {
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	client := &http.Client{Transport: tr, Timeout: 6 * time.Second}
	for _, target := range []string{"https://www.baidu.com", "http://www.baidu.com"} {
		resp, err := client.Get(target)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		ok := resp.StatusCode >= 200 && resp.StatusCode < 500
		resp.Body.Close()
		if ok {
			return true
		}
	}
	return false
}
