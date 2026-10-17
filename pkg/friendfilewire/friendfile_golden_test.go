package friendfilewire

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The golden test of the friendfile area (schema owner rules S1 and S2): every table
// round-trips through pkg/schemaread in binary, padded base64 and JSON text; every
// bound the code enforces accepts the value at the bound and refuses one past it by
// name; a damaged image of each form is refused by name; and the tables read the JSON
// the Go structs they replace write today.

// base is the test clock's origin: a test clock starts at 1000 seconds, never 0.
var base = time.Unix(1000, 0).UTC()

func reportOf(rep *TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

// builder is what every generated XBuilder offers.
type builder[T any] interface {
	Init(...TableAllocator) bool
	Lock() bool
	AsConst() *T
}

// held is a loaded table with the builder that owns its memory.
type held[T any] struct {
	root  *T
	owner any
}

// table is one table's codec and the functions a test needs beside it.
type table struct {
	name  string
	read  func(data []byte, fromJSON bool) (any, schemaread.Report)
	wire  func(v any) []byte
	json  func(v any) []byte
	codec *schemaread.Codec[any]
}

// newTable wires one generated table (S2: binary, padded base64 and JSON text, each
// refused by name when damaged).
func newTable[T any, B any, PB interface {
	*B
	builder[T]
}](name string, fromJSON, loadWire func(PB, []byte, *TableReport) bool, reset func(*T), measureWire func([]byte) (int64, error),
	measure func(*T, ...TableWriteContext) int64, save func(*T, []byte, ...TableWriteContext) int64,
	jsonMeasure func(*T, ...TableAllocator) int64, jsonSave func(*T, []byte, ...TableAllocator) int64) *table {
	load := func(data []byte, isJSON bool) (any, schemaread.Report) {
		b := PB(new(B))
		if !b.Init() {
			return nil, schemaread.Report{Malformed: true}
		}
		var rep TableReport
		var ok bool
		if isJSON {
			ok = fromJSON(b, data, &rep)
		} else {
			ok = loadWire(b, data, &rep)
		}
		if !ok || !b.Lock() {
			return nil, reportOf(&rep, false)
		}
		p := b.AsConst()
		if p == nil {
			return nil, schemaread.Report{Malformed: true}
		}
		return &held[T]{root: p, owner: b}, reportOf(&rep, ok)
	}
	wire := func(v any) []byte {
		h := v.(*held[T])
		n := measure(h.root)
		if n < 0 {
			return nil
		}
		out := make([]byte, n)
		if save(h.root, out) != n {
			return nil
		}
		return out
	}
	text := func(v any) []byte {
		h := v.(*held[T])
		n := jsonMeasure(h.root)
		if n < 0 {
			return nil
		}
		out := make([]byte, n)
		if jsonSave(h.root, out) != n {
			return nil
		}
		return out
	}
	c := &schemaread.Codec[any]{Name: name, Help: "nova-friend help", Reset: func(v *any) {},
		Measure: func(w []byte) error { _, err := measureWire(w); return err },
		LoadWire: func(v *any, data []byte) schemaread.Report {
			p, r := load(data, false)
			*v = p
			return r
		},
		LoadJSON: func(v *any, data []byte) schemaread.Report {
			p, r := load(data, true)
			*v = p
			return r
		},
		SaveWire: func(v *any) []byte {
			if *v == nil {
				return nil
			}
			return wire(*v)
		},
		SaveJSON: func(v *any) []byte {
			if *v == nil {
				return nil
			}
			return text(*v)
		}}
	return &table{name: name, read: load, wire: wire, json: text, codec: c}
}

// tables are the 13 tables of the area, keyed by their schema name.
func tables() map[string]*table {
	return map[string]*table{
		"NoticeName":     newTable("NOTICE_NAME", NoticeNameFromJson, NoticeNameLoadBuilder, NoticeNameReset, NoticeNameLoadMeasureReason, NoticeNameMeasure, NoticeNameSave, NoticeNameToJsonMeasure, NoticeNameToJson),
		"NoticeMark":     newTable("NOTICE_MARK", NoticeMarkFromJson, NoticeMarkLoadBuilder, NoticeMarkReset, NoticeMarkLoadMeasureReason, NoticeMarkMeasure, NoticeMarkSave, NoticeMarkToJsonMeasure, NoticeMarkToJson),
		"NoticeMessage":  newTable("NOTICE_MESSAGE", NoticeMessageFromJson, NoticeMessageLoadBuilder, NoticeMessageReset, NoticeMessageLoadMeasureReason, NoticeMessageMeasure, NoticeMessageSave, NoticeMessageToJsonMeasure, NoticeMessageToJson),
		"NoticeReceiver": newTable("NOTICE_RECEIVER", NoticeReceiverFromJson, NoticeReceiverLoadBuilder, NoticeReceiverReset, NoticeReceiverLoadMeasureReason, NoticeReceiverMeasure, NoticeReceiverSave, NoticeReceiverToJsonMeasure, NoticeReceiverToJson),
		"NoticeEnvelope": newTable("NOTICE_ENVELOPE", NoticeEnvelopeFromJson, NoticeEnvelopeLoadBuilder, NoticeEnvelopeReset, NoticeEnvelopeLoadMeasureReason, NoticeEnvelopeMeasure, NoticeEnvelopeSave, NoticeEnvelopeToJsonMeasure, NoticeEnvelopeToJson),
		"LaneEnd":        newTable("LANE_END", LaneEndFromJson, LaneEndLoadBuilder, LaneEndReset, LaneEndLoadMeasureReason, LaneEndMeasure, LaneEndSave, LaneEndToJsonMeasure, LaneEndToJson),
		"DeliveryCard":   newTable("DELIVERY_CARD", DeliveryCardFromJson, DeliveryCardLoadBuilder, DeliveryCardReset, DeliveryCardLoadMeasureReason, DeliveryCardMeasure, DeliveryCardSave, DeliveryCardToJsonMeasure, DeliveryCardToJson),
		"Delivery":       newTable("DELIVERY", DeliveryFromJson, DeliveryLoadBuilder, DeliveryReset, DeliveryLoadMeasureReason, DeliveryMeasure, DeliverySave, DeliveryToJsonMeasure, DeliveryToJson),
		"FriendRefusal":  newTable("FRIEND_REFUSAL", FriendRefusalFromJson, FriendRefusalLoadBuilder, FriendRefusalReset, FriendRefusalLoadMeasureReason, FriendRefusalMeasure, FriendRefusalSave, FriendRefusalToJsonMeasure, FriendRefusalToJson),
		"FriendStatus":   newTable("FRIEND_STATUS", FriendStatusFromJson, FriendStatusLoadBuilder, FriendStatusReset, FriendStatusLoadMeasureReason, FriendStatusMeasure, FriendStatusSave, FriendStatusToJsonMeasure, FriendStatusToJson),
		"FriendTask":     newTable("FRIEND_TASK", FriendTaskFromJson, FriendTaskLoadBuilder, FriendTaskReset, FriendTaskLoadMeasureReason, FriendTaskMeasure, FriendTaskSave, FriendTaskToJsonMeasure, FriendTaskToJson),
		"FriendQueue":    newTable("FRIEND_QUEUE", FriendQueueFromJson, FriendQueueLoadBuilder, FriendQueueReset, FriendQueueLoadMeasureReason, FriendQueueMeasure, FriendQueueSave, FriendQueueToJsonMeasure, FriendQueueToJson),
		"FriendWatch":    newTable("FRIEND_WATCH", FriendWatchFromJson, FriendWatchLoadBuilder, FriendWatchReset, FriendWatchLoadMeasureReason, FriendWatchMeasure, FriendWatchSave, FriendWatchToJsonMeasure, FriendWatchToJson),
	}
}

// subset reports whether every leaf of want is present and equal in got; a leaf of
// the zero value may be absent, since a variable table reads an absent field as zero.
func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, _ := got.(map[string]any)
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return len(w) == 0 && got == nil
		}
		for i := range w {
			if !subset(w[i], g[i]) {
				return false
			}
		}
		return true
	case nil:
		return got == nil
	case string:
		g, _ := got.(string)
		return g == w
	case bool:
		g, _ := got.(bool)
		return g == w
	case float64:
		g, _ := got.(float64)
		return g == w
	}
	return false
}

