package reminder

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ejsadiarin/coreban/internal/moment"
)

// Schedule kinds: a one-off moment, or a recurring preset at a time of day
// in the owner's timezone.
const (
	KindOnce   = "once"   // At
	KindDaily  = "daily"  // Time
	KindWeekly = "weekly" // Time on Weekdays
	KindEvery  = "every"  // Time every Days days, counting From
)

// Schedule is when a reminder fires. Only the fields of its Kind are set.
type Schedule struct {
	Kind string     `json:"kind"`
	At   *time.Time `json:"at,omitempty"`
	// Time is the time of day ("07:00") a recurring reminder fires at.
	Time string `json:"time,omitempty"`
	// Weekdays are iCalendar day codes (MO … SU), in week order.
	Weekdays []string `json:"weekdays,omitempty"`
	Days     int      `json:"days,omitempty"`
	// From is the date ("2026-11-10") an every-N-days count starts from.
	From string `json:"from,omitempty"`
}

// scheduleRequest is a schedule as clients send it. At is an RFC 3339
// instant, or a wall-clock time with no offset read in the owner's
// timezone ("2026-11-10T07:00").
type scheduleRequest struct {
	Kind     string   `json:"kind"`
	At       string   `json:"at"`
	Time     string   `json:"time"`
	Weekdays []string `json:"weekdays"`
	Days     int      `json:"days"`
	From     string   `json:"from"`
}

const (
	dateLayout = "2006-01-02"
	timeLayout = "15:04"
	maxEvery   = 365
)

// dayCodes are iCalendar's weekday codes, Sunday first like time.Weekday.
var dayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// weekOrder is the order weekdays are listed in: Monday first.
var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// inWeekOrder lists days' codes Monday first, each once.
func inWeekOrder(days []time.Weekday) []string {
	var out []string
	for _, d := range weekOrder {
		if slices.Contains(days, d) {
			out = append(out, dayCodes[d])
		}
	}
	return out
}

// parseSchedule validates a requested schedule for an owner in loc, now.
func parseSchedule(req scheduleRequest, loc *time.Location, now time.Time) (Schedule, error) {
	if req.Kind == KindOnce {
		at, ok := moment.Parse(strings.TrimSpace(req.At), loc)
		if !ok {
			return Schedule{}, errInvalid("schedule.at must be a date and time such as 2026-11-10T07:00")
		}
		if !at.After(now) {
			return Schedule{}, errInvalid("schedule.at must be in the future")
		}
		return Schedule{Kind: KindOnce, At: &at}, nil
	}
	if req.Kind != KindDaily && req.Kind != KindWeekly && req.Kind != KindEvery {
		return Schedule{}, errInvalid(`schedule.kind must be "once", "daily", "weekly", or "every"`)
	}
	tod, err := time.Parse(timeLayout, strings.TrimSpace(req.Time))
	if err != nil {
		return Schedule{}, errInvalid("schedule.time must be a time of day such as 07:00")
	}
	s := Schedule{Kind: req.Kind, Time: tod.Format(timeLayout)}
	switch req.Kind {
	case KindWeekly:
		if s.Weekdays, err = normalizeWeekdays(req.Weekdays); err != nil {
			return Schedule{}, err
		}
	case KindEvery:
		if req.Days < 1 || req.Days > maxEvery {
			return Schedule{}, errInvalid(fmt.Sprintf("schedule.days must be a whole number from 1 to %d", maxEvery))
		}
		s.Days, s.From = req.Days, now.In(loc).Format(dateLayout)
		if from := strings.TrimSpace(req.From); from != "" {
			if _, err := time.Parse(dateLayout, from); err != nil {
				return Schedule{}, errInvalid("schedule.from must be a date such as 2026-11-10")
			}
			s.From = from
		}
	}
	return s, nil
}

// normalizeWeekdays upper-cases, de-duplicates, and orders day codes.
func normalizeWeekdays(codes []string) ([]string, error) {
	var days []time.Weekday
	for _, c := range codes {
		i := slices.Index(dayCodes, strings.ToUpper(strings.TrimSpace(c)))
		if i < 0 {
			return nil, errInvalid("schedule.weekdays must list days such as MO, WE, FR")
		}
		days = append(days, time.Weekday(i))
	}
	if len(days) == 0 {
		return nil, errInvalid("schedule.weekdays must list days such as MO, WE, FR")
	}
	return inWeekOrder(days), nil
}

// rule is a recurring schedule as iCalendar has it: an RRULE and a
// floating DTSTART (a wall-clock date and time with no zone, held as UTC).
type rule struct {
	freq     string // "DAILY" or "WEEKLY"
	interval int    // DAILY: days between occurrences; 0 means plain daily
	byDay    []time.Weekday
	start    time.Time
}

