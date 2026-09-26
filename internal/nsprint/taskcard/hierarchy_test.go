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

// TestStitchBriefCarriesEveryChildsResult: the generated brief names each
// child's PR, head, read score, RESULT.md line 2 and finding, in order, and
// the plan's DONE-WHEN as the stitch's.
func TestStitchBriefCarriesEveryChildsResult(t *testing.T) {
	t.Parallel()
	p := taskcard.Plan{ID: "nova-tools-4317", Ref: "mas-bandwidth/nova-tools#4317", Where: "waiting",
		DoneWhen: "the plan's DONE-WHEN holds at the stitch's head",
		Children: []taskcard.Child{
			{ID: "nova-tools-5001", Title: "child one", Where: "landed", Repo: "mas-bandwidth/nova-tools", PR: "6001",
				Head: "abcdef0123456789abcdef0123456789abcdef01", Score: "9", Line2: "DONE", Finding: "one dup helper\nleft in pass.go", Paths: "a.go b.go"},
			{ID: "nova-tools-5002", Title: "child two", Where: "review", Ref: "mas-bandwidth/nova-tools#5002", Line2: "BLOCKED the fixture is red on dev", WhereOK: "fail"},
			{ID: "nova-tools-5003", Where: "waiting"},
		},
		Stitch: taskcard.Child{ID: "nova-tools-4317-stitch", Where: "waiting"}}
	b := taskcard.StitchBrief(p)
	if !strings.HasPrefix(b, taskcard.BriefMarker+" ") {
		t.Fatalf("brief starts %q, want the marker %q", b[:20], taskcard.BriefMarker)
	}
	for _, want := range []string{
		"Plan nova-tools-4317 (mas-bandwidth/nova-tools#4317), state working. DONE-WHEN (the stitch's): the plan's DONE-WHEN holds at the stitch's head",
		"- nova-tools-5001 landed pr=nova-tools#6001 head=abcdef012345 score=9/10\n  title: child one\n  result: DONE\n  finding: one dup helper left in pass.go\n  paths: a.go b.go\n",
		"- nova-tools-5002 review pr=mas-bandwidth/nova-tools#5002 head=- score=- outcome=fail\n  title: child two\n  result: BLOCKED the fixture is red on dev\n",
		"- nova-tools-5003 waiting pr=- head=- score=-\n",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
	// The order is the plan's.
	if strings.Index(b, "nova-tools-5001") > strings.Index(b, "nova-tools-5002") || strings.Index(b, "nova-tools-5002") > strings.Index(b, "nova-tools-5003") {
		t.Errorf("children out of order:\n%s", b)
	}
	if got := taskcard.StitchBrief(taskcard.Plan{ID: "p"}); !strings.Contains(got, "- (no children)") {
		t.Errorf("an empty plan's brief says so; got:\n%s", got)
	}
}

// TestWithBriefReplacesTheGeneratedSectionOnly: the stitch's own text
// before the marker is kept verbatim across rewrites; the section after it
// is replaced, not appended.
func TestWithBriefReplacesTheGeneratedSectionOnly(t *testing.T) {
	t.Parallel()
	head := "STREAM: s\nPATHS: a.go\nDONE-WHEN: x\n\nStitch of plan p: read every child."
	first := taskcard.WithBrief(head+"\n\n"+taskcard.BriefMarker+" (0)\n\n- (no children)\n", taskcard.BriefMarker+" (2)\n\n- c1 landed\n- c2 landed\n")
	second := taskcard.WithBrief(first, taskcard.BriefMarker+" (2)\n\n- c1 landed\n- c2 landed pr=x#1\n")
	if !strings.HasPrefix(second, head+"\n\n"+taskcard.BriefMarker) {
		t.Fatalf("the head is not kept verbatim:\n%s", second)
	}
	if strings.Count(second, taskcard.BriefMarker) != 1 || strings.Contains(second, "(no children)") || !strings.Contains(second, "pr=x#1") {
		t.Fatalf("the generated section is replaced, once:\n%s", second)
	}
	if got := taskcard.WithBrief("", "## Children (0)\n\n- (no children)\n"); got != "## Children (0)\n\n- (no children)\n" {
		t.Errorf("an empty body is the brief alone; got %q", got)
	}
	if got := taskcard.WithBrief("## Children (0)\n- old", "## Children (1)\n- new\n"); got != "## Children (1)\n- new\n" {
		t.Errorf("a body that is only the section is replaced; got %q", got)
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
