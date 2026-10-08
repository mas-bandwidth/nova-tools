package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// A red base is said once, on the base, never on each batch (docs/SPEC-SPRINT.md section 7,
// the base gate first; the owner, 2026-10-07, 9:20 PM: anything found running the candidate
// is fixed as critical). These tests drive land over the rig's bare origin with the land
// loop's bench seam standing in for the bench: no socket, no ssh, no real time; the fake
// gate answers by the commit it is asked to gate.

// redBaseRig is the land rig with the loop's bench seam standing in for the benches: the
// rig's member m1 (up from init) is one bench, and extra members brought up are more.
type redBaseRig struct {
	*landRig
	benches []string // the extra benches, down while a card is dealt
	mu      sync.Mutex
	red     map[string]bool // the tips whose gate fails with treeRedOut
	all     bool            // every tip's gate fails
	asked   []string        // "<host> <tip>" per gate asked, in order
}

// treeRedOut is go test's output for the module's TestTree failing, as a bench prints it
// after the gate's run markers; host's seconds differ, as two benches' do.
func treeRedOut(host string) string {
	secs := map[string]string{"m1": "0.01s", "vision": "0.02s"}[host]
	if secs == "" {
		secs = "0.03s"
	}
	return "--- FAIL: TestTree (" + secs + ")\n    docs_test.go:17: NOTES.md says BAD\nFAIL\nFAIL\texample.com/m/internal/docs\t" + secs + "\nFAIL\n"
}

func newRedBaseRig(t *testing.T, benches ...string) *redBaseRig {
	t.Helper()
	r := &redBaseRig{landRig: newLandRig(t), benches: benches, red: map[string]bool{}}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	for _, m := range benches {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.flight = &landFlight{} // as the loop's land: the gate goes to a bench
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: r.env, OwnRepo: true}, "rev-parse", "HEAD")
		if err != nil {
			return "", 1, err
		}
		tip := strings.TrimSpace(string(res.Stdout))
		r.mu.Lock()
		defer r.mu.Unlock()
		r.asked = append(r.asked, host+" "+tip)
		var out strings.Builder
		for _, run := range runs {
			out.WriteString(gateMark + strings.Join(run, " ") + "\n")
		}
		// a tree that holds a red tip is red: a head merged onto the red base does not fix it
		red := r.all
		for t := range r.red {
			if _, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: r.env, OwnRepo: true}, "merge-base", "--is-ancestor", t, tip); err == nil {
				red = true
			}
		}
		if red {
			out.WriteString(treeRedOut(host))
			return out.String(), 1, nil
		}
		return out.String() + "ok\n", 0, nil
	}
	b.mu.Unlock()
	return r
}

