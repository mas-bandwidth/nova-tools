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
	PRs    map[string]map[string]string `json:"prs"`
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
	for key, fields := range fx.PRs {
		must(client.HSet(ctx, s+":pr:"+key, fields).Err())
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
		"FOLD SPRINT sprint=" + s + " cards=9 done=7 useful=4 landed=3 usd=2.45 usd_per_useful=0.6125 usd_per_landed=0.816667 unpriced=1 tasks=5 tasks_done=4 receipts=42 useful_min=8\n",
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
