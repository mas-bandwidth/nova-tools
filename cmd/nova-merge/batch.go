package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE LANDING GATE, AS A VERB.
//
// Glenn, 2026-09-18: landing is by integration batches only, and "we need to stop using
// shell scripts for things". The gate ran that morning on hulk as a shell script --
// clone, merge each pull request head onto the current base IN ORDER, drop the ones that
// will not merge, then build, vet, test and run the lisp suite over the result -- and it
// found five poison pull requests in three minutes each time it ran. This is that script
// with the shell taken out, and with the three things a shell loop cannot do: a member
// that conflicts is dropped BY NAME rather than silently skipped, every step says what it
// is doing with the elapsed time while it runs, and the verdict is one line a caller can
// parse.
//
// IT PUSHES NOTHING AND IT OPENS NOTHING. The batch ends as a branch in a clone under
// --root and a line naming that branch's head; pushing it and opening the pull request is
// the caller's, who is the one who knows whether the batch is the one they wanted. There
// is no path to a push in this file, and the tests assert the remote saw none.
//
// It reuses `simulate`'s machinery rather than repeating it: hasConflicts, firstLine and
// runCheck are simulate's, and the difference between the two verbs is what they are for
// -- simulate PREDICTS which queued entry will turn the base red, and batch BUILDS the
// integration branch that lands.

// batchBase is the branch integration batches land on in this repository (Glenn,
// 2026-09-16: all merges into dev, main is fast-forwarded only when dev is green). It is
// the one default here, and --base overrides it.
const batchBase = "dev"

// batchTimeout is the per-step deadline. It is a duration rather than a count of seconds
// because the steps it bounds are a whole test suite rather than one git call, and 30
// minutes is longer than the gate has ever taken on any bench in the fleet.
const batchTimeout = "30m"

// batchStep is one step of the gate: the word its progress line carries, the command it
// runs in the merged tree, the program that must be on PATH for it to mean anything, the
// file of the checkout it runs, and whether its output is a `go test -json` stream.
type batchStep struct {
	name    string
	command string
	needs   string
	file    string
	stream  bool
	// env is what this step adds to the environment every step runs in, last value
	// wins. It is how the windows leg is spelled without a second runner.
	env []string
}

