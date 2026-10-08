package main

// landgo.go is the lander's go runs in the clone (docs/SPEC-SPRINT.md section 7, the
// tree gate; the generated ledgers' update runs are landledger.go's). Every run is in the
// caller's environment with GOFLAGS=-mod=readonly: no run writes go.mod or go.sum, so a
// run never leaves the clone dirty (under a caller's -mod=mod the update runs of
// 2026-10-03 rewrote go.mod and every resolution was refused for "the update run
// changed go.mod"), and a module that needs them changed fails the run, which is the
// card's finding.
//
// The tree gate is what every tip of the batch branch passes before the next head is
// merged: the module builds and vets (`go build ./...`, `go vet ./...`), its functional
// tier's files are vetted too (`go vet -tags functional ./...`, the Makefile's
// vet-functional: a plain vet compiles no `//go:build functional` file, so a redeclaration
// behind that tag landed on a base whose lint was red, PR 5435), and when a
// head changes a Go file, a document, or testdata (.go, .md, testdata/), the packages
// that test the tree itself (treeTests, where the clone has them) pass. The base's tip
// is gated once a batch before any head is merged, so a base that is red refuses the
// batch and blames no card, unless a head of the batch cures it (cureBase): that head lands
// first as the base fix. A head whose merged tree is red is taken off the batch branch
// and ends the batch as a head that does not merge does, the gate's run and output its
// finding. A clone with no go.mod has no module and no gate.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// landGoBudget bounds one go run in the clone: a build of the module, a vet, a test of
// the tree's own packages, or one update run (a build and two tests of one package).
const landGoBudget = 15 * time.Minute

// treeTests are the packages that test the tree itself (its docs and its tests), run by
// the gate when a head changes a .md or a _test.go file; one the clone lacks is not run.
var treeTests = []string{"internal/docs", "internal/ci"}

// goRun runs one go command (run) in the clone, in the lander's environment with
// GOFLAGS=-mod=readonly (caller flags preserved), GOCACHE set explicitly to the one build
// cache every run of this process shares (goCache), and set (NAME=value each); its combined
// output.
func (l *lander) goRun(ctx context.Context, dir string, run []string, set ...string) (string, error) {
	b := subproc.Prepare(ctx, landGoBudget, run[0], run[1:]...)
	defer b.Cancel()
	var env []string
	if l.a != nil {
		env = l.a.gitEnv
	}
	if env == nil {
		env = os.Environ()
	}
	with := []string{readonlyGoFlags(env)}
	if cache := l.goCache(ctx, env); cache != "" {
		with = append(with, "GOCACHE="+cache)
	}
	b.Cmd.Dir, b.Cmd.Env = dir, withEnv(env, append(with, set...)...)
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}

// goCache is the build cache the lander's go runs share, set explicitly on each so the
// gates of the streams merging at once (landpass.go) compile a package once: the GOCACHE
// env names, else the one the toolchain resolves (`go env GOCACHE`: the machine's shared
// cache where its go env names one, asked once a process), else a cache under the land
// root (<root>/cache/go-build, the swarm's own name for it). "" when none can be named.
func (l *lander) goCache(ctx context.Context, env []string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "GOCACHE="); ok && v != "" {
			return v
		}
	}
	if l.a == nil {
		return ""
	}
	l.a.goCacheOnce.Do(func() {
		b := subproc.Prepare(ctx, time.Minute, "go", "env", "GOCACHE")
		defer b.Cancel()
		b.Cmd.Env = env
		if out, err := b.Cmd.Output(); err == nil {
			l.a.goCachePath = strings.TrimSpace(string(out))
		}
		if l.a.goCachePath == "" || l.a.goCachePath == "off" {
			l.a.goCachePath = ""
			if root, err := l.a.landRoot(); err == nil {
				l.a.goCachePath = swarm.GoBuildCacheDir(root)
			}
		}
	})
	return l.a.goCachePath
}

// readonlyGoFlags returns GOFLAGS=... with -mod=readonly set, preserving any other
// flags from env's GOFLAGS entries and dropping any existing -mod or -mod=... flag.
func readonlyGoFlags(env []string) string {
	var terms []string
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "GOFLAGS="); ok {
			for _, term := range strings.Fields(val) {
				if !strings.HasPrefix(term, "-mod=") && term != "-mod" {
					terms = append(terms, term)
				}
			}
		}
	}
	terms = append(terms, "-mod=readonly")
	return "GOFLAGS=" + strings.Join(terms, " ")
}

// withEnv is env with each of set (NAME=value) in place of the NAME it held, else added;
// duplicate entries of NAME in env are dropped.
func withEnv(env []string, set ...string) []string {
	out := slices.Clone(env)
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		prefix := name + "="
		first := slices.IndexFunc(out, func(e string) bool { return strings.HasPrefix(e, prefix) })
		if first >= 0 {
			out[first] = kv
			seen := false
			out = slices.DeleteFunc(out, func(e string) bool {
				if strings.HasPrefix(e, prefix) {
					if !seen {
						seen = true
						return false
					}
					return true
				}
				return false
			})
		} else {
			out = append(out, kv)
		}
	}
	return out
}

