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
// It runs in its own tree, from its own clone, inside the wall, on a certified bench:
//
//   - the job's .git is read ONCE, by `git clone` and one fetch of its remote refs, into
//     a clone the gate owns under the job's slot; the worker's hooks and its
//     core.fsmonitor live in the worker's .git and config, which a clone does not copy,
//     and the clone has no template, so no hook exists on the gate's side either; every
//     git call the gate, hygiene and mutate make runs in that clone, with
//     core.hooksPath=/dev/null and core.fsmonitor=false besides;
//   - the base is a full sha and never a ref, because a ref resolves through a clone the
//     worker can rewrite (`git branch -f main <evil>` would make the judged range
//     evil..head);
//   - every go command runs through nova-sandbox with the narrowest lists a build needs:
//     the Go roots read, the one tree it works in written (plus the gate's own build
//     cache and home), the network denied, -buildvcs=false; nothing under the swarm
//     root is readable, so an untracked file the worker left in its copy cannot turn a
//     test green;
//   - the tree is removed on every path.
//
// The steps, in order, and the first failure decides:
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
// is run once at the BASE, and red there is the base's (ABSTAIN base-red). The bench
// speaks only through the wall's own exit codes and the lines before the first test
// result (ABSTAIN toolchain); a step that overruns the deadline is ABSTAIN timeout.
//
// What this version does not yet carry, each owed to a later task and said on the line:
// cert=hand until nova-pulse certify (T19) writes a record, and rule 5's staling of that record on a
// toolchain abstain waits for the same; the identity set comes from --identity until
// staging writes identity.tsv (T21); rule 9's reverted= and --seed are nova-review's
// (T01) and are not read here.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// AcceptInput is everything `nova-pulse accept` takes, held apart from flag parsing.
type AcceptInput struct {
	Job   string // the job directory: the worker's clone, whose HEAD is the card's commit
	Card  string // the card file cut wrote
	Base  string // the full sha of the base the range is judged against
	Bench string // the bench this gate runs on
	Cert  string // the bench's certification record
	// Identities is the set a commit's author and committer must be in: the pool's one
	// row at harvest. Empty is a refusal, never a pass.
	Identities []hyg.Identity
	// Sandbox is the nova-sandbox binary; empty looks the tool's own name up on PATH.
	Sandbox string
	// Fixtures is the selftest fixture tree (§1 rule 8): with no passing selftest on
	// file for this control id, accept runs one over these and refuses OK without them.
	Fixtures fs.FS
	// Root is the swarm root that <root>/accept/control/<id> hangs under; empty derives
	// it from the job's slot.
	Root string
	// Build is the gate binary's build identity, part of control=<id>; empty asks the
	// running binary.
	Build   string
	Timeout time.Duration
	Max     int
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time

	// control and noControlCheck are the selftest's own: the inner runs that PUT the
	// control on file are told the id and not to look for it.
	control        string
	noControlCheck bool
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

	job, slot, root, runDir string
	repo, home, gocache     string // the gate-owned clone of the job, and the wall's home and build cache
	wt, baseWt              string
	head, base              string
	certID                  string
	control                 string
	sandbox                 string
	reads                   []string

	changed []acceptChange
	dirs    []string        // touched package directories, sorted
	written map[string]bool // "<pkg>:<Test>" the card added or changed
	notes   []string
	tests   int
	red     int
}

