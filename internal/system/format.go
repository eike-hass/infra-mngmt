package system

import (
	"fmt"
	"time"
)

// HumanizeAge renders the age of t as a coarse relative string ("3 weeks ago"),
// matching `docker`/`podman`'s own "CREATED" column wording. A zero time
// returns "" so callers can render an em-dash. now is injected for testability.
func HumanizeAge(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 14*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/(24*7)), "week")
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/(24*30)), "month")
	default:
		return plural(int(d.Hours()/(24*365)), "year")
	}
}

func plural(n int, unit string) string {
	if n < 1 {
		n = 1
	}
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}
