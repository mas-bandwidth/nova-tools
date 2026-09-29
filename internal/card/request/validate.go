package request

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// Validate checks a request that is already a value and reports every refusal
// found, not only the first: it returns the validated request, which carries its
// canonical bytes and hash, or nil and a *Refusals. Parse runs it after reading a
// document. A request that is not a *Valid has not been checked, and nothing
// above this package takes one.
func Validate(req *Request) (*Valid, error) {
	c := newCollector()
	validate(req, c)
	if err := c.err(); err != nil {
		return nil, err
	}
	return newValid(req), nil
}

type validator struct{ c *collector }

func joinStrings[T ~string](list []T) string {
	parts := make([]string, len(list))
	for i, v := range list {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// fault adds the refusal a text fault names.
func (v validator) fault(index int, id, field, s string, cause Cause, max int, want string) {
	limit, next := want, "send a valid value"
	found := quote(s)
	if cause == CauseTooLong {
		limit, next, found = strconv.Itoa(max)+" bytes", "shorten the value", quote(s)
	}
	v.c.add(index, id, field, cause, found, limit, next)
}

// text checks one free-text value: present, clean of controls, bidi and
// zero-width characters, within max bytes, and of the form ok describes as want.
func (v validator) text(index int, id, field, s string, max int, ok func(string) bool, want string) {
	if s == "" {
		v.c.add(index, id, field, CauseRequired, "", want, "set the field")
		return
	}
	if cause := card.TextFault(s, max); cause != "" {
		v.fault(index, id, field, s, cause, max, want)
		return
	}
	if !ok(s) {
		v.c.add(index, id, field, CauseInvalidValue, quote(s), want, "send a value of that form")
	}
}

func (v validator) cardID(index int, id, field string, s ID) {
	if cause, why := card.IDFault(string(s)); cause != "" {
		switch cause {
		case CauseRequired:
			v.c.add(index, id, field, cause, "", why, "set the field")
		case CauseTooLong:
			v.c.add(index, id, field, cause, quote(string(s)), "64 bytes", "shorten the value")
		default:
			v.c.add(index, id, field, cause, quote(string(s)), why, "send a valid card ID")
		}
	}
}

func (v validator) digest(index int, id, field string, d Digest) {
	v.text(index, id, field, string(d), 64, func(s string) bool { return Digest(s).Valid() }, "64 lower-case hexadecimal characters (SHA-256)")
}

func (v validator) objectID(index int, id, field, s string) {
	v.text(index, id, field, s, 64, card.ValidObjectID, "40 or 64 lower-case hexadecimal characters (git object id)")
}

func (v validator) head(index int, id, field, s string) {
	v.text(index, id, field, s, MaxHeadBytes, card.ValidHead, "a git object id (40 or 64 lower-case hex) or a sha256: digest")
}

func (v validator) name(index int, id, field, s string) {
	v.text(index, id, field, s, MaxNameBytes, func(s string) bool { return card.ValidName(s, MaxNameBytes) },
		"letters, digits, underscore, dot and hyphen, starting with a letter, digit or underscore")
}

// token checks a reference: an issuer, actor, operation ID, source artifact,
// landing identity or verifier. It is printable ASCII with no space, comma or
// quote (card.TokenChars).
func (v validator) token(index int, id, field, s string, max int) {
	v.text(index, id, field, s, max, func(s string) bool { return card.ValidToken(s, max) },
		"a token of at most "+strconv.Itoa(max)+" bytes: "+card.TokenChars)
}

func (v validator) counter(index int, id, field, s string, atLeastOne bool) {
	want := "a decimal integer within uint64"
	ok := card.ValidCounter
	if atLeastOne {
		want, ok = "a decimal integer from 1 within uint64", card.CounterAtLeastOne
	}
	v.text(index, id, field, s, MaxCounterDigits, ok, want)
}

func (v validator) enum(index int, id, field, s string, allowed []string) {
	want := "one of " + strings.Join(allowed, ", ")
	if s == "" {
		v.c.add(index, id, field, CauseRequired, "", want, "set the field")
		return
	}
	if cause := card.TextFault(s, MaxIdentityBytes); cause != "" {
		v.fault(index, id, field, s, cause, MaxIdentityBytes, want)
		return
	}
	if !contains(allowed, s) {
		v.c.add(index, id, field, CauseInvalidValue, quote(s), want, "send one of the listed values")
	}
}

func stateNames() []string {
	out := make([]string, len(allStates))
	for i, s := range allStates {
		out[i] = string(s)
	}
	return out
}

func toStrings[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, s := range list {
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

// expect checks a guard; the revision is optional only when revOptional says so.
func (v validator) expect(index int, id, prefix string, e Expect, revOptional bool) {
	if !(revOptional && e.Revision == "") {
		v.counter(index, id, prefix+".revision", e.Revision, true)
	}
	v.name(index, id, prefix+".place.row", e.Place.Row)
	v.enum(index, id, prefix+".place.col", string(e.Place.Col), stateNames())
}

func (v validator) reason(index int, id, field, s string) {
	v.text(index, id, field, s, card.MaxReasonBytes, func(s string) bool { return s == strings.TrimSpace(s) },
		"one line with no leading or trailing blank")
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
		c.add(-1, "", "schema", CauseInvalidValue, strconv.Itoa(req.Schema), strconv.Itoa(SchemaVersion), "send schema "+strconv.Itoa(SchemaVersion))
	}
	v.enum(-1, "", "operation", string(req.Operation), toStrings(allOperations[:]))
	v.name(-1, "", "table", req.Table)
	if req.Operation.Mutating() || !req.Operation.Valid() {
		v.counter(-1, "", "epoch", req.Epoch, false)
		v.counter(-1, "", "expected_table_revision", req.TableRevision, false)
		if req.OperationID != "" {
			v.token(-1, "", "operation_id", req.OperationID, card.MaxOperationIDBytes)
		}
		v.token(-1, "", "actor", req.Actor, MaxIdentityBytes)
	} else {
		for _, f := range [][2]string{{"epoch", req.Epoch}, {"expected_table_revision", req.TableRevision}, {"operation_id", req.OperationID}, {"actor", req.Actor}} {
			if f[1] != "" {
				c.add(-1, "", f[0], CauseNotApplicable, quote(f[1]), "", "a read has no epoch, revision, operation ID or actor; remove the field")
			}
		}
	}

	present := map[string]bool{
		"admissions":   req.Admissions != nil,
		"inputs":       req.Inputs != nil,
		"evidence":     req.Evidence != nil,
		"replacements": req.Replacements != nil,
		"scope":        req.Scope != nil,
	}
	want := payloadOf(req.Operation)
	for _, name := range []string{"admissions", "inputs", "evidence", "replacements", "scope"} {
		if present[name] && name != want && req.Operation.Valid() {
			takes := want
			if takes == "" {
				takes = "no payload"
			}
			c.add(-1, "", name, CauseNotApplicable, "", "", string(req.Operation)+" takes "+takes+"; remove "+name)
		}
	}
	if want != "" && !present[want] && req.Operation != OpCheck {
		c.add(-1, "", want, CauseRequired, "", "", string(req.Operation)+" takes "+want)
	}
	switch req.Operation {
	case OpAdmit:
		v.admissions(req.Admissions)
	case OpApplyEvents:
		v.inputs(req.Inputs)
	case OpRecordEvidence:
		v.evidence(req.Evidence)
	case OpReplace:
		v.replacements(req.Replacements)
	case OpResolve, OpInspect, OpCheck:
		if req.Scope != nil {
			v.scope(req.Scope, req.Operation)
		}
	}
}

func (v validator) admission(index int, prefix string, a *Admission) {
	id := knownID(string(a.ID))
	v.cardID(index, id, prefix+"id", a.ID)
	v.digest(index, id, prefix+"digest", a.Digest)
	v.objectID(index, id, prefix+"object_id", a.ObjectID)
	v.objectID(index, id, prefix+"commit", a.Commit)
	v.repository(index, id, prefix+"repository", a.Repository)
	v.path(index, id, prefix+"path", a.Path)
	v.text(index, id, prefix+"kind", a.Kind, 32, card.ValidKind, "lower-case letters, digits and hyphen, starting with a letter")
	v.dependsOn(index, id, prefix+"depends_on", a.ID, a.DependsOn)
	if a.Entry != "" {
		v.text(index, id, prefix+"entry", a.Entry, MaxEntryBytes, func(s string) bool {
			return !strings.Contains(s, ",") && s == strings.TrimSpace(s)
		}, "one entry path with no comma and no padding")
	}
	v.text(index, id, prefix+"title", a.Title, MaxTitleBytes, func(s string) bool { return s == strings.TrimSpace(s) }, "one line with no leading or trailing blank")
	v.name(index, id, prefix+"row", a.Row)
	v.counter(index, id, prefix+"policy_version", a.PolicyVersion, true)
	v.digest(index, id, prefix+"policy_digest", a.PolicyDigest)
}

// repository checks an identity. A refusal names the rule it broke and never
// quotes the value: a caller may have sent an origin URL that carries a
// credential.
func (v validator) repository(index int, id, field string, r card.Repository) {
	if r == "" {
		v.c.add(index, id, field, CauseRequired, "", "a repository identity host[:port]/owner/name", "set the field")
		return
	}
	if why := card.RepositoryWhy(string(r)); why != "" {
		v.c.add(index, id, field, CauseInvalidRepository, "the value "+why+" (not quoted)", "a repository identity host[:port]/owner/name, no scheme, no user, no .git",
			"send the identity, not the remote URL")
	}
}

// path checks a repository-relative path.
func (v validator) path(index int, id, field, p string) {
	if cause, why := card.PathFault(p); cause != "" {
		found := quote(p)
		if cause == CauseRequired {
			found = ""
		}
		v.c.add(index, id, field, cause, found, why, "send a clean repository-relative path")
	}
}

func (v validator) dependsOn(index int, id, field string, self ID, deps []ID) {
	if len(deps) > MaxDependsOn {
		v.c.add(index, id, field, CauseTooMany, strconv.Itoa(len(deps))+" dependencies", strconv.Itoa(MaxDependsOn), "name at most "+strconv.Itoa(MaxDependsOn)+" prerequisites")
		deps = deps[:MaxDependsOn]
	}
	seen := map[ID]bool{}
	for i, d := range deps {
		f := field + "[" + strconv.Itoa(i) + "]"
		v.cardID(index, id, f, d)
		switch {
		case d == "":
		case d == self:
			v.c.add(index, id, f, CauseInvalidValue, quote(string(d)), "a card other than this one", "remove the card's own ID")
		case seen[d]:
			v.c.add(index, id, f, CauseRepeatedID, quote(string(d)), "each dependency once", "name each prerequisite once")
		}
		seen[d] = true
	}
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
	outside := map[ID]bool{}
	for i := 0; i < n; i++ {
		for _, d := range list[i].DependsOn {
			if _, in := first[d]; !in && d != "" {
				outside[d] = true
			}
		}
	}
	if len(outside) > MaxOutsideDependencies {
		v.c.add(-1, "", "admissions", CauseTooMany, strconv.Itoa(len(outside))+" outside dependencies", strconv.Itoa(MaxOutsideDependencies),
			"admit fewer cards, or cards with fewer prerequisites, in one request")
	}
}

func (v validator) inputs(list []Input) {
	if list == nil {
		return
	}
	n := v.count("inputs", len(list), MaxChangedEntries, "lifecycle input")
	for i := 0; i < n; i++ {
		v.input(i, &list[i])
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
				cc = CauseInputChain
			case reflect.DeepEqual(*ea, *eb):
				cc = CauseRepeatedID
			default:
				cc = CauseConflictingInputs
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
		case CauseInputChain:
			v.c.add(b, id, "expect.place.col", cause, quote(string(eb.Type)+"@"+string(eb.Expect.Place.Col)),
				"the source state is reachable only through another lifecycle input of this request",
				"one lifecycle input per card per request: send the later input in a request after the earlier one is applied")
		case CauseConflictingInputs:
			v.c.add(b, id, "id", cause, quote(string(eb.ID)), "one lifecycle input per card per request",
				"keep the one input that should apply and send any other in a later request")
		default:
			v.c.add(b, id, "id", cause, quote(string(eb.ID)), "one entry per card", "list each card once in a request")
		}
	}
}

func rank(c Cause) int {
	switch c {
	case CauseInputChain:
		return 3
	case CauseConflictingInputs:
		return 2
	case CauseRepeatedID:
		return 1
	}
	return 0
}

// chains says the source state of b is the destination of a.
func chains(a, b *Input) bool {
	dst, ok := Destination(a.Type, a.Expect.Place.Col)
	return ok && dst == b.Expect.Place.Col
}

func (v validator) input(i int, e *Input) {
	id := knownID(string(e.ID))
	v.cardID(i, id, "id", e.ID)
	v.enum(i, id, "type", string(e.Type), sortedTypes())
	v.expect(i, id, "expect", e.Expect, false)
	v.digest(i, id, "digest", e.Digest)
	v.token(i, id, "issuer", e.Issuer, MaxIdentityBytes)
	v.token(i, id, "source", e.Source, MaxRefBytes)
	required, allowed, known := fieldsOf(e.Type)
	if !known {
		return
	}
	for _, f := range inputFields {
		val := e.field(f)
		switch {
		case contains(required, f):
			v.inputField(i, id, e, f, val)
		case contains(allowed, f):
			if val != "" {
				v.inputField(i, id, e, f, val)
			}
		case val != "":
			v.c.add(i, id, f, CauseNotApplicable, quote(val), "", string(e.Type)+" does not carry "+f+"; remove it")
		}
	}
	if e.Expect.Place.Col.Valid() {
		if _, ok := Destination(e.Type, e.Expect.Place.Col); !ok {
			limit := "no source state: the type has no listed transition"
			next := "record this observation as evidence; a lifecycle input of this type moves no card"
			if src := SourceStates(e.Type); len(src) > 0 {
				limit = "source state one of " + joinStrings(src)
				next = "declare a source state the type moves from; the destination is derived, never sent"
			}
			v.c.add(i, id, "expect.place.col", CauseNoTransition, quote(string(e.Type)+"@"+string(e.Expect.Place.Col)), limit, next)
		}
	}
}

func sortedTypes() []string {
	out := toStrings(InputTypes())
	sort.Strings(out)
	return out
}

func (v validator) inputField(i int, id string, e *Input, f, val string) {
	switch f {
	case "head":
		v.head(i, id, f, val)
	case "result":
		v.enum(i, id, f, val, toStrings(ResultValues()))
	case "reason":
		v.reason(i, id, f, val)
	case "dependency":
		v.cardID(i, id, f, ID(val))
		if val == string(e.ID) && val != "" {
			v.c.add(i, id, f, CauseInvalidValue, quote(val), "a card other than this one", "name the failed prerequisite")
		}
	case "landing":
		v.token(i, id, f, val, MaxRefBytes)
	}
}

func (v validator) evidence(list []Evidence) {
	if list == nil {
		return
	}
	n := v.count("evidence", len(list), MaxChangedEntries, "evidence entry")
	first := map[ID]int{}
	total := 0
	recIDs := map[string]bool{}
	for i := 0; i < n; i++ {
		e := &list[i]
		id := knownID(string(e.ID))
		v.cardID(i, id, "id", e.ID)
		v.expect(i, id, "expect", e.Expect, true)
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
		total += len(e.Records)
		keys := map[string]int{}
		for j := 0; j < len(e.Records) && j < MaxEvidenceRecordsPerCard; j++ {
			v.record(i, id, j, &e.Records[j], recIDs, keys)
		}
	}
	if total > MaxEvidenceRecords {
		v.c.add(-1, "", "evidence", CauseTooMany, strconv.Itoa(total)+" records", strconv.Itoa(MaxEvidenceRecords)+" records in all",
			"send at most that many records in one request; narrow it, and the narrower request is a separate operation with its own ID")
	}
}

// record checks one submitted record. Within one entry at most one record has a
// kind, issuer and head: two observations of one check, or by one reader, at one
// head cannot be ordered inside one batch.
func (v validator) record(i int, id string, j int, r *Record, seen map[string]bool, keys map[string]int) {
	p := "records[" + strconv.Itoa(j) + "]."
	bad := false
	if !r.Kind.valid() || r.Kind == KindQueue {
		if r.Kind == "" {
			v.c.add(i, id, p+"kind", CauseRequired, "", "one of read, ci, sweep, landing", "set the field")
		} else if r.Kind == KindQueue {
			v.c.add(i, id, p+"kind", CauseNotApplicable, quote(string(r.Kind)), "read, ci, sweep or landing", "a queue rejection is written by the queue-rejected lifecycle input, never submitted")
		} else {
			v.text(i, id, p+"kind", string(r.Kind), MaxIdentityBytes, func(string) bool { return false }, "one of read, ci, sweep, landing")
		}
		bad = true
	}
	if r.Kind.valid() && r.Kind != KindQueue {
		var allowed []string
		for _, d := range DispositionsOf(r.Kind) {
			allowed = append(allowed, string(d))
		}
		v.enum(i, id, p+"disposition", string(r.Disposition), allowed)
	} else if r.Disposition == "" {
		v.c.add(i, id, p+"disposition", CauseRequired, "", "", "set the disposition")
	}
	v.token(i, id, p+"issuer", r.Issuer, MaxIdentityBytes)
	v.head(i, id, p+"head", r.Head)
	v.digest(i, id, p+"digest", r.Def)
	v.token(i, id, p+"verifier", r.Verifier, MaxVerifierBytes)
	v.token(i, id, p+"artifact", r.Artifact, MaxRefBytes)
	if bad || field(r) != "" {
		return
	}
	rid := id + "|" + r.ID()
	if seen[rid] {
		v.c.add(i, id, strings.TrimSuffix(p, "."), CauseRepeatedID, quote(r.ID()), "each record once per request", "submit each observation once")
		return
	}
	seen[rid] = true
	key := string(r.Kind) + "|" + r.Issuer + "|" + r.Head
	if first, dup := keys[key]; dup {
		v.c.add(i, id, strings.TrimSuffix(p, "."), CauseConflictingRecords, "records["+strconv.Itoa(first)+"] and records["+strconv.Itoa(j)+"] name one kind, issuer and head",
			"one record per kind, issuer and head in one entry", "keep the observation that should apply and send the other in a later request")
		return
	}
	keys[key] = j
}

// field is the first faulty field of a submitted record, or "".
func field(r *Record) string { f, _, _ := recordFault(*r, true); return f }

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
		v.expect(i, oid, "old.expect", r.Old.Expect, false)
		if col := r.Old.Expect.Place.Col; col.Valid() && !Replaceable(col) {
			v.c.add(i, oid, "old.expect.place.col", CauseNotEligible, quote(string(col)), strings.Join(toStrings(ReplaceableStates()), " or "),
				"only a card in "+strings.Join(toStrings(ReplaceableStates()), " or ")+" is replaced; end a card in any other state with a lifecycle input")
		}
		v.admission(i, "new.", &r.New)
		note(i, "old.id", r.Old.ID)
		note(i, "new.id", r.New.ID)
	}
}

