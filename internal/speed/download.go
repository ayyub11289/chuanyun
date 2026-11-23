package speed

import (
	"context"
	"errors"
	"time"
)

const (
	// DefaultMaxDuration 是手动下载测速的时间上限。
	DefaultMaxDuration = 12 * time.Second
	// DefaultMaxBytes 是手动下载测速的字节上限（8 MB，十进制）。
	DefaultMaxBytes int64 = 8 * DecimalMega
)

// Outcome 是一次手动测速的结束原因。
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeTimeCap   Outcome = "time"
	OutcomeByteCap   Outcome = "bytes"
	OutcomeError     Outcome = "error"
	OutcomeBadLimits Outcome = "bad-limits"
)

// DownloadResult 是测速结果。速率只在耗时有效时才有。
type DownloadResult struct {
	Outcome Outcome
	Bytes   int64
	Elapsed time.Duration
	Err     error
	Rate    ByteRate
}

// Reader 从已选中的代理读数据，最多 maxBytes 字节，并遵守 ctx。
type Reader func(ctx context.Context, maxBytes int64) (int64, error)

// RunDownload 执行一次有上限的下载。parent 被取消视为用户取消。
// 时间用 WithTimeout 封顶；字节由 Reader 保证不超过 maxBytes。
func RunDownload(parent context.Context, maxDuration time.Duration, maxBytes int64, read Reader) DownloadResult {
	if parent == nil {
		parent = context.Background()
	}
	if maxDuration <= 0 || maxBytes <= 0 || read == nil {
		return DownloadResult{Outcome: OutcomeBadLimits, Rate: ByteRate{}}
	}
	ctx, cancel := context.WithTimeout(parent, maxDuration)
	defer cancel()
	start := time.Now()
	n, err := read(ctx, maxBytes)
	elapsed := time.Since(start)
	if n > maxBytes {
		n = maxBytes
	}
	if n < 0 {
		n = 0
	}
	res := DownloadResult{Bytes: n, Elapsed: elapsed, Rate: Rates(n, elapsed)}
	switch {
	case parent.Err() != nil:
		res.Outcome = OutcomeCancelled
		res.Err = parent.Err()
	case n >= maxBytes:
		res.Outcome = OutcomeByteCap
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Outcome = OutcomeTimeCap
		res.Err = err
	case errors.Is(err, context.Canceled):
		res.Outcome = OutcomeCancelled
		res.Err = err
	case err != nil:
		res.Outcome = OutcomeError
		res.Err = err
	default:
		res.Outcome = OutcomeOK
	}
	return res
}
