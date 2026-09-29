package request

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// pad returns s padded with x to exactly n bytes.
func pad(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat("x", n-len(s))
}

// worstAdmission is an admission with every field at its bound; the free text
// fields are made of the two characters the canonical form escapes.
func worstAdmission(i int) Admission {
	deps := make([]ID, card.MaxDependsOn)
	for j := range deps {
		deps[j] = ID(pad(fmt.Sprintf("d%04d-%d-", i, j), card.MaxIDBytes))
	}
	return Admission{
		ID: ID(pad(fmt.Sprintf("c%04d-", i), card.MaxIDBytes)), Digest: Digest(dig), ObjectID: g64, Commit: g64,
		Repository: card.Repository("h.example/" + strings.Repeat("r", card.MaxRepositoryBytes-len("h.example/"))),
		Path:       strings.Repeat("p", card.MaxPathBytes), Kind: pad("fix-", 32), DependsOn: deps,
		Entry: strings.Repeat(`"`, card.MaxEntryBytes), Title: strings.Repeat(`"`, card.MaxTitleBytes),
		Row: strings.Repeat("r", card.MaxNameBytes), PolicyVersion: "18446744073709551615", PolicyDigest: Digest(dig2),
	}
}

func worstEnvelope(op Operation) *Request {
	r := envelope(op)
	r.Table = strings.Repeat("t", card.MaxNameBytes)
	r.Epoch, r.TableRevision = "18446744073709551615", "18446744073709551615"
	r.OperationID, r.Actor = strings.Repeat("o", card.MaxOperationIDBytes), strings.Repeat("a", card.MaxIdentityBytes)
	return r
}

func worstExpect(col State) Expect {
	return Expect{Revision: "18446744073709551615", Place: Place{Row: strings.Repeat("r", card.MaxNameBytes), Col: col}}
}

func worstRecord(i, j int) Record {
	return Record{Kind: KindLanding, Issuer: pad(fmt.Sprintf("i%d-%d", i, j), card.MaxIdentityBytes), Disposition: DispLanded,
		Head: Digest(dig).Tagged(), Def: Digest(dig), Verifier: strings.Repeat("v", MaxVerifierBytes), Artifact: pad(fmt.Sprintf("a%d-%d", i, j), card.MaxRefBytes)}
}

func worstEvidence(cards, records int) []Evidence {
	out := make([]Evidence, cards)
	left := records
	for i := range out {
		out[i] = Evidence{ID: ID(pad(fmt.Sprintf("c%04d-", i), card.MaxIDBytes)), Expect: worstExpect(Review)}
		n := records / cards
		if i < records%cards {
			n++
		}
		left -= n
		for j := 0; j < n; j++ {
			out[i].Records = append(out[i].Records, worstRecord(i, j))
		}
	}
	return out
}

func worstInput(t InputType, i int) Input {
	e := Input{ID: ID(pad(fmt.Sprintf("c%04d-", i), card.MaxIDBytes)), Type: t, Digest: Digest(dig), Issuer: strings.Repeat("i", card.MaxIdentityBytes), Source: strings.Repeat("s", card.MaxRefBytes)}
	src := SourceStates(t)
	e.Expect = worstExpect(src[0])
	for _, f := range reqOf(t) {
		switch f {
		case "head":
			e.Head = Digest(dig).Tagged()
		case "result":
			e.Result = ResultReturn
		case "reason":
			e.Reason = strings.Repeat(`"`, card.MaxReasonBytes)
		case "dependency":
			e.Dependency = ID(pad("dep", card.MaxIDBytes))
		case "landing":
			e.Landing = strings.Repeat("l", card.MaxRefBytes)
		}
	}
	for _, f := range allowOf(t) {
		if f == "head" {
			e.Head = Digest(dig).Tagged()
		}
	}
	return e
}

