package harvest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// The fake ssh is a bash script in t.TempDir() (a temp-dir program is a fake
// to testguard). Like ssh it drops the options and the target, joins the rest
// into the remote command line, logs it, and hands it to a zsh-login-shell
// emulator -- the bench's login shell, which is what broke #3291 -- before
// bash runs it. No zsh binary is needed.
//
// The emulator: `$name:X` outside single quotes is a zsh history modifier on
// the parameter (:r drops the extension, :h the last path part, :t keeps it,
// :e keeps the extension, :l/:u change case), where bash reads a literal
// colon. It is rewritten to the bash expansion with zsh's meaning.
const fakeSSHScript = `#!/bin/bash
while [ "$#" -gt 1 ] && [ "$1" = "-o" ]; do shift 2; done
[ "$#" -ge 2 ] || { echo "fake ssh: no target or remote command" >&2; exit 255; }
shift
line="$*"
[ -z "${FAKE_SSH_LOG:-}" ] || printf '%s\n' "$line" >> "$FAKE_SSH_LOG"
zsh=$(printf '%s' "$line" | perl -0777 -pe '
  my %m = (r => "%.*", h => "%/*", t => "##*/", e => "##*.", l => ",,", u => "^^");
  s{(\x27[^\x27]*\x27)|\$([A-Za-z_][A-Za-z0-9_]*):([hrtelu])}{defined $1 ? $1 : "\${$2$m{$3}}"}ge')
exec bash -c "$zsh"
`

func writeFakeSSH(t *testing.T, dir, log string) string {
	t.Helper()
	ssh := filepath.Join(dir, "ssh")
	script := strings.Replace(fakeSSHScript, "#!/bin/bash\n", "#!/bin/bash\nFAKE_SSH_LOG="+strconv.Quote(log)+"\n", 1)
	if err := os.WriteFile(ssh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return ssh
}

// zshForge is GitHub for the steps: it counts creates, can fail a create's
// reply after the PR is made (timeout, 5xx, 422-exists), and can hide a new
// PR from the lookup for k reads.
type zshForge struct {
	mu        sync.Mutex
	prs       map[int]harvest.PR
	next      int
	creates   int
	failReply string // "", "timeout", "5xx" or "422"
	hide      int    // lookups that miss the next created PR
	hidden    int    // the PR being hidden
	lookups   int
}

func newZshForge() *zshForge { return &zshForge{prs: map[int]harvest.PR{}, next: 100} }

func (f *zshForge) put(pr harvest.PR) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr.State == "" {
		pr.State = "open"
	}
	f.prs[pr.Number] = pr
}

func (f *zshForge) ListPRs(_ context.Context, _, branch string) ([]harvest.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	var out []harvest.PR
	for n := 1; n <= f.next+10; n++ {
		pr, ok := f.prs[n]
		if !ok || pr.Ref != branch {
			continue
		}
		if n == f.hidden && f.hide > 0 {
			f.hide--
			continue
		}
		out = append(out, pr)
	}
	return out, nil
}

func (f *zshForge) FindOpenPR(ctx context.Context, repo, branch string) (harvest.PR, bool, error) {
	prs, err := f.ListPRs(ctx, repo, branch)
	if err != nil || len(prs) == 0 {
		return harvest.PR{}, false, err
	}
	return prs[0], true, nil
}

// OpenPR makes the PR from the branch's pushed head (read from the origin),
// then answers as configured.
func (f *zshForge) OpenPR(_ context.Context, repo, branch, _, _, _ string) (harvest.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range f.prs {
		if pr.Ref == branch && pr.State == "open" {
			return harvest.PR{}, fmt.Errorf("%w: HTTP 422: A pull request already exists for %s", harvest.ErrAmbiguous, branch)
		}
	}
	f.next++
	f.creates++
	pr := harvest.PR{Number: f.next, Ref: branch, Head: pushedHead(branch), State: "open",
		URL: fmt.Sprintf("https://github.com/mas-bandwidth/%s/pull/%d", repo, f.next)}
	f.prs[pr.Number] = pr
	switch f.failReply {
	case "timeout":
		f.hidden = pr.Number
		return harvest.PR{}, fmt.Errorf("%w: POST pulls: context deadline exceeded (Client.Timeout)", harvest.ErrAmbiguous)
	case "5xx":
		f.hidden = pr.Number
		return harvest.PR{}, fmt.Errorf("%w: HTTP 502: Bad Gateway", harvest.ErrAmbiguous)
	case "422":
		f.hidden = pr.Number
		return harvest.PR{}, fmt.Errorf("%w: HTTP 422: A pull request already exists", harvest.ErrAmbiguous)
	}
	return pr, nil
}

