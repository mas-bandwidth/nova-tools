package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// costs retier (retier.go; internal/sprint/retier.go) and an api friend's usage
// (friendusage.go), the owner, 2026-10-04 4:22 to 4:45 PM.

// setFields writes fields of the primary id on the twin as a hand edit would, unsetting
// those named: a card made to look as one written before records carried a tier.
func (ta *testApp) setFields(id string, set map[string]string, unset []string) {
	ta.t.Helper()
	ctx := context.Background()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	c := snap.Work.Card(id)
	require.NotNil(ta.t, c, id)
	_, err = ta.m.Apply(ctx, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Work), Epoch: fmt.Sprint(st.PinnedEpoch()),
		ExpectedTableRevision: fmt.Sprint(snap.Work.Revision), OperationID: fmt.Sprintf("set-%s-%d", id, len(set)+len(unset)),
		Members: []ntable.BatchMemberEntry{{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)}, Set: set, Unset: unset}}})
	require.NoError(ta.t, err)
}

// untier makes the primary's records and totals as they were before records carried a
// tier: on_tier=- on every record, and no tier total.
func (ta *testApp) untier(id string) {
	ta.t.Helper()
	set, unset := map[string]string{}, []string{}
	for k, v := range ta.primary(id).Fields {
		switch {
		case strings.HasPrefix(k, sprint.FieldCostRecord):
			words := strings.Fields(v)
			for i, w := range words {
				if strings.HasPrefix(w, "on_tier=") {
					words[i] = "on_tier=-"
				}
			}
			set[k] = strings.Join(words, " ")
		case strings.HasPrefix(k, sprint.FieldCostTier):
			unset = append(unset, k)
		}
	}
	ta.setFields(id, set, unset)
}

// A landed card from before the tiers: where places its tiers by the read-time rule and
// says so (the alarm); costs retier --dry-run prints each stream's before and after and
// writes nothing; costs retier writes, the alarm is gone and the tiers still add up to the
// cost; a second run writes nothing.
func TestCostsRetierBackfillsTheTiersOnce(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"},
		[]string{"input=10 model=opencode/deepseek-v4-flash actual_usd=0.25 actual_by=harness", ""}, []string{"", ""})
	ta.untier("s1-1")
	code, out, errs := ta.do("where --json --costs")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, errs, "ALARM cost tiers: the read-time rule placed the tiers of 1 landed cards with no tier totals (s1=$1.25)")
	assert.Contains(t, out, `"cost_tier_guard":"$1.25"`)

	dry := ta.ok("costs retier --dry-run")
	assert.Contains(t, dry, "RETIER stream=s1 cards=1 records=3 cost=$3.25 before=pro:$2.00,no_tier:$1.25 after=flash:$0.25,pro:$3.00\n")
	assert.Contains(t, dry, "RETIER OK streams=1 cards=1 records=3 left=0: dry run, nothing was written")
	_, _, errs = ta.do("where --json --costs")
	assert.Contains(t, errs, "ALARM", "the dry run wrote nothing")

	assert.Contains(t, ta.ok("costs retier"), "RETIER OK streams=1 cards=1 records=3 left=0 written (a running machine applies them with its next tick)")
	var v whereView
	code, _, errs = ta.do("where --json --costs")
	require.Equal(t, 0, code)
	assert.NotContains(t, errs, "ALARM")
	ta.json("where --costs", &v)
	assert.Equal(t, "$3.25", v.Tables["work"]["s1"]["cost"])
	assert.Equal(t, map[string]any{"flash": "$0.25", "pro": "$3.00"}, v.Tables["work"]["s1"]["cost_by_tier"])
	assert.NotContains(t, v.Tables["work"]["s1"], "cost_tier_guard")
	assert.Contains(t, ta.ok("costs retier"), "nothing to write")
}

