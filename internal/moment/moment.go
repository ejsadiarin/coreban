// Package moment reads the moments owners type: an RFC 3339 instant, or a
// wall-clock time with no offset in the owner's timezone.
package moment

import (
	"fmt"
	"strings"
	"time"
)

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

// LoadZone loads tz as an owner's IANA zone. "Local" is refused: it means
// the server's zone, not the owner's. So are the host zoneinfo's extras
// (posix/, right/, Factory, posixrules), which LoadLocation finds on a dev
// box but the tzdata embedded in the binary does not have.
func LoadZone(tz string) (*time.Location, error) {
	switch {
	case tz == "", tz == "Local", tz == "Factory", tz == "posixrules",
		strings.HasPrefix(tz, "posix/"), strings.HasPrefix(tz, "right/"):
		return nil, fmt.Errorf("%q is not an IANA zone", tz)
	}
	return time.LoadLocation(tz)
}
