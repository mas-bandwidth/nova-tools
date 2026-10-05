package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A ready card pinned to a friend down or held (the owner, 2026-10-05: 31 ready cards
// pinned to friends down for the week or held, some for 20 hours, with no judgment;
// 2026-10-04: "pins are the exception; hard-pin only true ownership"). Past the
// friend_idle setting, the pin rule unpins a generator's preference and logs it, and a
// true ownership (the brief's `WHO: friend <name> (owner)`, or a rating card) raises one
// judgment per friend naming her cards with the unpin printed complete.

// pinOpen is the open pin judgments, by subject.
func pinOpen(w *world) map[string]Note {
	out := map[string]Note{}
	for _, o := range w.s.Open {
		if o.Note.Type == NPinnedDown {
			out[o.Subject()] = o.Note
		}
	}
	return out
}

// unpinCommand is the judgment's decision that unpins, printed complete.
func unpinCommand(t *testing.T, n Note) string {
	t.Helper()
	for _, d := range n.Decisions {
		if strings.HasPrefix(d, "nova-sprint unpin ") {
			return d
		}
	}
	t.Fatalf("no unpin decision in %v", n.Decisions)
	return ""
}

func TestACardPinnedToADownFriendRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	w := friendWorld(t,
		friendBrief("only friend amy"),    // s1-1: a generator's preference
		friendBrief("friend amy (owner)"), // s1-2: true ownership
		friendBrief("only friend amy"),    // s1-3: a preference
		friendBrief("only friend zhi"),    // s1-4: a preference, zhi held
		friendBrief("friend zhi (owner)"), // s1-5: zhi's own
		friendBrief("friend amy"),         // s1-6: a soft preference: never stuck
		friendBrief("only friend bob"),    // s1-7: bob is up
	)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "rate-amy-1", Brief: friendBrief("only friend amy")}}}))
	require.Equal(t, "only."+FriendRow("amy"), w.s.Primary("s1-2").F(FieldWho), "the owner mark is a hard pin")
	assert.True(t, PinOwned(w.s.Primary("s1-2")))
	assert.True(t, PinOwned(w.s.Primary("rate-amy-1")), "a rating card is its friend's own")
	assert.False(t, PinOwned(w.s.Primary("s1-1")))

	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Down}, {Name: "zhi", Width: 1, Status: Held}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}
	pin := func(r TickReq) Plan {
		t.Helper()
		p, _ := TickRulePin(w.s, r)
		return w.must(p)
	}
	r := TickReq{Friends: seats, AnswerRules: true}

	// the episode begins: nothing is judged or unpinned inside friend_idle
	p := pin(r)
	assert.Empty(t, p.Units)
	assert.Empty(t, pinOpen(w))
	w.tick(19 * time.Minute)
	p = pin(r)
	assert.Empty(t, p.Units)
	assert.Empty(t, pinOpen(w))

	// past friend_idle (20 minutes by default): the preferences unpinned by rule and
	// logged, one judgment per friend for her own cards
	w.tick(2 * time.Minute)
	p = pin(r)
	for _, id := range []string{"s1-1", "s1-3", "s1-4"} {
		assert.Empty(t, w.s.Primary(id).F(FieldWho), "%s: a preference is unpinned by rule", id)
		assert.Contains(t, w.s.Primary(id).F(FieldRuleAnswer), RulePin+": ")
	}
	var logged []string
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == "WHO unpinned" {
				logged = append(logged, n.Primaries...)
				assert.Equal(t, ruleWho(RulePin), n.Who)
				assert.Contains(t, n.What, NRuleAnswered+" "+RulePin)
			}
		}
	}
	assert.ElementsMatch(t, []string{"s1-1", "s1-3", "s1-4"}, logged, "each unpin is logged")
	for id, who := range map[string]string{"s1-2": "only.friend.amy", "rate-amy-1": "only.friend.amy", "s1-5": "only.friend.zhi", "s1-6": "friend.amy", "s1-7": "only.friend.bob"} {
		assert.Equal(t, who, w.s.Primary(id).F(FieldWho), "%s keeps its pin", id)
	}
	open := pinOpen(w)
	require.Len(t, open, 2, "one judgment per friend: %v", open)
	amy, zhi := open[FriendRow("amy")], open[FriendRow("zhi")]
	assert.Equal(t, Judgment, amy.Kind)
	assert.Contains(t, amy.What, "rate-amy-1")
	assert.Contains(t, amy.What, "s1-2")
	assert.NotContains(t, amy.What, "s1-1")
	assert.Contains(t, amy.What, "down")
	assert.Contains(t, zhi.What, "held")
	cmd := unpinCommand(t, amy)
	assert.True(t, strings.HasPrefix(cmd, "nova-sprint unpin rate-amy-1 s1-2 --reason '"), cmd)
	assert.True(t, strings.HasSuffix(cmd, "'"), cmd)
	assert.Equal(t, "nova-sprint unpin s1-5 --reason '", unpinCommand(t, zhi)[:len("nova-sprint unpin s1-5 --reason '")])
	assert.Contains(t, amy.Decisions, "ack")

	// the printed command does what it says
	reason := strings.TrimSuffix(strings.SplitN(cmd, "--reason '", 2)[1], "'")
	q := Unpin(w.s, UnpinReq{IDs: []string{"rate-amy-1", "s1-2"}, Reason: reason, Who: "tester"})
	require.Empty(t, q.Refused)
	require.Len(t, q.Units, 2)

	// once a friend: the next tick raises nothing new
	w.tick(time.Minute)
	p = pin(r)
	assert.Empty(t, p.Notes)
	assert.Empty(t, p.Units)
	assert.Len(t, pinOpen(w), 2)

	// amy up: her judgment closes and her episode ends
	seats[0].Status = Up
	p = pin(TickReq{Friends: seats, AnswerRules: true})
	open = pinOpen(w)
	assert.NotContains(t, open, FriendRow("amy"))
	assert.Contains(t, open, FriendRow("zhi"))
	_, had := w.s.Fleet.Prop(PropPinDownSince("amy"))
	v, _ := w.s.Fleet.Prop(PropPinDownSince("amy"))
	assert.True(t, !had || v == "", "her episode is cleared")
}

// With the rules off, nothing is unpinned by the machine: every pinned card waits behind a
// judgment instead, and the friend_idle setting is the bound.
func TestAPinnedCardWithTheRulesOffIsJudgedAtTheFriendIdleSetting(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"))
	w.must(Plan{Props: []PropWrite{{Table: Work, Name: PropFriendIdle, Value: "1h", WasAbsent: true}}})
	r := TickReq{Friends: []FriendSeat{{Name: "amy", Width: 1, Status: Down}}}
	pin := func() Plan {
		t.Helper()
		p, _ := TickRulePin(w.s, r)
		return w.must(p)
	}
	pin()
	w.tick(30 * time.Minute)
	pin()
	assert.Empty(t, pinOpen(w), "inside the friend_idle setting")
	w.tick(31 * time.Minute)
	p := pin()
	assert.Empty(t, p.Units, "the rules are off: no unpin")
	assert.Equal(t, "only.friend.amy", w.s.Primary("s1-1").F(FieldWho))
	open := pinOpen(w)
	require.Len(t, open, 1)
	assert.True(t, strings.HasPrefix(unpinCommand(t, open[FriendRow("amy")]), "nova-sprint unpin s1-1 --reason '"))
}
