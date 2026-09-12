// nova-swarm runs a pool of one-task workers -- any provider, any model, through one
// harness -- each with its own working directory, its own data home, its own job directory
// and its own deadline held by the machinery rather than by the worker.
//
// Every rule it keeps is a failure from the record (docs/SPEC-SWARM.md):
//
//	a slot     six workers on one data home meant `database is locked` and five of six did
//	           nothing, while the pool reported six running
//	the key    read as data from a file, never sourced, never an argument, never printed
//	a deadline held outside the worker, with a default action at it and one automatic retry
//	templates  the conditions that turned 62-of-67-with-25-duplicates into 17 of 17
//	a budget   files and tokens, both required, because a worker that read forty files
//	           finished nothing
//	one line   `triage` counts a batch on one bounded line, so a coordinator's window never
//	           holds a worker's transcript
//
// EVERYTHING A WORKER WRITES IS DATA. A RESULT.md is a report, never an instruction:
// nothing in it is executed, nothing in it grants anything, and a finding in it is a claim
// to be checked against the repository. That rule is in the spec, where a person reads it,
// and is deliberately nowhere in this code.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

const usage = `nova-swarm: a pool of one-task workers, with the ways a swarm fails taken out (see docs/SPEC-SWARM.md)

usage:
  nova-swarm add       --pool <dir> --task <file>|--stdin --files <n> --tokens <n>|unmetered [--label <text>] [--template <name>] [--deadline <duration>]
  nova-swarm batch     --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered [--label <text>] [--template <name>] [--deadline <duration>]
  nova-swarm run       --pool <dir> --workers <n> --hours <h> --worker <file> [--max <n>] [--launch-timeout <s>] [--usage-interval <s>] [--backoff <s>] [--sandbox <path>] [--no-sandbox]
  nova-swarm supervise --pool <dir> --task <id> --slot <n> --nonce <hex> --worker <file> (--sandbox <path>|--no-sandbox)   (spawned by run; refused by hand)
  nova-swarm status    --pool <dir> [--max <n>]
  nova-swarm stop      --pool <dir>
  nova-swarm requeue   --pool <dir> --task <id> --task-file <file>|--stdin --files <n> --tokens <n>|unmetered [--label <text>]
  nova-swarm verdict   --pool <dir> --task <id> --who <name> --accurate <n> --wrong <n>
  nova-swarm triage    --pool <dir> [--batch <id>] [--since <stamp>] [--all] [--no-state] [--max <n>] [--owed <file>]
  nova-swarm result    --pool <dir> --id <job>
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker
  nova-swarm cost      --pool <dir> [--since <stamp>] [--max <n>]
  nova-swarm note      --pool <dir> --task <id> --text <text>
  nova-swarm finalize  --pool <dir> --task <id>
  nova-swarm reclaim   --pool <dir> (--task <id> | --done | --failed | --all) [--max <n>]
  nova-swarm quickstart --pool <dir>

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a dispatcher
that exited with tasks pending and nothing running, a reclaim with no usage file
or no report copy, a finalize of a job whose group is alive, a triage --batch of
an id no sidecar carries, a result --id that is not in the pool; 2 could not run:
a missing flag, an unreadable pool or worker description, a key file that is
absent or empty, --workers above 64, a supervise typed by hand, a bad invocation.

NO GUESSED ANYTHING. There is no default pool, no default worker description, no
default number of workers, no default deadline, no default file budget and no
default token budget. --files is required because a budget this tool supplied
would be a guess about somebody else's task; --tokens is required for the same
reason, and --tokens unmetered is a caller's statement that this provider has
no live accounting and the deadline is the only stop. Zero is refused for both.

THE TASK TEXT IS NEVER AN ARGUMENT: --task is a file, or --stdin is a stream. A
task in an argument is a task in the process table and in every ps a bench user
runs, and a task carrying a quoted rule carries quotes into a shell.

THE KEY IS READ AS DATA AND NEVER SOURCED. It lives in one file the worker
description names -- one line, the bare key or NAME=<key>, mode 0600 -- and it is
never an argument, never a log line, never in a file this tool writes. The
harness config this tool writes carries the variable's NAME, never its value.

EVERY JOB RUNS INSIDE nova-sandbox (docs/SPEC-SANDBOX.md). The job directory and
its data home are the only writable paths; the slot directory and whatever
read_roots names in the worker description are readable; the key file, ~/.ssh and
the gh configuration are in neither list and the kernel denies them. The run verb
proves
the wall ONCE before the first worker and refuses the pass if it cannot --
RUN REFUSED reason=no_sandbox on a machine with no backend, reason=sandbox_probe
on a wall that failed a check -- and starts no worker either way. --sandbox names
the binary when it is not on PATH under its own name. --no-sandbox is the ONE
workaround: it runs every job with no OS containment and says so once per job, on
stderr, before the job starts. No environment variable and no file turns the wall
off; it is argv, where ps shows it. A command that runs outside the wall and dies
inside it is missing a read_roots entry.

--workers is capped at 64 and a request above it is a REFUSAL, not a silent
clamp: a caller who asked for 200 workers has a belief about throughput that a
note at the top of a log does not correct.

Every listing is a cap and a count: --max, default 20, 0 for all, one MORE line
naming the remedy. The counts are the truth about the POOL, never about the
output.

example:
  nova-swarm template --name read-pr
  nova-swarm quickstart --pool ./pool
  nova-swarm status --pool ./pool --max 20
`