// roundTrip puts want through every form (S2): written in each, read back by the
// strict reader, counted under the form it arrived in, and equal as wire and as JSON.
func roundTrip(t *testing.T, tb *table, text string) {
	t.Helper()
	r := schemaread.NewReader(tb.codec)
	var want any
	rep := tb.codec.LoadJSON(&want, []byte(text))
	require.False(t, rep.Malformed, "%s fixture is read", tb.name)
	require.Zero(t, rep.Clamped, "%s fixture is within its bounds", tb.name)
	require.Zero(t, rep.Unknown, "%s fixture names only fields of the table", tb.name)
	wantWire, wantJSON := tb.wire(want), tb.json(want)
	require.NotEmpty(t, wantWire)
	var fixture, written any
	require.NoError(t, json.Unmarshal([]byte(text), &fixture))
	require.NoError(t, json.Unmarshal(wantJSON, &written))
	assert.True(t, subset(fixture, written), "%s: every field of the fixture is written back", tb.name)
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		out, err := r.Write(&want, form)
		require.NoError(t, err, "write %s", form)
		got, result, err := r.Read(out)
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, form, result.Form)
		assert.Equal(t, string(wantWire), string(tb.wire(got)), "%s wire from %s", tb.name, form)
		assert.JSONEq(t, string(wantJSON), string(tb.json(got)), "%s JSON from %s", tb.name, form)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON))
	refuseDamage(t, tb, wantWire, wantJSON)
}

