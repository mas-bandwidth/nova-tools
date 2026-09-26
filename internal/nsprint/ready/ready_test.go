package ready

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

type mapForge map[string]deal.Ref

func (m mapForge) Ref(_ context.Context, repo string, n int) (deal.Ref, error) {
	r, ok := m[fmt.Sprintf("%s#%d", repo, n)]
	if !ok {
		return deal.Ref{}, errors.New("no answer")
	}
	return r, nil
}

// TestEvaluateFailsClosed: every question without a known answer blocks, and
// the ready set is an antichain (a later candidate overlapping an earlier
// ready one waits on it; an unset PATHS is the whole repo; other repos are
// disjoint).
func TestEvaluateFailsClosed(t *testing.T) {
	t.Parallel()

	const R = "o/r"
	item := func(id string, deps, paths []string, kv ...string) Item {
		it := Item{Sprint: "S", ID: id, Kind: KindCard, DependsOn: deps, Paths: paths, Repo: R, Base: "dev"}
		for i := 0; i+1 < len(kv); i += 2 {
			switch kv[i] {
			case "repo":
				it.Repo = kv[i+1]
			case "base":
				it.Base = kv[i+1]
			}
		}
		return it
	}
	snap := Snapshot{
		Candidates: []Item{
			item("first", nil, []string{"a/"}),
			item("second", nil, []string{"a/b.go"}),
			item("whole", nil, nil),
			item("other-repo", nil, []string{"a/"}, "repo", "o/other"),
			item("nobase", []string{R + "#1"}, []string{"z1"}, "base", ""),
			item("forge-down", []string{R + "#9"}, []string{"z2"}),
			item("ghost", []string{"missing"}, []string{"z3"}),
			item("landed-nobase", []string{"lnb"}, []string{"z4"}),
			item("issue-open", []string{R + "#2"}, []string{"z5"}),
			item("other-base", []string{R + "#3"}, []string{"z6"}),
		},
		Deps: map[string]deal.DepCard{
			"S/lnb": {Found: true, State: "landed"},
		},
	}
	forge := mapForge{
		R + "#1": {IsPR: true, Merged: true, State: "closed", Base: "dev"},
		R + "#2": {State: "open"},
		R + "#3": {IsPR: true, Merged: true, State: "closed", Base: "main"},
	}
	want := map[string]string{
		"first":         "",
		"second":        "WAIT PATHS first",
		"whole":         "WAIT PATHS first",
		"other-repo":    "",
		"nobase":        "UNKNOWN o/r#1 pr#1 base-unresolved",
		"forge-down":    "UNKNOWN o/r#9 pr#9 forge: no answer",
		"ghost":         "UNKNOWN missing unknown",
		"landed-nobase": "UNKNOWN lnb base-unresolved",
		"issue-open":    "WAIT o/r#2 issue#2 open",
		"other-base":    "WAIT o/r#3 pr#3 merged-into main not dev",
	}
	for _, v := range Evaluate(context.Background(), snap, forge) {
		w := want[v.Item.ID]
		if v.Blocker != w || v.Ready != (w == "") {
			t.Errorf("%s: ready=%v blocker %q, want %q", v.Item.ID, v.Ready, v.Blocker, w)
		}
	}
}

func TestFindRefusesAnAmbiguousBareID(t *testing.T) {
	t.Parallel()

	vs := []Verdict{{Item: Item{Sprint: "a", ID: "x"}}, {Item: Item{Sprint: "b", ID: "x"}}}
	if _, _, err := Find(vs, "x"); err == nil {
		t.Fatal("bare id in two sprints: want an error naming both")
	}
	if v, ok, err := Find(vs, "b/x"); err != nil || !ok || v.Item.Sprint != "b" {
		t.Fatalf("Find b/x = %+v %v %v", v, ok, err)
	}
}
