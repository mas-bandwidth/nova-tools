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
// merged: the module builds, formats and vets (`go build ./...`, `gofmt -l .`,
// `go vet ./...`), its functional tier's files are vetted too (`go vet -tags functional ./...`,
// the Makefile's vet-functional: a plain vet compiles no `//go:build functional` file, so a
// redeclaration behind that tag landed on a base whose lint was red, PR 5435), and when a
// head changes a Go file, a document, or testdata (.go, .md, testdata/), the analyzer
// classes and the packages that test the tree itself (treeTests, where the clone has them)
// pass. sprint.BaseClasses is that suite. The base's tip is gated once a batch before any
// head is merged, so a base that is red refuses the batch and blames no card, unless a head
// of the batch cures it (cureBase): that head lands first as the base fix. A head whose
// merged tree is red is taken off the batch branch and ends the batch as a head that does
// not merge does, the gate's run and output its finding. A clone with no go.mod has no
// module and no gate.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// landGoBudget bounds one go run in the clone: a build of the module, a vet, a test of
// the tree's own packages, or one update run (a build and two tests of one package).
const landGoBudget = 15 * time.Minute

const benchGateUnavailableWhy = "the configured remote bench did not run the tree gate; restore a bench and run land again"

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
	ownLandProcessGroup(b.Cmd)
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
		ownLandProcessGroup(b.Cmd)
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

