package sprintfn

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The probes of the second cold read that the first tests let through: the
// bound of a card's needs and read cards in the Lua (64 and 15, each held at
// the bound and one past it), the note check of `jnote`, and the cost each
// follow declares (held tight, to exactly what it makes).

// nameList is n names with a prefix, p0 to p<n-1>.
func nameList(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return out
}

// boundWorld is the standard world and four cards that name the most a card
// may (64 needs, 15 read cards) and one more (65, 16): the needs and read cards
// have no records, which a follow reads as absent.
func boundWorld(t *testing.T) *qworld {
	t.Helper()
	w := standard(t)
	wk := func(id, score string, kv ...string) card {
		return card{sprint.Work, "s1", "waiting", id, score, fields(append([]string{"kind", "work", "open", "1"}, kv...)...)}
	}
	w.cards(
		wk("b64", "101", "needs", join(nameList("nd", 64))),
		wk("b65", "102", "needs", join(nameList("nd", 65))),
		wk("c15", "103", "rcards", join(nameList("rd", 15))),
		wk("c16", "104", "rcards", join(nameList("rd", 16))),
	)
	return w
}

// TestNeedsAndReadCardsAreHeldAtTheirBound: a card names at most 64 needs and
// 15 read cards (1.0, 1.3.1). The twin and the Lua kinds read a card at the bound and
// refuse a card one past it DRIFT, naming the card, in `related` (the needs
// and rcards follows) and in `needchain`; moving the Lua's bound by one, in
// either direction, is an answer or a refusal the twin does not give.
func TestNeedsAndReadCardsAreHeldAtTheirBound(t *testing.T) {
	t.Parallel()
	w := boundWorld(t)
	h := newLuaHarness(t, w)
	related := func(id, follow string) sprint.SprintQ {
		return sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(id), Fields: []string{}, Follow: []string{follow}}
	}
	chain := func(id string) sprint.SprintQ {
		return sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids(id), Limit: 100, Fields: []string{}}
	}
	for _, c := range []struct {
		name string
		q    sprint.SprintQ
		card string // the card refused DRIFT, or "" for an answer
	}{
		{"64 needs, followed", related("b64", sprint.FollowNeeds), ""},
		{"65 needs, followed", related("b65", sprint.FollowNeeds), "b65"},
		{"15 read cards, followed", related("c15", sprint.FollowRCards), ""},
		{"16 read cards, followed", related("c16", sprint.FollowRCards), "c16"},
		{"64 needs, chained", chain("b64"), ""},
		{"65 needs, chained", chain("b65"), "b65"},
	} {
		_, _, err := w.tw.QueryFull(c.q)
		ref, refused := err.(*Refusal)
		switch {
		case c.card == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.card != "" && (!refused || ref.Code != codeDrift || len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != c.card || ref.Detail.Table != sprint.Work):
			t.Errorf("%s: %v, want DRIFT naming %s", c.name, err, c.card)
		}
		h.agree(c.name, mustEncode(t, c.q))
	}
	if h.answered[sprint.QueryRelated] != 2 || h.refused[sprint.QueryRelated] != 2 ||
		h.answered[sprint.QueryNeedchain] != 1 || h.refused[sprint.QueryNeedchain] != 1 {
		t.Fatalf("the Lua answered %v and refused %v", h.answered, h.refused)
	}
}

// TestJnoteReadsOnlyNoteLines: a note is read from its line by seq, and the
// line must be a note's (1.3.4): an entry line whose meta happens to hold a
// type and a cause is no note, and naming its seq is DRIFT in the twin and the
// Lua alike, never an answer with the type and cause of an entry.
func TestJnoteReadsOnlyNoteLines(t *testing.T) {
	t.Parallel()
	w := standard(t)
	// An entry line with a meta of the shape a note's has.
	meta := json.RawMessage(`{"type":"blocked","cause":"c1"}`)
	w.step(&Request{Body: Body{Entries: []tset.Entry{{Kind: "create", Table: sprint.Work, To: "s2:waiting",
		IDs: []string{"e1"}, Scores: []string{"40"}, About: []string{"e1"}, Each: []map[string]string{fields("kind", "work", "open", "0")}, Meta: meta}}}})
	entry := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))
	w.note("blocked", "c1", "p1")
	note := lastNote(w)
	h := newLuaHarness(t, w)

	q := sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(entry), Fields: []string{}, Subjects: 4}
	if ref := w.refused(q); ref.Code != codeDrift || len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != entry {
		t.Fatalf("an entry line read as a note: %v", ref)
	}
	h.agree("an entry line with a note's meta", mustEncode(t, q))
	if h.refused[sprint.QueryJnote] != 1 {
		t.Fatalf("the Lua did not refuse it: %v %v", h.answered, h.refused)
	}
	// The line of the note itself is read.
	q.Source = ids(note)
	res, _ := w.query(q)
	if n := res.(JnoteResult).Items[0]; n.Type != "blocked" || n.Cause != "c1" || len(n.Subjects) != 1 {
		t.Fatalf("%+v", n)
	}
	h.agree("the note", mustEncode(t, q))
	if h.answered[sprint.QueryJnote] != 1 {
		t.Fatalf("%v %v", h.answered, h.refused)
	}
}