// treeTested says a change to p is one the tree tests read: a Go file, a document, or
// under testdata.
func treeTested(p string) bool {
	return strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md") || strings.Contains(p, "testdata/")
}

// gateRuns is the tree gate's runs, in order: the build, the vet of the module and the vet
// of its functional-tier files (`-tags functional`, the Makefile's vet-functional), then
// the tree tests (have: the ones the clone holds) when tests is asked.
func gateRuns(tests bool, have []string) [][]string {
	runs := [][]string{
		{"go", "build", "./..."},
		{"go", "vet", "./..."},
		{"go", "vet", "-tags", "functional", "./..."},
	}
	if tests && len(have) > 0 {
		run := []string{"go", "test"}
		for _, p := range have {
			run = append(run, "./"+p+"/")
		}
		runs = append(runs, run)
	}
	return runs
}

// gateWhy is a red run as a finding, one line: the run, how it ended and its output.
func gateWhy(run []string, err error, out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(run, " ") + ": " + oneline.Err(err) + ": " + oneline.Cap(strings.Join(lines, " | "), 1500)
}

// baseGateFail is a base commit's failures of its tree gate under the base-gate rule: how
// many, the last finding, and when it is gated again.
type baseGateFail struct {
	n    int
	why  string
	next time.Time
}

// treeGateBase gates the base's tip at baseSha: "" when green, else the finding, and stop
// when the stream stops on it. A green base is cached for its commit, so the same base
// commit is not re-gated across streams or rounds. A red one is the base-gate rule's
// (docs/SPEC-SPRINT.md section 8, answered by rule; the coordinator, 2026-10-04: five streams sat
// stopped 16 minutes on a toolchain's transient "package ... is not in std"): it is gated
// again after sprint.BaseGateRetries[0], then after [1], each landing in between refused
// with the finding and when it is gated again; its third failure stops every stream that
// lands on it (stop), each with the error, the coordinator's judgment. With the rule off
// (nova-config's sprint row answer_rules_off), a red base is cached for its commit as a
// green one is, as before the rule: every landing on it refused until the base moves.
func (l *lander) treeGateBase(ctx context.Context, dir, baseSha string) (why string, stop bool) {
	if l.baseGateCache == nil {
		l.baseGateCache = map[string]string{}
	}
	if l.baseGateFails == nil {
		l.baseGateFails = map[string]*baseGateFail{}
	}
	if why, cached := l.baseGateCache[baseSha]; cached {
		return why, false
	}
	now := l.clock()
	f := l.baseGateFails[baseSha]
	switch {
	case f != nil && f.n > len(sprint.BaseGateRetries):
		return f.why, true
	case f != nil && now.Before(f.next):
		return f.said(), false
	}
	why = l.treeGate(ctx, dir, true)
	if l.deferral != nil {
		// every bench faulted: no verdict on the base, so nothing cached or counted
		return why, false
	}
	if why == "" || slices.Contains(l.offRules(ctx), sprint.RuleBaseGate) {
		l.baseGateCache[baseSha] = why
		delete(l.baseGateFails, baseSha)
		return why, false
	}
	if f == nil {
		f = &baseGateFail{}
		l.baseGateFails[baseSha] = f
	}
	f.n, f.why = f.n+1, why
	if f.n > len(sprint.BaseGateRetries) {
		f.why = fmt.Sprintf("the base %s failed its tree gate %d times, %s apart then %s: %s", shortSha(baseSha), f.n, sprint.BaseGateRetries[0], sprint.BaseGateRetries[1], why)
		return f.why, true
	}
	f.next = now.Add(sprint.BaseGateRetries[f.n-1])
	return f.said(), false
}

// said is a failure as a refused landing says it: the finding, and when the base is gated
// again.
func (f *baseGateFail) said() string {
	return fmt.Sprintf("%s; gated again at %s (failure %d of %d)", f.why, f.next.UTC().Format("15:04:05 MST"), f.n, len(sprint.BaseGateRetries)+1)
}

// shortSha is a commit id as a line says it.
func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// clock is the lander's clock: the app's, or the wall's.
func (l *lander) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	if l.a != nil && l.a.now != nil {
		return l.a.now()
	}
	return time.Now()
}

// offRules is the rules nova-config's sprint row turns off, read with the routes; given
// (a test), or none when the store does not say.
func (l *lander) offRules(ctx context.Context) []string {
	if l.rulesOff != nil || l.st == nil {
		return l.rulesOff
	}
	if l.a != nil {
		// a store read, under the server's line of control as every other read of land's
		l.a.serial.Lock()
		defer l.a.serial.Unlock()
	}
	off, err := l.st.RulesOff(ctx)
	if err != nil {
		return nil
	}
	return off
}

