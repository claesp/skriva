package web

import (
	"testing"
	"time"
)

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12 * time.Hour:                  "12h",
		90 * time.Minute:                "1h30m",
		time.Minute:                     "1m",
		90 * time.Second:                "1m30s",
		720 * time.Hour:                 "720h",
		time.Hour + 30*time.Second:      "1h0m30s",
		30*time.Minute + 15*time.Second: "30m15s",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