// queueOne queues one card in stream, its brief naming the repository and main, its work
// the files given; the base is not moved. The extra benches are down while the card is
// dealt (the deal gives a card to any up member, and m1 takes it) and up again after.
func (r *redBaseRig) queueOne(stream, id string, files map[string]string) {
	r.t.Helper()
	for _, m := range r.benches {
		r.ok("fleet down " + m)
	}
	defer func() {
		for _, m := range r.benches {
			r.ok("fleet up " + m)
		}
	}()
	path := filepath.Join(r.t.TempDir(), id+".md")
	require.NoError(r.t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite the files of "+id+".")), 0o600))
	r.promotionStream(stream)
	heads := map[string]string{id: r.card(id, files)}
	r.ok("add --stream " + stream + " --brief-file " + path + " --one")
	r.queued(heads, id)
}

// mainTip is origin's tip of main, as the worker sees it after a fetch.
func (r *redBaseRig) mainTip() string {
	r.git(r.worker, "fetch", "-q", "origin")
	return r.git(r.worker, "rev-parse", "refs/remotes/origin/main")
}

// gates is every gate the fake bench was asked, "<host> <tip>", in order.
func (r *redBaseRig) gates() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

// openJudgment is the text of the one open base-red judgment, "" for none; n is how many
// are open.
func (r *redBaseRig) openJudgment() (what string, n int) {
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(r.t, err)
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(r.t, err)
	for _, o := range s.Open {
		if o.Note.Kind == sprint.Judgment && o.Note.Type == sprint.NBaseRed {
			what, n = o.Note.What, n+1
		}
	}
	return what, n
}

// A red base ends the pass with one line, BASE RED <tip>: <test>, and one judgment to the
// seat; no batch is refused, no card is blamed, and nothing is merged. The base's tip is
// gated once, before any batch; the batches are not gated. A second pass says the line
// again, raises no second judgment and opens no second card; the dashboard's merge row
// reads the failing test out of the open judgment, red until the base is green again.
func TestARedBaseIsSaidOnceAndBlamesNoCard(t *testing.T) {
	t.Parallel()
	r := newRedBaseRig(t)
	r.queueOne("s1", "a1", map[string]string{"a1.go": "package main\n\nfunc a1() {}\n"})
	r.queueOne("s2", "b1", map[string]string{"b1.go": "package main\n\nfunc b1() {}\n"})
	tip := r.mainTip()
	r.mu.Lock()
	r.red[tip] = true
	r.mu.Unlock()

	code, out, errs := r.do("land --land-parallel 2")
	text := out + errs
	assert.Equal(t, 1, code, text)
	assert.Contains(t, errs, "BASE RED "+tip+": TestTree\n", "the one line, where the loop shows what went wrong")
	assert.Equal(t, 1, strings.Count(text, "BASE RED "), "said once: %s", text)
	assert.NotContains(t, text, "LAND REFUSED", "no batch is refused: %s", text)
	assert.NotContains(t, text, "LAND OK", "nothing is merged onto a red base: %s", text)
	assert.NotContains(t, text, "fact=conflict", "no card is blamed: %s", text)
	assert.Contains(t, text, "LAND DONE batches=0 cards=0 refused=0")
	assert.Equal(t, map[string]string{"a1": "merging/queued", "b1": "merging/queued"}, r.places("a1", "b1"), "every card stays where it was")
	gates := r.gates()
	require.NotEmpty(t, gates)
	assert.Equal(t, "m1 "+tip, gates[0], "the base's tip is gated first, before any batch")
	assert.LessOrEqual(t, len(gates), 2, "then at most the base's cure looked for among the first batch's heads (none fixes it); no batch is gated: %v", gates)

	// the one judgment, on the first stream in order; the other stream is neither refused
	// nor stopped, and resumes nothing
	assert.Equal(t, "stopped base", r.streamState("s1"), "the first stream in order carries the judgment and is resumed by rule when the base is green")
	assert.Equal(t, "merging", r.streamState("s2"), "the other stream is not stopped")
	what, n := r.openJudgment()
	assert.Equal(t, 1, n, "one judgment")
	assert.Contains(t, what, "BASE RED "+tip+": TestTree")
	assert.Contains(t, what, "the landing that turned it red: ")
	assert.Contains(t, what, "fix cards opened by rule in stream critical-base-red: red-TestTree")
	inbox := r.ok("inbox")
	assert.Contains(t, inbox, sprint.NBaseRed)
	assert.Contains(t, inbox, "TestTree")
	// the dashboard's lander row: red with the failing test while the judgment is open
	row := sprintdash.MergeRowOf(sprintdash.MergeFacts{Now: r.a.now(), BaseRed: []string{what}})
	assert.Equal(t, sprintdash.GateRed, row.BaseGate)
	assert.Equal(t, "TestTree", row.FailingTest)

	// the card by rule: critical, in critical-base-red, carrying the failing test, its
	// output, its file and its package as PATHS
	pr := r.primary("red-TestTree")
	brief := pr.F("brief")
	assert.Contains(t, brief, "red-TestTree: make TestTree green on the lander's tree gate (critical-base-red) tier: heavy")
	assert.Contains(t, brief, "\nPRIORITY: critical\n")
	assert.Contains(t, brief, "\nBASE: main\n")
	assert.Contains(t, brief, "\nPATHS: internal/docs/docs_test.go,internal/docs/*.go\n")
	assert.Contains(t, brief, "\nTEST: ./internal/docs TestTree\n")
	assert.Contains(t, brief, "THE TASK. The base main is red at its tip "+tip+": TestTree fails the lander's tree gate there")
	assert.Contains(t, brief, "docs_test.go:17: NOTES.md says BAD", "the failing test's output")
	level, _ := sprint.PriorityOfBrief(brief)
	assert.Equal(t, sprint.PriorityCritical, level)
	assert.Contains(t, errs, "NOTE fix cards opened by rule in stream critical-base-red: red-TestTree")
	assert.Contains(t, errs, "NOTE base red since ")

	// the next pass: said again, no second judgment, no second card, still no card blamed
	_, out, errs = r.do("land --land-parallel 2")
	text = out + errs
	assert.Contains(t, errs, "BASE RED "+tip+": TestTree\n")
	assert.NotContains(t, text, "fact=conflict")
	assert.Contains(t, errs, "NOTE a fix card is already open for TestTree")
	_, n = r.openJudgment()
	assert.Equal(t, 1, n, "the judgment stands alone")
	assert.Equal(t, map[string]string{"a1": "merging/queued", "b1": "merging/queued"}, r.places("a1", "b1"))
	again := r.gates()[len(gates):]
	assert.LessOrEqual(t, len(again), 2, "the pass re-checks the tip of the base that stopped a stream once (baseRecheck) and tries the other stream's head as its cure once; no batch is gated: %v", again)
	for _, g := range again {
		assert.NotContains(t, g, " "+strings.Split(gates[1], " ")[1], "a head found no cure is not tried again: %v", again)
	}
	r.clean()
}

// A green base proceeds: the base's tip is gated once, then the batch's tree, and the batch
// lands; no BASE RED line, no judgment, no card opened.
func TestAGreenBaseProceedsToTheBatch(t *testing.T) {
	t.Parallel()
	r := newRedBaseRig(t)
	r.queueOne("s1", "a1", map[string]string{"a1.go": "package main\n\nfunc a1() {}\n"})
	tip := r.mainTip()
	code, out, errs := r.do("land")
	text := out + errs
	assert.Equal(t, 0, code, text)
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	assert.NotContains(t, text, "BASE RED")
	gates := r.gates()
	require.Len(t, gates, 2, "the base's tip, then the batch's tree: %v", gates)
	assert.Equal(t, "m1 "+tip, gates[0], "the base first")
	assert.NotEqual(t, gates[0], gates[1], "then the batch's tree")
	_, n := r.openJudgment()
	assert.Equal(t, 0, n)
	assert.Equal(t, "landed", r.streamState("s1"), "its one card landed")
	r.clean()
}

// The landing that turned the base red is named: the newest commit behind the tip whose tree
// the pass recorded green is the last green, and the commit after it is the suspect, as git
// logs it.
func TestTheLandingThatTurnedTheBaseRedIsNamed(t *testing.T) {
	t.Parallel()
	r := newRedBaseRig(t)
	r.queueOne("s1", "a1", map[string]string{"a1.go": "package main\n\nfunc a1() {}\n"})
	r.queueOne("s2", "b1", map[string]string{"b1.go": "package main\n\nfunc b1() {}\n"})
	code, out, errs := r.do("land --stream s1")
	require.Equal(t, 0, code, out+errs)
	green := r.mainTip()
	// a landing from outside turns the base red
	r.git(r.worker, "switch", "-q", "--detach", "refs/remotes/origin/main")
	red := r.files("land x-9 (sprint stream x)", map[string]string{"NOTES.md": "fine\nBAD\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	require.Equal(t, red, r.mainTip())
	r.mu.Lock()
	r.red[red] = true
	r.mu.Unlock()

	_, out, errs = r.do("land --stream s2")
	assert.Contains(t, errs, "BASE RED "+red+": TestTree\n")
	assert.Contains(t, errs, "the landing that turned it red: "+shortSha(red)+" land x-9 (sprint stream x) (the last green tip is "+shortSha(green)+")", out+errs)
	what, n := r.openJudgment()
	assert.Equal(t, 1, n)
	assert.Contains(t, what, shortSha(red)+" land x-9 (sprint stream x)")
	assert.Equal(t, map[string]string{"b1": "merging/queued"}, r.places("b1"), "no card is blamed")
	r.clean()
}

// A gate that fails alike on two benches for the same batch is the tree's, not a bench's: the
// pass stops asking other benches and gates the base's tip itself before any head is blamed;
// the base red, it is said once and no card is blamed. Found 2026-10-07: the base's tip was
// recorded green by one gate and was red, so every batch's gate failed on every bench, each
// batch was refused as the card's conflict, and the benches were blamed.
func TestAGateRedAlikeOnTwoBenchesIsTheBases(t *testing.T) {
	t.Parallel()
	r := newRedBaseRig(t, "vision")
	r.queueOne("s1", "a1", map[string]string{"a1.go": "package main\n\nfunc a1() {}\n"})
	code, out, errs := r.do("land --stream s1")
	require.Equal(t, 0, code, out+errs)
	tip := r.mainTip()
	r.queueOne("s2", "b1", map[string]string{"b1.go": "package main\n\nfunc b1() {}\n"})
	require.Equal(t, tip, r.mainTip(), "the base is not moved: its tip stays recorded green")
	before := len(r.gates())
	// the base went red under the recorded green: every gate on every bench fails alike
	r.mu.Lock()
	r.all = true
	r.mu.Unlock()

	_, out, errs = r.do("land --stream s2")
	text := out + errs
	assert.Contains(t, errs, "BASE RED "+tip+": TestTree\n", text)
	assert.NotContains(t, text, "fact=conflict", "no card is blamed: %s", text)
	assert.NotContains(t, text, "LAND REFUSED", text)
	assert.Equal(t, map[string]string{"b1": "merging/queued"}, r.places("b1"))
	gates := r.gates()[before:]
	require.GreaterOrEqual(t, len(gates), 3, "the batch on two benches, then the base's tip: %v", gates)
	batchHosts := map[string]bool{}
	var batchTip string
	for _, g := range gates[:2] {
		host, t2, _ := strings.Cut(g, " ")
		batchHosts[host] = true
		batchTip = t2
	}
	assert.Equal(t, map[string]bool{"m1": true, "vision": true}, batchHosts, "the red batch gate is asked of both benches, and no more: %v", gates)
	assert.NotEqual(t, tip, batchTip, "the first two gates are the batch's tree")
	assert.Equal(t, tip, strings.TrimSpace(strings.SplitN(gates[2], " ", 2)[1]), "then the base's tip itself, before any head is blamed: %v", gates)
	for _, g := range gates {
		assert.NotContains(t, g, " "+batchTip+"^", "no head is gated alone")
	}
	assert.NotContains(t, text, "fails the tree gate", "no head is blamed with the finding: %s", text)
	assert.Contains(t, text, "alike: the tree's finding, not a bench's", "the two benches agree, so the finding is the tree's: %s", text)
	_, n := r.openJudgment()
	assert.Equal(t, 1, n, "one judgment")
	r.clean()
}

// Two benches' findings are one finding once the bench's name and the runs' seconds are set
// aside; a different test, or a different line, is another finding.
func TestSameFindingSetsAsideTheBenchAndTheSeconds(t *testing.T) {
	t.Parallel()
	a := "go test ./internal/docs/: exit status 1 on the bench vision: --- FAIL: TestTree (0.01s) | docs_test.go:17: NOTES.md says BAD | FAIL | FAIL example.com/m/internal/docs 0.012s"
	b := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(a, "vision", "space"), "0.01s", "0.20s"), "0.012s", "1.5s")
	assert.True(t, sameFinding(a, b))
	assert.False(t, sameFinding(a, strings.ReplaceAll(a, "TestTree", "TestOther")))
	assert.False(t, sameFinding(a, strings.ReplaceAll(a, "docs_test.go:17", "docs_test.go:19")))
	// the first failing test, its file, its line and its package read out of a finding
	r, ok := redTestOfFinding(a)
	require.True(t, ok)
	assert.Equal(t, sprint.RedTest{Test: "TestTree", Pkg: "example.com/m/internal/docs", Job: "the lander's tree gate", File: "docs_test.go", Line: "docs_test.go:17: NOTES.md says BAD"}, r)
	_, ok = redTestOfFinding("go build ./...: exit status 1: main.go:3:13: syntax error")
	assert.False(t, ok, "a build failure names no test: the base-gate rule's")
}
