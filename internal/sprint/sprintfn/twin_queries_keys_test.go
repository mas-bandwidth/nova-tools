package sprintfn

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func (w *qworld) key(q KeyQ) (QueryResult, QueryCharge) {
	w.t.Helper()
	res, c, err := w.tw.KeyQuery(q)
	if err != nil {
		w.t.Fatalf("%s: %v", q.Kind, err)
	}
	return res, c
}

func str(s string) *string { return &s }

// TestSprintKeyReads: the bounded sprint-key reads of the errata's addendum:
// the clock and R (1.2), the lease and {p}tick@e (1.1), the heartbeat (1.4.1),
// the dropping marks and parked keys of the names given, the scores in
// {p}missing@e, the jopen of subjects by name, and the due count at R.
func TestSprintKeyReads(t *testing.T) {
	t.Parallel()
	w := standard(t)
	wall := testTime.UnixMilli()

	t.Run("the clock and R while RUNNING", func(t *testing.T) {
		res, c := w.key(KeyQ{Kind: KeyClock})
		r := res.(ClockResult)
		// R = t - stopped_ms: 1,000 ms of stopped time before this span.
		if r.WallMS != strconv.FormatInt(wall, 10) || r.R != strconv.FormatInt(wall-1000, 10) ||
			r.Clock.StoppedMS == nil || *r.Clock.StoppedMS != "1000" || r.Clock.StoppedSinceMS == nil || *r.Clock.StoppedSinceMS != "" {
			t.Fatalf("%+v", r)
		}
		// One HMGET of the clock's five fields: a probe for each.
		if c.Probes != 5 || c.Records != 0 {
			t.Fatalf("charged %+v", c)
		}
	})
	t.Run("the clock and R while STOPPED: R does not move", func(t *testing.T) {
		since := wall - 500
		w.seed(w.hsetBare("clock", "stopped_since_ms", strconv.FormatInt(since, 10)))
		res, _ := w.key(KeyQ{Kind: KeyClock})
		// R = t - 1000 - (t - since) = since - 1000, whatever t is.
		if r := res.(ClockResult); r.R != strconv.FormatInt(since-1000, 10) {
			t.Fatalf("%+v", r)
		}
		w.seed(w.hsetBare("clock", "stopped_since_ms", ""))
	})
	t.Run("a clock that is not whole numbers is DRIFT", func(t *testing.T) {
		w.seed(w.hsetBare("clock", "stophold_ms", "soon"))
		_, _, err := w.tw.KeyQuery(KeyQ{Kind: KeyClock})
		if ref, ok := err.(*Refusal); !ok || ref.Code != codeDrift {
			t.Fatalf("%v", err)
		}
		w.seed(w.hsetBare("clock", "stophold_ms", "0"))
	})
	t.Run("the lease", func(t *testing.T) {
		res, _ := w.key(KeyQ{Kind: KeyLease})
		r := res.(LeaseResult)
		if *r.Owner != "tok" || *r.Name != "loop" || *r.Gen != "4" || *r.UntilMS != "9999999999999" || r.NowMS != strconv.FormatInt(wall, 10) {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("the tick hash", func(t *testing.T) {
		res, _ := w.key(KeyQ{Kind: KeyTick})
		if r := res.(TickResult); *r.Cur != "77" || *r.BehindN != "0" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("the heartbeat, its set fields", func(t *testing.T) {
		res, c := w.key(KeyQ{Kind: KeyHeartbeat})
		if r := res.(HeartbeatResult); !reflect.DeepEqual(r.Fields, map[string]string{"tick_at": "5", "ticks": "9"}) || c.Probes != len(HeartbeatFields) {
			t.Fatalf("%+v %+v", r, c)
		}
	})
	t.Run("dropping marks by stream", func(t *testing.T) {
		res, c := w.key(KeyQ{Kind: KeyDropping, Streams: []string{"s1", "s2"}})
		r := res.(DroppingResult)
		if r.Count != 1 || !reflect.DeepEqual(r.Marks, map[string]string{"s2": "op-drop"}) || c.Probes != 1+2 { // the count, and a probe for each stream named
			t.Fatalf("%+v %+v", r, c)
		}
		// No stream named: the count alone.
		res, c = w.key(KeyQ{Kind: KeyDropping, Streams: []string{}})
		if r := res.(DroppingResult); r.Count != 1 || len(r.Marks) != 0 || c.Probes != 1 {
			t.Fatalf("%+v %+v", r, c)
		}
	})
	t.Run("parked notes by key", func(t *testing.T) {
		res, _ := w.key(KeyQ{Kind: KeyParked, Keys: []string{"agenda-key-1", "agenda-key-2"}})
		if r := res.(ParkedResult); r.Count != 1 || !reflect.DeepEqual(r.Notes, map[string]string{"agenda-key-1": "note-9"}) {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("scores in missing", func(t *testing.T) {
		res, c := w.key(KeyQ{Kind: KeyMissing, IDs: []string{"ghost", "p1"}})
		r := res.(MissingResult)
		if len(r.Scores) != 2 || r.Scores[0] == nil || *r.Scores[0] != "100" || r.Scores[1] != nil || c.Probes != 2 { // a probe for each id
			t.Fatalf("%+v %+v", r, c)
		}
	})
	t.Run("jopen by name", func(t *testing.T) {
		w.note("blocked", "c1", "p1")
		id := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))
		res, _ := w.key(KeyQ{Kind: KeyJOpen, Subjects: []string{"p1", "p2"}, Names: []string{"blocked|c1", "other|x"}})
		r := res.(JOpenResult)
		if r.Items[0].Count != 1 || r.Items[0].Fields["blocked|c1"] == nil || *r.Items[0].Fields["blocked|c1"] != id ||
			r.Items[0].Fields["other|x"] != nil || r.Items[1].Count != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("every read makes no more probes than it declares", func(t *testing.T) {
		for _, q := range []KeyQ{{Kind: KeyClock}, {Kind: KeyLease}, {Kind: KeyTick}, {Kind: KeyHeartbeat}, {Kind: KeyDueCount},
			{Kind: KeyDropping, Streams: []string{"s1", "s2"}}, {Kind: KeyDropping, Streams: []string{}}, {Kind: KeyParked, Keys: []string{"a"}},
			{Kind: KeyMissing, IDs: []string{"a", "b"}}, {Kind: KeyMissing, IDs: []string{}},
			{Kind: KeyJOpen, Subjects: []string{"p1", "p2"}, Names: []string{"x|y"}}, {Kind: KeyJOpen, Subjects: []string{"p1"}, Names: []string{}}} {
			_, c := w.key(q)
			if c.Probes > KeyProbes(q) || c.Records != 0 || c.RangeIDs != 0 {
				t.Errorf("%s: charged %+v, declared %d probes", q.Kind, c, KeyProbes(q))
			}
		}
	})
	t.Run("the due count at R", func(t *testing.T) {
		// The due set holds unfinished:k1.w1 at 9000, untaken:a1.w2 at 8000,
		// mergeidle:s1 at 7000, unbegun at 6000 and unreported at 6500; R is now less
		// 1,000 ms, about 1.79 trillion, so all five are due. Seed one far ahead.
		w.seed(w.zadd("due", strconv.FormatInt(wall, 10), "later:x"))
		res, _ := w.key(KeyQ{Kind: KeyDueCount})
		r := res.(DueCountResult)
		if r.R != strconv.FormatInt(wall-1000, 10) || r.Due != 5 {
			t.Fatalf("%+v", r)
		}
	})
}

// TestSprintKeyReadsRefuseWrongType: a key that holds another type than the
// read's is WRONGTYPE, as Layer 1's typed read refuses it, and never a wrong
// answer.
func TestSprintKeyReadsRefuseWrongType(t *testing.T) {
	t.Parallel()
	w := newQWorld(t)
	w.seed(Command("ZADD", w.k("tick"), kindZSet, "1", "x"))
	_, _, err := w.tw.KeyQuery(KeyQ{Kind: KeyTick})
	if ref, ok := err.(*Refusal); !ok || ref.Code != "WRONGTYPE" {
		t.Fatalf("%v", err)
	}
}

// TestQueryWire: a query encodes to the object the design's E6 fixes (its kind
// and an explicit fields array), IT12's own static check accepts it, it reads
// back as the query that was written, and an object with an unknown key, a
// kind that does not match, or a fields that is not an array is refused.
func TestQueryWire(t *testing.T) {
	t.Parallel()
	qs := []sprint.SprintQ{
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("a", "b"), Fields: []string{"attempt"}, Follow: []string{sprint.FollowWork, sprint.FollowNeeds}},
		{Kind: sprint.QueryRelated, Table: sprint.Fleet, Source: head("m1:ready", 7), Fields: []string{}, Follow: []string{}},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: 48213, Offset: 2000 - 10, About: true, Limit: 5}, Fields: []string{}, Follow: []string{}},
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt"}, Heads: []sprint.HeadQ{
			{Index: sprint.HeadEligBelow, Limit: 3, Follow: []string{}}, {Index: sprint.HeadAgain, Limit: 2, Follow: []string{sprint.FollowWithdrawn}}}},
		{Kind: sprint.QueryWaiters, Source: head("missing", 5), Limit: 10, Fields: []string{"open"}},
		{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 4, Units: 10},
		{Kind: sprint.QueryFleet, Fields: []string{"status"}},
		{Kind: sprint.QueryReaders, Fields: []string{}, Units: 7},
		{Kind: sprint.QueryNeedchain, Source: ids("w1"), Limit: 50, Fields: []string{}},
		{Kind: sprint.QueryJnote, Source: ids("n7", "n9"), Fields: []string{}, Subjects: 20},
		{Kind: sprint.QueryJnote, Source: head("jnotes", 5), Fields: []string{}, Subjects: 20},
	}
	for _, q := range qs {
		enc, ref := EncodeSprintQ(q)
		if ref != nil {
			t.Fatalf("%s: %v", q.Kind, ref)
		}
		if r := checkSprintQuery(enc); r != nil {
			t.Fatalf("%s: IT12's static check refuses %s: %v", q.Kind, enc.Query, r)
		}
		back, ref := DecodeSprintQ(enc)
		if ref != nil {
			t.Fatalf("%s: %v", q.Kind, ref)
		}
		if !reflect.DeepEqual(back, q) {
			t.Errorf("%s: read back as\n%+v\nwant\n%+v", q.Kind, back, q)
		}
	}

	kq := []KeyQ{{Kind: KeyClock}, {Kind: KeyLease}, {Kind: KeyTick}, {Kind: KeyHeartbeat}, {Kind: KeyDueCount},
		{Kind: KeyDropping, Streams: []string{"s1"}}, {Kind: KeyParked, Keys: []string{"deal", "resolve:s1"}},
		{Kind: KeyMissing, IDs: []string{"a"}}, {Kind: KeyJOpen, Subjects: []string{"a"}, Names: []string{"t|c"}}}
	for _, q := range kq {
		enc, ref := EncodeKeyQ(q)
		if ref != nil {
			t.Fatalf("%s: %v", q.Kind, ref)
		}
		if r := checkSprintQuery(enc); r != nil {
			t.Fatalf("%s: IT12's static check refuses %s: %v", q.Kind, enc.Query, r)
		}
		back, ref := DecodeKeyQ(enc)
		if ref != nil || !reflect.DeepEqual(back, q) {
			t.Errorf("%s: read back as %+v (%v), want %+v", q.Kind, back, ref, q)
		}
	}

	bad := []struct{ name, kind, raw string }{
		{"an unknown key", "fleet", `{"kind":"fleet","fields":[],"extra":1}`},
		{"a kind that does not match", "fleet", `{"kind":"readers","fields":[]}`},
		{"no fields", "fleet", `{"kind":"fleet"}`},
		{"fields that is not an array", "fleet", `{"kind":"fleet","fields":"a"}`},
		{"fields that is null", "fleet", `{"kind":"fleet","fields":null}`},
		{"units that is not a number", "fleet", `{"kind":"fleet","fields":[],"units":"3"}`},
		{"a source of no kind", "waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{}}`},
		{"a line's seq as a number", "waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":5}}`},
		{"a line's seq not canonical", "waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"05"}}`},
		{"a head with a list of ids", "waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"missing","limit":1,"ids":[]}}`},
		{"a follow that is not one", "related", `{"kind":"related","t":"work","fields":[],"follow":["nothing"],"src":{"kind":"ids","ids":[]}}`},
		{"a follow twice", "related", `{"kind":"related","t":"work","fields":[],"follow":["work","work"],"src":{"kind":"ids","ids":[]}}`},
		{"a field Layer 1 reserves", "fleet", `{"kind":"fleet","fields":["revision"]}`},
		{"a field twice", "fleet", `{"kind":"fleet","fields":["a","a"]}`},
		{"an id with a blank", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":["a b"]}}`},
		{"a note id that is not one", "jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["x7"]}}`},
		{"a head of jnote that is not jnotes", "jnote", `{"kind":"jnote","fields":[],"src":{"kind":"head","key":"missing","limit":1}}`},
		{"a line as jnote's source", "jnote", `{"kind":"jnote","fields":[],"src":{"kind":"line","seq":"5"}}`},
		{"a front head that is not one", "front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"nope","limit":1,"follow":[]}]}`},
		{"a front head twice", "front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"again","limit":1,"follow":[]},{"index":"again","limit":2,"follow":[]}]}`},
		{"a head with no limit", "waiters", `{"kind":"waiters","limit":0,"fields":[],"src":{"kind":"ids","ids":[]}}`},
		{"streams over the most", "streams", `{"kind":"streams","fields":[],"limit":0,"units":251}`},
	}
	for _, c := range bad {
		_, ref := DecodeSprintQ(SprintQuery{Kind: c.kind, Query: json.RawMessage(c.raw)})
		if ref == nil || ref.Code != CodeRequest {
			t.Errorf("%s: %v, want REQUEST", c.name, ref)
		}
	}
	keyBad := []struct{ name, kind, raw string }{
		{"a key read with a field", KeyTick, `{"kind":"tick","fields":["a"]}`},
		{"dropping with no streams array", KeyDropping, `{"kind":"dropping","fields":[]}`},
		{"jopen with no names", KeyJOpen, `{"kind":"jopen","fields":[],"subjects":["a"]}`},
		{"a stream twice", KeyDropping, `{"kind":"dropping","fields":[],"streams":["a","a"]}`},
		{"names of another kind", KeyTick, `{"kind":"tick","fields":[],"streams":[]}`},
	}
	for _, c := range keyBad {
		_, ref := DecodeKeyQ(SprintQuery{Kind: c.kind, Query: json.RawMessage(c.raw)})
		if ref == nil || ref.Code != CodeRequest {
			t.Errorf("%s: %v, want REQUEST", c.name, ref)
		}
	}
	// The encoders refuse what the decoders would: E6 has no whole-record path.
	if _, ref := EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryFleet}); ref == nil || ref.Code != CodeRequest {
		t.Errorf("a nil projection: %v, want REQUEST", ref)
	}
	if _, ref := EncodeKeyQ(KeyQ{Kind: KeyClock, Keys: []string{"x"}}); ref == nil {
		t.Error("a clock read naming keys was encoded")
	}
}

// TestQueryDeclaredLimits: a query whose declared cost cannot fit a read of
// Layer 1's bounds is LIMIT before any store is touched (1.4.2: a read is
// sized before it is sent, never refused BUDGET), and a source past its bound
// is REQUEST.
func TestQueryDeclaredLimits(t *testing.T) {
	t.Parallel()
	limit := func(q sprint.SprintQ) {
		t.Helper()
		if ref := ValidateSprintQ(q); ref == nil || ref.Code != CodeLimit {
			t.Errorf("%s: %v, want LIMIT", q.Kind, ref)
		}
	}
	many := make([]string, 0, 5001)
	for i := 0; i < 5001; i++ {
		many = append(many, "c"+strconv.Itoa(i))
	}
	limit(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(many...), Fields: []string{}, Follow: []string{sprint.FollowWork}})
	limit(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(many[:200]...), Fields: []string{}, Follow: []string{sprint.FollowNeeds}})
	limit(sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids(many[:100]...), Limit: 100, Fields: []string{}})
	limit(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 100})
	limit(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids("n1", "n2", "n3"), Fields: []string{}})
	limit(sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s", Fields: []string{}, Heads: []sprint.HeadQ{
		{Index: sprint.HeadEligBelow, Limit: 1000, Follow: []string{sprint.FollowRCards}}}})
	// A cost that does fit.
	ok := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(many[:5000]...), Fields: []string{}}
	if ref := ValidateSprintQ(ok); ref != nil {
		t.Errorf("5,000 records: %v", ref)
	}
	// IT30's largest case is bounded at 10,000 records: a list of that many ids,
	// with no follow, fits, and one more does not.
	all := make([]string, 0, queryMaxRecords+1)
	for i := 0; i <= queryMaxRecords; i++ {
		all = append(all, "c"+strconv.Itoa(i))
	}
	ok.Source = ids(all[:queryMaxRecords]...)
	if ref := ValidateSprintQ(ok); ref != nil {
		t.Errorf("10,000 records: %v", ref)
	}
	ok.Source = ids(all...)
	limit(ok)
	if c := sprint.QueryCost(sprint.SprintQ{Kind: sprint.QueryNeedchain, Limit: queryMaxRecords, Source: ids("a")}); c.Records != queryMaxRecords {
		t.Errorf("needchain at its bound declares %d records", c.Records)
	}
}