// refuseDamage: a damaged image of each form is refused by name, never read.
func refuseDamage(t *testing.T, tb *table, wire, text []byte) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString(wire)
	flipped := append([]byte(nil), wire...)
	flipped[0] ^= 0xff
	bad := map[string][]byte{
		"binary cut after one byte":  wire[:1],
		"binary cut in half":         wire[:len(wire)/2],
		"binary one byte short":      wire[:len(wire)-1],
		"binary form byte flipped":   flipped,
		"base64 cut at three":        []byte(b64[:3]),
		"base64 cut in half":         []byte(b64[:len(b64)/2/4*4]),
		"base64 not padded":          []byte(strings.TrimRight(b64, "=") + "A"),
		"base64 with a foreign byte": []byte(b64[:4] + "!" + b64[5:]),
		"json cut after one byte":    text[:1],
		"json cut in half":           text[:len(text)/2],
		"json cut at three quarters": text[:len(text)*3/4],
		"empty":                      {},
		"text not a table":           []byte("not a table"),
	}
	for name, image := range bad {
		r := schemaread.NewReader(tb.codec)
		_, _, err := r.Read(image)
		var refusal *schemaread.Refusal
		if assert.ErrorAs(t, err, &refusal, "%s: %s: want a named refusal", tb.name, name) {
			assert.Equal(t, tb.name, refusal.Name, "%s: the refusal names the table", name)
			assert.NotEmpty(t, refusal.Reason, "%s: the refusal names its reason", name)
		}
	}
}

// refuseJSON: a value one past a bound is refused as clamped, never shortened.
func refuseJSON(t *testing.T, tb *table, text, field string) {
	t.Helper()
	_, _, err := schemaread.NewReader(tb.codec).Read([]byte(text))
	var refusal *schemaread.Refusal
	require.ErrorAs(t, err, &refusal, "%s past its bound: want a named refusal", field)
	assert.Equal(t, schemaread.ReasonClamped, refusal.Reason, "%s", field)
}

// acceptJSON: a value at a bound is read in every form.
func acceptJSON(t *testing.T, tb *table, text, field string) {
	t.Helper()
	t.Run("accept "+field, func(t *testing.T) { roundTrip(t, tb, text) })
}

func j(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func names(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"s": fmt.Sprintf("friend-%03d", i)}
	}
	return out
}

func message(from, token, body string, listed int) map[string]any {
	return map[string]any{
		"ModelActivity": true, "ID": "01J0000000000000000000000A", "From": from, "To": names(listed), "CC": names(listed / 2),
		"Subject": "a subject", "Re": "01J0000000000000000000000B", "Card": "a10-defs-friend-files", "Head": strings.Repeat("a", 40),
		"Kind": "ask", "At": base.Format(time.RFC3339Nano), "Body": body, "Wait": int64(5 * time.Minute), "Due": base.Add(5 * time.Minute).Format(time.RFC3339Nano),
		"Token": token, "Once": "once-key",
		"Marks": []map[string]any{{"Key": "k1", "Field": "f1", "Value": "v1", "Clear": false, "Forward": true, "Once": false}, {"Key": "k2", "Field": "f2", "Value": "v2", "Clear": true, "Forward": false, "Once": true}},
	}
}

// long is text past any wire short form: multibyte, so a byte count and a rune count differ.
func long(n int) string { return strings.Repeat("é", n) }

