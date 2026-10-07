package sprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// TestAFriendIsToldVerifiedAndProbedForEveryTiersModel: a friend's row maps each tier she
// serves to the model she runs it on (docs/SPEC-FRIEND.md, a friend's models). On the
// in-memory twin a pro card dealt to a friend whose row maps pro to a model carries that
// model on its work card and in its packet, and her brief's tier line says it; a one-shot
// lane reads it off her queue file and launches a fake harness with the model's flag; her
// finish is verified against it (a report naming no model, or another, is refused); a tier of
// hers with no model is dealt with none and draws the check's warning, on her config row and
// on her queue file's row; and a row whose every card runs on her session's model and names
// two models is refused. Proven before first use: a tier mapped to a model is dealt no real
// card until her probe of it reports that model (a hard-pinned card waits ready for her), a
// probe naming another model keeps it closed, and a changed model is a new probe. Pro draws
// pro (it is the tier a pro card is dealt on); flash is the tier every heavy ceiling starts on.
func TestAFriendIsToldVerifiedAndProbedForEveryTiersModel(t *testing.T) {
	t.Parallel()
	const opus = "claude-opus-5-5"

	// proven before first use: her pro model has answered no probe, so a pro card pinned
	// to her waits ready, and no machine is dealt it
	pw := friendWorld(t, "c: work tier: pro\nWHO: only friend amy\n\nThe task.")
	fresh := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "pro,flash", Models: map[string]string{"pro": opus}}
	assert.Equal(t, []string{"pro"}, FriendProbesOwed(fresh.Models, fresh.Probes), "the tier with a model is owed its probe; the tier with none is not")
	dealWith(pw, fresh)
	require.Equal(t, Ready, pw.s.StateOf("s1-1"), "no pro card reaches her before her probe returns")
	require.Nil(t, pw.s.Fleet.Card("s1-1.w1"))
	job := FriendProbeJob("pro", opus)
	assert.Equal(t, "probe-pro-"+opus, job)
	pb := FriendProbeBrief("amy", "pro", opus)
	assert.Contains(t, pb, "outbox/"+job+"/REPORT.md")
	assert.Contains(t, pb, "tier: pro model: "+opus)
	assert.Contains(t, pb, "run this probe in a child on "+opus)
	// a probe that names another model keeps the tier closed
	fresh.Probes = map[string]string{"pro": ReportModel("Verdict: LAND\nModel: claude-haiku-4-5-20251001\nHarness: claude\n")}
	dealWith(pw, fresh)
	require.Equal(t, Ready, pw.s.StateOf("s1-1"), "a probe naming another model proves nothing")
	// the probe naming her row's model opens it
	fresh.Probes = map[string]string{"pro": ReportModel("Verdict: LAND\nModel: " + opus + "\nHarness: claude\n")}
	assert.Empty(t, FriendProbesOwed(fresh.Models, fresh.Probes))
	dealWith(pw, fresh)
	require.NotNil(t, pw.s.Fleet.Card("s1-1.w1"), "her pro cards are dealt once the probe returned her row's model")
	// a changed model is a new probe, by its own job
	assert.False(t, FriendProven(map[string]string{"pro": "claude-fable-5-1"}, fresh.Probes, "pro"))
	assert.NotEqual(t, job, FriendProbeJob("pro", "claude-fable-5-1"))

	// told: the packet of a pro card dealt to her carries her row's pro model
	w := friendWorld(t, "c: work tier: pro\n\nThe task.", "c: work tier: flash\n\nThe task.")
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "pro,flash", Models: map[string]string{"pro": opus}, Probes: map[string]string{"pro": opus}}
	dealWith(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "the pro card is dealt to her")
	require.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, opus, wc.F(FieldModel), "the work card carries her row's model for its tier")
	assert.Equal(t, "pro", wc.F(FieldTier), "and the tier it was dealt on")
	p := PacketOf("sprint", 0, wc, w.s.Primary("s1-1"), nil, nil)
	assert.Equal(t, opus, p.Model, "the packet carries the model")
	assert.Equal(t, "pro", p.Tier)
	assert.Equal(t, "tier: pro model: "+opus, FriendTierLine(p.Tier, p.Model), "the brief's tier line")

	// a tier of hers with no model is still dealt, with no model: she runs it on her session's
	flashCard := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, flashCard, "a tier with no model is still served")
	assert.Empty(t, flashCard.F(FieldModel), "and its card carries no model")
	assert.Equal(t, "tier: flash", FriendTierLine("flash", ""))

	// ... and draws the check's warning, on her config row
	row := config.Row{Name: "amy", Fields: map[string]string{"tiers": "pro,flash", "model": "pro=" + opus, "mode": "batch", "children": "yes", "child_model": "yes"}}
	warn := config.FriendModelWarnings(row)
	require.Len(t, warn, 1)
	assert.Contains(t, warn[0], "serves tier flash with no model")
	k, _ := config.Lookup(config.KindFriend)
	require.NoError(t, k.Check(row), "a model for each tier is no refusal, nor is a tier with none")

	// a row whose every card runs on her session's model (no children) and names two models
	// asks more of her harness than it can do, and is refused; one model is not
	one := config.Row{Name: "fred", Fields: map[string]string{"tiers": "pro,flash", "model": "pro=m1,flash=m2", "mode": "batch", "children": "no", "child_model": "no"}}
	assert.ErrorContains(t, k.Check(one), "her harness cannot run them")
	one.Fields["model"] = "pro=m1,flash=m1"
	assert.NoError(t, k.Check(one))
	assert.Equal(t, 1, config.FriendWidth(config.Row{Fields: map[string]string{"width": "8", "mode": "batch", "children": "no"}}), "no children in one session is one card at a time, whatever the width")
	assert.Equal(t, 12, config.FriendWidth(config.Row{Fields: map[string]string{"width": "12", "mode": "one-shot", "children": "no"}}), "lanes are width with no children")
	assert.ErrorContains(t, k.Check(config.Row{Name: "amy", Fields: map[string]string{"tiers": "flash", "model": "pro=" + opus}}), "which she does not serve")

	// a one-shot lane: her queue file carries the card's model, and the lane's turn
	// launches the harness with its model flag
	dir := t.TempDir()
	in := filepath.Join(dir, "inbox", "s1-1.w1")
	require.NoError(t, os.MkdirAll(in, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(in, "BRIEF.md"), []byte("STATUS: x\n"+FriendTierLine(p.Tier, p.Model)+"\n"), 0o644))
	qrow := &friend.QueueRow{Tiers: []string{"pro", "flash"}, Models: map[string]string{"pro": opus}, Mode: "one-shot", Width: 2}
	q, err := json.Marshal(friend.Queue{Row: qrow, Tasks: []friend.Task{{ID: "s1-1.w1", Gen: 1, Job: "s1-1.w1", State: "queued", Tier: p.Tier, Model: p.Model}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), q, 0o644))
	c, found, err := friend.NextCard(dir, func(friend.Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, opus, c.Model, "the lane's card carries the model")
	text := friend.CardText(c, 1, 2, "nova-bus send ...", "", "", "", nil)
	assert.Contains(t, text, "This card runs on "+opus+" (tier pro)", "the lane's turn says it")
	var ran [][]string
	fake := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		ran = append(ran, append([]string{name}, args...))
		return "", 0, nil
	}
	oc := &friend.OpenCode{Dir: dir, Run: fake}
	_, err = oc.DeliverTo(friend.WithModel(friend.LaneContext(context.Background()), c.Model), "ses_1", text)
	require.NoError(t, err)
	require.Len(t, ran, 1)
	assert.Equal(t, []string{"opencode", "run", "--model", opus, "--session", "ses_1", text}, ran[0], "the harness is launched with the model flag")
	_, err = oc.DeliverTo(friend.LaneContext(context.Background()), "ses_1", text)
	require.NoError(t, err)
	assert.Equal(t, []string{"opencode", "run", "--session", "ses_1", text}, ran[1], "a card with no model is launched with no flag")

	// the queue file's row: what whoami prints, and the check's warning for the tier with none
	got, found, err := friend.ReadQueueRow(dir)
	require.NoError(t, err)
	require.True(t, found)
	lines := friend.WhoAmILines("amy", dir, "ses_1", got, "opencode")
	assert.Contains(t, lines[0], "models=pro="+opus+",flash=-")
	assert.Contains(t, lines[0], "how=lane")
	mf := friend.CheckModels("amy", got, "opencode")
	require.Len(t, mf.Warnings, 1)
	assert.Contains(t, mf.Warnings[0], "tier flash has no model")
	assert.Empty(t, mf.Refused)
	assert.NotEmpty(t, friend.CheckModels("amy", got, "codex").Refused, "a one-shot row on a harness that opens no lane on a model is refused")
	sess := friend.QueueRow{Tiers: []string{"pro", "flash"}, Models: map[string]string{"pro": "m1", "flash": "m2"}, Children: "no", Width: 8}
	assert.NotEmpty(t, friend.CheckModels("fred", sess, "claude").Refused, "her session's one model cannot run two")
	assert.Equal(t, 1, sess.Lanes())

	// verified on every finish
	assert.Empty(t, FriendModelMismatch(opus, "Verdict: LAND\nHead: abc\nModel: "+opus+"\n"))
	assert.Empty(t, FriendModelMismatch(opus, "Verdict: LAND\nUsage: tokens=12 model="+opus+"\n"), "the usage line's model= word counts")
	assert.Contains(t, FriendModelMismatch(opus, "Verdict: LAND\nModel: claude-haiku-4-5-20251001\n"), "ran on claude-haiku-4-5-20251001")
	assert.Contains(t, FriendModelMismatch(opus, "Verdict: LAND\n"), "names no model")
	assert.Empty(t, FriendModelMismatch("", "Verdict: LAND\n"), "a card with no model is not checked")

	// a card the level would move goes only to a friend whose row names a model for its
	// tier: bob takes pro with none, so amy's pro cards are refused him, the refusal
	// naming the tier; cat names one, and a card moved to her carries her model, not amy's
	const fable = "claude-fable-5-1"
	var pros []string
	for range 6 {
		pros = append(pros, "c: work tier: pro\nWHO: friend\n\nThe task.")
	}
	lw := friendWorld(t, pros...)
	giver := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "pro", Models: map[string]string{"pro": opus}, Probes: map[string]string{"pro": opus}}
	dealStarted(lw, giver)
	require.Equal(t, 2, lw.s.Fleet.Count(FriendRow("amy"), Ready), "two of amy's pro cards wait behind her lanes")
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "pro"}
	lp := FriendLevel(lw.s, FriendLevelReq{Seats: []FriendSeat{giver, bob}})
	assert.Empty(t, lp.Units, "no card carrying amy's model moves to bob, whose row names no pro model")
	require.Len(t, lp.Refused, 2, "each card the level would have moved is refused")
	for _, r := range lp.Refused {
		assert.Contains(t, r.Why, "tier pro has no model on the row of friend bob", "the refusal names the tier and the friend")
		assert.Equal(t, opus, lw.s.Fleet.Card(r.Key).F(FieldModel), "and the card stays amy's, on her model")
	}
	// the start bound's level refuses him the same way: amy's cards dealt and not started
	uw := friendWorld(t, pros[:2]...)
	dealWith(uw, giver)
	require.Equal(t, 2, uw.s.Fleet.Count(FriendRow("amy"), Ready))
	up := friendUnstartedLevel(uw.s, []FriendSeat{giver, bob}, func(string) (time.Duration, bool) { return 24 * time.Hour, true }, nil, nil, 0)
	assert.Empty(t, up.Units, "no card past the start bound moves to bob")
	require.Len(t, up.Refused, 2)
	for _, r := range up.Refused {
		assert.Contains(t, r.Why, "tier pro has no model on the row of friend bob")
	}
	cat := FriendSeat{Name: "cat", Width: 2, Status: Up, Class: "pro", Models: map[string]string{"pro": fable}, Probes: map[string]string{"pro": fable}}
	up = uw.must(friendUnstartedLevel(uw.s, []FriendSeat{giver, bob, cat}, func(string) (time.Duration, bool) { return 24 * time.Hour, true }, nil, nil, 0))
	require.Len(t, up.Units, 2, "cat names a pro model: past the start bound they move to her")
	for _, u := range up.Units {
		mc := uw.s.Fleet.Card(u.Key)
		assert.Equal(t, FriendRow("cat"), mc.Row)
		assert.Equal(t, fable, mc.F(FieldModel), "with her model")
	}
	lp = lw.must(FriendLevel(lw.s, FriendLevelReq{Seats: []FriendSeat{giver, bob, cat}}))
	require.NotEmpty(t, lp.Units, "cat names a pro model: amy's waiting cards move to her")
	for _, u := range lp.Units {
		mc := lw.s.Fleet.Card(u.Key)
		require.Equal(t, FriendRow("cat"), mc.Row, "never to bob")
		assert.Equal(t, fable, mc.F(FieldModel), "the moved card carries the receiver's model, never the giver's")
		mp := PacketOf("sprint", 0, mc, lw.s.Primary(mc.F("primary")), nil, nil)
		assert.Equal(t, "tier: pro model: "+fable, FriendTierLine(mp.Tier, mp.Model), "and her brief's tier line says hers")
	}

	// her packets carry her row's model for the tier, never the card's: a work card carrying
	// amy's model, read for a friend whose row names none for its tier, has no model, so her
	// brief has no tier line, her lane no model flag and her finish no model check
	packets := []Packet{{Card: "s1-9.w1", Kind: "work", Tier: "pro", Model: opus}, {Card: "s1-9.r1", Kind: "read", Tier: "pro", Model: opus}}
	FriendRowModels(packets, nil)
	assert.Empty(t, packets[0].Model, "a tier her row names no model for clears the carried one")
	assert.Equal(t, opus, packets[1].Model, "a read packet is left as it is")
	FriendRowModels(packets, map[string]string{"pro": fable})
	assert.Equal(t, fable, packets[0].Model, "her row's model, not the one the card carried")
}
