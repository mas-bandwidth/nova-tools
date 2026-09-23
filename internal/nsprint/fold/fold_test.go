package fold_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

type fixture struct {
	Sprint string                       `json:"sprint"`
	Hash   map[string]string            `json:"hash"`
	Policy map[string]string            `json:"policy"`
	Cards  map[string]map[string]string `json:"cards"`
	Disp   map[string]map[string]string `json:"disp"`
	Tasks  map[string][]string          `json:"tasks"`
	Log    int                          `json:"log"`
}

// seed loads testdata/sprint.json into a throwaway Redis under the sprint
// keys of #2756 section 2.3, the shape sprint open and the card and task
// transition functions write.
func seed(t *testing.T) (*miniredis.Miniredis, *redis.Client, fixture) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sprint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx fixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	s := "s:" + fx.Sprint
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(client.HSet(ctx, s, fx.Hash).Err())
	must(client.HSet(ctx, s+":policy", fx.Policy).Err())
	for label, fields := range fx.Cards {
		must(client.HSet(ctx, s+":card:"+label, fields).Err())
		must(client.SAdd(ctx, s+":idx:card:"+fields["state"], label).Err())
	}
	for key, fields := range fx.Disp {
		must(client.HSet(ctx, s+":disp:"+key, fields).Err())
	}
	for state, ids := range fx.Tasks {
		for _, id := range ids {
			must(client.HSet(ctx, s+":task:"+id, "state", state).Err())
			must(client.SAdd(ctx, s+":idx:task:"+state, id).Err())
		}
	}
	for i := 0; i < fx.Log; i++ {
		must(client.XAdd(ctx, &redis.XAddArgs{Stream: s + ":log", Values: []string{"kind", "card", "to", "ended"}}).Err())
	}
	return mr, client, fx
}

// workRepo is a throwaway nova-work checkout with one commit.
func workRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "fold-test"},
		{"config", "user.email", "fold-test@example.invalid"},
		{"commit", "-q", "--allow-empty", "-m", "nova-work root"},
	} {
		git(t, dir, args...)
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

var errKilled = errors.New("killed by the test")

func opts(fx fixture, work string) fold.Options {
	return fold.Options{
		Sprint: fx.Sprint,
		Work:   work,
		Actor:  "fold-test",
		Now:    func() time.Time { return time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC) },
	}
}

