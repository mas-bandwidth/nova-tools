package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISSUE #103, THE DOGFOOD IT CAME FROM: the Freddy swarm at n=64 on Mercury 2.5 (OpenCode)
// reading nova-tools v0.12.0, 2026-09-12. Two of forty jobs read a whole spec and a second
// spec, and OpenCode printed `Error: Rate limit reached: input token limit exceeded` and
// exited 1 after ~215s. The launcher and the dispatcher saw rc=1 and nothing else.
//
// The fixture is that log's own tail, ANSI paint and all, copied off the dead job
// (/Users/glenn/freddy-working-11/logs/20260912T122117Z-freddy-LOCAL-dedup-7253.log). A
// test that invents the provider's sentence is a test of the invention.
func inputLimitFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "harness-input-limit.log"))
	if err != nil {
		t.Fatalf("the provider's own words are the fixture: %v", err)
	}
	if !bytes.Contains(raw, []byte("\x1b[")) {
		t.Fatal("the fixture wants the terminal paint a real harness writes; without it this test cannot say the quote is readable")
	}
	return raw
}

// THE CLASS IS READ FROM A TABLE OF PROVIDER PHRASES, not from one sentence somebody typed
// once: every provider says this in its own words, and the tool meets a new provider every
// time a worker description changes.
func TestTheProvidersInputLimitIsItsOwnFailureClass(t *testing.T) {
	for _, c := range []struct {
		name, log, want string
		extra           []string
	}{
		{
			name: "OpenCode on Mercury 2.5, the two dead jobs of 2026-09-12",
			log:  string(inputLimitFixture(t)),
			want: "Error: Rate limit reached: input token limit exceeded",
		},
		{
			name: "the same sentence shouted",
			log:  "ERROR: RATE LIMIT REACHED: INPUT TOKEN LIMIT EXCEEDED\n",
			want: "ERROR: RATE LIMIT REACHED: INPUT TOKEN LIMIT EXCEEDED",
		},
		{
			name:  "a provider whose words this table does not hold, taught by the worker description",
			log:   "provider: the conversation exceeds this deployment's intake\n",
			want:  "provider: the conversation exceeds this deployment's intake",
			extra: []string{"exceeds this deployment's intake"},
		},
		{
			name: "a plain 429, which is a WAIT and not a size",
			log:  "fake harness: error: the provider answered HTTP 429 Too Many Requests (rate limit)\n",
			want: "",
		},
		{
			name: "a worker writing ABOUT the input limit in its own report",
			log:  "the spec says a task must fit the model's input limit, which this one does\n",
			want: "",
		},
		{
			name: "a clean log",
			log:  "> build\nRESULT.md written\n",
			want: "",
		},
	} {
		got, ok := InputLimited([]byte(c.log), c.extra)
		if (c.want != "") != ok {
			t.Errorf("%s: the input limit was found=%t, want %t (%q)", c.name, ok, c.want != "", got)
			continue
		}
		if got != c.want {
			t.Errorf("%s: the line quoted is\n  %q\nwant\n  %q", c.name, got, c.want)
		}
	}
}

// AND IT IS NOT A RATE LIMIT (#103, beside the rc=429 misclass of #80). `RateLimited`
// matches the words `rate limit`, which are IN the provider's sentence -- so this death was
// read as a 429: the slot was held for the backoff and the SAME task was launched again, to
// spend another 215 seconds and another input proving the same spec still does not fit.
// A request that did not fit is not a wait.
func TestAnInputLimitIsNotReadAsARateLimitToRetry(t *testing.T) {
	log := inputLimitFixture(t)
	if !RateLimited(log) {
		t.Fatal("the provider's sentence holds the words `rate limit`; this test is about what is done with that")
	}
	if _, ok := InputLimited(log, nil); !ok {
		t.Fatal("the input limit is the more specific truth about this log and it is the one that decides")
	}
	// The decision `finish` makes, asked here: an input-limited job is never handed to the
	// 429 path, so it is never re-queued.
	if RetriableRateLimit(log, nil) {
		t.Error("a job that died because its input did not fit is not retried: a second identical run proves the same thing twice")
	}
	if !RetriableRateLimit([]byte("error: HTTP 429 Too Many Requests\n"), nil) {
		t.Error("a plain 429 is still the dispatcher's business: hold the slot, wait, retry once")
	}
}

// A LIMIT THE HARNESS HIT, BACKED OFF FROM AND THEN ANSWERED PAST IS HISTORY (dogfood D5,
// 2026-09-11, arriving by the other road): the completion evidence the supervisor wrote is
// the outcome, and a reap's own end is the supervisor's verdict.
func TestTheEndAnInputLimitMayRewrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "harness.log"), inputLimitFixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, end string
		rc        int
		want      string
	}{
		{"the harness died on it", EndFailed, 1, EndInputLimit},
		{"the harness left no evidence at all", EndUnknown, -1, EndInputLimit},
		{"the harness met it, backed off and answered", EndDone, 0, EndDone},
		{"a worker reaped at its deadline", EndKilled, -1, EndKilled},
		{"a job ended at its budget ceiling", EndBudget, -1, EndBudget},
		{"a job whose usage source stopped being readable", EndUnverifiable, -1, EndUnverifiable},
	} {
		got, quote := InputLimitEnd(dir, c.end, c.rc, nil)
		if got != c.want {
			t.Errorf("%s: end=%s, want %s", c.name, got, c.want)
		}
		if (c.want == EndInputLimit) != (quote != "") {
			t.Errorf("%s: the provider's words ride with the class, got %q", c.name, quote)
		}
	}
	// A job directory with no log at all answers at once and changes nothing.
	if got, _ := InputLimitEnd(t.TempDir(), EndFailed, 1, nil); got != EndFailed {
		t.Errorf("no log is no evidence: end=%s, want %s", got, EndFailed)
	}
}

