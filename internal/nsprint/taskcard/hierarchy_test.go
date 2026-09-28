package taskcard_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestPlanStateIsDerived is nova-tools#4317's rule: a parent's state is
// derived, never stored: working while any child works, review when the
// stitch is in review, landed when the stitch lands.
func TestPlanStateIsDerived(t *testing.T) {
	t.Parallel()
	ch := func(wheres ...string) []taskcard.Child {
		var out []taskcard.Child
		for i, w := range wheres {
			out = append(out, taskcard.Child{ID: "c" + string(rune('1'+i)), Where: w})
		}
		return out
	}
	cases := []struct {
		name   string
		plan   taskcard.Plan
		want   string
		folded string
	}{
		{"all waiting", taskcard.Plan{Where: "waiting", Children: ch("waiting", "waiting"), Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "waiting", "waiting=2"},
		{"one ready", taskcard.Plan{Where: "waiting", Children: ch("waiting", "ready"), Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "ready", "ready=1"},
		{"one working", taskcard.Plan{Where: "waiting", Children: ch("landed", "working", "ready")}, "working", "working=1"},
		{"child in review is work in flight", taskcard.Plan{Where: "waiting", Children: ch("landed", "review")}, "working", "review=1"},
		{"child merging", taskcard.Plan{Where: "waiting", Children: ch("landed", "merging")}, "working", "merging=1"},
		{"children landed, stitch waiting", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "waiting", "landed=2"},
		{"stitch ready is ready", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "ready"}}, "ready", "stitch=p-stitch:ready"},
		{"stitch working", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "working"}}, "working", "stitch=p-stitch:working"},
		{"a failed child is stuck, never waiting", taskcard.Plan{Where: "waiting", Children: []taskcard.Child{{ID: "c1", Where: "landed"}, {ID: "c2", Where: "done", WhereOK: "fail"}, {ID: "c3", Where: "working"}}, Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "stuck", "done=1"},
		{"a done/ok child is not stuck", taskcard.Plan{Where: "waiting", Children: []taskcard.Child{{ID: "c1", Where: "done", WhereOK: "ok"}, {ID: "c2", Where: "waiting"}}, Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "waiting", "done=1"},
		{"a parked child is parked", taskcard.Plan{Where: "waiting", Children: ch("landed", "parked", "working"), Stitch: taskcard.Child{ID: "p-stitch", Where: "waiting"}}, "parked", "parked=1"},
		{"a parked stitch is parked", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "parked"}}, "parked", "stitch=p-stitch:parked"},
		{"the stitch moved on wins over a parked child", taskcard.Plan{Where: "waiting", Children: ch("landed", "parked"), Stitch: taskcard.Child{ID: "p-stitch", Where: "review"}}, "review", "parked=1"},
		{"stitch in review", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "review"}}, "review", "stitch=p-stitch:review"},
		{"stitch merging", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "merging"}}, "merging", "stitch=p-stitch:merging"},
		{"stitch landed lands the plan", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "landed"}}, "landed", "stitch=p-stitch:landed"},
		{"parent landed", taskcard.Plan{Where: "landed", Children: ch("landed"), Stitch: taskcard.Child{ID: "p-stitch", Where: "landed"}}, "landed", "landed=1"},
		{"parent parked", taskcard.Plan{Where: "parked", Children: ch("working")}, "parked", "working=1"},
		{"parent done", taskcard.Plan{Where: "done", Children: ch("landed")}, "done", "landed=1"},
		{"no stitch yet", taskcard.Plan{Where: "waiting", Children: ch("landed", "landed")}, "waiting", "stitch=-"},
	}
	for _, tc := range cases {
		tc.plan.ID = "p"
		if got := tc.plan.State(); got != tc.want {
			t.Errorf("%s: state %q, want %q", tc.name, got, tc.want)
		}
		line := tc.plan.Line()
		if !strings.HasPrefix(line, "plan p "+tc.want+" children=") || !strings.Contains(line, " "+tc.folded) {
			t.Errorf("%s: line %q lacks state %s or %s", tc.name, line, tc.want, tc.folded)
		}
	}
}