// Control 20 (#2756 section 8), the fold half: kill fold midway and re-run,
// and nova-work has exactly one commit for the sprint. The kill lands at
// every step boundary: after the fold file is written, after it is staged,
// after the commit and before the store records it.
func TestControl20FoldOnce(t *testing.T) {
	for _, killAt := range []string{fold.StepWritten, fold.StepStaged, fold.StepCommitted} {
		t.Run("kill-after-"+killAt, func(t *testing.T) {
			_, client, fx := seed(t)
			work := workRepo(t)
			ctx := context.Background()
			s := "s:" + fx.Sprint

			o := opts(fx, work)
			o.Hook = func(step string) error {
				if step == killAt {
					return errKilled
				}
				return nil
			}
			var out bytes.Buffer
			if _, err := fold.Run(ctx, client, o, &out); !errors.Is(err, errKilled) {
				t.Fatalf("killed run: err %v, want the kill", err)
			}
			if st := client.HGet(ctx, s, "status").Val(); st != "closed" {
				t.Fatalf("after a kill at %s the status is %q; the store must not say folded before the commit is recorded", killAt, st)
			}

			out.Reset()
			res, err := fold.Run(ctx, client, opts(fx, work), &out)
			if err != nil {
				t.Fatalf("re-run: %v\n%s", err, out.String())
			}
			commits := strings.Fields(git(t, work, "log", "--format=%H", "--grep", "Sprint-Fold: "+fx.Sprint))
			if len(commits) != 1 {
				t.Fatalf("kill at %s then re-run made %d fold commits, want 1\n%s", killAt, len(commits), out.String())
			}
			if total := strings.Fields(git(t, work, "log", "--format=%H")); len(total) != 2 {
				t.Fatalf("nova-work has %d commits, want root + one fold", len(total))
			}
			if res.FoldSHA != commits[0] {
				t.Fatalf("result fold sha %s, commit %s", res.FoldSHA, commits[0])
			}
			wantMade := killAt != fold.StepCommitted
			if res.Made != wantMade {
				t.Fatalf("re-run after a kill at %s: made=%v, want %v (a commit that exists is found, not made)", killAt, res.Made, wantMade)
			}
			h := client.HGetAll(ctx, s).Val()
			if h["status"] != "folded" || h["fold_sha"] != commits[0] {
				t.Fatalf("sprint hash after the fold: status %q fold_sha %q, want folded %s", h["status"], h["fold_sha"], commits[0])
			}
			if n := client.XLen(ctx, s+":log").Val(); n != int64(fx.Log)+1 {
				t.Fatalf("log has %d entries, want %d: one receipt for closed -> folded", n, fx.Log+1)
			}
			if !strings.Contains(out.String(), "FOLD COMMIT sprint="+fx.Sprint+" sha="+commits[0]) {
				t.Fatalf("re-run output names no commit:\n%s", out.String())
			}
			if st := git(t, work, "status", "--porcelain"); st != "" {
				t.Fatalf("nova-work checkout left dirty: %s", st)
			}

			// A third run finds the sprint folded: no commit, no receipt.
			out.Reset()
			res, err = fold.Run(ctx, client, opts(fx, work), &out)
			if err != nil || res.Made || res.FoldSHA != commits[0] {
				t.Fatalf("run on a folded sprint: %+v %v", res, err)
			}
			if n := len(strings.Fields(git(t, work, "log", "--format=%H"))); n != 2 {
				t.Fatalf("a run on a folded sprint made a commit: %d commits", n)
			}
			if n := client.XLen(ctx, s+":log").Val(); n != int64(fx.Log)+1 {
				t.Fatalf("a run on a folded sprint wrote a receipt: %d entries", n)
			}
		})
	}
}

