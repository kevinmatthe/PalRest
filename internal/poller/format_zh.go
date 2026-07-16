package poller

import (
	"fmt"
	"time"
)

// formatDurationZH renders durations for in-game Chinese announcements.
func formatDurationZH(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d小时%d分", h, m)
	case h > 0:
		return fmt.Sprintf("%d小时", h)
	case m > 0 && s > 0:
		return fmt.Sprintf("%d分%d秒", m, s)
	case m > 0:
		return fmt.Sprintf("%d分", m)
	default:
		return fmt.Sprintf("%d秒", s)
	}
}

// formatTimeZH formats reset times in Asia/Shanghai for player-facing messages.
func formatTimeZH(t time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return t.In(loc).Format("01月02日 15:04")
}
