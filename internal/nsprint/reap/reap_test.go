package reap_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reap"
)

const (
	S    = "reap-0925"
	repo = "mas-bandwidth/nova-tools"
)

func head(n int) string { return strings.Repeat(strconv.Itoa(n%10), 40) }

// TestDecideRules is the policy alone.
func TestDecideRules(t *testing.T) {
	t.Parallel()

	h := head(7)
	base := func(n int) reap.PR {
		return reap.PR{Repo: repo, N: n, Key: fmt.Sprintf("pr:nova-tools:%d", n), Exists: true, State: "open", Head: h}
	}
	withTasks := func(p reap.PR, ts ...reap.Task) reap.PR { p.Tasks = ts; return p }
	for _, tc := range []struct {
		name    string
		p       reap.PR
		verdict string
		rule    string
	}{
		{"task landed with another pr", withTasks(base(1), reap.Task{ID: "build-9-x", State: "landed", PR: 2}), reap.Reap, reap.RuleLandedBy},
		{"task landed with this pr", withTasks(base(1), reap.Task{ID: "build-9-x", State: "landed", PR: 1}), reap.Keep, ""},
		{"task landed, no pr", withTasks(base(1), reap.Task{ID: "build-9-x", State: "landed"}), reap.Keep, ""},
		{"merging task is not live", withTasks(func() reap.PR { p := base(1); p.Gone = "1"; return p }(), reap.Task{ID: "build-9-x", State: "merging"}), reap.Reap, reap.RuleBranchGone},
		{"parked task is live", withTasks(func() reap.PR { p := base(1); p.Gone = "1"; return p }(), reap.Task{ID: "fix-1-x", State: "parked"}), reap.Keep, ""},
		{"closed task, no read", withTasks(base(1), reap.Task{ID: "build-9-x", State: "closed"}), reap.Keep, ""},
		{"read 7 and card done", withTasks(func() reap.PR { p := base(1); p.Reads = "SCORE who=emma head=" + h[:8] + " score=7/10"; return p }(), reap.Task{ID: "build-9-x", State: "done"}), reap.Reap, reap.RuleReadUnder8},
		{"read 8 keeps a gone branch", func() reap.PR {
			p := base(1)
			p.Gone, p.Reads = "1", "SCORE who=emma head="+h[:8]+" score=8/10"
			return p
		}(), reap.Keep, ""},
		{"jev lines are not reads", withTasks(func() reap.PR { p := base(1); p.Reads = "SCORE who=jev head=" + h[:8] + " score=3/10"; return p }(), reap.Task{ID: "build-9-x", State: "closed"}), reap.Keep, ""},
		{"closed record", func() reap.PR { p := base(1); p.State, p.Gone = "closed", "1"; return p }(), reap.Done, ""},
		{"closed_at", func() reap.PR { p := base(1); p.ClosedAt, p.Gone = "1", "1"; return p }(), reap.Done, ""},
		{"stuck", func() reap.PR { p := base(1); p.Gone, p.ReapClose, p.Attempts = "1", "failed:500", 10; return p }(), reap.Stuck, reap.RuleBranchGone},
		{"nine failures retry", func() reap.PR { p := base(1); p.Gone, p.ReapClose, p.Attempts = "1", "failed:500", 9; return p }(), reap.Reap, reap.RuleBranchGone},
	} {
		d := reap.Decide([]reap.PR{tc.p})[0]
		if d.Verdict != tc.verdict || d.Rule != tc.rule {
			t.Errorf("%s: %s %s (%s), want %s %s", tc.name, d.Verdict, d.Rule, d.Why, tc.verdict, tc.rule)
		}
	}
	// A sibling PR of the same label landed: landed-by that PR.
	a, b := base(1), base(2)
	a.Label, b.Label, b.State = "card-z", "card-z", "landed"
	if d := reap.Decide([]reap.PR{a, b})[0]; d.Verdict != reap.Reap || d.Rule != reap.RuleLandedBy || d.By != "nova-tools#2" {
		t.Errorf("same label: %+v", d)
	}
}