// The fixture sprint prints $ per landed card per route, and per useful card,
// from the store alone. An unpriced card is counted, never priced at zero; a
// read at a stale head or a HOLD is not useful; ci cards are on their own line.
func TestFoldCostPerLandedPerRoute(t *testing.T) {
	_, client, fx := seed(t)
	work := workRepo(t)
	var out bytes.Buffer
	if _, err := fold.Run(context.Background(), client, opts(fx, work), &out); err != nil {
		t.Fatalf("fold: %v\n%s", err, out.String())
	}
	s := fx.Sprint
	for _, want := range []string{
		"FOLD ROUTE sprint=" + s + " route=fable cards=4 done=3 useful=3 landed=2 usd=1.8 usd_per_useful=0.6 usd_per_landed=0.9 unpriced=0\n",
		"FOLD ROUTE sprint=" + s + " route=kimi cards=2 done=1 useful=0 landed=0 usd=0.05 usd_per_useful=- usd_per_landed=- unpriced=1\n",
		"FOLD ROUTE sprint=" + s + " route=sonnet cards=3 done=3 useful=1 landed=1 usd=0.6 usd_per_useful=0.6 usd_per_landed=0.6 unpriced=0\n",
		"FOLD CI sprint=" + s + " cards=1 done=1\n",
		// The sprint's spend is unknown while kimi's c8 is unpriced, even
		// though c8 is neither useful nor landed, so its per-card figures are -.
		"FOLD SPRINT sprint=" + s + " cards=9 done=7 useful=4 landed=3 usd=2.45 usd_per_useful=- usd_per_landed=- unpriced=1 tasks=5 tasks_done=4 receipts=42 useful_min=8\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing line %q in\n%s", want, out.String())
		}
	}
	// The routes print in name order, before the sprint line.
	o := out.String()
	if !(strings.Index(o, "route=fable") < strings.Index(o, "route=kimi") &&
		strings.Index(o, "route=kimi") < strings.Index(o, "route=sonnet") &&
		strings.Index(o, "route=sonnet") < strings.Index(o, "FOLD SPRINT")) {
		t.Errorf("route lines out of order:\n%s", o)
	}
	// The commit carries the same numbers, so the graph is the record.
	body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", s+".sexp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(sprint-fold "` + s + `"`,
		`:nova-work-sha "09fbedc905218d5d4bdf5d039d0baff366ef2545"`,
		`(route "fable" :cards 4 :done 3 :useful 3 :landed 2 :usd "1.8" :usd-per-useful "0.6" :usd-per-landed "0.9" :unpriced 0)`,
		`(route "kimi" :cards 2 :done 1 :useful 0 :landed 0 :usd "0.05" :usd-per-useful "-" :usd-per-landed "-" :unpriced 1)`,
		`:landed 3`,
		`:receipts 42`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("fold file lacks %q:\n%s", want, body)
		}
	}
}

func TestFoldRefusesASprintThatIsNotClosed(t *testing.T) {
	_, client, fx := seed(t)
	work := workRepo(t)
	ctx := context.Background()
	client.HSet(ctx, "s:"+fx.Sprint, "status", "open")
	var out bytes.Buffer
	_, err := fold.Run(ctx, client, opts(fx, work), &out)
	if err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("fold of an open sprint: %v, want a not-closed refusal", err)
	}
	if n := len(strings.Fields(git(t, work, "log", "--format=%H"))); n != 1 {
		t.Fatalf("a refused fold made a commit")
	}
	_, err = fold.Run(ctx, client, fold.Options{Sprint: "absent-sprint", Work: work}, &out)
	if err == nil || !strings.Contains(err.Error(), "no sprint") {
		t.Fatalf("fold of an absent sprint: %v", err)
	}
}

func TestMainFlagsAndExitCodes(t *testing.T) {
	mr, _, fx := seed(t)
	work := workRepo(t)
	var stdout, stderr bytes.Buffer
	code := fold.Main(context.Background(), []string{fx.Sprint, "--store", mr.Addr(), "--work", work, "--as", "fold-test"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "FOLD RECORDED sprint="+fx.Sprint) {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := fold.Main(context.Background(), []string{"--store", mr.Addr()}, &stdout, &stderr); code != 2 ||
		!strings.Contains(stderr.String(), "run: nova-sprint help") {
		t.Fatalf("no sprint name: exit %d stderr %q", code, stderr.String())
	}
}

// An unpriced card that is useful or landed puts an unknown cost in the
// denominator of $ per useful and $ per landed, so those figures are
// suppressed (-) on its route and on the sprint line, never printed as if
// exact; usd itself stays the priced sum with unpriced counting the gap.
func TestFoldSuppressesPerCardWhenUnpricedCardCounts(t *testing.T) {
	_, client, fx := seed(t)
	ctx := context.Background()
	s := "s:" + fx.Sprint
	// c10: a landed kimi card with no usd field.
	if err := client.HSet(ctx, s+":card:c10", map[string]string{
		"kind": "model", "route": "kimi", "state": "landed", "outcome": "DONE",
		"repo": "nova-tools", "pr": "110", "head": "cccc1010",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, s+":idx:card:landed", "c10").Err(); err != nil {
		t.Fatal(err)
	}
	work := workRepo(t)
	var out bytes.Buffer
	if _, err := fold.Run(ctx, client, opts(fx, work), &out); err != nil {
		t.Fatalf("fold: %v\n%s", err, out.String())
	}
	sp := fx.Sprint
	for _, want := range []string{
		"FOLD ROUTE sprint=" + sp + " route=kimi cards=3 done=2 useful=1 landed=1 usd=0.05 usd_per_useful=- usd_per_landed=- unpriced=2\n",
		// Routes with every useful and landed card priced keep their figures.
		"FOLD ROUTE sprint=" + sp + " route=fable cards=4 done=3 useful=3 landed=2 usd=1.8 usd_per_useful=0.6 usd_per_landed=0.9 unpriced=0\n",
		"FOLD SPRINT sprint=" + sp + " cards=10 done=8 useful=5 landed=4 usd=2.45 usd_per_useful=- usd_per_landed=- unpriced=2 ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing line %q in\n%s", want, out.String())
		}
	}
	body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", sp+".sexp"))
	if err != nil {
		t.Fatal(err)
	}
	want := `(route "kimi" :cards 3 :done 2 :useful 1 :landed 1 :usd "0.05" :usd-per-useful "-" :usd-per-landed "-" :unpriced 2)`
	if !bytes.Contains(body, []byte(want)) {
		t.Errorf("fold file lacks %q:\n%s", want, body)
	}
	if msg := git(t, work, "log", "-1", "--format=%s"); !strings.HasSuffix(msg, "usd per landed -") {
		t.Errorf("commit subject %q does not suppress usd per landed", msg)
	}
}

// An unpriced card that is neither useful nor landed still leaves the line's
// total spend (the numerator of both ratios) unknown, so a route with a priced
// useful, landed card and an unpriced failed card prints - for both, on the
// route line, the sprint line, the fold sexp and the commit subject.
func TestFoldSuppressesPerCardWhenUnpricedCardIsOutsideTheDenominator(t *testing.T) {
	_, client, fx := seed(t)
	ctx := context.Background()
	s := "s:" + fx.Sprint
	// c11: a priced, landed kimi card; kimi's c8 stays unpriced and FAILED.
	if err := client.HSet(ctx, s+":card:c11", map[string]string{
		"kind": "model", "route": "kimi", "state": "landed", "outcome": "DONE",
		"repo": "nova-tools", "pr": "111", "head": "cccc1111", "usd": "0.05",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, s+":idx:card:landed", "c11").Err(); err != nil {
		t.Fatal(err)
	}
	work := workRepo(t)
	var out bytes.Buffer
	if _, err := fold.Run(ctx, client, opts(fx, work), &out); err != nil {
		t.Fatalf("fold: %v\n%s", err, out.String())
	}
	sp := fx.Sprint
	for _, want := range []string{
		"FOLD ROUTE sprint=" + sp + " route=kimi cards=3 done=2 useful=1 landed=1 usd=0.1 usd_per_useful=- usd_per_landed=- unpriced=1\n",
		"FOLD ROUTE sprint=" + sp + " route=fable cards=4 done=3 useful=3 landed=2 usd=1.8 usd_per_useful=0.6 usd_per_landed=0.9 unpriced=0\n",
		"FOLD SPRINT sprint=" + sp + " cards=10 done=8 useful=5 landed=4 usd=2.5 usd_per_useful=- usd_per_landed=- unpriced=1 ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing line %q in\n%s", want, out.String())
		}
	}
	body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", sp+".sexp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(route "kimi" :cards 3 :done 2 :useful 1 :landed 1 :usd "0.1" :usd-per-useful "-" :usd-per-landed "-" :unpriced 1)`,
		`(all :cards 10 :done 8 :useful 5 :landed 4 :usd "2.5" :usd-per-useful "-" :usd-per-landed "-" :unpriced 1)`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("fold file lacks %q:\n%s", want, body)
		}
	}
	if msg := git(t, work, "log", "-1", "--format=%s"); !strings.HasSuffix(msg, "usd per landed -") {
		t.Errorf("commit subject %q does not suppress usd per landed", msg)
	}
}