// The worst-case request of each operation is valid, has the entry counts the
// table takes (127 changed entries and the card operation record are 128; the
// guard-only entries are at most 1,024), fits a canonical request, and its
// manifest, by the design's per-entry sizes, fits 1 MiB.
func TestWorstCaseRequestOfEachOperationFitsOneTableManifest(t *testing.T) {
	t.Parallel()
	report := map[string]int{}
	check := func(name string, r *Request, changed, guards, manifestMax int) *Valid {
		t.Helper()
		v, err := Validate(r)
		if err != nil {
			t.Fatalf("%s: the worst-case request is refused: %v", name, err)
		}
		canon := v.Canonical()
		if len(canon) > MaxCanonicalBytes {
			t.Errorf("%s: canonical %d bytes exceeds %d", name, len(canon), MaxCanonicalBytes)
		}
		if changed+card.CardOperationRecordEntries > card.TableChangedEntries {
			t.Errorf("%s: %d changed entries and the operation record exceed the table's %d", name, changed, card.TableChangedEntries)
		}
		if guards > card.TableGuardOnlyEntries {
			t.Errorf("%s: %d guard-only entries exceed the table's %d", name, guards, card.TableGuardOnlyEntries)
		}
		if manifestMax > card.TableManifestBytes {
			t.Errorf("%s: worst manifest %d exceeds the table's %d", name, manifestMax, card.TableManifestBytes)
		}
		report[name] = len(canon)
		return v
	}

	// admit: 127 admissions of 8 distinct outside dependencies each
	r := worstEnvelope(OpAdmit)
	for i := 0; i < MaxChangedEntries; i++ {
		r.Admissions = append(r.Admissions, worstAdmission(i))
	}
	v := check("admit", r, MaxChangedEntries, outsideDeps(v0(t, r)), card.ManifestAdmitMax)
	if got := outsideDeps(v); got != MaxChangedEntries*MaxDependsOn || got > MaxOutsideDependencies {
		t.Errorf("admit: %d outside dependencies, want %d within %d", got, MaxChangedEntries*MaxDependsOn, MaxOutsideDependencies)
	}

	// apply: 127 lifecycle inputs, every type at its maximum fields in turn
	r = worstEnvelope(OpApplyEvents)
	types := InputTypes()
	for i := 0; i < MaxChangedEntries; i++ {
		r.Inputs = append(r.Inputs, worstInput(types[i%len(types)], i))
	}
	check("inputs", r, MaxChangedEntries, 1, card.ManifestInputsMax)

	// evidence: 127 cards and 512 records in all, every record at its maximum
	r = worstEnvelope(OpRecordEvidence)
	r.Evidence = worstEvidence(MaxChangedEntries, MaxEvidenceRecords)
	check("evidence", r, MaxChangedEntries, 0, card.ManifestEvidenceMax)
	// The escaping factor of an evidence request: every record field is a token
	// (letters, digits and . : / @ + _ -), which holds no quote or backslash, so
	// escaping the canonical request as a string adds nothing but its own quotes.
	// the request's own quotes are its JSON structure; inside the records there is
	// none, so a record line escaped as a string is exactly its own length: the
	// factor is 1.
	for _, e := range r.Evidence {
		for _, rc := range e.Records {
			if card.EscapedSize([]byte(rc.Line())) != len(rc.Line()) {
				t.Fatalf("a record line changes size when escaped: %s", rc.Line())
			}
			if strings.ContainsAny(rc.Line(), `"\`) {
				t.Fatalf("a record line holds a character that escapes: %s", rc.Line())
			}
			if len(rc.Line()) > MaxRecordBytes {
				t.Fatalf("a record line of %d bytes exceeds %d", len(rc.Line()), MaxRecordBytes)
			}
		}
	}
	// one card at the per-card bound
	one := worstEnvelope(OpRecordEvidence)
	one.Evidence = worstEvidence(1, MaxEvidenceRecordsPerCard)
	check("evidence-one-card", one, 1, 0, card.ManifestEvidenceMax)

	// replace: 63 pairs are 126 members, and the record is the 127th changed... the 128th entry
	r = worstEnvelope(OpReplace)
	for i := 0; i < MaxReplacementPairs; i++ {
		nw := worstAdmission(i)
		nw.ID = ID(pad(fmt.Sprintf("n%04d-", i), card.MaxIDBytes))
		r.Replacements = append(r.Replacements, Replacement{Old: Retired{ID: ID(pad(fmt.Sprintf("o%04d-", i), card.MaxIDBytes)), Digest: Digest(dig), Expect: worstExpect(Ready)}, New: nw})
	}
	vr := check("replace", r, 2*MaxReplacementPairs, len(vr0(t, r)), card.ManifestReplaceMax)
	if len(vr.Cards()) != 2*MaxReplacementPairs+MaxReplacementPairs*MaxDependsOn {
		t.Errorf("replace: %d cards enumerated", len(vr.Cards()))
	}

	// resolve: 113 cards by ID, each with 8 dependencies outside the scope
	r = worstEnvelope(OpResolve)
	r.Scope = &Scope{IDs: idsN(MaxResolveCards)}
	check("resolve", r, MaxResolveCards, MaxResolveCards*(1+MaxDependsOn), card.ManifestResolveMax)

	// inspect: 1,024 cards by ID (a read, no manifest)
	in := envelope(OpInspect)
	in.Scope = &Scope{IDs: idsN(MaxScopeCards)}
	check("inspect", in, 0, MaxScopeCards, 0)

	for name, n := range report {
		t.Logf("worst-case canonical %-18s %8d bytes (limit %d)", name, n, MaxCanonicalBytes)
	}
}

func v0(t *testing.T, r *Request) *Valid {
	t.Helper()
	v, err := Validate(r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func vr0(t *testing.T, r *Request) []CardRef {
	t.Helper()
	var out []CardRef
	for _, c := range v0(t, r).Cards() {
		if c.Role == RoleDependency {
			out = append(out, c)
		}
	}
	return out
}

func outsideDeps(v *Valid) int {
	n := 0
	for _, c := range v.Cards() {
		if c.Role == RoleDependency {
			n++
		}
	}
	return n
}

// Each request bound is within the bound of the field of the manager design's
// size table (its per-entry maxima assume these), so its per-entry sums hold.
func TestFieldBoundsAreWithinTheManagerDesign(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		ours, theirs int
	}{
		{"card ID", card.MaxIDBytes, 64},
		{"operation ID", card.MaxOperationIDBytes, 128},
		{"actor and issuer", card.MaxIdentityBytes, 64},
		{"repository identity", card.MaxRepositoryBytes, 256},
		{"path", card.MaxPathBytes, 512},
		{"entry", card.MaxEntryBytes, 256},
		{"kind", 32, 32},
		{"artifact, source and landing", card.MaxRefBytes, 256},
		{"verifier", MaxVerifierBytes, 32},
		{"reason", card.MaxReasonBytes, 512},
		{"head", MaxHeadBytes, 71},
		{"record line", MaxRecordBytes, 519},
		{"dependencies of a card, in bytes", MaxDependsOn*card.MaxIDBytes + MaxDependsOn - 1, 520},
		{"dependencies of a card, in cards", MaxDependsOn, 8},
	} {
		if c.ours > c.theirs {
			t.Errorf("%s: %d over the design's %d", c.name, c.ours, c.theirs)
		}
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"admissions", MaxChangedEntries, 127}, {"replacement pairs", MaxReplacementPairs, 63}, {"lifecycle inputs", MaxChangedEntries, 127},
		{"evidence cards", MaxChangedEntries, 127}, {"records per card", MaxEvidenceRecordsPerCard, 16}, {"records in all", MaxEvidenceRecords, 512},
		{"resolve cards", MaxResolveCards, 113},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, the design says %d", c.name, c.got, c.want)
		}
	}
}

// One over each bound refuses the whole request, with the limit and a remedy.
func TestOneOverEachCountBoundRefusesTheWholeRequest(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func() *Request{
		"admissions": func() *Request {
			r := envelope(OpAdmit)
			r.Admissions = manyAdmissions(MaxChangedEntries + 1)
			return r
		},
		"inputs": func() *Request {
			r := envelope(OpApplyEvents)
			for i := 0; i <= MaxChangedEntries; i++ {
				r.Inputs = append(r.Inputs, input(InStart, fmt.Sprintf("c%d", i)))
			}
			return r
		},
		"evidence": func() *Request {
			r := envelope(OpRecordEvidence)
			r.Evidence = evidenceCards(MaxChangedEntries+1, 1)
			return r
		},
		"replacements": func() *Request {
			r := envelope(OpReplace)
			r.Replacements = manyReplacements(MaxReplacementPairs + 1)
			return r
		},
	} {
		_, err := Validate(mk())
		rs, ok := err.(*Refusals)
		if !ok || !rs.Has(-1, name, CauseTooMany) || rs.List[0].Limit == "" || rs.List[0].Next == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
}