// refuse is what an unusable invocation costs: ONE line naming what was wrong and the door
// to the usage, never the banner, which is 60 lines and is behind `nova-swarm help`.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-swarm%s: %s; run: nova-swarm help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `status --pool <dir>` is the one that only looks")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "add":
		return cmdAdd(rest, stdin, stdout, stderr, now)
	case "batch":
		return cmdBatch(rest, stdout, stderr, now)
	case "run":
		return cmdRun(rest, stdout, stderr, now)
	case "supervise":
		return cmdSupervise(rest, stdout, stderr, now)
	case "status":
		return cmdStatus(rest, stdout, stderr, now)
	case "stop":
		return cmdStop(rest, stdout, stderr)
	case "requeue":
		return cmdRequeue(rest, stdin, stdout, stderr, now)
	case "verdict":
		return cmdVerdict(rest, stdout, stderr)
	case "triage":
		return cmdTriage(rest, stdout, stderr, now)
	case "result":
		return cmdResult(rest, stdout, stderr)
	case "template":
		return cmdTemplate(rest, stdout, stderr)
	case "cost":
		return cmdCost(rest, stdout, stderr)
	case "note":
		return cmdNote(rest, stdout, stderr, now)
	case "finalize":
		return cmdFinalize(rest, stdout, stderr, now)
	case "reclaim":
		return cmdReclaim(rest, stdout, stderr)
	case "quickstart":
		return cmdQuickstart(rest, stdout, stderr)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", cmd))
}

// ------------------------------------------------------------------------------- flags

// flags is one verb's flag set with package flag's two mouths closed: its error text quotes
// the argument it could not parse, and its usage dump is discarded, so an argument beginning
// with a dash cannot author a line of stderr before any code here runs.
type flags struct {
	verb     string
	fs       *flag.FlagSet
	problems []string
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{verb: verb, fs: fs}
}

// parse runs the flag set. It reports nothing about required flags: those are checked by
// want and wantCount, which collect EVERY independent problem so that one run names them
// all (ONBOARDING point 2) rather than sending a first run back three times.
func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-swarm %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	return true
}

// want records a missing required flag with what it WANTS, never only what was wrong.
func (f *flags) want(value, name, wants string) {
	if strings.TrimSpace(value) == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
}

// wantCount is the same for a count whose floor is one: zero is refused rather than read as
// "unlimited" or as "none".
func (f *flags) wantCount(value int, name, wants string) {
	if value < 1 {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required and is at least 1, got %d; it wants %s; refusing to guess", name, value, wants))
	}
}

func (f *flags) add(problem string) { f.problems = append(f.problems, problem) }

// refused prints every problem this run found, one line each, and reports whether there
// were any. Three independent flags cost one run, not three.
func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-swarm %s: %s\n", f.verb, oneline.Escape(p))
	}
	return len(f.problems) > 0
}

// tokens reads --tokens, which is a number or the explicit word `unmetered` and has no
// default: a budget nothing observes is a promise the tool cannot keep, and a caller saying
// so out loud is the only way this tool will run without one.
func (f *flags) tokens(value string) (int, bool) {
	switch {
	case strings.TrimSpace(value) == "":
		f.add("--tokens is required; it wants a token budget for this job, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; refusing to guess")
		return 0, false
	case value == "unmetered":
		return 0, true
	}
	n, err := parseInt(value)
	switch {
	case err != nil:
		f.add(fmt.Sprintf("--tokens wants a number of tokens or the word `unmetered`, got %q", value))
	case n < 1:
		f.add(fmt.Sprintf("--tokens is a budget and is at least 1, got %d; `unmetered` is how a caller says there is no accounting", n))
	}
	return n, false
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err
}

func openPool(verb, dir string, stderr io.Writer) (*swarm.Pool, bool) {
	p, err := swarm.OpenPool(dir)
	if err != nil {
		// A refusal that offers a recovery offers one that WORKS: `quickstart` is the verb
		// that makes a pool, and the audit's first stumble was a missing pool that named no
		// remedy at all (2026-09-11).
		fmt.Fprintf(stderr, "nova-swarm %s: %s; the verb that makes one: nova-swarm quickstart --pool %s\n",
			verb, oneline.Err(err), oneline.Escape(dir))
		return nil, false
	}
	return p, true
}

// maxFlag is the ceiling every listing here carries.
func maxFlag(fs *flag.FlagSet) *int {
	return fs.Int("max", bounded.Default, "at most this many item lines, 0 for all")
}

