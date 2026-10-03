package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// THE GATE VERDICT (docs/SPEC-SPRINT.md section 5; SPEC-NOVA-DECIDE section 9). When a work
// card's child ends `verdict: not-done` and the gate output it names (its result's `output:`
// file, else its body) holds go test failures, native, before the member reports the take,
// runs the failing tests once at the card's start (one run, in the child's own wall and
// environment, bounded by gateRunWait: are they red at the base?), asks nova-decide's gate
// decision of each failure, records it, and routes the gate at the bars its frame carries, the
// sprint row's: a failure flaky at or above the flaky bar is rerun once at the head (bounded
// the same), its result attached as the decision's outcome; one pre-existing at or above the
// pre-existing bar is the base's or the member's. A bar the row leaves empty (the default)
// takes no route: the decisions are recorded and shown, and nothing is rerun or reclassified.
// One line says where the gate went and what was decided, `NATIVE GATE label=<l> op=<op>
// route=<green|pre-existing|caused> tests=<names> classes=<Test>:<class>:<p>,...`: the member
// reads green as the work done (its rerun passed) and pre-existing as the take failed
// `pre-existing: <tests>`, which is never the card's failure; caused, or a gate decision that
// could not be made (said on one NATIVE NOTE line), is the take as the child reported it. Every
// decision is in the machine's record, <root>/decide/gate.jsonl, under
// <primary>@<attempt>@gate/<pkg>.<Test>.

// gateRunner runs the failures' tests once in the checkout dir and says which are red, by
// key (decide.Failure.Key). A test's runner is a fake; native's runs go test in the child's
// wall (nativeGateRunner).
type gateRunner func(ctx context.Context, dir string, fs []decide.Failure) (red map[string]bool, err error)

// gateRecord is the machine's record of gate decisions.
func gateRecord(root string) string { return filepath.Join(root, "decide", "gate.jsonl") }

// gateOp is a work card's gate op: <primary>@<attempt>@gate.
func gateOp(card string, attempt int) string {
	primary := card
	if i := strings.LastIndex(card, ".w"); i > 0 {
		primary = card[:i]
	}
	return primary + "@" + strconv.Itoa(attempt) + "@gate"
}

// gateRunWait bounds one run of the gate's failing tests (the base run, the rerun): a red
// gate's report waits at most this for each, then the decision's asks (decideWait).
const gateRunWait = 3 * time.Minute

// maxGateOutput bounds the gate output native reads.
const maxGateOutput = 4 << 20

// gateText is the child's gate output: the file its result's `output:` names (inside the
// job or the child's temp), else the result's body, where the contract puts the gate's
// output for the readers; "" with the result's verdict when the child did not end not-done.
func gateText(jobDir, tmpDir string) (verdict, text string) {
	cr, shimmed := cardcontract.ReadFinish(jobDir)
	own := ""
	if path, ok := swarm.FindCardResult(jobDir); ok {
		if b, err := os.ReadFile(path); err == nil {
			own = string(b)
			if !shimmed {
				cr = typedrec.ParseCardResult(b)
			}
		}
	}
	if cr.Verdict != "not-done" {
		return cr.Verdict, ""
	}
	if out := strings.TrimSpace(cr.Output); out != "" && out != "-" {
		for _, p := range []string{out, filepath.Join(jobDir, out), filepath.Join(jobDir, swarm.JobRepo, out)} {
			if !filepath.IsAbs(p) {
				continue
			}
			p = filepath.Clean(p)
			if !strictlyWithin(jobDir, p) && (tmpDir == "" || !strictlyWithin(tmpDir, p)) {
				continue
			}
			if b, err := readCapped(p, maxGateOutput); err == nil && len(decide.ParseGateOutput(string(b))) > 0 {
				return cr.Verdict, string(b)
			}
		}
	}
	return cr.Verdict, cr.Body + "\n" + own
}