// batchGate is the suite, in order. A step whose program or file is missing is SKIPPED
// OUT LOUD: a gate that quietly ran three of its four steps and printed OK is a gate that
// says green about a thing it did not check.
var batchGate = []batchStep{
	{name: "build", command: "go build ./...", needs: "go"},
	{name: "vet", command: "go vet ./...", needs: "go"},
	// EDGE 25, batch 7: THE GATE RUNS ON ONE OPERATING SYSTEM AND CI RUNS ON THREE.
	// Three members went green under the gate on linux and red on CI's windows legs --
	// cmd/nova-sandbox's path fixtures and internal/dogfood's exec-bit discovery -- and
	// the batch pull request went red after the gate had said OK. A cross vet is cheap,
	// needs no second machine, and catches the whole BUILD-level half of that class: a
	// file that does not compile for windows, a syscall that is not there, a constant
	// that is unix-only. It does not catch a windows-only TEST failure, which is what
	// the forge's own windows leg is for; the gate says what it checked and no more.
	{name: crossVetStep, command: "go vet ./...", needs: "go", env: []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"}},
	{name: "test", command: strings.Join(ciTestArgs(), " "), needs: "go", stream: true},
	{name: "lisp", command: "sh tools/ci/lisp-test.sh", needs: "sbcl", file: "tools/ci/lisp-test.sh"},
}

// crossVetStep is the cross vet's name, and it is a constant because it is THE ONE STEP A
// UNIT TEST MUST NOT RUN.
//
// Its cost is not its own compile: `GOOS=windows go vet ./...` has to build the WINDOWS
// STANDARD LIBRARY into the build cache before it can type-check anything, which is ~5 s
// on an idle 64-core bench with a cold cache and far more on a shared darwin runner --
// eight of them run on one of those machines. Paid inside `go test`, that is wall clock
// taken from the package's own -timeout, and the package's serial tests are what the
// parallel ones are waiting behind: on 2026-09-18 it turned the merge group's
// `test-hosted-merge (darwin, 1)` leg into `panic: test timed out after 1m40s` with ten
// parallel tests reported at 14 s each -- not one of them slow, all of them starved,
// every one blocked in Cmd.Wait on a git child that could not get the machine.
//
// So the step is in the product's gate, where it is paid once per bench and cached, and
// the tests run Deps.BatchGate instead, which is this list without it.
// TestTheGateCrossVetsForWindows pins the step in the real list, and pins that the tests'
// list differs from it by this one name and no other.
const crossVetStep = "vet-windows"

// sdkDir is where this fleet's hand-installed toolchains live, under the home directory:
// `sdk/go1.26.5/bin/go`, `sdk/sbcl-2.5.8-x86-64-linux/bin/sbcl`. A bench that HAS the
// program and has not put it on PATH is a bench with the program, and a gate that says
// "sbcl is not on this machine" about a machine holding sbcl is a gate that skipped a
// step it could have run (edge 2).
//
// Rule 13's line is that every path this tool WRITES comes from a flag a person gave it.
// This is a path it LOOKS IN, it is one directory, and it is written here rather than
// guessed from anything the shell happened to carry.
const sdkDir = "sdk"

// ciTestArgs IS THE ONE LIST: the test command CI runs, mirrored here so that the gate
// tests the way CI tests.
//
// WHERE THAT COMMAND LIVES MOVED IN integration-4. It used to be written out inline in
// the `test` step of the `test` job in .github/workflows/ci.yml; that step now reads
//
//	run: make test PKGS="${{ matrix.entry.packages }}"
//
// and the command itself is the Makefile's `test` target, which is
//
//	GOFLAGS=-json $(GO) test -count=1 $(PKGS) | tee $RUNNER_TEMP/test.json
//	$(GO) run ./cmd/nova-ci slowtests --budget "$budget" < $RUNNER_TEMP/test.json
//
// and every flag on it is on the gate for a reason:
//
//	-json        CI reads that stream with cmd/nova-ci slowtests, so every leg runs its
//	             tests under -json -- which turns the verbose stream on in every test
//	             binary and changes what a tool under test sees. integration-4 went green
//	             on hulk under a plain `go test ./...` and three CI legs then failed. CI
//	             delivers it as GOFLAGS=-json on the OUTER command; the gate writes it as
//	             an argv flag, which is the same thing for that command and survives the
//	             goenv.Clean environment every step runs in -- Clean strips GOFLAGS on
//	             purpose, so that an INNER go command a test spawns cannot inherit it.
//	-count=1     no cached result may stand in for a run; a gate reading a cache from
//	             before the merge is a gate reading the wrong tree.
//
// THERE IS NO -timeout HERE ANY MORE. The old inline step carried `-timeout 5m` and the
// gate carried it too; the Makefile's `test` target does not, so neither does the gate --
// the per-package deadline is go's own default and this verb's --timeout still bounds the
// whole step. A gate that kept a 5 m package deadline CI does not set is a gate that can
// go red on a tree CI passes, which is the divergence this list exists to prevent.
//
// ./... stands where CI writes ${{ matrix.entry.packages }}: CI splits the tree across a
// matrix and the union of those legs is the tree, which one gate run covers in one
// command.
//
// WHAT IS DELIBERATELY NOT MIRRORED is the fair-share step's GOMAXPROCS. That is the
// MACHINE's fact -- its cores divided by NOVA_RUNNERS_PER_MACHINE, which the runner
// service exports -- and row 9 of #828 is what writing such a divisor into a file costs:
// it stayed 4 the day the fleet went to 8. This package reads no environment variable
// (rule 13), so the share is --gomaxprocs, passed by the caller on a bench that is also
// running CI.
//
// TestTheGateTestsTheWayCIDoes reads ci.yml, the Makefile and this list together, so the
// day any one of them moves is the day it goes red.
func ciTestArgs() []string {
	return []string{"go", "test", "-json", "-count=1", "./..."}
}

// batchTempVars are the variables a child reads to find its temp directory, and this ONE
// LINE is the only place in this binary that names any of them. It is a SET and never a
// read: the gate hands every check a temp directory inside the batch's own working
// directory, which is rule 13 carried into the subprocesses. A `go test` or an sbcl suite
// that keys off the ambient one writes into /tmp instead, and two gates on one host
// wrecked each other's state that way -- "held by another process", "destination exists"
// -- on 2026-09-18 (tools/ci/lisp-test.sh).
var batchTempVars = []string{"TMPDIR", "GOTMPDIR", "LISP_TEST_TMPROOT", "TMP", "TEMP"}

// privateTempEnv is env with every temp-directory variable pointed at tmp, each named
// exactly once, so a child cannot inherit two answers and pick the other one.
func privateTempEnv(env []string, tmp string) []string {
	drop := map[string]bool{}
	for _, name := range batchTempVars {
		drop[name] = true
	}
	out := make([]string, 0, len(env)+len(batchTempVars))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if drop[key] {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range batchTempVars {
		out = append(out, name+"="+tmp)
	}
	return out
}

// cmdBatch runs one integration batch and says whether it may land.
func cmdBatch(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("batch")
	name := f.fs.String("name", "", "")
	prRaw := f.fs.String("pr", "", "")
	base := f.fs.String("base", batchBase, "")
	root := f.fs.String("root", "", "")
	repo := f.fs.String("repo", "", "")
	reference := f.fs.String("reference", "", "")
	timeoutRaw := f.fs.String("timeout", batchTimeout, "")
	gomaxprocs := f.fs.Int("gomaxprocs", 0, "")
	// --require-checks is ON, and --no-require-checks turns it off out loud (edge 25).
	// A member whose own head has never gone green is a member the gate is being asked
	// to judge for the first time IN COMBINATION, which is the one thing a batch cannot
	// do: it would report the batch red for a fault that is one member's alone.
	noRequireChecks := f.fs.Bool("no-require-checks", false, "")
	// --receipt-file carries the BATCH OK lines of batches ALREADY BUILT, so a member that
	// is itself a gated tree is admitted on the gate's own evidence rather than on a
	// `ci-ok` the forge has not finished running. It is the same receipt `nova-merge land`
	// reads and the same parser (internal/merge.ParseBatchReceipt): one receipt, one
	// meaning, wherever it is presented.
	receiptFile := f.fs.String("receipt-file", "", "")
	// --require-lisp is for the caller who needs the lisp suite RUN. Without it a bench
	// with no sbcl skips that step and says so on the verdict line (edge 2); with it, a
	// bench with no sbcl is a bench that cannot judge this batch, and the gate says FAIL
	// rather than a green nobody may trust.
	requireLisp := f.fs.Bool("require-lisp", false, "")
	reviewersFile := f.fs.String("reviewers", "", "")
	noRequireHolds := f.fs.Bool("no-require-holds", false, "")
	reason := f.fs.String("reason", "", "")
	untypedComments := f.fs.String("untyped-comments", "", "")
	lane := f.fs.String("lane", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("name", *name, "the batch's own name: its directory under --root, and the branch rowan/<name> it builds")
	f.require("pr", *prRaw, "the pull request numbers to land, in the order they land, like 1301,1302,1307")
	f.require("root", *root, "the directory this batch clones and builds under, which it rebuilds on every run")
	f.require("repo", *repo, "the <owner>/<name> whose pull request heads are merged, like mas-bandwidth/nova-tools")
	// --reviewers XOR --no-require-holds: exactly one required (SPEC-DECIDE reading 3, demanded test 28)
	if (*reviewersFile == "") == (*noRequireHolds == false) {
		f.problem("exactly one of --reviewers <file> or --no-require-holds --reason <text> is required; exit 2 with neither or both")
	}
	if *noRequireHolds && strings.TrimSpace(*reason) == "" {
		f.problem("--no-require-holds requires --reason <text>")
	}
	if *untypedComments == "ignore" && strings.TrimSpace(*reason) == "" {
		f.problem("--untyped-comments=ignore requires --reason <text>")
	}
	if *untypedComments != "" && *untypedComments != "ignore" {
		f.problem(fmt.Sprintf("--untyped-comments must be ignore, got %q", *untypedComments))
	}
	// --name is one path element and half a ref name, so it is held to both: a name that
	// could climb out of --root is a name that could remove a directory nobody named, and
	// a name git could read as an option is lesson 48.
	if s := strings.TrimSpace(*name); s != "" && !safepath.NameOK(s) {
		f.problem(fmt.Sprintf("--name is one path element of letters, digits, dot, dash and underscore, and never begins with a dash, got %q", *name))
	} else if s != "" {
		if err := merge.ValidRefName("rowan/" + s); err != nil {
			f.problem(fmt.Sprintf("--name: %s", oneline.Escape(err.Error())))
		}
	}
	if s := strings.TrimSpace(*base); s != "" {
		if err := merge.ValidRefName(s); err != nil {
			f.problem(fmt.Sprintf("--base: %s", oneline.Escape(err.Error())))
		}
	}
	prs, perr := parsePRList(*prRaw)
	if perr != nil {
		f.problem(oneline.Escape(perr.Error()))
	}
	timeout, terr := time.ParseDuration(*timeoutRaw)
	if terr != nil || timeout <= 0 {
		f.problem(fmt.Sprintf("--timeout is a duration per step like 30m, got %q", *timeoutRaw))
	}
	// --gomaxprocs is CI's fair-share number, which is the machine's fact rather than this
	// file's (see ciTestArgs). Zero is "take the machine", which is right on a bench doing
	// nothing else and wrong on one that is also running CI.
	if *gomaxprocs < 0 {
		f.problem(fmt.Sprintf("--gomaxprocs is the share of the machine this batch takes, the way CI divides its cores by the runners on it; 0 is all of them, and a negative one is a typo, got %d", *gomaxprocs))
	}
	if !f.done(stderr) {
		return 2
	}
	return runBatch(batchRun{
		steps:           deps.BatchGate,
		name:            *name,
		base:            *base,
		root:            *root,
		repo:            *repo,
		reference:       *reference,
		prs:             prs,
		timeout:         timeout,
		gomaxprocs:      *gomaxprocs,
		requireLisp:     *requireLisp,
		requireCheck:    !*noRequireChecks,
		receiptFile:     strings.TrimSpace(*receiptFile),
		reviewersFile:   strings.TrimSpace(*reviewersFile),
		noRequireHolds:  *noRequireHolds,
		reason:          strings.TrimSpace(*reason),
		untypedComments: strings.TrimSpace(*untypedComments),
		lane:            strings.TrimSpace(*lane),
	}, stdout, stderr, deps)
}

// batchRun is one batch's whole invocation, checked, so the run below reads as the steps
// it performs rather than as a second pass over the flags.
type batchRun struct {
	// steps is the suite this run performs. Nil is batchGate, which is what every
	// invocation of the binary uses; a caller injects a shorter one only through
	// Deps.BatchGate, and only the tests do.
	steps           []batchStep
	name            string
	base            string
	root            string
	repo            string
	reference       string
	prs             []int
	timeout         time.Duration
	gomaxprocs      int
	requireLisp     bool
	requireCheck    bool
	receiptFile     string
	reviewersFile   string
	noRequireHolds  bool
	reason          string
	untypedComments string
	lane            string
	holdsCount      int
	dispositions    string
	reviewersSHA    string
}

func runBatch(in batchRun, stdout, stderr io.Writer, deps Deps) int {
	start := time.Now()
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return batchRefused(stderr, err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return batchRefused(stderr, err)
	}
	// THE WORKING DIRECTORY IS REBUILT EVERY RUN, so a batch never merges on top of a
	// tree an earlier one left half-merged. It is a path this tool COMPUTED, so its
	// removal is safepath's and nobody else's: Glenn, 2026-09-17, "it is just one mistake
	// away from deleting the whole disk".
	work := filepath.Join(rootAbs, in.name)
	if err := safepath.RemoveUnder(rootAbs, work); err != nil {
		return batchRefused(stderr, err)
	}
	tmp := filepath.Join(work, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return batchRefused(stderr, err)
	}
	clone := filepath.Join(work, "repo")
	cloneArgs := []string{"clone", "--quiet"}
	if strings.TrimSpace(in.reference) != "" {
		// A mirror on this bench makes the clone local rather than a download. It is an
		// optimisation and never a requirement: without it the clone is an ordinary one.
		cloneArgs = append(cloneArgs, "--reference", in.reference)
	}
	// `--` before the URL, so a URL beginning with a dash is a URL and not an option.
	cloneArgs = append(cloneArgs, "--", deps.RepoURL(in.repo), clone)
	if _, err := merge.NewGit(work, in.timeout, deps.Runner).Run(cloneArgs...); err != nil {
		return batchRefused(stderr, err)
	}
	g := merge.NewGit(clone, in.timeout, deps.Runner)
	if _, err := g.Run("fetch", "--quiet", "origin", in.base); err != nil {
		return batchRefused(stderr, fmt.Errorf("could not fetch origin/%s: %w", in.base, err))
	}
	branch := "rowan/" + in.name
	if _, err := g.Run("checkout", "--quiet", "-B", branch, "FETCH_HEAD"); err != nil {
		return batchRefused(stderr, fmt.Errorf("could not start %s at origin/%s: %w", branch, in.base, err))
	}
	baseSHA, err := g.Out("rev-parse", "HEAD")
	if err != nil {
		return batchRefused(stderr, err)
	}
	fmt.Fprintf(stderr, "BATCH START name=%s base=%s prs=%d t=%.1fs\n",
		oneline.Field(in.name), oneline.Field(baseSHA), len(in.prs), since(start))

	// EDGE 1: THE TOOLCHAIN IS CHECKED BEFORE THE FIRST MERGE, NOT DISCOVERED IN A STEP.
	// With go1.22 on PATH and a go.mod asking for 1.26 the whole gate ran, the build step
	// went red, and the failure a caller read was `step=build reason="go: downloading
	// go1.26 (linux/amd64)"` -- a NOTICE, not an error, naming no remedy, after minutes
	// of merging. The requirement is a fact of the tree, so this is the earliest point it
	// can be known at all: the base is checked out and nothing has been merged yet.
	if code := checkToolchain(clone, stderr); code != 0 {
		return code
	}

	prs, prechecked, code := admissible(&in, stdout, stderr, deps, start)
	if code != 0 {
		return code
	}
	members, dropped, code := mergeMembers(g, in, prs, stderr, start)
	if code != 0 {
		return code
	}
	dropped = append(prechecked, dropped...)
	headSHA, err := g.Out("rev-parse", "HEAD")
	if err != nil {
		return batchRefused(stderr, err)
	}

	env := ciTestEnv(tmp, in.gomaxprocs)
	// EDGE 2: THE SKIPPED STEPS ARE ON THE VERDICT LINE. `BATCH SKIP lisp reason="sbcl is
	// not on this machine"` went to stderr and `BATCH OK` said nothing about it, so the
	// one line a caller parses claimed a green gate over a suite that ran three of its
	// four steps. Every skip is named on the verdict line, green or red, and
	// --require-lisp turns the skip into a failure for a caller who needs that step run.
	var skipped []string
	type ready struct {
		step batchStep
		bin  string
	}
	var plan []ready
	gate := in.steps
	if gate == nil {
		gate = batchGate
	}
	for _, step := range gate {
		why, bin := stepUnavailable(step, clone)
		if why == "" {
			plan = append(plan, ready{step: step, bin: bin})
			continue
		}
		if step.name == "lisp" && in.requireLisp {
			fmt.Fprintf(stdout, "BATCH FAIL %s step=%s packages=none tests=none reason=%q\n",
				batchLine(in, baseSHA, headSHA, members, dropped, append(skipped, step.name)),
				oneline.Field(step.name), oneline.Cap(why+"; --require-lisp asked for this step to be RUN, not skipped", oneline.TailBytes))
			return 1
		}
		skipped = append(skipped, step.name)
		fmt.Fprintf(stderr, "BATCH SKIP %s reason=%q t=%.1fs\n", oneline.Field(step.name), why, since(start))
	}
	line := batchLine(in, baseSHA, headSHA, members, dropped, skipped)
	for _, r := range plan {
		step := r.step
		fmt.Fprintf(stderr, "BATCH STEP %s command=%q t=%.1fs\n", oneline.Field(step.name), step.command, since(start))
		out, err := runCheck(clone, step.command, in.timeout, append(withBin(env, r.bin), step.env...))
		if err == nil {
			continue
		}
		pkgs, tests, reason := stepFailure(step, out, err)
		fmt.Fprintf(stdout, "BATCH FAIL %s step=%s packages=%s tests=%s reason=%q\n",
			line, oneline.Field(step.name), oneline.Field(numberOrNone(pkgs)), oneline.Field(numberOrNone(tests)),
			oneline.Cap(reason, oneline.TailBytes))
		return 1
	}
	fmt.Fprintf(stdout, "BATCH OK %s\n", line)
	return 0
}

// batchLine is the fields every verdict line carries, green or red.
func batchLine(in batchRun, baseSHA, headSHA string, members, dropped []int, skipped []string) string {
	line := fmt.Sprintf("name=%s base=%s head=%s members=%s dropped=%s skipped=%s checks=%s",
		oneline.Field(in.name), oneline.Field(baseSHA), oneline.Field(headSHA),
		oneline.Field(numberList(members)), oneline.Field(numberList(dropped)),
		oneline.Field(numberOrNone(skipped)), oneline.Field(checksWord(in)))

	if in.noRequireHolds {
		line += fmt.Sprintf(" holds=waived reason=%q", in.reason)
	} else {
		line += fmt.Sprintf(" holds=%d dispositions=%s reviewers=%s",
			in.holdsCount, oneline.Field(in.dispositions), oneline.Field(in.reviewersSHA))
	}
	if in.untypedComments == "ignore" {
		line += fmt.Sprintf(" untyped=ignored reason=%q", in.reason)
	}
	return line
}

// checksWord is what the verdict line says about edge 25's admission: `required` when
// every member's own head had to be green before it was merged, `waived` when the caller
// passed --no-require-checks and took that on themselves.
func checksWord(in batchRun) string {
	if in.requireCheck {
		return "required"
	}
	return "waived"
}

// batchRequiredCheck is the check a member's own head must have gone green on before the
// gate will merge it. It is CI's one rollup job, the same name the merge condition reads.
const batchRequiredCheck = "ci-ok"

func getReviewersSHA(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)

	checkCmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if out, err := checkCmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return "", fmt.Errorf("reviewer file %s is outside a git repository", path)
	}

	statusCmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--", abs)
	if out, err := statusCmd.CombinedOutput(); err != nil || len(strings.TrimSpace(string(out))) > 0 {
		return "", fmt.Errorf("reviewer file %s has uncommitted changes or is untracked", path)
	}

	logCmd := exec.Command("git", "-C", dir, "log", "-1", "--format=%H", "--", abs)
	out, err := logCmd.CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(out))) < 12 {
		return "", fmt.Errorf("reviewer file %s has no git commit", path)
	}
	return strings.TrimSpace(string(out))[:12], nil
}

