package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// fakeDecide answers the read decision with one p(defect) and counts its asks: the
// backend a decide read is handed in a test, in place of Jev over its transport.
type fakeDecide struct {
	defect float64
	asks   int
}

func (f *fakeDecide) Name() string { return "fake" }

func (f *fakeDecide) Ask(ctx context.Context, s decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	f.asks++
	n := func(p float64) *float64 { return &p }
	return decide.Fixed{Table: map[string]decide.FixedAnswer{
		"does_task": {Noul: n(0.8)}, "lines_changed": {Noul: n(0.9)}, "inside_paths": {Noul: n(0.95)}, "defect": {Noul: n(f.defect)},
		"verdict": {Choice: decide.Land, P: map[string]float64{decide.Land: 0.7, decide.Bounce: 0.3}},
	}}.Ask(ctx, s, state)
}

// readOrigin is a repository whose work changed docs/a.md on its branch sprint/w: the
// root it is under, the origin, and the work's head.
func readOrigin(t *testing.T) (root, origin, head string) {
	t.Helper()
	root = t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	require.NoError(t, os.MkdirAll(filepath.Join(seed, "docs"), 0o755))
	write(t, filepath.Join(seed, "docs", "a.md"), "The box holds three cards.\n")
	gitAs(t, seed, "add", ".")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	write(t, filepath.Join(w, "docs", "a.md"), "The box holds four cards.\n")
	gitAs(t, w, "commit", "-q", "-am", "work")
	head = gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")
	return root, origin, head
}

// readFrame is the read's frame over readOrigin, with fr's bars and tier.
func readFrame(fr cardcontract.Frame, origin, head string) *cardcontract.Frame {
	fr.Kind, fr.Card, fr.Attempt, fr.Model, fr.Repo, fr.BaseRef, fr.ReviewBase, fr.StageSha, fr.Branch = "read", "w.r1", 1, "fake/fake-model", origin, "main", "main", head, "sprint/w"
	return &fr
}

// readCard is the card a read of readOrigin's work is handed.
var readCard = []byte(member.CardText(member.Packet{Card: "w.r1", Kind: "read", Attempt: 1, Primary: "w", Worker: "m1", Head: "abc",
	Report: "the worker says it is fine", Brief: "c: change the box (s1) tier: flash\n\nThe task.\n"}))

// stagedRead is a read's checkout staged as native stages it, its frame installed: the
// config, the job, its start and its head.
func stagedRead(t *testing.T, bars cardcontract.Frame) (cfg nativeRunConfig, job, start, head string) {
	t.Helper()
	root, origin, head := readOrigin(t)
	fr := readFrame(bars, origin, head)
	slot := filepath.Join(root, "slot")
	job = filepath.Join(slot, "jobs", "w.r1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	st, err := swarm.StageCard(swarm.StageOptions{TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
		BenchHome: filepath.Join(root, "no-bench"), Base: &swarm.CardBase{Repo: origin, Sha: head, Ref: "main", Named: origin}, Branch: fr.Branch})
	require.NoError(t, err)
	cfg = nativeRunConfig{slotDir: slot, root: root, model: fr.Model, frame: fr, label: "w.r1", card: readCard,
		resultsRoot: filepath.Join(root, "results", "w.r1")}
	start, err = installFrame(cfg, job, st.BaseSha, nil)
	require.NoError(t, err)
	require.NotEmpty(t, start)
	return cfg, job, start, head
}

// The frame carries the packet's bars as they are, bounce as bounce and review as review:
// swapped, ParseBars would refuse them and no decide read would ever run.
func TestTheFrameCarriesTheDecideBars(t *testing.T) {
	t.Parallel()
	f := frameOf(member.Packet{Card: "c1.r1", Kind: "read", Attempt: 1, Head: "abc", Brief: "c1: do it (s1) tier: flash\n\nThe task.", DecideBounce: "0.5", DecideReview: "0.3"}, "p/m", t.TempDir())
	assert.Equal(t, []string{"0.5", "0.3"}, []string{f.DecideBounce, f.DecideReview})
	f = frameOf(member.Packet{Card: "c1.w1", Kind: "work", Attempt: 1, Brief: "c1: do it (s1) tier: flash\n\nThe task.", DecideBounce: "0.5", DecideReview: "0.3"}, "p/m", t.TempDir())
	assert.Empty(t, f.DecideBounce+f.DecideReview, "a work card is never decided")
}