// THE TASK BUDGET NAMES THE WINDOW IT FITS, and the dispatcher checks it BEFORE the launch.
// The second half of #103: Freddy's own TEAM-SPEC asks a task for exact files and a hard
// budget, and a whole 600-line spec plus a second spec does not fit Mercury's input. A task
// that names `max_input` and hands the harness more than that is refused with the class and
// the MEASURED size, before a provider is paid to say the same thing.
func TestATaskOverItsMaxInputIsRefusedWithTheMeasuredSize(t *testing.T) {
	for _, c := range []struct {
		name     string
		maxInput int
		prompt   int
		want     bool
	}{
		{"a task that names no window", 0, 1 << 20, false},
		{"a prompt inside the window", 40000, 39999, false},
		{"a prompt exactly at the window", 40000, 40000, false},
		{"a prompt over the window", 40000, 40001, true},
	} {
		reason, over := OverMaxInput(Sidecar{MaxInput: c.maxInput}, c.prompt)
		if over != c.want {
			t.Errorf("%s: over=%t, want %t", c.name, over, c.want)
			continue
		}
		if !over {
			continue
		}
		// THE REFUSAL SAYS WHAT THE INPUT WANTS (SPEC-SWARM, exit codes): the measured
		// size and the ceiling, both, so the remedy is a number a person can act on and
		// not an adjective.
		for _, want := range []string{"40001", "40000", "max_input"} {
			if !strings.Contains(reason, want) {
				t.Errorf("%s: the refusal wants %s in it, got %q", c.name, want, reason)
			}
		}
	}
}

// A WORKER DESCRIPTION MAY TEACH THE TOOL ONE MORE PROVIDER'S SENTENCE, and what it teaches
// is ADDED to the table, never substituted for it: a caller naming their own provider's
// words cannot silently un-teach the tool OpenCode's.
func TestAWorkerDescriptionMayNameItsOwnProviderPhrases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.json")
	body := `{"name":"w","provider":"p","model":"m","env_var":"P_KEY","key_file":"` +
		filepath.ToSlash(filepath.Join(dir, "key")) + `","usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],` +
		`"worker_dir":"` + filepath.ToSlash(filepath.Join(dir, "home")) + `","deadline":"20m","input_limit_phrases":["the conversation is too large for this deployment"]}`
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w, problems := LoadWorker(path)
	if len(problems) > 0 {
		t.Fatalf("input_limit_phrases is a field a description may carry: %v", problems)
	}
	if _, ok := InputLimited([]byte("provider: the conversation is too large for this deployment\n"), w.InputLimitPhrases); !ok {
		t.Error("the phrase the description named is matched")
	}
	if _, ok := InputLimited(inputLimitFixture(t), w.InputLimitPhrases); !ok {
		t.Error("and the table's own phrases still are: what a description adds, it adds")
	}
}

// END TO END ON THE RECOVERY PATH: a job whose supervisor recorded `rc=1 end=failed` with
// the provider's refusal in its harness log is named `input-limit`, lands in failed/, and is
// NOT re-queued. This is the shape of the two dead jobs, and it needs no provider.
func TestARecoveredJobThatDiedOnTheInputLimitIsNamed(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	id := NewID(time.Now().UTC(), "inputlimit")
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Files: 3, Tokens: 100000, RC: -1, Job: jobDir, Slot: 1, Started: Stamp(time.Now().UTC())}
	if err := p.Add([]byte("read two whole specs, one lens"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(id, Pending, Running); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), inputLimitFixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := "0123456789ab"
	if err := WriteJSON(ExitPath(jobDir), &ExitRecord{RC: 1, End: EndFailed, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(p.slotPath(1), SlotFile{
		Job: id, JobDir: jobDir, State: SlotLaunched, Pid: 0, Pgid: 0, JobPgid: 0,
		PidStarted: "-", RunnerPid: 0, Nonce: nonce, LaunchedAt: Stamp(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	Run(RunInput{Pool: p, Worker: w, Workers: 1, Hours: 0.0001, Stdout: &out, Stderr: &errb,
		NoSandbox: true, Now: func() time.Time { return time.Now().UTC() }})
	if !strings.Contains(out.String(), "end="+EndInputLimit) {
		t.Errorf("the RUN line names the class a reader triages by:\n%s%s", out.String(), errb.String())
	}
	moved, err := p.ReadSidecar(Failed, id)
	if err != nil {
		t.Fatalf("an input-limited job lands in failed/: %v", err)
	}
	if moved.End != EndInputLimit {
		t.Errorf("the sidecar wants end=%s, got %q", EndInputLimit, moved.End)
	}
	if !strings.Contains(moved.Limit, "input token limit exceeded") {
		t.Errorf("the sidecar carries the provider's own words so triage never opens a log, got %q", moved.Limit)
	}
	pending, _ := p.List(Pending)
	if len(pending) != 0 {
		t.Errorf("a task too big for the model is not re-queued; %d pending", len(pending))
	}
	row, err := os.ReadFile(p.UsagePath(id))
	if err != nil {
		t.Fatalf("the usage row outlives the job: %v", err)
	}
	if !strings.Contains(string(row), "\t"+EndInputLimit+"\t") {
		t.Errorf("the `end` column is what the token ledger reads:\n%s", row)
	}
}