// TestQueriesAreReadOnly: no query writes: the Mem's state, the sprint's keys
// and the log are byte-equal after every kind of query, refused or answered.
func TestQueriesAreReadOnly(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.note("blocked", "c1", "p1")
	before := string(image(t, w.tw, w.m, w.log))
	for _, q := range []sprint.SprintQ{
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1", "w1", "v1"), Fields: []string{"open"}, Follow: sprint.Follows},
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadAgain, Limit: 3}}},
		{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 3, Fields: []string{}},
		{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 2},
		{Kind: sprint.QueryFleet, Fields: []string{}},
		{Kind: sprint.QueryReaders, Fields: []string{}},
		{Kind: sprint.QueryNeedchain, Source: ids("w1"), Limit: 5, Fields: []string{}},
		{Kind: sprint.QueryJnote, Source: head("jnotes", 3), Fields: []string{}, Subjects: 3},
		{Kind: sprint.QueryRelated, Table: "nosuch", Source: ids("p1"), Fields: []string{}},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:nosuch", 3), Fields: []string{}},
	} {
		w.tw.Query(q)
	}
	for _, q := range []KeyQ{{Kind: KeyClock}, {Kind: KeyLease}, {Kind: KeyDueCount}, {Kind: KeyMissing, IDs: []string{"a"}}} {
		w.tw.KeyQuery(q)
	}
	if after := string(image(t, w.tw, w.m, w.log)); after != before {
		t.Fatal("a query changed the twin")
	}
}