// admissible is edge 25's gate in front of the gate: every member whose OWN head has no
// green ci-ok is dropped BEFORE the merge, by name and with the state it was in.
// In addition (#1572 / SPEC-DECIDE reading 3), it collects every hold on the member
// and drops one carrying an unreleased HOLD or a pending comment.
func admissible(in *batchRun, stdout, stderr io.Writer, deps Deps, start time.Time) (keep, dropped []int, code int) {
	var rs *merge.ReviewerSet
	if !in.noRequireHolds {
		var err error
		rs, err = merge.LoadReviewers(in.reviewersFile)
		if err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf("reviewer file %s could not be read: %w", in.reviewersFile, err))
		}
		in.reviewersSHA, err = getReviewersSHA(in.reviewersFile)
		if err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf("reviewer file %s could not be read: %w", in.reviewersFile, err))
		}
		in.dispositions = deps.Now().UTC().Format(time.RFC3339)
	}

	host := deps.NewHost(in.repo, in.timeout)
	if stampHost, ok := host.(interface{ DispositionsStamp() string }); ok && stampHost.DispositionsStamp() != "" {
		in.dispositions = stampHost.DispositionsStamp()
	}

	// SPEC-DECIDE reading 3: hold check runs for every PR in in.prs
	var survivors []int
	for _, n := range in.prs {
		pr, err := host.PR(n)
		if err != nil {
			if !in.noRequireHolds {
				return nil, nil, batchRefused(stderr, fmt.Errorf(
					"pull request %d could not be read, and the hold read is on, so this gate cannot tell whether a reader has held it: %w; pass --no-require-holds to merge it anyway and own that", n, err))
			}
		}

		var vs []merge.Verdict
		if in.lane != "" {
			laneVs, _ := merge.LoadLaneVerdicts(in.lane, n)
			vs = append(vs, laneVs...)
		}

		opts := merge.VerdictOpts{
			Author:          pr.Author,
			CurrentHead:     pr.HeadOID,
			Reviewers:       rs,
			UntypedComments: in.untypedComments,
		}
		forgeVs, err := host.Verdicts(n, opts)
		if err != nil {
			if !in.noRequireHolds {
				return nil, nil, batchRefused(stderr, fmt.Errorf(
					"pull request %d's comments and reviews could not be read, and the hold read is on: %w; pass --no-require-holds to merge it anyway and own that", n, err))
			}
		} else {
			if in.noRequireHolds {
				for _, v := range forgeVs {
					if v.Source == "record" {
						vs = append(vs, v)
					}
				}
			} else {
				vs = append(vs, forgeVs...)
			}
		}

		holds := merge.UnliftedHolds(vs, pr.HeadOID, pr.Author, rs)
		if len(holds) > 0 {
			dropped = append(dropped, n)
			in.holdsCount++
			h := holds[0]
			if h.Source == "comment-pending" {
				fmt.Fprintf(stderr, "BATCH DROP #%d reason=\"head %s has a pending comment\" who=unknown hold=%s source=comment-pending at=%s\n",
					n, oneline.Field(merge.Short(pr.HeadOID)), oneline.Field(h.ID), oneline.Field(h.At))
			} else {
				carried := "no"
				if h.Carried {
					carried = "yes"
				}
				conf := h.Conf
				if conf == "" {
					conf = "-"
				}
				fmt.Fprintf(stderr, "BATCH DROP #%d reason=\"head %s carries an unreleased HOLD\" who=%s hold=%s source=%s held_at=%s carried=%s at=%s conf=%s\n",
					n, oneline.Field(merge.Short(pr.HeadOID)), oneline.Field(h.Who), oneline.Field(h.ID), oneline.Field(h.Source), oneline.Field(merge.Short(h.Head)), oneline.Field(carried), oneline.Field(h.At), oneline.Field(conf))
			}
			continue
		}
		survivors = append(survivors, n)
	}

	if !in.requireCheck {
		fmt.Fprintf(stderr, "BATCH NOTE checks=waived reason=%q t=%.1fs\n",
			"--no-require-checks was given: a member is merged whatever its own head last did, and a red batch may be one member's own fault",
			since(start))
		return survivors, dropped, 0
	}
	receipts, err := batchReceipts(in.receiptFile)
	if err != nil {
		return nil, nil, batchRefused(stderr, err)
	}
	for _, n := range survivors {
		pr, err := host.PR(n)
		if err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf(
				"pull request %d could not be read, and --require-checks is on, so this gate cannot tell whether its head has been green on its own: %w; pass --no-require-checks to merge it anyway and own that", n, err))
		}
		// A MEMBER THAT IS ITSELF A GATED TREE NEEDS NO ci-ok. A batch's own branch --
		// rowan/integration-*, the shape this verb builds and nothing else does -- and a
		// head named by a BATCH OK receipt the caller presented are both evidence the
		// gate produced; requiring the forge's rollup on top of them would refuse a batch
		// pull request whose CI is still running, which is every batch pull request in
		// the minutes after it is opened. It is the same receipt `nova-merge land` takes
		// and the same parser reads it (#1347).
		if merge.IsBatchBranch(pr.HeadRef) {
			keep = append(keep, n)
			fmt.Fprintf(stderr, "BATCH NOTE #%d checks=batch-branch reason=%q t=%.1fs\n", n,
				"its head branch is a batch's own, which is the gate's own evidence", since(start))
			continue
		}
		if receipts[strings.ToLower(strings.TrimSpace(pr.HeadOID))] {
			keep = append(keep, n)
			fmt.Fprintf(stderr, "BATCH NOTE #%d checks=receipt reason=%q t=%.1fs\n", n,
				"a BATCH OK receipt names this very head", since(start))
			continue
		}
		checks, err := host.Checks(pr.HeadOID)
		if err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf(
				"pull request %d's checks could not be read, and --require-checks is on: %w; pass --no-require-checks to merge it anyway and own that", n, err))
		}
		state := checkState(checks.ForSHA(pr.HeadOID), batchRequiredCheck)
		if state == "green" {
			keep = append(keep, n)
			continue
		}
		dropped = append(dropped, n)
		fmt.Fprintf(stderr, "BATCH DROP #%d reason=%q t=%.1fs\n", n,
			fmt.Sprintf("head %s has no green %s (state=%s)", oneline.Field(pr.HeadOID), batchRequiredCheck, oneline.Field(state)), since(start))
	}
	return keep, dropped, 0
}