// wantMax refuses a NEGATIVE ceiling. Zero already means all, so a negative number is a typo
// with two readings -- and the reading this tool took was "all", on the one flag whose job
// is to bound output, at the largest state (the new-user audit, F6, 2026-09-11).
func (f *flags) wantMax(value int) {
	if value < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all, so a negative ceiling is a typo with two readings and this tool refuses to pick one", value))
	}
}

// ------------------------------------------------------------------------------- verbs

func cmdAdd(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("add")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	useStdin := f.fs.Bool("stdin", false, "")
	files := f.fs.Int("files", 0, "")
	tokens := f.fs.String("tokens", "", "")
	label := f.fs.String("label", "", "")
	template := f.fs.String("template", "", "")
	deadline := f.fs.String("deadline", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if *task == "" && !*useStdin {
		f.add("--task is required; it wants a FILE holding the task text, or --stdin to read it from a stream: a task in an argument is a task in the process table")
	}
	f.wantCount(*files, "files", "the file budget for this job: a worker that may open no file is a worker asked for a plan")
	budget, unmetered := f.tokens(*tokens)
	if *deadline != "" {
		if _, err := time.ParseDuration(*deadline); err != nil {
			f.add(fmt.Sprintf("--deadline wants a duration such as 20m, got %q", *deadline))
		}
	}
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("add", *pool, stderr)
	if !ok {
		return 2
	}
	text, err := readTask(*task, *useStdin, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm add: %s\n", oneline.Err(err))
		return 2
	}
	if *template != "" {
		wrapped, err := swarm.WrapTemplate(*template, *files, text)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm add: %s\n", oneline.Err(err))
			return 2
		}
		text = wrapped
	}
	sc := swarm.Sidecar{
		ID: swarm.NewID(now, *label), Label: *label, Template: *template, Files: *files,
		Tokens: budget, Unmetered: unmetered, Deadline: *deadline, RC: -1,
	}
	if err := p.Add(text, sc); err != nil {
		fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	pending, _ := p.List(swarm.Pending)
	// BOTH budgets on the line: the audit found `tokens=` printed and `files=` not, so the
	// half a caller cannot re-derive from the line was the half missing from it.
	fmt.Fprintf(stdout, "ADD OK id=%s label=%s template=%s deadline=%s files=%d tokens=%s batch=- pending=%d\n",
		oneline.Field(sc.ID), oneline.Field(dash(*label)), oneline.Field(dash(*template)),
		oneline.Field(dash(*deadline)), sc.Files, oneline.Field(sc.BudgetWord()), len(pending))
	return 0
}

func cmdBatch(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("batch")
	pool := f.fs.String("pool", "", "")
	tasks := f.fs.String("tasks", "", "")
	files := f.fs.Int("files", 0, "")
	tokens := f.fs.String("tokens", "", "")
	label := f.fs.String("label", "", "")
	template := f.fs.String("template", "", "")
	deadline := f.fs.String("deadline", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*tasks, "tasks", "a directory holding one task file per job")
	f.wantCount(*files, "files", "the file budget every job in this batch carries")
	budget, unmetered := f.tokens(*tokens)
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("batch", *pool, stderr)
	if !ok {
		return 2
	}
	entries, err := os.ReadDir(*tasks)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm batch: --tasks wants a readable directory of task files: %s\n", oneline.Err(err))
		return 2
	}
	// A BATCH IS ALL OF ITS TASKS OR NONE: every file is read first, and one that cannot be
	// read queues nothing at all.
	type queued struct {
		name string
		text []byte
	}
	var all []queued
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(*tasks, e.Name()))
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm batch: %s could not be read, so nothing was queued: %s\n", oneline.Escape(e.Name()), oneline.Err(err))
			return 2
		}
		all = append(all, queued{e.Name(), raw})
	}
	if len(all) == 0 {
		fmt.Fprintf(stderr, "BATCH REFUSED: %s holds no regular file; a batch of no tasks is a typo\n", oneline.Field(*tasks))
		return 1
	}
	batchID := swarm.NewID(now, *label)
	for i, q := range all {
		text := q.text
		if *template != "" {
			wrapped, err := swarm.WrapTemplate(*template, *files, text)
			if err != nil {
				fmt.Fprintf(stderr, "nova-swarm batch: %s\n", oneline.Err(err))
				return 2
			}
			text = wrapped
		}
		sc := swarm.Sidecar{
			ID: swarm.NewID(now.Add(time.Duration(i)*time.Millisecond), *label), Label: *label,
			Template: *template, Files: *files, Tokens: budget, Unmetered: unmetered,
			Deadline: *deadline, Batch: batchID, RC: -1,
		}
		if err := p.Add(text, sc); err != nil {
			fmt.Fprintf(stderr, "BATCH REFUSED: %s\n", oneline.Err(err))
			return 2
		}
	}
	pending, _ := p.List(swarm.Pending)
	fmt.Fprintf(stdout, "BATCH OK id=%s tasks=%d pending=%d\n", oneline.Field(batchID), len(all), len(pending))
	return 0
}