// listKeys are the JSON names of every list the structs write: the lists of text and of
// tables, and the map written as a list of entries. A struct leaves one nil and
// encoding/json writes null for it unless the field is omitempty.
var listKeys = map[string]bool{"To": true, "CC": true, "Marks": true, "senders": true, "recovery_messages": true,
	"tasks": true, "cards": true, "refused": true}

// nullListsAsEmpty is the one place the test says that a nil list and an empty list are
// the same thing to a table: the source's JSON is read through it before it is compared.
func nullListsAsEmpty(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil && listKeys[k] {
				x[k] = []any{}
				continue
			}
			x[k] = nullListsAsEmpty(e)
		}
	case []any:
		for i := range x {
			x[i] = nullListsAsEmpty(x[i])
		}
	}
	return v
}

// toTableShape is the bridge the writers cross when a record moves to the table, the two
// shapes the compiler has no direct form for: a list of text (to, cc, senders) becomes a
// list of one-field tables, and a map[string]int (Status.Refused) becomes a list of verb
// and count entries in the key's sorted order, the order encoding/json writes a map in.
// fromTableShape is the way back; it keeps an empty list as an empty list.
func toTableShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil && listKeys[k] {
				// a nil Go slice or map is JSON null, and a list of a table is never null: it is empty.
				x[k] = []any{}
				continue
			}
			switch k {
			case "To", "CC", "senders":
				if list, ok := e.([]any); ok {
					out := make([]any, len(list))
					for i, s := range list {
						out[i] = map[string]any{"s": s}
					}
					x[k] = out
					continue
				}
			case "refused":
				if m, ok := e.(map[string]any); ok {
					keys := make([]string, 0, len(m))
					for key := range m {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					list := make([]any, 0, len(keys))
					for _, key := range keys {
						list = append(list, map[string]any{"verb": key, "count": m[key]})
					}
					x[k] = list
					continue
				}
			}
			x[k] = toTableShape(e)
		}
	case []any:
		for i := range x {
			x[i] = toTableShape(x[i])
		}
	}
	return v
}

func fromTableShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			list, _ := e.([]any)
			switch k {
			case "To", "CC", "senders":
				out := make([]any, len(list))
				for i, entry := range list {
					out[i] = entry.(map[string]any)["s"]
				}
				x[k] = out
				continue
			case "refused":
				m := map[string]any{}
				for _, entry := range list {
					m[entry.(map[string]any)["verb"].(string)] = entry.(map[string]any)["count"]
				}
				x[k] = m
				continue
			}
			x[k] = fromTableShape(e)
		}
	case []any:
		for i := range x {
			x[i] = fromTableShape(x[i])
		}
	}
	return v
}

func reshape(text []byte, f func(any) any) []byte {
	var v any
	if err := json.Unmarshal(text, &v); err != nil {
		panic(err)
	}
	return []byte(j(f(v)))
}