var (
	acceptTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	acceptSkipCall = regexp.MustCompile(`\bt\.(Skip|SkipNow|Skipf)\(`)
	acceptResult   = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): ([A-Za-z_0-9]+)`)
	acceptWallMark = regexp.MustCompile(`SANDBOX REFUSED|SANDBOX DENIED|\bWALL\b|Operation not permitted|[Nn]o space left on device|executable file not found|command not found|cannot find GOROOT|toolchain not available`)
	acceptFullSHA  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// errTimedOut is the wall answering after the gate's deadline: the command was killed,
// and whatever it printed is not a verdict on the card.
var errTimedOut = errors.New("the gate's deadline passed")

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

	// The control id is computed as soon as its three inputs are known, so every line
	// this run prints -- REJECT included -- carries the id an OK would have needed.
	g.control = g.in.control
	if g.control == "" {
		build := g.in.Build
		if build == "" {
			build = buildinfo.Version("")
		}
		digest := "-"
		if g.in.Fixtures != nil {
			if d, err := FixtureDigest(g.in.Fixtures); err == nil {
				digest = d
			}
		}
		g.control = ControlID(build, digest, g.certID)
	}

	job, err := filepath.Abs(g.in.Job)
	if err != nil {
		return g.refused(fmt.Sprintf("--job %s: %v", g.in.Job, err), "pass the job directory")
	}
	if _, err := os.Stat(filepath.Join(job, ".git")); err != nil {
		return g.refused(fmt.Sprintf("--job %s is not a git working copy", g.in.Job), "the job directory is the worker's clone, whose HEAD is the card's commit")
	}
	g.job = job
	// The base is a full sha and nothing else. A ref resolves through the worker's clone,
	// and `git branch -f main <evil>` there would make the judged range evil..head and
	// let a hidden commit through under the card (cold read of f927bccc, HIGH 2).
	if !acceptFullSHA.MatchString(g.in.Base) {
		return g.refused(fmt.Sprintf("--base %s is not a full commit sha", oneline.Field(g.in.Base)), "the base is the forty-hex sha the card was cut against; a ref is a name the worker's clone can move")
	}

	if stop := g.bench(); stop != nil {
		return g.finish(stop)
	}
	if err := g.makeRun(); err != nil {
		g.note(err.Error())
		return g.abstain("toolchain")
	}
	defer g.cleanup()

	// The range is read from the gate's own clone, never from the worker's.
	head, err := g.git(g.repo, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return g.refused("the job's clone has no HEAD commit", "a card ends at a commit; a job with none has nothing to accept")
	}
	base, err := g.git(g.repo, "rev-parse", g.in.Base+"^{commit}")
	if err != nil {
		return g.refused(fmt.Sprintf("--base %s names no commit reachable in the job's clone", sha12(g.in.Base)), "the clone must hold the base the card was cut against")
	}
	if mb, err := g.git(g.repo, "merge-base", base, head); err == nil && mb != "" {
		base = mb
	}
	g.head, g.base = head, base
	if head == base {
		return g.reject("no-test", "-")
	}
	if err := g.addWorktree(); err != nil {
		g.note(err.Error())
		return g.abstain("toolchain")
	}

	for _, step := range []func() *acceptStop{g.hygiene, g.survey, g.shape, g.buildVet, g.weakened, g.headTests, g.gateWeakened, g.mutate} {
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
	// names them itself with --read. The toolchain's own build cache is not used; the
	// gate's is under its root.
	cmd := exec.CommandContext(g.ctx, "go", "env", "GOROOT", "GOMODCACHE")
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		g.note("go env failed: the go toolchain is not on PATH or does not answer")
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 2 {
		g.note("go env answered with other than two roots")
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	for _, r := range lines {
		if r = strings.TrimSpace(r); r != "" {
			if _, err := os.Stat(r); err == nil {
				g.reads = append(g.reads, r)
			}
		}
	}
	return nil
}

// makeRun makes the gate's own directory under the job's slot and, inside it, a clone of
// the job that the gate OWNS: the worker's .git is read once, by `git clone` and one
// `fetch` of its remote refs, and never operated in again. The worker's hooks and its
// core.fsmonitor live in its .git and its config, which a clone does not copy, and the
// clone is made with no template so it has no hooks of its own (cold read of f927bccc,
// HIGH 1). Every later git call, hygiene's and mutate's included, runs in this clone.
func (g *acceptGate) makeRun() error {
	g.slot = filepath.Dir(g.job)
	if filepath.Base(g.slot) == "jobs" {
		g.slot = filepath.Dir(g.slot)
	}
	g.root = g.in.Root
	if g.root == "" {
		g.root = filepath.Dir(g.slot)
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
	g.repo = filepath.Join(run, "repo")
	if _, err := acceptGitOut(g.ctx, run, "clone", "-q", "--no-hardlinks", "--template=", g.job, g.repo); err != nil {
		return fmt.Errorf("could not clone the job: %v", err)
	}
	// The worker's remote refs come too, so a base on the integration branch is
	// reachable; a clone with none of them is still whole for a head that descends
	// from its base, so a fetch that finds nothing is not an error.
	_, _ = acceptGitOut(g.ctx, g.repo, "fetch", "-q", g.job, "+refs/remotes/*:refs/remotes/*")
	g.home = filepath.Join(run, "home")
	if err := os.MkdirAll(g.home, 0o755); err != nil {
		return err
	}
	// The build cache is this run's alone and goes with it: the card's tests may write
	// it (it is in their wall), so one shared across runs would let a card poison the
	// next card's build (cold read 2 of #1721). A cold cache per run is the price.
	g.gocache = filepath.Join(run, "gocache")
	if err := os.MkdirAll(g.gocache, 0o755); err != nil {
		return err
	}
	return nil
}

// addWorktree is the head's tree, from the gate's own clone.
func (g *acceptGate) addWorktree() error {
	wt := filepath.Join(g.runDir, "head")
	if _, err := g.git(g.repo, "worktree", "add", "--detach", wt, g.head); err != nil {
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
	if g.repo != "" {
		for _, wt := range []string{g.wt, g.baseWt} {
			if wt != "" {
				_, _ = acceptGitOut(ctx, g.repo, "worktree", "remove", "--force", wt)
			}
		}
		_, _ = acceptGitOut(ctx, g.repo, "worktree", "prune")
	}
	if g.runDir != "" {
		_ = safepath.RemoveUnder(filepath.Join(g.slot, "accept"), g.runDir)
	}
}

// (a) hygiene: the four checks, one package, and the first token in the spec's order
// decides. It reads the gate's clone, never the worker's.
func (g *acceptGate) hygiene() *acceptStop {
	fs, err := hyg.Check(g.ctx, hyg.Options{
		Repo: g.repo, Base: g.base, Head: g.head, Paths: g.h.Paths,
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
// the card wrote or changed (their body at head differs from the base, or they are new),
// keyed by package and name so a same-named test elsewhere is not this one.
func (g *acceptGate) survey() *acceptStop {
	out, err := g.git(g.repo, "diff", "--no-ext-diff", "--no-renames", "--name-status", g.base, g.head)
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
				g.written[d+":"+name] = true
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
		if stop := g.timedOut(err); stop != nil {
			return stop
		}
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err, false) {
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
			src, err := g.git(g.repo, "show", g.base+":"+f)
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
		if stop := g.timedOut(err); stop != nil {
			return stop
		}
		for _, f := range overlay {
			// The head's copy put back before the head's own suite runs; a restore that
			// failed would have (c2) judge the base's file (cold read, LOW 7).
			if _, cerr := g.git(g.wt, "checkout", "--", f); cerr != nil {
				return g.toolchain(fmt.Errorf("could not restore %s after the overlay: %v", f, cerr))
			}
		}
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err, true) {
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
		if stop := g.timedOut(err); stop != nil {
			return stop
		}
		g.tests += acceptCountResults(out)
		if err == nil {
			continue
		}
		if acceptToolchainRed(out, err, true) {
			g.note("test: " + acceptFirstLine(out))
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		failing := acceptFailed(out)
		if len(failing) == 0 {
			return &acceptStop{verdict: "REJECT", reason: "red-at-head", at: acceptFirstLine(out)}
		}
		var untouched []string
		for _, n := range failing {
			if !g.written[d+":"+n] && n != g.h.TestName {
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
		if _, err := g.git(g.repo, "worktree", "add", "--detach", wt, g.base); err != nil {
			return g.toolchain(fmt.Errorf("could not add a worktree of the base %s: %v", sha12(g.base), err))
		}
		g.baseWt = wt
	}
	out, err := g.testRun(g.baseWt, d, names)
	if stop := g.timedOut(err); stop != nil {
		return stop
	}
	if err == nil {
		return nil
	}
	if acceptToolchainRed(out, err, true) {
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
// red. The suites run through the same wall, in worktrees under the gate's directory,
// from the gate's own clone.
func (g *acceptGate) mutate() *acceptStop {
	if !g.kind.Step(StepMutate) {
		return nil
	}
	res, err := review.Mutate(g.ctx, review.MutateOptions{
		Repo: g.repo, Base: g.base, Head: g.head, TempRoot: g.runDir, Exec: g.execWalled,
	})
	if stop := g.timedOut(err); stop != nil {
		return stop
	}
	switch {
	case errors.Is(err, review.ErrNoTestsChanged):
		return &acceptStop{verdict: "REJECT", reason: "no-test", at: "-"}
	case errors.Is(err, review.ErrNoChangeToRevert):
		// Nothing but tests changed: there is no fix the named test could go red
		// without.
		return &acceptStop{verdict: "REJECT", reason: "named-test-not-red", at: g.h.TestName}
	case err != nil:
		return g.toolchain(fmt.Errorf("mutate could not run: %v", err))
	}
	// A suite the wall refused, or that said nothing, proved nothing about the card:
	// the control did not run, and that is the bench's (cold read 2 of #1721, the
	// CRITICAL: such a run was being scored as every unit red).
	for _, s := range res.Skips {
		if strings.Contains(s.Reason, "could not be run") {
			g.note("mutate: " + s.File + ": " + s.Reason)
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		// The revert would not COMPILE. That is not a kill and it is not the bench's
		// either: a test coupled to the fix only by compiling against its new symbol
		// asserts nothing the revert could disprove, and the red team walked the most
		// common vacuous shape there is straight through this control with it (#1807).
		// It is checked before the verdict so a second, genuinely red file cannot carry
		// it through.
		if strings.HasPrefix(s.Reason, review.SkipRevertNoCompile) {
			g.note("mutate: " + s.File + ": " + s.Reason)
			return &acceptStop{verdict: "REJECT", reason: "vacuous-test", at: s.File}
		}
	}
	g.red = res.Red
	for _, green := range res.Greens {
		if g.written[path.Dir(green.File)+":"+green.Name] {
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
// (SPEC-SWARM rule 12: no argument re-parsed, no quote re-interpreted).
//
// The lists are the narrowest that run a Go build (cold read of f927bccc, HIGH 3): READ
// the Go roots and nothing under the swarm root -- a `--read <slot>` admitted the
// worker's copy, and a test reading ../../jobs/<label>/... went green under the real
// wall; WRITE the one tree this command works in, first (the cwd and the temp
// directory default to it), then the gate's own build cache and home -- never one root
// over head, base and mutate trees together, so a head test cannot reach the base tree.
// The network is denied (§1 rule 10). Builds carry -buildvcs=false, so nothing reads the
// tree's .git file.
func (g *acceptGate) execWalled(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	var argv []string
	for _, r := range g.reads {
		argv = append(argv, "--read", r)
	}
	argv = append(argv, "--write", dir, "--write", g.gocache, "--write", g.home, "--net-deny", "--cwd", dir, "--", name)
	argv = append(argv, acceptNoVCS(name, args)...)
	cmd := exec.CommandContext(ctx, g.sandbox, argv...)
	cmd.Dir = dir
	// When the deadline kills the wall, a grandchild it started may still hold the
	// output pipe; without a bound, CombinedOutput would wait for it for as long as it
	// runs, and a step that overran --timeout would overrun it again (cold read of
	// f927bccc, MEDIUM 5: the test measured 36 s against an 8 s budget).
	cmd.WaitDelay = 2 * time.Second
	// The verdict is a property of the range, never of the environment the gate was
	// started in (internal/goenv: a caller's GOFLAGS=-json would hide every result).
	// HOME and GOCACHE are the gate's, inside its write set, as the wall's rule 9 wants.
	cmd.Env = append(acceptEnvForTheWall(goenv.Clean(os.Environ())), "HOME="+g.home, "GOCACHE="+g.gocache)
	return cmd
}

// acceptEnvForTheWall drops GOENV and every secret-shaped name (goenv.IsSecretName,
// the same predicate the native-argv log redacts by -- reused, not copied) before
// an operator's environment reaches a card's tests inside the wall.
func acceptEnvForTheWall(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "GOENV") || goenv.IsSecretName(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// acceptNoVCS puts -buildvcs=false after a go build, vet or test verb that lacks it.
func acceptNoVCS(name string, args []string) []string {
	if name != "go" || len(args) == 0 {
		return args
	}
	switch args[0] {
	case "build", "vet", "test":
	default:
		return args
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-buildvcs") {
			return args
		}
	}
	return append([]string{args[0], "-buildvcs=false"}, args[1:]...)
}

func (g *acceptGate) wall(dir, name string, args ...string) (string, error) {
	out, err := g.execWalled(g.ctx, dir, name, args...).CombinedOutput()
	if g.ctx.Err() != nil {
		return string(out), errTimedOut
	}
	return string(out), err
}

// timedOut is checked after every wall call: a step that overran --timeout is the
// gate's budget speaking, ABSTAIN timeout, never a red charged to the card (cold read
// of f927bccc, MEDIUM 5).
func (g *acceptGate) timedOut(err error) *acceptStop {
	if errors.Is(err, errTimedOut) || g.ctx.Err() != nil {
		return &acceptStop{verdict: "ABSTAIN", reason: "timeout"}
	}
	return nil
}

// testBodiesAt reads every Test function declared directly in one package directory at
// one ref, from the gate's clone: name -> body, and name -> the file that declares it.
func (g *acceptGate) testBodiesAt(ref, dir string) (map[string]string, map[string]string, error) {
	args := []string{"ls-tree", "-r", "--name-only", ref}
	if dir != "." && dir != "" {
		args = append(args, "--", dir)
	}
	out, err := g.git(g.repo, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("could not list %s at %s: %v", dir, sha12(ref), err)
	}
	bodies, files := map[string]string{}, map[string]string{}
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p == "" || !acceptIsTestFile(p) || path.Dir(p) != dir {
			continue
		}
		src, err := g.git(g.repo, "show", ref+":"+p)
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
// next top-level func. Trailing blank lines belong to the file, not the function: a
// test whose only difference between base and head is the blank line before a new
// neighbour was not changed by the card.
func acceptTestBodies(src string) map[string]string {
	out := map[string]string{}
	for _, m := range acceptTestFunc.FindAllStringSubmatchIndex(src, -1) {
		end := len(src)
		if j := strings.Index(src[m[1]:], "\nfunc "); j >= 0 {
			end = m[1] + j + 1
		}
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
// all, or when the wall itself answered (nova-sandbox's exits 125, 126, 127: refused, not
// executed, not found).
//
// It is ALSO the bench's when a wall or toolchain line appears before the first test
// result -- but only for a command whose output the card does not control. cardRuns says
// which kind this is. `go build` and `go vet` print the toolchain's own words, with the
// `# pkg` and `[build failed]` carve-outs that keep a compiler error naming WALL the
// card's (cold read 2 of #1721, item 4). `go test` runs the CARD'S CODE, and a card's
// TestMain or package init prints before any `=== RUN`: the red team got `ABSTAIN
// toolchain` for a card-controlled red by printing "Operation not permitted" from
// TestMain (#1806, PROBE-A), dodging the REJECT, the track-record fail, and staling the
// bench's certification record on the way out. Output a card's process can write is
// never the bench's voice; there, the exit code speaks and nothing else does.
func acceptToolchainRed(out string, err error, cardRuns bool) bool {
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return true
	}
	if ee != nil {
		switch ee.ExitCode() {
		case sandbox.ExitRefused, sandbox.ExitNotExecuted, sandbox.ExitNotFound:
			return true
		}
	}
	prefix := out
	if i := strings.Index(out, "=== RUN"); i >= 0 {
		prefix = out[:i]
	}
	// Compiler text is the card's, whatever words it holds: `undefined: WALL` is a
	// build error naming WALL, not the wall (cold read 2 of #1721, item 4). A package
	// header or a build-failed marker in the prefix means the toolchain ran and spoke
	// about the code; only the exit codes above then speak for the bench.
	for _, line := range strings.Split(prefix, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") || strings.Contains(t, "[build failed]") || strings.Contains(t, "[setup failed]") {
			return false
		}
	}
	if cardRuns {
		return false
	}
	return acceptWallMark.MatchString(acceptFirstLine(prefix))
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

