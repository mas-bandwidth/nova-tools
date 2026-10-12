package healthreport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enrolled(audience string) Enrollment {
	return Enrollment{Version: 1, ID: "bob-health", Epoch: 2, Friend: "bob", Store: "127.0.0.1:6380", User: "bob", Coordinator: "ada", Audience: audience, SecretEnv: "NOVA_BOB_HEALTH_CAPABILITY"}
}
func secret() string {
	return base64.RawURLEncoding.EncodeToString([]byte("fixture-only-32-byte-capability!"))
}
func failed(e Enrollment) Event {
	return Event{Sequence: 7, Episode: "episode-3", Transition: "failed", Friend: e.Friend, Store: e.Store, User: e.User, Coordinator: e.Coordinator, Audience: e.Audience, FailedAt: time.Date(2026, 10, 12, 23, 0, 0, 0, time.UTC), Class: "auth", Reason: "login refused (WRONGPASS)"}
}
func encoded(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

func TestCapabilityCannotAuthorizeAnotherIdentityOrEpoch(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	c, err := NewCapability(e, secret())
	require.NoError(t, err)
	h := http.Header{}
	c.headers(h)
	got, err := c.Authenticate(h)
	require.NoError(t, err)
	assert.Equal(t, e, got.Enrollment())
	for _, change := range []func(http.Header){
		func(h http.Header) { h.Set(HeaderID, "alice-health") },
		func(h http.Header) { h.Set(HeaderEpoch, "1") },
		func(h http.Header) { h.Set(HeaderEpoch, "02") },
		func(h http.Header) {
			h.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32))))
		},
		func(h http.Header) { h.Add("Authorization", h.Get("Authorization")) },
		func(h http.Header) { h.Add(HeaderEpoch, "2") },
		func(h http.Header) { h.Del("Authorization") },
	} {
		bad := h.Clone()
		change(bad)
		_, err := c.Authenticate(bad)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret())
	}
	_, err = (Principal{}).ParseEvent(encoded(t, failed(e)))
	require.Error(t, err)
	for _, format := range []string{"%v", "%+v", "%#v"} {
		assert.NotContains(t, fmt.Sprintf(format, c), secret())
	}
}

func TestGrantCannotSelectAnUnverifiedOrAmbiguousAudience(t *testing.T) {
	t.Parallel()
	for _, audience := range []string{
		"http://127.0.0.1:8443" + Path,
		"https://owner:secret@127.0.0.1:8443" + Path,
		"https://127.0.0.1:8443/verbs",
		"https://127.0.0.1:8443" + Path + "?secret=x",
		"https://127.0.0.1:8443" + Path + "#fragment",
		"https://127.0.0.1:8443/health/v1/%62us-fault",
	} {
		err := enrolled(audience).Validate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), audience)
	}
	e := enrolled("https://127.0.0.1:8443" + Path)
	for _, modify := range []func(*Enrollment){
		func(e *Enrollment) { e.Epoch = 0 },
		func(e *Enrollment) { e.Friend = "../bob" },
		func(e *Enrollment) { e.SecretEnv = "secret-value" },
		func(e *Enrollment) { e.Store = "127.0.0.1:not-a-port" },
		func(e *Enrollment) { e.Store = "/tmp/../redis.sock" },
	} {
		bad := e
		modify(&bad)
		require.Error(t, bad.Validate())
	}
	for _, raw := range []string{"", secret() + "=", base64.RawURLEncoding.EncodeToString(make([]byte, 31)), base64.RawURLEncoding.EncodeToString(make([]byte, 32))} {
		_, err := NewCapability(e, raw)
		require.Error(t, err)
	}
}

