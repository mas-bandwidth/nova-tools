package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reworked card's next brief opens with the fix (the night of 2026-10-05: lint-pkg-tlc-tbb
// five attempts, sec-rocketnet-server-dos-zhi four, presence-from-session-only five, each sent
// back with the same finding, because the fix rode on a 'The coordinator asks:' line under the
// start and over a long card, and the next lane read the card and never reached it).
const reworkCarried = "89abcdef0123456789abcdef0123456789abcdef"

// reworkCard is a held work card at attempt 2 as the server renders a rework's brief
// (cmd/nova-sprint friendBrief): STATUS, the working directory, the start with the carried
// head, why, the finding and the fix, then the card with its own STOP, RULES and steps.
func reworkCard(card, fix string) HeldCard {
	job := card + "~15"
	branch := "sprint/" + card + ".g1.e15"
	brief := fmt.Sprintf("STATUS: nova-sprint card %s, epoch 15, attempt 2; push your work to the branch %s; when done, write outbox/%s/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n", card, branch, job) +
		"Work in ~/bob-working/jobs/" + job + "/: every clone, worktree and build output goes inside it.\n" +
		"This attempt starts from the current tip of main on origin, never from an older base: fetch it and start your branch there. Carry the work of attempt 1 onto it yourself: its head, " + reworkCarried + ", is the last pushed by any attempt before this one (`git diff origin/main..." + reworkCarried + "` shows that work); where it does not apply cleanly, redo it. The Head you report must be origin's tip of your branch when sync reads it; the attempt is expected to start from the tip named above.\n" +
		"This attempt exists because: attempt 1 finished and a reader found it broken\n" +
		"A reader found: TestPresenceFromSession is not run by the gate\n" +
		"The coordinator asks: " + fix + "\n" +
		"\nREPO: mas-bandwidth/nova-tools\nBASE: main\nSTART: presence is read from three places\nSTOP: presence is read from the session alone, and every caller is moved\nPATHS: internal/presence/**\n" +
		"You are a child of the coordinator.\n\nRULES.\nWork only in the job directory.\n" + strings.Repeat("A long rule a lane reads before it reaches the end.\n", 200) +
		"\nSTEP 1. Enter the worktree.\nSTEP 2. Do THE TASK.\n"
	return HeldCard{Card: card, Job: job, Col: "working", Branch: branch, Attempt: 2, Epoch: 15, Repo: "mas-bandwidth/nova-tools", Base: "main", Brief: brief}
}

