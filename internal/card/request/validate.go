package request

import (
	"reflect"
	"strconv"
	"strings"
)

// Validate checks a request that is already a value and reports every refusal
// found, not only the first. It returns nil, or a *Refusals. Parse runs it after
// reading a document; a caller that builds a Request runs it before Canonical.
func Validate(req *Request) error {
	c := newCollector()
	validate(req, c)
	return c.err()
}

type validator struct{ c *collector }

func joinStrings[T ~string](list []T) string {
	parts := make([]string, len(list))
	for i, v := range list {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// knownID returns s for a refusal's card ID when it is safe to print.
func knownID(s string) string {
	if s == "" {
		return ""
	}
	if cause, _ := textFault(s, MaxIDBytes); cause != "" {
		return ""
	}
	return s
}

// text checks one string field: present, clean, within max bytes, and of the
// grammar ok describes as want.
func (v validator) text(index int, id, field, s string, max int, ok func(string) bool, want string) {
	if s == "" {
		v.c.add(index, id, field, CauseRequired, "", want, "set the field")
		return
	}
	if cause, found := textFault(s, max); cause != "" {
		limit, remedy := want, "send a valid value"
		if cause == CauseTooLong {
			limit, remedy = strconv.Itoa(max)+" bytes", "shorten the value"
		}
		v.c.add(index, id, field, cause, found, limit, remedy)
		return
	}
	if !ok(s) {
		v.c.add(index, id, field, CauseBadValue, quote(s), want, "send a value of that form")
	}
}

func (v validator) cardID(index int, id, field string, s ID) {
	v.text(index, id, field, string(s), MaxIDBytes, ValidID, "ASCII letters, digits, underscore and hyphen")
}

func (v validator) digest(index int, id, field string, d Digest) {
	v.text(index, id, field, string(d), 64, func(s string) bool { return Digest(s).Valid() }, "64 lowercase hexadecimal characters (SHA-256)")
}

func (v validator) gitID(index int, id, field, s string) {
	v.text(index, id, field, s, 64, validGitID, "40 or 64 lowercase hexadecimal characters (git object id)")
}

func (v validator) name(index int, id, field, s string) {
	v.text(index, id, field, s, MaxNameBytes, func(s string) bool { return nameRE.MatchString(s) }, "letters, digits, underscore, dot and hyphen, starting with a letter, digit or underscore")
}

func (v validator) identity(index int, id, field, s string) {
	v.text(index, id, field, s, MaxIdentityBytes, func(s string) bool { return nameRE.MatchString(s) }, "letters, digits, underscore, dot and hyphen, starting with a letter, digit or underscore")
}

func (v validator) ref(index int, id, field, s string) {
	v.text(index, id, field, s, MaxRefBytes, validRef, "a reference with no whitespace")
}

func (v validator) counter(index int, id, field, s string, atLeastOne bool) {
	want := "a decimal integer within uint64"
	ok := validCounter
	if atLeastOne {
		want, ok = "a decimal integer from 1 within uint64", counterAtLeastOne
	}
	v.text(index, id, field, s, MaxCounterDigits, ok, want)
}

func (v validator) enum(index int, id, field, s string, allowed []string) {
	if s == "" {
		v.c.add(index, id, field, CauseRequired, "", "one of "+strings.Join(allowed, ", "), "set the field")
		return
	}
	if cause, found := textFault(s, MaxIdentityBytes); cause != "" {
		v.c.add(index, id, field, cause, found, "one of "+strings.Join(allowed, ", "), "send a valid value")
		return
	}
	if !contains(allowed, s) {
		v.c.add(index, id, field, CauseBadValue, quote(s), "one of "+strings.Join(allowed, ", "), "send one of the listed values")
	}
}

func stateNames() []string {
	out := make([]string, len(States))
	for i, s := range States {
		out[i] = string(s)
	}
	return out
}

// count checks an array's size against 1..max and returns how many entries to
// go on checking.
func (v validator) count(field string, n, max int, unit string) int {
	if n == 0 {
		v.c.add(-1, "", field, CauseEmptyArray, "0 entries", "at least 1", "send at least one "+unit+"; an empty array does nothing")
		return 0
	}
	if n > max {
		v.c.add(-1, "", field, CauseTooMany, strconv.Itoa(n)+" entries", strconv.Itoa(max)+" entries",
			"send at most "+strconv.Itoa(max)+" entries in one request; narrow it, and the narrower request is a separate operation with its own ID")
		return max
	}
	return n
}

func (v validator) expect(index int, id, prefix string, e Expect) {
	v.counter(index, id, prefix+".revision", e.Revision, true)
	v.name(index, id, prefix+".place.row", e.Place.Row)
	v.enum(index, id, prefix+".place.col", string(e.Place.Col), stateNames())
}

func validate(req *Request, c *collector) {
	if req == nil {
		c.add(-1, "", "", CauseRequired, "no request", "", "send a request")
		return
	}
	if req.Operation.Valid() {
		c.op = req.Operation
	}
	// The size comes first: past the cap the list is cut, and this is the refusal
	// the caller most needs to see.
	if n := len(Canonical(req)); n > MaxCanonicalBytes {
		c.add(-1, "", "", CauseTooLarge, strconv.Itoa(n)+" bytes canonical", strconv.Itoa(MaxCanonicalBytes)+" bytes",
			"send fewer or smaller entries; the request is never split for you")
	}
	v := validator{c}
	if req.Schema != SchemaVersion {
		c.add(-1, "", "schema", CauseBadValue, strconv.Itoa(req.Schema), strconv.Itoa(SchemaVersion), "send schema "+strconv.Itoa(SchemaVersion))
	}
	ops := make([]string, len(Operations))
	for i, o := range Operations {
		ops[i] = string(o)
	}
	v.enum(-1, "", "operation", string(req.Operation), ops)
	v.name(-1, "", "table", req.Table)
	if req.Operation.Mutating() || !req.Operation.Valid() {
		v.counter(-1, "", "epoch", req.Epoch, false)
		v.counter(-1, "", "expected_table_revision", req.TableRevision, false)
		v.identity(-1, "", "operation_id", req.OperationID)
		v.identity(-1, "", "actor", req.Actor)
	} else {
		for _, f := range [][2]string{{"epoch", req.Epoch}, {"expected_table_revision", req.TableRevision}, {"operation_id", req.OperationID}, {"actor", req.Actor}} {
			if f[1] != "" {
				c.add(-1, "", f[0], CauseNotApplicable, quote(f[1]), "", "an inspect reads only; remove the field")
			}
		}
	}

	present := map[string]bool{
		"admissions":   req.Admissions != nil,
		"events":       req.Events != nil,
		"evidence":     req.Evidence != nil,
		"replacements": req.Replacements != nil,
		"scope":        req.Scope != nil,
	}
	want := map[Operation]string{OpAdmit: "admissions", OpResolve: "scope", OpApplyEvents: "events", OpRecordEvidence: "evidence", OpReplace: "replacements", OpInspect: "scope"}[req.Operation]
	for _, name := range []string{"admissions", "events", "evidence", "replacements", "scope"} {
		if present[name] && name != want && req.Operation.Valid() {
			c.add(-1, "", name, CauseNotApplicable, "", "", string(req.Operation)+" takes "+want+"; remove "+name)
		}
	}
	if want != "" && !present[want] {
		c.add(-1, "", want, CauseRequired, "", "", string(req.Operation)+" takes "+want)
	}
	switch req.Operation {
	case OpAdmit:
		v.admissions(req.Admissions)
	case OpApplyEvents:
		v.events(req.Events)
	case OpRecordEvidence:
		v.evidence(req.Evidence)
	case OpReplace:
		v.replacements(req.Replacements)
	case OpResolve, OpInspect:
		if req.Scope != nil {
			v.scope(req.Scope)
		}
	}
}

func (v validator) admission(index int, prefix string, a *Admission) {
	id := knownID(string(a.ID))
	v.cardID(index, id, prefix+"id", a.ID)
	v.digest(index, id, prefix+"digest", a.Digest)
	v.gitID(index, id, prefix+"object_id", a.ObjectID)
	v.gitID(index, id, prefix+"commit", a.Commit)
	v.ref(index, id, prefix+"repository", a.Repository)
	v.text(index, id, prefix+"path", a.Path, MaxPathBytes, validRepoPath, "a clean repository-relative path: slash separated, no empty, . or .. segment, no backslash")
	v.name(index, id, prefix+"row", a.Row)
}

func (v validator) admissions(list []Admission) {
	if list == nil {
		return
	}
	n := v.count("admissions", len(list), MaxChangedEntries, "admission")
	first := map[ID]int{}
	for i := 0; i < n; i++ {
		a := &list[i]
		v.admission(i, "", a)
		if a.ID == "" {
			continue
		}
		if _, dup := first[a.ID]; dup {
			v.c.add(i, knownID(string(a.ID)), "id", CauseRepeatedID, quote(string(a.ID)), "one entry per card", "list each card once in a request")
			continue
		}
		first[a.ID] = i
	}
}

func (v validator) events(list []Event) {
	if list == nil {
		return
	}
	n := v.count("events", len(list), MaxChangedEntries, "event")
	for i := 0; i < n; i++ {
		v.event(i, &list[i])
	}
	for b := 1; b < n; b++ {
		eb := &list[b]
		if eb.ID == "" {
			continue
		}
		cause, hit := Cause(""), false
		for a := 0; a < b; a++ {
			ea := &list[a]
			if ea.ID != eb.ID {
				continue
			}
			hit = true
			var cc Cause
			switch {
			case chains(ea, eb) || chains(eb, ea):
				cc = CauseEventChain
			case reflect.DeepEqual(*ea, *eb):
				cc = CauseRepeatedID
			default:
				cc = CauseConflictingEvents
			}
			if rank(cc) > rank(cause) {
				cause = cc
			}
		}
		if !hit {
			continue
		}
		id := knownID(string(eb.ID))
		switch cause {
		case CauseEventChain:
			v.c.add(b, id, "expect.place.col", cause, quote(string(eb.Type)+"@"+string(eb.Expect.Place.Col)),
				"the source state is reachable only through another event of this request",
				"one event per card per request: send the later event in a request after the earlier one is applied")
		case CauseConflictingEvents:
			v.c.add(b, id, "id", cause, quote(string(eb.ID)), "one event per card per request",
				"keep the one event that should apply and send any other in a later request")
		default:
			v.c.add(b, id, "id", cause, quote(string(eb.ID)), "one entry per card", "list each card once in a request")
		}
	}
}

func rank(c Cause) int {
	switch c {
	case CauseEventChain:
		return 3
	case CauseConflictingEvents:
		return 2
	case CauseRepeatedID:
		return 1
	}
	return 0
}

// chains says the source state of b is the destination of a.
func chains(a, b *Event) bool {
	dst, ok := Destination(a.Type, a.Expect.Place.Col)
	return ok && dst == b.Expect.Place.Col
}

func (v validator) event(i int, e *Event) {
	id := knownID(string(e.ID))
	v.cardID(i, id, "id", e.ID)
	types := sortedTypes()
	v.enum(i, id, "type", string(e.Type), types)
	v.expect(i, id, "expect", e.Expect)
	v.digest(i, id, "digest", e.Digest)
	v.identity(i, id, "issuer", e.Issuer)
	v.ref(i, id, "source", e.Source)
	spec, known := eventSpecs[e.Type]
	if !known {
		return
	}
	for _, f := range eventFields {
		val := e.field(f)
		switch {
		case contains(spec.required, f):
			v.eventField(i, id, e, f, val)
		case contains(spec.allowed, f):
			if val != "" {
				v.eventField(i, id, e, f, val)
			}
		case val != "":
			v.c.add(i, id, f, CauseNotApplicable, quote(val), "", string(e.Type)+" does not carry "+f+"; remove it")
		}
	}
	if e.Expect.Place.Col.Valid() {
		if _, ok := Destination(e.Type, e.Expect.Place.Col); !ok {
			limit := "no source state: the type has no listed transition"
			remedy := "record this observation as evidence; an event of this type moves no card"
			if src := SourceStates(e.Type); len(src) > 0 {
				limit = "source state one of " + joinStrings(src)
				remedy = "declare a source state the type moves from; the destination is derived, never sent"
			}
			v.c.add(i, id, "expect.place.col", CauseNoTransition, quote(string(e.Type)+"@"+string(e.Expect.Place.Col)), limit, remedy)
		}
	}
}

func (v validator) eventField(i int, id string, e *Event, f, val string) {
	switch f {
	case "head":
		v.gitID(i, id, f, val)
	case "result":
		v.enum(i, id, f, val, []string{ResultSuccess, ResultFailure, ResultReturn})
	case "reason":
		v.text(i, id, f, val, MaxReasonBytes, validReason, "one line with no leading or trailing blank")
	case "dependency":
		v.cardID(i, id, f, ID(val))
		if val == string(e.ID) && val != "" {
			v.c.add(i, id, f, CauseBadValue, quote(val), "a card other than this one", "name the failed prerequisite")
		}
	case "landing":
		v.ref(i, id, f, val)
	}
}

func (v validator) evidence(list []Evidence) {
	if list == nil {
		return
	}
	n := v.count("evidence", len(list), MaxChangedEntries, "evidence entry")
	first := map[ID]int{}
	evSeen := map[ID]bool{}
	for i := 0; i < n; i++ {
		e := &list[i]
		id := knownID(string(e.ID))
		v.cardID(i, id, "id", e.ID)
		v.digest(i, id, "digest", e.Digest)
		v.expect(i, id, "expect", e.Expect)
		if e.ID != "" {
			if _, dup := first[e.ID]; dup {
				v.c.add(i, id, "id", CauseRepeatedID, quote(string(e.ID)), "one entry per card",
					"put every record for a card in that card's one entry")
			} else {
				first[e.ID] = i
			}
		}
		if e.Records == nil {
			v.c.add(i, id, "records", CauseRequired, "", "", "set the records")
			continue
		}
		switch {
		case len(e.Records) == 0:
			v.c.add(i, id, "records", CauseEmptyArray, "0 records", "at least 1", "send at least one record")
		case len(e.Records) > MaxEvidenceRecordsPerCard:
			v.c.add(i, id, "records", CauseTooMany, strconv.Itoa(len(e.Records))+" records", strconv.Itoa(MaxEvidenceRecordsPerCard)+" records",
				"send at most that many records for one card in one request")
		}
		for j := 0; j < len(e.Records) && j < MaxEvidenceRecordsPerCard; j++ {
			v.record(i, id, j, &e.Records[j], evSeen)
		}
	}
}

func (v validator) record(i int, id string, j int, r *EvidenceRecord, seen map[ID]bool) {
	p := "records[" + strconv.Itoa(j) + "]."
	v.cardID(i, id, p+"evidence_id", r.EvidenceID)
	if r.EvidenceID != "" {
		if seen[r.EvidenceID] {
			v.c.add(i, id, p+"evidence_id", CauseRepeatedID, quote(string(r.EvidenceID)), "each evidence ID once per request", "give each record its own evidence ID")
		}
		seen[r.EvidenceID] = true
	}
	v.enum(i, id, p+"kind", r.Kind, []string{KindRead, KindCI})
	switch r.Kind {
	case KindRead:
		v.enum(i, id, p+"disposition", r.Disposition, []string{DispAccept, DispReject})
		if r.Head != "" {
			v.gitID(i, id, p+"head", r.Head)
		}
	case KindCI:
		v.enum(i, id, p+"disposition", r.Disposition, []string{DispGreen, DispRed})
		v.gitID(i, id, p+"head", r.Head)
	default:
		if r.Disposition != "" {
			v.enum(i, id, p+"disposition", r.Disposition, []string{DispAccept, DispReject, DispGreen, DispRed})
		} else {
			v.c.add(i, id, p+"disposition", CauseRequired, "", "", "set the disposition")
		}
		if r.Head != "" {
			v.gitID(i, id, p+"head", r.Head)
		}
	}
	v.identity(i, id, p+"issuer", r.Issuer)
	v.ref(i, id, p+"source", r.Source)
}

func (v validator) replacements(list []Replacement) {
	if list == nil {
		return
	}
	n := v.count("replacements", len(list), MaxReplacementPairs, "replacement pair")
	seen := map[ID]bool{}
	note := func(i int, field string, id ID) {
		if id == "" {
			return
		}
		if seen[id] {
			v.c.add(i, knownID(string(id)), field, CauseRepeatedID, quote(string(id)), "each card ID once per request across old and new",
				"a card is either replaced or admitted once in a request")
		}
		seen[id] = true
	}
	for i := 0; i < n; i++ {
		r := &list[i]
		oid := knownID(string(r.Old.ID))
		v.cardID(i, oid, "old.id", r.Old.ID)
		v.digest(i, oid, "old.digest", r.Old.Digest)
		v.expect(i, oid, "old.expect", r.Old.Expect)
		if col := r.Old.Expect.Place.Col; col.Valid() && col != Waiting && col != Ready {
			v.c.add(i, oid, "old.expect.place.col", CauseNotEligible, quote(string(col)), "waiting or ready",
				"only a card that is waiting or ready is replaced; end a card in any other state with an event")
		}
		v.admission(i, "new.", &r.New)
		note(i, "old.id", r.Old.ID)
		note(i, "new.id", r.New.ID)
	}
}

func (v validator) scope(s *Scope) {
	const f = "scope"
	hasSel := s.Row != "" || s.Col != "" || s.Bound != 0
	switch {
	case s.IDs != nil && hasSel:
		v.c.add(-1, "", f, CauseBadValue, "ids with row, col or bound", "either an ID array or a selection", "send one form")
		return
	case s.IDs == nil && !hasSel:
		v.c.add(-1, "", f, CauseRequired, "", "an ID array or a selection", "name the cards by ID or by a row and column with a bound")
		return
	}
	if s.IDs != nil {
		n := v.count(f+".ids", len(s.IDs), MaxGuardOnlyEntries, "ID")
		first := map[ID]bool{}
		for i := 0; i < n; i++ {
			field := f + ".ids[" + strconv.Itoa(i) + "]"
			v.cardID(-1, "", field, s.IDs[i])
			if s.IDs[i] != "" && first[s.IDs[i]] {
				v.c.add(-1, knownID(string(s.IDs[i])), field, CauseRepeatedID, quote(string(s.IDs[i])), "each ID once", "list each card once")
			}
			first[s.IDs[i]] = true
		}
		return
	}
	if s.Row == "" && s.Col == "" {
		v.c.add(-1, "", f, CauseRequired, "a bound alone", "a row, a column or both", "declare the selection's row, column or both")
	}
	if s.Row != "" {
		v.name(-1, "", f+".row", s.Row)
	}
	if s.Col != "" {
		v.enum(-1, "", f+".col", string(s.Col), stateNames())
	}
	switch {
	case s.Bound == 0:
		v.c.add(-1, "", f+".bound", CauseRequired, "", "1 to "+strconv.Itoa(MaxGuardOnlyEntries), "declare the most cards the selection may hold")
	case s.Bound < 0:
		v.c.add(-1, "", f+".bound", CauseBadValue, strconv.Itoa(s.Bound), "1 to "+strconv.Itoa(MaxGuardOnlyEntries), "declare a positive bound")
	case s.Bound > MaxGuardOnlyEntries:
		v.c.add(-1, "", f+".bound", CauseTooMany, strconv.Itoa(s.Bound), strconv.Itoa(MaxGuardOnlyEntries),
			"declare a bound within the limit; a larger selection is narrowed by row or column")
	}
}