// batchReceipts reads --receipt-file: every BATCH OK line in it, keyed by the head it
// names. A file with no readable receipt at all is a refusal rather than an empty set --
// a caller who presented evidence and had it silently ignored would read a `ci-ok`
// refusal and have no idea why.
func batchReceipts(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--receipt-file could not be read: %w", err)
	}
	heads := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		rec, err := merge.ParseBatchReceipt(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		heads[strings.ToLower(rec.Head)] = true
	}
	if len(heads) == 0 {
		return nil, fmt.Errorf("--receipt-file %s holds no BATCH OK line naming a head; a receipt is the landing gate's own green line", path)
	}
	return heads, nil
}

// checkState is what the named check last did on this commit: green, failure, pending,
// or none when the head carries no such check at all. NONE IS NOT GREEN -- a head whose
// workflows were never queued has no evidence, which is the whole point of the read.
func checkState(c merge.Checks, name string) string {
	state := "none"
	for _, d := range c.Details {
		if d.Name != name {
			continue
		}
		switch merge.Bucket(d.Conclusion) {
		case "green":
			return "green"
		case "red":
			state = "failure"
		default:
			if state != "failure" {
				state = "pending"
			}
		}
	}
	return state
}

// withBin puts one directory in front of the step's PATH, for a program this gate found
// under ~/sdk rather than on PATH. The step's own command and anything IT runs -- the
// lisp step is a shell script that calls sbcl by name -- then find it.
func withBin(env []string, bin string) []string {
	// ALWAYS A COPY: the caller holds one env for the whole suite and every step appends
	// its own to what this returns, so handing back the caller's own slice would let one
	// step's append write into the next step's environment.
	if bin == "" {
		return append([]string(nil), env...)
	}
	out := make([]string, 0, len(env)+1)
	path := bin
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "PATH") {
			if value != "" {
				path = bin + string(os.PathListSeparator) + value
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

// mergeMembers merges every pull request head onto the branch IN THE ORDER GIVEN, which
// is the order they will land. A head that will not merge is dropped and said out loud,
// and the members after it are still judged -- on the tree without it, which is the tree
// that would land.
func mergeMembers(g *merge.Git, in batchRun, prs []int, stderr io.Writer, start time.Time) (members, dropped []int, code int) {
	for _, n := range prs {
		if _, err := g.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf("could not fetch pull/%d/head: %w", n, err))
		}
		// The merge writes a commit object, so it carries nova-merge's own identity: a CI
		// runner has no git identity anywhere and `git merge --no-ff` there dies with
		// "Committer identity unknown" (the Ubuntu leg of #57).
		message := fmt.Sprintf("merge pull request #%d into %s", n, in.name)
		_, err := g.Run(merge.Identity("merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD")...)
		if err == nil {
			members = append(members, n)
			fmt.Fprintf(stderr, "BATCH MERGED #%d t=%.1fs\n", n, since(start))
			continue
		}
		unmerged, cerr := hasConflicts(g)
		if cerr != nil {
			return nil, nil, batchRefused(stderr, cerr)
		}
		if !unmerged {
			return nil, nil, batchRefused(stderr, fmt.Errorf("the merge of pull/%d failed and left no conflicting file: %w", n, err))
		}
		if _, aerr := g.Run("merge", "--abort"); aerr != nil {
			return nil, nil, batchRefused(stderr, aerr)
		}
		dropped = append(dropped, n)
		fmt.Fprintf(stderr, "BATCH DROP #%d reason=%q t=%.1fs\n", n, "the merge conflicts with the members ahead", since(start))
	}
	return members, dropped, 0
}

