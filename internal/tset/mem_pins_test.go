package tset

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Each test here pins one rule the twin and the codec keep, at the edge where
// another value would be a different rule.

// A step the codec refuses before the twin reads it is refused as the store
// refuses it: the active epoch is the detail of an epoch refusal only.
func TestMemRefusesAMalformedStepWithoutAnActiveEpoch(t *testing.T) {
	t.Parallel()
	m := memPlanFixture(t)
	for _, c := range []struct {
		name string
		step Step
		code string
	}{
		{"an unknown kind", Step{Epoch: "0", Space: "mem-plan:", Entries: []Entry{{Kind: "nonesuch", Table: "work"}}}, "REQUEST"},
		{"no entry", Step{Epoch: "0", Space: "mem-plan:"}, "REQUEST"},
	} {
		_, err := m.Step(context.Background(), c.step)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != c.code || refusal.Detail.ActiveEpoch != "" {
			t.Errorf("%s: %v, want %s with no active epoch", c.name, err, c.code)
		}
		if _, refusal := m.Plan(c.step); refusal == nil || refusal.Code != c.code || refusal.Detail.ActiveEpoch != "" {
			t.Errorf("%s: plan refusal %+v, want %s with no active epoch", c.name, refusal, c.code)
		}
	}
}

// An epoch to read is a decimal: 0, or digits with no leading zero that fit 64
// bits.
func TestValidReadEpochIsADecimalThatFitsSixtyFourBits(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    Decimal
		want bool
	}{
		{"0", true}, {"1", true}, {"10", true}, {"9999999999999999999", true},
		{"18446744073709551615", true},
		{"", false}, {"00", false}, {"01", false}, {"-1", false}, {"1a", false},
		{"18446744073709551616", false}, {"100000000000000000000", false}, {"99999999999999999999", false},
	} {
		if got := validReadEpoch(c.d); got != c.want {
			t.Errorf("validReadEpoch(%q) = %v, want %v", c.d, got, c.want)
		}
	}
}

// A rows entry emits one log line when it added a row, deleted one or both,
// and none when it did neither.
func TestLogLinesOfARowsEntryNeedOneAddedOrDeletedRow(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		entry   MemPlanEntry
		wantDel bool
		want    int
	}{
		{"a row added", MemPlanEntry{Added: []RowRank{{Row: "r1", Rank: "1"}}}, false, 1},
		{"a row deleted", MemPlanEntry{Deleted: []string{"r1"}}, true, 1},
		{"a row added and one deleted", MemPlanEntry{Added: []RowRank{{Row: "r2", Rank: "2"}}, Deleted: []string{"r1"}}, true, 1},
		{"neither", MemPlanEntry{}, false, 0},
	} {
		c.entry.Entry = Entry{Kind: "rows", Table: "work"}
		lines, refusal := LogLines(LogLinesInput{NowMS: "1", Entries: []MemPlanEntry{c.entry}})
		if refusal != nil || len(lines) != c.want {
			t.Fatalf("%s: %d lines (%v), want %d", c.name, len(lines), refusal, c.want)
		}
		if c.want == 1 && strings.Contains(lines[0].D, `"del"`) != c.wantDel {
			t.Errorf("%s: line %s, want del %v", c.name, lines[0].D, c.wantDel)
		}
	}
}

// A range over a table names the cell it ranges, and one over a key names
// neither: a range with a table and no cell is refused.
func TestARangeQueryNamesItsCellOrItsKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		q    ReadQuery
		ok   bool
	}{
		{"a cell of a table", ReadQuery{Kind: "range", Table: "work", Cell: "r:c", Min: "-inf", Max: "+inf", Limit: 1}, true},
		{"a key", ReadQuery{Kind: "range", Key: "read:key", Min: "-inf", Max: "+inf", Limit: 1}, true},
		{"a table and no cell", ReadQuery{Kind: "range", Table: "work", Min: "-inf", Max: "+inf", Limit: 1}, false},
		{"a cell and no table", ReadQuery{Kind: "range", Cell: "r:c", Min: "-inf", Max: "+inf", Limit: 1}, false},
		{"neither", ReadQuery{Kind: "range", Min: "-inf", Max: "+inf", Limit: 1}, false},
	} {
		err := ValidateReadPlan(ReadPlan{Space: "read:", Epoch: "0", Queries: []ReadQuery{c.q}})
		var refusal *Refusal
		if c.ok && err != nil || !c.ok && (!errors.As(err, &refusal) || refusal.Code != "REQUEST") {
			t.Errorf("%s: %v, want ok %v", c.name, err, c.ok)
		}
	}
}

// A cell the last member left is not kept: the table holds no empty cell, for a
// member removed and for one moved away.
func TestMemKeepsNoCellTheLastMemberLeft(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		leave Entry
		kept  string
	}{
		{"removed", Entry{Kind: "remove", Table: "work", From: "r:c", IDs: []string{"p"}, Revs: []Decimal{"1"}}, ""},
		{"moved to another cell", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:d", IDs: []string{"p"}, Revs: []Decimal{"1"}}, "d"},
	} {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"},
		}}})
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{c.leave}})
		cells := s7Table(t, m).Cells
		want := map[string]map[string]map[string]string{}
		if c.kept != "" {
			want["r"] = map[string]map[string]string{c.kept: {"p": "1"}}
		}
		if !reflect.DeepEqual(cells, want) {
			t.Errorf("%s: cells %v, want %v", c.name, cells, want)
		}
	}
}

// An advance plans one command for each table's definition, beside the epoch
// marker's, so the planned counts of a step grow by one command and by the
// bytes of the definition for each table a namespace holds.
func TestMemAdvancePlansEachTablesDefinition(t *testing.T) {
	t.Parallel()
	planned := func(tables ...string) (int64, int64) {
		t.Helper()
		m := NewMem()
		const space = "adv:"
		for _, table := range tables {
			if err := m.DefineTable(space, table, TableDefinition{Columns: []string{"c"}, MemberPrefix: space + "member:" + table + ":",
				EpochKey: space + "sprint:epoch", EpochField: "n"}); err != nil {
				t.Fatal(err)
			}
		}
		op, intent := "clear-0", "clear intent"
		plan, refusal := m.Plan(Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Entries: []Entry{{Kind: "advance", AdvanceFrom: "0"}}})
		if refusal != nil || plan == nil {
			t.Fatalf("plan of an advance over %v: %+v", tables, refusal)
		}
		return plan.PlannedCommands, plan.PlannedArgvBytes
	}
	c1, b1 := planned("work")
	c2, b2 := planned("work", "merge")
	if c2-c1 != 1 || b2 <= b1 {
		t.Fatalf("one table plans %d commands and %d bytes, two plan %d and %d: want one more command and more bytes", c1, b1, c2, b2)
	}
}