func TestEventIsBoundToEveryEnrolledAuthorityField(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	event := failed(e)
	for _, modify := range []func(*Event){
		func(v *Event) { v.Friend = "alice" },
		func(v *Event) { v.User = "alice" },
		func(v *Event) { v.Store = "127.0.0.1:6381" },
		func(v *Event) { v.Coordinator = "eve" },
		func(v *Event) { v.Audience = "https://127.0.0.1:8444" + Path },
		func(v *Event) { v.Sequence = 0 },
		func(v *Event) { v.Class = "provider-limit" },
		func(v *Event) { v.Reason = "WRONGPASS\nsecret" },
		func(v *Event) { v.RecoveredAt = v.FailedAt.Add(time.Second) },
	} {
		bad := event
		modify(&bad)
		_, err := ParseEvent(encoded(t, bad), e)
		require.Error(t, err)
	}
	for _, epoch := range []uint64{2, 3} {
		rotated := e
		rotated.Epoch = epoch
		got, err := ParseEvent(encoded(t, event), rotated)
		require.NoError(t, err)
		assert.Equal(t, event, got)
	}
	recovery := event
	recovery.Transition = "recovered"
	recovery.RecoveredAt = event.FailedAt.Add(time.Minute)
	require.NoError(t, recovery.Validate(e))
	recovery.RecoveredAt = event.FailedAt.Add(-time.Second)
	require.Error(t, recovery.Validate(e))
}

func TestStrictParserAndReceiptPreventTwoMeaningsOrWrongAcknowledgments(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	event := failed(e)
	raw := encoded(t, event)
	for _, bad := range [][]byte{
		append(append([]byte{}, raw...), []byte(` {}`)...),
		[]byte(strings.Replace(string(raw), `"sequence":7`, `"sequence":7,"sequence":8`, 1)),
		[]byte(strings.Replace(string(raw), `"reason":"login refused (WRONGPASS)"`, `"reason":null`, 1)),
		[]byte(strings.Replace(string(raw), `"sequence":7`, `"extra":{},"sequence":7`, 1)),
		[]byte(strings.Replace(string(raw), `"sequence":7`, `"unknown":1,"sequence":7`, 1)),
		[]byte(strings.Repeat(" ", MaxEvent+1)),
	} {
		_, err := ParseEvent(bad, e)
		require.Error(t, err)
	}
	receipt := ReceiptFor(event, raw)
	got, err := parseReceipt(encoded(t, receipt), receipt)
	require.NoError(t, err)
	assert.Equal(t, receipt, got)
	for _, modify := range []func(*Receipt){func(r *Receipt) { r.Sequence-- }, func(r *Receipt) { r.Episode = "older" }, func(r *Receipt) { r.Digest = strings.Repeat("0", 64) }} {
		wrong := receipt
		modify(&wrong)
		_, err := parseReceipt(encoded(t, wrong), receipt)
		require.Error(t, err)
	}
	_, err = ParseEnrollment([]byte(`{"version":1,"version":2}`))
	require.Error(t, err)
}

func TestEveryResolvedAddressMustRemainPrivate(t *testing.T) {
	t.Parallel()
	local := netip.AddrFrom4([4]byte{127, 0, 0, 1})
	tail := netip.AddrFrom4([4]byte{100, 64, 0, 1})
	public := netip.AddrFrom4([4]byte{203, 0, 113, 1}) // documentation-only address; never dialed
	assert.True(t, privateIPs([]netip.Addr{local, tail}))
	assert.False(t, privateIPs([]netip.Addr{local, public}), "mixed DNS must be refused before the first dial")
	assert.False(t, privateIPs(nil))
	assert.False(t, privateIPs([]netip.Addr{{}}))
	assert.False(t, privateIPs([]netip.Addr{netip.AddrFrom4([4]byte{100, 63, 255, 255})}))
	assert.True(t, privateIPs([]netip.Addr{netip.AddrFrom4([4]byte{100, 127, 255, 255})}))
	assert.False(t, privateIPs([]netip.Addr{netip.AddrFrom4([4]byte{100, 128, 0, 0})}))
}

func TestEnrollmentUsesCanonicalFriendNamesAndAlarmWireClasses(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	for _, row := range []struct {
		name  string
		valid bool
	}{
		{"bob-1", true}, {strings.Repeat("a", 64), true},
		{"", false}, {"Bob", false}, {"friend.bob", false}, {"bob_one", false}, {strings.Repeat("a", 65), false},
	} {
		grant := e
		grant.Friend = row.name
		if row.valid {
			assert.NoError(t, grant.Validate())
		} else {
			assert.Error(t, grant.Validate())
		}
	}
	for _, class := range []string{"auth", "connection"} {
		v := failed(e)
		v.Class = class
		_, err := ParseEvent(encoded(t, v), e)
		require.NoError(t, err, "existing send-alarm class remains a valid wire observation")
	}
}