// goDirective matches the `go <version>` line of a go.mod.
var goDirective = regexp.MustCompile(`(?m)^go\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// goVersionLine matches the version `go version` prints: `go version go1.26.5 linux/amd64`.
var goVersionLine = regexp.MustCompile(`go([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// checkToolchain refuses, with the remedy, a bench whose `go` is older than the tree's
// go.mod asks for -- before any step runs and before a minute of merging is spent.
//
// It does NOT let the go command solve this by downloading a toolchain: a gate is a
// verdict about a tree on THIS machine with THIS toolchain, and a step that silently
// fetched another one is a gate whose answer nobody can reproduce. A tree with no go.mod,
// no `go` directive, or a `go` this tool could not run at all is left alone: this check
// refuses what it KNOWS is wrong and never guesses.
func checkToolchain(clone string, stderr io.Writer) int {
	raw, err := os.ReadFile(filepath.Join(clone, "go.mod"))
	if err != nil {
		return 0
	}
	m := goDirective.FindStringSubmatch(string(raw))
	if m == nil {
		return 0
	}
	want := m[1]
	cmd := exec.Command("go", "version")
	// goenv.Clean like every other go command this binary runs: a caller's GOFLAGS can
	// change what an inner go command prints, and this reads what it printed.
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	v := goVersionLine.FindStringSubmatch(string(out))
	if v == nil {
		return 0
	}
	have := v[1]
	if !olderThan(have, want) {
		return 0
	}
	return batchRefused(stderr, fmt.Errorf(
		"this machine's go is go%s and %s asks for go%s; put a go%s or newer on PATH -- on this fleet that is ~/sdk/go%s*/bin -- and run this again. The gate does not download a toolchain: a verdict a bench reached with a compiler it fetched mid-run is a verdict nobody can reproduce",
		have, filepath.Join(clone, "go.mod"), want, want, want))
}

// olderThan compares two dotted go versions numerically, so go1.9 is older than go1.22
// (which a string compare calls newer) and go1.22 is older than go1.26.5.
func olderThan(have, want string) bool {
	hs, ws := strings.Split(have, "."), strings.Split(want, ".")
	for i := 0; i < len(hs) || i < len(ws); i++ {
		h, w := 0, 0
		if i < len(hs) {
			h, _ = strconv.Atoi(hs[i])
		}
		if i < len(ws) {
			w, _ = strconv.Atoi(ws[i])
		}
		if h != w {
			return h < w
		}
	}
	return false
}

// batchRefused is what a tool error costs: ONE line on stderr and exit 2, which is this
// tool's "could not run". A red batch is exit 1 and a green one is 0, and a clone that
// could not be made is neither -- it is nothing anybody may read as a verdict.
func batchRefused(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "BATCH REFUSED: %s\n", oneline.Err(err))
	return 2
}

// stepUnavailable is why a step cannot run here, or the empty string when it can. The
// second answer is the directory the program was found in when it was found OFF PATH, so
// the caller can put that directory in front of the step's own PATH.
func stepUnavailable(step batchStep, clone string) (why, binDir string) {
	if step.file != "" {
		if _, err := os.Stat(filepath.Join(clone, filepath.FromSlash(step.file))); err != nil {
			return "this checkout holds no " + step.file, ""
		}
	}
	if step.needs != "" {
		if _, err := exec.LookPath(step.needs); err == nil {
			return "", ""
		}
		if dir := lookInSDK(step.needs); dir != "" {
			return "", dir
		}
		return step.needs + " is not on this machine and is not under " + filepath.Join("~", sdkDir), ""
	}
	return "", ""
}

// lookInSDK is the one place off PATH this gate looks: `~/sdk/<anything>/bin/<program>`.
// It answers the directory, so the step's environment gets it in front of PATH and the
// SCRIPT the step runs finds the program too -- `sh tools/ci/lisp-test.sh` calls sbcl by
// name, so an absolute path handed only to the shell would not have reached it.
//
// The newest match wins by name, which is how these directories sort: sbcl-2.5.8 after
// sbcl-2.4.0. A directory that holds no such program is skipped rather than guessed at.
func lookInSDK(program string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(home, sdkDir))
	if err != nil {
		return ""
	}
	found := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin := filepath.Join(home, sdkDir, e.Name(), "bin")
		for _, name := range []string{program, program + ".exe"} {
			if info, err := os.Stat(filepath.Join(bin, name)); err == nil && !info.IsDir() {
				found = bin
				break
			}
		}
	}
	return found
}