// calibSet is the Jev calibration set the fold fixture runs (nova-tools #3081).
// Rows 1-5 are the five resolved disagreements of the 2026-09-22 scorecard
// (reports/jev-scorecard-2026-09-22.md; the rowan-tools #176 few-shots) with
// the ruled friend line at the head Jev read. #2781 at 0a897035 is Stella's
// false-confidence case: Jev said UNSURE, her HOLD 4 at the same head found a
// Redis Spend partial-write defect. #2961 at afd3efb0 is her coverage case:
// the work-type step stamped allowed=- without enforcing allowed_routes[type];
// Emma's APPROVE 10 there is superseded by Stella's HOLD 6. The schema 90xx
// rows are synthetic conformance cells, so the set has a second work type.
// Resolved times order the date split: the newest third is the held-out set.
const calibSet = `{"repo":"nova-tools","pr":2519,"head":"907546af","work_type":"issue-fix-red-first","who":"emma","verdict":"HOLD","score":6,"resolved_at":"2026-09-22T16:53:00Z","tag":"few-shot","note":"CI red at head (G1); Jev PASS 8"}
{"repo":"nova-tools","pr":2522,"head":"9ee81556","work_type":"issue-fix-red-first","who":"stella","verdict":"APPROVE","score":9,"resolved_at":"2026-09-22T16:54:00Z","tag":"few-shot"}
{"repo":"nova-tools","pr":2543,"head":"86889917","work_type":"issue-fix-red-first","who":"rowan-ruling","verdict":"HOLD","score":0,"resolved_at":"2026-09-22T16:55:00Z","tag":"few-shot","note":"the test checks its own fixture; ruled not an 8+, no score"}
{"repo":"nova-tools","pr":2622,"head":"312a4b38","work_type":"issue-fix-red-first","who":"emma","verdict":"APPROVE","score":10,"resolved_at":"2026-09-22T16:56:00Z","tag":"few-shot"}
{"repo":"nova-tools","pr":2651,"head":"468acdbc","work_type":"issue-fix-red-first","who":"stella","verdict":"HOLD","score":7,"resolved_at":"2026-09-22T20:00:00Z","tag":"few-shot","note":"fail-open on lsof failure"}
{"repo":"schema","pr":9001,"head":"c0de9001","work_type":"conformance-cell","who":"johnny","verdict":"APPROVE","score":9,"resolved_at":"2026-09-22T21:00:00Z","note":"synthetic"}
{"repo":"schema","pr":9002,"head":"c0de9002","work_type":"conformance-cell","who":"johnny","verdict":"HOLD","score":3,"resolved_at":"2026-09-22T21:01:00Z","note":"synthetic self-check tautology"}
{"repo":"schema","pr":9003,"head":"c0de9003","work_type":"conformance-cell","who":"johnny","verdict":"APPROVE","score":8,"resolved_at":"2026-09-22T21:02:00Z","note":"synthetic"}
{"repo":"nova-tools","pr":2781,"head":"0a897035","work_type":"issue-fix-red-first","who":"stella","verdict":"HOLD","score":4,"resolved_at":"2026-09-23T01:06:16Z","tag":"false-confidence","note":"Jev UNSURE; Stella HOLD 4 found a Redis Spend partial write"}
{"repo":"schema","pr":9004,"head":"c0de9004","work_type":"conformance-cell","who":"johnny","verdict":"HOLD","score":2,"resolved_at":"2026-09-23T02:00:00Z","note":"synthetic"}
{"repo":"nova-tools","pr":2961,"head":"afd3efb0","work_type":"issue-fix-red-first","who":"stella","verdict":"HOLD","score":6,"resolved_at":"2026-09-23T04:30:00Z","tag":"coverage","note":"allowed=- stamped, allowed_routes[type] never enforced"}
{"repo":"schema","pr":9005,"head":"c0de9005","work_type":"conformance-cell","who":"johnny","verdict":"APPROVE","score":9,"resolved_at":"2026-09-23T05:00:00Z","note":"synthetic"}
`

