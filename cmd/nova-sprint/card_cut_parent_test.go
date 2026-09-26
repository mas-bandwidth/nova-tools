package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// fakePlanStore is card cut --parent's two store seams in tests: the parent
// records it knows and the one bind it records.
type fakePlanStore struct {
	facts  map[string]planFacts
	bound  []string // parent, stitch, then the children
	refuse string   // a bind refusal
}

func (f *fakePlanStore) plan(_ context.Context, id string) (planFacts, error) {
	return f.facts[id], nil
}

func (f *fakePlanStore) bind(_ context.Context, parent string, children []string, stitch, by string) (taskcard.Result, error) {
	if f.refuse != "" {
		return taskcard.Result{}, &taskcard.Refused{Why: f.refuse}
	}
	f.bound = append([]string{parent, stitch}, children...)
	return taskcard.Result{Status: "MOVED", From: "ready", To: "waiting"}, nil
}

func planParent(where string) planFacts {
	return planFacts{Rec: map[string]string{"where": where, "stream": "autonomy", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": cutFromSHA, "title": "work as a hierarchy", "ref": "mas-bandwidth/nova-tools#4317",
		"done_when": "a parent card cuts children and a stitch; the parent lands when the stitch lands", "paths": "internal/nsprint/taskcard",
		"test": "./cmd/nova-sprint TestCardCutParentCutsChildrenAndStitchInOneCall"}}
}

const childRows = "id\ttitle\tpaths\tdone-when\tbody\tdepends-on\troute\ttest\n" +
	"h-model\tthe model\tinternal/nsprint/taskcard/hierarchy.go\tPlan.State is derived\tbuild it\tnone\tpro\t./internal/nsprint/taskcard TestPlanStateIsDerived\n" +
	"h-verb\tthe verb\tcmd/nova-sprint/card_cut_from.go internal/nsprint/taskcard/hierarchy.go\tcard cut --parent cuts children and stitch\tbuild it\th-model\tpro\t./cmd/nova-sprint TestCardCutParentCutsChildrenAndStitchInOneCall\n" +
	"h-table\tthe table\tcmd/nova-sprint/ws.go\tstream ls --tree\tbuild it\tnone\tfriend\tnone a friend's table row\n"

