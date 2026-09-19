package pulse

// The accept gate: mechanical accept or reject of a swarm card's commit, before any push.
//
// SPEC-TOOLWORK.md §1 rules 2-5 and 10 (PR #1637), issue #1648. A decision is
// mechanical when it is made from exit codes, git objects and typed lines, by a verb
// that makes no model call and reads no prose as an instruction. This verb reads the
// card `cut` wrote (its typed header is the card writer's) and the job's git objects,
// and it NEVER opens RESULT.md: a worker cannot name its own gate, widen its own paths
// or supply its own seed, because nothing it wrote is read here.
//
// It runs in its own throwaway worktree of the job's head, under the job's slot, so an
// untracked file the worker left in its clone cannot turn a test green; every command
// runs through nova-sandbox; the tree is removed on every path. The steps, in order,
// and the first failure decides:
//
//	(a)  hygiene       identity, stray-file, secret, out-of-path   (internal/hygiene)
//	(b)  shape         the named test exists at head; the kind changed a test file
//	(c1) build, vet    at head
//	(b2) the base's tests survive: every Test at the base in a touched package is
//	     still at head with no skip gained, and the base's copy of every pre-existing
//	     test file the card changed, overlaid on the head, passes   (test-weakened)
//	(c2) the changed packages' tests at head                       (red-at-head)
//	(d)  nova-review mutate with the change reverted: a test the card wrote that stays
//	     green is vacuous-test; the card's named TEST: must be among the reds
//
// Build and vet run before the overlay on purpose: an overlay over a tree that does not
// compile would say test-weakened about a build error, and the token must name the
// fault. Each command runs ONCE. A red is a finding, never a rerun, because a gate that
// reruns until green accepts every flaky fix. A red the card neither changed nor named
// is run once at the BASE, and red there is the base's (ABSTAIN base-red). A red whose
// first non-notice line is the wall's or a missing toolchain's is the bench's (ABSTAIN
// toolchain).
//
// What this version does not yet carry, each owed to a later task and said on the line:
// control=- until --selftest (T04) puts a passing selftest on file; cert=hand until
// nova-pulse certify (T19) writes a record; the identity set comes from --identity until
// staging writes identity.tsv (T21).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// AcceptInput is everything `nova-pulse accept` takes, held apart from flag parsing.
type AcceptInput struct {
	Job   string // the job directory: the worker's clone, whose HEAD is the card's commit
	Card  string // the card file cut wrote
	Base  string // the ref the range is judged against
	Bench string // the bench this gate runs on
	Cert  string // the bench's certification record
	// Identities is the set a commit's author and committer must be in: the pool's one
	// row at harvest. Empty is a refusal, never a pass.
	Identities []hyg.Identity
	// Sandbox is the nova-sandbox binary; empty looks the tool's own name up on PATH.
	Sandbox string
	Timeout time.Duration
	Max     int
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time
}

// acceptStop is the first failure, in the words the verdict line prints.
type acceptStop struct {
	verdict string // REJECT or ABSTAIN
	reason  string
	at      string
}

type acceptChange struct {
	status string // A, D, M, T (renames are off, so a rename is a D and an A)
	path   string
}

type acceptGate struct {
	in    AcceptInput
	ctx   context.Context
	start time.Time
	h     CardHeader
	kind  Kind
	label string

	job, slot, runDir, wt, baseWt string
	head, base                    string
	certID                        string
	sandbox                       string
	reads, writes                 []string

	changed []acceptChange
	dirs    []string        // touched package directories, sorted
	written map[string]bool // Test functions the card added or changed
	notes   []string
	tests   int
	red     int
}

