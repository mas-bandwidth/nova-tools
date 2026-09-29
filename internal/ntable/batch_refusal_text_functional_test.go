//go:build functional

package ntable_test

// Every refusal of a batch and of a read set names the operation, the member
// at fault, the state found against the state expected, changed=no, and a next
// command. The commands run: cmd/nova-table proves it by executing each one.

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func refusalFixtureBatch(rev, op string, m ...ntable.BatchMemberEntry) ntable.BatchManifest {
	return ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: op, Actor: "p", Members: m}
}

func TestBatchRefusalsNameOperationMemberStateAndNextCommand(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	place := func(row, col string) *ntable.MemberExpect {
		return &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: row, Col: col}}
	}
	guard := &ntable.MemberExpect{}
	cases := []struct {
		name     string
		manifest ntable.BatchManifest
		want     []string // every one must appear
		never    []string // none may appear
	}{
		{
			"not a member (move)",
			refusalFixtureBatch(rev, "r-nm", ntable.BatchMemberEntry{ID: "zz", Expect: guard, Move: &ntable.MemberMoveOp{Row: "build", Col: "done"}}),
			[]string{`batch "r-nm"`, `member "zz"`, "expected an existing member", "observed no member record", "changed=no", "; run: nova-table member find 'demo' 'zz'"},
			[]string{"cell members", "''"},
		},
		{
			"not a member (guard only)",
			refusalFixtureBatch(rev, "r-nm2", ntable.BatchMemberEntry{ID: "zz", Expect: guard}),
			[]string{`member "zz"`, "expected an existing member", "observed no member record", "changed=no", "; run: nova-table member find 'demo' 'zz'"},
			[]string{"cell members"},
		},
		{
			"member exists (create)",
			refusalFixtureBatch(rev, "r-exists", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "done", Score: 1}}),
			[]string{`batch "r-exists"`, `member "a"`, "expected absent", "observed placed at build:ready", "changed=no", "; run: nova-table member find 'demo' 'a'"},
			nil,
		},
		{
			"epoch ahead",
			func() ntable.BatchManifest {
				m := refusalFixtureBatch(rev, "r-ahead", ntable.BatchMemberEntry{ID: "a", Expect: guard})
				m.Epoch = "3"
				return m
			}(),
			[]string{`batch "r-ahead"`, "ahead of the active epoch", "requested epoch 3", "active epoch 0", "changed=no", "; run: nova-table show 'demo'"},
			[]string{"no such table", "create"},
		},
		{
			"failed place guard",
			refusalFixtureBatch(rev, "r-pg", ntable.BatchMemberEntry{ID: "a", Expect: place("test", "done")}),
			[]string{`batch "r-pg"`, "failed place guard", `member "a"`, "expected place test:done", "observed build:ready", "changed=no", "; run: nova-table member find 'demo' 'a'"},
			[]string{"disagree", "drift", "DRIFT"},
		},
		{
			"member revision",
			refusalFixtureBatch(rev, "r-mrev", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Revision: "9"}}),
			[]string{`member "a"`, "expected 9, observed 1", "changed=no", "; run: nova-table member find 'demo' 'a'"},
			nil,
		},
		{
			"table revision",
			refusalFixtureBatch("1", "r-trev", ntable.BatchMemberEntry{ID: "a", Expect: guard}),
			[]string{`batch "r-trev"`, "expected 1, observed " + rev, "changed=no", "; run: nova-table show 'demo'"},
			nil,
		},
		{
			"duplicate member",
			refusalFixtureBatch(rev, "r-twice", ntable.BatchMemberEntry{ID: "a", Expect: guard}, ntable.BatchMemberEntry{ID: "a", Expect: guard}),
			[]string{`member "a"`, "TWICE", "changed=no", "; run: nova-table"},
			nil,
		},
		{
			"field guard",
			refusalFixtureBatch(rev, "r-fg", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Fields: map[string]ntable.FieldGuard{"role": {Absent: boolPtr(true)}}}}),
			[]string{`member "a"`, `field "role"`, "expected absent", `observed "x"`, "changed=no", "; run: nova-table member find 'demo' 'a'"},
			nil,
		},
		{
			"unknown destination row",
			refusalFixtureBatch(rev, "r-row", ntable.BatchMemberEntry{ID: "n", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "nope", Col: "ready", Score: 1}}),
			[]string{`batch "r-row"`, `member "n"`, `row "nope"`, "changed=no", "; run: nova-table row add 'demo' 'nope'"},
			nil,
		},
		{
			"unknown destination column",
			refusalFixtureBatch(rev, "r-col", ntable.BatchMemberEntry{ID: "n", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "nope", Score: 1}}),
			[]string{`batch "r-col"`, `member "n"`, `column "nope"`, "changed=no", "; run: nova-table show 'demo'"},
			nil,
		},
	}
	for _, tc := range cases {
		_, err := ntable.ApplyBatch(ctx, c, tc.manifest)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		got := err.Error()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: refusal lacks %q:\n  %s", tc.name, w, got)
			}
		}
		for _, w := range tc.never {
			if strings.Contains(got, w) {
				t.Errorf("%s: refusal holds %q:\n  %s", tc.name, w, got)
			}
		}
	}
}

func TestBatchOnAMissingTableNamesTheNextCommand(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	m := refusalFixtureBatch("0", "r-none", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}})
	m.Table = "ghost"
	_, err := ntable.ApplyBatch(ctx, c, m)
	if err == nil {
		t.Fatal("accepted")
	}
	for _, w := range []string{`table "ghost" batch "r-none"`, "no such table", "changed=no", "; run: nova-table list"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("refusal lacks %q: %v", w, err)
		}
	}
	if strings.Contains(err.Error(), "<") {
		t.Errorf("the suggested command holds a placeholder: %v", err)
	}
}

func TestReadSetRefusalsNameOperationStateAndNextCommand(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	cases := []struct {
		name  string
		scope ntable.ReadSetScope
		epoch []uint64
		want  []string
		never []string
	}{
		{
			"epoch ahead",
			ntable.ReadSetScope{Members: []string{"a"}}, []uint64{3},
			[]string{`table "demo" read set`, "ahead of the active epoch", "requested epoch 3", "active epoch 0", "changed=no", "; run: nova-table show 'demo'"},
			[]string{"no such table", "create"},
		},
		{
			"unknown row",
			ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "nope", Col: "ready"}}}, nil,
			[]string{`table "demo" read set`, `row "nope"`, "changed=no", "; run: nova-table show 'demo'"},
			nil,
		},
		{
			"unknown column",
			ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "build", Col: "nope"}}}, nil,
			[]string{`table "demo" read set`, `column "nope"`, "changed=no", "; run: nova-table show 'demo'"},
			nil,
		},
	}
	for _, tc := range cases {
		_, err := ntable.ReadSet(context.Background(), c, "demo", tc.scope, tc.epoch...)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		got := err.Error()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: refusal lacks %q:\n  %s", tc.name, w, got)
			}
		}
		for _, w := range tc.never {
			if strings.Contains(got, w) {
				t.Errorf("%s: refusal holds %q:\n  %s", tc.name, w, got)
			}
		}
	}
}