// acceptGitOut runs one git command with no hook and no filesystem monitor, whatever the
// repository's own config says: a hook is a program the repository chose, and the gate
// runs none of them (cold read of f927bccc, HIGH 1).
func acceptGitOut(ctx context.Context, dir string, args ...string) (string, error) {
	argv := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
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
		if abstain := g.requireControl(); abstain != "" {
			return g.abstain(abstain)
		}
		return g.ok()
	}
	if stop.verdict == "ABSTAIN" {
		return g.abstain(stop.reason)
	}
	return g.reject(stop.reason, stop.at)
}

func (g *acceptGate) ok() int {
	g.flushNotes()
	fmt.Fprintf(g.in.Stdout, "ACCEPT OK label=%s kind=%s head=%s base=%s tests=%d red_without=%d edits=- control=%s bench=%s cert=%s took=%s\n",
		oneline.Field(g.label), oneline.Field(g.kind.Name), sha12(g.head), sha12(g.base), g.tests, g.red, g.controlField(),
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
	fmt.Fprintf(g.in.Stdout, "ACCEPT REJECT label=%s kind=%s head=%s reason=%s at=%s control=%s bench=%s cert=%s took=%s\n",
		oneline.Field(g.label), oneline.Field(g.kind.Name), head, oneline.Field(reason), oneline.Field(at),
		g.controlField(),
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

func (g *acceptGate) controlField() string {
	if g.control == "" {
		return "-"
	}
	return g.control
}

// requireControl is §1 rule 8: no ACCEPT OK without a passing selftest for this control
// id on file. With none there and a fixture tree to hand, the gate runs the selftest
// itself, here, and its lines print before the verdict; with none and no fixtures it
// abstains control-stale, because a green nobody saw red first is not a green.
func (g *acceptGate) requireControl() string {
	if g.in.noControlCheck {
		return ""
	}
	if controlOnFile(g.root, g.control) {
		return ""
	}
	if g.in.Fixtures == nil {
		g.note(fmt.Sprintf("no passing selftest on file for control=%s under %s, and no fixtures to run one", g.control, oneline.Field(filepath.Join(g.root, controlDir))))
		return "control-stale"
	}
	code := Selftest(SelftestInput{
		Fixtures: g.in.Fixtures, Root: g.root, Bench: g.in.Bench, Cert: g.in.Cert, Sandbox: g.in.Sandbox,
		Build: g.in.Build, Timeout: g.in.Timeout, Max: g.in.Max, Stdout: g.in.Stdout, Stderr: g.in.Stderr, Now: g.in.Now,
	})
	if code != 0 {
		return "control-red"
	}
	if !controlOnFile(g.root, g.control) {
		// The selftest computed a different id than this run did: the same three
		// inputs should agree, and when they do not the green is not this run's.
		g.note("the selftest passed under another control id; the inputs disagree")
		return "control-stale"
	}
	return ""
}

// gateWeakened is the eligibility rule's 10: a card whose diff touches the gate's own
// sources, seeds or fixtures has the BASE's selftest seeds run against the HEAD's gate.
// The head's nova-pulse is built in the gate's worktree, inside the wall; the base's
// fixture tree is taken from the base commit; the head's binary runs `accept --selftest`
// over it, inside the wall. Any seed that no longer draws its token is gate-weakened.
func (g *acceptGate) gateWeakened() *acceptStop {
	touched := false
	for _, c := range g.changed {
		if TouchesGate(c.path) {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}
	binDir := filepath.Join(g.runDir, "gate-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return g.toolchain(err)
	}
	out, err := g.wall(g.wt, "go", "build", "-o", binDir, "./cmd/nova-pulse")
	if err != nil {
		if acceptToolchainRed(out, err, false) {
			g.note("gate build: " + acceptFirstLine(out))
			return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
		}
		return &acceptStop{verdict: "REJECT", reason: "gate-weakened", at: "build:" + acceptFirstLine(out)}
	}
	bin, err := acceptOneBinary(binDir)
	if err != nil {
		return &acceptStop{verdict: "REJECT", reason: "gate-weakened", at: "build:" + err.Error()}
	}
	baseFx := filepath.Join(g.runDir, "base-fixtures")
	if err := os.MkdirAll(baseFx, 0o755); err != nil {
		return g.toolchain(err)
	}
	const fixturePath = "cmd/nova-pulse/testdata/accept"
	// From the gate's own clone, hook-off, like every other git call (cold read HIGH 1).
	archive := exec.CommandContext(g.ctx, "git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "archive", "--format=tar", g.base, fixturePath)
	archive.Dir = g.repo
	archive.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	tarBytes, err := archive.Output()
	if err != nil {
		g.note("the base carries no " + fixturePath + "; the base's seeds cannot be run against the head's gate")
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	untar := exec.CommandContext(g.ctx, "tar", "-x", "-C", baseFx)
	untar.Stdin = strings.NewReader(string(tarBytes))
	if out, err := untar.CombinedOutput(); err != nil {
		return g.toolchain(fmt.Errorf("could not unpack the base's fixtures: %v: %s", err, acceptFirstLine(string(out))))
	}
	gwRoot := filepath.Join(g.runDir, "gw")
	args := []string{"accept", "--selftest", "--fixtures", filepath.Join(baseFx, filepath.FromSlash(fixturePath)), "--root", gwRoot,
		"--bench", g.in.Bench, "--cert", g.in.Cert, "--sandbox", g.sandbox, "--timeout", fmt.Sprint(int(g.in.Timeout / time.Second))}
	out, err = g.wall(g.wt, bin, args...)
	if err == nil {
		return nil
	}
	// The card BUILT this binary from its own head, so everything it prints is the
	// card's (#1806): only the wall's exit codes speak for the bench here.
	if acceptToolchainRed(out, err, true) {
		g.note("head's gate selftest: " + acceptFirstLine(out))
		return &acceptStop{verdict: "ABSTAIN", reason: "toolchain"}
	}
	at := "-"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ACCEPT SEED ") && strings.HasSuffix(strings.TrimRight(line, "\r"), " WRONG") {
			at = fieldOf(line, "name=")
			break
		}
	}
	g.note("the base's seeds against the head's gate: " + acceptFirstLine(out))
	return &acceptStop{verdict: "REJECT", reason: "gate-weakened", at: at}
}

// acceptOneBinary is the one file `go build -o <dir>` left there.
func acceptOneBinary(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) != 1 {
		return "", fmt.Errorf("go build left %d files, want one binary", len(files))
	}
	return files[0], nil
}