// parsePRList reads the pull request numbers, separated by commas or spaces, in the
// order given -- which is the order they will be merged, so it is never sorted. A
// repeated number is a typo with two readings and is refused.
func parsePRList(raw string) ([]int, error) {
	seen := map[int]bool{}
	var prs []int
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--pr holds %q, which is not a pull request number", field)
		}
		if seen[n] {
			return nil, fmt.Errorf("--pr names #%d twice; a member lands once, and a repeat is a typo with two readings", n)
		}
		seen[n] = true
		prs = append(prs, n)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("--pr names no pull request; a batch of nothing is not a batch")
	}
	return prs, nil
}

// ciTestEnv is the environment every step runs in: a temp directory inside the batch's
// own working directory, and CI's fair share of the machine when the caller named one.
// GOMAXPROCS is what ci.yml's fair-share step sets and the only environment variable that
// step sets; a zero share is this process's own, which is every core.
func ciTestEnv(tmp string, gomaxprocs int) []string {
	// goenv.Clean FIRST: every step below is a go command whose output this verb
	// parses into packages and test names, and a caller's GOFLAGS=-json -- which CI's
	// own `make test` exports -- would turn that output into a JSON stream the parser
	// reads as a different result. The temp directory and GOMAXPROCS are appended
	// after Clean, where the last value wins.
	env := privateTempEnv(goenv.Clean(os.Environ()), tmp)
	if gomaxprocs > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(gomaxprocs))
	}
	return env
}

