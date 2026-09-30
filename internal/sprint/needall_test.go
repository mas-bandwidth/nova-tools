package sprint

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// needAll is the guard of Table.Cards, a scan of every card of a table, and it
// answers only on the work table loaded whole: its rows read, and every cell of
// every row (a row's cell in each state) read whole. The loop that decides it
// walks every row and every state. A loop that stopped at the first row, or at
// the first state, or read one cell as the whole, would let a scan answer over a
// table of which a plan read only part, and hand a rule the cards that happen to
// be known as if they were the table. The existing scan tests read one row, so
// they cannot tell a loop over every row from a loop over the first.

// scanWorld is a work table of three streams with a card in every one of the six
// cells of each, so that a card that goes missing from a scan names its row and
// state.
func scanWorld() (*Snapshot, []string) {
	streams := []string{"s1", "s2", "s3"}
	var cards []*Card
	var want []string
	for _, row := range streams {
		for i, st := range States {
			id := fmt.Sprintf("%s-%s", row, st)
			cards = append(cards, pcard(id, row, string(st), float64(i+1)))
			want = append(want, id)
		}
	}
	return wholeSnapshot(streams, nil, cards...), sortedStrings(want)
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// scanPlan reads the rows of the streams and each of the six cells of every one
// whole, in the plan's order: cells[i] is the range of row i's state j at
// i*len(States)+j.
func scanPlan(world *Snapshot) (ReadPlan, ReadAnswer) {
	rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryStreams}}}
	for _, row := range world.Work.Rows() {
		for _, st := range States {
			rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: row + ":" + string(st), Limit: MaxRangeLimit, Records: true})
		}
	}
	return rp, wholeStore{world}.Answer(rp)
}

func TestNeedAllWalksEveryRowThroughCards(t *testing.T) {
	t.Parallel()
	world, want := scanWorld()
	rows := world.Work.Rows()
	if len(rows) != 3 || len(want) != 18 {
		t.Fatalf("the world has %d rows and %d cards", len(rows), len(want))
	}

	// Every cell of every row read whole: the scan answers, with every card of
	// every row and no other, and the read is not put in the log.
	rp, ans := scanPlan(world)
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(s.Work.Cards()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Cards of a work table read whole, over %d rows: %v, want %v", len(rows), got, want)
	}
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("a scan of a table loaded whole was refused: %v", err)
	}

	// A table built whole is scanned without a plan.
	if got := ids(world.Work.Cards()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Cards of a table built whole: %v", got)
	}

	// Any one cell of any one row not read whole refuses the scan, whichever row
	// and whichever state it is: the loop visits every one of the eighteen. In a
	// test build the refusal is a panic naming the table, and in a release build
	// the result is empty and the read is in the log, once, while LoadedCards still
	// gives the cards that came.
	for ri, row := range rows {
		for si, st := range States {
			name := row + ":" + string(st)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				rp, ans := scanPlan(world)
				i := ri*len(States) + si
				sl := rp.TsetSlots()[i]
				if sl.Kind != AnswerRange || rp.Ranges[sl.Index].Cell != name {
					t.Fatalf("slot %d is %+v, not the range of %s", i, sl, name)
				}
				ans.Tset[i].HasMore = true // the cell has more members than it returned
				strict, err := loadPartial(rp, ans, true)
				if err != nil {
					t.Fatal(err)
				}
				if msg := mustPanic(t, func() { strict.Work.Cards() }); !strings.Contains(msg, unloadedMessage+": work cards") {
					t.Errorf("Cards with %s not read whole, in a test build: %q", name, msg)
				}
				loose, err := loadPartial(rp, ans, false)
				if err != nil {
					t.Fatal(err)
				}
				if got := loose.Work.Cards(); got != nil {
					t.Errorf("Cards with %s not read whole gave %v, not none", name, ids(got))
				}
				if reads := loose.Unloaded(); len(reads) != 1 || reads[0] != unloadedMessage+": work cards" {
					t.Errorf("Cards with %s not read whole: the log is %q", name, reads)
				}
				if got := len(loose.Work.LoadedCards()); got == 0 {
					t.Errorf("LoadedCards with %s not read whole gave none of what came", name)
				}
			})
		}
	}

	// A row whose cells the plan did not read at all, though the table lists it
	// (the stream query gave three rows, the ranges name two): the table holds
	// rows that Cards would not visit if the loop read the rows the ranges name,
	// and it is refused.
	for drop := range rows {
		rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryStreams}}}
		for ri, row := range rows {
			if ri == drop {
				continue
			}
			for _, st := range States {
				rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: row + ":" + string(st), Limit: MaxRangeLimit, Records: true})
			}
		}
		loose, err := loadPartial(rp, wholeStore{world}.Answer(rp), false)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(loose.Work.Rows()); got != len(rows) {
			t.Fatalf("the table lists %d rows, not %d", got, len(rows))
		}
		if got := loose.Work.Cards(); got != nil {
			t.Errorf("Cards with the cells of %s never read gave %v, not none", rows[drop], ids(got))
		}
		if reads := loose.Unloaded(); len(reads) != 1 || reads[0] != unloadedMessage+": work cards" {
			t.Errorf("Cards with the cells of %s never read: the log is %q", rows[drop], reads)
		}
	}

	// The rows themselves not read: no row is known, so no cell of one can be
	// whole, and the scan is refused whatever ranges were read.
	rp2, ans2 := scanPlan(world)
	rp2.Sprint, ans2.Sprint = nil, nil
	noRows, err := loadPartial(rp2, ans2, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := noRows.Work.Cards(); got != nil {
		t.Errorf("Cards with the rows not read gave %v", ids(got))
	}
	if reads := noRows.Unloaded(); len(reads) != 1 || reads[0] != unloadedMessage+": work cards" {
		t.Errorf("Cards with the rows not read: the log is %q", reads)
	}
}