// sessionStore writes an OpenCode store at path with one session per row: directory,
// title, model JSON, cost and input tokens.
func sessionStore(t *testing.T, path string, rows [][5]string, createdMs int64) {
	t.Helper()
	if _, err := exec.LookPath(swarm.SQLiteBinary); err != nil {
		t.Skip("no sqlite3 on PATH")
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	sql := `CREATE TABLE session (id TEXT, directory TEXT, title TEXT, model TEXT, cost REAL, tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER, time_created INTEGER);`
	for i, r := range rows {
		sql += fmt.Sprintf(`INSERT INTO session VALUES ('s%d', '%s', '%s', '%s', %s, %s, 50, 0, 1000, 0, %d);`, i, r[0], r[1], r[2], r[3], r[4], createdMs)
	}
	out, err := exec.Command(swarm.SQLiteBinary, path, sql).CombinedOutput()
	require.NoError(t, err, string(out))
}

// An api friend (billing api on her nova-config row) is priced from her OpenCode
// sessions: friend sync says when it finds none at the finish, never estimating; costs
// retier --dry-run then names her cards found, priced and unrecovered and the total, and
// costs retier puts the usage in the record, priced on the model's route and tier. A
// subscription friend's work stays out of the dollars.
func TestAnApiFriendIsPricedFromHerSessions(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"billing": config.BillingAPI}, "t")
	require.NoError(t, err)
	ta.m.SetRoutes(costRoutes())
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	brief := filepath.Join(t.TempDir(), "s1-1.md")
	require.NoError(t, os.WriteFile(brief, []byte(passingBrief("s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", "**Verdict:** LAND\nHead: "+landHead+"\n\nDone.\n")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD USAGE friend=amy card=s1-1.w1 none: no OpenCode session of ")
	ta.ok("tick") // the finish is queued for the pump while the machine runs
	dir := filepath.Join(root, "amy-working")
	model := `{"id":"deepseek-v4-flash","providerID":"opencode"}`
	sessionStore(t, filepath.Join(dir, "opencode", "opencode.db"), [][5]string{
		{dir, "Amy one-shot s1-1.w1", model, "0.5", "1000"},
		{dir, "Amy one-shot s1-10.w1", model, "9", "9"}, // another card: not s1-1 as a whole word
	}, ta.a.now().UnixMilli())
	dry := ta.ok("costs retier --dry-run --root " + root)
	assert.Contains(t, dry, "RETIER FRIEND friend=amy billing=api cards=1 priced=1 unrecovered=0 total=$0.50 missing=-\n")
	ta.ok("costs retier --root " + root)
	ta.ok("tick") // a running machine applies the writes with its next pump
	var c cardView
	ta.json("card s1-1", &c)
	var work sprint.Consumer
	for _, con := range c.Cost.Consumers {
		if con.Kind == "work" {
			work = con
		}
	}
	assert.Equal(t, int64(1000), work.Usage.Tokens.Input)
	assert.Equal(t, "0.5", work.Usage.Actual)
	assert.Equal(t, "flash", work.Tier, "the model's route's tier")
	assert.Equal(t, "flash-a", work.Usage.Route, "priced on the model's route")
	assert.Contains(t, ta.ok("costs retier --dry-run --root "+root), "RETIER OK", "nothing left for her")
}

func TestTitleNamesTheCardAsAWholeWord(t *testing.T) {
	t.Parallel()
	assert.True(t, titleNames("Freddy one-shot fp-sec81-f4.w1", "fp-sec81-f4"))
	assert.True(t, titleNames("Card: fp-mach-10 (5th) (@general subagent)", "fp-mach-10"))
	assert.False(t, titleNames("Card: fp-mach-100", "fp-mach-10"))
	assert.False(t, titleNames("Card: xfp-mach-10", "fp-mach-10"))
	assert.Equal(t, "inception/mercury-2.5", openCodeModel(`{"id":"mercury-2.5","providerID":"inception","variant":"default"}`))
}
