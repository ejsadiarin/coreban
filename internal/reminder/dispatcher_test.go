package reminder_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/reminder"
	"github.com/ejsadiarin/coreban/internal/testharness"
)

// inbox is a fake notifier. Tests share one database, so a test reads
// only what reached its own owner's topic.
type inbox struct {
	mu   sync.Mutex
	sent []reminder.Notification
	// fail, when set, is returned for every send instead of delivering.
	fail error
	// afterSend, when set, runs after each delivery.
	afterSend func()
}

func (n *inbox) Notify(_ context.Context, msg reminder.Notification) error {
	if n.fail != nil {
		return n.fail
	}
	n.mu.Lock()
	n.sent = append(n.sent, msg)
	n.mu.Unlock()
	if n.afterSend != nil {
		n.afterSend()
	}
	return nil
}

func (n *inbox) to(topic string) []reminder.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []reminder.Notification
	for _, m := range n.sent {
		if m.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}

// tickAt runs one dispatch with the clock reading now.
func tickAt(t *testing.T, ctx context.Context, n reminder.Notifier, now time.Time) {
	t.Helper()
	d := reminder.NewDispatcher(testharness.Postgres(t), n, func() time.Time { return now })
	if err := d.Tick(ctx); err != nil {
		t.Fatalf("tick at %s: %v", now, err)
	}
}

// subscribed is an owner with their own notification target in tz.
func subscribed(t *testing.T, tz string) (owner, string) {
	o, _ := newOwner(t)
	topic := "coreban-" + uuid.NewString()[:12]
	o.setTarget(topic, tz)
	return o, topic
}

func TestAOneOffIsSentAtItsMomentAndOnlyOnce(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila, _ := time.LoadLocation("Asia/Manila")
	at := future(manila, 2*time.Hour)
	r := o.create("renew the cert", wall(at))
	n := &inbox{}
	ctx := context.Background()

	tickAt(t, ctx, n, at.Add(-time.Second))
	if got := n.to(topic); len(got) != 0 {
		t.Fatalf("sent before its moment: %+v", got)
	}

	tickAt(t, ctx, n, at)
	got := n.to(topic)
	if len(got) != 1 {
		t.Fatalf("sent %d at its moment, want 1", len(got))
	}
	if got[0].Message != "renew the cert" || got[0].Late || !got[0].At.Equal(at) {
		t.Errorf("notification = %+v, want the message, on time, due %s", got[0], at)
	}

	tickAt(t, ctx, n, at.Add(time.Hour))
	if got := n.to(topic); len(got) != 1 {
		t.Errorf("sent %d after a later tick, want still 1", len(got))
	}

	listed := o.list()[0]
	if listed.ID != r.ID || listed.NextFireAt != nil {
		t.Errorf("next_fire_at = %v, want none left", listed.NextFireAt)
	}
	if d := listed.LastDelivery; d == nil || d.Late || d.Error != nil || d.At != at.Format(time.RFC3339) {
		t.Errorf("last_delivery = %+v, want on time at %s", d, at.Format(time.RFC3339))
	}
}

func TestAOneOffMissedWhileDownIsSentOnceFlaggedLate(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila, _ := time.LoadLocation("Asia/Manila")
	at := future(manila, 2*time.Hour)
	o.create("standup", wall(at))
	n := &inbox{}
	ctx := context.Background()

	back := at.Add(3 * time.Hour) // coreban was down through the moment
	tickAt(t, ctx, n, back)
	tickAt(t, ctx, n, back.Add(time.Minute))

	got := n.to(topic)
	if len(got) != 1 {
		t.Fatalf("sent %d, want exactly 1", len(got))
	}
	if !got[0].Late || !got[0].At.Equal(at) || got[0].At.Location().String() != "Asia/Manila" {
		t.Errorf("notification = %+v, want late, due %s in the owner's zone", got[0], at)
	}
	if d := o.list()[0].LastDelivery; d == nil || !d.Late {
		t.Errorf("last_delivery = %+v, want late", d)
	}
}

func TestARestartMidDispatchDoesNotSendTwice(t *testing.T) {
	o, topic := subscribed(t, "UTC")
	at := future(time.UTC, 2*time.Hour)
	o.create("first", wall(at))
	o.create("second", wall(at))

	// The process dies right after the first send: before the dispatcher
	// can record anything, and before it reaches the second.
	ctx, die := context.WithCancel(context.Background())
	n := &inbox{afterSend: die}
	d := reminder.NewDispatcher(testharness.Postgres(t), n, func() time.Time { return at })
	_ = d.Tick(ctx)
	if got := n.to(topic); len(got) != 1 {
		t.Fatalf("sent %d before dying, want 1", len(got))
	}

	// The restarted process dispatches from the same database: it sends
	// what was never reached, and nothing that already went out.
	restarted := &inbox{}
	tickAt(t, context.Background(), restarted, at.Add(time.Second))
	again := restarted.to(topic)
	if len(again) != 1 || again[0].Message == n.to(topic)[0].Message {
		t.Errorf("after the restart sent %+v, want only the reminder the first run never reached", again)
	}
}

