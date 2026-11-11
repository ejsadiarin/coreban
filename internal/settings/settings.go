// Package settings serves each owner's notification target (an ntfy topic
// on the homelab's own ntfy server) and the timezone their schedules are
// read in.
package settings

import (
	"errors"
	"fmt"
	"strings"
	"time"
	// The runtime image ships without zoneinfo; embed it so every IANA zone
	// validates (and later resolves) the same everywhere.
	_ "time/tzdata"
)

// Settings is one owner's notification settings. A nil NtfyTopic means the
// owner has no notification target, so their reminders are not sent. Saved
// says the owner has saved their own; until then these are the defaults,
// which lets a client tell a chosen UTC from an unset one. Writes ignore it.
type Settings struct {
	NtfyTopic *string `json:"ntfy_topic"`
	Timezone  string  `json:"timezone"`
	Saved     bool    `json:"saved"`
}

// defaults are an owner's settings until they save their own.
var defaults = Settings{Timezone: "UTC"}

// TopicPrefix confines owners to coreban's own topics on the homelab ntfy
// server, where coreban's token may write to coreban-* and nothing else.
const TopicPrefix = "coreban-"

// maxTopicLen is ntfy's limit on a topic name, prefix included.
const maxTopicLen = 64

// invalid is a validation failure the owner can fix; its message says how
// and is shown to them as is.
func invalid(msg string) error { return errors.New(msg) }

// normalizeTopic trims topic and checks it is a bare coreban- topic name.
// Empty means no notification target and comes back nil.
func normalizeTopic(topic *string) (*string, error) {
	if topic == nil {
		return nil, nil
	}
	t := strings.TrimSpace(*topic)
	switch {
	case t == "":
		return nil, nil
	case strings.ContainsAny(t, "/:"):
		return nil, invalid("ntfy_topic must be a topic name, not a URL")
	case !strings.HasPrefix(t, TopicPrefix):
		return nil, invalid("ntfy_topic must start with " + TopicPrefix)
	case t == TopicPrefix:
		return nil, invalid("ntfy_topic must have a name after " + TopicPrefix)
	case len(t) > maxTopicLen:
		return nil, invalid(fmt.Sprintf("ntfy_topic must be at most %d characters", maxTopicLen))
	}
	for _, r := range t[len(TopicPrefix):] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return nil, invalid("ntfy_topic may only use letters, digits, - and _")
		}
	}
	return &t, nil
}

// normalize checks v and returns it trimmed, ready to store. Every error it
// returns is a validation failure for the owner.
func normalize(v Settings) (Settings, error) {
	topic, err := normalizeTopic(v.NtfyTopic)
	if err != nil {
		return Settings{}, err
	}
	tz := strings.TrimSpace(v.Timezone)
	if !validTimezone(tz) {
		return Settings{}, invalid("timezone must be an IANA zone such as Asia/Manila")
	}
	return Settings{NtfyTopic: topic, Timezone: tz}, nil
}

// validTimezone reports whether tz names an IANA zone. "Local" is refused:
// it means the server's zone, not the owner's. So are the host zoneinfo's
// extras (posix/, right/, Factory, posixrules), which LoadLocation finds on
// a dev box but the embedded tzdata in the image does not have.
func validTimezone(tz string) bool {
	switch {
	case tz == "", tz == "Local", tz == "Factory", tz == "posixrules",
		strings.HasPrefix(tz, "posix/"), strings.HasPrefix(tz, "right/"):
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}
