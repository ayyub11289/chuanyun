package speed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRatesUnitsAndUnavailable(t *testing.T) {
	r := Rates(1_250_000, time.Second)
	if !r.OK {
		t.Fatal("expected rate")
	}
	// 1.25e6 字节/秒 = 10e6 比特/秒 = 10 Mbps，同时是 1.25 MB/s。
	if r.BitsPerSecond != 10_000_000 || r.BytesPerSecond != 1_250_000 {
		t.Fatalf("rate=%+v", r)
	}
	if FormatMbps(r.BitsPerSecond) != "10.00 Mbps" {
		t.Fatalf("mbps=%s", FormatMbps(r.BitsPerSecond))
	}
	if FormatMBps(r.BytesPerSecond) != "1.25 MB/s" {
		t.Fatalf("MBps=%s", FormatMBps(r.BytesPerSecond))
	}
	if !strings.Contains(FormatPair(r), "Mbps") || !strings.Contains(FormatPair(r), "MB/s") {
		t.Fatalf("pair=%s", FormatPair(r))
	}
	if Rates(-1, time.Second).OK || Rates(10, 0).OK || Rates(10, -time.Second).OK {
		t.Fatal("invalid inputs must be unavailable, not zero")
	}
	if FormatPair(ByteRate{}) != "不可用" {
		t.Fatal("missing rate should say 不可用")
	}
	if FormatBytes(1_500_000) != "约 1.50 MB（1500000 字节）" {
		t.Fatalf("bytes=%s", FormatBytes(1_500_000))
	}
}

func TestCounterDeltasResetAndFirstSample(t *testing.T) {
	var c Counter
	at := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	if _, _, ok := c.Observe(100, 200, at); ok {
		t.Fatal("first sample has no rate")
	}
	up, down, ok := c.Observe(100+125000, 200+250000, at.Add(time.Second))
	if !ok || up.BitsPerSecond != 1_000_000 || down.BitsPerSecond != 2_000_000 {
		t.Fatalf("up=%+v down=%+v ok=%v", up, down, ok)
	}
	// 计数回绕（核心重启）不能把差值算成负数或巨大速率。
	if _, _, ok := c.Observe(1, 1, at.Add(2*time.Second)); ok {
		t.Fatal("reset must not yield a rate")
	}
	if _, _, ok := c.Observe(1, 1, at.Add(2*time.Second)); ok {
		t.Fatal("same timestamp must not yield a rate")
	}
}

func TestDownloadByteCapTimeCapAndCancel(t *testing.T) {
	byteCap := RunDownload(context.Background(), time.Second, 100, func(ctx context.Context, maxBytes int64) (int64, error) {
		if maxBytes != 100 {
			t.Fatalf("cap=%d", maxBytes)
		}
		return maxBytes, nil
	})
	if byteCap.Outcome != OutcomeByteCap || byteCap.Bytes != 100 {
		t.Fatalf("%+v", byteCap)
	}

	timeCap := RunDownload(context.Background(), 20*time.Millisecond, 1_000_000, func(ctx context.Context, maxBytes int64) (int64, error) {
		<-ctx.Done()
		return 30, ctx.Err()
	})
	if timeCap.Outcome != OutcomeTimeCap || timeCap.Bytes != 30 {
		t.Fatalf("%+v", timeCap)
	}

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := RunDownload(parent, time.Second, 1000, func(ctx context.Context, maxBytes int64) (int64, error) {
		return 12, ctx.Err()
	})
	if cancelled.Outcome != OutcomeCancelled || cancelled.Bytes != 12 {
		t.Fatalf("%+v", cancelled)
	}

	bad := RunDownload(context.Background(), 0, 10, func(context.Context, int64) (int64, error) { return 10, nil })
	if bad.Outcome != OutcomeBadLimits || bad.Rate.OK {
		t.Fatalf("%+v", bad)
	}
}