// treePackages are the treeTests the clone at dir holds as Go packages: a directory with
// no .go file in it (the nova-sprint repo's internal/ci holds only data, 2026-10-05) is no
// package, and `go test` of it fails every batch on that base, so it is not run.
func treePackages(dir string) []string {
	var have []string
	for _, p := range treeTests {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p), "*.go"))
		if len(matches) > 0 {
			have = append(have, p)
		}
	}
	return have
}

// treeGate runs the gate on the clone's tree, the tree tests too when tests: "" when it
// is green or the clone has no module, else the finding (gateWhy). In the server's land
// loop with a fleet member other than this machine up, the gate goes to the first such
// member that grants its Go lane, asked in the ring's order from the slot the batch's
// stream hashes to (benchRing, landring.go), as one bench run (benchGate); a bench that
// cannot be reached runs it here instead, and never blames the card. A bench whose run
// failed for the bench and not the tree (bench.ClassifyGate) is a fault: said, marked on
// its fleet row, and the ring's next slot asked. When every member of the ring faulted, or
// every up member is marked, the gate is deferred: deferral holds the faults and the
// finding is deferWhy's, which blames nothing (the callers defer the landing one tick).
// Otherwise, and for a land command on its own, it runs here (goRun). The ledgers' update
// runs stay here.
func (l *lander) treeGate(ctx context.Context, dir string, tests bool) string {
	l.deferral = nil
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	if l.a != nil && l.a.gateRan != nil {
		l.a.gateRan(dir, tests)
	}
	runs := gateRuns(tests, treePackages(dir))
	hosts, marked, inLoop := l.gateBenches(ctx)
	if len(hosts) == 0 && len(marked) > 0 {
		return l.deferGate(marked)
	}
	if len(hosts) > 0 {
		why, ran, faults := l.ringGateFaults(ctx, hosts, dir, runs, tests)
		if ran {
			return why
		}
		if faults != nil {
			return l.deferGate(append(marked, faults...))
		}
	}
	start := l.clock()
	defer func() {
		if inLoop {
			l.ranOnBench("here", l.clock().Sub(start))
		}
	}()
	for _, run := range runs {
		l.stage("gate", strings.Join(run, " "))
		if out, err := l.goRun(ctx, dir, run); err != nil {
			return gateWhy(run, err, out)
		}
	}
	return ""
}

// benchGate runs the gate's runs on a bench: the first host of the ring whose Go lane is
// granted, the ring being hosts (the up benches in the fleet's order) started at the slot
// the lander's gate key (the batch's stream) hashes to (benchRing), the tree staged there
// from the bench's own mirror at the gated commit (gateStage, bench.StageLine), the runs in
// order, the first red ending it. Before the ring is asked the commit is pushed to a
// temporary ref on the remote the bench fetches (bench.GateRef), deleted after whatever the
// gate did (bench.WithGateRef): only the sha crosses the tailnet, never the clone. Every
// stage is said (copySaid): on the loop's idle line as the step ("gate copy <host> <n>MB
// <t>s via mirror", "gate copy refused: <why>") and as a NOTE of the batch. A bench whose
// stage is refused is left for the next slot of the ring; one refused twice in the pass is
// passed over for the rest of it, said with the reason (bench.StageSkips). ran is false
// when the gate did not run on a bench (the commit could not be pushed, no lane before ctx
// ended, a bench not answering, every slot's stage refused): the caller runs it here, and
// that is nobody's finding. A gate that ran records the ring's size and the slot for the
// batch's LAND line (gateRing, gateSlot).
func (l *lander) benchGate(ctx context.Context, hosts []string, dir string, runs [][]string, tests bool) (why string, ran bool) {
	why, ran, _ = l.ringGateFaults(ctx, hosts, dir, runs, tests)
	return why, ran
}

// ringGateFaults is benchGate with the faults when every slot of the ring faulted (a stage
// refused, or a run the bench failed: bench.ClassifyGate): non-nil only then, and ran false.
func (l *lander) ringGateFaults(ctx context.Context, hosts []string, dir string, runs [][]string, tests bool) (why string, ran bool, faults []bench.Fault) {
	st, err := l.gateStage(ctx, dir)
	if err != nil {
		l.copySaid((&bench.StageError{Step: "the gated tree", Err: err}).Error())
		return "", false, nil
	}
	if st == nil { // a test's gateBench seam stands in for the bench and its stage
		return l.ringGate(ctx, hosts, dir, runs, tests, nil)
	}
	gerr := bench.WithGateRef(ctx, landRefGit{l: l, dir: dir}, st.Sha, st.Ref, func() error {
		why, ran, faults = l.ringGate(ctx, hosts, dir, runs, tests, st)
		return nil
	})
	if gerr != nil {
		l.copySaid(gerr.Error())
	}
	return why, ran, faults
}

