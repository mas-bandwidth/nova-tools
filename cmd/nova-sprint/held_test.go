package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// held lists the held cards and what each waits on, and sentinels the sentinels
// and what each gates, from one read each (the comfort list of 2026-10-03, item
// 1: finding what a wave waits on took two children an hour of card calls).
func TestHeldAndSentinelsListWhatAWaveWaitsOn(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 2")
	ta.ok("add --stream a --sentinel a-stop")
	ta.ok("add --stream a a-3 a-4 --needs a-1")
	ta.ok("add --one --stream a a-5 --held")
	ta.ok("add --one --stream b --count 1")
	ta.ok("add --one --stream b b-2 --needs b-1 --held")

	out := ta.ok("held")
	assert.Contains(t, out, "HELD a-3 stream=a held=- behind=a-stop needs=a-1\n")
	assert.Contains(t, out, "HELD a-4 stream=a held=- behind=a-stop needs=a-1\n")
	assert.Contains(t, out, "HELD a-5 stream=a held=yes behind=a-stop needs=-\n")
	assert.Contains(t, out, "HELD b-2 stream=b held=yes behind=- needs=b-1\n")
	assert.Contains(t, out, "HELD OK cards=4 held=2 behind=3 streams=0 members=0\n")
	assert.NotContains(t, out, "HELD a-1 ")
	assert.Contains(t, ta.ok("held --stream b"), "HELD OK cards=1 held=1 behind=0 streams=0 members=0\n")

	out = ta.ok("sentinels")
	assert.Contains(t, out, "SENTINEL a-stop stream=a reached=- behind=3 needs=-\n")
	assert.Contains(t, out, "SENTINELS OK sentinels=1\n")
	assert.Contains(t, ta.ok("sentinels --stream b"), "SENTINELS OK sentinels=0\n")

	var h struct{ Cards []heldCard }
	ta.json("held --stream a", &h)
	if assert.Len(t, h.Cards, 3) {
		assert.Equal(t, heldCard{ID: "a-3", Stream: "a", Behind: []string{"a-stop"}, Needs: []string{"a-1"}}, h.Cards[0])
	}
	var sv struct{ Sentinels []sentinelCard }
	ta.json("sentinels", &sv)
	assert.Equal(t, []sentinelCard{{ID: "a-stop", Stream: "a", Behind: 3}}, sv.Sentinels)

	code, _, errs := ta.do("held a-1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "takes no words")
}

// A stream's or a member's hold is a hold `held` names with its reason and age,
// even when the stream hold left no work card waiting: the defect of 2026-10-07
// was a stream hold `held` printed 0 for while 186 cards sat idle.
func TestHeldListsHeldStreamsAndMembers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 2")
	ta.ok("hold a m1 --reason red")

	out := ta.ok("held")
	assert.Contains(t, out, "HELD-TARGET a kind=stream reason=red")
	assert.Contains(t, out, "HELD-TARGET m1 kind=member reason=red")
	assert.Contains(t, out, "HELD OK cards=0 held=0 behind=0 streams=1 members=1\n")
	assert.NotContains(t, out, "HELD a-1 ")

	var v struct{ Holds []heldTarget }
	ta.json("held", &v)
	if assert.Len(t, v.Holds, 2) {
		assert.Equal(t, heldTarget{Name: "a", Kind: "stream", Reason: "red", At: v.Holds[0].At, Age: v.Holds[0].Age}, v.Holds[0])
		assert.Equal(t, "member", v.Holds[1].Kind)
		assert.Equal(t, "m1", v.Holds[1].Name)
	}
	assert.NotEmpty(t, v.Holds[0].Age)
}