func cmdRun(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("run")
	pool := f.fs.String("pool", "", "")
	workers := f.fs.Int("workers", 0, "")
	hours := f.fs.Float64("hours", 0, "")
	worker := f.fs.String("worker", "", "")
	max := maxFlag(f.fs)
	launchTimeout := f.fs.Int("launch-timeout", 10, "")
	usageInterval := f.fs.Int("usage-interval", 5, "")
	backoff := f.fs.Int("backoff", int(swarm.DefaultBackoff/time.Second), "")
	// THE WALL (docs/SPEC-SANDBOX.md). Every job runs inside nova-sandbox: --sandbox names
	// the binary when it is not on PATH under its own name, and --no-sandbox is rule 11's
	// ONE loud workaround, which a person types and no environment variable can produce.
	sandboxPath := f.fs.String("sandbox", "", "")
	noSandbox := f.fs.Bool("no-sandbox", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	if *noSandbox && *sandboxPath != "" {
		f.add("--no-sandbox and --sandbox together: one asks for no wall at all and the other names the wall to use; pass at most one")
	}
	f.wantMax(*max)
	if *backoff < 1 {
		f.add("--backoff wants a positive number of seconds to wait before retrying a provider's 429; zero or less is not a wait")
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.wantCount(*workers, "workers", "how many workers run at once, at most 64")
	f.want(*worker, "worker", "the worker description: which provider, which model, which env var, which key file")
	if *hours <= 0 {
		f.add("--hours is required and is more than 0; it wants this dispatcher's own deadline, after which it starts nothing new; refusing to guess")
	}
	if *workers > swarm.WorkerCap {
		f.add(fmt.Sprintf("--workers is capped at %d, got %d; this is a refusal rather than a clamp, because a caller who asked for more has a belief about throughput that a note in a log does not correct", swarm.WorkerCap, *workers))
	}
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("run", *pool, stderr)
	if !ok {
		return 2
	}
	w, problems := swarm.LoadWorker(*worker)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(stderr, "nova-swarm run: %s\n", oneline.Err(problem))
		}
		return 2
	}
	key, err := swarm.ReadKey(w.KeyFile, w.EnvVar)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm run: %s\n", oneline.Err(err))
		return 2
	}
	if mode, loose := swarm.KeyFileMode(w.KeyFile); loose {
		fmt.Fprintf(stdout, "RUN NOTE the key file is mode %04o and readable beyond its owner: chmod 600 %s\n", mode, oneline.Escape(w.KeyFile))
	}
	if _, err := exec.LookPath(w.Harness); err != nil {
		fmt.Fprintf(stderr, "nova-swarm run: the harness %s is not on PATH, so nothing could start; install it or name another in %s\n",
			oneline.Field(w.Harness), oneline.Field(*worker))
		return 2
	}
	// The wall is resolved ONCE, here, before the lock and before the first worker, so
	// that every job of this pass runs inside the same binary and a miss is one refusal
	// rather than one per job.
	wall := ""
	if !*noSandbox {
		found, err := swarm.LookSandbox(*sandboxPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm run: %s\n", oneline.Err(err))
			return 2
		}
		wall = found
	} else {
		fmt.Fprintf(stdout, "RUN NOTE --no-sandbox: this pass runs every job with NO OS containment, and says so once per job; the wall is docs/SPEC-SANDBOX.md and the remedy is to drop the flag\n")
	}
	// A budget nothing can observe is a promise this tool cannot keep, and the moment to
	// say so is BEFORE the first worker, which is the point at which the caller can still
	// fix it.
	if w.Usage == swarm.UsageNone {
		pending, _ := p.List(swarm.Pending)
		for _, sc := range pending {
			if !sc.Unmetered && sc.Tokens > 0 {
				fmt.Fprintf(stderr, "nova-swarm run: %s says `usage: none` while task %s carries --tokens %d; a budget nothing can observe is a promise this tool cannot keep. Re-queue it with `--tokens unmetered`, or name a usage source\n",
					oneline.Field(*worker), oneline.Field(sc.ID), sc.Tokens)
				return 2
			}
		}
	}
	release, err := p.TakeLock(swarm.RunLock, 2*time.Second)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm run: %s\n", oneline.Err(err))
		return 2
	}
	defer release()
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm run: this binary's own path could not be found, and the supervisor is this binary: %s\n", oneline.Err(err))
		return 2
	}
	return swarm.Run(swarm.RunInput{
		Pool: p, Worker: w, Key: key, Workers: *workers, Hours: *hours, Max: *max,
		LaunchTimeout: time.Duration(*launchTimeout) * time.Second,
		UsageInterval: time.Duration(*usageInterval) * time.Second,
		Backoff:       time.Duration(*backoff) * time.Second,
		Stdout:        stdout, Stderr: stderr, Now: func() time.Time { return time.Now().UTC() },
		Supervisor: self, WorkerFile: *worker, Sandbox: wall, NoSandbox: *noSandbox,
	})
}

