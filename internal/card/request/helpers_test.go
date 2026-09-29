package request

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// seeds is the fixed seed list every property test runs.
var seeds = []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987, 2026}

var (
	dig  = strings.Repeat("a", 64)
	dig2 = strings.Repeat("b", 64)
	g40  = strings.Repeat("c", 40)
	g64  = strings.Repeat("d", 64)
)

func envelope(op Operation) *Request {
	r := &Request{Schema: SchemaVersion, Operation: op, Table: "work"}
	if op.Mutating() {
		r.Epoch, r.TableRevision, r.OperationID, r.Actor = "3", "12", "op-17", "coordinator"
	}
	return r
}

func adm(id string) Admission {
	return Admission{ID: ID(id), Digest: Digest(dig), ObjectID: g40, Commit: g40, Repository: "example.org/team/repo", Path: "cards/" + id + ".md", Row: "build"}
}

func expect(rev string, row string, col State) Expect {
	return Expect{Revision: rev, Place: Place{Row: row, Col: col}}
}

// event builds a valid event of a type that has a transition, from its first
// source state, with its required fields.
func event(t EventType, id string) Event {
	src := SourceStates(t)
	col := Review
	if len(src) > 0 {
		col = src[0]
	}
	e := Event{ID: ID(id), Type: t, Expect: expect("2", "build", col), Digest: Digest(dig), Issuer: "reader-1", Source: "artifact/" + id}
	for _, f := range eventSpecs[t].required {
		switch f {
		case "head":
			e.Head = g40
		case "result":
			e.Result = ResultSuccess
		case "reason":
			e.Reason = "needs another pass"
		case "dependency":
			e.Dependency = ID("dep-" + id)
		case "landing":
			e.Landing = g64
		}
	}
	return e
}

func evidenceEntry(id string) Evidence {
	return Evidence{ID: ID(id), Digest: Digest(dig), Expect: expect("4", "build", Review), Records: []EvidenceRecord{
		{EvidenceID: ID("ev-" + id + "-1"), Kind: KindRead, Disposition: DispAccept, Head: g40, Issuer: "reader-1", Source: "review/1"},
		{EvidenceID: ID("ev-" + id + "-2"), Kind: KindCI, Disposition: DispGreen, Head: g40, Issuer: "ci", Source: "run/9"},
	}}
}

func validRequest(op Operation) *Request {
	r := envelope(op)
	switch op {
	case OpAdmit:
		r.Admissions = []Admission{adm("c1"), adm("c2")}
	case OpApplyEvents:
		r.Events = []Event{event(EvStart, "c1"), event(EvResult, "c2"), event(EvVerdictAccept, "c3")}
	case OpRecordEvidence:
		r.Evidence = []Evidence{evidenceEntry("c1")}
	case OpReplace:
		r.Replacements = []Replacement{{Old: Retired{ID: "old1", Digest: Digest(dig), Expect: expect("2", "build", Ready)}, New: adm("new1")}}
	case OpResolve:
		r.Scope = &Scope{Row: "build", Col: Waiting, Bound: 100}
	case OpInspect:
		r.Scope = &Scope{IDs: []ID{"c1", "c2"}}
	}
	return r
}

// triples renders the refusals of an error as sorted "index|field|cause".
func triples(err error) []string {
	var out []string
	if err == nil {
		return out
	}
	rs, ok := err.(*Refusals)
	if !ok {
		return []string{"not a *Refusals: " + err.Error()}
	}
	for _, f := range rs.List {
		out = append(out, fmt.Sprintf("%d|%s|%s", f.Index, f.Field, f.Cause))
	}
	sort.Strings(out)
	return out
}

