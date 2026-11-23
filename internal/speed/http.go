package speed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DownloadURL 是经代理拉取的测速对象。字节数由上限截断，不把这次访问当成直连宽带测试。
const DownloadURL = "https://speed.cloudflare.com/__down?bytes=8000000"

// LatencyURL 是经代理的短请求，用来量隧道往返，而不是服务器端口的 TCP 握手。
const LatencyURL = "https://www.gstatic.com/generate_204"

// ProxyFunc 要求请求走给定的 HTTP 代理。空地址直接报错，避免悄悄变成直连。
func ProxyFunc(proxyURL string) (func(*http.Request) (*url.URL, error), error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errors.New("代理地址无效")
	}
	return http.ProxyURL(u), nil
}

func proxyClient(ctx context.Context, proxyURL string) (*http.Client, error) {
	pf, err := ProxyFunc(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: pf, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second},
	}, nil
}

// ReadThroughProxy 经 proxyURL 下载 rawURL，最多 maxBytes 字节。onProgress 可为 nil。
func ReadThroughProxy(ctx context.Context, proxyURL, rawURL string, maxBytes int64, onProgress func(int64)) (int64, error) {
	if maxBytes <= 0 {
		return 0, errors.New("字节上限无效")
	}
	client, err := proxyClient(ctx, proxyURL)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return countLimited(ctx, resp.Body, maxBytes, onProgress)
}

func countLimited(ctx context.Context, r io.Reader, maxBytes int64, onProgress func(int64)) (int64, error) {
	buf := make([]byte, 32*1024)
	var n int64
	for n < maxBytes {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		want := int64(len(buf))
		if remain := maxBytes - n; remain < want {
			want = remain
		}
		nr, err := r.Read(buf[:want])
		n += int64(nr)
		if onProgress != nil && nr > 0 {
			onProgress(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}

// TunnelRoundTrip 经代理请求 generate_204。返回的 error 只表示这次隧道请求失败。
func TunnelRoundTrip(ctx context.Context, proxyURL string) error {
	client, err := proxyClient(ctx, proxyURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatencyURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