// jevAnswers is what each prompt says per PR: verdict and score (0 = no
// score). p-current is the live lines at those heads (the scorecard and the
// PR comments); the candidates answer only on the held-out rows.
var jevAnswers = map[string]map[int]struct {
	verdict string
	score   int
}{
	"p-current": {
		2519: {"PASS", 8}, 2522: {"BOUNCE", 3}, 2543: {"BOUNCE", 3}, 2622: {"BOUNCE", 3}, 2651: {"UNSURE", 7},
		2781: {"UNSURE", 0}, 2961: {"UNSURE", 0},
		9001: {"PASS", 9}, 9002: {"BOUNCE", 2}, 9003: {"UNSURE", 6}, 9004: {"BOUNCE", 3}, 9005: {"PASS", 8},
	},
	// p-regress turns the two false-confidence/coverage silences into passes.
	"p-regress": {2781: {"PASS", 7}, 9004: {"BOUNCE", 2}, 2961: {"PASS", 8}, 9005: {"PASS", 9}},
	// p-bouncy bounces a conformance cell the friend approved.
	"p-bouncy": {2781: {"BOUNCE", 4}, 9004: {"BOUNCE", 2}, 2961: {"BOUNCE", 5}, 9005: {"BOUNCE", 4}},
	// p-gates holds both of Stella's cases and keeps the cells right.
	"p-gates": {2781: {"BOUNCE", 4}, 9004: {"BOUNCE", 2}, 2961: {"BOUNCE", 5}, 9005: {"PASS", 9}},
}