// ruleOf is the rule for a recurring schedule, starting on today's date in
// the owner's zone (or the schedule's From).
func ruleOf(s Schedule, today string) rule {
	from := today
	if s.From != "" {
		from = s.From
	}
	start, _ := time.Parse(dateLayout+" "+timeLayout, from+" "+s.Time)
	r := rule{freq: "DAILY", start: start}
	switch s.Kind {
	case KindWeekly:
		r.freq = "WEEKLY"
		for _, c := range s.Weekdays {
			r.byDay = append(r.byDay, time.Weekday(slices.Index(dayCodes, c)))
		}
		// iCalendar counts DTSTART as an occurrence, so start on a chosen day.
		for !slices.Contains(r.byDay, r.start.Weekday()) {
			r.start = r.start.AddDate(0, 0, 1)
		}
	case KindEvery:
		r.interval = s.Days
	}
	return r
}

// rrule is the rule's RRULE text.
func (r rule) rrule() string {
	switch {
	case r.freq == "WEEKLY":
		return "FREQ=WEEKLY;BYDAY=" + strings.Join(inWeekOrder(r.byDay), ",")
	case r.interval > 0:
		return "FREQ=DAILY;INTERVAL=" + strconv.Itoa(r.interval)
	}
	return "FREQ=DAILY"
}

// parseRule reads back a stored RRULE and floating DTSTART.
func parseRule(rrule string, start time.Time) (rule, error) {
	r := rule{start: start}
	for _, part := range strings.Split(rrule, ";") {
		key, val, _ := strings.Cut(part, "=")
		switch key {
		case "FREQ":
			r.freq = val
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return rule{}, fmt.Errorf("rrule %q: bad INTERVAL", rrule)
			}
			r.interval = n
		case "BYDAY":
			for _, c := range strings.Split(val, ",") {
				i := slices.Index(dayCodes, c)
				if i < 0 {
					return rule{}, fmt.Errorf("rrule %q: bad BYDAY", rrule)
				}
				r.byDay = append(r.byDay, time.Weekday(i))
			}
		}
	}
	if r.freq != "DAILY" && r.freq != "WEEKLY" {
		return rule{}, fmt.Errorf("rrule %q: unsupported FREQ", rrule)
	}
	if r.freq == "WEEKLY" && len(r.byDay) == 0 {
		return rule{}, fmt.Errorf("rrule %q: WEEKLY needs BYDAY", rrule)
	}
	return r, nil
}

// schedule is the rule as the API shows it.
func (r rule) schedule() Schedule {
	s := Schedule{Kind: KindDaily, Time: r.start.Format(timeLayout)}
	switch {
	case r.freq == "WEEKLY":
		s.Kind, s.Weekdays = KindWeekly, inWeekOrder(r.byDay)
	case r.interval > 0:
		s.Kind, s.Days, s.From = KindEvery, r.interval, r.start.Format(dateLayout)
	}
	return s
}

// startDay is the floating DTSTART's date at midnight.
func (r rule) startDay() time.Time {
	return time.Date(r.start.Year(), r.start.Month(), r.start.Day(), 0, 0, 0, 0, time.UTC)
}

func (r rule) onDay(day time.Time) bool {
	if r.freq == "WEEKLY" {
		return slices.Contains(r.byDay, day.Weekday())
	}
	days := int(day.Sub(r.startDay()).Hours()/24 + 0.5)
	return days%max(r.interval, 1) == 0
}

// next is the first occurrence at or after t, in the owner's zone loc. A
// time of day that a daylight-saving jump skips on some date fires once
// that day, as the clock reads after the jump (02:30 → 03:30), as RFC 5545
// has it; one the clocks pass twice fires the first time.
func (r rule) next(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	day = maxTime(day, r.startDay())
	// Any rule recurs within a week, or within its interval.
	for range 8 + 2*max(r.interval, 1) {
		if r.onDay(day) {
			at := wallTime(day, r.start.Hour(), r.start.Minute(), loc)
			if !at.Before(t) {
				return at
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	panic(fmt.Sprintf("reminder: rule %s has no occurrence after %s", r.rrule(), t))
}

// wallTime is day at hour:minute in loc. A time a forward jump skips is
// read with the offset in force before the jump, which lands it as far
// past the jump as it was into the gap (02:30 → 03:30 EDT). time.Date
// leaves that case unspecified, so it isn't relied on.
func wallTime(day time.Time, hour, minute int, loc *time.Location) time.Time {
	at := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
	if at.Hour() == hour && at.Minute() == minute {
		return at
	}
	_, before := at.Add(-12 * time.Hour).Zone()
	wall := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, time.UTC)
	return wall.Add(-time.Duration(before) * time.Second).In(loc)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