func readCapped(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

// nativeGate makes a framed work card's gate decision, when its child ended not-done on a red
// gate: the base run, the decisions, the rerun of the flaky ones when the flaky bar is set,
// and the NATIVE GATE line (out). A gate decision that cannot be made is one NATIVE NOTE line
// (errOut) and the take is reported as the child said.
func nativeGate(cfg nativeRunConfig, jobDir, tmpDir, start string, run gateRunner, out, errOut io.Writer) {
	fr := cfg.frame
	if fr == nil || fr.Kind != "work" {
		return
	}
	verdict, text := gateText(jobDir, tmpDir)
	failures := decide.ParseGateOutput(text)
	if verdict != "not-done" || len(failures) == 0 {
		return
	}
	note := func(why string) {
		fmt.Fprintf(errOut, "NATIVE NOTE: %s no gate decision: %s; the take is reported as the child said\n", oneline.Field(cfg.label), oneline.Escape(why))
	}
	bars, err := decide.ParseGateBars(fr.DecideGateFlaky, fr.DecideGatePreexisting)
	if err != nil {
		note(err.Error())
		return
	}
	dc := deciderOf(cfg)
	if dc == nil {
		note(decide.JevSecret + " is absent from this environment (the member loop's nova-secrets keys)")
		return
	}
	repo := filepath.Join(jobDir, swarm.JobRepo)
	ctx, cancel := context.WithTimeout(context.Background(), 2*gateRunWait+decideWait)
	defer cancel()
	in := decide.GateInput{Failures: failures, Paths: decide.CardPaths(string(cfg.card))}
	if start != "" {
		diff, err := gitrun.Output(ctx, gitrun.Options{C: repo, OwnRepo: true}, "diff", "-M", "--no-color", "--end-of-options", start, "HEAD")
		if err == nil {
			in.Diff = decide.DiffSummary(diff)
		}
		bctx, bcancel := context.WithTimeout(ctx, gateRunWait)
		in.BaseRed, err = baseRed(bctx, repo, filepath.Join(jobDir, ".gate-base"), start, failures, run)
		bcancel()
		if err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: %s the gate's failures were not run at the base %s: %s; asked as not run\n", oneline.Field(cfg.label), oneline.Field(start), oneline.Escape(err.Error()))
		}
	}
	if err := os.MkdirAll(filepath.Dir(gateRecord(cfg.root)), 0o755); err != nil {
		note("the record's directory: " + err.Error())
		return
	}
	op := gateOp(fr.Card, fr.Attempt)
	dctx, dcancel := context.WithTimeout(ctx, decideWait)
	res, err := decide.Gate(dctx, dc.backend, bars, in, gateRecord(cfg.root), op, dc.now())
	dcancel()
	if err != nil {
		note(err.Error())
		return
	}
	route, named := res.Route, res.PreExistingTests()
	switch res.Route {
	case decide.Flaky:
		rctx, rcancel := context.WithTimeout(ctx, gateRunWait)
		red, err := run(rctx, repo, res.Rerun())
		rcancel()
		if err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: %s the flaky failures' rerun did not run: %s; read as red again\n", oneline.Field(cfg.label), oneline.Escape(err.Error()))
			red = map[string]bool{}
			for _, k := range decide.Keys(res.Rerun()) {
				red[k] = true
			}
		}
		if route, err = decide.SettleGate(gateRecord(cfg.root), res, red, in.BaseRed, dc.now()); err != nil {
			fmt.Fprintf(errOut, "NATIVE NOTE: %s the rerun's result was not attached to its decisions: %s\n", oneline.Field(cfg.label), oneline.Escape(err.Error()))
		}
		switch route {
		case decide.Green:
			named = res.Rerun()
		case decide.Caused:
			named = nil
			for _, f := range res.Rerun() {
				if red[f.Key()] {
					named = append(named, f)
				}
			}
		}
	case decide.Caused:
		named = nil
		for _, c := range res.Calls {
			if c.Route == decide.Caused {
				named = append(named, c.Failure)
			}
		}
	}
	fmt.Fprintf(out, "NATIVE GATE label=%s op=%s route=%s tests=%s classes=%s\n", oneline.Field(cfg.label), oneline.Field(op), oneline.Field(route),
		oneline.Escape(strings.ReplaceAll(decide.Names(named), " ", "")), oneline.Escape(res.Classes()))
}