// ringGate asks the ring for a lane and runs the gate there, stepping to the next slot
// when a bench's stage is refused or its run faulted (benchGate); faults is every slot's
// fault when none was left to ask (a slot passed over this pass for its refused stages is
// one), nil when the gate ran or was not run for any other reason.
func (l *lander) ringGate(ctx context.Context, hosts []string, dir string, runs [][]string, tests bool, st *bench.MirrorStage) (string, bool, []bench.Fault) {
	skips := l.stageSkips()
	tried, said := map[string]bool{}, map[string]bool{}
	var faults []bench.Fault
	for {
		ring, notes := skips.Ring(benchRing(l.gateKey, hosts))
		for _, n := range notes {
			if !said[n] {
				said[n] = true
				l.copySaid(n)
			}
		}
		var live []string
		for _, h := range ring {
			if !tried[h] {
				live = append(live, h)
			}
		}
		if len(live) == 0 {
			// every slot faulted: this gate's, and those passed over this pass for theirs
			for _, h := range benchRing(l.gateKey, hosts) {
				if why, skip := skips.Skipped(h); skip && !tried[h] {
					faults = append(faults, bench.Fault{Host: h, Kind: bench.FaultCopy, What: why})
				}
			}
			return "", false, faults
		}
		host, err := l.takeGateLane(ctx, live)
		if err != nil {
			return "", false, nil
		}
		tried[host] = true
		why, ran, refused, fault := l.gateOn(ctx, host, dir, runs, tests, st)
		if refused != nil {
			skips.Fail(host, refused.Error())
			fault = &bench.Fault{Host: host, Kind: bench.FaultCopy, What: refused.Error()}
		}
		if fault != nil {
			l.benchFaulted(ctx, *fault)
			faults = append(faults, *fault)
			continue
		}
		if ran {
			l.gateRing, l.gateSlot = len(hosts), ringSlot(l.gateKey, len(hosts))
		}
		return why, ran, nil
	}
}

// gateOn runs the gate on host, whose Go lane the lander holds and gives back: the finding
// ("" green) and ran; or the stage's refusal when the tree never reached the bench; or the
// bench's fault when the run failed for the bench and not the tree (bench.ClassifyGate:
// its git, its disk, its temporary directory, its ssh, its toolchain), which is no finding.
func (l *lander) gateOn(ctx context.Context, host, dir string, runs [][]string, tests bool, st *bench.MirrorStage) (why string, ran bool, refused *bench.StageError, fault *bench.Fault) {
	defer l.giveGateLane(host)
	l.stage("gate", "bench "+host+" held by "+l.laneWho()+" ("+l.gateWhose()+"): "+strings.Join(runs[0], " "))
	ctx, cancel := context.WithTimeout(ctx, time.Duration(len(runs))*landGoBudget)
	defer cancel()
	start := l.clock()
	out, code, err := l.runOnBench(ctx, host, dir, runs, tests, st)
	wall := l.clock().Sub(start)
	if errors.As(err, &refused) {
		return "", false, refused, nil
	}
	if err != nil {
		// a bench that answered and could not make its run (its disk full, its quota) is a
		// fault; one that did not answer, or anything else, runs the gate here, as before
		if f, ok := bench.ClassifyGate(host, 0, err.Error()); ok && !errors.Is(err, bench.ErrNoBench) && ctx.Err() == nil {
			return "", false, nil, &f
		}
		return "", false, nil, nil
	}
	if code == 0 {
		l.ranOnBench(host, wall)
		return "", true, nil, nil
	}
	if f, ok := bench.ClassifyGate(host, code, out); ok {
		return "", false, nil, &f
	}
	l.ranOnBench(host, wall)
	return gateWhy(redRun(runs, out), fmt.Errorf("exit status %d on the bench %s", code, host), out), true, nil, nil
}

// benchFaulted is one bench fault met by a gate: said (the GATE FAULT line, on the beat and
// with the batch), kept for the pass's one judgment, and, but for a copy (a refused stage,
// passed over for the pass by bench.StageSkips), the bench marked on its fleet row for
// sprint.BenchFaultFor so the ring skips it; a disk or tmp fault runs the bench's disk-guard
// loop at once.
func (l *lander) benchFaulted(ctx context.Context, f bench.Fault) {
	l.stage("gate "+f.Line(), l.gateWhose())
	l.gateFaults = append(l.gateFaults, f)
	if f.Kind == bench.FaultCopy || l.a == nil || l.st == nil {
		return
	}
	r := sprint.BenchFaultReq{Member: f.Host, Kind: f.Kind, What: f.What, Who: l.c.actor}
	step := store.Step{Verb: "land", Args: store.ArgsOf(r), Load: []string{sprint.Fleet},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MarkBenchFault(s, r) }}
	l.a.serial.Lock()
	_, _ = l.st.Run(ctx, step) // ignored: an unmarked bench faults again at its next gate and is marked then
	l.a.serial.Unlock()
	if f.Kind == bench.FaultDisk || f.Kind == bench.FaultTmp {
		l.guardBench(f.Host)
	}
}