// TestCardCutParentCutsChildrenAndStitchInOneCall is nova-tools#4317's
// DONE-WHEN: one call cuts the rows as the parent's children (its stream,
// parent and phase on each) and the stitch behind them (DEPENDS-ON every
// child, the parent's DONE-WHEN, the union of PATHS, route frontier, kind
// stitch, a brief with the marker), then binds the parent; the receipts name
// each and the plan.
func TestCardCutParentCutsChildrenAndStitchInOneCall(t *testing.T) {
	t.Parallel()
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	plans := &fakePlanStore{facts: map[string]planFacts{"nova-tools-4317": planParent("ready")}}
	d := cutDeps(forge, st)
	d.Plan, d.Bind = plans.plan, plans.bind
	code, out := runCutFrom(cutFromOpts{Text: []byte(childRows), Parent: "nova-tools-4317", Repo: ""}, d)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"CARD CUT row=1 id=h-model ref=mas-bandwidth/nova-tools#5000 stream=autonomy to=waiting depends=none\n",
		"CARD CUT row=2 id=h-verb ref=mas-bandwidth/nova-tools#5001 stream=autonomy to=waiting depends=h-model\n",
		"CARD CUT row=3 id=h-table ref=mas-bandwidth/nova-tools#5002 stream=autonomy to=waiting depends=none\n",
		"CARD CUT row=stitch id=nova-tools-4317-stitch ref=mas-bandwidth/nova-tools#5003 stream=autonomy to=waiting depends=h-model,h-verb,h-table\n",
		"CARD CUT PLAN parent=nova-tools-4317 children=3 stitch=nova-tools-4317-stitch parent_to=waiting depends=nova-tools-4317-stitch\n",
		"CARD CUT FROM file=cards.tsv rows=4 cut=4 already=0 refused=0 filed=4 reused=0 github=on ms=0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Join(plans.bound, " ") != "nova-tools-4317 nova-tools-4317-stitch h-model h-verb h-table" {
		t.Fatalf("bound %v", plans.bound)
	}
	if len(st.batches) != 1 || len(st.batches[0]) != 4 {
		t.Fatalf("pushes %d batches of %d, want one of 4", len(st.batches), len(st.batches[0]))
	}
	byID := map[string]taskcard.PushRequest{}
	for _, r := range st.batches[0] {
		byID[r.ID] = r
	}
	child := byID["h-verb"]
	if child.Stream != "autonomy" || child.DependsOn != "h-model" || child.Why != "card cut --parent nova-tools-4317" ||
		strings.Join(child.Fields, " ") != "parent nova-tools-4317 phase child" || child.Spec.BaseSHA != cutFromSHA || child.Spec.Route != "pro" {
		t.Errorf("child request %+v (fields %v, spec %+v)", child, child.Fields, *child.Spec)
	}
	stitch := byID["nova-tools-4317-stitch"]
	if stitch.DependsOn != "h-model,h-verb,h-table" || strings.Join(stitch.Fields, " ") != "parent nova-tools-4317 phase stitch" ||
		stitch.Spec.Kind != "stitch" || stitch.Spec.Route != "frontier" || stitch.Spec.Est != "60" || stitch.Title != "stitch: work as a hierarchy" ||
		stitch.Spec.DoneWhen != "a parent card cuts children and a stitch; the parent lands when the stitch lands" ||
		stitch.Spec.Paths != "internal/nsprint/taskcard/hierarchy.go cmd/nova-sprint/card_cut_from.go cmd/nova-sprint/ws.go" {
		t.Errorf("stitch request %+v (fields %v, spec %+v)", stitch, stitch.Fields, *stitch.Spec)
	}
	if !strings.Contains(stitch.Spec.Body, "PARENT: nova-tools-4317 (stitch of the plan)") || !strings.Contains(stitch.Spec.Body, "\n"+taskcard.BriefMarker) ||
		!strings.Contains(stitch.Spec.Body, "Stitch of plan nova-tools-4317 (mas-bandwidth/nova-tools#4317)") {
		t.Errorf("stitch body:\n%s", stitch.Spec.Body)
	}
	// The stitch's issue is filed last, after every child (its DEPENDS-ON).
	if forge.titles[3] != "stitch: work as a hierarchy" || !strings.Contains(forge.bodies[3], "DEPENDS-ON: mas-bandwidth/nova-tools#5000,mas-bandwidth/nova-tools#5001,mas-bandwidth/nova-tools#5002") {
		t.Errorf("filed %v; last body:\n%s", forge.titles, forge.bodies[3])
	}
	// Reused issues from the ledger on a rerun: nothing is filed twice and
	// the cards are already; the bind still runs with every child.
	forge2 := &fakeCutForge{}
	plans.bound = nil
	d.File = forge2.file
	code, out = runCutFrom(cutFromOpts{Text: []byte(childRows), Parent: "nova-tools-4317", Repo: ""}, d)
	if code != 0 || len(forge2.titles) != 0 || strings.Count(out, "to=already") != 4 || !strings.Contains(out, "CARD CUT PLAN parent=nova-tools-4317 children=3") {
		t.Errorf("rerun: exit %d filed %d:\n%s", code, len(forge2.titles), out)
	}
}

