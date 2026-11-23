package speed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TunnelTag 是配置里代理出站的标签。只累计这个出站，不算直连和本机其他网卡。
const TunnelTag = "proxy"

// Conn 是 clash API 里的一条连接。
type Conn struct {
	Upload   int64
	Download int64
	Chains   []string
}

// TunnelTotals 只累加链路里包含 tunnelTag 的连接。其他连接视为与这条隧道无关。
func TunnelTotals(conns []Conn, tunnelTag string) (upload, download int64) {
	if tunnelTag == "" {
		return 0, 0
	}
	for _, c := range conns {
		if !chainHas(c.Chains, tunnelTag) {
			continue
		}
		if c.Upload > 0 {
			upload += c.Upload
		}
		if c.Download > 0 {
			download += c.Download
		}
	}
	return upload, download
}

func chainHas(chains []string, tag string) bool {
	for _, c := range chains {
		if c == tag {
			return true
		}
	}
	return false
}

// ParseConnections 解析 sing-box / clash 的 /connections。
// 缺少 connections 字段时返回错误，调用方应显示不可用，而不是当成 0。
func ParseConnections(body []byte) ([]Conn, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, errors.New("空响应")
	}
	var raw struct {
		Connections []struct {
			Upload   int64    `json:"upload"`
			Download int64    `json:"download"`
			Chains   []string `json:"chains"`
		} `json:"connections"`
		HasConnections bool
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, err
	}
	if _, ok := generic["connections"]; !ok {
		return nil, errors.New("响应没有 connections")
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]Conn, 0, len(raw.Connections))
	for _, c := range raw.Connections {
		out = append(out, Conn{Upload: c.Upload, Download: c.Download, Chains: c.Chains})
	}
	return out, nil
}

// FetchTunnelBytes 请求本机 clash API，返回当前隧道的累计上下行字节。
func FetchTunnelBytes(ctx context.Context, baseURL, secret string) (int64, int64, error) {
	if baseURL == "" {
		return 0, 0, errors.New("没有流量接口地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/connections", nil)
	if err != nil {
		return 0, 0, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("流量接口 HTTP %d", resp.StatusCode)
	}
	conns, err := ParseConnections(body)
	if err != nil {
		return 0, 0, err
	}
	up, down := TunnelTotals(conns, TunnelTag)
	return up, down, nil
}