func (v validator) scope(s *Scope, op Operation) {
	const f = "scope"
	forms := 0
	if s.IDs != nil {
		forms++
	}
	if s.Rows != nil {
		forms++
	}
	if s.All {
		forms++
	}
	switch {
	case forms > 1:
		v.c.add(-1, "", f, CauseInvalidValue, "more than one of ids, rows and all", "one form: card IDs, whole rows, or the whole table", "send one form")
		return
	case forms == 0:
		v.c.add(-1, "", f, CauseRequired, "", "one of ids, rows, all", "name the cards by ID, by whole rows, or the whole table")
		return
	}
	switch {
	case s.IDs != nil:
		max := MaxScopeCards
		if op == OpResolve {
			max = MaxResolveCards
		}
		n := v.count(f+".ids", len(s.IDs), max, "ID")
		first := map[ID]bool{}
		for i := 0; i < n; i++ {
			field := f + ".ids[" + strconv.Itoa(i) + "]"
			v.cardID(-1, "", field, s.IDs[i])
			if s.IDs[i] != "" && first[s.IDs[i]] {
				v.c.add(-1, knownID(string(s.IDs[i])), field, CauseRepeatedID, quote(string(s.IDs[i])), "each ID once", "list each card once")
			}
			first[s.IDs[i]] = true
		}
	case s.Rows != nil:
		n := v.count(f+".rows", len(s.Rows), MaxScopeRows, "row")
		first := map[string]bool{}
		for i := 0; i < n; i++ {
			field := f + ".rows[" + strconv.Itoa(i) + "]"
			v.name(-1, "", field, s.Rows[i])
			if s.Rows[i] != "" && first[s.Rows[i]] {
				v.c.add(-1, "", field, CauseRepeatedID, quote(s.Rows[i]), "each row once", "list each row once")
			}
			first[s.Rows[i]] = true
		}
	}
}
