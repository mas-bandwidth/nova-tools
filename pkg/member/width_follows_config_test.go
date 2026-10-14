package member

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widthSprint is a fake sprint whose queue answers carry the width nova-config names
// for the worker, as the server's queue --json does (the-deal-reads-every-width-from-
// nova-config-alone): a worker has no width of its own, so a test gives it one only
// through the poll. A queue answer that already names a width keeps it.
type widthSprint struct {
	Sprint
	width int
}

// answerWidthSprint is widthSprint over an AnswerSprint, which keeps saying what became
// of each write (member.go answerOf).
type answerWidthSprint struct {
	widthSprint
	answer AnswerSprint
}

// rowWidth is s answering width with every queue: the poll's answer is the only width.
func rowWidth(s Sprint, width int) Sprint {
	w := widthSprint{Sprint: s, width: width}
	if a, ok := s.(AnswerSprint); ok {
		return &answerWidthSprint{widthSprint: w, answer: a}
	}
	return &w
}

func (s *widthSprint) Run(args ...string) (int, []byte) {
	code, out := s.Sprint.Run(args...)
	return code, withWidth(args, code, out, s.width)
}

func (s *answerWidthSprint) RunAnswer(args ...string) (int, []byte, sprintwire.Answer) {
	code, out, answer := s.answer.RunAnswer(args...)
	return code, withWidth(args, code, out, s.width), answer
}

// withWidth is a queue answer with "width" set to width when it names none (or 0).
func withWidth(args []string, code int, out []byte, width int) []byte {
	if len(args) == 0 || args[0] != "queue" || code != 0 || width == 0 {
		return out
	}
	var q map[string]json.RawMessage
	if json.Unmarshal(out, &q) != nil {
		return out
	}
	if w, ok := q["width"]; ok && string(w) != "0" {
		return out
	}
	q["width"] = json.RawMessage(fmt.Sprint(width))
	b, err := json.Marshal(q)
	if err != nil {
		return out
	}
	return b
}

// newWidthRig is a rig whose fake queue answers width (rowWidth), as after a poll that
// already answered it; g.s stays the script.
func newWidthRig(width int, cfg Config) *rig {
	g := newRig(cfg)
	g.m.sprint = rowWidth(g.s, width)
	g.m.width = width // as a poll before this one answered: a reader sizes its first packet ask by it
	return g
}

// TestMemberLanesFollowTheConfigWidthUpAndDown pins the rule (the owner, 2026-10-14:
// "nova-config is *THE* one singular definition of width!"; "machines and friends must
// poll and auto update to follow the config width. no exceptions!"): a member runs exactly
// the width its poll answers, on every pass, with no restart and no width of its own.
// Raised, it takes up to the new width; lowered, it kills nothing and starts nothing until
// its running cards are under it; 0, it starts nothing and says so once, naming
// nova-config as the remedy (docs/SPEC-SWARM.md, the width; tla/FriendLane.tla
// LanesStartNeverPastWidth, NoLaneKilledOnLower).
func TestMemberLanesFollowTheConfigWidthUpAndDown(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m"})
	ids := make([]string, 10)
	packets := map[string]Packet{}
	for i := range ids {
		ids[i] = fmt.Sprintf("c%02d", i+1)
		packets[ids[i]] = pk(ids[i])
	}
	// queue is the poll's answer at width w: the cards working on this row, the rest ready
	queue := func(w int, workingIDs ...string) {
		var cards []queueCard
		for _, id := range ids {
			if p, ok := packets[id]; ok && containsID(workingIDs, id) {
				cards = append(cards, working(id, 1, &p))
			} else if !containsID(g.r.started(), id) {
				cards = append(cards, ready(id))
			}
		}
		g.s.set("queue", 0, queueJSONWidth(t, w, cards...))
	}
	take := func(cards ...string) {
		var ps []Packet
		for _, id := range cards {
			ps = append(ps, packets[id])
		}
		g.s.set("take", 0, takeJSON(t, ps...))
	}
	end := func(cards ...string) {
		for _, id := range cards {
			g.r.child(id).end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "h", Report: "r"})
		}
	}

	// width 4: four start
	queue(4)
	take(ids[:4]...)
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 4, g.m.Running(), "width 4 answered: four run:\n%s", g.out.String())
	require.Equal(t, []string{"take --as m --limit 4 --json --epoch 7"}, g.s.lines("take"))

	// width 2 under four running: none killed, none started
	g.s.reset()
	queue(2, ids[:4]...)
	take(ids[4:6]...)
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 4, g.m.Running(), "a lowered width kills nothing")
	assert.Empty(t, g.r.endedLaunches(), "no launch ended for the lowered width")
	assert.Empty(t, g.s.lines("take"), "no take while running is at or above the width")
	assert.Contains(t, g.out.String(), "width 4 -> 2 (nova-config)\n", "the lowered width is followed on the next poll")

	// two finish: the lanes fall to two, and still nothing new starts at width 2
	end(ids[0], ids[1])
	for range 3 {
		queue(2, ids[2:4]...)
		_, err = g.tick(t)
		require.NoError(t, err)
		require.LessOrEqual(t, g.m.Running(), 4)
	}
	require.Equal(t, 2, g.m.Running(), "the two ended were reported and their lanes freed")
	assert.Empty(t, g.s.lines("take"), "running 2 at width 2: nothing taken")
	assert.Len(t, g.r.started(), 4, "nothing new started while lowered")

	// width 6: the next pass takes up to six
	g.s.reset()
	queue(6, ids[2:4]...)
	take(ids[4:8]...)
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"take --as m --limit 4 --json --epoch 7"}, g.s.lines("take"), "raised to 6 with 2 running: room for 4")
	require.Equal(t, 6, g.m.Running(), "width 6 answered: six run")

	// width 0: nothing new starts, nothing is killed, said once with nova-config's remedy
	g.s.reset()
	end(ids[2])
	for range 3 {
		queue(0, ids[3:8]...)
		take(ids[8:]...)
		_, err = g.tick(t)
		require.NoError(t, err)
	}
	assert.Empty(t, g.s.lines("take"), "width 0: nothing taken")
	assert.Equal(t, 5, g.m.Running(), "width 0 kills nothing; the one ended was reported")
	assert.Len(t, g.r.started(), 8, "width 0: nothing new started")
	note := "NOTE width 0: nova-config names no width for this worker, so it takes nothing"
	assert.Equal(t, 1, strings.Count(g.out.String(), note), "said once:\n%s", g.out.String())
	assert.Contains(t, g.out.String(), "(nova-config machine set <m> --width <n>)\n", "the remedy is nova-config's")
	assert.NotContains(t, g.out.String(), "fleet sync", "nova-config is the one width: no hand sync is a remedy")
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
