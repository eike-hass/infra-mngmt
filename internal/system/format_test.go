package system

import (
	"testing"
	"time"
)

func TestHumanizeAge(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{1 * time.Minute, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{2 * time.Hour, "2 hours ago"},
		{3 * 24 * time.Hour, "3 days ago"},
		{3 * 7 * 24 * time.Hour, "3 weeks ago"},
		{2 * 30 * 24 * time.Hour, "2 months ago"},
		{2 * 365 * 24 * time.Hour, "2 years ago"},
	}
	for _, c := range cases {
		if got := HumanizeAge(now.Add(-c.ago), now); got != c.want {
			t.Errorf("HumanizeAge(-%s) = %q, want %q", c.ago, got, c.want)
		}
	}
}

func TestHumanizeAgeZero(t *testing.T) {
	if got := HumanizeAge(time.Time{}, time.Now()); got != "" {
		t.Errorf("HumanizeAge(zero) = %q, want empty", got)
	}
}

func TestHumanizeAgeFuture(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	if got := HumanizeAge(now.Add(time.Hour), now); got != "just now" {
		t.Errorf("HumanizeAge(future) = %q, want 'just now'", got)
	}
}