// TestEachFollowIsHeldToTheCostItDeclares: for each follow a query whose
// follow makes every probe it declares, and reads every record it declares, is
// held to exactly that: the probes made equal the probes declared
// (sprintfn.QueryProbes and the Lua's Q.declared_probes) and the records read
// equal sprint.QueryCost's. A follow that declares less than it makes (an
// `index` that says one probe and makes two) is over its declared cost here,
// whatever slack the other follows of a bigger query carry, and one that
// declares more is not tight.
func TestEachFollowIsHeldToTheCostItDeclares(t *testing.T) {
	t.Parallel()
	w := boundWorld(t)
	h := newLuaHarness(t, w)
	one := func(table, id, follow string) sprint.SprintQ {
		return sprint.SprintQ{Kind: sprint.QueryRelated, Table: table, Source: ids(id), Fields: []string{}, Follow: []string{follow}}
	}
	for _, c := range []struct {
		name string
		q    sprint.SprintQ
		// records and probes the query makes, as the design's cost table counts them
		records, probes int
		exactRecords    bool
	}{
		{"work", one(sprint.Work, "a1", sprint.FollowWork), 2, 2, true},
		{"withdrawn", one(sprint.Work, "x1", sprint.FollowWithdrawn), 2, 2, true},
		{"rcards", one(sprint.Work, "c15", sprint.FollowRCards), 1 + 15, 2, true},
		{"merge", one(sprint.Work, "q1", sprint.FollowMerge), 2, 2, true},
		{"control", one(sprint.Work, "p1", sprint.FollowControl), 2, 2, true},
		{"needs", one(sprint.Work, "b64", sprint.FollowNeeds), 1 + 64, 1 + 1 + 64, true},
		{"member", one(sprint.Fleet, "k1.w1", sprint.FollowMember), 2, 2, true},
		// jopen, due and index read no record: they are probes, and the design's cost
		// table counts each as one record beside them, which a query is not held to
		{"jopen", one(sprint.Work, "p1", sprint.FollowJOpen), 1, 2, false},
		{"due", one(sprint.Fleet, "k1.w1", sprint.FollowDue), 1, 2, false},
		{"index", one(sprint.Work, "p1", sprint.FollowIndex), 1, 1 + 2, false},
		{"index of a ready card", one(sprint.Work, "f1", sprint.FollowIndex), 1, 1 + 2, false},
		{"front, heads with the index and jopen follows", sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{
			{Index: sprint.HeadEligBelow, Limit: 2, Follow: []string{sprint.FollowIndex, sprint.FollowJOpen}}}}, 3, 8 + 1 + 1 + 2*3, false},
		{"waiters", sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 2, Fields: []string{}}, 1 + 2, 4, true},
	} {
		_, charge, err := w.tw.QueryFull(c.q)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		declared, cost := QueryProbes(c.q), sprint.QueryCost(c.q)
		if charge.Probes != c.probes || declared != c.probes {
			t.Errorf("%s: made %d probes and declared %d, want both %d", c.name, charge.Probes, declared, c.probes)
		}
		if charge.Records != c.records || (c.exactRecords && cost.Records != c.records) || charge.Records > cost.Records {
			t.Errorf("%s: read %d records, declared %d, want %d", c.name, charge.Records, cost.Records, c.records)
		}
		// The Lua makes the probes it declares, and reads the records it declares.
		enc := mustEncode(t, c.q)
		lrecords, _, lprobes := h.declared(enc)
		h.agree(c.name, enc)
		if h.charge.Probes != c.probes || lprobes != c.probes {
			t.Errorf("%s: the Lua made %d probes and declared %d, want both %d", c.name, h.charge.Probes, lprobes, c.probes)
		}
		if h.charge.Records != c.records || (c.exactRecords && lrecords != c.records) || h.charge.Records > lrecords {
			t.Errorf("%s: the Lua read %d records and declared %d, want %d", c.name, h.charge.Records, lrecords, c.records)
		}
	}
	if h.answered[sprint.QueryRelated]+h.answered[sprint.QueryFront]+h.answered[sprint.QueryWaiters] != 13 {
		t.Fatalf("the Lua answered %v and refused %v", h.answered, h.refused)
	}
}