// guardBench runs host's disk-guard loop at once (bench.DiskGuardLine), beside the landing
// and bounded by bench.StepBudget: a test's guardBench seam stands in for the bench, and with
// the gate's bench seam set and no guard seam nothing is run.
func (l *lander) guardBench(host string) {
	b := l.a.landState()
	b.mu.Lock()
	guard, seam := b.guardBench, b.gateBench
	b.mu.Unlock()
	if guard == nil {
		if seam != nil || testguard.Refusing() {
			return
		}
		guard = func(ctx context.Context, host string) error {
			_, err := bench.Exec{}.Shell(ctx, host, bench.DiskGuardLine, io.Discard, io.Discard)
			return err
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bench.StepBudget)
		defer cancel()
		_ = guard(ctx, host) // ignored: the loop runs on its own period whatever this kick did
	}()
}

// deferGate is a tree gate every bench of the ring faulted: the faults kept as the gate's
// deferral and the batch's (each said once), the pass's record of them for its one
// judgment, and the finding deferWhy says.
func (l *lander) deferGate(faults []bench.Fault) string {
	for _, f := range faults {
		if !slices.Contains(l.gateFaults, f) {
			l.gateFaults = append(l.gateFaults, f)
		}
	}
	l.deferral = faults
	l.locks().deferred(faults)
	return deferWhy(faults)
}

// deferWhy is a deferred gate's finding: every bench faulted, the kinds, and each bench's.
func deferWhy(faults []bench.Fault) string {
	var each []string
	for _, f := range faults {
		each = append(each, f.Host+" "+f.Kind+": "+f.What)
	}
	return "every bench faulted: " + faultKinds(faults) + " (" + strings.Join(each, "; ") + ")"
}

// faultKinds is the kinds of faults, sorted, each once, comma separated.
func faultKinds(faults []bench.Fault) string {
	var kinds []string
	for _, f := range faults {
		if !slices.Contains(kinds, f.Kind) {
			kinds = append(kinds, f.Kind)
		}
	}
	slices.Sort(kinds)
	return strings.Join(kinds, ", ")
}

// copySaid says one stage line: the loop's idle line carries it as the step, and the
// batch's report as a NOTE ("tree gate: <line>", the batch's also).
func (l *lander) copySaid(line string) {
	l.stage("gate "+line, l.gateWhose())
	l.ledgerLog = append(l.ledgerLog, "tree gate: "+line)
}

// gateStage is the mirror stage of the tree at dir (bench.MirrorStage): its commit, the
// temporary ref that commit is pushed to, the bench's mirror of the clone's repository and
// the clone's remote. nil with no error when a test's gateBench seam stands in for the
// bench. A tree whose files differ from its commit cannot be staged from a mirror: the
// gate runs here instead, said.
func (l *lander) gateStage(ctx context.Context, dir string) (*bench.MirrorStage, error) {
	if l.a != nil && l.a.landState().gate() != nil {
		return nil, nil
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	dirty, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	if dirty != "" {
		return nil, fmt.Errorf("the tree at %s holds changes its commit %s does not (%s); a bench stages a commit, so this gate runs here", dir, shortSha(sha), firstLine(dirty, nil))
	}
	remote, err := l.git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return nil, err
	}
	st := &bench.MirrorStage{Mirror: bench.MirrorDir(path.Base(strings.TrimRight(remote, "/"))), Remote: remote, Ref: bench.GateRef(l.gateKey, sha), Sha: sha}
	return st, st.Validate()
}

// landRefGit pushes and deletes the gate's temporary ref from the clone at dir to its
// origin (bench.RefGit).
type landRefGit struct {
	l   *lander
	dir string
}

func (g landRefGit) Push(ctx context.Context, sha, ref string) error {
	_, err := g.l.git(ctx, g.dir, "push", "--quiet", "--no-verify", "origin", sha+":"+ref)
	return err
}

func (g landRefGit) Delete(ctx context.Context, ref string) error {
	_, err := g.l.git(ctx, g.dir, "push", "--quiet", "--no-verify", "origin", ":"+ref)
	return err
}

// stageSkipsOf is each land pass's record of the benches whose stage failed, by the pass's
// shared locks (lander.locks): weak keys, each entry removed when its pass is collected.
var stageSkipsOf sync.Map // weak.Pointer[landShared] -> *bench.StageSkips

