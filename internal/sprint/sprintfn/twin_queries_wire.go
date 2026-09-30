package sprintfn

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The wire of the sprint's queries (upper design 1.0, "Composite queries";
// errata 1 E6 and the addendum; item IT30), and the checks a query passes
// before any store is touched. The design fixes each query's meaning and cost
// and no bytes, so this is the narrower reading, listed as open questions in
// the pull request. A query is one JSON object whose "kind" is its kind and
// whose "fields" is an explicit array (E6: empty means the summary, never an
// implicit whole record):
//
//	related   {"kind","t","src","follow","fields"}
//	front     {"kind","stream","heads":[{"index","limit","follow"}],"fields","keys"?}
//	waiters   {"kind","src","limit","fields","after"?,"missing"?,"keys"?}
//	streams   {"kind","limit","units","fields","counts"?,"keys"?}
//	fleet     {"kind","units","fields"}          readers the same
//	needchain {"kind","src","limit","fields"}
//	jnote     {"kind","src","subjects","fields"}
//
// An id source "src" is {"kind":"ids","ids":[...]}, {"kind":"head","key","limit"}
// or {"kind":"line","seq","about","offset","limit"}; a line's seq is an exact
// decimal string, like every seq and time on the wire. The keys marked "?" are
// IT08's extensions of 1.0's queries and are written only when set: "after" is
// the cursor of a waiters query of one id (sprint.SprintQ.WaiterAfter),
// "missing" reads the head of wait:n only for the ids in {p}missing@e,
// "counts" are the work table's columns a streams query counts for every
// stream, and "keys" the sprint keys a query also reads (queryKeys). Lua's validate(q,
// index) checks the same shapes, and a test holds the two equal once a store
// runs the Lua (TestTwinEqualsLuaQueries).

// The bounded sprint-key reads the errata's addendum lists for RT1, by the
// kinds they are registered under with NS.SP.query. The design names the
// reads and no kinds; these names are this item's choice.
const (
	KeyClock     = "clock"     // {p}clock and R
	KeyLease     = "lease"     // {p}lease
	KeyTick      = "tick"      // {p}tick@e: cur, behind_n
	KeyHeartbeat = "heartbeat" // {p}heartbeat, by its fixed fields
	KeyDropping  = "dropping"  // {p}dropping@e marks of the streams named
	KeyParked    = "parked"    // {p}parked@e notes of the keys named
	KeyMissing   = "missing"   // scores in {p}missing@e of the ids named
	KeyJOpen     = "jopen"     // {p}jopen:<subject>@e of the subjects named
	KeyDueCount  = "duecount"  // the due entries at or below R
)

// CompositeKinds are the eight composite queries of 1.0, in its order.
var CompositeKinds = []string{sprint.QueryRelated, sprint.QueryFront, sprint.QueryWaiters, sprint.QueryStreams,
	sprint.QueryFleet, sprint.QueryReaders, sprint.QueryNeedchain, sprint.QueryJnote}

// SprintKeyKinds are the bounded sprint-key read kinds, in the order above.
var SprintKeyKinds = []string{KeyClock, KeyLease, KeyTick, KeyHeartbeat, KeyDropping, KeyParked, KeyMissing,
	KeyJOpen, KeyDueCount}

// Bounds of a query, each the design's own.
const (
	// queryMaxFields is the fields a projection may name: a record read names
	// at most 128 (L1 6), and a read also asks the fields its follows derive from
	// (attempt, rcards, needs, member) and `streams` the four of a control card
	// (state, cause, other, need_card), which leaves this many to the query.
	queryMaxFields = tset.MaxFieldsPerMember - 4
	// queryMaxName is the bytes of an id, a row, a column or a field name (L1 6).
	queryMaxName = tset.MaxIdentifierBytes
	// probeChunk is the ids one HMGET or ZMSCORE names: a command carries at most
	// 2,002 argv values, its name and key included (L1 1.4).
	probeChunk = 2000
	// followMaxNeeds is the needs a card names (1.0, `needs`: 64 a card, refused
	// at add).
	followMaxNeeds = 64
	// followMaxRCards is the read cards a primary keeps in `rcards` (1.3.1).
	followMaxRCards = 15
	// keyQueryMaxNames is the names a sprint-key read probes in one command:
	// one HMGET or ZMSCORE (L1 1.4).
	keyQueryMaxNames = probeChunk
)

