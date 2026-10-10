package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A brief correction edits the card in place (the owner, 2026-10-06: "We gotta stop doing
// this twin shit. it's waste."): the same id, the brief changed, the next attempt staged
// from the last pushed head, the record saying what changed and why. These run on the twin.

// boundWorld is a sprint whose attempt cap is one, with n cards of stream s1 each failed
// once: each in review at its brief's bound, its brief-defect judgment open.
func boundWorld(t *testing.T, n int) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --attempts 1")
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		id := "s1-" + strconv.Itoa(i)
		ta.ok("add --stream s1 " + id + " --one --brief-file " + writeNeedsBrief(t, dir, id, "the old work of "+id, ""))
	}
	ta.deal(n)
	for i := 1; i <= n; i++ {
		ta.failOnce("m1", "s1-"+strconv.Itoa(i)+".w1@1", "no RESULT.md")
		pr := ta.primary("s1-" + strconv.Itoa(i))
		require.Equal(t, sprint.Review, pr.Col)
		_, at := sprint.AtBriefBound(pr, "", 1)
		require.True(t, at, "s1-%d is at its bound", i)
	}
	return ta
}

// briefWrong is the brief-defect judgments the inbox holds open.
func briefWrong(ta *testApp) []sprint.Group {
	var out []sprint.Group
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NBriefWrong && g.Kind == sprint.Judgment {
			out = append(out, g)
		}
	}
	return out
}

// briefLine is the command the inbox prints for the group's brief decision, without the
// program's name.
func briefLine(t *testing.T, g sprint.Group) string {
	t.Helper()
	for _, c := range g.Commands {
		if c.Decision == "brief" {
			require.Len(t, c.Lines, 1)
			return strings.TrimPrefix(c.Lines[0], "nova-sprint ")
		}
	}
	t.Fatalf("no brief decision in %+v", g.Commands)
	return ""
}

// A card in review at its bound takes a corrected brief in place: the same id, ready, its
// bound reset (the bound counts attempts under one brief), its judgment closed, the record
// and card naming who edited it, at which attempt and what changed; the deal then cuts its
// next attempt, attempt 2 of the same card.
func TestBriefEditsACardInReviewInPlace(t *testing.T) {
	t.Parallel()
	ta := boundWorld(t, 1)
	ta.group(sprint.NBriefWrong, "s1")

	out := ta.ok("brief s1-1 --brief-file " + writeNeedsBrief(t, t.TempDir(), "s1-1", "the new work of s1-1", ""))
	assert.Contains(t, out, "s1-1 brief edited in place by coordinator at attempt 1: - the old work of s1-1 | + the new work of s1-1")
	pr := ta.primary("s1-1")
	assert.Equal(t, sprint.Ready, pr.Col, "the next attempt waits for the deal")
	assert.Equal(t, "1", pr.F("attempt"))
	assert.Equal(t, "1", pr.F(sprint.FieldBriefAttempt), "the bound counts from the edit")
	assert.Contains(t, pr.F("brief"), "the new work of s1-1")
	_, at := sprint.AtBriefBound(pr, "", 1)
	assert.False(t, at, "the bound is reset")
	assert.Empty(t, briefWrong(ta), "the brief-defect judgment closed")

	ta.deal(1)
	pr = ta.primary("s1-1")
	assert.Equal(t, sprint.Working, pr.Col)
	assert.Equal(t, "2", pr.F("attempt"), "the attempt rises")
	assert.Equal(t, "s1-1.w2", pr.F("work"), "the same id, its next attempt")
	assert.Contains(t, ta.ok("card s1-1"), "brief edited in place by coordinator at attempt 1: - the old work of s1-1 | + the new work of s1-1")
	ta.clean()
}

// The bound's judgment over several cards is answered by the command the inbox prints for
// it: brief --group <id> --expect <n> --dir <dir> --answers <notes>, one corrected brief a
// card; one brief for a group of several is refused naming --dir, nothing written.
func TestBriefGroupAnswersTheBoundJudgment(t *testing.T) {
	t.Parallel()
	ta := boundWorld(t, 3)
	g := ta.group(sprint.NBriefWrong, "s1")
	require.Equal(t, 3, g.Size)
	line := briefLine(t, g)
	assert.Equal(t, "brief --group "+g.ID+" --expect 3 --dir '<a directory of the corrected briefs, <id>.md a card>' --answers "+strings.Join(g.Notes, ","), line)

	applies := ta.applies()
	code, _, errs := ta.do("brief --group " + g.ID + " --expect 3 --brief-file " + writeNeedsBrief(t, t.TempDir(), "x", "one for all", ""))
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "give --dir <dir> holding <id>.md for each of s1-1, s1-2, s1-3")
	assert.Equal(t, applies, ta.applies(), "a refused group wrote")

	dir := t.TempDir()
	require.Equal(t, []string{"s1-1", "s1-2", "s1-3"}, g.Primaries)
	for _, id := range g.Primaries {
		writeNeedsBrief(t, dir, id, "the corrected work of "+id, "")
	}
	out := ta.ok(strings.Replace(line, "'<a directory of the corrected briefs, <id>.md a card>'", dir, 1))
	assert.Contains(t, out, "BRIEF OK moved=3 refused=0")
	for _, id := range g.Primaries {
		pr := ta.primary(id)
		assert.Equal(t, sprint.Ready, pr.Col, id)
		assert.Equal(t, "1", pr.F(sprint.FieldBriefAttempt), id)
		assert.Contains(t, pr.F("brief"), "the corrected work of "+id)
	}
	assert.Empty(t, briefWrong(ta), "the group is answered")
	ta.clean()
}