// stageSkips is this pass's record (stageSkipsOf).
func (l *lander) stageSkips() *bench.StageSkips {
	s := l.locks()
	key := weak.Make(s)
	v, loaded := stageSkipsOf.LoadOrStore(key, &bench.StageSkips{})
	if !loaded {
		runtime.AddCleanup(s, func(k weak.Pointer[landShared]) { stageSkipsOf.Delete(k) }, key)
	}
	return v.(*bench.StageSkips)
}

// gateMark starts the line a bench gate prints before each of its runs.
const gateMark = "GATE RUN: "

// gateScript is the gate's runs as one shell line on the bench: each run named on its
// own line (gateMark) and then run, the first red ending the line with its status.
func gateScript(runs [][]string) string {
	parts := []string{"set -e"}
	for _, run := range runs {
		words := make([]string, len(run))
		for i, w := range run {
			words[i] = bench.Quote(w)
		}
		parts = append(parts, "echo "+bench.Quote(gateMark+strings.Join(run, " ")), strings.Join(words, " "))
	}
	return strings.Join(parts, "; ")
}

// redRun is the run a red bench gate ended on: the last one its output names, else the
// first.
func redRun(runs [][]string, out string) []string {
	last := runs[0]
	for _, line := range strings.Split(out, "\n") {
		if named, ok := strings.CutPrefix(strings.TrimSpace(line), gateMark); ok {
			for _, run := range runs {
				if strings.Join(run, " ") == named {
					last = run
				}
			}
		}
	}
	return last
}

// gateBenches is the up fleet members other than this machine that bench.CheckHost
// accepts, in the fleet's order, and inLoop when the land running is the server's land
// loop's. Only the loop sends the gate out: a land command on its own (a hand land, the
// install walkthrough) keeps it in this process. A unit test under the host guard with no
// bench seam keeps it here too, so a member brought up in a test is not sshed to.
func (l *lander) gateBenches(ctx context.Context) (hosts []string, marked []bench.Fault, inLoop bool) {
	if l == nil || l.a == nil || l.st == nil {
		return nil, nil, false
	}
	b := l.a.landState()
	b.mu.Lock()
	inLoop = b.flight != nil
	seam := b.gateBench
	b.mu.Unlock()
	if !inLoop || (testguard.Refusing() && seam == nil) {
		return nil, nil, inLoop
	}
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Fleet}, nil)
	l.a.serial.Unlock()
	if err != nil || s == nil {
		return nil, nil, inLoop
	}
	self := l.a.machineName()
	faulted := sprint.BenchFaultsNow(s.Fleet.Props(), s.Now)
	for _, m := range s.UpMembers() {
		if self != "" && strings.EqualFold(m, self) {
			continue
		}
		if bench.CheckHost(m) != nil {
			continue
		}
		if f, ok := faulted[m]; ok {
			// marked faulted on its row: skipped by the ring until the mark ends
			marked = append(marked, bench.Fault{Host: m, Kind: f.Kind, What: "marked until " + f.Until.UTC().Format(time.RFC3339) + ": " + f.What})
			continue
		}
		hosts = append(hosts, m)
	}
	return hosts, marked, inLoop
}

// takeGateLane asks hosts (a ring: benchRing's order) for a Go lane one host at a time
// until one grants it: a host whose lane is held is skipped to the next, and the first
// granted wins, its place on the others given back; the granted host. When none grants,
// the lander keeps its place on the ring's first host alone (the slot the batch hashes
// to) and gives back the rest, and the ring is asked again from that host on the next
// land-loop cycle (waitLaneAsk): that loop is the clock, so this ask adds no timer of its
// own, and the beat keeps printing. The lander asks under its own name (laneWho), so a
// sibling fork's held lane is held to it too and it steps past it. The stage names the
// lane it waits on and whose gate waits (the stuck judgment carries it).
func (l *lander) takeGateLane(ctx context.Context, hosts []string) (string, error) {
	if len(hosts) == 0 {
		return "", fmt.Errorf("no bench to take a Go lane on")
	}
	who := l.laneWho()
	proc := "lane take go --machine " + hosts[0] + " --as " + who + " (" + l.gateWhose() + ")"
	asked := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			for h := range asked {
				l.giveGateLane(h)
			}
			return "", err
		}
		for _, h := range hosts {
			l.a.serial.Lock()
			ans, err := l.st.LaneStep(ctx, sprint.LaneGo, h, who, false)
			l.a.serial.Unlock()
			if err != nil {
				continue // this lane cannot be read now; the next may grant
			}
			asked[h] = true
			if ans.Granted {
				for o := range asked {
					if o != h {
						l.giveGateLane(o)
					}
				}
				return h, nil
			}
		}
		for h := range asked {
			if h != hosts[0] {
				l.giveGateLane(h) // the wait is on the batch's own slot alone
				delete(asked, h)
			}
		}
		l.stage("lane", proc)
		if err := l.waitLaneAsk(ctx); err != nil {
			for h := range asked {
				l.giveGateLane(h)
			}
			return "", err
		}
	}
}

