package reminder_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (o owner) createScheduled(message string, sched map[string]any) item {
	o.t.Helper()
	rec := o.do(http.MethodPost, reminders, map[string]any{"message": message, "schedule": sched})
	expect(o.t, rec, http.StatusCreated)
	return decode[item](o.t, rec)
}

func daily(at string) map[string]any { return map[string]any{"kind": "daily", "time": at} }

func weekly(at string, days ...string) map[string]any {
	return map[string]any{"kind": "weekly", "time": at, "weekdays": days}
}

func every(n int, from, at string) map[string]any {
	return map[string]any{"kind": "every", "days": n, "from": from, "time": at}
}

func mustLoad(t *testing.T, tz string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func parseNext(t *testing.T, r item) time.Time {
	t.Helper()
	if r.NextFireAt == nil {
		t.Fatalf("%q has no next firing", r.Message)
	}
	next, err := time.Parse(time.RFC3339, *r.NextFireAt)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestEachPresetShowsItsNextFiringInTheOwnersTimezone(t *testing.T) {
	o, _ := newOwner(t)
	o.setTarget(nil, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	now := time.Now()

	d := o.createScheduled("workout", daily("07:00"))
	if d.Schedule.Kind != "daily" || d.Schedule.Time != "07:00" {
		t.Errorf("daily schedule = %+v", d.Schedule)
	}
	next := parseNext(t, d)
	if _, off := next.Zone(); off != 8*3600 || next.In(manila).Hour() != 7 || next.Minute() != 0 ||
		!next.After(now) || next.Sub(now) > 24*time.Hour {
		t.Errorf("daily next = %s, want the next 07:00 in +08:00", *d.NextFireAt)
	}

	w := o.createScheduled("CKA drills", weekly("19:30", "fr", "MO", "we", "MO"))
	if !slices.Equal(w.Schedule.Weekdays, []string{"MO", "WE", "FR"}) {
		t.Errorf("weekdays = %v, want [MO WE FR] in week order", w.Schedule.Weekdays)
	}
	next = parseNext(t, w).In(manila)
	if wd := next.Weekday(); (wd != time.Monday && wd != time.Wednesday && wd != time.Friday) ||
		next.Hour() != 19 || next.Minute() != 30 || next.Sub(now) > 7*24*time.Hour {
		t.Errorf("weekly next = %s, want a Mon/Wed/Fri 19:30 within a week", next)
	}

	e := o.createScheduled("shell drills", every(3, "2026-01-01", "08:00"))
	if e.Schedule.Days != 3 || e.Schedule.From != "2026-01-01" {
		t.Errorf("every schedule = %+v", e.Schedule)
	}
	next = parseNext(t, e).In(manila)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, manila)
	days := int(time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, manila).Sub(from).Hours()/24 + 0.5)
	if days%3 != 0 || next.Hour() != 8 || next.Sub(now) > 3*24*time.Hour {
		t.Errorf("every-3 next = %s (%d days from the start), want a multiple of 3 days at 08:00 within 3 days", next, days)
	}
}

func TestRecurringSchedulesAreRejectedWithAReason(t *testing.T) {
	o, _ := newOwner(t)
	for _, c := range []struct {
		sched map[string]any
		want  string
	}{
		{map[string]any{"kind": "hourly"}, `schedule.kind must be "once", "daily", "weekly", or "every"`},
		{daily("7am"), "schedule.time must be a time of day such as 07:00"},
		{daily("24:00"), "schedule.time must be a time of day such as 07:00"},
		{weekly("07:00"), "schedule.weekdays must list days such as MO, WE, FR"},
		{weekly("07:00", "XX"), "schedule.weekdays must list days such as MO, WE, FR"},
		{every(0, "2026-01-01", "07:00"), "schedule.days must be a whole number from 1 to 365"},
		{every(3, "Jan 1", "07:00"), "schedule.from must be a date such as 2026-11-10"},
	} {
		rec := o.do(http.MethodPost, reminders, map[string]any{"message": "x", "schedule": c.sched})
		expect(t, rec, http.StatusBadRequest)
		if got := decode[map[string]string](t, rec)["error"]; got != c.want {
			t.Errorf("%v: error = %q, want %q", c.sched, got, c.want)
		}
	}
}

func TestEditingSwitchesBetweenPresets(t *testing.T) {
	o, _ := newOwner(t)
	r := o.create("stretch", wall(future(time.UTC, time.Hour)))
	path := reminders + "/" + r.ID

	rec := o.do(http.MethodPatch, path, map[string]any{"schedule": daily("06:15")})
	expect(t, rec, http.StatusOK)
	got := decode[item](t, rec)
	if got.Schedule.Kind != "daily" || got.Schedule.At != "" || parseNext(t, got).Hour() != 6 {
		t.Errorf("after switching to daily: %+v", got)
	}

	rec = o.do(http.MethodPatch, path, map[string]any{"schedule": once(wall(future(time.UTC, 48*time.Hour)))})
	expect(t, rec, http.StatusOK)
	if got := decode[item](t, rec); got.Schedule.Kind != "once" || got.Schedule.Time != "" {
		t.Errorf("after switching back to once: %+v", got.Schedule)
	}
}

// sends returns the local wall times (in loc) of what reached topic.
func sends(n *inbox, topic string, loc *time.Location) []string {
	var out []string
	for _, m := range n.to(topic) {
		out = append(out, m.At.In(loc).Format("Mon 2006-01-02 15:04 MST"))
	}
	return out
}

func TestADailyReminderKeepsItsLocalTimeAcrossDaylightSaving(t *testing.T) {
	o, topic := subscribed(t, "America/New_York")
	ny := mustLoad(t, "America/New_York")
	o.createScheduled("standup", daily("07:00"))
	n := &inbox{}
	ctx := context.Background()

	// Clocks spring forward on Sun 14 Mar 2027 and fall back on Sun 7 Nov.
	for _, at := range []time.Time{
		time.Date(2027, 3, 13, 7, 0, 0, 0, ny),
		time.Date(2027, 3, 14, 6, 59, 0, 0, ny), // a minute early: nothing
		time.Date(2027, 3, 14, 7, 0, 0, 0, ny),
		time.Date(2027, 3, 15, 7, 0, 0, 0, ny),
	} {
		tickAt(t, ctx, n, at)
	}
	want := []string{"Sat 2027-03-13 07:00 EST", "Sun 2027-03-14 07:00 EDT", "Mon 2027-03-15 07:00 EDT"}
	if got := sends(n, topic, ny); !slices.Equal(got, want) {
		t.Errorf("spring forward sends = %v, want %v", got, want)
	}

	n = &inbox{}
	for _, at := range []time.Time{
		time.Date(2027, 11, 6, 7, 0, 0, 0, ny),
		time.Date(2027, 11, 7, 7, 0, 0, 0, ny),
		time.Date(2027, 11, 8, 7, 0, 0, 0, ny),
	} {
		tickAt(t, ctx, n, at)
	}
	want = []string{"Sat 2027-11-06 07:00 EDT", "Sun 2027-11-07 07:00 EST", "Mon 2027-11-08 07:00 EST"}
	if got := sends(n, topic, ny); !slices.Equal(got, want) {
		t.Errorf("fall back sends = %v, want %v", got, want)
	}
	for _, m := range n.to(topic) {
		if m.Late {
			t.Errorf("%s sent late", m.At)
		}
	}
}

func TestAReminderInTheSpringForwardGapStillFiresThatDay(t *testing.T) {
	o, topic := subscribed(t, "America/New_York")
	ny := mustLoad(t, "America/New_York")
	o.createScheduled("02:30 doesn't exist on the 14th", daily("02:30"))
	n := &inbox{}
	for day := 13; day <= 15; day++ {
		for hour := 0; hour < 24; hour++ {
			tickAt(t, context.Background(), n, time.Date(2027, 3, day, hour, 30, 0, 0, ny))
		}
	}
	// 02:30 doesn't exist on the 14th: it fires as the clock reads after
	// the jump, 03:30 (RFC 5545's rule for a nonexistent local time).
	want := []string{"Sat 2027-03-13 02:30 EST", "Sun 2027-03-14 03:30 EDT", "Mon 2027-03-15 02:30 EDT"}
	if got := sends(n, topic, ny); !slices.Equal(got, want) {
		t.Errorf("sends = %v, want %v", got, want)
	}
}

func TestAWeekdayReminderFiresOnlyOnItsDays(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	o.createScheduled("CKA", weekly("07:00", "MO", "WE", "FR"))
	n := &inbox{}
	for day := 4; day <= 10; day++ { // Mon 4 Jan 2027 through Sun 10 Jan
		tickAt(t, context.Background(), n, time.Date(2027, 1, day, 7, 0, 0, 0, manila))
	}
	want := []string{"Mon 2027-01-04 07:00 PST", "Wed 2027-01-06 07:00 PST", "Fri 2027-01-08 07:00 PST"}
	if got := sends(n, topic, manila); !slices.Equal(got, want) {
		t.Errorf("sends = %v, want %v", got, want)
	}
}

func TestAnEveryNDaysReminderCountsFromItsStart(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	o.createScheduled("shell drills", every(3, "2027-01-01", "07:00"))
	n := &inbox{}
	for day := 1; day <= 10; day++ {
		tickAt(t, context.Background(), n, time.Date(2027, 1, day, 7, 0, 0, 0, manila))
	}
	want := []string{"Fri 2027-01-01 07:00 PST", "Mon 2027-01-04 07:00 PST", "Thu 2027-01-07 07:00 PST", "Sun 2027-01-10 07:00 PST"}
	if got := sends(n, topic, manila); !slices.Equal(got, want) {
		t.Errorf("sends = %v, want %v", got, want)
	}
}

func TestMissedRecurringOccurrencesAreSkippedNotReplayed(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	o.createScheduled("water", daily("07:00"))
	n := &inbox{}
	ctx := context.Background()

	tickAt(t, ctx, n, time.Date(2027, 2, 1, 7, 0, 0, 0, manila))
	// Down from the 1st until midday on the 4th: three occurrences missed.
	tickAt(t, ctx, n, time.Date(2027, 2, 4, 12, 0, 0, 0, manila))
	if got := sends(n, topic, manila); !slices.Equal(got, []string{"Mon 2027-02-01 07:00 PST"}) {
		t.Errorf("sends = %v, want only the 1st: missed ones skipped", got)
	}
	tickAt(t, ctx, n, time.Date(2027, 2, 5, 7, 0, 0, 0, manila))
	got := n.to(topic)
	if len(got) != 2 || got[1].Late || !got[1].At.Equal(time.Date(2027, 2, 5, 7, 0, 0, 0, manila)) {
		t.Errorf("after the gap: %+v, want the 5th on time", got)
	}
}

func TestChangingTheOwnersTimezoneReanchorsFutureOccurrences(t *testing.T) {
	o, _ := newOwner(t)
	topic := "coreban-" + uuid.NewString()[:12]
	o.setTarget(topic, "UTC")
	o.createScheduled("journal", daily("07:00"))

	o.setTarget(topic, "Asia/Manila")
	next := parseNext(t, o.list()[0])
	if _, off := next.Zone(); off != 8*3600 || next.Hour() != 7 {
		t.Errorf("next after moving to Manila = %s, want 07:00+08:00", next.Format(time.RFC3339))
	}

	n := &inbox{}
	manila := mustLoad(t, "Asia/Manila")
	at := time.Date(2027, 4, 1, 7, 0, 0, 0, manila)
	tickAt(t, context.Background(), n, at.Add(-time.Hour)) // 07:00 in some other zone is not it
	tickAt(t, context.Background(), n, at)
	if got := sends(n, topic, manila); !slices.Equal(got, []string{"Thu 2027-04-01 07:00 PST"}) {
		t.Errorf("sends = %v, want 07:00 Manila", got)
	}
}

func TestAReminderInTheFallBackOverlapFiresOnce(t *testing.T) {
	o, topic := subscribed(t, "America/New_York")
	ny := mustLoad(t, "America/New_York")
	o.createScheduled("01:30 happens twice on the 7th", daily("01:30"))
	n := &inbox{}
	first := time.Date(2027, 11, 7, 5, 30, 0, 0, time.UTC) // 01:30 EDT
	for _, at := range []time.Time{first, first.Add(time.Hour) /* 01:30 EST */, first.Add(25 * time.Hour)} {
		tickAt(t, context.Background(), n, at)
	}
	want := []string{"Sun 2027-11-07 01:30 EDT", "Mon 2027-11-08 01:30 EST"}
	if got := sends(n, topic, ny); !slices.Equal(got, want) {
		t.Errorf("sends = %v, want %v", got, want)
	}
}

func TestMovingToAZoneWithTheSameOffsetRightAfterASendDoesNotRepeatIt(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	o.createScheduled("once a day", daily("07:00"))
	n := &inbox{}
	at := time.Date(2027, 5, 3, 7, 0, 0, 0, manila)
	tickAt(t, context.Background(), n, at)

	o.setTarget(topic, "Asia/Singapore") // also +08:00
	tickAt(t, context.Background(), n, at.Add(30*time.Second))
	if got := sends(n, topic, manila); !slices.Equal(got, []string{"Mon 2027-05-03 07:00 PST"}) {
		t.Errorf("sends = %v, want the 07:00 once", got)
	}
}
