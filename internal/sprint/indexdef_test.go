package sprint

import (
	"reflect"
	"strings"
	"testing"
)

// The definition, on a population built by hand, against what each definition
// of section 1.3.1 says by the letter. This is what the property tests hold
// IndexOps to, so it is held to something itself.
func TestIndexDefinitionOfAPopulationBuiltByHand(t *testing.T) {
	t.Parallel()
	cards := []*IndexCard{
		// Stream s1.
		iw("p1", "s1", Waiting, 10, "kind", "primary"),                                // free to go
		iw("p2", "s1", Waiting, 20, "kind", "primary", "needs", "p1", "open", "1"),    // counts p1, which is open
		iw("p3", "s1", Waiting, 25, "kind", "primary", "needs", "p8"),                 // p8 has landed: not counted
		iw("p12", "s1", Waiting, 27, "kind", "primary", "needs", "gone", "open", "1"), // a need with no record
		iw("p14", "s1", Waiting, 28, "kind", "primary", "refused", "resolve: x"),      // free, and refused
		iw("g1", "s1", Waiting, 50, "kind", "sentinel", "needs", "p4", "open", "1"),   // a sentinel counting p4, which is ready
		iw("p4", "s1", Ready, 30, "kind", "primary", "attempt", "0"),                  // never dealt
		iw("p5", "s1", Ready, 40, "kind", "primary", "attempt", "1"),                  // dealt before
		iw("p6", "s1", Ready, 45, "kind", "primary", "attempt", "3", "bound", "3"),    // at its bound
		iw("p7", "s1", Ready, 46, "kind", "primary", "attempt", "0", "refused", "deal: no room"),
		iw("p13", "s1", Ready, 47, "kind", "primary", "attempt", "2", "refused", "deal: no room"),
		iw("p8", "s1", Landed, 5, "kind", "primary", "attempt", "1"),
		iw("p9", "s1", Working, 60, "kind", "primary", "attempt", "1"),
		iw("p10", "s1", Review, 61, "kind", "primary", "attempt", "1"),
		iw("p11", "s1", Merging, 62, "kind", "primary", "attempt", "1"),
		ixUnplaced(iw("p15", "s1", Waiting, 63, "kind", "primary")), // dropped: a record with no place
		// Stream s2.
		iw("p20", "s2", Waiting, 15, "kind", "primary", "needs", "p9", "open", "0"), // p9 is open (working), so it is counted
		iw("p21", "s2", Ready, 41, "kind", "primary", "attempt", "2"),
		// The fleet.
		ifl("p9.w1", "m1", Ready, "kind", "work", "due_untaken", "900"),
		ifl("p9.w2", "m1", Working, "kind", "work", "due_unfinished", "1200"),
		ifl("p9.w3", "m2", Ready, "kind", "work"),                           // no due field
		ifl("p9.w4", "m2", Withdrawn, "kind", "work", "due_untaken", "900"), // not in the state
		ifl("p9.w5", "m2", DoneOK, "kind", "work", "due_unfinished", "1200"),
		// The readers.
		ird("p10.r1.ra", "ra", Asked, "kind", "read", "due_unbegun", "600"),
		ird("p10.r1.rb", "rb", Reading, "kind", "read", "due_unreported", "1500"),
		ird("p10.r1.rc", "rc", OK, "kind", "read", "due_unbegun", "600"),
		// The streams' control cards.
		ictl("s1", "due_mergeidle", "1800"),
		ictl("s2"),
		nil,
	}
	got, err := IndexDefinition(cards)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"again:s1 p5@40",
		"again:s2 p21@41",
		"due mergeidle:s1@1800",
		"due unbegun:p10.r1.ra@600",
		"due unfinished:p9.w2@1200",
		"due unreported:p10.r1.rb@1500",
		"due untaken:p9.w1@900",
		"elig:s1 p1@10",
		"elig:s1 p3@25",
		"elig:s2 p20@15",
		"fresh:s1 p4@30",
		"sent:s1 g1@50",
		"wait:p1 p2",
		"wait:p4 g1",
		"wait:p9 p20",
	}
	if !reflect.DeepEqual(got.Lines(), want) {
		t.Errorf("IndexDefinition:\n got  %q\n want %q", got.Lines(), want)
	}
}

func TestIndexDefinitionRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		what string
		card *IndexCard
		want string
	}{
		{"open on a waiting card", iw("p1", "s1", Waiting, 1, "open", "two"), "field open is \"two\""},
		{"attempt on a ready card", iw("p1", "s1", Ready, 1, "attempt", "1.5"), "field attempt is \"1.5\""},
		{"a due field, fleet", ifl("w", "m1", Ready, "due_untaken", "soon"), "field due_untaken is \"soon\""},
		{"a due field, working", ifl("w", "m1", Working, "due_unfinished", "x"), "field due_unfinished is \"x\""},
		{"a due field, asked", ird("r", "ra", Asked, "due_unbegun", "x"), "field due_unbegun is \"x\""},
		{"a due field, reading", ird("r", "ra", Reading, "due_unreported", "x"), "field due_unreported is \"x\""},
		{"a due field, control", ictl("s1", "due_mergeidle", "x"), "field due_mergeidle is \"x\""},
		{"a waiting card placed with no row", iw("p1", "", Waiting, 10, "kind", "primary"), "card p1 is placed at waiting with no row"},
		{"a control card with its deadline and no row", ictl("", "due_mergeidle", "5"), "is placed at ctl with no row"},
	} {
		got, err := IndexDefinition([]*IndexCard{row.card})
		if err == nil || !strings.Contains(err.Error(), row.want) {
			t.Errorf("%s: error %v, want one naming %q", row.what, err, row.want)
		}
		if got != nil {
			t.Errorf("%s: indexes %v returned beside the refusal", row.what, got)
		}
	}
	// What is not read is not refused: the same fields where no definition reads them.
	if got, err := IndexDefinition([]*IndexCard{iw("p1", "s1", Review, 1, "open", "two", "attempt", "x"), ifl("w", "m1", Withdrawn, "due_untaken", "x")}); err != nil || len(got) != 0 {
		t.Errorf("fields no definition reads: %v, %v", got, err)
	}
}

