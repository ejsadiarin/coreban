package settings_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

const path = "/api/coreban/settings"

type settings struct {
	NtfyTopic *string `json:"ntfy_topic"`
	Timezone  string  `json:"timezone"`
}

// owner is one caller driving the API with a fixed injected identity.
type owner struct {
	t *testing.T
	h http.Handler
}

func as(t *testing.T, id testharness.Identity) owner {
	return owner{t: t, h: testharness.Router(t, id)}
}

func newOwner(t *testing.T) (owner, uuid.UUID) {
	id := uuid.New()
	return as(t, testharness.Identity{Owner: id, Scopes: []string{"coreban:read", "coreban:write"}}), id
}

func (o owner) do(method string, body any) *httptest.ResponseRecorder {
	o.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			o.t.Fatalf("encode body: %v", err)
		}
	}
	rec := httptest.NewRecorder()
	o.h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) settings {
	t.Helper()
	var v settings
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func (o owner) get() settings {
	o.t.Helper()
	rec := o.do(http.MethodGet, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("get settings: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode(o.t, rec)
}

func (o owner) put(topic any, tz string) *httptest.ResponseRecorder {
	o.t.Helper()
	return o.do(http.MethodPut, map[string]any{"ntfy_topic": topic, "timezone": tz})
}

func topicOf(s settings) string {
	if s.NtfyTopic == nil {
		return "<none>"
	}
	return *s.NtfyTopic
}

func TestANewOwnerHasNoTargetAndUTCUntilTheySaveTheirSettings(t *testing.T) {
	o, _ := newOwner(t)
	if got := o.get(); got.NtfyTopic != nil || got.Timezone != "UTC" {
		t.Fatalf("fresh owner settings = topic %s, timezone %q; want no topic, UTC", topicOf(got), got.Timezone)
	}

	rec := o.put("coreban-ejs_phone", "Asia/Manila")
	if got := decode(t, rec); rec.Code != http.StatusOK || topicOf(got) != "coreban-ejs_phone" || got.Timezone != "Asia/Manila" {
		t.Fatalf("put: status = %d, body %s; want 200 with the saved settings", rec.Code, rec.Body)
	}
	if got := o.get(); topicOf(got) != "coreban-ejs_phone" || got.Timezone != "Asia/Manila" {
		t.Errorf("after put: topic %s, timezone %q; want coreban-ejs_phone, Asia/Manila", topicOf(got), got.Timezone)
	}

	// Clearing the topic leaves the owner without a notification target.
	o.put(nil, "Europe/Berlin")
	if got := o.get(); got.NtfyTopic != nil || got.Timezone != "Europe/Berlin" {
		t.Errorf("after clearing: topic %s, timezone %q; want no topic, Europe/Berlin", topicOf(got), got.Timezone)
	}
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestTopicsMustBeACorebanPrefixedNameNeverAURL(t *testing.T) {
	o, _ := newOwner(t)
	if rec := o.put("coreban-phone", "UTC"); rec.Code != http.StatusOK {
		t.Fatalf("seed: status = %d, body %s", rec.Code, rec.Body)
	}

	for _, c := range []struct {
		topic, wantError string
	}{
		{"https://ntfy.sh/coreban-phone", "ntfy_topic must be a topic name, not a URL"},
		{"ntfy.sh/coreban-phone", "ntfy_topic must be a topic name, not a URL"},
		{"phone", "ntfy_topic must start with coreban-"},
		{"Coreban-phone", "ntfy_topic must start with coreban-"},
		{"coreban-", "ntfy_topic must have a name after coreban-"},
		{"coreban-my phone", "ntfy_topic may only use letters, digits, - and _"},
		{"coreban-ph%6Fne", "ntfy_topic may only use letters, digits, - and _"},
		{"coreban-" + string(bytes.Repeat([]byte("a"), 57)), "ntfy_topic must be at most 64 characters"},
	} {
		rec := o.put(c.topic, "UTC")
		if rec.Code != http.StatusBadRequest || errorOf(t, rec) != c.wantError {
			t.Errorf("topic %q: status = %d, body %s; want 400 %q", c.topic, rec.Code, rec.Body, c.wantError)
		}
	}
	if got := o.get(); topicOf(got) != "coreban-phone" {
		t.Errorf("after rejected puts: topic %s, want unchanged coreban-phone", topicOf(got))
	}

	// The longest allowed name, surrounding whitespace trimmed.
	longest := "coreban-" + string(bytes.Repeat([]byte("a"), 56))
	if rec := o.put("  "+longest+" ", "UTC"); rec.Code != http.StatusOK || topicOf(decode(t, rec)) != longest {
		t.Errorf("64-character topic: status = %d, body %s; want 200 saved trimmed", rec.Code, rec.Body)
	}
	// An empty topic clears the target, like null.
	if rec := o.put("", "UTC"); rec.Code != http.StatusOK || decode(t, rec).NtfyTopic != nil {
		t.Errorf("empty topic: status = %d, body %s; want 200 with no topic", rec.Code, rec.Body)
	}
}

func TestTimezoneMustBeAnIANAZone(t *testing.T) {
	o, _ := newOwner(t)
	for _, tz := range []string{
		"", "Local", "Mars/Olympus_Mons", "GMT+8", "../../etc/passwd", "asia/manila",
		// Host zoneinfo extras that the embedded tzdata in the image lacks.
		"posix/Asia/Manila", "right/UTC", "Factory", "posixrules",
	} {
		rec := o.put("coreban-phone", tz)
		if rec.Code != http.StatusBadRequest || errorOf(t, rec) != "timezone must be an IANA zone such as Asia/Manila" {
			t.Errorf("timezone %q: status = %d, body %s; want 400", tz, rec.Code, rec.Body)
		}
	}
	if got := o.get(); got.NtfyTopic != nil || got.Timezone != "UTC" {
		t.Errorf("after rejected puts: %s %q, want the untouched defaults", topicOf(got), got.Timezone)
	}
	for _, tz := range []string{"UTC", "America/Argentina/Buenos_Aires", "Etc/GMT-8"} {
		if rec := o.put(nil, tz); rec.Code != http.StatusOK || decode(t, rec).Timezone != tz {
			t.Errorf("timezone %q: status = %d, body %s; want 200", tz, rec.Code, rec.Body)
		}
	}
}

func TestSettingsArePerOwnerAndTargetedReadsCannotWrite(t *testing.T) {
	target, targetID := newOwner(t)
	target.put("coreban-target-"+targetID.String()[:8], "Asia/Manila")
	want := target.get()

	other, _ := newOwner(t)
	if got := other.get(); got.NtfyTopic != nil || got.Timezone != "UTC" {
		t.Errorf("another owner sees topic %s, timezone %q; want their own defaults", topicOf(got), got.Timezone)
	}

	adminID := uuid.New()
	admin := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}, ActAs: targetID})
	if got := admin.get(); topicOf(got) != topicOf(want) || got.Timezone != want.Timezone {
		t.Errorf("admin targeted read = %s %q, want the target's %s %q", topicOf(got), got.Timezone, topicOf(want), want.Timezone)
	}
	if rec := admin.put("coreban-admin-"+adminID.String()[:8], "Europe/Berlin"); rec.Code != http.StatusOK {
		t.Fatalf("admin put: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := target.get(); topicOf(got) != topicOf(want) || got.Timezone != want.Timezone {
		t.Errorf("target after admin's put = %s %q, want unchanged (writes bind to the caller)", topicOf(got), got.Timezone)
	}
	adminSelf := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}})
	if got := adminSelf.get(); got.Timezone != "Europe/Berlin" {
		t.Errorf("admin's own timezone = %q, want Europe/Berlin", got.Timezone)
	}

	nonAdmin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"coreban:read"}, ActAs: targetID})
	if got := nonAdmin.get(); got.NtfyTopic != nil {
		t.Errorf("non-admin with act_as reads topic %s, want their own (none)", topicOf(got))
	}
}