var (
	acceptTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	acceptSkipCall = regexp.MustCompile(`\bt\.(Skip|SkipNow|Skipf)\(`)
	acceptResult   = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): ([A-Za-z_0-9]+)`)
	acceptWallMark = regexp.MustCompile(`SANDBOX REFUSED|SANDBOX DENIED|\bWALL\b|Operation not permitted|[Nn]o space left on device|executable file not found|command not found|cannot find GOROOT|toolchain not available`)
)

// Accept runs the gate and prints one ACCEPT line. Exit 0 is OK, 1 is REJECT, 2 is
// ABSTAIN or REFUSED.
func Accept(in AcceptInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.Timeout <= 0 {
		in.Timeout = 30 * time.Minute
	}
	if in.Max < 0 {
		in.Max = 0
	}
	g := &acceptGate{in: in, start: in.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()
	g.ctx = ctx
	return g.run()
}

func (g *acceptGate) run() int {
	h, err := ReadCardHeader(g.in.Card)
	if err != nil {
		return g.refused(err.Error(), "cut the card again with the typed header, SPEC-TOOLWORK §5 rule 1")
	}
	g.h, g.label = h, h.Label
	if g.label == "" {
		g.label = "-"
	}
	if h.Kind == "" {
		return g.refused("card has no KIND: line", "the gate is chosen by KIND: from the tool's table and by nothing else; cut the card with the typed header")
	}
	kind, ok := KindNamed(h.Kind)
	if !ok {
		// A kind the table does not hold: refused by cut, abstained by accept (§5 rule
		// 3). `unknown-kind` is not in the spec's ABSTAIN grammar; it is the token that
		// sentence needs.
		g.kind = Kind{Name: h.Kind}
		return g.abstain("unknown-kind")
	}
	g.kind = kind
	if !kind.Gated() {
		return g.refused(fmt.Sprintf("kind %s declares no gate (gate=none)", kind.Name), "harvest skips the gate for this kind and its row says gate=none; there is nothing here to judge")
	}
	if !kind.ControlBuilt() {
		return g.refused(fmt.Sprintf("kind %s's control is not built in this version of the tool", kind.Name), "an ACCEPT OK its control never proved would be a green nobody saw red first; wait for the task that builds it")
	}
	if missing := h.Missing(); len(missing) > 0 {
		return g.refused(fmt.Sprintf("card lacks %s", strings.Join(missing, ", ")), "cut writes the typed header from the pool row; a gated kind carries every line")
	}
	if h.TestNone && kind.Step(StepMutate) {
		return g.refused(fmt.Sprintf("kind %s names its test and this card says TEST: none", kind.Name), "the mutate control asks whether the NAMED test went red")
	}

	id, why := acceptReadCert(g.in.Cert, g.in.Bench, h.LegsOrDefault())
	if why != "" {
		g.note("cert: " + why)
		return g.abstain("bench-uncertified")
	}
	g.certID = id

	job, err := filepath.Abs(g.in.Job)
	if err != nil {
		return g.refused(fmt.Sprintf("--job %s: %v", g.in.Job, err), "pass the job directory")
	}
	if _, err := os.Stat(filepath.Join(job, ".git")); err != nil {
		return g.refused(fmt.Sprintf("--job %s is not a git working copy", g.in.Job), "the job directory is the worker's clone, whose HEAD is the card's commit")
	}
	g.job = job
	head, err := g.git(job, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return g.refused("the job's clone has no HEAD commit", "a card ends at a commit; a job with none has nothing to accept")
	}
	base, err := g.git(job, "rev-parse", g.in.Base+"^{commit}")
	if err != nil {
		if base, err = g.git(job, "rev-parse", "origin/"+g.in.Base+"^{commit}"); err != nil {
			return g.refused(fmt.Sprintf("--base %s names no commit in the job's clone", g.in.Base), "pass the ref the card was cut against; the clone must hold it")
		}
	}
	if mb, err := g.git(job, "merge-base", base, head); err == nil && mb != "" {
		base = mb
	}
	g.head, g.base = head, base
	if head == base {
		return g.reject("no-test", "-")
	}

	if stop := g.bench(); stop != nil {
		return g.finish(stop)
	}
	if err := g.makeRun(); err != nil {
		g.note(err.Error())
		return g.abstain("toolchain")
	}
	defer g.cleanup()

	for _, step := range []func() *acceptStop{g.hygiene, g.survey, g.shape, g.buildVet, g.weakened, g.headTests, g.mutate} {
		if g.ctx.Err() != nil {
			return g.abstain("timeout")
		}
		if stop := step(); stop != nil {
			return g.finish(stop)
		}
	}
	return g.finish(nil)
}

// bench resolves the wall and the Go roots the wall is asked to admit. A missing wall
// or a missing go is the bench's.
func (g *acceptGate) bench() *acceptStop {
	name := strings.TrimSpace(g.in.Sandbox)
	if name == "" {
		name = "nova-sandbox"
	}
	found, err := exec.LookPath(name)
	if err != nil {
		g.note(fmt.Sprintf("no nova-sandbox: %s is on no PATH entry and --sandbox names no file", name))
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	g.sandbox = found
	// The Go roots are asked of the toolchain, the way `nova-sandbox run --go` asks
	// them, never guessed (SPEC-SANDBOX, `--go`): the bare wrap has no --go, so accept
	// names them itself with --read and --write.
	cmd := exec.CommandContext(g.ctx, "go", "env", "GOROOT", "GOMODCACHE", "GOCACHE")
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		g.note("go env failed: the go toolchain is not on PATH or does not answer")
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 3 {
		g.note("go env answered with other than three roots")
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	for _, r := range lines[:2] {
		if r = strings.TrimSpace(r); r != "" {
			if _, err := os.Stat(r); err == nil {
				g.reads = append(g.reads, r)
			}
		}
	}
	if cache := strings.TrimSpace(lines[2]); cache != "" {
		_ = os.MkdirAll(cache, 0o755)
		g.writes = append(g.writes, cache)
	}
	return nil
}

// makeRun makes the gate's own directory under the job's slot and the worktree of the
// head inside it. Nothing here touches the worker's copy.
func (g *acceptGate) makeRun() error {
	g.slot = filepath.Dir(g.job)
	if filepath.Base(g.slot) == "jobs" {
		g.slot = filepath.Dir(g.slot)
	}
	acceptDir := filepath.Join(g.slot, "accept")
	if err := os.MkdirAll(acceptDir, 0o755); err != nil {
		return fmt.Errorf("could not make %s: %v", acceptDir, err)
	}
	run, err := os.MkdirTemp(acceptDir, sanitizeLabel(g.label)+"-")
	if err != nil {
		return fmt.Errorf("could not make the gate's directory under %s: %v", acceptDir, err)
	}
	g.runDir = run
	wt := filepath.Join(run, "head")
	if _, err := g.git(g.job, "worktree", "add", "--detach", wt, g.head); err != nil {
		return fmt.Errorf("could not add a worktree of %s: %v", sha12(g.head), err)
	}
	g.wt = wt
	return nil
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "card"
	}
	return b.String()
}

func (g *acceptGate) cleanup() {
	ctx := context.WithoutCancel(g.ctx)
	for _, wt := range []string{g.wt, g.baseWt} {
		if wt != "" {
			_, _ = acceptGitOut(ctx, g.job, "worktree", "remove", "--force", wt)
		}
	}
	if g.runDir != "" {
		_ = safepath.RemoveUnder(filepath.Join(g.slot, "accept"), g.runDir)
	}
	_, _ = acceptGitOut(ctx, g.job, "worktree", "prune")
}

// (a) hygiene: the four checks, one package, and the first token in the spec's order
// decides.
func (g *acceptGate) hygiene() *acceptStop {
	fs, err := hyg.Check(g.ctx, hyg.Options{
		Repo: g.job, Base: g.base, Head: g.head, Paths: g.h.Paths,
		Identities: g.in.Identities, Kind: g.kind.Name,
	})
	if err != nil {
		g.note("hygiene could not run: " + err.Error())
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	if len(fs) == 0 {
		return nil
	}
	order := map[string]int{"identity": 0, "stray-file": 1, "secret": 2, "out-of-path": 3}
	sort.SliceStable(fs, func(i, j int) bool { return order[fs[i].Token] < order[fs[j].Token] })
	for _, f := range fs[1:] {
		g.note(fmt.Sprintf("hygiene reason=%s at=%s: %s", oneline.Field(f.Token), oneline.Field(f.At), oneline.Escape(f.Why)))
	}
	return &acceptStop{verdict: "REJECT", reason: fs[0].Token, at: fs[0].At}
}

// survey reads the range once: what changed, which packages, and which Test functions
// the card wrote or changed (their body at head differs from the base, or they are new).
func (g *acceptGate) survey() *acceptStop {
	out, err := g.git(g.job, "diff", "--no-ext-diff", "--no-renames", "--name-status", g.base, g.head)
	if err != nil {
		g.note("could not read the change: " + err.Error())
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	dirs := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		status, p, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok {
			continue
		}
		p = strings.TrimSpace(p)
		g.changed = append(g.changed, acceptChange{status: strings.TrimSpace(status)[:1], path: p})
		if strings.HasSuffix(p, ".go") {
			dirs[path.Dir(p)] = true
		}
	}
	for d := range dirs {
		g.dirs = append(g.dirs, d)
	}
	sort.Strings(g.dirs)
	g.written = map[string]bool{}
	for _, d := range g.dirs {
		baseBodies, _, err := g.testBodiesAt(g.base, d)
		if err != nil {
			return g.toolchain(err)
		}
		headBodies, _, err := g.testBodiesAt(g.head, d)
		if err != nil {
			return g.toolchain(err)
		}
		for name, body := range headBodies {
			if bb, ok := baseBodies[name]; !ok || bb != body {
				g.written[name] = true
			}
		}
	}
	return nil
}

// (b) shape: the kind changed at least one test file, and the named test is at head.
func (g *acceptGate) shape() *acceptStop {
	if !g.kind.Step(StepShape) {
		return nil
	}
	anyTest := false
	for _, c := range g.changed {
		if c.status != "D" && acceptIsTestFile(c.path) {
			anyTest = true
		}
	}
	if !anyTest {
		return &acceptStop{verdict: "REJECT", reason: "no-test", at: "-"}
	}
	if g.h.TestNone {
		return nil
	}
	bodies, _, err := g.testBodiesAt(g.head, g.h.TestPkg)
	if err != nil {
		return g.toolchain(err)
	}
	if _, ok := bodies[g.h.TestName]; !ok {
		return &acceptStop{verdict: "REJECT", reason: "named-test-missing", at: g.h.TestName}
	}
	return nil
}

// (c1) build and vet at head, inside the wall, once each.
func (g *acceptGate) buildVet() *acceptStop {
	if !g.kind.Step(StepPositive) {
		return nil
	}
	for _, step := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		out, err := g.wall(g.wt, "go", step...)
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err) {
			g.note(step[0] + ": " + acceptFirstLine(out))
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		return &acceptStop{verdict: "REJECT", reason: step[0], at: acceptFirstLine(out)}
	}
	return nil
}

// (b2) the base's tests survive at head: present, no skip gained, and the base's copy of
// a changed pre-existing test file still passes over the head. TEST-EDIT: excuses the
// named file from all but deletion, and the excused file is listed for the reader.
func (g *acceptGate) weakened() *acceptStop {
	if !g.kind.Step(StepPositive) {
		return nil
	}
	for _, c := range g.changed {
		if c.status == "D" && acceptIsTestFile(c.path) {
			return &acceptStop{verdict: "REJECT", reason: "test-weakened", at: c.path}
		}
	}
	for _, d := range g.dirs {
		baseBodies, baseFile, err := g.testBodiesAt(g.base, d)
		if err != nil {
			return g.toolchain(err)
		}
		headBodies, _, err := g.testBodiesAt(g.head, d)
		if err != nil {
			return g.toolchain(err)
		}
		names := make([]string, 0, len(baseBodies))
		for n := range baseBodies {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if g.h.IsTestEdit(baseFile[n]) {
				continue
			}
			hb, ok := headBodies[n]
			if !ok {
				return &acceptStop{verdict: "REJECT", reason: "test-weakened", at: n}
			}
			if len(acceptSkipCall.FindAllString(hb, -1)) > len(acceptSkipCall.FindAllString(baseBodies[n], -1)) {
				return &acceptStop{verdict: "REJECT", reason: "test-weakened", at: n}
			}
		}
		// The overlay: base copies of the pre-existing test files the card changed.
		var overlay []string
		for _, c := range g.changed {
			if c.status != "M" || !acceptIsTestFile(c.path) || path.Dir(c.path) != d {
				continue
			}
			if g.h.IsTestEdit(c.path) {
				g.note(fmt.Sprintf("test-edit=%s: a pre-existing test body changed under TEST-EDIT:; that is judgment the gate does not have, so the reader judges it", oneline.Field(c.path)))
				continue
			}
			overlay = append(overlay, c.path)
		}
		if len(overlay) == 0 {
			continue
		}
		var run []string
		for _, f := range overlay {
			src, err := g.git(g.job, "show", g.base+":"+f)
			if err != nil {
				return g.toolchain(fmt.Errorf("could not read %s at the base: %v", f, err))
			}
			if err := os.WriteFile(filepath.Join(g.wt, filepath.FromSlash(f)), []byte(src+"\n"), 0o644); err != nil {
				return g.toolchain(err)
			}
			for n, file := range baseFile {
				if file == f {
					run = append(run, n)
				}
			}
		}
		sort.Strings(run)
		out, err := g.testRun(g.wt, d, run)
		for _, f := range overlay {
			_, _ = g.git(g.wt, "checkout", "--", f)
		}
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err) {
			g.note("overlay: " + acceptFirstLine(out))
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		failing := acceptFailed(out)
		if len(failing) == 0 {
			// The base's test no longer compiles against the head: the card changed what
			// it used, and did not say so with TEST-EDIT:.
			return &acceptStop{verdict: "REJECT", reason: "test-weakened", at: overlay[0]}
		}
		if stop := g.redAtBase(d, failing); stop != nil {
			return stop
		}
		return &acceptStop{verdict: "REJECT", reason: "test-weakened", at: failing[0]}
	}
	return nil
}

// (c2) the changed packages' tests at head, each package once. A red the card neither
// wrote nor named is run once at the base before it is charged to the card.
func (g *acceptGate) headTests() *acceptStop {
	if !g.kind.Step(StepPositive) {
		return nil
	}
	for _, d := range g.dirs {
		out, err := g.testRun(g.wt, d, nil)
		g.tests += acceptCountResults(out)
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err) {
			g.note("test: " + acceptFirstLine(out))
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		failing := acceptFailed(out)
		if len(failing) == 0 {
			return &acceptStop{verdict: "REJECT", reason: "red-at-head", at: acceptFirstLine(out)}
		}
		var untouched []string
		for _, n := range failing {
			if !g.written[n] && n != g.h.TestName {
				untouched = append(untouched, n)
			}
		}
		if len(untouched) > 0 {
			if stop := g.redAtBase(d, untouched); stop != nil {
				return stop
			}
		}
		return &acceptStop{verdict: "REJECT", reason: "red-at-head", at: failing[0]}
	}
	return nil
}

// redAtBase runs the named tests ONCE at the base. Red there is the base's fault and an
// ABSTAIN; green there returns nil and the caller charges the red to the card.
func (g *acceptGate) redAtBase(d string, names []string) *acceptStop {
	if g.baseWt == "" {
		wt := filepath.Join(g.runDir, "base")
		if _, err := g.git(g.job, "worktree", "add", "--detach", wt, g.base); err != nil {
			return g.toolchain(fmt.Errorf("could not add a worktree of the base %s: %v", sha12(g.base), err))
		}
		g.baseWt = wt
	}
	out, err := g.testRun(g.baseWt, d, names)
	if err == nil {
		return nil
	}
	if acceptToolchainRed(out, err) {
		g.note("base: " + acceptFirstLine(out))
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	failing := acceptFailed(out)
	if len(failing) == 0 {
		failing = names
	}
	g.note(fmt.Sprintf("base-red at=%s: red at the base %s too; the base's fault, not the card's", oneline.Field(failing[0]), sha12(g.base)))
	return &acceptStop{verdict: "ABSTAIN", reason: "base-red", at: failing[0]}
}

// (d) the card's negative control: the change reverted, the tests kept, and they must go
// red. The suites run through the same wall, in a worktree under the gate's directory.
func (g *acceptGate) mutate() *acceptStop {
	if !g.kind.Step(StepMutate) {
		return nil
	}
	res, err := review.Mutate(g.ctx, review.MutateOptions{
		Repo: g.job, Base: g.base, Head: g.head, TempRoot: g.runDir, Exec: g.execWalled,
	})
	switch {
	case errors.Is(err, review.ErrNoTestsChanged):
		return &acceptStop{verdict: "REJECT", reason: "no-test", at: "-"}
	case errors.Is(err, review.ErrNoChangeToRevert):
		// Nothing but tests changed: there is no fix the named test could go red
		// without.
		return &acceptStop{verdict: "REJECT", reason: "named-test-not-red", at: g.h.TestName}
	case err != nil:
		if g.ctx.Err() != nil {
			return &acceptStop{verdict: "ABSTAIN", reason: "timeout"}
		}
		return g.toolchain(fmt.Errorf("mutate could not run: %v", err))
	}
	g.red = res.Red
	for _, green := range res.Greens {
		if g.written[green.Name] {
			// A test the card wrote that is green without the change proves nothing
			// about the change (SPEC-REVIEW rule, `docs/SPEC-REVIEW.md:653-654`). A
			// pre-existing test that stays green was never about this fix and is
			// not charged.
			return &acceptStop{verdict: "REJECT", reason: "vacuous-test", at: green.Name}
		}
	}
	if !res.Pass {
		at := "-"
		if len(res.Skips) > 0 {
			at = res.Skips[0].File
		} else if len(res.Greens) > 0 {
			at = res.Greens[0].Name
		}
		return &acceptStop{verdict: "REJECT", reason: "vacuous-test", at: at}
	}
	for _, red := range res.Reds {
		if red.Name == g.h.TestName {
			return nil
		}
	}
	return &acceptStop{verdict: "REJECT", reason: "named-test-not-red", at: g.h.TestName}
}

// testRun is one `go test` of one package, inside the wall, over the named tests or all.
func (g *acceptGate) testRun(dir, pkg string, names []string) (string, error) {
	args := []string{"test", "-count=1", "-v"}
	if len(names) > 0 {
		quoted := make([]string, 0, len(names))
		for _, n := range names {
			quoted = append(quoted, regexp.QuoteMeta(n))
		}
		args = append(args, "-run", "^("+strings.Join(quoted, "|")+")$")
	}
	args = append(args, acceptPkgArg(pkg))
	return g.wall(dir, "go", args...)
}

func acceptPkgArg(pkg string) string {
	if pkg == "." || pkg == "" {
		return "./"
	}
	return "./" + pkg + "/"
}

// execWalled is the wrap: nova-sandbox's flags, then --, then the command verbatim
// (SPEC-SWARM rule 12: no argument re-parsed, no quote re-interpreted). The slot is read,
// the gate's own directory and the Go build cache are written, the Go roots are read.
func (g *acceptGate) execWalled(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	argv := []string{"--read", g.slot}
	for _, r := range g.reads {
		argv = append(argv, "--read", r)
	}
	argv = append(argv, "--write", g.runDir)
	for _, w := range g.writes {
		argv = append(argv, "--write", w)
	}
	argv = append(argv, "--cwd", dir, "--", name)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, g.sandbox, argv...)
	cmd.Dir = dir
	// The verdict is a property of the range, never of the environment the gate was
	// started in (internal/goenv: a caller's GOFLAGS=-json would hide every result).
	cmd.Env = goenv.Clean(os.Environ())
	return cmd
}

func (g *acceptGate) wall(dir, name string, args ...string) (string, error) {
	out, err := g.execWalled(g.ctx, dir, name, args...).CombinedOutput()
	return string(out), err
}

// testBodiesAt reads every Test function declared directly in one package directory at
// one ref: name -> body, and name -> the file that declares it.
func (g *acceptGate) testBodiesAt(ref, dir string) (map[string]string, map[string]string, error) {
	args := []string{"ls-tree", "-r", "--name-only", ref}
	if dir != "." && dir != "" {
		args = append(args, "--", dir)
	}
	out, err := g.git(g.job, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("could not list %s at %s: %v", dir, sha12(ref), err)
	}
	bodies, files := map[string]string{}, map[string]string{}
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p == "" || !acceptIsTestFile(p) || path.Dir(p) != dir {
			continue
		}
		src, err := g.git(g.job, "show", ref+":"+p)
		if err != nil {
			return nil, nil, fmt.Errorf("could not read %s at %s: %v", p, sha12(ref), err)
		}
		for name, body := range acceptTestBodies(src) {
			bodies[name], files[name] = body, p
		}
	}
	return bodies, files, nil
}

// acceptTestBodies splits a Go source into its Test functions, each body running to the
// next top-level func.
func acceptTestBodies(src string) map[string]string {
	out := map[string]string{}
	for _, m := range acceptTestFunc.FindAllStringSubmatchIndex(src, -1) {
		end := len(src)
		if j := strings.Index(src[m[1]:], "\nfunc "); j >= 0 {
			end = m[1] + j + 1
		}
		// Trailing blank lines belong to the file, not the function: a test whose only
		// difference between base and head is the blank line before a new neighbour
		// was not changed by the card.
		out[src[m[2]:m[3]]] = strings.TrimSpace(src[m[0]:end])
	}
	return out
}

func acceptIsTestFile(p string) bool { return strings.HasSuffix(p, "_test.go") }

// acceptFailed lists the tests a `go test -v` run reported FAIL, in output order,
// each once.
func acceptFailed(out string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := acceptResult.FindStringSubmatch(line)
		if m != nil && m[1] == "FAIL" && !seen[m[2]] {
			seen[m[2]] = true
			names = append(names, m[2])
		}
	}
	return names
}

func acceptCountResults(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if m := acceptResult.FindStringSubmatch(line); m != nil && strings.HasPrefix(line, "---") {
			n++
		}
	}
	return n
}

// acceptFirstLine is the first line of a command's output that is not a notice: not
// empty, not go's downloading chatter, not a package header, not a test's own PASS.
func acceptFirstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		switch {
		case t == "", strings.HasPrefix(t, "go: downloading"), strings.HasPrefix(t, "go: finding"),
			strings.HasPrefix(t, "go: extracting"), strings.HasPrefix(t, "# "), strings.HasPrefix(t, "=== "),
			strings.HasPrefix(t, "--- PASS"), t == "PASS", strings.HasPrefix(t, "ok "), strings.HasPrefix(t, "?"):
			continue
		}
		return oneline.Cap(t, oneline.TailBytes)
	}
	return "-"
}

// acceptToolchainRed: the red is the bench's when the command could not be started at
// all, or when its first non-notice line is the wall's refusal, a missing toolchain or a
// full disk.
func acceptToolchainRed(out string, err error) bool {
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return true
	}
	return acceptWallMark.MatchString(acceptFirstLine(out))
}

func (g *acceptGate) toolchain(err error) *acceptStop {
	g.note(err.Error())
	return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
}

// acceptReadCert reads the bench's certification record. Until nova-pulse certify (T19)
// writes one, a hand-written record is `bench=<name> legs=<a,b>` and is reported as
// cert=hand; a CERTIFY OK line's cert=<id> is used when present. The record must name
// this bench and every leg the card needs.
func acceptReadCert(path, bench string, legs []string) (id string, why string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Sprintf("no certification record at %s", path)
	}
	fields := map[string]string{}
	for _, tok := range strings.Fields(string(raw)) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			fields[k] = v
		}
	}
	if fields["bench"] != bench {
		return "", fmt.Sprintf("the record names bench %q, not %q", fields["bench"], bench)
	}
	have := map[string]bool{}
	for _, l := range strings.Split(fields["legs"], ",") {
		if l = strings.TrimSpace(l); l != "" {
			have[l] = true
		}
	}
	for _, leg := range legs {
		if !have[leg] {
			return "", fmt.Sprintf("the record certifies no %s leg for bench %s", leg, bench)
		}
	}
	if id = fields["cert"]; id == "" {
		id = "hand"
	}
	return id, ""
}

func (g *acceptGate) git(dir string, args ...string) (string, error) {
	return acceptGitOut(g.ctx, dir, args...)
}

func acceptGitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *acceptGate) note(s string) { g.notes = append(g.notes, s) }

func (g *acceptGate) flushNotes() {
	if len(g.notes) == 0 {
		return
	}
	list := bounded.Capped(g.in.Stdout, g.in.Max, "ACCEPT", "note", "--max <n> raises the ceiling, --max 0 prints every note")
	for _, n := range g.notes {
		list.Line("ACCEPT NOTE " + oneline.Escape(n))
	}
	list.More()
}

func (g *acceptGate) took() string {
	return oneline.Field(g.in.Now().Sub(g.start).Round(time.Millisecond).String())
}

func (g *acceptGate) finish(stop *acceptStop) int {
	if stop == nil {
		return g.ok()
	}
	if stop.verdict == "ABSTAIN" {
		return g.abstain(stop.reason)
	}
	return g.reject(stop.reason, stop.at)
}

func (g *acceptGate) ok() int {
	g.flushNotes()
	fmt.Fprintf(g.in.Stdout, "ACCEPT OK label=%s kind=%s head=%s base=%s tests=%d red_without=%d edits=- control=- bench=%s cert=%s took=%s\n",
		oneline.Field(g.label), oneline.Field(g.kind.Name), sha12(g.head), sha12(g.base), g.tests, g.red,
		oneline.Field(g.in.Bench), oneline.Field(g.certID), g.took())
	return 0
}

func (g *acceptGate) reject(reason, at string) int {
	g.flushNotes()
	head := "-"
	if g.head != "" {
		head = sha12(g.head)
	}
	if at == "" {
		at = "-"
	}
	cert := g.certID
	if cert == "" {
		cert = "-"
	}
	fmt.Fprintf(g.in.Stdout, "ACCEPT REJECT label=%s kind=%s head=%s reason=%s at=%s control=- bench=%s cert=%s took=%s\n",
		oneline.Field(g.label), oneline.Field(g.kind.Name), head, oneline.Field(reason), oneline.Field(at),
		oneline.Field(g.in.Bench), oneline.Field(cert), g.took())
	return 1
}

func (g *acceptGate) abstain(reason string) int {
	g.flushNotes()
	fmt.Fprintf(g.in.Stdout, "ACCEPT ABSTAIN label=%s kind=%s reason=%s bench=%s took=%s\n",
		oneline.Field(g.label), oneline.Field(g.kind.Name), oneline.Field(reason), oneline.Field(g.in.Bench), g.took())
	return 2
}

func (g *acceptGate) refused(reason, remedy string) int {
	fmt.Fprintf(g.in.Stderr, "ACCEPT REFUSED: %s (%s)\n", oneline.Escape(reason), oneline.Escape(remedy))
	return 2
}