func (f *zshForge) ReadPR(_ context.Context, _ string, n int) (harvest.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[n]
	if !ok {
		return harvest.PR{}, fmt.Errorf("HTTP 404: pull %d", n)
	}
	return pr, nil
}

// origins maps a branch to the bare origin it was pushed to, so the fake
// forge reads a PR head from what the harvest really pushed.
var origins sync.Map

func pushedHead(branch string) string {
	v, ok := origins.Load(branch)
	if !ok {
		return ""
	}
	out, err := exec.Command("git", "--git-dir", v.(string), "rev-parse", "-q", "--verify", "refs/heads/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// zshBench is one fixture card on one bench: a bare origin (GitHub's git), a
// results dir whose repo the wrapper's commit step committed, the card hash
// as card end leaves it, and a fake ssh whose login shell is the zsh emulator.
type zshBench struct {
	t             *testing.T
	c             *redis.Client
	st            *store.Store
	forge         *zshForge
	sprint, bench string
	label, branch string
	sha, origin   string
	ssh, sshLog   string
	repo          string
	sleeps        []time.Duration
	passes        int
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newZshBench(t *testing.T, label string) *zshBench {
	t.Helper()
	for _, bin := range []string{"bash", "git", "perl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s unavailable", bin)
		}
	}
	c := startRedis(t)
	z := &zshBench{t: t, c: c, st: store.New(c), forge: newZshForge(), sprint: "control-2932", bench: "superman", label: label}
	z.branch = card.WrapperBranch(z.sprint, label, 1)
	dir := t.TempDir()
	z.origin = filepath.Join(dir, "origin.git")
	gitRun(t, dir, "init", "-q", "--bare", z.origin)
	seed := filepath.Join(dir, "seed")
	gitRun(t, dir, "init", "-q", seed)
	gitRun(t, seed, "commit", "-q", "--allow-empty", "-m", "base")
	gitRun(t, seed, "push", "-q", z.origin, "HEAD:refs/heads/dev")
	origins.Store(z.branch, z.origin)
	t.Cleanup(func() { origins.Delete(z.branch) })

	// The wrapper's commit step on an uncommitted out/repo, as card end
	// leaves it in the absolute results dir.
	identity := z.sprint + "/" + label + "/09fbedc9/" + z.bench + "/1"
	results := filepath.Join(dir, "results", filepath.FromSlash(identity))
	z.repo = filepath.Join(results, "repo")
	if err := os.MkdirAll(results, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, results, "clone", "-q", "--branch", "dev", z.origin, z.repo)
	if err := os.WriteFile(filepath.Join(z.repo, "work.txt"), []byte("probe-harvest-2932 superman\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := card.CommitOutput(z.repo, z.branch, "RESULT: "+label+" OK", z.bench)
	if err != nil || commit.Note != "COMMITTED" {
		t.Fatalf("commit step = %+v, %v", commit, err)
	}
	z.sha = commit.SHA

	ctx := context.Background()
	key := "s:" + z.sprint + ":card:" + label
	if err := c.HSet(ctx, key, "kind", "model", "repo", "nova-tools", "base", "dev", "base_sha", "09fbedc9",
		"state", "ended", "outcome", "DONE", "reason", "done", "bench", z.bench, "attempt", "1",
		"identity", identity, "token_sha", "abcdef012345", "branch", z.branch,
		"pushed_sha", z.sha, "results", results).Err(); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "s:"+z.sprint+":idx:card:ended", label)
	c.SAdd(ctx, "s:"+z.sprint+":bench:"+z.bench+":ended", label)
	c.HSet(ctx, "bench:"+z.bench+":beat", "host", "superman.fixture", "user", "nova")

	z.sshLog = filepath.Join(dir, "ssh.log")
	z.ssh = writeFakeSSH(t, dir, z.sshLog)
	return z
}

// pass is one `card harvest --once` pass for the bench with a new lease
// instance; fault stops it at one step.
func (z *zshBench) pass(faultAt string) harvest.BenchResult {
	z.t.Helper()
	z.passes++
	opt := harvest.Options{
		Sprint: z.sprint, Benches: []string{z.bench}, Clock: time.Minute,
		Instance: fmt.Sprintf("pass-%d", z.passes),
		Forge:    z.forge,
		Pusher:   harvest.SSHPusher{SSH: z.ssh},
		Sleep: func(_ context.Context, d time.Duration) error {
			z.sleeps = append(z.sleeps, d)
			return nil
		},
	}
	if faultAt != "" {
		opt.Fault = func(step string) error {
			if step == faultAt {
				return errors.New("process died")
			}
			return nil
		}
	}
	res := harvest.Run(context.Background(), z.st, opt)[0]
	if faultAt != "" {
		// A dead worker releases nothing; its lease runs out on its TTL.
		z.c.Del(context.Background(), "lease:harvest:"+z.bench)
	}
	return res
}

func (z *zshBench) card() map[string]string {
	return z.c.HGetAll(context.Background(), "s:"+z.sprint+":card:"+z.label).Val()
}

func (z *zshBench) receipts() int {
	n := 0
	for _, e := range z.c.XRange(context.Background(), "s:"+z.sprint+":log", "-", "+").Val() {
		if e.Values["to"] == "harvested" && e.Values["id"] == z.label {
			n++
		}
	}
	return n
}

func (z *zshBench) idem() string {
	return z.c.HGet(context.Background(), "s:"+z.sprint+":idem", "pr:nova-tools:"+z.branch).Val()
}

// harvested is the end state every subtest reaches: harvested through (e),
// creates as given, one HARVESTED receipt, pr the PR whose REST head.ref is
// the branch and head.sha is pushed_sha, and the branch on the origin intact.
func (z *zshBench) harvested(creates int) {
	z.t.Helper()
	h := z.card()
	if h["state"] != "harvested" || h["harvest_step"] != "harvested" || h["head"] != z.sha {
		z.t.Fatalf("card = %v; want state and harvest_step harvested at %s", h, z.sha)
	}
	if z.forge.creates != creates {
		z.t.Fatalf("forge.creates = %d, want %d", z.forge.creates, creates)
	}
	if n := z.receipts(); n != 1 {
		z.t.Fatalf("%d HARVESTED receipts in s:<S>:log, want 1", n)
	}
	n, _ := strconv.Atoi(h["pr"])
	pr, err := z.forge.ReadPR(context.Background(), "nova-tools", n)
	if err != nil || pr.Ref != z.branch || pr.Head != z.sha {
		z.t.Fatalf("card pr %s reads back %+v (%v); want head.ref %s head.sha %s", h["pr"], pr, err, z.branch, z.sha)
	}
	if z.idem() != h["pr"] {
		z.t.Fatalf("idem %q, want the card's pr %s", z.idem(), h["pr"])
	}
	if tip := gitRun(z.t, "", "--git-dir", z.origin, "rev-parse", "refs/heads/"+z.branch); tip != z.sha {
		z.t.Fatalf("origin %s at %s, want pushed_sha %s", z.branch, tip, z.sha)
	}
}

func (z *zshBench) failedWith(res harvest.BenchResult, code string) {
	z.t.Helper()
	if len(res.Failed) != 1 || res.Failed[0].Code != code {
		z.t.Fatalf("pass = %+v; want one HARVEST-FAILED err=%s", res, code)
	}
}

// TestHarvestZshBenchEndToEnd is the #2932 DONE-WHEN: a card the wrapper
// committed is pushed through a zsh login shell intact, gets exactly one PR
// whose REST head is pushed_sha, and is harvested; a second pass writes
// nothing; and a pass that dies at any durable step is finished by the next
// pass with one PR and one receipt.
func TestHarvestZshBenchEndToEnd(t *testing.T) {
	t.Run("zsh-emulator-has-teeth", func(t *testing.T) {
		for _, bin := range []string{"bash", "perl"} {
			if _, err := exec.LookPath(bin); err != nil {
				t.Skipf("%s unavailable", bin)
			}
		}
		dir := t.TempDir()
		ssh := writeFakeSSH(t, dir, filepath.Join(dir, "ssh.log"))
		run := func(remote string) string {
			t.Helper()
			out, err := exec.Command(ssh, "-o", "BatchMode=yes", "nova@superman", remote).CombinedOutput()
			if err != nil {
				t.Fatalf("fake ssh %q: %v %s", remote, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		// The raw form #3291 sent: the login shell reads $x:r as a modifier.
		if got := run(`x=main; echo refs/heads/$x:refs/harvest/y`); got != "refs/heads/mainefs/harvest/y" {
			t.Fatalf("zsh login emulator gave %q; want the #3291 mangle refs/heads/mainefs/harvest/y", got)
		}
		bash, err := exec.Command("bash", "-c", `x=main; echo refs/heads/$x:refs/harvest/y`).Output()
		if err != nil || strings.TrimSpace(string(bash)) != "refs/heads/main:refs/harvest/y" {
			t.Fatalf("bash gave %q (%v)", bash, err)
		}
		// benchsh's form: a single-quoted word is literal to zsh too.
		if got := run(`echo '$x:refs/y' "$(printf %s ok)"`); got != "$x:refs/y ok" {
			t.Fatalf("quoted word mangled: %q", got)
		}
	})

	t.Run("happy-then-second-pass-noop", func(t *testing.T) {
		t.Parallel()
		z := newZshBench(t, "probe-harvest-2932-superman")
		res := z.pass("")
		if res.Err != nil || len(res.Cards) != 1 || len(res.Failed) != 0 || res.Cards[0].Via != "opened" {
			t.Fatalf("first pass = %+v; want one card opened and harvested", res)
		}
		z.harvested(1)
		log, err := os.ReadFile(z.sshLog)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(log)), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "bash -s -- '") || strings.Contains(lines[0], "$") {
			t.Fatalf("remote command lines %q; want one `bash -s -- '<quoted>'` with nothing for zsh to parse", lines)
		}
		before := z.card()
		idem := z.c.HGetAll(context.Background(), "s:"+z.sprint+":idem").Val()
		logLen := z.c.XLen(context.Background(), "s:"+z.sprint+":log").Val()
		res = z.pass("")
		if res.Err != nil || len(res.Cards) != 0 || len(res.Failed) != 0 {
			t.Fatalf("second pass = %+v; want n=0", res)
		}
		if z.forge.creates != 1 || fmt.Sprint(z.card()) != fmt.Sprint(before) ||
			fmt.Sprint(z.c.HGetAll(context.Background(), "s:"+z.sprint+":idem").Val()) != fmt.Sprint(idem) ||
			z.c.XLen(context.Background(), "s:"+z.sprint+":log").Val() != logLen {
			t.Fatal("the second pass opened a PR or wrote to the card, idem or log")
		}
	})

	crash := []struct{ name, at, step string }{
		{"crash-after-push", harvest.FaultAfterPush, harvest.StepPushed},
		{"crash-after-intent-before-create", harvest.FaultAfterIntent, harvest.StepIntent},
		{"crash-after-create-before-record", harvest.FaultAfterCreate, harvest.StepIntent},
		{"crash-after-record-before-receipt", harvest.FaultAfterRecord, harvest.StepPublished},
	}
	for _, tc := range crash {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			z := newZshBench(t, "card-"+tc.at)
			res := z.pass(tc.at)
			if res.Err == nil || len(res.Cards) != 0 {
				t.Fatalf("faulted pass = %+v; want it stopped at %s", res, tc.at)
			}
			h := z.card()
			if h["state"] != "ended" || h["harvest_step"] != tc.step || z.receipts() != 0 {
				t.Fatalf("after the crash at %s: card %v receipts %d; want ended at harvest_step %s, no receipt", tc.at, h, z.receipts(), tc.step)
			}
			if tc.at == harvest.FaultAfterCreate && z.idem() != "" {
				t.Fatalf("idem %q written before the PR was verified", z.idem())
			}
			res = z.pass("")
			if res.Err != nil || len(res.Cards) != 1 {
				t.Fatalf("fresh pass = %+v; want the card harvested", res)
			}
			creates := 1
			z.harvested(creates)
		})
	}

	t.Run("create-ambiguous-found-by-readback", func(t *testing.T) {
		t.Parallel()
		z := newZshBench(t, "card-readback-found")
		z.forge.failReply, z.forge.hide = "5xx", 1
		res := z.pass("")
		if res.Err != nil || len(res.Cards) != 1 || res.Cards[0].Via != "readback" {
			t.Fatalf("pass = %+v; want the PR found by the readback and harvested", res)
		}
		if len(z.sleeps) != 2 || z.sleeps[0] != time.Second || z.sleeps[1] != 2*time.Second {
			t.Fatalf("readback waits %v; want 1s then 2s (found on the second lookup)", z.sleeps)
		}
		z.harvested(1)
	})

	t.Run("create-ambiguous-not-found", func(t *testing.T) {
		t.Parallel()
		z := newZshBench(t, "card-readback-lost")
		z.forge.failReply, z.forge.hide = "timeout", 3
		res := z.pass("")
		z.failedWith(res, harvest.CodeCreateAmbiguous)
		if fmt.Sprint(z.sleeps) != "[1s 2s 4s]" || z.forge.creates != 1 {
			t.Fatalf("readback waits %v creates %d; want [1s 2s 4s] and one POST", z.sleeps, z.forge.creates)
		}
		if h := z.card(); h["harvest_step"] != harvest.StepIntent || h["state"] != "ended" || z.idem() != "" {
			t.Fatalf("card %v idem %q; want it left at intent, nothing reserved", h, z.idem())
		}
		z.forge.failReply = ""
		lookups := z.forge.lookups
		res = z.pass("")
		if res.Err != nil || len(res.Cards) != 1 || res.Cards[0].Via != "rest" || z.forge.lookups != lookups+1 {
			t.Fatalf("next pass = %+v lookups %d; want the lookup first, found, no second POST", res, z.forge.lookups-lookups)
		}
		z.harvested(1)
	})

	t.Run("idem-names-other-pr", func(t *testing.T) {
		t.Parallel()
		z := newZshBench(t, "card-idem-other")
		ctx := context.Background()
		z.forge.put(harvest.PR{Number: 7, Ref: "nova/control-2932/someone-else-a1", Head: strings.Repeat("7", 40)})
		// PR 9 is this branch's, at pushed_sha, once the push lands.
		gitRun(t, z.repo, "push", "-q", "origin", z.sha+":refs/heads/"+z.branch)
		z.forge.put(harvest.PR{Number: 9, Ref: z.branch, Head: z.sha})
		z.c.HSet(ctx, "s:"+z.sprint+":idem", "pr:nova-tools:"+z.branch, "7")
		z.c.HSet(ctx, "s:"+z.sprint+":card:"+z.label, "harvest_step", "published")
		res := z.pass("")
		z.failedWith(res, harvest.CodeIdemMismatch)
		if z.forge.creates != 0 || z.receipts() != 0 || z.card()["state"] != "ended" || z.idem() != "7" {
			t.Fatalf("creates %d receipts %d card %v idem %s; want no create, no receipt, nothing moved", z.forge.creates, z.receipts(), z.card(), z.idem())
		}

		// ns_harvest_pr: a fenced lease writes nothing, and a card with no
		// harvest_step (never pushed) is refused.
		other := "card-no-step"
		c := z.c
		c.HSet(ctx, "s:"+z.sprint+":card:"+other, "state", "ended", "outcome", "DONE", "bench", z.bench,
			"attempt", "1", "repo", "nova-tools", "pushed_sha", z.sha)
		otherBranch := card.WrapperBranch(z.sprint, other, 1)
		if r := c.FCall(ctx, harvest.FunctionLease, nil, z.bench, "holder", "tok", 60000).Val(); r != "TAKEN" {
			t.Fatalf("lease = %v", r)
		}
		fenced := c.FCall(ctx, harvest.FunctionPR, nil, z.sprint, z.bench, "holder", "stolen", other, "nova-tools", otherBranch, "11").Val()
		if fenced != "FENCED|" {
			t.Fatalf("ns_harvest_pr with a fenced token = %v, want FENCED|", fenced)
		}
		refused := c.FCall(ctx, harvest.FunctionPR, nil, z.sprint, z.bench, "holder", "tok", other, "nova-tools", otherBranch, "11").Val()
		if refused != "STEP|" {
			t.Fatalf("ns_harvest_pr on a card with no harvest_step = %v, want STEP|", refused)
		}
		if v := c.HGet(ctx, "s:"+z.sprint+":idem", "pr:nova-tools:"+otherBranch).Val(); v != "" {
			t.Fatalf("idem written (%s) by a fenced or refused ns_harvest_pr", v)
		}
		if s := c.HGet(ctx, "s:"+z.sprint+":card:"+other, "harvest_step").Val(); s != "" {
			t.Fatalf("harvest_step %q written by a fenced or refused ns_harvest_pr", s)
		}
	})
}

// TestHarvestStepForwardOnly (#2932 control 2): ns_harvest_step moves only
// forward, refuses published and harvested (their own writers), and a fenced
// token writes nothing.
func TestHarvestStepForwardOnly(t *testing.T) {
	c := startRedis(t)
	ctx := context.Background()
	seedEnded(t, c, "ctl-a", "card1", "model", "DONE", sha("card1"))
	if r := c.FCall(ctx, harvest.FunctionLease, nil, "ctl-a", "me", "tok", 60000).Val(); r != "TAKEN" {
		t.Fatalf("lease = %v", r)
	}
	step := func(token, s string) string {
		return fmt.Sprint(c.FCall(ctx, harvest.FunctionStep, nil, sprint, "ctl-a", "me", token, "card1", s).Val())
	}
	field := func(f string) string { return c.HGet(ctx, "s:"+sprint+":card:card1", f).Val() }
	if r := step("stolen", "pushed"); r != "FENCED|" || field("harvest_step") != "" {
		t.Fatalf("fenced step = %s, harvest_step %q; want FENCED| and nothing written", r, field("harvest_step"))
	}
	if r := step("tok", "pushed"); r != "OK|pushed" || field("harvest_step") != "pushed" || field("harvest_step_at") == "" {
		t.Fatalf("pushed = %s", r)
	}
	at := field("harvest_step_at")
	if r := step("tok", "pushed"); r != "OK|pushed" || field("harvest_step_at") != at {
		t.Fatalf("pushed again = %s (at %s -> %s); want a no-op", r, at, field("harvest_step_at"))
	}
	if r := step("tok", "intent"); r != "OK|intent" || field("harvest_step") != "intent" || field("harvest_intent_at") == "" {
		t.Fatalf("intent = %s", r)
	}
	if r := step("tok", "intent"); r != "OK|intent" {
		t.Fatalf("intent again = %s, want a no-op", r)
	}
	if r := step("tok", "pushed"); r != "BACKWARD|intent" || field("harvest_step") != "intent" {
		t.Fatalf("backward = %s, harvest_step %s", r, field("harvest_step"))
	}
	for _, s := range []string{"published", "harvested", "bogus"} {
		if r := step("tok", s); r != "REFUSED|"+s || field("harvest_step") != "intent" {
			t.Fatalf("%s = %s; want REFUSED|%s and nothing written", s, r, s)
		}
	}
}

// TestHarvestDueSkipsNoCommit (#2932 control 9): an ended DONE card with
// pushed_sha "-" committed nothing: it is not in ns_harvest_due's rows and a
// pass runs no push for it.
func TestHarvestDueSkipsNoCommit(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	c.HSet(ctx, "bench:ctl-a:beat", "host", "ctl-a.fixture", "user", "nova")
	seedEnded(t, c, "ctl-a", "read-card", "model", "DONE", "-")
	seedEnded(t, c, "ctl-a", "work-card", "model", "DONE", sha("work-card"))
	rows, err := c.FCallRO(ctx, harvest.FunctionDue, nil, sprint, "ctl-a", 256).StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3+10 || rows[3] != "work-card" {
		t.Fatalf("due = %q; want only work-card (rows of 10)", rows)
	}
	forge := newForge()
	forge.heads["nova/"+sprint+"/work-card-a1"] = sha("work-card")
	pusher := &fixturePusher{pushes: map[string]int{}}
	res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: time.Minute,
		Instance: "due-1", Forge: forge, Pusher: pusher})
	if res[0].Err != nil || len(res[0].Cards) != 1 || len(res[0].Failed) != 0 {
		t.Fatalf("pass = %+v; want work-card only", res[0])
	}
	if pusher.pushes["nova/"+sprint+"/read-card-a1"] != 0 {
		t.Fatal("a pushed_sha - card was pushed")
	}
	if s := c.HGet(ctx, "s:"+sprint+":card:read-card", "state").Val(); s != "ended" {
		t.Fatalf("read-card %s, want ended (its read is #3036's report rule)", s)
	}
}