// The store's checked reads refuse a member past these bounds (L1 6, 7); a
// composite query is planned inside them before it is sent (1.4.2).
const (
	queryMaxRecords  = sprint.MaxReadRecords  // records a read requests or returns
	queryMaxRangeIDs = sprint.MaxReadRangeIDs // range ids a read returns
	queryMaxHead     = sprint.MaxRangeLimit   // ids one range head returns
)

// The heads of a sprint's indexes a source names, as their first word
// (1.3.1): a key is one of these with its argument after a colon, or one of
// the bare keys, and any other key a source names is a cell "<row>:<col>".
var indexHeadPrefixes = map[string]bool{sprint.IndexSent: true, sprint.IndexElig: true, sprint.IndexFresh: true,
	sprint.IndexAgain: true, sprint.IndexWait: true}

// The bare index keys of 1.3.1 that an id source may name.
const (
	indexAskWait = "askwait"
	indexMissing = "missing"
	indexJNotes  = "jnotes"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.~-]*$`)

// validName says an id, a stream, a member, a table or a field name is one
// word of the sprint's alphabet: letters, digits and _ . ~ -, at most 256
// bytes. It is narrower than Layer 1's (any UTF-8), since the sprint makes
// every one of its own names.
func validName(s string) bool { return len(s) <= queryMaxName && nameRE.MatchString(s) }

// validFieldName is a projection's field name: a name Layer 1 does not reserve
// (epoch, revision and place:<table> are not application fields).
func validFieldName(s string) bool {
	return validName(s) && s != "epoch" && s != "revision" && !strings.HasPrefix(s, "place:")
}

// headKind splits a source key into an index of 1.3.1 and its argument, and
// says whether it is one. A key that is not an index is a cell.
func headKind(key string) (index, arg string, isIndex bool) {
	switch key {
	case indexAskWait, indexMissing, indexJNotes:
		return key, "", true
	}
	i := strings.IndexByte(key, ':')
	if i > 0 && indexHeadPrefixes[key[:i]] && validName(key[i+1:]) {
		return key[:i], key[i+1:], true
	}
	return "", "", false
}

// cellOf splits "<row>:<col>".
func cellOf(cell string) (row, col string, ok bool) {
	i := strings.IndexByte(cell, ':')
	if i <= 0 || !validName(cell[:i]) || !validName(cell[i+1:]) {
		return "", "", false
	}
	return cell[:i], cell[i+1:], true
}

// validHeadKey says a head names an index of 1.3.1 with its argument, or a
// cell.
func validHeadKey(key string) bool {
	if _, _, ok := headKind(key); ok {
		return true
	}
	_, _, ok := cellOf(key)
	return ok
}

func queryRequestRefusal() *Refusal { return refuse(PhaseOpen, CodeRequest, RefusalDetail{}) }

func limitRefusal(budget string) *Refusal {
	return refuse(PhaseOpen, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: budget}})
}

func distinctNames(list []string, valid func(string) bool) bool {
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		if !valid(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

// validFollow says every follow is one of 1.0's and none repeats.
func validFollow(follow []string) bool {
	known := map[string]bool{}
	for _, f := range sprint.Follows {
		known[f] = true
	}
	seen := map[string]bool{}
	for _, f := range follow {
		if !known[f] || seen[f] {
			return false
		}
		seen[f] = true
	}
	return true
}

// validSource checks the shape of an id source against the source kinds a query
// takes. How many ids a list names is a size, not a shape: ValidateSprintQ
// checks it after every shape (REQUEST before LIMIT).
func validSource(s sprint.IDSource, kinds ...string) bool {
	ok := false
	for _, k := range kinds {
		ok = ok || k == s.Kind
	}
	if !ok {
		return false
	}
	switch s.Kind {
	case sprint.SourceIDs:
		return distinctNames(s.IDs, validName)
	case sprint.SourceHead:
		return validHeadKey(s.Key) && s.Limit >= 1 && s.Limit <= queryMaxHead
	case sprint.SourceLine:
		most := sprint.MaxLineIDs
		if s.About {
			most = sprint.MaxAboutIDs
		}
		return s.Seq >= 1 && s.Seq <= maxSeq && s.Offset >= 0 && s.Offset < most && s.Limit >= 0
	}
	return false
}

// queryKeys are the sprint keys a composite query may also read, by the kinds
// that may name them (sprint.SprintQ.Keys; rules_position_read.go): the
// dropping marks of the streams the query reaches, {p}next@e.streams, and the
// jopen keys, each read as the one field its rule tests (jopenFields): jopen:G
// only by `front`, whose first sentinel it is about.
var queryKeys = map[string]bool{sprint.KeyDropping: true, sprint.KeyNextStreams: true, sprint.KeyJOpenG: true, sprint.KeyJOpenSprint: true}

// keysKinds are the composite kinds whose answer reaches streams, and so may
// name sprint keys.
var keysKinds = map[string]bool{sprint.QueryFront: true, sprint.QueryWaiters: true, sprint.QueryStreams: true}

// validExtensions checks IT08's extensions of a query (the "?" keys above):
// Keys only of a kind that reaches streams, each a key of queryKeys named once;
// Counts only of `streams`, distinct column names, at most a table's columns;
// WaiterAfter and Missing only of `waiters`, and a cursor only of a list of one
// id, the cursor itself an id.
func validExtensions(q sprint.SprintQ) bool {
	if len(q.Keys) > 0 {
		if !keysKinds[q.Kind] || !distinctNames(q.Keys, func(k string) bool { return queryKeys[k] }) {
			return false
		}
		if q.Kind != sprint.QueryFront && slices.Contains(q.Keys, sprint.KeyJOpenG) {
			return false
		}
	}
	if len(q.Counts) > 0 && (q.Kind != sprint.QueryStreams || len(q.Counts) > maxTableColumns || !distinctNames(q.Counts, validName)) {
		return false
	}
	if q.Missing && q.Kind != sprint.QueryWaiters {
		return false
	}
	if q.WaiterAfter != "" && (q.Kind != sprint.QueryWaiters || q.Source.Kind != sprint.SourceIDs || len(q.Source.IDs) != 1 || !validName(q.WaiterAfter)) {
		return false
	}
	return true
}

// ValidateSprintQ checks a composite query's shape and its declared cost
// against Layer 1's read bounds, before any store is touched: a REQUEST for a
// malformed query (E6: no implicit whole-record projection, so a nil Fields
// is one), a LIMIT for one whose declared cost (sprint.QueryCost) cannot fit
// a read. The shape is checked whole before any size: a malformed query that
// is also too large is REQUEST, in this function and in the Lua kinds' validate
// alike. It is the Go of the Lua kinds' validate.
func ValidateSprintQ(q sprint.SprintQ) *Refusal {
	if q.Fields == nil || len(q.Fields) > queryMaxFields || !distinctNames(q.Fields, validFieldName) {
		return queryRequestRefusal()
	}
	switch q.Kind {
	case sprint.QueryRelated:
		if !validName(q.Table) || !validSource(q.Source, sprint.SourceIDs, sprint.SourceHead, sprint.SourceLine) ||
			!validFollow(q.Follow) {
			return queryRequestRefusal()
		}
	case sprint.QueryFront:
		if !validName(q.Stream) || len(q.Heads) > 4 {
			return queryRequestRefusal()
		}
		seen := map[string]bool{}
		for _, h := range q.Heads {
			switch h.Index {
			case sprint.HeadEligBelow, sprint.HeadFreshBelow, sprint.HeadFreshAbove, sprint.HeadAgain:
			default:
				return queryRequestRefusal()
			}
			if seen[h.Index] || h.Limit < 1 || h.Limit > queryMaxHead || !validFollow(h.Follow) {
				return queryRequestRefusal()
			}
			seen[h.Index] = true
		}
	case sprint.QueryWaiters:
		if !validSource(q.Source, sprint.SourceIDs, sprint.SourceHead, sprint.SourceLine) || q.Limit < 1 || q.Limit > queryMaxHead {
			return queryRequestRefusal()
		}
	case sprint.QueryStreams:
		if q.Limit < 0 || q.Limit > queryMaxHead || q.Units < 0 || q.Units > sprint.MaxStreams {
			return queryRequestRefusal()
		}
	case sprint.QueryFleet:
		if q.Units < 0 || q.Units > sprint.MaxMembers {
			return queryRequestRefusal()
		}
	case sprint.QueryReaders:
		if q.Units < 0 || q.Units > sprint.MaxReaders {
			return queryRequestRefusal()
		}
	case sprint.QueryNeedchain:
		if !validSource(q.Source, sprint.SourceIDs, sprint.SourceHead, sprint.SourceLine) || q.Limit < 1 || q.Limit > queryMaxRecords {
			return queryRequestRefusal()
		}
	case sprint.QueryJnote:
		if !validSource(q.Source, sprint.SourceIDs, sprint.SourceHead) || q.Subjects < 0 || q.Subjects > sprint.MaxAboutIDs {
			return queryRequestRefusal()
		}
		// The notes are the ones a list names by id (n<seq>) or the open notes,
		// the head of {p}jnotes@e.
		if q.Source.Kind == sprint.SourceHead && q.Source.Key != indexJNotes {
			return queryRequestRefusal()
		}
		for _, id := range q.Source.IDs {
			if _, _, ok := noteSeq(id); !ok {
				return queryRequestRefusal()
			}
		}
	default:
		return queryRequestRefusal()
	}
	if !validExtensions(q) {
		return queryRequestRefusal()
	}
	// The sizes, after every shape. A list longer than a read may return is past
	// a bound, as Layer 1's own ids query refuses one (LIMIT), and not a
	// malformed source.
	if q.Source.Kind == sprint.SourceIDs && len(q.Source.IDs) > queryMaxRecords {
		return limitRefusal("record")
	}
	if c := sprint.QueryCost(q); c.Records > queryMaxRecords {
		return limitRefusal("record")
	} else if c.RangeIDs > queryMaxRangeIDs {
		return limitRefusal("range_id")
	}
	return nil
}

// KeyQ is one of the bounded sprint-key reads (the errata's addendum): which
// kind, and the names it probes. The names a kind takes are the field of it
// that is set: Streams for dropping, Keys for parked, IDs for missing,
// Subjects and Names for jopen.
type KeyQ struct {
	Kind     string
	Streams  []string // dropping: the streams whose marks are read
	Keys     []string // parked: the agenda keys whose parked notes are read
	IDs      []string // missing: the needs whose scores in {p}missing@e are read
	Subjects []string // jopen: the subjects whose open judgments are read
	Names    []string // jopen: the "<type>|<cause>" fields read of each subject, beside its count
}

// ValidateKeyQ checks a sprint-key read's shape before any store is touched:
// a kind from SprintKeyKinds, and only the names that kind takes, each a name
// of the sprint's alphabet (a parked key and a jopen field name are opaque
// text of at most 256 bytes).
func ValidateKeyQ(q KeyQ) *Refusal {
	text := func(s string) bool { return s != "" && len(s) <= queryMaxName && !strings.ContainsAny(s, "\x00\r\n") }
	distinct := func(list []string, valid func(string) bool) bool {
		return len(list) <= keyQueryMaxNames && distinctNames(list, valid)
	}
	none := func(lists ...[]string) bool {
		for _, l := range lists {
			if l != nil {
				return false
			}
		}
		return true
	}
	ok := false
	switch q.Kind {
	case KeyClock, KeyLease, KeyTick, KeyHeartbeat, KeyDueCount:
		ok = none(q.Streams, q.Keys, q.IDs, q.Subjects, q.Names)
	case KeyDropping:
		ok = q.Streams != nil && distinct(q.Streams, validName) && none(q.Keys, q.IDs, q.Subjects, q.Names)
	case KeyParked:
		ok = q.Keys != nil && distinct(q.Keys, text) && none(q.Streams, q.IDs, q.Subjects, q.Names)
	case KeyMissing:
		ok = q.IDs != nil && distinct(q.IDs, validName) && none(q.Streams, q.Keys, q.Subjects, q.Names)
	case KeyJOpen:
		ok = q.Subjects != nil && q.Names != nil && distinct(q.Subjects, validName) && distinct(q.Names, text) &&
			none(q.Streams, q.Keys, q.IDs)
	}
	if !ok {
		return queryRequestRefusal()
	}
	return nil
}

// sourceObject is an id source as its wire object.
func sourceObject(s sprint.IDSource) map[string]any {
	m := map[string]any{"kind": s.Kind}
	switch s.Kind {
	case sprint.SourceIDs:
		m["ids"] = nonNilStrings(s.IDs)
	case sprint.SourceHead:
		m["key"], m["limit"] = s.Key, s.Limit
	case sprint.SourceLine:
		m["seq"], m["about"], m["offset"], m["limit"] = strconv.FormatUint(s.Seq, 10), s.About, s.Offset, s.Limit
	}
	return m
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// EncodeSprintQ is the query as ns_sprint_read carries it: the object above.
// A query that fails ValidateSprintQ is refused and nothing is encoded.
func EncodeSprintQ(q sprint.SprintQ) (SprintQuery, *Refusal) {
	if ref := ValidateSprintQ(q); ref != nil {
		return SprintQuery{}, ref
	}
	m := map[string]any{"kind": q.Kind, "fields": nonNilStrings(q.Fields)}
	switch q.Kind {
	case sprint.QueryRelated:
		m["t"], m["src"], m["follow"] = q.Table, sourceObject(q.Source), nonNilStrings(q.Follow)
	case sprint.QueryFront:
		heads := make([]map[string]any, 0, len(q.Heads))
		for _, h := range q.Heads {
			heads = append(heads, map[string]any{"index": h.Index, "limit": h.Limit, "follow": nonNilStrings(h.Follow)})
		}
		m["stream"], m["heads"] = q.Stream, heads
	case sprint.QueryWaiters:
		m["src"], m["limit"] = sourceObject(q.Source), q.Limit
		if q.WaiterAfter != "" {
			m["after"] = q.WaiterAfter
		}
		if q.Missing {
			m["missing"] = true
		}
	case sprint.QueryStreams:
		m["limit"] = q.Limit
		if q.Units > 0 {
			m["units"] = q.Units
		}
		if len(q.Counts) > 0 {
			m["counts"] = q.Counts
		}
	case sprint.QueryFleet, sprint.QueryReaders:
		if q.Units > 0 {
			m["units"] = q.Units
		}
	case sprint.QueryNeedchain:
		m["src"], m["limit"] = sourceObject(q.Source), q.Limit
	case sprint.QueryJnote:
		m["src"] = sourceObject(q.Source)
		if q.Subjects > 0 {
			m["subjects"] = q.Subjects
		}
	}
	if len(q.Keys) > 0 {
		m["keys"] = q.Keys
	}
	b, err := json.Marshal(m)
	if err != nil {
		return SprintQuery{}, queryRequestRefusal()
	}
	return SprintQuery{Kind: q.Kind, Query: b}, nil
}

// EncodeKeyQ is a sprint-key read as ns_sprint_read carries it. Its "fields"
// is the empty array: a key read returns no record (E6).
func EncodeKeyQ(q KeyQ) (SprintQuery, *Refusal) {
	if ref := ValidateKeyQ(q); ref != nil {
		return SprintQuery{}, ref
	}
	m := map[string]any{"kind": q.Kind, "fields": []string{}}
	switch q.Kind {
	case KeyDropping:
		m["streams"] = q.Streams
	case KeyParked:
		m["keys"] = q.Keys
	case KeyMissing:
		m["ids"] = q.IDs
	case KeyJOpen:
		m["subjects"], m["names"] = q.Subjects, q.Names
	}
	b, err := json.Marshal(m)
	if err != nil {
		return SprintQuery{}, queryRequestRefusal()
	}
	return SprintQuery{Kind: q.Kind, Query: b}, nil
}

// wireObject decodes a query object and holds it to the keys its kind takes:
// an unknown key is a malformed query, never ignored.
func wireObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, *Refusal) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, queryRequestRefusal()
	}
	ok := map[string]bool{"kind": true, "fields": true}
	for _, k := range allowed {
		ok[k] = true
	}
	for k := range m {
		if !ok[k] {
			return nil, queryRequestRefusal()
		}
	}
	return m, nil
}

// wireList decodes a required array of strings.
func wireList(m map[string]json.RawMessage, key string) ([]string, bool) {
	raw, present := m[key]
	var out []string
	if !present || json.Unmarshal(raw, &out) != nil || out == nil {
		return nil, false
	}
	return out, true
}

// isNull says a JSON value is null, which no field of a query may be: Layer 1's
// decoder reads it as cjson.null, which is no number, string or list.
func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

// wireInt decodes an optional integer; absent is 0, and null is not one.
func wireInt(m map[string]json.RawMessage, key string) (int, bool) {
	raw, present := m[key]
	if !present {
		return 0, true
	}
	var n int
	return n, !isNull(raw) && json.Unmarshal(raw, &n) == nil
}

func wireString(m map[string]json.RawMessage, key string) (string, bool) {
	var s string
	raw, present := m[key]
	return s, present && !isNull(raw) && json.Unmarshal(raw, &s) == nil
}

// wireSource decodes an id source object.
func wireSource(raw json.RawMessage) (sprint.IDSource, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return sprint.IDSource{}, false
	}
	kind, ok := wireString(m, "kind")
	if !ok {
		return sprint.IDSource{}, false
	}
	var allowed []string
	switch kind {
	case sprint.SourceIDs:
		allowed = []string{"kind", "ids"}
	case sprint.SourceHead:
		allowed = []string{"kind", "key", "limit"}
	case sprint.SourceLine:
		allowed = []string{"kind", "seq", "about", "offset", "limit"}
	default:
		return sprint.IDSource{}, false
	}
	for k := range m {
		found := false
		for _, a := range allowed {
			found = found || a == k
		}
		if !found {
			return sprint.IDSource{}, false
		}
	}
	s := sprint.IDSource{Kind: kind}
	switch kind {
	case sprint.SourceIDs:
		if s.IDs, ok = wireList(m, "ids"); !ok {
			return sprint.IDSource{}, false
		}
	case sprint.SourceHead:
		var ok2 bool
		s.Key, ok = wireString(m, "key")
		s.Limit, ok2 = wireInt(m, "limit")
		if !ok || !ok2 {
			return sprint.IDSource{}, false
		}
	case sprint.SourceLine:
		seq, ok1 := wireString(m, "seq")
		n, err := strconv.ParseUint(seq, 10, 64)
		if !ok1 || err != nil || strconv.FormatUint(n, 10) != seq {
			return sprint.IDSource{}, false
		}
		s.Seq = n
		if raw, present := m["about"]; present && (isNull(raw) || json.Unmarshal(raw, &s.About) != nil) {
			return sprint.IDSource{}, false
		}
		var ok2, ok3 bool
		s.Offset, ok2 = wireInt(m, "offset")
		s.Limit, ok3 = wireInt(m, "limit")
		if !ok2 || !ok3 {
			return sprint.IDSource{}, false
		}
	}
	return s, true
}

// DecodeSprintQ reads a composite query's wire object into IT05's SprintQ and
// checks it (ValidateSprintQ): what the Lua kinds' validate does to a decoded
// object. The kind is q.Kind; a mismatch is the caller's (checkSprintQuery).
func DecodeSprintQ(q SprintQuery) (sprint.SprintQ, *Refusal) {
	out := sprint.SprintQ{Kind: q.Kind}
	bad := func() (sprint.SprintQ, *Refusal) { return sprint.SprintQ{}, queryRequestRefusal() }
	var m map[string]json.RawMessage
	var ref *Refusal
	switch q.Kind {
	case sprint.QueryRelated:
		m, ref = wireObject(q.Query, "t", "src", "follow")
	case sprint.QueryFront:
		m, ref = wireObject(q.Query, "stream", "heads", "keys")
	case sprint.QueryWaiters:
		m, ref = wireObject(q.Query, "src", "limit", "after", "missing", "keys")
	case sprint.QueryNeedchain:
		m, ref = wireObject(q.Query, "src", "limit")
	case sprint.QueryStreams:
		m, ref = wireObject(q.Query, "limit", "units", "counts", "keys")
	case sprint.QueryFleet, sprint.QueryReaders:
		m, ref = wireObject(q.Query, "units")
	case sprint.QueryJnote:
		m, ref = wireObject(q.Query, "src", "subjects")
	default:
		return bad()
	}
	if ref != nil {
		return sprint.SprintQ{}, ref
	}
	if kind, ok := wireString(m, "kind"); !ok || kind != q.Kind {
		return bad()
	}
	var ok bool
	if out.Fields, ok = wireList(m, "fields"); !ok {
		return bad()
	}
	switch q.Kind {
	case sprint.QueryRelated:
		if out.Table, ok = wireString(m, "t"); !ok {
			return bad()
		}
		if out.Follow, ok = wireList(m, "follow"); !ok {
			return bad()
		}
		if out.Source, ok = wireSource(m["src"]); !ok {
			return bad()
		}
	case sprint.QueryFront:
		if out.Stream, ok = wireString(m, "stream"); !ok {
			return bad()
		}
		var heads []map[string]json.RawMessage
		if raw, present := m["heads"]; !present || json.Unmarshal(raw, &heads) != nil || heads == nil {
			return bad()
		}
		for _, h := range heads {
			for k := range h {
				if k != "index" && k != "limit" && k != "follow" {
					return bad()
				}
			}
			var hq sprint.HeadQ
			var ok1, ok2, ok3 bool
			hq.Index, ok1 = wireString(h, "index")
			hq.Limit, ok2 = wireInt(h, "limit")
			hq.Follow, ok3 = wireList(h, "follow")
			if !ok1 || !ok2 || !ok3 {
				return bad()
			}
			out.Heads = append(out.Heads, hq)
		}
	case sprint.QueryWaiters, sprint.QueryNeedchain:
		var ok1 bool
		if out.Source, ok = wireSource(m["src"]); !ok {
			return bad()
		}
		if out.Limit, ok1 = wireInt(m, "limit"); !ok1 {
			return bad()
		}
		if _, present := m["after"]; present {
			if out.WaiterAfter, ok = wireString(m, "after"); !ok {
				return bad()
			}
		}
		if raw, present := m["missing"]; present && (isNull(raw) || json.Unmarshal(raw, &out.Missing) != nil) {
			return bad()
		}
	case sprint.QueryStreams:
		var ok2 bool
		_, present := m["limit"] // the stuck ids asked for is required, and may be 0
		out.Limit, ok = wireInt(m, "limit")
		out.Units, ok2 = wireInt(m, "units")
		if !present || !ok || !ok2 {
			return bad()
		}
		if _, present := m["counts"]; present {
			if out.Counts, ok = wireList(m, "counts"); !ok {
				return bad()
			}
		}
	case sprint.QueryFleet, sprint.QueryReaders:
		if out.Units, ok = wireInt(m, "units"); !ok {
			return bad()
		}
	case sprint.QueryJnote:
		var ok1 bool
		if out.Source, ok = wireSource(m["src"]); !ok {
			return bad()
		}
		if out.Subjects, ok1 = wireInt(m, "subjects"); !ok1 {
			return bad()
		}
	}
	if _, present := m["keys"]; present {
		if out.Keys, ok = wireList(m, "keys"); !ok {
			return bad()
		}
	}
	if ref := ValidateSprintQ(out); ref != nil {
		return sprint.SprintQ{}, ref
	}
	return out, nil
}

// DecodeKeyQ reads a sprint-key read's wire object into a KeyQ and checks it
// (ValidateKeyQ). Its "fields" must be the empty array.
func DecodeKeyQ(q SprintQuery) (KeyQ, *Refusal) {
	out := KeyQ{Kind: q.Kind}
	bad := func() (KeyQ, *Refusal) { return KeyQ{}, queryRequestRefusal() }
	var allowed []string
	switch q.Kind {
	case KeyClock, KeyLease, KeyTick, KeyHeartbeat, KeyDueCount:
	case KeyDropping:
		allowed = []string{"streams"}
	case KeyParked:
		allowed = []string{"keys"}
	case KeyMissing:
		allowed = []string{"ids"}
	case KeyJOpen:
		allowed = []string{"subjects", "names"}
	default:
		return bad()
	}
	m, ref := wireObject(q.Query, allowed...)
	if ref != nil {
		return KeyQ{}, ref
	}
	if kind, ok := wireString(m, "kind"); !ok || kind != q.Kind {
		return bad()
	}
	if fields, ok := wireList(m, "fields"); !ok || len(fields) != 0 {
		return bad()
	}
	var ok1, ok2 bool
	switch q.Kind {
	case KeyDropping:
		out.Streams, ok1 = wireList(m, "streams")
		ok2 = true
	case KeyParked:
		out.Keys, ok1 = wireList(m, "keys")
		ok2 = true
	case KeyMissing:
		out.IDs, ok1 = wireList(m, "ids")
		ok2 = true
	case KeyJOpen:
		out.Subjects, ok1 = wireList(m, "subjects")
		out.Names, ok2 = wireList(m, "names")
	default:
		ok1, ok2 = true, true
	}
	if !ok1 || !ok2 {
		return bad()
	}
	if ref := ValidateKeyQ(out); ref != nil {
		return KeyQ{}, ref
	}
	return out, nil
}