func TestStitchIDAndPRRef(t *testing.T) {
	t.Parallel()
	if got := taskcard.StitchID("nova-tools-4317"); got != "nova-tools-4317-stitch" {
		t.Errorf("stitch id %q", got)
	}
	for _, tc := range []struct {
		c    taskcard.Child
		want string
	}{
		{taskcard.Child{Repo: "mas-bandwidth/nova-tools", PR: "12"}, "nova-tools#12"},
		{taskcard.Child{Repo: "nova-tools", PR: "12"}, "nova-tools#12"},
		{taskcard.Child{PR: "12"}, "#12"},
		{taskcard.Child{PR: "0", Ref: "o/r#3"}, "o/r#3"},
		{taskcard.Child{Ref: "o/r#3"}, "o/r#3"},
		{taskcard.Child{}, ""},
	} {
		if got := tc.c.PRRef(); got != tc.want {
			t.Errorf("PRRef(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
	if k, err := taskcard.ResultKind(taskcard.KindStitch); err != nil || k != "fix" {
		t.Errorf("ResultKind(stitch) = %q, %v; want fix (a stitch is a build task)", k, err)
	}
}

// TestPlanStateOverEveryStitchPhase is the #4317 fix's invariant: a plan
// never sits in a state with no way on. Over every phase of the stitch
// (every child landed), a stitch that can still land the plan gives its own
// phase (or waiting while it waits on its edges); one that ended done/ok,
// done/fail or has no record gives stuck with a Remedy that re-cuts it; no
// phase gives waiting when the stitch is terminal.
func TestPlanStateOverEveryStitchPhase(t *testing.T) {
	t.Parallel()
	landed := []taskcard.Child{{ID: "c1", Where: "landed"}, {ID: "c2", Where: "landed"}}
	for _, tc := range []struct {
		where, ok, want string
	}{
		{"waiting", "", "waiting"},
		{"ready", "", "ready"},
		{"working", "", "working"},
		{"review", "", "review"},
		{"merging", "", "merging"},
		{"landed", "ok", "landed"},
		{"parked", "", "parked"},
		{"done", "ok", taskcard.Stuck},
		{"done", "fail", taskcard.Stuck},
		{"", "", taskcard.Stuck}, // the stitch's record is gone
	} {
		p := taskcard.Plan{ID: "pc", Where: "waiting", Children: landed, Stitch: taskcard.Child{ID: "pc-stitch", Where: tc.where, WhereOK: tc.ok}}
		got := p.State()
		if got != tc.want {
			t.Errorf("stitch %s/%s: state %q, want %q", tc.where, tc.ok, got, tc.want)
		}
		terminal := tc.where == "done" || tc.where == "landed" || tc.where == ""
		if terminal && got == "waiting" {
			t.Errorf("stitch %s/%s: waiting on a stitch that can never land", tc.where, tc.ok)
		}
		r := p.Remedy()
		if got == taskcard.Stuck {
			if !strings.Contains(r, "nova-sprint card cut --parent pc re-cuts the stitch (pc-stitch-2, DEPENDS-ON every child)") || !strings.Contains(r, "stitch pc-stitch ") {
				t.Errorf("stitch %s/%s: remedy %q does not name the re-cut", tc.where, tc.ok, r)
			}
		} else if r != "" {
			t.Errorf("stitch %s/%s: remedy %q for state %s", tc.where, tc.ok, r, got)
		}
	}
	// A cancelled stitch and a failed child: drop the child, then re-cut.
	p := taskcard.Plan{ID: "pc", Where: "waiting", Children: []taskcard.Child{{ID: "c1", Where: "landed"}, {ID: "c2", Where: "done", WhereOK: "fail"}},
		Stitch: taskcard.Child{ID: "pc-stitch-2", Where: "done", WhereOK: "fail"}}
	if p.State() != taskcard.Stuck || !strings.Contains(p.Remedy(), "stitch pc-stitch-2 ended done/fail and the plan lands only with its stitch, and c2 ended done/fail; nova-sprint card stitch --drop c2 drops it, then nova-sprint card cut --parent pc re-cuts the stitch (pc-stitch-3,") {
		t.Fatalf("stitch and child ended: %s %q", p.State(), p.Remedy())
	}
	if line := p.Line(); !strings.HasPrefix(line, "plan pc stuck children=2 ") || !strings.HasSuffix(line, " stitch=pc-stitch-2:done") {
		t.Fatalf("line %q", line)
	}
}

// TestNextStitchID: the re-cut ids count up from the first stitch, and only
// a parent's own stitch ids are its stitches.
func TestNextStitchID(t *testing.T) {
	t.Parallel()
	for old, want := range map[string]string{"p-stitch": "p-stitch-2", "p-stitch-2": "p-stitch-3", "p-stitch-9": "p-stitch-10", "": "p-stitch-2"} {
		if got := taskcard.NextStitchID("p", old); got != want {
			t.Errorf("NextStitchID(p, %q) = %q, want %q", old, got, want)
		}
	}
	for id, want := range map[string]bool{"p-stitch": true, "p-stitch-2": true, "p-stitch-10": true, "p-stitch-1": false, "p-stitch-02": false, "p-stitch-x": false, "q-stitch": false, "p": false} {
		if got := taskcard.IsStitchOf("p", id); got != want {
			t.Errorf("IsStitchOf(p, %q) = %v, want %v", id, got, want)
		}
	}
}