// baseRed runs the failures' tests at start in a worktree at dir and says which are red;
// a build failure (no test) is not run. The worktree is removed after.
func baseRed(ctx context.Context, repo, dir, start string, failures []decide.Failure, run gateRunner) (map[string]bool, error) {
	var tests []decide.Failure
	for _, f := range failures {
		if f.Test != "" {
			tests = append(tests, f)
		}
	}
	if len(tests) == 0 {
		return map[string]bool{}, nil
	}
	git := gitrun.Options{C: repo, OwnRepo: true}
	if _, err := gitrun.Output(ctx, git, "worktree", "add", "--detach", "--force", "--", dir, start); err != nil {
		return nil, err
	}
	defer gitrun.Output(context.Background(), git, "worktree", "remove", "--force", "--", dir) // ignored: the job's sweep removes the directory
	return run(ctx, dir, tests)
}

// testNameRE is a test function's name: what a -run pattern may hold.
var testNameRE = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)

// nativeGateRunner is the real gateRunner: one `go -C <dir> test -count=1 -run '^(A|B)$'
// <pkgs>` in the child's wall (wrap: nativeSandboxArgv's) with the child's environment, so
// the rerun meets what the child's gate met, the build cache warm. A test its output names
// failing is red; a package that failed with no test named has all its tests red; a run that
// failed with no go test output at all is an error.
func nativeGateRunner(wall string, wrap func([]string) []string, env []string, cwd, goBin string) gateRunner {
	return func(ctx context.Context, dir string, fs []decide.Failure) (map[string]bool, error) {
		var names, pkgs []string
		seen := map[string]bool{}
		for _, f := range fs {
			if !testNameRE.MatchString(f.Test) {
				continue
			}
			if !seen["test "+f.Test] {
				names, seen["test "+f.Test] = append(names, f.Test), true
			}
			if !seen["pkg "+f.Pkg] {
				pkgs, seen["pkg "+f.Pkg] = append(pkgs, f.Pkg), true
			}
		}
		if len(names) == 0 {
			return map[string]bool{}, nil
		}
		goPath := "go"
		if goBin != "" {
			goPath = filepath.Join(goBin, "go")
		}
		argv := append([]string{goPath, "-C", dir, "test", "-count=1", "-run", "^(" + strings.Join(names, "|") + ")$"}, pkgs...)
		path := argv[0]
		if wall != "" {
			path, argv = wall, wrap(argv)
		} else {
			argv = argv[1:]
		}
		b := subproc.Prepare(ctx, gateRunWait, path, argv...)
		defer b.Cancel()
		b.Cmd.Dir, b.Cmd.Env = cwd, env
		raw, err := b.Cmd.CombinedOutput()
		return gateRed(fs, string(raw), b.Wrap("the gate's rerun", err))
	}
}

// gateRed is which of fs a rerun's output and exit say are red.
func gateRed(fs []decide.Failure, out string, runErr error) (map[string]bool, error) {
	red := map[string]bool{}
	if runErr == nil {
		return red, nil
	}
	got := decide.ParseGateOutput(out)
	if len(got) == 0 {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) {
			return nil, runErr
		}
		return nil, fmt.Errorf("go test failed with no test output: %s", oneline.Cap(strings.TrimSpace(out), 300))
	}
	for _, f := range fs {
		for _, g := range got {
			if g.Pkg == f.Pkg && (g.Test == f.Test || g.Test == "") {
				red[f.Key()] = true
			}
		}
	}
	return red, nil
}
