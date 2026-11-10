// Package reminder holds an owner's reminders and the dispatcher that
// pushes each to the owner's notification target at its moment.
package reminder

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// PausedNoTarget says a reminder will not be sent until its owner sets a
// notification target.
const PausedNoTarget = "no_notification_target"

// DefaultCap is how many reminders one owner may have unless configured
// otherwise (REMINDER_CAP).
const DefaultCap = 100

// maxMessageLen keeps a message well inside one ntfy notification (ntfy
// turns bodies over 4096 bytes into attachments).
const maxMessageLen = 1000

// Delivery is the latest send of a reminder: when it went out, whether it
// was sent after its moment had passed, and why it failed if it did.
type Delivery struct {
	At    time.Time `json:"at"`
	Late  bool      `json:"late"`
	Error *string   `json:"error"`
}

// Reminder is a message the owner wants pushed to them on a schedule. Its
// times are rendered in the owner's timezone.
type Reminder struct {
	ID       uuid.UUID `json:"id"`
	Message  string    `json:"message"`
	Schedule Schedule  `json:"schedule"`
	// NextFireAt is the occurrence still to be sent; nil once none is left.
	NextFireAt *time.Time `json:"next_fire_at"`
	// Paused says why NextFireAt will not be sent when it comes, if so.
	Paused       *string   `json:"paused"`
	LastDelivery *Delivery `json:"last_delivery"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// errInvalid marks a request the owner must correct; its text is the 400
// detail.
type errInvalid string

func (e errInvalid) Error() string { return string(e) }

// normalizeMessage trims a message and checks it can be sent.
func normalizeMessage(raw string) (string, error) {
	msg := strings.TrimSpace(raw)
	switch {
	case msg == "":
		return "", errInvalid("message is required")
	case utf8.RuneCountInString(msg) > maxMessageLen:
		return "", errInvalid(fmt.Sprintf("message must be at most %d characters", maxMessageLen))
	}
	return msg, nil
}

// ErrNotFound is returned for a reminder the owner doesn't have. It never
// distinguishes "missing" from "someone else's".
var ErrNotFound = errors.New("reminder not found")

// ErrCapReached is returned when the owner already has as many reminders
// as one owner may.
var ErrCapReached = errors.New("reminder cap reached")

// ErrRetryLater marks a send that failed for a reason that may pass, such
// as ntfy still starting after a reboot: the dispatcher puts the occurrence
// back and tries again later instead of recording it as failed.
var ErrRetryLater = errors.New("try again later")