func TestAWaitIsCountedOnlyWhileTheNeedIsOpen(t *testing.T) {
	t.Parallel()
	waiter := iw("w", "s1", Waiting, 9, "kind", "primary", "needs", "n", "open", "1")
	for _, row := range []struct {
		what string
		need *IndexCard
		want []string
	}{
		{"open, waiting", iw("n", "s1", Waiting, 1, "kind", "primary"), []string{"wait:n w"}},
		{"open, working", iw("n", "s1", Working, 1, "kind", "primary"), []string{"wait:n w"}},
		{"open, in another stream", iw("n", "s2", Merging, 1, "kind", "primary"), []string{"wait:n w"}},
		{"landed", iw("n", "s1", Landed, 1, "kind", "primary"), nil},
		{"dropped, the record kept", ixUnplaced(iw("n", "s1", Waiting, 1, "kind", "primary")), nil},
		{"no record", nil, nil},
		{"a card of another table with its id", ifl("n", "m1", Ready), nil},
	} {
		x, err := IndexDefinition([]*IndexCard{waiter, row.need})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, l := range x.Lines() {
			if strings.HasPrefix(l, "wait:") {
				got = append(got, l)
			}
		}
		if !reflect.DeepEqual(got, row.want) {
			t.Errorf("need %s: %q, want %q", row.what, got, row.want)
		}
	}
	// A card that is not waiting counts nothing, whatever it names.
	x, _ := IndexDefinition([]*IndexCard{ixAt(waiter, Ready), iw("n", "s1", Waiting, 1, "kind", "primary")})
	for _, l := range x.Lines() {
		if strings.HasPrefix(l, "wait:") {
			t.Errorf("a ready card is counted: %s", l)
		}
	}
}

func TestIndexesApplyAndCompare(t *testing.T) {
	t.Parallel()
	x := Indexes{}
	x.Apply([]IndexOp{
		{Key: IndexKey{IndexElig, "s1"}, Add: []Scored{{"p1", 10}, {"p2", 20}}},
		{Key: IndexKey{IndexWait, "n"}, Add: []Scored{{"w", 0}}},
	})
	wantLines(t, "added", x.Lines(), "elig:s1 p1@10", "elig:s1 p2@20", "wait:n w")
	x.Apply([]IndexOp{
		{Key: IndexKey{IndexElig, "s1"}, Rem: []string{"p1", "absent"}, Add: []Scored{{"p2", 25}}},
		{Key: IndexKey{IndexWait, "n"}, Rem: []string{"w"}},
	})
	wantLines(t, "removed and re-scored; a key left empty goes", x.Lines(), "elig:s1 p2@25")
	if _, in := x[IndexKey{IndexWait, "n"}]; in {
		t.Error("a key with no member is still there")
	}

	want := Indexes{IndexKey{IndexElig, "s1"}: {"p2": 20, "p3": 30}, IndexKey{IndexWait, "n"}: {"w": 0}}
	wantLines(t, "diff", x.Diff(want),
		"elig:s1 p2 is scored 25, and 20 in the definition",
		"elig:s1 p3 is in the definition and not in the running index",
		"wait:n w is in the definition and not in the running index")
	wantLines(t, "diff the other way", want.Diff(x),
		"elig:s1 p2 is scored 20, and 25 in the definition",
		"elig:s1 p3 is in the running index and not in the definition",
		"wait:n w is in the running index and not in the definition")
	// The members of wait carry no score: a different score is no difference.
	a := Indexes{IndexKey{IndexWait, "n"}: {"w": 1}}
	b := Indexes{IndexKey{IndexWait, "n"}: {"w": 2}}
	wantLines(t, "wait scores", a.Diff(b))
	if !reflect.DeepEqual(a.Lines(), []string{"wait:n w"}) {
		t.Errorf("wait lines: %q", a.Lines())
	}
	wantLines(t, "equal", x.Diff(x))
}

func TestAnOpPrintsItsMembers(t *testing.T) {
	t.Parallel()
	o := IndexOp{Key: IndexKey{IndexFresh, "s1"}, Rem: []string{"a", "b"}, Add: []Scored{{"c", 1.5}, {"d", 2}}}
	if got, want := o.String(), "fresh:s1 -a -b +c@1.5 +d@2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := (IndexKey{IndexDue, ""}).String(), "due"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