func TestLatencyTimeoutIsNotSuccessOrTCP(t *testing.T) {
	ok := MeasureTunnel(context.Background(), time.Second, func(context.Context) error { return nil }, func() time.Time {
		return time.Unix(100, 0)
	})
	// now 固定，耗时为 0，仍然是成功状态，但毫秒为 0 来自时钟而不是编造的 TCP 值。
	if ok.Status != LatencyOK {
		t.Fatalf("%+v", ok)
	}
	text := FormatLatency("节点A", ok)
	if strings.Contains(text, "TCP 握手）") && strings.Contains(text, "ms") {
		// 文案必须声明这不是 TCP。
	} else {
		t.Fatalf("text=%s", text)
	}
	if !strings.Contains(text, "不是 TCP 握手") {
		t.Fatalf("text=%s", text)
	}

	timeout := MeasureTunnel(context.Background(), time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, time.Now)
	if timeout.Status != LatencyTimeout || timeout.RTT != 0 && timeout.Status == LatencyOK {
		t.Fatalf("%+v", timeout)
	}
	if strings.Contains(FormatLatency("节点B", timeout), "ms") {
		t.Fatalf("timeout must not show a millisecond value: %s", FormatLatency("节点B", timeout))
	}

	failed := MeasureTunnel(context.Background(), time.Second, func(context.Context) error {
		return errors.New("连接被重置")
	}, time.Now)
	if failed.Status != LatencyError || strings.Contains(FormatLatency("节点C", failed), " ms") {
		t.Fatalf("%+v %s", failed, FormatLatency("节点C", failed))
	}
	missing := MeasureTunnel(context.Background(), time.Second, nil, time.Now)
	if missing.Status != LatencyUnavailable {
		t.Fatalf("%+v", missing)
	}
}

func TestTunnelTotalsIgnoreDirect(t *testing.T) {
	body := []byte(`{
	  "downloadTotal": 999999,
	  "uploadTotal": 999999,
	  "connections": [
	    {"upload": 100, "download": 400, "chains": ["proxy"]},
	    {"upload": 5, "download": 9, "chains": ["direct"]},
	    {"upload": 7, "download": 8, "chains": ["dns-out","direct"]}
	  ]
	}`)
	conns, err := ParseConnections(body)
	if err != nil {
		t.Fatal(err)
	}
	up, down := TunnelTotals(conns, TunnelTag)
	if up != 100 || down != 400 {
		t.Fatalf("up=%d down=%d", up, down)
	}
	if _, err := ParseConnections([]byte(`{"downloadTotal":1,"uploadTotal":1}`)); err == nil {
		t.Fatal("totals without connections must be unavailable")
	}
}

func TestFetchTunnelBytesOfflineAPI(t *testing.T) {
	// 离线：本地假 clash API，不是真实节点，也不是整机网卡。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"connections":[{"upload":10,"download":20,"chains":["proxy"]},{"upload":99,"download":99,"chains":["direct"]}]}`)
	}))
	defer srv.Close()
	up, down, err := FetchTunnelBytes(context.Background(), srv.URL, "secret")
	if err != nil || up != 10 || down != 20 {
		t.Fatalf("up=%d down=%d err=%v", up, down, err)
	}
}

func TestReadThroughProxyIsNotDirect(t *testing.T) {
	// 离线：只有假代理会回数据。若客户端忽略代理去直连 example.test，必然失败。
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.RequestURI, "example.test") && r.Host == "" {
			http.Error(w, "not a proxy request", http.StatusBadGateway)
			return
		}
		_, _ = w.Write(make([]byte, 50))
	}))
	defer proxy.Close()
	n, err := ReadThroughProxy(context.Background(), proxy.URL, "http://example.test/down", 40, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 40 {
		t.Fatalf("read %d, want byte cap 40", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err = ReadThroughProxy(ctx, proxy.URL, "http://example.test/down", 1000, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v n=%d", err, n)
	}
}