// The inbox prints the brief decision in the form the verb accepts: for one card,
// brief <id> --brief-file <path>, which runs as printed with the placeholder filled.
func TestInboxPrintsTheBriefDecisionTheVerbRuns(t *testing.T) {
	t.Parallel()
	ta := boundWorld(t, 1)
	line := briefLine(t, ta.group(sprint.NBriefWrong, "s1"))
	assert.Equal(t, "brief s1-1 --brief-file '<the corrected brief>'", line)
	file := writeNeedsBrief(t, t.TempDir(), "s1-1", "the corrected work", "")
	assert.Contains(t, ta.ok(strings.Replace(line, "'<the corrected brief>'", file, 1)), "s1-1 brief edited in place by coordinator at attempt 1")
	assert.Equal(t, sprint.Ready, ta.primary("s1-1").Col)
}

// readersOf is the readers table's read cards on the primary still placed.
func (ta *testApp) readersOf(id string) []*sprint.Card {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	s, err := st.Load(context.Background(), []string{sprint.Readers}, nil)
	require.NoError(ta.t, err)
	return s.Readers.Of(id)
}

// A card in review whose attempt pushed a head and whose read is open takes a brief in
// place: the readers table's read is retired with the edit, and the next attempt is staged
// from that pushed head (BaseOf, the rework's carry), the same id.
func TestBriefRetiresItsReadsAndCarriesThePushedHead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 s1-1 --one --brief-file " + writeNeedsBrief(t, t.TempDir(), "s1-1", "the old work", ""))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head " + landHead + " --report done")
	ta.ok("ask s1-1")
	require.NotEmpty(t, ta.readersOf("s1-1"), "a read is open at the edit")

	out := ta.ok("brief s1-1 --brief-file " + writeNeedsBrief(t, t.TempDir(), "s1-1", "the new work", ""))
	assert.Contains(t, out, "s1-1 brief edited in place by coordinator at attempt 1")
	assert.Empty(t, ta.readersOf("s1-1"), "the open read is retired with the edit")
	assert.Equal(t, sprint.Ready, ta.primary("s1-1").Col)

	ta.deal(1)
	var take struct {
		Packets []member.Packet `json:"packets"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("take --as m1 s1-1.w2@1 --json")), &take))
	require.Len(t, take.Packets, 1)
	assert.Equal(t, landHead, take.Packets[0].BaseHead, "staged from the last pushed head")
	assert.Equal(t, 1, take.Packets[0].BaseFrom)
	assert.Contains(t, take.Packets[0].Brief, "the new work")
	ta.clean()
}

// A friend's read open on her fleet row is retired with the card's attempt, by a brief
// edited in place and by a rework alike: left, it keeps her room and closes against the
// attempt that was replaced, raising a read-broken judgment on it.
func TestAFriendsOpenReadIsRetiredByBriefAndByRework(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ verb, line string }{
		{"brief", "brief s1-1 --brief-file "},
		{"rework", "rework s1-1 --fix 'handle the empty case'"},
	} {
		ta, _, id := friendReadTwin(t, false)
		require.True(t, ta.fleetCard(id).Placed(), "%s: amy's read is open", c.verb)
		line := c.line
		if c.verb == "brief" {
			line += writeBrief(t, "s1-1: the flash card corrected (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nAS A READ\nread the flash card")
		}
		ta.ok(line)
		ta.ok("tick") // the machine runs: the change drains at the pump
		rc := ta.fleetCard(id)
		assert.False(t, rc.Placed(), "%s: amy's read is retired", c.verb)
		assert.Equal(t, c.verb, rc.F("retired_by"), c.verb)
		code, _, errs := ta.do("read --as " + sprint.FriendRow("amy") + " --broken " + id + " --finding 'stale'")
		assert.NotEqual(t, 0, code, "%s: no verdict closes against the replaced attempt: %s", c.verb, errs)
		for _, g := range ta.inboxGroups() {
			assert.False(t, g.Type == sprint.NReadBroken && g.Kind == sprint.Judgment, "%s: no read-broken judgment on the old attempt", c.verb)
		}
	}
}