func TestAnOwnerCannotClaimAnotherOwnersTopic(t *testing.T) {
	alice, aliceID := newOwner(t)
	topic := "coreban-alice-" + aliceID.String()[:8]
	if rec := alice.put(topic, "UTC"); rec.Code != http.StatusOK {
		t.Fatalf("alice put: status = %d, body %s", rec.Code, rec.Body)
	}

	mallory, _ := newOwner(t)
	rec := mallory.put(topic, "UTC")
	if rec.Code != http.StatusConflict || errorOf(t, rec) != "ntfy_topic is already in use; choose another name" {
		t.Errorf("mallory claims alice's topic: status = %d, body %s; want 409", rec.Code, rec.Body)
	}
	if got := mallory.get(); got.NtfyTopic != nil {
		t.Errorf("mallory's topic = %s, want none", topicOf(got))
	}

	// Saving your own topic again is not a conflict, and a released topic
	// can be claimed.
	if rec := alice.put(topic, "Asia/Manila"); rec.Code != http.StatusOK {
		t.Errorf("alice re-saves her topic: status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	alice.put(nil, "UTC")
	if rec := mallory.put(topic, "UTC"); rec.Code != http.StatusOK {
		t.Errorf("claim a released topic: status = %d, body %s; want 200", rec.Code, rec.Body)
	}
}