// gateRuns is SPEC-SPRINT's base class suite (sprint.BaseClasses), preserving the build
// first, then formatting, vet and the functional-tier vet. Analyzer classes and the tree
// tests need their packages (have: the ones the clone holds) and run only when tests is asked.
func gateRuns(tests bool, have []string) [][]string {
	var runs [][]string
	for _, class := range sprint.BaseClasses {
		if class.Name == "class-tests" {
			if tests && len(have) > 0 {
				run := slices.Clone(class.Run)
				for _, p := range have {
					run = append(run, "./"+p+"/")
				}
				runs = append(runs, run)
			}
			continue
		}
		if len(class.Needs) > 0 && (!tests || slices.ContainsFunc(class.Needs, func(p string) bool { return !slices.Contains(have, p) })) {
			continue
		}
		runs = append(runs, slices.Clone(class.Run))
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
func (l *lander) treeGateBase(ctx context.Context, dir, baseSha string, branch ...string) (why string, stop bool) {
	s := l.locks()
	s.gateMu.Lock()
	if l.baseGateCache == nil {
		l.baseGateCache = map[string]string{}
	}
	if l.baseGateFails == nil {
		l.baseGateFails = map[string]*baseGateFail{}
	}
	if why, cached := l.baseGateCache[baseSha]; cached {
		s.gateMu.Unlock()
		return why, false
	}
	now := l.clock()
	f := l.baseGateFails[baseSha]
	switch {
	case f != nil && f.n > len(sprint.BaseGateRetries):
		s.gateMu.Unlock()
		return f.why, true
	case f != nil && now.Before(f.next):
		s.gateMu.Unlock()
		return f.said(), false
	}
	s.gateMu.Unlock()
	why = l.treeGate(ctx, dir, true)
	if err := ctx.Err(); err != nil {
		return err.Error(), false // an abandoned gate never counts as a red base
	}
	if why == benchGateUnavailableWhy {
		return why, false // infrastructure refusal is not a red base or a retry
	}
	if why != "" {
		base := l.base
		if len(branch) > 0 {
			base = branch[0]
		}
		if base == "" {
			base = baseSha
		}
		why = sprint.ClassGateWhy(dir, base, baseSha, why)
	}
	off := why != "" && slices.Contains(l.offRules(ctx), sprint.RuleBaseGate)
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if why == "" || off {
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
// stream hashes to (benchRing, landring.go), as one bench run (benchGate). A configured
// remote bench that cannot run the gate refuses it; it does not run Go on this machine.
// A land command on its own, or a loop with no remote bench configured, runs here
// (goRun). The ledgers' update runs stay here.
func (l *lander) treeGate(ctx context.Context, dir string, tests bool) string {
	if err := ctx.Err(); err != nil {
		return err.Error()
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	if l.a != nil && l.a.gateRan != nil {
		l.a.gateRan(dir, tests)
	}
	if err := ctx.Err(); err != nil {
		return err.Error()
	}
	runs := gateRuns(tests, treePackages(dir))
	hosts, inLoop, remote := l.gateBenches(ctx)
	// A test's gateBench seam stands in for the bench when no fleet member is up
	// (the class-gate regression: a fake runner, no socket, no beat to keep fresh).
	if len(hosts) == 0 && l.a != nil {
		if gate := l.a.landState().gate(); gate != nil {
			out, code, err := gate(ctx, "seam", dir, runs, tests)
			if err == nil && code != bench.NoAnswer {
				if code == 0 {
					return ""
				}
				return gateWhy(redRun(runs, out), fmt.Errorf("exit status %d on the bench seam", code), out)
			}
		}
	}
	if len(hosts) > 0 {
		if why, ran := l.benchGate(ctx, hosts, dir, runs, tests); ran {
			return why
		}
	}
	if remote {
		l.stage("gate", "remote bench unavailable")
		return benchGateUnavailableWhy
	}
	start := l.clock()
	defer func() {
		if inLoop {
			l.ranOnBench("here", l.clock().Sub(start))
		}
	}()
	for _, run := range runs {
		l.stage("gate", strings.Join(run, " "))
		out, err := l.goRun(ctx, dir, run)
		if err = gateRunError(run, out, err); err != nil {
			return gateWhy(run, err, out)
		}
	}
	return ""
}

// gateRunError enforces the formatter's silent success (SPEC-SPRINT, the base's class
// gate); the other classes report failure by their exit status. A gofmt that exits zero
// and lists files is red.
func gateRunError(run []string, out string, err error) error {
	if err == nil && len(run) > 0 && run[0] == "gofmt" && strings.TrimSpace(out) != "" {
		return fmt.Errorf("gofmt listed files that need formatting")
	}
	return err
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
// batch's LAND line (gateRing, gateSlot). A delete of the temporary ref that fails is
// said (copySaid) and left off the finding: why and ran stay what the ring set, so a
// cleanup refusal does not skip the clone and is not recorded as the head or the base
// failing the tree. A push refusal is said the same way; the ring was not asked, so
// ran stays false and the caller runs the gate here.
func (l *lander) benchGate(ctx context.Context, hosts []string, dir string, runs [][]string, tests bool) (why string, ran bool) {
	st, err := l.gateStage(ctx, dir)
	if err != nil {
		l.copySaid((&bench.StageError{Step: "the gated tree", Err: err}).Error())
		return "", false
	}
	if st == nil { // a test's gateBench seam stands in for the bench and its stage
		return l.ringGate(ctx, hosts, dir, runs, tests, nil)
	}
	gerr := bench.WithGateRef(ctx, landRefGit{l: l, dir: dir}, st.Sha, st.Ref, func() error {
		why, ran = l.ringGate(ctx, hosts, dir, runs, tests, st)
		return nil
	})
	if gerr != nil {
		l.copySaid(gerr.Error())
	}
	return why, ran
}

// ringGate asks the ring for a lane and runs the gate there, stepping to the next slot
// when a bench's stage is refused (benchGate).
func (l *lander) ringGate(ctx context.Context, hosts []string, dir string, runs [][]string, tests bool, st *bench.MirrorStage) (string, bool) {
	skips := l.stageSkips()
	tried, said := map[string]bool{}, map[string]bool{}
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
			return "", false
		}
		host, err := l.takeGateLane(ctx, live)
		if err != nil {
			return "", false
		}
		tried[host] = true
		why, ran, refused := l.gateOn(ctx, host, dir, runs, tests, st)
		if refused != nil {
			skips.Fail(host, refused.Error())
			continue
		}
		if ran {
			l.gateRing, l.gateSlot = len(hosts), ringSlot(l.gateKey, len(hosts))
		}
		return why, ran
	}
}

// gateOn runs the gate on host, whose Go lane the lander holds and gives back: the finding
// ("" green) and ran, or the stage's refusal when the tree never reached the bench.
func (l *lander) gateOn(ctx context.Context, host, dir string, runs [][]string, tests bool, st *bench.MirrorStage) (why string, ran bool, refused *bench.StageError) {
	defer l.giveGateLane(host)
	l.stage("gate", "bench "+host+" held by "+l.laneWho()+" ("+l.gateWhose()+"): "+strings.Join(runs[0], " "))
	ctx, cancel := context.WithTimeout(ctx, time.Duration(len(runs))*landGoBudget)
	defer cancel()
	start := l.clock()
	out, code, err := l.runOnBench(ctx, host, dir, runs, tests, st)
	wall := l.clock().Sub(start)
	if errors.As(err, &refused) {
		return "", false, refused
	}
	if err != nil || code == bench.NoAnswer {
		return "", false, nil
	}
	l.ranOnBench(host, wall)
	if code == 0 {
		return "", true, nil
	}
	return gateWhy(redRun(runs, out), fmt.Errorf("exit status %d on the bench %s", code, host), out), true, nil
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
		command := strings.Join(words, " ")
		if len(run) > 0 && run[0] == "gofmt" {
			command = "gate_format=$(" + command + "); printf '%s\\n' \"$gate_format\"; test -z \"$gate_format\""
		}
		parts = append(parts, "echo "+bench.Quote(gateMark+strings.Join(run, " ")), command)
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
// accepts, in the fleet's order. remote says the loop has another fleet member configured,
// even when it is down or the fleet could not be read. inLoop says land is the server's land
// loop's. Only the loop sends the gate out: a land command on its own (a hand land, the
// install walkthrough) keeps it in this process. A unit test under the host guard with no
// bench seam keeps it here too, so a member brought up in a test is not sshed to.
func (l *lander) gateBenches(ctx context.Context) (hosts []string, inLoop, remote bool) {
	if l == nil || l.a == nil || l.st == nil {
		return nil, false, false
	}
	b := l.a.landState()
	b.mu.Lock()
	inLoop = b.flight != nil
	seam := b.gateBench
	b.mu.Unlock()
	if !inLoop || (testguard.Refusing() && seam == nil) {
		return nil, inLoop, false
	}
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Fleet}, nil)
	l.a.serial.Unlock()
	if err != nil || s == nil {
		return nil, inLoop, true
	}
	self := l.a.machineName()
	for _, m := range s.Members() {
		if self == "" || !strings.EqualFold(m, self) {
			remote = true
			break
		}
	}
	for _, m := range s.UpMembers() {
		if self != "" && strings.EqualFold(m, self) {
			continue
		}
		if bench.CheckHost(m) != nil {
			continue
		}
		hosts = append(hosts, m)
	}
	return hosts, inLoop, remote
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
	if wait := gateWaitWhy(ctx); wait != "" {
		return "", wait // cancellation says nothing about the card
	}
	if why == "" {
		return "", ""
	}
	if why == benchGateUnavailableWhy {
		return "", why // no card failed its gate
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
	s := l.locks()
	s.gateMu.Lock()
	if l.cureTried == nil {
		l.cureTried = map[string]bool{}
	}
	s.gateMu.Unlock()
	tried := func(h sprint.CureHead) string { return baseSha + " " + h.ID + "@" + h.Head }
	heads := make([]sprint.CureHead, len(cards))
	at := map[string]int{}
	for i, c := range cards {
		heads[i], at[c.id] = sprint.CureHead{ID: c.id, Head: c.head}, i
	}
	notes, repairs := map[string]string{}, map[string]string{}
	benchUnavailable := false
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
			gate := l.treeGate(ctx, dir, true)
			benchUnavailable = benchUnavailable || gate == benchGateUnavailableWhy
			return gate
		},
		Tried: func(h sprint.CureHead) bool {
			s.gateMu.Lock()
			defer s.gateMu.Unlock()
			return l.cureTried[tried(h)]
		},
	})
	l.conflictKind, l.conflictPaths = "", nil // a try's conflict is no card's stop
	if err := ctx.Err(); err != nil {
		return -1, err.Error() // an abandoned gate proves no head unable to cure the base
	}
	if benchUnavailable {
		return -1, benchGateUnavailableWhy // leave every head eligible for a later cure search
	}
	s.gateMu.Lock()
	for _, t := range cure.Tried {
		l.cureTried[tried(t.CureHead)] = true
	}
	s.gateMu.Unlock()
	if err != nil {
		return -1, "the search for a fix of the red base " + shortSha(baseSha) + " failed: " + oneline.Err(err)
	}
	if !cure.Found() {
		return -1, ""
	}
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