// stepFailure is what a red step says: the failing packages, the failing tests, and the
// one line a reader is pointed at.
//
// A step whose output is a `go test -json` stream is read as one; a step whose output is
// text is read as text. The fallback is not a nicety: a build failure writes plain text on
// stderr, runCheck captures both streams together, and a stream read as JSON-or-nothing
// would name no failing package at all on exactly the run that has one.
func stepFailure(step batchStep, out string, err error) (pkgs, tests []string, reason string) {
	if step.stream {
		if pkgs, tests, ok := testFailures(out); ok {
			return pkgs, tests, firstFailure(pkgs, tests)
		}
	}
	pkgs, tests = failuresIn(out)
	return pkgs, tests, firstLine(dropGoNotices(out), err)
}

// goNotice matches the lines the go command writes about ITSELF rather than about the
// tree: `go: downloading go1.26 (linux/amd64)`, `go: downloading golang.org/x/...`.
var goNotice = regexp.MustCompile(`^go: (downloading|finding|extracting|upgraded|added|toolchain)\b`)

// dropGoNotices takes those lines off the front of a step's output, so the one line the
// verdict quotes is THE ERROR and not the progress note in front of it.
//
// EDGE 1: a build that failed because the toolchain was too old reported
// `reason="go: downloading go1.26 (linux/amd64)"`. That line is not a failure, it names
// nothing to fix, and it was chosen for the verdict only because firstLine takes the
// first line that says anything. A notice is not the news.
func dropGoNotices(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if s := strings.TrimSpace(line); s == "" || goNotice.MatchString(s) {
			continue
		}
		return strings.Join(lines[i:], "\n")
	}
	// Everything was a notice: hand back what there was rather than nothing, so a reader
	// sees what the step said instead of an empty reason.
	return out
}

