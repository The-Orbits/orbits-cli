package output

import (
	"fmt"
	"strings"
	"time"
)

// now is time.Now, replaceable in tests.
var now = time.Now

// When is a time for a person: "3 Oct 2026, 10:30 (1 day ago)", in local
// time. The zero time is a dash.
func When(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return fmt.Sprintf("%s (%s)", t.Local().Format("2 Jan 2006, 15:04"), relative(t))
}

// WhenPtr is When for an optional time; nil is a dash.
func WhenPtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return When(*t)
}

// Ago is a time as a short relative one, for tables: "2 days ago".
func Ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return relative(t)
}

// AgoPtr is Ago for an optional time; nil is a dash.
func AgoPtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return Ago(*t)
}

// WhenString is When for an RFC 3339 string; one that does not parse comes
// back as it was, and "" is a dash.
func WhenString(s string) string {
	if s == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return When(t)
}

func relative(t time.Time) string {
	d := now().Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		s = plural(int(d/time.Minute), "min")
	case d < 24*time.Hour:
		s = plural(int(d/time.Hour), "hour")
	case d < 30*24*time.Hour:
		s = plural(int(d/(24*time.Hour)), "day")
	case d < 365*24*time.Hour:
		s = plural(int(d/(30*24*time.Hour)), "month")
	default:
		s = plural(int(d/(365*24*time.Hour)), "year")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func plural(n int, unit string) string {
	if n == 1 || unit == "min" {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// Seconds is a duration in seconds for a person: 42s, 1:12, or a dash for nil.
func Seconds(s *int) string {
	if s == nil {
		return "—"
	}
	return Elapsed(time.Duration(*s) * time.Second)
}

// Bytes is a size for a person: 512 B, 48 KB, 3.2 MB.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	s := fmt.Sprintf("%.1f", v)
	if v >= 10 {
		s = fmt.Sprintf("%.0f", v)
	}
	return strings.TrimSuffix(s, ".0") + " " + string("KMGTPE"[exp]) + "B"
}
