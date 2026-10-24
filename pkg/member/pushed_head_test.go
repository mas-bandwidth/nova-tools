package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ordered is every recorded call in order, each argv joined.
func (s *scriptSprint) ordered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// A work card whose push put a commit on its branch says that head to the sprint with
// progress --head before its finish (nova-sprint FieldProgressHead), so a redeal of the
// attempt carries the pushed work into the next one instead of staging from BASE.
func TestAPushedHeadIsReportedBeforeTheFinish(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed the thing"})
	_, err := g.tick(t)
	require.NoError(t, err)
	want := "progress --as m c1@1 --epoch 7 --head " + fullSha
	assert.Equal(t, []string{want}, g.s.lines("progress"))
	calls := g.s.ordered()
	head, finish := -1, -1
	for i, c := range calls {
		switch {
		case c == want:
			head = i
		case strings.HasPrefix(c, "finish "):
			finish = i
		}
	}
	require.NotEqual(t, -1, finish, "the card is finished")
	assert.Less(t, head, finish, "the head is reported before the finish: a finish the store loses still leaves it carried")
	assert.NotContains(t, g.out.String(), "progress --head", "a head the store took says nothing")
}

// The report is best effort: a progress --head the store refuses, or does not answer, is a
// NOTE on the member's output, and the card is finished at its pushed head all the same.
func TestARefusedHeadReportNeverFailsTheCard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		code int
		said string
	}{
		{"refused", 1, "refused (exit 1; the finish carries the head): card c1 is not working"},
		{"unanswered", 2, "not answered (the finish carries the head): card c1 is not working"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pu := &fakePusher{def: Push{Sha: fullSha}}
			g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "landed"})
			g.s.set("progress", tc.code, "card c1 is not working\n")
			_, err := g.tick(t)
			require.NoError(t, err)
			assert.Len(t, g.s.lines("progress"), 1)
			assert.Equal(t, []string{"finish --as m c1@1 --report pushed=" + fullSha + " to work/c1: landed --head " + fullSha + " --branch work/c1 --epoch 7"}, g.s.lines("finish"))
			assert.Contains(t, g.out.String(), "NOTE c1: progress --head "+fullSha+" "+tc.said+"\n")
		})
	}
}

// Nothing pushed is nothing reported: a refused push and a child with no commit send no
// progress --head, so a redeal never carries a head origin does not hold.
func TestNoPushReportsNoHead(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		pu  Push
		res Result
	}{
		"refused":   {Push{Refused: "! [rejected] (non-fast-forward)"}, Result{Ran: true, OK: true, Head: fullSha, Report: "landed"}},
		"no commit": {Push{Sha: fullSha}, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: "nothing to commit"}},
		"short sha": {Push{Sha: "0123456"}, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g, _ := pushRig(t, &fakePusher{def: tc.pu}, tc.res)
			_, err := g.tick(t)
			require.NoError(t, err)
			assert.Empty(t, g.s.lines("progress"))
			assert.Len(t, g.s.lines("finish"), 1)
		})
	}
}
