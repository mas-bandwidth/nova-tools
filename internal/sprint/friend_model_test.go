package sprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// TestAFriendIsToldVerifiedAndProbedForEveryTiersModel: a friend's row maps each tier she
// serves to the model she runs it on (docs/SPEC-FRIEND.md, a friend's models). On the
// in-memory twin a heavy card dealt to a friend whose row maps heavy to a model carries that
// model on its work card and in its packet, and her brief's tier line says it; a one-shot
// lane reads it off her queue file and launches a fake harness with the model's flag; her
// finish is verified against it (a report naming no model, or another, is refused); a tier of
// hers with no model is dealt with none and draws the check's warning, on her config row and
// on her queue file's row; and a row whose every card runs on her session's model and names
// two models is refused. The probe card of a tier newly added is not built (the report of
// friend-tier-models-g.w1 says so).
func TestAFriendIsToldVerifiedAndProbedForEveryTiersModel(t *testing.T) {
	t.Parallel()
	const heavy = "claude-opus-5-5"

	// told: the packet of a heavy card dealt to her carries her row's heavy model
	w := friendWorld(t, "c: work tier: heavy\n\nThe task.", "c: work tier: pro\n\nThe task.")
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "heavy,pro", Models: map[string]string{"heavy": heavy}}
	dealWith(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "the heavy card is dealt to her")
	require.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, heavy, wc.F(FieldModel), "the work card carries her row's model for its tier")
	assert.Equal(t, "heavy", wc.F(FieldTier), "and the tier it was dealt on")
	p := PacketOf("sprint", 0, wc, w.s.Primary("s1-1"), nil, nil)
	assert.Equal(t, heavy, p.Model, "the packet carries the model")
	assert.Equal(t, "heavy", p.Tier)
	assert.Equal(t, "tier: heavy model: "+heavy, FriendTierLine(p.Tier, p.Model), "the brief's tier line")

	// a tier of hers with no model is still dealt, with no model: she runs it on her session's
	pro := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, pro, "a tier with no model is still served")
	assert.Empty(t, pro.F(FieldModel), "and its card carries no model")
	assert.Equal(t, "tier: pro", FriendTierLine("pro", ""))

	// ... and draws the check's warning, on her config row
	row := config.Row{Name: "amy", Fields: map[string]string{"tiers": "heavy,pro", "model": "heavy=" + heavy, "mode": "batch", "children": "yes", "child_model": "yes"}}
	warn := config.FriendModelWarnings(row)
	require.Len(t, warn, 1)
	assert.Contains(t, warn[0], "serves tier pro with no model")
	k, _ := config.Lookup(config.KindFriend)
	require.NoError(t, k.Check(row), "a model for each tier is no refusal, nor is a tier with none")

	// a row whose every card runs on her session's model (no children) and names two models
	// asks more of her harness than it can do, and is refused; one model is not
	one := config.Row{Name: "fred", Fields: map[string]string{"tiers": "heavy,pro", "model": "heavy=m1,pro=m2", "mode": "batch", "children": "no", "child_model": "no"}}
	assert.ErrorContains(t, k.Check(one), "her harness cannot run them")
	one.Fields["model"] = "heavy=m1,pro=m1"
	assert.NoError(t, k.Check(one))
	assert.Equal(t, 1, config.FriendWidth(config.Row{Fields: map[string]string{"width": "8", "mode": "batch", "children": "no"}}), "no children in one session is one card at a time, whatever the width")
	assert.Equal(t, 12, config.FriendWidth(config.Row{Fields: map[string]string{"width": "12", "mode": "one-shot", "children": "no"}}), "lanes are width with no children")
	assert.ErrorContains(t, k.Check(config.Row{Name: "amy", Fields: map[string]string{"tiers": "pro", "model": "heavy=" + heavy}}), "which she does not serve")

	// a one-shot lane: her queue file carries the card's model, and the lane's turn
	// launches the harness with its model flag
	dir := t.TempDir()
	job := filepath.Join(dir, "inbox", "s1-1.w1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(job, "BRIEF.md"), []byte("STATUS: x\n"+FriendTierLine(p.Tier, p.Model)+"\n"), 0o644))
	qrow := &friend.QueueRow{Tiers: []string{"heavy", "pro"}, Models: map[string]string{"heavy": heavy}, Mode: "one-shot", Width: 2}
	q, err := json.Marshal(friend.Queue{Row: qrow, Tasks: []friend.Task{{ID: "s1-1.w1", Gen: 1, Job: "s1-1.w1", State: "queued", Tier: p.Tier, Model: p.Model}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), q, 0o644))
	c, found, err := friend.NextCard(dir, func(friend.Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, heavy, c.Model, "the lane's card carries the model")
	text := friend.CardText(c, 1, 2, "nova-bus send ...", "", "", nil)
	assert.Contains(t, text, "This card runs on "+heavy+" (tier heavy)", "the lane's turn says it")
	var ran [][]string
	fake := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		ran = append(ran, append([]string{name}, args...))
		return "", 0, nil
	}
	oc := &friend.OpenCode{Dir: dir, Run: fake}
	_, err = oc.DeliverTo(friend.WithModel(friend.LaneContext(context.Background()), c.Model), "ses_1", text)
	require.NoError(t, err)
	require.Len(t, ran, 1)
	assert.Equal(t, []string{"opencode", "run", "--model", heavy, "--session", "ses_1", "--dir", dir, text}, ran[0], "the harness is launched with the model flag")
	_, err = oc.DeliverTo(friend.LaneContext(context.Background()), "ses_1", text)
	require.NoError(t, err)
	assert.Equal(t, []string{"opencode", "run", "--session", "ses_1", "--dir", dir, text}, ran[1], "a card with no model is launched with no flag")

	// the queue file's row: what whoami prints, and the check's warning for the tier with none
	got, found, err := friend.ReadQueueRow(dir)
	require.NoError(t, err)
	require.True(t, found)
	lines := friend.WhoAmILines("amy", dir, "ses_1", got, "opencode")
	assert.Contains(t, lines[0], "models=heavy="+heavy+",pro=-")
	assert.Contains(t, lines[0], "how=lane")
	mf := friend.CheckModels("amy", got, "opencode")
	require.Len(t, mf.Warnings, 1)
	assert.Contains(t, mf.Warnings[0], "tier pro has no model")
	assert.Empty(t, mf.Refused)
	assert.NotEmpty(t, friend.CheckModels("amy", got, "codex").Refused, "a one-shot row on a harness that opens no lane on a model is refused")
	sess := friend.QueueRow{Tiers: []string{"heavy", "pro"}, Models: map[string]string{"heavy": "m1", "pro": "m2"}, Children: "no", Width: 8}
	assert.NotEmpty(t, friend.CheckModels("fred", sess, "claude").Refused, "her session's one model cannot run two")
	assert.Equal(t, 1, sess.Lanes())

	// verified on every finish
	assert.Empty(t, FriendModelMismatch(heavy, "Verdict: LAND\nHead: abc\nModel: "+heavy+"\n"))
	assert.Empty(t, FriendModelMismatch(heavy, "Verdict: LAND\nUsage: tokens=12 model="+heavy+"\n"), "the usage line's model= word counts")
	assert.Contains(t, FriendModelMismatch(heavy, "Verdict: LAND\nModel: claude-haiku-4-5-20251001\n"), "ran on claude-haiku-4-5-20251001")
	assert.Contains(t, FriendModelMismatch(heavy, "Verdict: LAND\n"), "names no model")
	assert.Empty(t, FriendModelMismatch("", "Verdict: LAND\n"), "a card with no model is not checked")
}