// waitLaneAsk blocks until the land loop's next cycle, or until ctx ends. The
// loop fills one slot a cycle (landPulse); this function holds no clock.
func (l *lander) waitLaneAsk(ctx context.Context) error {
	b := l.a.landState()
	b.mu.Lock()
	ch := b.pulse
	b.mu.Unlock()
	if ch == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// giveGateLane returns this lander's Go lane, or its place in the queue, under its own name
// (laneWho): a sibling's hold or place on the same bench stays. A cancelled landing still
// gives it back; the hold expires at LaneHoldFor when the give does not land.
func (l *lander) giveGateLane(machine string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	_, _ = l.st.LaneStep(ctx, sprint.LaneGo, machine, l.laneWho(), true) // ignored: the hold expires at LaneHoldFor when this give does not land
}

// gatesBase makes the lander's next gate the base re-check's (baseRecheck): no batch, so the
// base re-checked is the ring's key, and the gate holds a bench's lane as landLaneBase.
func (l *lander) gatesBase(base string) {
	l.gateKey, l.laneAs = base, landLaneBase
}

// laneWho is the holder this lander's gate records on a bench's Go lane: lander/<stream> in
// a stream's fork, landLaneBase in the base re-check, landLaneWho otherwise.
func (l *lander) laneWho() string {
	if l.laneAs != "" {
		return l.laneAs
	}
	return landLaneWho
}

// gateWhose names whose gate holds or waits on a bench's lane, for the stage and so the
// stuck judgment: the stream's, or the base's in the base re-check.
func (l *lander) gateWhose() string {
	switch {
	case l.laneAs == landLaneBase:
		return "the gate of the base " + l.gateKey
	case l.gateKey != "":
		return "the gate of stream " + l.gateKey
	}
	return "the lander's gate"
}

// runOnBench runs the gate's runs on host (gateScript) in the tree st stages from the
// bench's mirror (never a copy of dir): the output, the exit status, and err when the runs
// could not be reached (bench.Run's error; a *bench.StageError when the stage was refused).
// The stage is said as it ends (copySaid). A test's gateBench seam stands in for the bench
// and its stage: a *bench.StageError it returns is a refused stage.
func (l *lander) runOnBench(ctx context.Context, host, dir string, runs [][]string, withGit bool, st *bench.MirrorStage) (string, int, error) {
	if l.a != nil {
		if gate := l.a.landState().gate(); gate != nil {
			out, code, err := gate(ctx, host, dir, runs, withGit)
			var refused *bench.StageError
			if errors.As(err, &refused) {
				l.copySaid(refused.Error())
			}
			return out, code, err
		}
	}
	if st == nil {
		return "", 0, fmt.Errorf("no mirror stage for %s: the gate runs here", dir)
	}
	var buf bytes.Buffer
	res, err := bench.Run(ctx, bench.Exec{}, bench.Options{
		Hosts:  []string{host},
		Stage:  st,
		Argv:   []string{"sh", "-c", gateScript(runs)},
		Stdout: &buf,
		Stderr: &buf,
		Now:    l.clock,
		Staged: func(s bench.Stage) { l.copySaid(s.Line()) },
	})
	return buf.String(), res.Code, err
}

// gateCard is the tree gate on one card merged onto the batch branch at before: red, the
// card is taken off the batch branch (reset to before) and ends the batch as a head that
// does not merge does, the finding its reason; card and env are mergeHead's. A merge that
// made no commit (the head is in the base already) is not gated: it changes nothing the
// base does not hold.
func (l *lander) gateCard(ctx context.Context, dir string, c landCard, before string) (card, env string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return "", ""
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only", "-M", before, after)
	if err != nil {
		return "", "the files the merge of " + c.id + " changed could not be listed: " + firstLine("", err)
	}
	why := l.treeGate(ctx, dir, slices.ContainsFunc(strings.Split(changed, "\n"), treeTested))
	if why == "" {
		return "", ""
	}
	if l.deferral != nil {
		return "", why // every bench faulted: no card's finding
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
		return "", "the batch branch could not be reset after " + c.id + " failed the tree gate: " + firstLine("", err)
	}
	return "the head " + c.head + " of " + c.id + " fails the tree gate: " + why, ""
}

// cureBase looks for the red base's fix among the batch's cards (sprint.FindBaseCure; docs/
// SPEC-SPRINT.md section 8, the base cure): each head merged onto the base alone, through the
// lander's own checks (mergeHead, checkCard) and the base's tree gate with the tree tests.
// The first green is the cure: its index in cards, the batch branch left at its merge, and its
// landing note naming the fix; -1 when no head cures the base, the branch at the base again.
// A head found no cure on this base is not tried on it again. env is a failure that is not a
// card's.
func (l *lander) cureBase(ctx context.Context, dir, stream string, cards []landCard, baseSha, why string) (cured int, env string) {
	if l.cureTried == nil {
		l.cureTried = map[string]bool{}
	}
	tried := func(h sprint.CureHead) string { return baseSha + " " + h.ID + "@" + h.Head }
	heads := make([]sprint.CureHead, len(cards))
	at := map[string]int{}
	for i, c := range cards {
		heads[i], at[c.id] = sprint.CureHead{ID: c.id, Head: c.head}, i
	}
	notes, repairs := map[string]string{}, map[string]string{}
	var deferred []bench.Fault
	cure, err := sprint.FindBaseCure(ctx, sprint.BaseCureReq{RepoDir: dir, Base: baseSha, Heads: heads, Env: l.a.gitEnv,
		Merge: func(ctx context.Context, h sprint.CureHead) (string, string) {
			c := cards[at[h.ID]]
			card, env, note := l.mergeHead(ctx, dir, stream, c)
			if card == "" && env == "" {
				var repaired string
				if card, env, repaired = l.checkCard(ctx, dir, stream, c, baseSha); repaired != "" {
					note = strings.TrimPrefix(note+"; "+repaired, "; ")
				}
				repairs[h.ID] = repaired
			}
			notes[h.ID] = note
			return card, env
		},
		Gate: func(ctx context.Context, dir string) string {
			why := l.treeGate(ctx, dir, true)
			if l.deferral != nil {
				deferred = l.deferral
			}
			return why
		},
		Tried: func(h sprint.CureHead) bool { return l.cureTried[tried(h)] },
	})
	l.conflictKind, l.conflictPaths = "", nil // a try's conflict is no card's stop
	if deferred == nil {
		// a search a faulted bench cut short tried no head for certain: each is tried again
		for _, t := range cure.Tried {
			l.cureTried[tried(t.CureHead)] = true
		}
	}
	if err != nil {
		return -1, "the search for a fix of the red base " + shortSha(baseSha) + " failed: " + oneline.Err(err)
	}
	if !cure.Found() {
		if deferred != nil {
			// a head's gate found every bench faulted: no head was shown not to cure the base
			l.deferral = deferred
			return -1, deferWhy(deferred)
		}
		return -1, ""
	}
	l.deferral = nil // the cure's own gate was green
	i := at[cure.ID]
	c := &cards[i]
	c.resolved = cure.Note(baseSha, why)
	if n := notes[cure.ID]; n != "" {
		c.resolved = n + "; " + c.resolved
	}
	if r := repairs[cure.ID]; r != "" {
		l.ledgerLog = append(l.ledgerLog, cure.ID+": "+r)
	}
	l.baseFix = cure.ID + " " + c.resolved
	return i, ""
}

// deferred records one deferred gate's faults for the pass's one judgment.
func (s *landShared) deferred(faults []bench.Fault) {
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	s.deferredN++
	for _, f := range faults {
		if !slices.Contains(s.faults, f) {
			s.faults = append(s.faults, f)
		}
	}
}

// judgeFaults raises the pass's one judgment when any of its gates was deferred because
// every bench faulted: "every bench faulted: <kinds>", with each bench's fault, however
// many streams and bases were deferred, never one a stream. The same benches and kinds as
// the last judgment's raise none (app.faultJudged); a pass that deferred nothing clears it.
func (l *lander) judgeFaults(ctx context.Context) {
	s := l.locks()
	s.faultMu.Lock()
	faults, n := slices.Clone(s.faults), s.deferredN
	s.faultMu.Unlock()
	if n == 0 || l.a == nil || l.st == nil {
		if l.a != nil {
			l.a.faultJudged = ""
		}
		return
	}
	var benches []string
	for _, f := range faults {
		if !slices.Contains(benches, f.Host+" "+f.Kind) {
			benches = append(benches, f.Host+" "+f.Kind)
		}
	}
	slices.Sort(benches)
	key := strings.Join(benches, ", ")
	if key == l.a.faultJudged {
		return
	}
	var each []string
	for _, f := range faults {
		each = append(each, f.Host+" "+f.Kind+": "+f.What)
	}
	note := sprint.Note{
		Kind:        sprint.Judgment,
		Type:        sprint.NOpStuck,
		StreamLevel: true,
		Who:         l.c.actor,
		Decisions:   append([]string(nil), sprint.Decisions[sprint.NOpStuck]...),
		What: fmt.Sprintf("every bench faulted: %s; %d tree gates deferred this pass, their landings tried again each tick (%s); the base is not marked red and no card is blamed",
			faultKinds(faults), n, oneline.Cap(strings.Join(each, "; "), 1500)),
	}
	l.a.serial.Lock()
	res, err := l.st.Run(ctx, store.NoteStep("land", note))
	l.a.serial.Unlock()
	if err == nil && len(res.Refused) == 0 {
		l.a.faultJudged = key
	}
}