func TestConcurrentDispatchersSendEachReminderOnce(t *testing.T) {
	o, topic := subscribed(t, "UTC")
	at := future(time.UTC, 2*time.Hour)
	const count = 20
	for i := range count {
		o.create("r"+string(rune('a'+i)), wall(at))
	}
	n := &inbox{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { tickAt(t, context.Background(), n, at) })
	}
	wg.Wait()

	seen := map[string]int{}
	for _, m := range n.to(topic) {
		seen[m.Message]++
	}
	if len(seen) != count {
		t.Errorf("sent %d distinct reminders, want %d", len(seen), count)
	}
	for msg, c := range seen {
		if c != 1 {
			t.Errorf("%q sent %d times", msg, c)
		}
	}
}

func TestNothingIsSentWithoutANotificationTargetUntilOneIsSet(t *testing.T) {
	o, _ := newOwner(t)
	at := future(time.UTC, 2*time.Hour)
	o.create("held", wall(at))
	n := &inbox{}
	ctx := context.Background()

	tickAt(t, ctx, n, at.Add(time.Minute))
	listed := o.list()[0]
	if listed.LastDelivery != nil || listed.NextFireAt == nil || listed.Paused == nil {
		t.Fatalf("after a tick with no target: %+v, want still pending and paused", listed)
	}

	topic := "coreban-" + uuid.NewString()[:12]
	o.setTarget(topic, "UTC")
	tickAt(t, ctx, n, at.Add(2*time.Minute))
	got := n.to(topic)
	if len(got) != 1 || !got[0].Late {
		t.Errorf("after setting a target: %+v, want it sent once, late", got)
	}
}

func TestAFailedSendIsRecordedAndNotRetried(t *testing.T) {
	o, topic := subscribed(t, "UTC")
	at := future(time.UTC, 2*time.Hour)
	o.create("lost", wall(at))
	ctx := context.Background()

	tickAt(t, ctx, &inbox{fail: errors.New("ntfy: 403 Forbidden")}, at)
	n := &inbox{}
	tickAt(t, ctx, n, at.Add(time.Minute))
	if got := n.to(topic); len(got) != 0 {
		t.Errorf("retried: %+v", got)
	}

	d := o.list()[0].LastDelivery
	if d == nil || d.Error == nil || *d.Error != "ntfy: 403 Forbidden" {
		t.Errorf("last_delivery = %+v, want the failure recorded", d)
	}
	// Rescheduling re-arms it.
	r := o.list()[0]
	rec := o.do(http.MethodPatch, reminders+"/"+r.ID, map[string]any{"schedule": once(wall(future(time.UTC, 3*time.Hour)))})
	expect(t, rec, http.StatusOK)
	if decode[item](t, rec).NextFireAt == nil {
		t.Error("rescheduling a failed reminder did not re-arm it")
	}
}

func TestAReminderNtfyCouldNotTakeYetIsSentOnceItCan(t *testing.T) {
	o, topic := subscribed(t, "UTC")
	at := future(time.UTC, 2*time.Hour)
	o.create("after the reboot", wall(at))
	ctx := context.Background()

	// coreban is back before ntfy is: the catch-up can't get through.
	down := &inbox{fail: fmt.Errorf("ntfy unreachable: %w", reminder.ErrRetryLater)}
	d := reminder.NewDispatcher(testharness.Postgres(t), down, func() time.Time { return at.Add(time.Hour) })
	if err := d.Tick(ctx); !errors.Is(err, reminder.ErrRetryLater) {
		t.Fatalf("tick while ntfy is unreachable = %v, want ErrRetryLater", err)
	}
	if r := o.list()[0]; r.NextFireAt == nil || r.LastDelivery != nil {
		t.Fatalf("after ntfy was unreachable: %+v, want it still pending", r)
	}

	n := &inbox{}
	tickAt(t, ctx, n, at.Add(time.Hour+time.Minute))
	tickAt(t, ctx, n, at.Add(time.Hour+2*time.Minute))
	got := n.to(topic)
	if len(got) != 1 || !got[0].Late || !got[0].At.Equal(at) {
		t.Errorf("once ntfy was back: %+v, want it sent once, late, for %s", got, at)
	}
}
