package web

import (
	"testing"
	"time"
)

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 10, 31, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{-time.Minute, "Just now"},
		{0, "Just now"},
		{time.Second, "1 second ago"},
		{2 * time.Second, "2 seconds ago"},
		{59 * time.Second, "59 seconds ago"},
		{time.Minute, "1 minute ago"},
		{24 * time.Minute, "24 minutes ago"},
		{time.Hour, "1 hour ago"},
		{23 * time.Hour, "23 hours ago"},
		{24 * time.Hour, "Yesterday"},
		{38 * time.Hour, "Yesterday"},  // 2026-10-30 00:30.
		{39 * time.Hour, "2 days ago"}, // 2026-10-29 23:30.
		{10 * 24 * time.Hour, "10 days ago"},
		{11 * 24 * time.Hour, "2026-10-20"},
		{400 * 24 * time.Hour, "2025-09-26"},
	}
	for _, tt := range tests {
		if got := relativeTime(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("relativeTime(now - %v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}

func TestTimeElement(t *testing.T) {
	now := time.Date(2026, 10, 31, 14, 30, 0, 0, time.UTC)
	cet := time.FixedZone("CET", 3600)
	got := string(timeElement(time.Date(2026, 10, 31, 15, 6, 7, 0, cet), now))
	want := `<time datetime="2026-10-31T14:06:07Z" title="2026-10-31T14:06:07+00:00">23 minutes ago</time>`
	if got != want {
		t.Fatalf("timeElement = %s, want %s", got, want)
	}
}
