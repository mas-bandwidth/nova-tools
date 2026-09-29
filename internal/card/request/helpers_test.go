package request

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
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
	return Admission{ID: ID(id), Digest: Digest(dig), ObjectID: g40, Commit: g40, Repository: "example.org/team/repo", Path: "cards/" + id + ".md",
		Kind: "fix-red", Title: "Fix " + id, Row: "build", PolicyVersion: "1", PolicyDigest: Digest(dig2)}
}

func expect(rev string, row string, col State) Expect {
	return Expect{Revision: rev, Place: Place{Row: row, Col: col}}
}

// input builds a valid lifecycle input of a type, from its first source state,
// with its required fields.
func input(t InputType, id string) Input {
	src := SourceStates(t)
	col := Review
	if len(src) > 0 {
		col = src[0]
	}
	e := Input{ID: ID(id), Type: t, Expect: expect("2", "build", col), Digest: Digest(dig), Issuer: "reader-1", Source: "artifact/" + id}
	for _, f := range reqOf(t) {
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

func rec(kind EvidenceKind, disp Disposition, issuer, head, artifact string) Record {
	return Record{Kind: kind, Issuer: issuer, Disposition: disp, Head: head, Def: Digest(dig), Verifier: "forge", Artifact: artifact}
}

func evidenceEntry(id string) Evidence {
	return Evidence{ID: ID(id), Expect: expect("4", "build", Review), Records: []Record{
		rec(KindRead, DispAccept, "reader-1", g40, "review:1"),
		rec(KindCI, DispGreen, "ci:unit", g40, "run:9"),
	}}
}

func validRequest(op Operation) *Request {
	r := envelope(op)
	switch op {
	case OpAdmit:
		r.Admissions = []Admission{adm("c1"), adm("c2")}
	case OpApplyEvents:
		r.Inputs = []Input{input(InStart, "c1"), input(InResult, "c2"), input(InVerdictAccept, "c3")}
	case OpRecordEvidence:
		r.Evidence = []Evidence{evidenceEntry("c1")}
	case OpReplace:
		r.Replacements = []Replacement{{Old: Retired{ID: "old1", Digest: Digest(dig), Expect: expect("2", "build", Ready)}, New: adm("new1")}}
	case OpResolve:
		r.Scope = &Scope{Rows: []string{"build"}}
	case OpInspect:
		r.Scope = &Scope{IDs: []ID{"c1", "c2"}}
	case OpCheck:
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

func mustParse(t *testing.T, doc string) *Valid {
	t.Helper()
	r, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

// genRequest builds a valid random request of the operation.
func genRequest(rng *rand.Rand, op Operation) *Request {
	r := envelope(op)
	if op.Mutating() {
		r.Epoch = fmt.Sprint(rng.Uint64())
		r.TableRevision = fmt.Sprint(rng.Uint64())
		if rng.Intn(4) != 0 {
			r.OperationID = fmt.Sprintf("op-%d", rng.Intn(1000000))
		} else {
			r.OperationID = ""
		}
		r.Actor = []string{"coordinator", "worker.1", "A_b-c", "agent@host"}[rng.Intn(4)]
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
		switch rng.Intn(3) {
		case 0:
			return hex(40)
		case 1:
			return hex(64)
		}
		return "sha256:" + hex(64)
	}
	texts := []string{"plain", "with <html> & \"quotes\"", "unicode \u00e9\u4e16\u754c", "back\\slash", "x"}
	rev := func() string { return fmt.Sprint(1 + rng.Intn(1000)) }
	newAdm := func(id string) Admission {
		a := Admission{ID: ID(id), Digest: Digest(hex(64)), ObjectID: hex(40), Commit: hex(40), Repository: card.Repository("example.org/r" + fmt.Sprint(rng.Intn(9))),
			Path: fmt.Sprintf("d%d/%s.md", rng.Intn(3), id), Kind: []string{"fix-red", "read", "text"}[rng.Intn(3)], Title: texts[rng.Intn(len(texts))],
			Row: row(), PolicyVersion: fmt.Sprint(1 + rng.Intn(9)), PolicyDigest: Digest(hex(64))}
		if rng.Intn(2) == 0 {
			a.Entry = "work/" + hex(4)
		}
		for k := rng.Intn(4); k > 0; k-- {
			a.DependsOn = append(a.DependsOn, ID(fmt.Sprintf("dep%d", rng.Intn(20)+k*20)))
		}
		return a
	}
	switch op {
	case OpAdmit:
		for i := 0; i < n; i++ {
			r.Admissions = append(r.Admissions, newAdm(fmt.Sprintf("c%d", i)))
		}
	case OpApplyEvents:
		types := InputTypes()
		for i := 0; i < n; i++ {
			t := types[rng.Intn(len(types))]
			src := SourceStates(t)
			e := Input{ID: ID(fmt.Sprintf("c%d", i)), Type: t, Expect: expect(rev(), row(), src[rng.Intn(len(src))]), Digest: Digest(hex(64)), Issuer: "reader-" + fmt.Sprint(rng.Intn(5)), Source: "src/" + hex(6)}
			required, allowed, _ := fieldsOf(t)
			for _, f := range append(append([]string{}, required...), allowed...) {
				if !contains(required, f) && rng.Intn(2) == 0 {
					continue
				}
				switch f {
				case "head":
					e.Head = gid()
				case "result":
					e.Result = []ResultValue{ResultSuccess, ResultFailure, ResultReturn}[rng.Intn(3)]
				case "reason":
					e.Reason = texts[rng.Intn(len(texts))]
				case "dependency":
					e.Dependency = ID(fmt.Sprintf("dep%d", i))
				case "landing":
					e.Landing = "land:" + hex(12)
				}
			}
			r.Inputs = append(r.Inputs, e)
		}
	case OpRecordEvidence:
		for i := 0; i < n; i++ {
			e := Evidence{ID: ID(fmt.Sprintf("c%d", i)), Expect: expect(rev(), row(), States()[rng.Intn(len(States()))])}
			if rng.Intn(3) == 0 {
				e.Expect.Revision = ""
			}
			for j := 0; j < 1+rng.Intn(MaxEvidenceRecordsPerCard); j++ {
				kind := []EvidenceKind{KindRead, KindCI, KindSweep, KindLanding}[rng.Intn(4)]
				ds := DispositionsOf(kind)
				e.Records = append(e.Records, Record{Kind: kind, Issuer: fmt.Sprintf("who%d", j), Disposition: ds[rng.Intn(len(ds))], Head: gid(), Def: Digest(hex(64)), Verifier: "v" + hex(3), Artifact: "s/" + hex(4)})
			}
			r.Evidence = append(r.Evidence, e)
		}
	case OpReplace:
		for i := 0; i < n; i++ {
			nw := newAdm(fmt.Sprintf("new%d", i))
			r.Replacements = append(r.Replacements, Replacement{
				Old: Retired{ID: ID(fmt.Sprintf("old%d", i)), Digest: Digest(hex(64)), Expect: expect(rev(), row(), []State{Waiting, Ready}[rng.Intn(2)])},
				New: nw})
		}
	case OpResolve, OpInspect, OpCheck:
		switch rng.Intn(3) {
		case 0:
			r.Scope = &Scope{IDs: idsN(n)}
		case 1:
			r.Scope = &Scope{Rows: []string{row()}}
		default:
			r.Scope = &Scope{All: true}
		}
		if op == OpCheck && rng.Intn(2) == 0 {
			r.Scope = nil
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
	case bool:
		fmt.Fprint(b, t)
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
	case reflect.Pointer:
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

func reqOf(t InputType) []string { r, _, _ := fieldsOf(t); return r }

func allowOf(t InputType) []string { _, a, _ := fieldsOf(t); return a }