func cmdSupervise(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("supervise")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	slot := f.fs.Int("slot", 0, "")
	nonce := f.fs.String("nonce", "", "")
	worker := f.fs.String("worker", "", "")
	usageInterval := f.fs.Int("usage-interval", 5, "")
	// The wall this job runs inside, handed down by the dispatcher that resolved it. A
	// supervisor is run's child and nobody's verb, so neither flag is one a person types.
	sandboxPath := f.fs.String("sandbox", "", "")
	noSandbox := f.fs.Bool("no-sandbox", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the pool this job belongs to")
	f.want(*task, "task", "the task id this supervisor owns")
	f.want(*nonce, "nonce", "the launch nonce the runner drew for this slot")
	f.want(*worker, "worker", "the worker description this job runs under")
	f.wantCount(*slot, "slot", "the slot reserved for this job")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("supervise", *pool, stderr)
	if !ok {
		return 2
	}
	// `supervise` is run's child and nobody's verb. A hand-typed one is refused.
	// If run.lock is not held, verify that our slot is actually reserved, orphaned, or launched
	// with our nonce before proceeding (which allows an orphaned child to complete identification or abort per rule 18).
	if release, err := p.TakeLock(swarm.RunLock, 0); err == nil {
		release()
		sf, err := p.ReadSlot(*slot)
		if err != nil || *nonce == "" || sf.Nonce != *nonce || (sf.State != swarm.SlotReserved && sf.State != swarm.SlotOrphaned && sf.State != swarm.SlotLaunched) {
			return refuse(stderr, " supervise", "no `nova-swarm run` holds this pool's lock, and supervise is run's child rather than a verb; it wants to be spawned by `nova-swarm run --pool <dir> --workers <n> --hours <h> --worker <file>`")
		}
	}
	// The wall this job runs inside is the dispatcher's, handed down: one of the two is
	// required, and neither is a thing a person types. It is checked HERE, after the door
	// above, so that a hand-typed `supervise` is told about the door rather than about a
	// flag it was never meant to pass.
	if *sandboxPath == "" && !*noSandbox {
		return refuse(stderr, " supervise", "--sandbox is required and names the nova-sandbox binary this job runs inside (docs/SPEC-SANDBOX.md); `nova-swarm run` resolves it once and passes it to every supervisor, and --no-sandbox is the one workaround it passes instead")
	}
	w, problems := swarm.LoadWorker(*worker)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(stderr, "nova-swarm supervise: %s\n", oneline.Err(problem))
		}
		return 2
	}
	key, err := swarm.ReadKey(w.KeyFile, w.EnvVar)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm supervise: %s\n", oneline.Err(err))
		return 2
	}
	sc, err := p.ReadSidecar(swarm.Running, *task)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm supervise: task %s is not running in %s\n", oneline.Field(*task), oneline.Field(*pool))
		return 2
	}
	return swarm.Supervise(swarm.SuperviseInput{
		Pool: p, Task: *task, Slot: *slot, Nonce: *nonce, Worker: w, Sidecar: sc, Key: key,
		Sandbox:       *sandboxPath,
		UsageInterval: time.Duration(*usageInterval) * time.Second,
		Stdout:        stdout, Stderr: stderr, Now: func() time.Time { return time.Now().UTC() },
	})
}

func cmdStatus(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("status")
	pool := f.fs.String("pool", "", "")
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	f.wantMax(*max)
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("status", *pool, stderr)
	if !ok {
		return 2
	}
	counts := map[string]int{}
	list := bounded.Capped(stdout, *max, "STATUS", "task", "nova-swarm status --pool "+*pool+" --max 0")
	for _, state := range []string{swarm.Running, swarm.Pending, swarm.Done, swarm.Failed} {
		tasks, err := p.List(state)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm status: %s could not be read: %s\n", oneline.Field(state), oneline.Err(err))
			return 2
		}
		counts[state] = len(tasks)
		for _, sc := range tasks {
			list.Line(fmt.Sprintf("STATUS TASK id=%s state=%s slot=%s for=%s tail=%s",
				oneline.Field(sc.ID), oneline.Field(state), oneline.Field(slotWord(sc)),
				oneline.Field(forWord(sc, now)), oneline.Escape(oneline.Cap(tail(p, sc), oneline.TailBytes))))
		}
	}
	list.More()
	numbers, bad, _ := p.SlotNumbers()
	quarantined := 0
	for _, n := range numbers {
		if d := p.Decide(n); d.Kind == swarm.DecideQuarantine {
			quarantined++
		}
	}
	quarantined += len(bad)
	fmt.Fprintf(stdout, "STATUS OK pending=%d running=%d done=%d failed=%d slots=%d/%d quarantined=%d\n",
		counts[swarm.Pending], counts[swarm.Running], counts[swarm.Done], counts[swarm.Failed],
		len(numbers), len(numbers), quarantined)
	return 0
}

