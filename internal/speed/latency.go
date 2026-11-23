package speed

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// LatencyStatus 是隧道往返的测量状态。它不是到服务器端口的 TCP 握手时间。
type LatencyStatus string

const (
	LatencyUnmeasured  LatencyStatus = "unmeasured"
	LatencyMeasuring   LatencyStatus = "measuring"
	LatencyOK          LatencyStatus = "ok"
	LatencyTimeout     LatencyStatus = "timeout"
	LatencyError       LatencyStatus = "error"
	LatencyUnavailable LatencyStatus = "unavailable"
)

// Latency 是某一次经代理的往返结果。
type Latency struct {
	Status LatencyStatus
	RTT    time.Duration
	At     time.Time
	Detail string
}

// MeasureTunnel 调用 roundTrip（必须走代理）并记录耗时。
// 超时和错误分开，成功时 RTT 是整次请求的耗时，不是 TCP 建连时间。
func MeasureTunnel(ctx context.Context, timeout time.Duration, roundTrip func(ctx context.Context) error, now func() time.Time) Latency {
	if now == nil {
		now = time.Now
	}
	if roundTrip == nil {
		return Latency{Status: LatencyUnavailable, At: now(), Detail: "未配置隧道测量"}
	}
	if timeout <= 0 {
		return Latency{Status: LatencyUnavailable, At: now(), Detail: "超时上限无效"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := now()
	err := roundTrip(cctx)
	elapsed := now().Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	at := now()
	if err == nil {
		return Latency{Status: LatencyOK, RTT: elapsed, At: at}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return Latency{Status: LatencyTimeout, At: at, Detail: "超时"}
	}
	if errors.Is(err, context.Canceled) {
		return Latency{Status: LatencyError, At: at, Detail: "已取消"}
	}
	return Latency{Status: LatencyError, At: at, Detail: err.Error()}
}

// FormatLatency 给界面用的一行字。没有成功样本时不显示毫秒数。
func FormatLatency(name string, v Latency) string {
	switch v.Status {
	case LatencyOK:
		return fmt.Sprintf("%s：%d ms（隧道往返，不是 TCP 握手）", name, v.RTT.Milliseconds())
	case LatencyMeasuring:
		return name + "：测量中"
	case LatencyTimeout:
		return name + "：超时"
	case LatencyError:
		if v.Detail == "" {
			return name + "：失败"
		}
		return name + "：失败（" + v.Detail + "）"
	case LatencyUnavailable:
		if v.Detail == "" {
			return name + "：不可用"
		}
		return name + "：不可用（" + v.Detail + "）"
	default:
		return name + "：未测量"
	}
}
