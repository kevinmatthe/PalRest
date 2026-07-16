package poller

import (
	"testing"
	"time"
)

func TestFormatDurationZH(t *testing.T) {
	cases := map[time.Duration]string{
		0:              "0秒",
		45 * time.Second: "45秒",
		3 * time.Minute:  "3分",
		90 * time.Minute: "1小时30分",
		2 * time.Hour:    "2小时",
	}
	for in, want := range cases {
		if got := formatDurationZH(in); got != want {
			t.Fatalf("formatDurationZH(%v)=%q want %q", in, got, want)
		}
	}
}

func TestFormatTimeZH(t *testing.T) {
	at := time.Date(2026, 7, 16, 4, 0, 0, 0, time.UTC)
	got := formatTimeZH(at)
	if got != "07月16日 12:00" {
		t.Fatalf("formatTimeZH=%q", got)
	}
}