// testFailures reads a `go test -json` stream for the packages and tests that failed,
// decoded by internal/ci/slowtests -- THE SAME DECODER cmd/nova-ci slowtests reads CI's
// stream with, so the gate and the budget check cannot disagree about what a stream said.
// ok is false when the output is not all JSON, which is the caller's signal to read it as
// text instead.
func testFailures(out string) (pkgs, tests []string, ok bool) {
	events, err := slowtests.Parse(strings.NewReader(out))
	if err != nil {
		return nil, nil, false
	}
	seenPkg, seenTest := map[string]bool{}, map[string]bool{}
	for _, ev := range events {
		if ev.Action != "fail" || ev.Package == "" {
			continue
		}
		if ev.Test == "" {
			if !seenPkg[ev.Package] {
				seenPkg[ev.Package] = true
				pkgs = append(pkgs, ev.Package)
			}
			continue
		}
		if !seenTest[ev.Test] {
			seenTest[ev.Test] = true
			tests = append(tests, ev.Test)
		}
	}
	return pkgs, tests, true
}

// firstFailure is the one line a -json stream is reduced to: the first failing test and
// where it lives, or the first failing package when the failure named no test -- which is
// what a package that would not build looks like inside the stream.
func firstFailure(pkgs, tests []string) string {
	switch {
	case len(tests) > 0 && len(pkgs) > 0:
		return tests[0] + " failed in " + pkgs[0]
	case len(tests) > 0:
		return tests[0] + " failed"
	case len(pkgs) > 0:
		return pkgs[0] + " failed with no test named, which is a package that would not build"
	}
	return "go test exited non-zero and its stream named no failure"
}

// failuresIn reads a go test run's own output for the packages that failed and the tests
// that failed in them, so the FAIL line names what to look at and a reader does not have
// to open the log to find out. A build failure prints `FAIL <pkg> [build failed]`, whose
// package is read the same way.
func failuresIn(out string) (pkgs, tests []string) {
	seenPkg, seenTest := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 2 && fields[0] == "FAIL" && !strings.HasPrefix(fields[1], "["):
			if !seenPkg[fields[1]] {
				seenPkg[fields[1]] = true
				pkgs = append(pkgs, fields[1])
			}
		case len(fields) >= 3 && fields[0] == "---" && fields[1] == "FAIL:":
			name := strings.TrimSuffix(fields[2], ":")
			if !seenTest[name] {
				seenTest[name] = true
				tests = append(tests, name)
			}
		}
	}
	return pkgs, tests
}

// numberList renders the members and the dropped as one token a caller can split, and
// "none" where there are none -- never an empty value, which reads as a field the tool
// forgot to fill in.
func numberList(ns []int) string {
	if len(ns) == 0 {
		return "none"
	}
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, ",")
}

// numberOrNone is numberList for a list of names, capped, because a run where everything
// failed must still print one line.
func numberOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return oneline.Cap(strings.Join(names, ","), oneline.TailBytes)
}

// since is how long the batch has been running, in seconds, for the progress lines. It is
// the wall clock on purpose: the question a reader has at minute three is how long this
// has really taken, which no injected clock can answer.
func since(start time.Time) float64 { return time.Since(start).Seconds() }
