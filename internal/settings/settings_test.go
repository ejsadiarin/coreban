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
	Saved     bool    `json:"saved"`
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

func (o owner) put(topic any) *httptest.ResponseRecorder {
	o.t.Helper()
	return o.do(http.MethodPut, map[string]any{"ntfy_topic": topic})
}

func topicOf(s settings) string {
	if s.NtfyTopic == nil {
		return "<none>"
	}
	return *s.NtfyTopic
}

func TestANewOwnerHasNoTargetUntilTheySaveOne(t *testing.T) {
	o, _ := newOwner(t)
	if got := o.get(); got.NtfyTopic != nil || got.Saved {
		t.Fatalf("fresh owner settings = topic %s, saved %v; want no topic, unsaved", topicOf(got), got.Saved)
	}

	// A saved field in the request is ignored, not stored.
	rec := o.do(http.MethodPut, map[string]any{"ntfy_topic": "coreban-ejs_phone", "saved": false})
	if got := decode(t, rec); rec.Code != http.StatusOK || topicOf(got) != "coreban-ejs_phone" || !got.Saved {
		t.Fatalf("put: status = %d, body %s; want 200 with the saved settings", rec.Code, rec.Body)
	}
	if got := o.get(); topicOf(got) != "coreban-ejs_phone" || !got.Saved {
		t.Errorf("after put: topic %s, saved %v; want coreban-ejs_phone, saved", topicOf(got), got.Saved)
	}

	// Clearing the topic leaves the owner without a notification target.
	o.put(nil)
	if got := o.get(); got.NtfyTopic != nil {
		t.Errorf("after clearing: topic %s, want none", topicOf(got))
	}
}

func TestTheTimezoneIsTheGatewaysAndNoLongerASetting(t *testing.T) {
	o, _ := newOwner(t)
	// A client that still sends one, valid or not, isn't refused; it is
	// ignored, neither stored nor shown.
	for _, tz := range []any{"Asia/Manila", "Mars/Olympus_Mons", ""} {
		rec := o.do(http.MethodPut, map[string]any{"ntfy_topic": "coreban-phone-tz", "timezone": tz})
		if rec.Code != http.StatusOK {
			t.Errorf("put with timezone %q: status = %d, body %s; want 200", tz, rec.Code, rec.Body)
		}
	}
	rec := o.do(http.MethodGet, nil)
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if _, ok := raw["timezone"]; ok || raw["ntfy_topic"] != "coreban-phone-tz" {
		t.Errorf("settings = %s, want the topic and no timezone", rec.Body)
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
	if rec := o.put("coreban-phone"); rec.Code != http.StatusOK {
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
		rec := o.put(c.topic)
		if rec.Code != http.StatusBadRequest || errorOf(t, rec) != c.wantError {
			t.Errorf("topic %q: status = %d, body %s; want 400 %q", c.topic, rec.Code, rec.Body, c.wantError)
		}
	}
	if got := o.get(); topicOf(got) != "coreban-phone" {
		t.Errorf("after rejected puts: topic %s, want unchanged coreban-phone", topicOf(got))
	}

	// The longest allowed name, surrounding whitespace trimmed.
	longest := "coreban-" + string(bytes.Repeat([]byte("a"), 56))
	if rec := o.put("  " + longest + " "); rec.Code != http.StatusOK || topicOf(decode(t, rec)) != longest {
		t.Errorf("64-character topic: status = %d, body %s; want 200 saved trimmed", rec.Code, rec.Body)
	}
	// An empty topic clears the target, like null.
	if rec := o.put(""); rec.Code != http.StatusOK || decode(t, rec).NtfyTopic != nil {
		t.Errorf("empty topic: status = %d, body %s; want 200 with no topic", rec.Code, rec.Body)
	}
}

func TestSettingsArePerOwnerAndTargetedReadsCannotWrite(t *testing.T) {
	target, targetID := newOwner(t)
	target.put("coreban-target-" + targetID.String()[:8])
	want := target.get()

	other, _ := newOwner(t)
	if got := other.get(); got.NtfyTopic != nil || got.Saved {
		t.Errorf("another owner sees topic %s, saved %v; want their own defaults", topicOf(got), got.Saved)
	}

	adminID := uuid.New()
	admin := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}, ActAs: targetID})
	if got := admin.get(); topicOf(got) != topicOf(want) {
		t.Errorf("admin targeted read = %s, want the target's %s", topicOf(got), topicOf(want))
	}
	if rec := admin.put("coreban-admin-" + adminID.String()[:8]); rec.Code != http.StatusOK {
		t.Fatalf("admin put: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := target.get(); topicOf(got) != topicOf(want) {
		t.Errorf("target after admin's put = %s, want unchanged (writes bind to the caller)", topicOf(got))
	}
	adminSelf := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}})
	if got, mine := adminSelf.get(), "coreban-admin-"+adminID.String()[:8]; topicOf(got) != mine {
		t.Errorf("admin's own topic = %s, want %s", topicOf(got), mine)
	}

	nonAdmin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"coreban:read"}, ActAs: targetID})
	if got := nonAdmin.get(); got.NtfyTopic != nil {
		t.Errorf("non-admin with act_as reads topic %s, want their own (none)", topicOf(got))
	}
}

func TestAnOwnerCannotClaimAnotherOwnersTopic(t *testing.T) {
	alice, aliceID := newOwner(t)
	topic := "coreban-alice-" + aliceID.String()[:8]
	if rec := alice.put(topic); rec.Code != http.StatusOK {
		t.Fatalf("alice put: status = %d, body %s", rec.Code, rec.Body)
	}

	mallory, _ := newOwner(t)
	rec := mallory.put(topic)
	if rec.Code != http.StatusConflict || errorOf(t, rec) != "ntfy_topic is already in use; choose another name" {
		t.Errorf("mallory claims alice's topic: status = %d, body %s; want 409", rec.Code, rec.Body)
	}
	if got := mallory.get(); got.NtfyTopic != nil {
		t.Errorf("mallory's topic = %s, want none", topicOf(got))
	}

	// Saving your own topic again is not a conflict, and a released topic
	// can be claimed.
	if rec := alice.put(topic); rec.Code != http.StatusOK {
		t.Errorf("alice re-saves her topic: status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	alice.put(nil)
	if rec := mallory.put(topic); rec.Code != http.StatusOK {
		t.Errorf("claim a released topic: status = %d, body %s; want 200", rec.Code, rec.Body)
	}
}