func TestAReworkedBriefOpensWithTheFix(t *testing.T) {
	t.Parallel()
	const fix = "add TestPresenceFromSession to the gate list in internal/ci/gate.go"
	h := reworkCard("presence-from-session-only.w2", fix)

	got := ReworkedBrief(h.Brief)
	lines := strings.Split(got, "\n")
	require.Greater(t, len(lines), 4)
	assert.Equal(t, strings.SplitN(h.Brief, "\n", 2)[0], lines[0], "STATUS stays the first line")
	assert.Equal(t, "THE ONE THING LEFT: "+fix, lines[1], "the fix is the first line after STATUS")
	assert.Equal(t, "The reader found: TestPresenceFromSession is not run by the gate", lines[2], "the finding the second")
	assert.Contains(t, lines[3], "attempt 1's head "+reworkCarried, "the carried head")
	assert.Contains(t, lines[3], "sprint/presence-from-session-only.w2.g1.e15", "and the branch it is carried onto")
	assert.Contains(t, lines[4], "HOLD", "a LAND that does not address the fix is said to be a HOLD")
	for _, k := range []string{"testpresencefromsession", "gate"} {
		assert.Contains(t, lines[4], k, "the key words are named")
	}
	fixAt, rules, task := strings.Index(got, "THE ONE THING LEFT"), strings.Index(got, "\nRULES."), strings.Index(got, "STEP 2. Do THE TASK")
	assert.True(t, fixAt < rules && rules < task, "the fix comes before RULES and THE TASK")
	stop, ok := strings.CutPrefix(lineWith(got, "STOP: "), "STOP: ")
	assert.True(t, ok)
	assert.Equal(t, fix, stop, "the attempt's STOP is the fix alone")
	assert.NotContains(t, got, "every caller is moved", "the card's own STOP is not this attempt's")
	assert.NotContains(t, got, "The coordinator asks:", "the fix is said once, first")
	assert.Equal(t, 1, strings.Count(got, "A reader found: ")+strings.Count(got, "The reader found: "), "the finding is said once")
	for _, kept := range []string{"Work in ~/bob-working", "This attempt starts from the current tip", "This attempt exists because:", "REPO: mas-bandwidth/nova-tools", "BASE: main", "START: presence", "PATHS: internal/presence/**", "\nRULES.\n"} {
		assert.Contains(t, got, kept, "the rest of the brief is kept")
	}
	assert.Equal(t, got, ReworkedBrief(got), "a brief already reworked is left as it is")
	p, ok := PacketOf(HeldCard{Card: h.Card, Job: h.Job, Brief: got})
	require.True(t, ok)
	assert.Equal(t, Packet{Card: h.Card, Job: h.Job, Repo: "mas-bandwidth/nova-tools", Base: "main", Branch: h.Branch, Attempt: 2}, p, "the packet reads the same from the reworked brief")
	assert.Equal(t, fix, BriefFix(got))
	assert.Equal(t, fix, BriefFix(h.Brief), "the fix reads from either form")

	// a first attempt, and a rework of failed work with no reader's finding and nothing carried
	first := workCard("first.w1", "working")
	assert.Equal(t, first.Brief, ReworkedBrief(first.Brief), "a brief with no fix is left as it is")
	assert.Equal(t, "", BriefFix(first.Brief))
	failed := strings.Replace(h.Brief, "A reader found: TestPresenceFromSession is not run by the gate\n", "", 1)
	failed = strings.Replace(failed, "This attempt exists because: attempt 1 finished and a reader found it broken", "This attempt exists because: attempt 1 failed: the bench went red", 1)
	failed = strings.Replace(failed, lineWith(failed, "This attempt starts from"), "This attempt starts from the current tip of main on origin, never from an older base: fetch it and start your branch there. No attempt before this one pushed work to carry.", 1)
	fl := strings.Split(ReworkedBrief(failed), "\n")
	assert.Equal(t, "THE ONE THING LEFT: "+fix, fl[1])
	assert.Equal(t, "The reader found: no reader's finding; attempt 1 failed: the bench went red", fl[2], "with no finding, why the attempt exists")
	assert.Contains(t, fl[3], "nothing carried", "no carried head is said as none")

	// the daemon writes the reworked form into her inbox
	r := newRig(t)
	row := &twinRow{}
	r.d.Held = row.held
	row.set(h)
	r.run(t, 1)
	assert.Equal(t, got, briefOf(t, r.d.Dir, h.Job), "the daemon writes the brief with the fix first")

	// a LAND whose report does not carry the fix's key words is a HOLD by the daemon; one that
	// does is a LAND
	const head = "0123456789abcdef0123456789abcdef01234567"
	f := &finishes{}
	r.d.Finish = f.finish
	other := reworkCard("lint-pkg-tlc-tbb.w5", fix)
	inboxJob(t, r.d.Dir, other.Job, other.Brief) // written by another hand, in the server's form
	outboxReport(t, r.d.Dir, h.Job, "Verdict: LAND\nHead: "+head+"\n\nThe presence package reads the session now.\n")
	outboxReport(t, r.d.Dir, other.Job, "Verdict: LAND\nHead: "+head+"\n\nTestPresenceFromSession is in the gate list (internal/ci/gate.go).\n")
	row.set(h, other)
	r.run(t, 2)
	byCard := map[string][]string{}
	for _, argv := range f.got() {
		byCard[argv[3]] = argv
	}
	held := byCard[h.Card+"@1"]
	require.NotEmpty(t, held, "%v", f.got())
	assert.Contains(t, held, "--failed", "a LAND that never reached the first line is a HOLD")
	assert.Contains(t, held, head, "its head is kept")
	assert.True(t, strings.HasPrefix(held[len(held)-1], "friend bob HOLD: "), "%q", held[len(held)-1])
	assert.Contains(t, held[len(held)-1], "THE ONE THING LEFT")
	landed := byCard[other.Card+"@1"]
	require.NotEmpty(t, landed, "%v", f.got())
	assert.NotContains(t, landed, "--failed", "a LAND that addresses the fix lands")
	assert.Contains(t, strings.Join(r.records, "\n"), "outbox: finished card "+h.Card+" from outbox/"+h.Job+"/REPORT.md (Verdict LAND, held by the daemon")
	_, err := os.Stat(filepath.Join(r.d.Dir, "outbox", h.Job, "REPORT.md"))
	assert.NoError(t, err, "the report stays as she wrote it")
}

func TestFixAddressedReadsTheKeyWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fix, report string
		ok          bool
	}{
		{"add TestPresenceFromSession to the gate list", "TestPresenceFromSession now runs in the gate.", true},
		{"add TestPresenceFromSession to the gate list", "Presence is fixed.", false},
		{"assert the bound", "the bound is asserted", true},
		{"do it", "anything", true}, // no key words: nothing to grep for
		{"cap rn_server_accept at MAX_PENDING in server.c and test the flood", "rn_server_accept is capped at MAX_PENDING; the flood test is red before", true},
		{"cap rn_server_accept at MAX_PENDING in server.c and test the flood", "the server is capped", false},
	} {
		missing, ok := FixAddressed(tc.report, tc.fix)
		assert.Equal(t, tc.ok, ok, "%q in %q: missing %v", tc.fix, tc.report, missing)
	}
	assert.LessOrEqual(t, len(FixKeyWords(strings.Repeat("alpha beta gamma delta epsilon zeta theta iota kappa lambda omicron sigma ", 50))), MaxFixKeyWords)
}

// lineWith is the first line of s that starts with prefix.
func lineWith(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}