// TestCardCutParentRefusalsNameTheRemedy: nothing is filed, pushed or bound
// when the parent is missing, has moved on, a row names another stream, the
// parent has no DONE-WHEN, or the stitch moved on; each refusal is one line
// with the remedy.
func TestCardCutParentRefusalsNameTheRemedy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		facts planFacts
		rows  string
		want  string
	}{
		{"missing", planFacts{}, childRows, "no task:nova-tools-4317: push the parent first (card cut --issue <n>, or task push --id nova-tools-4317)"},
		{"working", planParent("working"), childRows, "task:nova-tools-4317 is working; a plan is cut while its parent waits (waiting or ready)"},
		{"landed", planParent("landed"), childRows, "task:nova-tools-4317 is landed; a plan is cut"},
		{"other stream", planParent("waiting"), "title\tstream\tpaths\tdone-when\nx\tswarm: cards\ta.go\tholds\n", `stream \"swarm: cards\" is not the parent's stream \"autonomy\" (children ride the parent's stream)`},
		{"no done-when", func() planFacts { f := planParent("waiting"); delete(f.Rec, "done_when"); return f }(), childRows, "task:nova-tools-4317 has no DONE-WHEN; the stitch's DONE-WHEN is the parent's"},
		{"stitch moved on", func() planFacts {
			f := planParent("waiting")
			f.Rec["stitch"], f.StitchWhere = "nova-tools-4317-stitch", "working"
			return f
		}(), childRows, "task:nova-tools-4317 is a plan whose stitch nova-tools-4317-stitch is working: more children are cut while the stitch waits"},
		{"another stitch", func() planFacts {
			f := planParent("waiting")
			f.Rec["stitch"], f.StitchWhere = "other", "waiting"
			return f
		}(), childRows, "task:nova-tools-4317 is a plan whose stitch is other, not nova-tools-4317-stitch"},
	}
	for _, tc := range cases {
		forge, st := &fakeCutForge{}, &fakeCutStore{}
		plans := &fakePlanStore{facts: map[string]planFacts{"nova-tools-4317": tc.facts}}
		d := cutDeps(forge, st)
		d.Plan, d.Bind = plans.plan, plans.bind
		code, out := runCutFrom(cutFromOpts{Text: []byte(tc.rows), Parent: "nova-tools-4317"}, d)
		if code != 1 || !strings.Contains(out, tc.want) {
			t.Errorf("%s: exit %d, want 1 with %q:\n%s", tc.name, code, tc.want, out)
		}
		if len(forge.titles) != 0 || len(st.batches) != 0 || plans.bound != nil {
			t.Errorf("%s: wrote something: filed %d pushed %d bound %v", tc.name, len(forge.titles), len(st.batches), plans.bound)
		}
		if n := strings.Count(out, "REFUSED"); n != 1 {
			t.Errorf("%s: %d REFUSED lines, want 1:\n%s", tc.name, n, out)
		}
	}
	// A bind refusal after the push names bind: the cards stand, the parent
	// is as it was.
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	plans := &fakePlanStore{facts: map[string]planFacts{"nova-tools-4317": planParent("waiting")}, refuse: "task:nova-tools-4317 is working; a plan is cut while its parent waits (waiting or ready)"}
	d := cutDeps(forge, st)
	d.Plan, d.Bind = plans.plan, plans.bind
	code, out := runCutFrom(cutFromOpts{Text: []byte(childRows), Parent: "nova-tools-4317"}, d)
	if code != 1 || !strings.Contains(out, "CARD CUT REFUSED parent=nova-tools-4317 why=\"bind: task:nova-tools-4317 is working;") || strings.Count(out, "\nCARD CUT row=") != 3 {
		t.Errorf("bind refusal: exit %d:\n%s", code, out)
	}
}