func wantTriples(w ...string) []string {
	out := append([]string{}, w...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustParse(t *testing.T, doc string) *Request {
	t.Helper()
	r, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

// clone copies a request through its canonical form.
func clone(t testing.TB, r *Request) *Request {
	t.Helper()
	c, err := Parse(Canonical(r))
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return c
}

// genRequest builds a valid random request of the operation.
func genRequest(rng *rand.Rand, op Operation) *Request {
	r := envelope(op)
	if op.Mutating() {
		r.Epoch = fmt.Sprint(rng.Uint64())
		r.TableRevision = fmt.Sprint(rng.Uint64())
		r.OperationID = fmt.Sprintf("op-%d", rng.Intn(1000000))
		r.Actor = []string{"coordinator", "worker.1", "A_b-c"}[rng.Intn(3)]
	}
	r.Table = []string{"work", "t.1", "T_x"}[rng.Intn(3)]
	n := 1 + rng.Intn(6)
	row := func() string { return []string{"build", "docs", "stream-2"}[rng.Intn(3)] }
	hex := func(n int) string {
		const h = "0123456789abcdef"
		b := make([]byte, n)
		for i := range b {
			b[i] = h[rng.Intn(16)]
		}
		return string(b)
	}
	gid := func() string {
		if rng.Intn(2) == 0 {
			return hex(40)
		}
		return hex(64)
	}
	texts := []string{"plain", "with <html> & \"quotes\"", "unicode é世界", "back\\slash", "x"}
	rev := func() string { return fmt.Sprint(1 + rng.Intn(1000)) }
	switch op {
	case OpAdmit:
		for i := 0; i < n; i++ {
			a := Admission{ID: ID(fmt.Sprintf("c%d", i)), Digest: Digest(hex(64)), ObjectID: gid(), Commit: gid(), Repository: "example.org/r" + fmt.Sprint(rng.Intn(9)), Path: fmt.Sprintf("d%d/c%d.md", rng.Intn(3), i), Row: row()}
			r.Admissions = append(r.Admissions, a)
		}
	case OpApplyEvents:
		var types []EventType
		for _, t := range EventTypes {
			if len(SourceStates(t)) > 0 {
				types = append(types, t)
			}
		}
		for i := 0; i < n; i++ {
			t := types[rng.Intn(len(types))]
			src := SourceStates(t)
			e := Event{ID: ID(fmt.Sprintf("c%d", i)), Type: t, Expect: expect(rev(), row(), src[rng.Intn(len(src))]), Digest: Digest(hex(64)), Issuer: "reader-" + fmt.Sprint(rng.Intn(5)), Source: "src/" + hex(6)}
			spec := eventSpecs[t]
			for _, f := range append(append([]string{}, spec.required...), spec.allowed...) {
				if !contains(spec.required, f) && rng.Intn(2) == 0 {
					continue
				}
				switch f {
				case "head":
					e.Head = gid()
				case "result":
					e.Result = []string{ResultSuccess, ResultFailure, ResultReturn}[rng.Intn(3)]
				case "reason":
					e.Reason = texts[rng.Intn(len(texts))]
				case "dependency":
					e.Dependency = ID(fmt.Sprintf("dep%d", i))
				case "landing":
					e.Landing = gid()
				}
			}
			r.Events = append(r.Events, e)
		}
	case OpRecordEvidence:
		for i := 0; i < n; i++ {
			e := Evidence{ID: ID(fmt.Sprintf("c%d", i)), Digest: Digest(hex(64)), Expect: expect(rev(), row(), States[rng.Intn(len(States))])}
			for j := 0; j < 1+rng.Intn(MaxEvidenceRecordsPerCard); j++ {
				rec := EvidenceRecord{EvidenceID: ID(fmt.Sprintf("ev%d-%d", i, j)), Issuer: "who", Source: "s/" + hex(4)}
				if rng.Intn(2) == 0 {
					rec.Kind, rec.Disposition = KindRead, []string{DispAccept, DispReject}[rng.Intn(2)]
					if rng.Intn(2) == 0 {
						rec.Head = gid()
					}
				} else {
					rec.Kind, rec.Disposition, rec.Head = KindCI, []string{DispGreen, DispRed}[rng.Intn(2)], gid()
				}
				e.Records = append(e.Records, rec)
			}
			r.Evidence = append(r.Evidence, e)
		}
	case OpReplace:
		for i := 0; i < n; i++ {
			nw := adm(fmt.Sprintf("new%d", i))
			nw.Digest = Digest(hex(64))
			r.Replacements = append(r.Replacements, Replacement{
				Old: Retired{ID: ID(fmt.Sprintf("old%d", i)), Digest: Digest(hex(64)), Expect: expect(rev(), row(), []State{Waiting, Ready}[rng.Intn(2)])},
				New: nw})
		}
	case OpResolve, OpInspect:
		switch rng.Intn(3) {
		case 0:
			r.Scope = &Scope{IDs: idsN(n)}
		case 1:
			r.Scope = &Scope{Row: row(), Bound: 1 + rng.Intn(MaxGuardOnlyEntries)}
		default:
			r.Scope = &Scope{Row: row(), Col: States[rng.Intn(len(States))], Bound: 1 + rng.Intn(MaxGuardOnlyEntries)}
		}
	}
	return r
}

func idsN(n int) []ID {
	out := make([]ID, n)
	for i := range out {
		out[i] = ID(fmt.Sprintf("c%d", i))
	}
	return out
}

// jsonWriter re-emits a generic JSON value with shuffled key order, random
// whitespace and random \u escapes.
func jsonWriter(rng *rand.Rand, b *strings.Builder, v any) {
	ws := func() {
		for i := rng.Intn(3); i > 0; i-- {
			b.WriteString([]string{" ", "\n", "\t", "\r\n"}[rng.Intn(4)])
		}
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		b.WriteByte('{')
		ws()
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
				ws()
			}
			jsonString(rng, b, k)
			ws()
			b.WriteByte(':')
			ws()
			jsonWriter(rng, b, t[k])
			ws()
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		ws()
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
				ws()
			}
			jsonWriter(rng, b, e)
			ws()
		}
		b.WriteByte(']')
	case string:
		jsonString(rng, b, t)
	case json.Number:
		b.WriteString(t.String())
	default:
		b.WriteString("null")
	}
}

func jsonString(rng *rand.Rand, b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case rng.Intn(4) == 0 && r < 0x10000:
			fmt.Fprintf(b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// leaves calls f on every settable string and int of v, in a fixed order.
func leaves(v reflect.Value, f func(reflect.Value)) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			leaves(v.Elem(), f)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			leaves(v.Field(i), f)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			leaves(v.Index(i), f)
		}
	case reflect.String, reflect.Int:
		f(v)
	}
}
