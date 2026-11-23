// Package speed 计算隧道速率、限制手动下载测速，并解析 sing-box 的本隧道流量。
// 这里没有真实节点：数字只来自调用方给出的字节数和时间，算不出来就标成不可用。
package speed

import (
	"fmt"
	"time"
)

// DecimalMega 是十进制的「兆」：Mbps 与 MB/s 都用 10^6，不用 1024。
const DecimalMega = 1_000_000

// ByteRate 是一段时间里的平均速率。OK 为假时没有可显示的速率。
type ByteRate struct {
	BitsPerSecond  float64
	BytesPerSecond float64
	OK             bool
}

// Rates 由字节数和耗时计算速率。耗时不是正数、或字节数为负时返回不可用，不编造 0。
func Rates(bytes int64, elapsed time.Duration) ByteRate {
	if bytes < 0 || elapsed <= 0 {
		return ByteRate{}
	}
	sec := elapsed.Seconds()
	if sec <= 0 {
		return ByteRate{}
	}
	bps := float64(bytes) / sec
	return ByteRate{BitsPerSecond: bps * 8, BytesPerSecond: bps, OK: true}
}

// FormatMbps 把比特每秒写成兆比特每秒。
func FormatMbps(bitsPerSecond float64) string {
	return fmt.Sprintf("%.2f Mbps", bitsPerSecond/DecimalMega)
}

// FormatMBps 把字节每秒写成十进制兆字节每秒。
func FormatMBps(bytesPerSecond float64) string {
	return fmt.Sprintf("%.2f MB/s", bytesPerSecond/DecimalMega)
}

// FormatPair 同时写出 Mbps 和 MB/s。没有速率时返回「不可用」。
func FormatPair(r ByteRate) string {
	if !r.OK {
		return "不可用"
	}
	return FormatMbps(r.BitsPerSecond) + "（" + FormatMBps(r.BytesPerSecond) + "）"
}

// FormatBytes 标出约数和原始字节，避免把 MB 当成 Mbps。
func FormatBytes(n int64) string {
	if n < 0 {
		return "不可用"
	}
	return fmt.Sprintf("约 %.2f MB（%d 字节）", float64(n)/DecimalMega, n)
}

// FormatThroughput 是实时上下行。任一方向不可用则整行不可用，避免半真半假。
func FormatThroughput(up, down ByteRate) string {
	if !up.OK || !down.OK {
		return "不可用"
	}
	return "上行 " + FormatPair(up) + " · 下行 " + FormatPair(down)
}