// TestCardCutParentDryRunAndMoreChildren: --dry-run prints the rows, the
// stitch and the plan line and writes nothing; a parent that already has
// children waiting on its stitch gets the new rows appended and the stitch's
// edges grow to the old children too; --stitch-route and --stitch-est set
// the stitch's route and est; --no-github ids are slugs.
func TestCardCutParentDryRunAndMoreChildren(t *testing.T) {
	t.Parallel()
	forge, st := &fakeCutForge{}, &fakeCutStore{}
	facts := planParent("waiting")
	facts.Rec["children"], facts.Rec["stitch"], facts.StitchWhere = "nova-tools-5000 nova-tools-5001", "nova-tools-4317-stitch", "waiting"
	plans := &fakePlanStore{facts: map[string]planFacts{"nova-tools-4317": facts}}
	d := cutDeps(forge, st)
	d.Plan, d.Bind = plans.plan, plans.bind
	rows := "title\tpaths\tdone-when\nthe docs\tdocs/CLI.md\tdocumented\n"
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows), Parent: "nova-tools-4317", DryRun: true, NoGitHub: true, StitchRoute: "friend", StitchEst: "2 h"}, d)
	if code != 0 {
		t.Fatalf("dry run exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"CARD CUT DRY row=1 id=the-docs stream=autonomy who=any route=friend est=30 depends=none title=\"the docs\"\n",
		"CARD CUT DRY row=stitch id=nova-tools-4317-stitch stream=autonomy who=any route=friend est=\"2 h\" depends=the-docs,nova-tools-5000,nova-tools-5001 title=\"stitch: work as a hierarchy\"\n",
		"CARD CUT DRY PLAN parent=nova-tools-4317 children=1 stitch=nova-tools-4317-stitch parent_to=waiting depends=nova-tools-4317-stitch\n",
		"CARD CUT FROM file=cards.tsv rows=2 cut=0 already=0 refused=0 filed=0 reused=0 github=off ms=0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(forge.titles) != 0 || len(st.batches) != 0 || plans.bound != nil {
		t.Fatalf("a dry run wrote: filed %d pushed %d bound %v", len(forge.titles), len(st.batches), plans.bound)
	}
	// The same, for real: the waiting stitch is never pushed again (its row
	// is to=already; its edges grow at the bind) and the bind appends.
	code, out = runCutFrom(cutFromOpts{Text: []byte(rows), Parent: "nova-tools-4317", NoGitHub: true}, d)
	if code != 0 || strings.Join(plans.bound, " ") != "nova-tools-4317 nova-tools-4317-stitch the-docs" {
		t.Fatalf("exit %d bound %v:\n%s", code, plans.bound, out)
	}
	if len(st.batches) != 1 || len(st.batches[0]) != 1 || st.batches[0][0].ID != "the-docs" ||
		!strings.Contains(out, "CARD CUT row=stitch id=nova-tools-4317-stitch ref=- stream=autonomy to=already depends=the-docs,nova-tools-5000,nova-tools-5001\n") ||
		!strings.Contains(out, "CARD CUT PLAN parent=nova-tools-4317 children=1 stitch=nova-tools-4317-stitch parent_to=waiting depends=nova-tools-4317-stitch\n") {
		t.Fatalf("pushed %v:\n%s", st.batches, out)
	}
}

// TestCardCutParentFlags: the flag set refuses the shapes that cannot be
// meant, one line each with the remedy, before any store is dialled.
func TestCardCutParentFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"card", "cut", "--parent", "p", "--issue", "4"}, "cut --parent <id> wants --from <children.tsv|->"},
		{[]string{"card", "cut", "--parent", "p", "--from", "x.tsv", "--stream", "s"}, "cut --parent takes no --stream"},
		{[]string{"card", "cut", "--parent", "p~1", "--from", "x.tsv"}, "--parent wants a task id"},
		{[]string{"card", "cut", "--parent", "p", "--from", "x.tsv", "--stitch-route", "gpu"}, "--stitch-route wants"},
		{[]string{"card", "cut", "--parent", "p", "--from", "x.tsv", "--stitch-est", "soon"}, "--stitch-est wants minutes"},
		{[]string{"card", "cut", "--parent", "p", "--from", "x.tsv", "--repo", "nope"}, "--repo wants <owner/name>"},
		{[]string{"card", "cut", "--parent", "p", "--from", "/no/such/children.tsv"}, "cannot read --from"},
	} {
		code, stdout, stderr := runSprint(tc.args...)
		if code != 2 || !strings.Contains(stderr, tc.want) || strings.Count(stderr, "\n") != 1 || stdout != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit 2 and one line with %q", tc.args, code, stdout, stderr, tc.want)
		}
	}
}