// TestHarvestRelativeResultsRefused (#2932 control 7): a relative results
// in the card hash is HARVEST-FAILED err=results-relative and runs no ssh.
func TestHarvestRelativeResultsRefused(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	c.HSet(ctx, "bench:ctl-a:beat", "host", "ctl-a.fixture", "user", "nova")
	seedEnded(t, c, "ctl-a", "rel-card", "model", "DONE", sha("rel-card")) // results is the relative identity
	dir := t.TempDir()
	log := filepath.Join(dir, "ssh.log")
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/bash\necho \"$@\" >> "+strconv.Quote(log)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"}, Clock: time.Minute,
		Instance: "rel-1", Forge: newForge(), Pusher: harvest.SSHPusher{SSH: ssh}})
	if len(res[0].Failed) != 1 || res[0].Failed[0].Code != harvest.CodeResultsRelative {
		t.Fatalf("pass = %+v; want HARVEST-FAILED err=results-relative", res[0])
	}
	if _, err := os.Stat(log); err == nil {
		b, _ := os.ReadFile(log)
		t.Fatalf("ssh ran for a relative results dir: %q", b)
	}
}

// TestHarvestLoopEveryOpenSprintEveryBench (#2932 control 6): --sprint all
// --bench all covers every sprint in `sprints` and every bench in `benches`,
// and a bench with no beat is skipped while the others finish.
func TestHarvestLoopEveryOpenSprintEveryBench(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	sprints := []string{sprint, "control-0000c014"}
	c.SAdd(ctx, "sprints", sprints[0], sprints[1])
	c.SAdd(ctx, "benches", "ctl-a", "ctl-b", "ctl-nobeat")
	forge := newForge()
	for _, b := range []string{"ctl-a", "ctl-b"} {
		c.HSet(ctx, "bench:"+b+":beat", "host", b+".fixture", "user", "nova")
	}
	for _, s := range sprints {
		for _, b := range []string{"ctl-a", "ctl-b", "ctl-nobeat"} {
			label := s[len(s)-4:] + "-" + b
			key := "s:" + s + ":card:" + label
			c.HSet(ctx, key, "kind", "model", "repo", "nova-tools", "base", "dev", "state", "ended", "outcome", "DONE",
				"bench", b, "attempt", "1", "identity", s+"/"+label, "pushed_sha", sha(label), "results", "/r/"+label)
			c.SAdd(ctx, "s:"+s+":idx:card:ended", label)
			c.SAdd(ctx, "s:"+s+":bench:"+b+":ended", label)
			forge.heads["nova/"+s+"/"+label+"-a1"] = sha(label)
		}
	}
	plan, err := harvest.NewPlan(ctx, st, "all", []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(plan.Sprints) != "[control-0000c013 control-0000c014]" || fmt.Sprint(plan.Benches) != "[ctl-a ctl-b]" ||
		fmt.Sprint(plan.NoBeat) != "[ctl-nobeat]" {
		t.Fatalf("plan = %+v; want both sprints, ctl-a and ctl-b, ctl-nobeat skipped", plan)
	}
	pusher := &fixturePusher{pushes: map[string]int{}}
	total := 0
	for _, s := range plan.Sprints {
		for _, r := range harvest.Run(ctx, st, harvest.Options{Sprint: s, Benches: plan.Benches, Clock: time.Minute,
			Instance: "all-1", Forge: forge, Pusher: pusher}) {
			if r.Err != nil || len(r.Failed) != 0 {
				t.Fatalf("%s %s: %+v", s, r.Bench, r)
			}
			total += len(r.Cards)
		}
	}
	if total != 4 {
		t.Fatalf("harvested %d cards, want 4 (2 sprints x 2 beating benches)", total)
	}
	for _, s := range sprints {
		label := s[len(s)-4:] + "-ctl-nobeat"
		if st := c.HGet(ctx, "s:"+s+":card:"+label, "state").Val(); st != "ended" {
			t.Fatalf("%s on the no-beat bench is %s, want ended", label, st)
		}
	}
	named, err := harvest.NewPlan(ctx, st, sprint, []string{"ctl-b", "ctl-nobeat"})
	if err != nil || fmt.Sprint(named.Sprints) != "["+sprint+"]" || fmt.Sprint(named.Benches) != "[ctl-b]" || fmt.Sprint(named.NoBeat) != "[ctl-nobeat]" {
		t.Fatalf("named plan = %+v, %v", named, err)
	}
}