// tail is the ONE line a status entry carries about what a worker is doing. The prototype
// tailed a log file, which means a log line's content reached a report unescaped and
// unbounded; here it is the report's own heading, one line, capped.
func tail(p *swarm.Pool, sc swarm.Sidecar) string {
	raw, _, err := p.ReportBytes(sc)
	if err != nil {
		return "-"
	}
	report := swarm.ParseReport(raw)
	if report.Heading != "" {
		return report.Heading
	}
	return report.Class
}

func slotWord(sc swarm.Sidecar) string {
	if sc.Slot == 0 {
		return "-"
	}
	return fmt.Sprint(sc.Slot)
}

func forWord(sc swarm.Sidecar, now time.Time) string {
	if sc.Started == "" {
		return "-"
	}
	started, err := time.Parse(time.RFC3339, sc.Started)
	if err != nil {
		return "-"
	}
	return now.Sub(started).Round(time.Second).String()
}

func cmdStop(args []string, stdout, stderr io.Writer) int {
	f := newFlags("stop")
	pool := f.fs.String("pool", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("stop", *pool, stderr)
	if !ok {
		return 2
	}
	if err := os.WriteFile(p.Path("stop"), []byte("stop\n"), 0o644); err != nil {
		fmt.Fprintf(stderr, "nova-swarm stop: the stop file could not be written: %s\n", oneline.Err(err))
		return 2
	}
	running, _ := p.List(swarm.Running)
	fmt.Fprintf(stdout, "STOP OK pool=%s running=%d\n", oneline.Field(p.Dir), len(running))
	return 0
}

func cmdRequeue(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("requeue")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	taskFile := f.fs.String("task-file", "", "")
	useStdin := f.fs.Bool("stdin", false, "")
	files := f.fs.Int("files", 0, "")
	tokens := f.fs.String("tokens", "", "")
	label := f.fs.String("label", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*task, "task", "the id of the finished task this one replaces")
	if *taskFile == "" && !*useStdin {
		f.add("--task-file is required; it wants a FILE holding the NEW task text, or --stdin: a requeue with the same text is a retry, and a retry of a task that failed for what it said will fail the same way")
	}
	f.wantCount(*files, "files", "the file budget for the new job: the remedy that worked in batch 3 was a SMALLER one")
	budget, unmetered := f.tokens(*tokens)
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("requeue", *pool, stderr)
	if !ok {
		return 2
	}
	old, found := p.Where(*task)
	if !found {
		fmt.Fprintf(stderr, "REQUEUE REFUSED: no task %s in %s\n", oneline.Field(*task), oneline.Field(p.Dir))
		return 1
	}
	// WORK IN FLIGHT IS NEVER REPLACED UNDER ITSELF. --task said it wanted a finished task
	// and accepted any state at all (the new-user audit, F7).
	if old == swarm.Running {
		fmt.Fprintf(stderr, "REQUEUE REFUSED: %s is running; a task in flight ends at its deadline or by nova-swarm stop --pool %s, and is replaced after that\n",
			oneline.Field(*task), oneline.Escape(p.Dir))
		return 1
	}
	oldSc, _ := p.ReadSidecar(old, *task)
	text, err := readTask(*taskFile, *useStdin, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm requeue: %s\n", oneline.Err(err))
		return 2
	}
	sc := swarm.Sidecar{
		ID: swarm.NewID(now, orElse(*label, oldSc.Label)), Label: orElse(*label, oldSc.Label),
		Template: oldSc.Template, Files: *files, Tokens: budget, Unmetered: unmetered,
		Deadline: oldSc.Deadline, Batch: oldSc.Batch, From: *task, RC: -1,
	}
	if err := p.Add(text, sc); err != nil {
		fmt.Fprintf(stderr, "REQUEUE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	// AND IT REPLACES. The old task moves to aborted/ carrying the id that replaced it, so
	// the refusal that names this verb as its remedy leaves a pool with ONE task in it and
	// not two -- the audit's `usage: none` refusal took pending from 2 to 4 and printed
	// itself again.
	replaced := true
	oldSc.ReplacedBy = sc.ID
	if err := p.WriteSidecar(old, oldSc); err != nil {
		replaced = false
	} else if err := p.Claim(*task, old, swarm.Aborted); err != nil {
		replaced = false
	}
	if !replaced {
		fmt.Fprintf(stderr, "REQUEUE REFUSED: %s was queued as %s but the old task could not be moved to %s; move it by hand before the next run\n",
			oneline.Field(*task), oneline.Field(sc.ID), oneline.Escape(p.Path(swarm.Aborted)))
		return 1
	}
	// THE GRAMMAR IS THE LINE (SPEC-SWARM.md:593): `REQUEUE OK id=<id> from=<old-id>
	// changed=<true>`. `was=` and `replaced=` were in no grammar line and no rule sentence,
	// and the refusal above is the only path where the replacement did not happen -- so
	// `replaced=` was a field that could only ever read true.
	fmt.Fprintf(stdout, "REQUEUE OK id=%s from=%s changed=true\n",
		oneline.Field(sc.ID), oneline.Field(*task))
	return 0
}

func cmdVerdict(args []string, stdout, stderr io.Writer) int {
	f := newFlags("verdict")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	who := f.fs.String("who", "", "")
	accurate := f.fs.Int("accurate", -1, "")
	wrong := f.fs.Int("wrong", -1, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*task, "task", "the id of the task these counts are about")
	f.want(*who, "who", "the name of the reader recording them: a verdict is a person's act")
	if *accurate < 0 {
		f.add("--accurate is required and is 0 or more; it wants how many of this task's findings you checked and found accurate")
	}
	if *wrong < 0 {
		f.add("--wrong is required and is 0 or more; it wants how many you checked and found wrong")
	}
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("verdict", *pool, stderr)
	if !ok {
		return 2
	}
	state, found := p.Where(*task)
	if !found {
		fmt.Fprintf(stderr, "VERDICT REFUSED: no task %s in %s\n", oneline.Field(*task), oneline.Field(p.Dir))
		return 1
	}
	sc, err := p.ReadSidecar(state, *task)
	if err != nil {
		fmt.Fprintf(stderr, "VERDICT REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	sc.Verdict = &swarm.Verdict{Who: *who, Accurate: *accurate, Wrong: *wrong}
	if err := p.WriteSidecar(state, sc); err != nil {
		fmt.Fprintf(stderr, "VERDICT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "VERDICT OK id=%s who=%s accurate=%d wrong=%d\n",
		oneline.Field(*task), oneline.Field(*who), *accurate, *wrong)
	return 0
}

func cmdTriage(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("triage")
	pool := f.fs.String("pool", "", "")
	batch := f.fs.String("batch", "", "")
	since := f.fs.String("since", "", "")
	all := f.fs.Bool("all", false, "")
	noState := f.fs.Bool("no-state", false, "")
	owed := f.fs.String("owed", "", "")
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	f.wantMax(*max)
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("triage", *pool, stderr)
	if !ok {
		return 2
	}
	// RULE 1's SECOND HALF (SPEC-SWARM.md:80): triage "counts a finding that matches an
	// owed item and is not marked `dup:` as `duplicate`". Without this flag the match was
	// dead code -- the field was read and nothing ever assigned it.
	var owedList []string
	if *owed != "" {
		items, err := swarm.ReadOwed(*owed)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm triage: --owed wants a readable file of the items this pull request already owes, one per line or as `- ` bullets: %s\n", oneline.Err(err))
			return 2
		}
		owedList = items
	}
	return swarm.Triage(swarm.TriageInput{
		Pool: p, Batch: *batch, Since: *since, All: *all, NoState: *noState, Max: *max,
		Owed:   owedList,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return now },
	})
}

func cmdResult(args []string, stdout, stderr io.Writer) int {
	f := newFlags("result")
	pool := f.fs.String("pool", "", "")
	id := f.fs.String("id", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*id, "id", "the job id whose report you want printed verbatim")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("result", *pool, stderr)
	if !ok {
		return 2
	}
	return swarm.ResultByID(p, *id, stdout, stderr)
}

func cmdTemplate(args []string, stdout, stderr io.Writer) int {
	f := newFlags("template")
	name := f.fs.String("name", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	// THE BANNER AND THE REFUSAL NAME THE SAME SET. The list was typed twice and the
	// tool answered to a fifth name (`worker`) that neither copy mentioned.
	f.want(*name, "name", "one of "+strings.Join(swarm.TemplateNames(), ", "))
	if f.refused(stderr) {
		return 2
	}
	body, err := swarm.Template(*name)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm template: %s\n", oneline.Err(err))
		return 2
	}
	// A template is printed VERBATIM because it is a document a person redirects into a
	// file, not an event line: escaping it would fold it into one unusable line. Every
	// byte of it is a constant in this binary.
	fmt.Fprint(stdout, body)
	return 0
}

func cmdCost(args []string, stdout, stderr io.Writer) int {
	f := newFlags("cost")
	pool := f.fs.String("pool", "", "")
	since := f.fs.String("since", "", "")
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	f.wantMax(*max)
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("cost", *pool, stderr)
	if !ok {
		return 2
	}
	return swarm.Cost(p, *since, *max, stdout, stderr)
}

func cmdNote(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("note")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	text := f.fs.String("text", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*task, "task", "the id of the RUNNING job to leave the note for")
	f.want(*text, "text", "the note itself, one line: it is data to the worker and never an instruction to this tool")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("note", *pool, stderr)
	if !ok {
		return 2
	}
	sc, err := p.ReadSidecar(swarm.Running, *task)
	if err != nil || sc.Job == "" {
		fmt.Fprintf(stderr, "NOTE REFUSED: %s is not running in %s; a note is for a job that can still read it\n",
			oneline.Field(*task), oneline.Field(p.Dir))
		return 1
	}
	n, err := swarm.AppendNote(sc.Job, *text, now)
	if err != nil {
		fmt.Fprintf(stderr, "NOTE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "NOTE OK id=%s notes=%d\n", oneline.Field(*task), n)
	return 0
}

func cmdFinalize(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("finalize")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	f.want(*task, "task", "the id of the ended job whose runner died before finalizing it")
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("finalize", *pool, stderr)
	if !ok {
		return 2
	}
	code, line := swarm.FinalizeByHand(p, *task, now)
	if code == 0 {
		fmt.Fprintln(stdout, line)
	} else {
		fmt.Fprintln(stderr, line)
	}
	return code
}

func cmdReclaim(args []string, stdout, stderr io.Writer) int {
	f := newFlags("reclaim")
	pool := f.fs.String("pool", "", "")
	task := f.fs.String("task", "", "")
	done := f.fs.Bool("done", false, "")
	failedOnly := f.fs.Bool("failed", false, "")
	all := f.fs.Bool("all", false, "")
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	f.wantMax(*max)
	f.want(*pool, "pool", "the directory that holds this pool's tasks")
	if *task == "" && !*done && !*failedOnly && !*all {
		f.add("--task is required; it wants the id of the job whose directory is to be removed, or --done, --failed or --all for every job in that state: this is the one thing this tool deletes")
	}
	if f.refused(stderr) {
		return 2
	}
	p, ok := openPool("reclaim", *pool, stderr)
	if !ok {
		return 2
	}
	// A PASS WHERE EVERYTHING FAILED needed one `reclaim --task <id>` per failure before
	// this: `--done` swept done/ and nothing else, so the failure path leaked disk (the
	// new-user audit, F9, 2026-09-11).
	ids := []string{*task}
	if *done || *failedOnly || *all {
		ids = nil
		var states []string
		switch {
		case *all:
			states = []string{swarm.Done, swarm.Failed}
		case *failedOnly:
			states = []string{swarm.Failed}
		default:
			states = []string{swarm.Done}
		}
		for _, state := range states {
			list, _ := p.List(state)
			for _, sc := range list {
				ids = append(ids, sc.ID)
			}
		}
	}
	worst := 0
	list := bounded.Capped(stdout, *max, "RECLAIM", "task", "nova-swarm reclaim --pool "+*pool+" --all --max 0")
	for _, id := range ids {
		sc, found := p.FindAnywhere(id)
		if !found {
			fmt.Fprintf(stderr, "RECLAIM REFUSED id=%s: no such task in %s\n", oneline.Field(id), oneline.Field(p.Dir))
			worst = 1
			continue
		}
		freed, usagePath, err := p.Reclaim(id, sc.Job)
		if err != nil {
			fmt.Fprintf(stderr, "RECLAIM REFUSED id=%s: %s; nova-swarm finalize --pool %s --task %s\n",
				oneline.Field(id), oneline.Err(err), oneline.Escape(p.Dir), oneline.Field(id))
			worst = 1
			continue
		}
		list.Line(fmt.Sprintf("RECLAIM OK id=%s freed=%d usage=%s", oneline.Field(id), freed, oneline.Field(usagePath)))
	}
	list.More()
	return worst
}

// cmdQuickstart is ONBOARDING point 4: one line needing nothing the caller has to invent.
// It makes the pool's structure under a directory the caller named and says what the three
// next commands are -- and it writes no task, because a verb that queued work nobody asked
// for would be a first run with a side effect.
func cmdQuickstart(args []string, stdout, stderr io.Writer) int {
	f := newFlags("quickstart")
	pool := f.fs.String("pool", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the directory this pool lives in; it is made if it is missing")
	if f.refused(stderr) {
		return 2
	}
	if err := os.MkdirAll(*pool, 0o755); err != nil {
		fmt.Fprintf(stderr, "nova-swarm quickstart: --pool wants a directory that can be made: %s\n", oneline.Err(err))
		return 2
	}
	p, ok := openPool("quickstart", *pool, stderr)
	if !ok {
		return 2
	}
	pending, _ := p.List(swarm.Pending)
	fmt.Fprintf(stdout, "QUICKSTART OK pool=%s pending=%d next=add,run,triage\n", oneline.Field(p.Dir), len(pending))
	fmt.Fprintf(stdout, "QUICKSTART NOTE a task is a file: nova-swarm add --pool %s --task <file> --files <n> --tokens <n>\n", oneline.Escape(p.Dir))
	fmt.Fprintf(stdout, "QUICKSTART NOTE a worker description says whose model runs: nova-swarm run --pool %s --workers <n> --hours <h> --worker <file>\n", oneline.Escape(p.Dir))
	fmt.Fprintf(stdout, "QUICKSTART NOTE the conditions are worth more than the model: nova-swarm template --name read-pr\n")
	return 0
}

// ------------------------------------------------------------------------------- helpers

func readTask(path string, useStdin bool, stdin io.Reader) ([]byte, error) {
	if useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("--stdin wants the task text on standard input: %w", err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, fmt.Errorf("--stdin read an empty task; it wants the task text")
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--task wants a readable file holding the task text: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("--task %s is empty; it wants the task text", path)
	}
	return raw, nil
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func orElse(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
