package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The head trap: a finish without --head records the card's id as its head,
// which land cannot merge. The worker's packet names --head <commit> on its
// report line, so the command it pastes names the commit.
func TestThePacketsReportLineNamesTheHead(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	out := ta.ok("take --as m1 s1-1.w1@1")
	assert.Contains(t, out, "  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]")
	ta.clean()
}

// landResult is land's --json, whatever its exit.
type landResult struct {
	Status string      `json:"status"`
	Items  []landBatch `json:"items"`
}

// landJSON runs a land line with --json and returns its exit and its result.
func (r *landRig) landJSON(line string) (int, landResult) {
	r.t.Helper()
	code, out, errs := r.do(line + " --json")
	var v landResult
	require.NoError(r.t, json.Unmarshal([]byte(out), &v), "%s: %s%s", line, out, errs)
	return code, v
}

// queuedOneWithoutHead takes s1-1 to the merge queue at a pushed commit and
// s1-2 behind it finished with no --head, its head the card's id.
func (r *landRig) queuedOneWithoutHead() {
	r.t.Helper()
	r.ok("add --stream s1 --count 2")
	head := r.head("s1-1", "main", "a.txt", "a\n")
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	r.deal(2)
	r.ok("take --as m1 --limit 100")
	r.ok("finish --as m1 s1-1.w1@1 --head " + head)
	r.ok("finish --as m1 s1-2.w1@1")
	r.ok("ask")
	r.ok("read --as reader-a --ok --limit 100")
	r.ok("read --as reader-b --ok --limit 100")
	r.ok("accept --read-ok")
}

// land --dry-run refuses a head that is not a commit id exactly as land does:
// the cards before it a batch that lands, that card refused with the conflict
// fact and the same words, and nothing recorded by the dry run; then land
// itself refuses it in those words.
func TestLandDryRunRefusesAHeadThatIsNotACommitAsLandDoes(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.queuedOneWithoutHead()
	applies := r.applies()
	land := "land --repo-dir " + r.clone + " --base main"

	code, dry := r.landJSON(land + " --dry-run")
	assert.Equal(t, 1, code)
	assert.Equal(t, "refused", dry.Status)
	require.Len(t, dry.Items, 2)
	assert.Equal(t, "ok", dry.Items[0].Status)
	assert.Equal(t, []string{"s1-1"}, dry.Items[0].IDs)
	assert.Equal(t, "refused", dry.Items[1].Status)
	assert.Equal(t, []string{"s1-2"}, dry.Items[1].IDs)
	assert.Equal(t, "conflict", dry.Items[1].WouldRecord)
	assert.Empty(t, dry.Items[1].Fact, "a dry run records no fact")
	assert.Contains(t, dry.Items[1].Reason, "the head s1-2.w1 of s1-2 is not a commit id")
	assert.Contains(t, dry.Items[1].Reason, "--head <commit>")

	code, _, errs := r.do(land + " --dry-run")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 would_record=conflict dry_run=yes reason=the head s1-2.w1 of s1-2 is not a commit id")
	assert.Contains(t, errs, "NOTE land would report this as merge --conflict and stop stream s1; nothing was reported (dry run)")
	assert.Equal(t, applies, r.applies(), "a dry run wrote")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))

	code, real := r.landJSON(land)
	assert.Equal(t, 1, code)
	require.Len(t, real.Items, 2)
	assert.Equal(t, "ok", real.Items[0].Status)
	assert.Equal(t, "conflict", real.Items[1].Fact)
	assert.Equal(t, dry.Items[1].Reason, real.Items[1].Reason, "the dry run's words are land's")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
}

// A batch land cannot place is refused naming every problem at once, the dry
// run and land alike: no BASE:, no REPO:, and the head that is not a commit
// id it would meet next, with one land line that supplies both flags.
func TestLandNamesEveryProblemOfABatchAtOnce(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.queuedOneWithoutHead()
	applies := r.applies()
	for _, line := range []string{"land --dry-run", "land"} {
		code, _, errs := r.do(line)
		assert.Equal(t, 1, code, line)
		require.Contains(t, errs, "reason=", line)
		reason := errs[strings.Index(errs, "reason="):]
		for _, want := range []string{
			"card s1-1 names no BASE: line and no --base was given, and the card names no REPO: line and no --repo-dir was given; run: nova-sprint land --stream s1 --base <branch> --repo-dir <clone>",
			"the head s1-2.w1 of s1-2 is not a commit id",
		} {
			assert.Contains(t, reason, want, line)
		}
	}
	assert.Equal(t, applies, r.applies(), "a refusal before any git wrote")
}