// fakeJev answers from jevAnswers and records which rows each prompt saw.
type fakeJev struct{ calls map[string][]int }

func (f *fakeJev) Score(_ context.Context, prompt string, rows []fold.CalibRow) ([]fold.JevLine, error) {
	if f.calls == nil {
		f.calls = map[string][]int{}
	}
	var out []fold.JevLine
	for _, r := range rows {
		f.calls[prompt] = append(f.calls[prompt], r.PR)
		a, ok := jevAnswers[prompt][r.PR]
		if !ok {
			continue
		}
		out = append(out, fold.JevLine{Repo: "mas-bandwidth/" + r.Repo, PR: r.PR, Head: r.Head + "0000", Verdict: a.verdict, Score: a.score})
	}
	return out, nil
}

func calib(t *testing.T, candidate string, scorer fold.Scorer) *fold.JevCalib {
	t.Helper()
	set, err := fold.ReadCalibSet(strings.NewReader(calibSet))
	if err != nil {
		t.Fatal(err)
	}
	return &fold.JevCalib{Set: set, PromptSHA: "p-current", Candidate: candidate, Scorer: scorer}
}

// TestFoldRunsJevEvalPerType is the DONE-WHEN of nova-tools #3081: the fold
// re-scores the resolved PRs with the current prompt and writes agreement,
// MAE, false-pass and false-bounce per work type into the fold record; a
// candidate prompt worse on the held-out third in false passes or false
// bounces is refused and the current prompt stays; one that holds is adopted.
func TestFoldRunsJevEvalPerType(t *testing.T) {
	ctx := context.Background()
	t.Run("current", func(t *testing.T) {
		_, client, fx := seed(t)
		client.HSet(ctx, "jev:prompt", "sha", "p-current")
		work := workRepo(t)
		o := opts(fx, work)
		jev := &fakeJev{}
		o.Jev = calib(t, "", jev)
		var out bytes.Buffer
		res, err := fold.Run(ctx, client, o, &out)
		if err != nil {
			t.Fatalf("fold: %v\n%s", err, out.String())
		}
		if res.PromptRefused != "" {
			t.Fatalf("no candidate, yet refused: %s", res.PromptRefused)
		}
		s := fx.Sprint
		for _, want := range []string{
			"FOLD JEV SET sprint=" + s + " prompt_sha=p-current heads=12 holdout=4 set_sha=",
			// The inverse finding of 2026-09-22, reproduced: Jev scores the held
			// tool PRs above the approved ones (sep < 0), so the score gate fails.
			"FOLD JEV TYPE sprint=" + s + " prompt_sha=p-current type=conformance-cell heads=5 decided=4 agree=4/4 false_pass=0 false_bounce=0 unsure=1 mae=1.00 sep=+5.17 pass_prec_lb=0.342 bounce_prec_lb=0.342 promote=none\n",
			"FOLD JEV TYPE sprint=" + s + " prompt_sha=p-current type=issue-fix-red-first heads=7 decided=4 agree=1/4 false_pass=1 false_bounce=2 unsure=3 mae=3.75 sep=-3.00 pass_prec_lb=0.000 bounce_prec_lb=0.061 promote=none\n",
			"FOLD JEV TAG sprint=" + s + " prompt_sha=p-current tag=coverage heads=1 missed=1 prs=nova-tools#2961\n",
			"FOLD JEV TAG sprint=" + s + " prompt_sha=p-current tag=false-confidence heads=1 missed=1 prs=nova-tools#2781\n",
			"FOLD JEV TAG sprint=" + s + " prompt_sha=p-current tag=few-shot heads=5 missed=4 prs=nova-tools#2519,nova-tools#2522,nova-tools#2622,nova-tools#2651\n",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q in\n%s", want, out.String())
			}
		}
		if strings.Contains(out.String(), "FOLD JEV CANDIDATE") {
			t.Errorf("a candidate line with no candidate:\n%s", out.String())
		}
		body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", s+".sexp"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`:jev (:prompt-sha "p-current" :set-sha "`,
			`(type "issue-fix-red-first" :heads 7 :decided 4 :agree 1 :false-pass 1 :false-bounce 2 :unsure 3 :mae "3.75" :sep "-3.00" :pass-prec-lb "0.000" :bounce-prec-lb "0.061" :promote "none")`,
			`(type "conformance-cell" :heads 5 :decided 4 :agree 4 :false-pass 0 :false-bounce 0 :unsure 1 :mae "1.00" :sep "+5.17"`,
			`(tag "false-confidence" :heads 1 :missed 1 :prs ("nova-tools#2781"))`,
			`(tag "coverage" :heads 1 :missed 1 :prs ("nova-tools#2961"))`,
		} {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("fold record lacks %q:\n%s", want, body)
			}
		}
		if n := len(jev.calls["p-current"]); n != 12 {
			t.Errorf("current prompt scored %d rows, want all 12", n)
		}
		if got := client.HGet(ctx, "jev:prompt", "sha").Val(); got != "p-current" {
			t.Errorf("jev:prompt sha %q after a fold with no candidate", got)
		}
	})

	for _, tc := range []struct {
		candidate, reason string
	}{
		{"p-regress", "false_pass 0>2 on issue-fix-red-first"},
		{"p-bouncy", "false_bounce 0>1 on conformance-cell"},
	} {
		t.Run("refuses-"+tc.candidate, func(t *testing.T) {
			_, client, fx := seed(t)
			client.HSet(ctx, "jev:prompt", "sha", "p-current")
			work := workRepo(t)
			o := opts(fx, work)
			jev := &fakeJev{}
			o.Jev = calib(t, tc.candidate, jev)
			var out bytes.Buffer
			res, err := fold.Run(ctx, client, o, &out)
			if err != nil {
				t.Fatalf("fold: %v\n%s", err, out.String())
			}
			if !strings.Contains(res.PromptRefused, tc.reason) {
				t.Fatalf("refusal %q, want it to name %q\n%s", res.PromptRefused, tc.reason, out.String())
			}
			if got := client.HGet(ctx, "jev:prompt", "sha").Val(); got != "p-current" {
				t.Fatalf("a refused candidate moved jev:prompt to %q", got)
			}
			if !strings.Contains(out.String(), "FOLD JEV CANDIDATE sprint="+fx.Sprint+" candidate="+tc.candidate+" current=p-current holdout=4 ") ||
				!strings.Contains(out.String(), " adopted=no reason=") {
				t.Errorf("no refusing candidate line:\n%s", out.String())
			}
			// The candidate saw only the held-out third, never the rows it could be tuned on.
			if got := jev.calls[tc.candidate]; len(got) != 4 || got[0] != 2781 || got[3] != 9005 {
				t.Errorf("candidate scored rows %v, want the held-out 2781 9004 2961 9005", got)
			}
			// The fold itself is recorded: the loser's numbers stay in the fold.
			if st := client.HGet(ctx, "s:"+fx.Sprint, "status").Val(); st != "folded" {
				t.Errorf("status %q: a refused prompt must not stop the fold", st)
			}
			body, _ := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", fx.Sprint+".sexp"))
			if !bytes.Contains(body, []byte(`:candidate (:sha "`+tc.candidate+`"`)) || !bytes.Contains(body, []byte(`:adopted "no"`)) {
				t.Errorf("fold record lacks the refused candidate:\n%s", body)
			}
		})
	}

	t.Run("adopts-p-gates", func(t *testing.T) {
		_, client, fx := seed(t)
		client.HSet(ctx, "jev:prompt", "sha", "p-current")
		work := workRepo(t)
		o := opts(fx, work)
		o.Jev = calib(t, "p-gates", &fakeJev{})
		var out bytes.Buffer
		res, err := fold.Run(ctx, client, o, &out)
		if err != nil || res.PromptRefused != "" {
			t.Fatalf("fold: %v refused %q\n%s", err, res.PromptRefused, out.String())
		}
		h := client.HGetAll(ctx, "jev:prompt").Val()
		if h["sha"] != "p-gates" || h["prev"] != "p-current" || h["fold"] != fx.Sprint {
			t.Fatalf("jev:prompt after adoption: %v", h)
		}
		if !strings.Contains(out.String(), " adopted=yes") {
			t.Errorf("no adopting line:\n%s", out.String())
		}
	})

	t.Run("refuses-a-stale-current", func(t *testing.T) {
		_, client, fx := seed(t)
		client.HSet(ctx, "jev:prompt", "sha", "p-other")
		work := workRepo(t)
		o := opts(fx, work)
		o.Jev = calib(t, "p-gates", &fakeJev{})
		var out bytes.Buffer
		_, err := fold.Run(ctx, client, o, &out)
		if err == nil || !strings.Contains(err.Error(), "p-other") {
			t.Fatalf("calibrating against a prompt the store does not run: %v", err)
		}
		if n := len(strings.Fields(git(t, work, "log", "--format=%H"))); n != 1 {
			t.Fatalf("a refused calibration made a fold commit")
		}
	})

	t.Run("refuses-a-missing-answer", func(t *testing.T) {
		_, client, fx := seed(t)
		work := workRepo(t)
		o := opts(fx, work)
		o.Jev = calib(t, "", &fakeJev{})
		o.Jev.PromptSHA = "p-gates" // answers only 4 of 12: no evidence is not a verdict
		var out bytes.Buffer
		_, err := fold.Run(ctx, client, o, &out)
		if err == nil || !strings.Contains(err.Error(), "no line for nova-tools#2519") {
			t.Fatalf("a scorer that skipped rows: %v", err)
		}
	})
}

// TestJevEvalHelper is the jev-eval stand-in Main runs through --jev-eval: it
// reads calibration rows on stdin and writes ledger-shaped lines on stdout.
func TestJevEvalHelper(t *testing.T) {
	if os.Getenv("FOLD_JEV_HELPER") != "1" {
		t.Skip("helper process for TestMainJevEvalExitCodes")
	}
	prompt := ""
	for i, a := range os.Args {
		if a == "--prompt-sha" && i+1 < len(os.Args) {
			prompt = os.Args[i+1]
		}
	}
	set, err := fold.ReadCalibSet(os.Stdin)
	if err != nil {
		os.Exit(9)
	}
	lines, _ := (&fakeJev{}).Score(context.Background(), prompt, set.Rows)
	enc := json.NewEncoder(os.Stdout)
	for _, l := range lines {
		_ = enc.Encode(map[string]any{"repo": l.Repo, "pr": l.PR, "head": l.Head, "verdict": l.Verdict, "score": l.Score})
	}
	os.Exit(0)
}

// Main wires --calib, --prompt-sha, --candidate and --jev-eval: a regressing
// candidate folds the sprint and exits 3 with the current prompt unchanged.
func TestMainJevEvalExitCodes(t *testing.T) {
	t.Setenv("FOLD_JEV_HELPER", "1")
	set := filepath.Join(t.TempDir(), "calib.jsonl")
	if err := os.WriteFile(set, []byte(calibSet), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := os.Args[0] + " -test.run=^TestJevEvalHelper$ --"
	for _, tc := range []struct {
		candidate string
		code      int
		sha       string
	}{{"p-regress", 3, "p-current"}, {"p-gates", 0, "p-gates"}} {
		mr, client, fx := seed(t)
		client.HSet(context.Background(), "jev:prompt", "sha", "p-current")
		var stdout, stderr bytes.Buffer
		code := fold.Main(context.Background(), []string{fx.Sprint, "--store", mr.Addr(), "--work", workRepo(t),
			"--calib", set, "--prompt-sha", "p-current", "--candidate", tc.candidate, "--jev-eval", helper}, &stdout, &stderr)
		if code != tc.code {
			t.Fatalf("candidate %s: exit %d, want %d\nstdout %s\nstderr %s", tc.candidate, code, tc.code, stdout.String(), stderr.String())
		}
		if got := client.HGet(context.Background(), "jev:prompt", "sha").Val(); got != tc.sha {
			t.Fatalf("candidate %s: jev:prompt sha %q, want %q", tc.candidate, got, tc.sha)
		}
		if !strings.Contains(stdout.String(), "FOLD RECORDED sprint="+fx.Sprint) {
			t.Fatalf("candidate %s: the fold was not recorded\n%s", tc.candidate, stdout.String())
		}
	}
}