// A flash card's first read is decided before any child at the frame's bars (0.5 and 0.3,
// the sprint row's): p(defect) 0.6 is a broken read whose finding is the decision, 0.2 an
// ok read, both RESULT.md in the contract's shape, published where the member reads it; 0.4
// leaves the read to the strings read, whose verdict is then the decision's outcome. Every
// decision is in the machine's record under the read card at the head it read.
func TestADecideReadRoutesTheReadByItsBars(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		defect  float64
		route   string
		verdict string
	}{
		{"bounce", 0.6, decide.RouteBounce, "broken"},
		{"strings", 0.4, decide.RouteStrings, ""},
		{"land", 0.2, decide.RouteLand, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, job, start, head := stagedRead(t, cardcontract.Frame{DecideBounce: "0.5", DecideReview: "0.3"})
			fake := &fakeDecide{defect: tc.defect}
			cfg.decider = &decider{backend: fake, now: func() time.Time { return time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC) }}
			var out, errs bytes.Buffer
			route, op := nativeDecide(cfg, job, start, head, &out, &errs)
			require.Empty(t, errs.String())
			assert.Equal(t, tc.route, route)
			assert.Equal(t, "w.r1@"+head[:12], op)
			assert.Contains(t, out.String(), "DECIDE w.r1 op="+op+" route="+tc.route)
			assert.Equal(t, 1, fake.asks)
			ds, err := decide.Load(decideRecord(cfg.root))
			require.NoError(t, err)
			require.Len(t, ds, 1)
			assert.Contains(t, ds[0].State, "+The box holds four cards.", "the decision is asked over the work's diff")
			assert.Contains(t, ds[0].State, "CARD (the whole task the worker was given):\nc: change the box (s1) tier: flash\n\nThe task.\n\nDIFF", "the card is the work card's brief alone, as the bars were calibrated")
			assert.NotContains(t, ds[0].State, "the worker says it is fine", "never the read's mechanics or the worker's report")
			raw, err := os.ReadFile(swarm.ResultPath(job))
			if tc.route == decide.RouteStrings {
				require.True(t, os.IsNotExist(err), "a strings read writes its own result")
				write(t, swarm.ResultPath(job), "head: "+head+"\nbranch: sprint/w\nverdict: broken\ngate: ok\noutput: -\nreport: docs/a.md:1 the count is wrong\n")
				settleDecided(cfg, job, op, &errs)
				require.Empty(t, errs.String())
				ds, err = decide.Load(decideRecord(cfg.root))
				require.NoError(t, err)
				require.NotNil(t, ds[0].Outcome)
				assert.Equal(t, decide.Bounce, ds[0].Outcome.Label, "the strings read's broken is the decision's BOUNCE")
				return
			}
			require.NoError(t, err)
			cr := typedrec.ParseCardResult(raw)
			assert.True(t, cr.Shaped, "%s", raw)
			assert.Equal(t, tc.verdict, cr.Verdict)
			assert.Equal(t, head, cr.Head)
			assert.Contains(t, cr.Report, "decide: p(defect)=")
			assert.True(t, typedrec.NamesADefect(cr.Report), "the finding names a file: %s", cr.Report)
			dir := publishDecided(cfg, job, &errs)
			require.NotEmpty(t, dir, errs.String())
			published := newestResult(cfg.resultsRoot)
			assert.Equal(t, filepath.Join(dir, "RESULT.md"), published, "the member finds the result")
		})
	}
}

// The decide read's key is native's own: no child is handed JEV_API_KEY, whatever native's
// environment holds, and a worker's own secret still is.
func TestTheDecideKeyIsNeverTheChilds(t *testing.T) {
	t.Parallel()
	environ := []string{"PATH=/bin", decide.JevSecret + "=jev-test-key", "PROVIDER_API_KEY=p"}
	env := nativeChildEnvFrom(environ, "data", "job", "tmp", "", "PROVIDER_API_KEY", "", "", nil)
	assert.NotContains(t, env, decide.JevSecret+"=jev-test-key")
	assert.Contains(t, env, "PROVIDER_API_KEY=p")
	env = nativeChildEnvFrom(environ, "data", "job", "tmp", "", decide.JevSecret, "", "", nil)
	assert.NotContains(t, env, decide.JevSecret+"=jev-test-key", "not when a worker description names it as its secret either")
}