func TestFriendFilesGolden(t *testing.T) {
	t.Parallel()
	all := tables()
	require.Len(t, all, 13, "the area declares thirteen tables")

	t.Run("small", func(t *testing.T) {
		t.Parallel()
		fixtures := map[string]string{
			"NoticeName":     `{"s":"rowan"}`,
			"NoticeMark":     j(map[string]any{"Key": "k", "Field": "f", "Value": "v", "Clear": true, "Forward": false, "Once": true}),
			"NoticeMessage":  j(message("rowan", "tok", "body", 3)),
			"NoticeReceiver": `{"identity":"stella","redis":"127.0.0.1:6381","proof":"prove it","acknowledge":"ack it"}`,
			"NoticeEnvelope": j(map[string]any{
				"local_receiver": map[string]any{"identity": "stella", "redis": "127.0.0.1:6381", "proof": "p", "acknowledge": "a"},
				"saved_at":       base.Format(time.RFC3339Nano), "hook_epoch": 7, "hook_role": "friend", "event_id": "01J0000000000000000000000C",
				"challenge": message("rowan", "", "challenge", 1), "messages": 3, "senders": names(2),
				"recovery_since": "01J0000000000000000000000D", "recovery_coverage": "oldest retained 01J0000000000000000000000E",
				"recovery_messages": []any{message("aurora", "", "one", 1), message("emma", "", "two", 0)}, "possible_duplicates": true,
			}),
			"LaneEnd": j(map[string]any{"Exit": 2, "Err": "boom", "Wall": int64(90 * time.Second), "Cap": "10m", "Rejected": "bash", "Turns": 3,
				"Restart": base.Format(time.RFC3339Nano), "Started": base.Add(-time.Hour).Format(time.RFC3339Nano), "NoReport": true,
				"Capped": int64(2 * time.Hour), "Tier": "pro", "Overrun": int64(time.Minute), "Tail": "line one\nline two"}),
			"DeliveryCard": j(map[string]any{"card": "c1", "job": "c1.w1~21.g1", "col": "ready", "kind": "work", "branch": "sprint/c1", "tier": "pro",
				"stream": "v15", "attempt": 2, "gen": 4, "epoch": uint64(21), "repo": "mas-bandwidth/nova-tools", "base": "sprint/v15", "brief": "RESULT: c1\n", "lane": 1}),
			"Delivery":      j(map[string]any{"friend": "rowan", "cards": []any{map[string]any{"card": "c1", "job": "j1", "brief": "b"}, map[string]any{"card": "c2", "job": "j2", "brief": "b2", "epoch": 21}}}),
			"FriendRefusal": `{"verb":"friend cards","count":3}`,
			"FriendStatus": j(map[string]any{"friend": "rowan", "harness": "claude", "started": base.Format(time.RFC3339Nano), "at": base.Format(time.RFC3339Nano),
				"seat": "rowan", "beats": 9, "last_beat": base.Format(time.RFC3339Nano), "beat_error": "e", "store_error": "s", "width": 2, "session": "ok",
				"session_id": "sid", "session_reason": "r", "broken_at": base.Format(time.RFC3339Nano), "limit_kind": "limit", "limit_until": base.Format(time.RFC3339Nano),
				"mode": "one-shot", "lanes": "1:s:c/1", "paced": 1, "window": "5h 62% 7d 31%", "pacing": "80", "held_known": true, "held": 3, "inbox": 4, "missing": 1,
				"inbox_error": "ie", "held_from": "friend cards", "refused": []any{map[string]any{"verb": "friend beat", "count": 1}, map[string]any{"verb": "friend cards", "count": 2}},
				"last_refusal": "friend cards: why", "last_refusal_at": base.Format(time.RFC3339Nano), "proof": "ok", "proof_at": base.Format(time.RFC3339Nano)}),
			"FriendTask":  `{"gen":2,"job":"j","id":"t1","state":"working","deliverable":"d"}`,
			"FriendQueue": `{"tasks":[{"gen":0,"job":"","id":"t1","state":"queued","deliverable":""},{"gen":2,"job":"j","id":"t2","state":"done","deliverable":"d"}]}`,
			"FriendWatch": `{"after":"1700000000000-0","wake_offset":4096}`,
		}
		require.Len(t, fixtures, len(all), "every table has a fixture")
		for name, text := range fixtures {
			t.Run(name, func(t *testing.T) { t.Parallel(); roundTrip(t, all[name], text) })
		}
	})

	t.Run("long text and 200 entry lists and maps", func(t *testing.T) {
		t.Parallel()
		refusals := make([]map[string]any, 200)
		tasks := make([]map[string]any, 200)
		cards := make([]map[string]any, 200)
		for i := range refusals {
			refusals[i] = map[string]any{"verb": fmt.Sprintf("verb-%03d", i), "count": i}
			tasks[i] = map[string]any{"gen": i, "job": fmt.Sprintf("job-%03d", i), "id": fmt.Sprintf("t%03d", i), "state": "queued", "deliverable": long(500)}
			cards[i] = map[string]any{"card": fmt.Sprintf("c%03d", i), "job": fmt.Sprintf("c%03d.w1~21.g1", i), "epoch": 21, "brief": long(2000)}
		}
		recovery := make([]any, 200)
		for i := range recovery {
			recovery[i] = message("aurora", "", long(300), 2)
		}
		fixtures := map[string]string{
			"NoticeMessage": j(message(strings.Repeat("f", 64), strings.Repeat("t", 128), long(100000), 200)),
			"NoticeEnvelope": j(map[string]any{
				"local_receiver": map[string]any{"identity": long(200), "redis": long(200), "proof": long(70000), "acknowledge": long(70000)},
				"saved_at":       base.Format(time.RFC3339Nano), "hook_epoch": 7, "hook_role": "friend", "event_id": "e", "messages": 200, "senders": names(200),
				"recovery_messages": recovery, "recovery_coverage": long(5000),
			}),
			"LaneEnd":      j(map[string]any{"Exit": 1, "Err": long(40000), "Tail": long(60000), "Cap": long(3000), "Rejected": long(3000)}),
			"Delivery":     j(map[string]any{"friend": "rowan", "cards": cards}),
			"FriendStatus": j(map[string]any{"friend": "rowan", "refused": refusals, "inbox_error": long(70000), "beat_error": long(20000), "window": long(1000), "last_refusal": long(1000)}),
			"FriendQueue":  j(map[string]any{"tasks": tasks}),
			"FriendWatch":  j(map[string]any{"after": long(5000), "wake_offset": int64(1) << 40}),
			"DeliveryCard": j(map[string]any{"card": long(500), "job": strings.Repeat("j", 200), "brief": long(300000)}),
		}
		for name, text := range fixtures {
			t.Run(name, func(t *testing.T) { t.Parallel(); roundTrip(t, all[name], text) })
		}
	})

	// The bounds the code enforces (the bound table is in the head of each schema): the
	// value at a bound is read in every form, the value one past is refused as clamped.
	t.Run("bounds", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			field, table string
			at, past     string
		}{
			{"NoticeName.s (bus.go:37 MaxName)", "NoticeName", `{"s":"` + strings.Repeat("n", 64) + `"}`, `{"s":"` + strings.Repeat("n", 65) + `"}`},
			{"NoticeMessage.from (bus.go:37 MaxName)", "NoticeMessage", j(message(strings.Repeat("f", 64), "", "", 1)), j(message(strings.Repeat("f", 65), "", "", 1))},
			{"NoticeMessage.token (token.go:32 MaxToken)", "NoticeMessage", j(message("a", strings.Repeat("t", 128), "", 1)), j(message("a", strings.Repeat("t", 129), "", 1))},
			{"NoticeMessage.body (bus.go:36 MaxBody)", "NoticeMessage", j(message("a", "", strings.Repeat("b", 1<<20), 1)), j(message("a", "", strings.Repeat("b", 1<<20+1), 1))},
			{"DeliveryCard.job (inbox.go validJob 200)", "DeliveryCard", `{"card":"c","job":"` + strings.Repeat("j", 200) + `","brief":"b"}`, `{"card":"c","job":"` + strings.Repeat("j", 201) + `","brief":"b"}`},
		} {
			tb := all[tc.table]
			t.Run(tc.field, func(t *testing.T) {
				t.Parallel()
				acceptJSON(t, tb, tc.at, tc.field)
				refuseJSON(t, tb, tc.past, tc.field)
			})
		}
		// the bounds inside a list: every name of to, cc and senders, and the challenge's own.
		overName := names(3)
		overName[2] = map[string]any{"s": strings.Repeat("n", 65)}
		okName := names(3)
		okName[2] = map[string]any{"s": strings.Repeat("n", 64)}
		to := func(v any) string { m := message("a", "", "", 1); m["To"] = v; return j(m) }
		cc := func(v any) string { m := message("a", "", "", 1); m["CC"] = v; return j(m) }
		refuseJSON(t, all["NoticeMessage"], to(overName), "to[2]")
		refuseJSON(t, all["NoticeMessage"], cc(overName), "cc[2]")
		roundTrip(t, all["NoticeMessage"], to(okName))
		roundTrip(t, all["NoticeMessage"], cc(okName))
		env := func(senders any, challenge map[string]any) string {
			return j(map[string]any{"local_receiver": map[string]any{}, "hook_epoch": 1, "senders": senders, "challenge": challenge})
		}
		refuseJSON(t, all["NoticeEnvelope"], env(overName, message("a", "", "", 1)), "senders[2]")
		refuseJSON(t, all["NoticeEnvelope"], env(okName, message(strings.Repeat("f", 65), "", "", 1)), "challenge.from")
		roundTrip(t, all["NoticeEnvelope"], env(okName, message(strings.Repeat("f", 64), "", "", 1)))
		// a delivery refuses a card whose job is past the bound, in the middle of a list.
		cards := []any{map[string]any{"card": "a", "job": "ok", "brief": "b"}, map[string]any{"card": "b", "job": strings.Repeat("j", 201), "brief": "b"}}
		refuseJSON(t, all["Delivery"], j(map[string]any{"friend": "rowan", "cards": cards}), "cards[1].job")
	})

	// A table that declares no bound for a record accepts what the code accepts: the
	// state, the lane end and the notice envelope hold no field bound (their schemas'
	// bound tables), so a value of any size reads.
	t.Run("no invented bounds", func(t *testing.T) {
		t.Parallel()
		huge := long(500000)
		for name, text := range map[string]string{
			"FriendWatch": j(map[string]any{"after": huge, "wake_offset": int64(-1)}),
			"FriendTask":  j(map[string]any{"gen": -5, "job": huge, "id": huge, "state": "anything", "deliverable": huge}),
			"LaneEnd":     j(map[string]any{"Exit": -1, "Tail": huge, "Err": huge, "Turns": 1 << 40}),
		} {
			t.Run(name, func(t *testing.T) { t.Parallel(); roundTrip(t, all[name], text) })
		}
	})

	// The tables read the JSON the Go structs they replace write today (S1: every field
	// the struct serializes is a field of the table with the same meaning).
	t.Run("sources", func(t *testing.T) {
		t.Parallel()
		msg := bus.Message{ModelActivity: true, ID: "01J0000000000000000000000A", From: "rowan", To: []string{"stella", "emma"}, CC: []string{"aurora"},
			Subject: "s", Re: "01J0000000000000000000000B", Card: "c", Head: strings.Repeat("a", 40), Kind: bus.KindAsk, At: base, Body: "body",
			Wait: 5 * time.Minute, Due: base.Add(5 * time.Minute), Token: "tok", Once: "o", Marks: []bus.Mark{{Key: "k", Field: "f", Value: "v", Forward: true}}}
		var env notice.Envelope
		env.LocalReceiver.Identity, env.LocalReceiver.Redis, env.LocalReceiver.Proof, env.LocalReceiver.Acknowledge = "stella", "r:1", "p", "a"
		env.SavedAt, env.HookEpoch, env.HookRole, env.EventID, env.Challenge, env.Messages = base, 9, hook.Friend, "e1", &msg, 4
		env.Senders, env.RecoverySince, env.RecoveryCoverage, env.Recovery, env.PossibleDuplicates = []string{"rowan", "emma"}, "since", "cov", []bus.Message{msg, {From: "emma", Kind: ""}}, true
		var bare notice.Envelope // no challenge, no recovery: nil slices and absent pointers
		bare.SavedAt = base
		paced := 2
		status := friend.Status{Friend: "rowan", Harness: "claude", Started: base, At: base, Seat: "rowan", Beats: 4, LastBeat: base, Width: 2, Session: friend.SessionOK,
			Paced: &paced, Window: "5h 1%", HeldKnown: true, Held: 2, InboxJobs: 3, Missing: 1, HeldFrom: friend.FromCards,
			Refused: map[string]int{friend.FriendCardsVerb: 2, friend.FriendBeatVerb: 1}, LastRefusal: "friend cards: x", LastRefusalAt: base, Proof: "ok", ProofAt: base}
		lane := friend.LaneEnd{Exit: 3, Err: "e", Wall: time.Minute, Cap: "10m", Rejected: "bash", Turns: 2, Restart: base, Started: base, NoReport: true,
			Capped: time.Hour, Tier: "pro", Overrun: time.Second, Tail: "a\nb"}
		held := friend.HeldAnswer{Friend: "rowan", Cards: []friend.HeldCard{{Card: "c", Job: "c.w1~21.g1", Col: "ready", Kind: "work", Branch: "b", Tier: "pro", Stream: "s",
			Attempt: 2, Gen: 1, Epoch: 21, Repo: "r", Base: "bb", Brief: "brief", Lane: 1, Why: "not serialized"}}}
		queue := friend.Queue{Tasks: []friend.Task{{Gen: 2, Job: "j", ID: "t", State: "working", Deliverable: "d"}, {ID: "u", State: ""}}}
		for _, tc := range []struct {
			table  string
			src    any
			back   func() any
			bridge bool
		}{
			{"NoticeEnvelope", env, func() any { return new(notice.Envelope) }, true},
			{"NoticeEnvelope", bare, func() any { return new(notice.Envelope) }, true},
			{"NoticeMessage", msg, func() any { return new(bus.Message) }, true},
			{"NoticeMessage", bus.Message{From: "rowan"}, func() any { return new(bus.Message) }, true},
			{"LaneEnd", lane, func() any { return new(friend.LaneEnd) }, false},
			{"LaneEnd", friend.LaneEnd{}, func() any { return new(friend.LaneEnd) }, false},
			{"Delivery", held, func() any { return new(friend.HeldAnswer) }, true},
			{"Delivery", friend.HeldAnswer{Friend: "rowan"}, func() any { return new(friend.HeldAnswer) }, true},
			{"FriendStatus", status, func() any { return new(friend.Status) }, true},
			{"FriendStatus", friend.Status{}, func() any { return new(friend.Status) }, true},
			{"FriendQueue", queue, func() any { return new(friend.Queue) }, true},
			{"FriendQueue", friend.Queue{}, func() any { return new(friend.Queue) }, true},
			{"FriendWatch", friend.Watch{After: "1700000000000-0", WakeOffset: 4096}, func() any { return new(friend.Watch) }, false},
		} {
			tb := all[tc.table]
			t.Run(tc.table, func(t *testing.T) {
				t.Parallel()
				text, err := json.Marshal(tc.src)
				require.NoError(t, err)
				if tc.bridge {
					text = reshape(text, toTableShape)
				}
				var v any
				rep := tb.codec.LoadJSON(&v, text)
				require.False(t, rep.Malformed, "%s reads the struct's JSON: %s", tc.table, text)
				require.Zero(t, rep.Clamped, "%s", tc.table)
				require.Zero(t, rep.Unknown, "%s names every field the struct writes: %s", tc.table, text)
				require.Zero(t, rep.KindMismatch, "%s reads every list of the struct as a list: %s", tc.table, text)
				// the strict reader takes the same text in every form, with no kind mismatch
				strict := schemaread.NewReader(tb.codec)
				_, result, err := strict.Read(text)
				require.NoError(t, err, "%s is read strictly: %s", tc.table, text)
				require.Equal(t, schemaread.FormJSON, result.Form)
				// through the wire, and back to the Go struct
				var viaWire any
				wireRep := tb.codec.LoadWire(&viaWire, tb.wire(v))
				require.False(t, wireRep.Malformed)
				out := tb.json(viaWire)
				if tc.bridge {
					out = reshape(out, fromTableShape)
				}
				back := tc.back()
				require.NoError(t, json.Unmarshal(out, back), "%s", out)
				again, err := json.Marshal(back)
				require.NoError(t, err)
				want, err := json.Marshal(tc.src)
				require.NoError(t, err)
				// a nil list is null in the struct's JSON and empty in the table: the one difference, said here.
				assert.JSONEq(t, string(reshape(want, nullListsAsEmpty)), string(again), "%s survives the table and returns to the struct", tc.table)
			})
		}
	})

	// A nil list is JSON null in the struct's JSON (To, CC and Marks of a message, the tasks
	// of a queue, the cards of a delivery, the lists of a notice), and a table's list is
	// never null: the bridge writes it as an empty list, and the strict reader takes the
	// bridged text with no kind mismatch. The raw null is refused by name, so the bridge
	// is what the check pins.
	t.Run("nil lists", func(t *testing.T) {
		t.Parallel()
		var bare notice.Envelope
		bare.SavedAt = base
		var noMarks bus.Message
		noMarks.From = "rowan"
		for _, tc := range []struct {
			table string
			src   any
			list  string
		}{
			{"NoticeEnvelope", bare, ""},
			{"NoticeMessage", noMarks, `"To":null`},
			{"FriendQueue", friend.Queue{}, `"tasks":null`},
			{"Delivery", friend.HeldAnswer{Friend: "rowan"}, `"cards":null`},
		} {
			tb := all[tc.table]
			t.Run(tc.table, func(t *testing.T) {
				t.Parallel()
				raw, err := json.Marshal(tc.src)
				require.NoError(t, err)
				if tc.list != "" {
					require.Contains(t, string(raw), tc.list, "the struct writes a nil list as null")
					var v any
					rep := tb.codec.LoadJSON(&v, raw)
					require.NotZero(t, rep.KindMismatch, "a null list is a kind mismatch")
					_, _, err := schemaread.NewReader(tb.codec).Read(raw)
					var refusal *schemaread.Refusal
					require.ErrorAs(t, err, &refusal)
					require.Equal(t, schemaread.ReasonKindMismatch, refusal.Reason)
				}
				bridged := reshape(raw, toTableShape)
				var v any
				rep := tb.codec.LoadJSON(&v, bridged)
				require.False(t, rep.Malformed)
				require.Zero(t, rep.KindMismatch, "%s: %s", tc.table, bridged)
				_, _, err = schemaread.NewReader(tb.codec).Read(bridged)
				require.NoError(t, err, "%s: %s", tc.table, bridged)
			})
		}
	})
}
