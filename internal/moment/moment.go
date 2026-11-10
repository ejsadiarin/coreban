// Package moment reads the moments owners type: an RFC 3339 instant, or a
// wall-clock time with no offset in the owner's timezone.
package moment

import "time"

var wallLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04"}

// Parse reads s as an RFC 3339 instant, or as a wall-clock time
// ("2026-11-10T07:00") in loc. It reports false for anything else.
func Parse(s string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range wallLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