// TestQueryKeysUnderEpoch: the per-epoch keys of the sprint carry the read's
// epoch, written at every epoch, @0 included (1.0, "Keys").
func TestQueryKeysUnderEpoch(t *testing.T) {
	t.Parallel()
	w := newQWorld(t)
	e := w.tw.newEval("3", "1")
	if got := e.key("quarantine"); got != testPrefix+"sprint:quarantine@3" {
		t.Fatalf("%s", got)
	}
	if got := w.tw.newEval("0", "1").key("jopen:p1"); got != testPrefix+"sprint:jopen:p1@0" {
		t.Fatalf("%s", got)
	}
	if got := e.bare("lease"); got != testPrefix+"sprint:lease" {
		t.Fatalf("%s", got)
	}
	if !strings.HasPrefix(e.sk, testPrefix) {
		t.Fatal("the sprint's keys are under the prefix")
	}
}

// TestQueryProbesDeclared: the probes a query declares (QueryProbes) cover the
// largest answers: a card with every follow at its bound makes one probe for
// each need's wait:n, and the declared count is not below them.
func TestQueryProbesDeclared(t *testing.T) {
	t.Parallel()
	q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("a"), Fields: []string{}, Follow: sprint.Follows}
	if got := QueryProbes(q); got < followMaxNeeds+1+1+2 {
		t.Fatalf("one card with every follow declares %d probes", got)
	}
	if got := QueryProbes(sprint.SprintQ{Kind: "nosuch"}); got != queryMaxProbes {
		t.Fatalf("a kind that is not a query declares %d", got)
	}
}
