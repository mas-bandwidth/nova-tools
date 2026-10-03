package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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
	start, err = installFrame(cfg, job, st.BaseSha)
	require.NoError(t, err)
	require.NotEmpty(t, start)
	return cfg, job, start, head
}

// A whole native run of a decide read past its bounce bar stages the checkout, decides, and
// ends with no child: the run is OK with the decision's broken read in RESULT.md, published.
func TestNativeRunEndsADecidedReadWithNoChild(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, origin, head := readOrigin(t)
	slot := filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	fake := &fakeDecide{defect: 0.6}
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{binary: bin, model: "fake/fake-model", label: "w.r1", card: readCard, slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true, benchHome: filepath.Join(root, "no-bench"), resultsRoot: filepath.Join(root, "results", "w.r1"),
		frame: readFrame(cardcontract.Frame{DecideBounce: "0.5", DecideReview: "0.3"}, origin, head), decider: &decider{backend: fake, now: time.Now}}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, 1, fake.asks)
	assert.Equal(t, []string{"OK", ""}, func() []string { v, w := nativeVerdictWhy(res); return []string{v, w} }(), "the NATIVE line is OK")
	_, err := os.Stat(filepath.Join(res.job, "harness-output.log"))
	assert.True(t, os.IsNotExist(err), "no child ran")
	require.NotEmpty(t, res.resultsDir)
	raw, err := os.ReadFile(filepath.Join(res.resultsDir, "RESULT.md"))
	require.NoError(t, err)
	assert.Equal(t, "broken", typedrec.ParseCardResult(raw).Verdict)
}

// A decide read that cannot be made falls back to the strings read, never to a verdict:
// whole native runs over a backend that answers 402, one whose context ran out and one
// whose answer is outside the schema each run the child, publish no ok read of their own
// and record nothing, and say why on one NOTE line. A read in the band between the bars
// runs the child too, its decision recorded.
func TestNativeRunFallsBackToTheStringsReadWhenNoDecisionIsMade(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	jev := func(send decide.Send) decide.Backend { return decide.Jev{Model: decide.JevModel, Send: send} }
	for _, tc := range []struct {
		name     string
		backend  decide.Backend
		note     string
		recorded int
	}{
		{"402", jev(func(context.Context, []byte) ([]byte, error) {
			return nil, errors.New(`the backend answered HTTP 402: "payment required"`)
		}), "no decide read: the backend answered HTTP 402", 0},
		{"timeout", jev(func(context.Context, []byte) ([]byte, error) { return nil, context.DeadlineExceeded }), "no decide read: context deadline exceeded", 0},
		{"malformed", jev(func(context.Context, []byte) ([]byte, error) { return []byte(`{"answers":{}}`), nil }), "no decide read: the backend's answers do not fit the schema", 0},
		{"strings band", &fakeDecide{defect: 0.4}, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, origin, head := readOrigin(t)
			slot := filepath.Join(root, "slot-1")
			require.NoError(t, os.MkdirAll(slot, 0o755))
			write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
			var errOut bytes.Buffer
			res, code := nativeRun(nativeRunConfig{binary: bin, model: "fake/fake-model", label: "w.r1", card: readCard, slotDir: slot, root: root,
				deadline: 30 * time.Second, noWall: true, benchHome: filepath.Join(root, "no-bench"), resultsRoot: filepath.Join(root, "results", "w.r1"),
				frame: readFrame(cardcontract.Frame{DecideBounce: "0.5", DecideReview: "0.3"}, origin, head), decider: &decider{backend: tc.backend, now: time.Now}}, &errOut)
			require.Equal(t, 0, code, errOut.String())
			_, err := os.Stat(filepath.Join(res.job, "harness-output.log"))
			assert.NoError(t, err, "the strings read's child ran")
			if raw, err := os.ReadFile(swarm.ResultPath(res.job)); err == nil {
				assert.NotEqual(t, "ok", typedrec.ParseCardResult(raw).Verdict, "no ok read is published for an undecided read")
			}
			ds, err := decide.Load(decideRecord(root))
			require.NoError(t, err)
			assert.Len(t, ds, tc.recorded)
			if tc.note != "" {
				assert.Contains(t, errOut.String(), "NATIVE NOTE: w.r1 "+tc.note)
				assert.Contains(t, errOut.String(), "; the strings read runs")
			}
		})
	}
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

// A read whose frame names no bars (a pro card's, or a flash card's second read) is a
// strings read: no decision is asked and nothing is recorded. A decide read with no key in
// the environment, or bars it cannot read, says so once and the strings read runs.
func TestAReadWithNoBarsAsksNoDecision(t *testing.T) {
	t.Parallel()
	cfg, job, start, head := stagedRead(t, cardcontract.Frame{Tier: "pro"})
	fake := &fakeDecide{defect: 0.9}
	cfg.decider = &decider{backend: fake, now: time.Now}
	var out, errs bytes.Buffer
	route, op := nativeDecide(cfg, job, start, head, &out, &errs)
	assert.Equal(t, []string{"", ""}, []string{route, op})
	assert.Zero(t, fake.asks)
	assert.Empty(t, out.String()+errs.String())
	_, err := os.Stat(decideRecord(cfg.root))
	assert.True(t, os.IsNotExist(err))

	cfg.frame.DecideBounce, cfg.frame.DecideReview = "0.3", "0.5"
	route, _ = nativeDecide(cfg, job, start, head, &out, &errs)
	assert.Empty(t, route)
	assert.Contains(t, errs.String(), "NATIVE NOTE: w.r1 no decide read: decide_review 0.5 is above decide_bounce 0.3; the strings read is the band between them; the strings read runs")
	assert.Zero(t, fake.asks)
}

// The decide read's key is native's own: no child is handed JEV_API_KEY, whatever native's
// environment holds, and a worker's own secret still is.
func TestTheDecideKeyIsNeverTheChilds(t *testing.T) {
	t.Parallel()
	environ := []string{"PATH=/bin", decide.JevSecret + "=jev-test-key", "PROVIDER_API_KEY=p"}
	env := nativeChildEnvFrom(environ, "data", "job", "tmp", "", "PROVIDER_API_KEY", "", "", "")
	assert.NotContains(t, env, decide.JevSecret+"=jev-test-key")
	assert.Contains(t, env, "PROVIDER_API_KEY=p")
	env = nativeChildEnvFrom(environ, "data", "job", "tmp", "", decide.JevSecret, "", "", "")
	assert.NotContains(t, env, decide.JevSecret+"=jev-test-key", "not when a worker description names it as its secret either")
}
