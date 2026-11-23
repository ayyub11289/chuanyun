package speed

import "time"

// Counter 把累计字节差分成速率。第一拍、时间不前进或计数回绕时不给出速率。
type Counter struct {
	prevUp   int64
	prevDown int64
	prevAt   time.Time
	hasPrev  bool
}

// Observe 记录一次隧道累计字节。ok 为假表示这一拍还不能显示速率。
func (c *Counter) Observe(upload, download int64, at time.Time) (up, down ByteRate, ok bool) {
	if upload < 0 || download < 0 {
		c.hasPrev = false
		return ByteRate{}, ByteRate{}, false
	}
	if !c.hasPrev || at.Before(c.prevAt) || !at.After(c.prevAt) || upload < c.prevUp || download < c.prevDown {
		c.prevUp = upload
		c.prevDown = download
		c.prevAt = at
		c.hasPrev = true
		return ByteRate{}, ByteRate{}, false
	}
	elapsed := at.Sub(c.prevAt)
	up = Rates(upload-c.prevUp, elapsed)
	down = Rates(download-c.prevDown, elapsed)
	c.prevUp = upload
	c.prevDown = download
	c.prevAt = at
	ok = up.OK && down.OK
	return up, down, ok
}
