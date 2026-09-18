package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// `batch --on <machine>`: the gate on a bench, the forge on this machine.
//
// THE BENCH IS FAKE AND THE WORK IS REAL. fakeBench answers THE SAME SCRIPTS a real bench
// answers -- the text merge.RemoteScript builds, not a paraphrase of it -- by running them
// against /bin/sh on this machine, out of the same bare fixture repository every other test
// here drives. So the clone, the merges in order, the conflict, the build, the vets, the
// test suite and the bundle are all really performed; what is stubbed is the ssh, which is
// the one thing a unit test may not have (Glenn, 2026-09-17: tests never touch the network).

// fakeBench is one machine, reachable without an ssh. It records every script it was asked
// to run, so a test can say WHERE a step happened -- which is the whole claim of this verb.
type fakeBench struct {
	name string
	mu   sync.Mutex
	// scripts is every script this bench was handed, in order.
	scripts []string
	// refuse, when set, answers a script whose text contains its key instead of running it,
	// so a test can make one step of the seam fail without a broken machine.
	refuse map[string]string
}

func newFakeBench(name string) *fakeBench { return &fakeBench{name: name} }

func (b *fakeBench) Name() string { return b.name }

func (b *fakeBench) Exec(script string, _ time.Duration) (string, error) {
	b.mu.Lock()
	b.scripts = append(b.scripts, script)
	refuse := b.refuse
	b.mu.Unlock()
	for needle, answer := range refuse {
		if strings.Contains(script, needle) {
			return answer, nil
		}
	}
	cmd := exec.Command("sh", "-c", script)
	// The machine's own environment, not a cleaned one: the prelude inside the script is
	// what drops GOFLAGS and puts the bench toolchain in front, and a test that cleaned the
	// environment here would be testing something the real seam does not do.
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Get is the same `cat` the real seam does, without the ssh around it.
func (b *fakeBench) Get(remotePath, localPath string, _ time.Duration) error {
	if err := merge.ValidRemotePath(remotePath); err != nil {
		return err
	}
	src, err := os.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

// ran says whether any script this bench was handed holds the text.
func (b *fakeBench) ran(text string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.scripts {
		if strings.Contains(s, text) {
			return true
		}
	}
	return false
}

// benchRegistry writes a machines registry naming one bench, one CI runner host and one
// coordination host. It is the file's real shape -- name, ssh, os/arch, roles, seat, cores,
// notes -- because fleet.ReadRegistry is the reader under test here too.
func benchRegistry(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "machines.tsv")
	body := strings.Join([]string{
		"# the fixture's fleet",
		"bench1\tbench1\tlinux/x64\tbench\tseat-bench1\t64\ta bench",
		"runner1\trunner1\tlinux/x64\trunner\t-\t4\tCI-only",
		"studio\tstudio\tdarwin/arm64\tcoordination\tstudio\t32\tthe window",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// onLab is a batch fixture wired to a fake bench: the registry, the two roots, and the lab.
type onLab struct {
	*lab
	bench     *fakeBench
	machines  string
	root      string // the root ON THE MACHINE
	localRoot string // the root on THIS machine
}

func newOnLab(t *testing.T) *onLab {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell on this machine; every step of a remote gate is one")
	}
	l := batchRepo(t)
	o := &onLab{
		lab:       l,
		bench:     newFakeBench("bench1"),
		machines:  benchRegistry(t, l.dir),
		root:      filepath.Join(l.dir, "bench"),
		localRoot: filepath.Join(l.dir, "here"),
	}
	l.bench = o.bench
	return o
}

// on runs `batch --on bench1` with the fixture's flags and whatever else the test adds.
func (o *onLab) on(name string, extra ...string) (int, string, string) {
	o.t.Helper()
	args := append([]string{"batch", "--on", "bench1", "--machines", o.machines,
		"--name", name, "--repo", "o/n", "--base", "dev", "--timeout", "5m",
		"--root", o.root, "--local-root", o.localRoot}, extra...)
	return o.run(args...)
}

// THE GREEN RUN, AND WHERE EACH HALF OF IT HAPPENED. This is the claim: the clone, the
// merges and every step of the suite ran ON THE MACHINE, the forge was read from HERE, and
// the batch came back as a bundle.
func TestBatchOnRunsTheSuiteOnTheMachineAndKeepsTheForgeHere(t *testing.T) {
	o := newOnLab(t)
	before := len(o.pushes())

	exit, stdout, stderr := o.on("integration-on", "--pr", "1 2")

	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	// THE VERDICT NAMES THE MACHINE. A receipt that did not say which machine judged the
	// tree would be evidence about a run nobody can place -- and `land` reads this line.
	contains(t, stdout, "BATCH OK name=integration-on")
	contains(t, stdout, "members=1 dropped=2")
	contains(t, stdout, "on=bench1")
	absent(t, stdout, "on=local")
	// EVERY PROGRESS LINE CARRIES IT TOO, so a reader watching a run that has said nothing
	// for three minutes knows which machine to go and look at.
	for _, want := range []string{
		"BATCH START name=integration-on",
		"BATCH MERGED #1 ",
		"BATCH DROP #2 ",
		"BATCH STEP build ",
		"BATCH STEP vet-windows ",
		"BATCH STEP test ",
	} {
		contains(t, stderr, want)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "BATCH ") && !strings.Contains(line, "on=bench1") {
			t.Errorf("every progress line of a remote gate carries on=bench1: %q", line)
		}
	}
	// THE WORK REALLY WENT TO THE MACHINE: the clone, the merge of each member and every
	// step of the suite are scripts that bench was handed.
	for _, want := range []string{
		"git clone --quiet",
		// nova-merge's own identity on the merge commit: a machine with no git identity
		// anywhere dies on `git merge --no-ff` with "Committer identity unknown".
		"git -c user.name=nova-merge -c user.email=nova-merge@localhost merge --no-ff --no-edit",
		"go build ./...",
		"GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...",
		strings.Join(ciTestArgs(), " "),
		"git bundle create",
	} {
		if !o.bench.ran(want) {
			t.Errorf("the machine must have run %q; it ran:\n%s", want, strings.Join(o.bench.scripts, "\n\n"))
		}
	}
	// AND THE FORGE NEVER DID. A bench holds no credential and no gh, which is the whole
	// reason this verb exists; a script that reached the forge would be a batch that
	// depended on a bench having one.
	for _, never := range []string{"gh ", "git push", "api.github.com"} {
		if o.bench.ran(never) {
			t.Errorf("the machine must never be asked to %q: the forge half stays here", never)
		}
	}
	// Nothing was pushed at all: `--land` is what pushes, and it was not given.
	if got := len(o.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; a gate without --land pushes nothing", got-before)
	}
}

// THE BATCH COMES BACK AS A GIT BUNDLE, which is what the landing children carried by hand.
// It is brought over on every green gate and not only before a landing: a batch that cannot
// cross the seam is a batch nobody can push, and the run that learns that should be the run
// that built it.
func TestBatchOnBringsTheBatchBackAsABundle(t *testing.T) {
	o := newOnLab(t)

	exit, stdout, stderr := o.on("integration-bundle", "--pr", "1")
	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH BUNDLE name=integration-bundle")
	bundle := filepath.Join(o.localRoot, "integration-bundle", batchBundle)
	info, err := os.Stat(bundle)
	if err != nil {
		t.Fatalf("the bundle must be on THIS machine at %s: %v\n%s", bundle, err, stderr)
	}
	if info.Size() == 0 {
		t.Fatal("the bundle came back empty")
	}
	// IT IS A REAL BUNDLE AND IT HOLDS THE HEAD THE VERDICT NAMED. git reads it here, in a
	// clone of the fixture, which is exactly what the landing does with it.
	clone := filepath.Join(o.dir, "read-the-bundle")
	o.git(o.dir, "clone", "--quiet", o.remote, clone)
	o.git(clone, "fetch", "--quiet", bundle, "refs/heads/rowan/integration-bundle:refs/heads/from-bundle")
	head := o.git(clone, "rev-parse", "refs/heads/from-bundle")
	contains(t, stdout, "head="+head)
}

// THE RED RUN ON A BENCH. #2 conflicts and is dropped by name on the machine, #3's failing
// test turns the batch red there, and the one line a caller parses names the step, the
// package and the test -- out of a `go test -json` stream that crossed the seam.
func TestBatchOnGoesRedOnTheFailingMemberAndNamesTheTest(t *testing.T) {
	o := newOnLab(t)

	exit, stdout, stderr := o.on("integration-on-red", "--pr", "1,2,3")

	if exit != 1 {
		t.Fatalf("a red batch is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL name=integration-on-red")
	contains(t, stdout, "members=1,3")
	contains(t, stdout, "dropped=2")
	contains(t, stdout, "step=test")
	contains(t, stdout, "packages=example.com/batch/pkg/c")
	contains(t, stdout, "tests=TestBroken")
	contains(t, stdout, "on=bench1")
	absent(t, stdout, "BATCH OK")
	// A RED GATE CARRIES NOTHING BACK: there is no bundle, because there is nothing to land.
	contains(t, stderr, "BATCH DROP #2 ")
	absent(t, stderr, "BATCH BUNDLE")
}

// THE LANDING IS THIS MACHINE'S, whatever machine gated the tree. The push comes out of a
// clone built HERE from the bundle the bench sent back, and the bench is never asked to push
// anything -- it holds no credential, which is the whole reason this verb exists.
//
// It is landLab's own script, so the seven steps after BATCH OK are the seven this tool
// already has tests for; what is added here is that they happened on the other side of the
// seam from the gate.
func TestBatchOnLandsFromThisMachineAndNeverFromTheBench(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell on this machine; every step of a remote gate is one")
	}
	s := landLab(t, "integration-8", &landScript{
		ciOK:        []string{"success"},
		queue:       []string{"QUEUED"},
		mergedAfter: 2,
	})
	bench := newFakeBench("bench1")
	s.lab.bench = bench
	machines := benchRegistry(t, s.dir)
	// The gate's own clone is on the MACHINE, under --root, which is where landScript reads
	// the head it scripts the forge's answers about.
	s.root = filepath.Join(s.dir, "bench")
	localRoot := filepath.Join(s.dir, "here")

	exit, stdout, stderr := s.lab.run("batch", "--on", "bench1", "--machines", machines,
		"--name", "integration-8", "--pr", "1,2", "--repo", "o/n", "--base", "dev",
		"--timeout", "5m", "--interval", "30s", "--land",
		"--root", s.root, "--local-root", localRoot)

	if exit != 0 {
		t.Fatalf("a landed batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH OK name=integration-8")
	contains(t, stdout, "on=bench1")
	contains(t, stdout, "BATCH LAND OK name=integration-8")
	// THE BRANCH REACHED THE FORGE FROM HERE. The fixture's bare repository has the ref,
	// and the bench was never asked to push.
	contains(t, remoteRefs(s.lab), "refs/heads/rowan/integration-8")
	contains(t, stderr, "BATCH LAND STEP push branch=rowan/integration-8 ")
	if bench.ran("git push") {
		t.Error("the bench must never push: it holds no credential, which is why this verb exists")
	}
	// The clone the push came out of is the LOCAL one, fed from the bundle.
	local := filepath.Join(localRoot, "integration-8", "repo")
	if _, err := os.Stat(local); err != nil {
		t.Errorf("the landing pushes from a clone on THIS machine at %s: %v", local, err)
	}
	// And the pull request carries the receipt naming the machine that gated the tree.
	if len(s.host.Opened) != 1 {
		t.Fatalf("want one pull request opened, got %v", s.host.Opened)
	}
	contains(t, s.host.Opened[0], "on=bench1")
}

// THE LOCK OF 2026-09-18, reached through this verb: a runner host is CI-only, and a gate
// beside the merge group's shards makes the shards slow, the gate red and the queue stop.
// The check is on the NAME, so a refused machine is never even connected to.
func TestBatchOnRefusesAMachineThatMayNotTakeWork(t *testing.T) {
	o := newOnLab(t)
	for _, c := range []struct{ machine, why string }{
		{"runner1", "runner-host"},
		{"studio", "coordination-host"},
		{"nosuch", "unknown-machine"},
	} {
		exit, stdout, stderr := o.run("batch", "--on", c.machine, "--machines", o.machines,
			"--name", "integration-no", "--pr", "1", "--repo", "o/n", "--base", "dev",
			"--root", o.root, "--local-root", o.localRoot)
		if exit != 2 {
			t.Errorf("--on %s is exit 2, got %d\n%s", c.machine, exit, stderr)
		}
		contains(t, stderr, "BATCH REFUSED")
		contains(t, stderr, c.why)
		absent(t, stdout, "BATCH OK")
		if len(o.bench.scripts) != 0 {
			t.Errorf("a refused machine is never connected to; the bench ran %d scripts", len(o.bench.scripts))
		}
	}
}

// --root IS A PATH ON THE OTHER MACHINE, and the gate REMOVES its own working directory
// there before it rebuilds it. Glenn, 2026-09-17, on exactly that: "it is just one mistake
// away from deleting the whole disk".
func TestBatchOnRefusesARootThatCouldNotSafelyBeRemoved(t *testing.T) {
	o := newOnLab(t)
	for _, bad := range []string{"nova-bench/integration", "~/x", "/a/../../etc", "/a/b;rm -rf ~"} {
		exit, stdout, stderr := o.run("batch", "--on", "bench1", "--machines", o.machines,
			"--name", "integration-no", "--pr", "1", "--repo", "o/n", "--base", "dev",
			"--root", bad, "--local-root", o.localRoot)
		if exit != 2 {
			t.Errorf("--root %q is exit 2, got %d\n%s", bad, exit, stderr)
		}
		contains(t, stderr, "BATCH REFUSED")
		absent(t, stdout, "BATCH OK")
	}
	if len(o.bench.scripts) != 0 {
		t.Errorf("a refused root is never sent; the bench ran %d scripts", len(o.bench.scripts))
	}
}

// A FLAG THAT DOES NOTHING IS A FLAG THAT LIED to whoever typed it: --local-root and
// --machines belong to --on, --on wants a --local-root, and --plan needs no bench.
func TestBatchOnAndItsCompanionsAreRefusedApart(t *testing.T) {
	o := newOnLab(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--local-root", o.localRoot}, "--local-root belongs to --on"},
		{[]string{"--machines", o.machines}, "--machines belongs to --on"},
		{[]string{"--on", "bench1", "--machines", o.machines}, "--local-root is required"},
		{[]string{"--on", "bench1", "--machines", o.machines, "--local-root", o.localRoot, "--plan"}, "--plan and --on do not go together"},
	} {
		args := append([]string{"batch", "--name", "integration-no", "--pr", "1",
			"--repo", "o/n", "--base", "dev", "--root", o.root}, c.args...)
		exit, stdout, stderr := o.run(args...)
		if exit != 2 {
			t.Errorf("%v is exit 2, got %d\n%s", c.args, exit, stderr)
		}
		contains(t, stderr, c.want)
		absent(t, stdout, "BATCH")
	}
}

// EDGE 1 ON THE OTHER MACHINE: the toolchain that is checked is the BENCH's, because the
// bench is what is going to build. It is asked before the first merge, and the refusal names
// the machine and the remedy.
func TestBatchOnChecksTheToolchainOnTheMachineThatBuilds(t *testing.T) {
	o := newOnLab(t)
	// The fixture's base asks for a go nobody has.
	o.git(o.work, "checkout", "-q", "dev")
	o.write("go.mod", "module example.com/batch\n\ngo 99.1\n")
	o.commit("a go nobody has")
	o.git(o.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	o.git(o.work, "checkout", "-q", "main")

	exit, stdout, stderr := o.on("integration-on-tc", "--pr", "1")

	if exit != 2 {
		t.Fatalf("a toolchain the machine has not is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "the go on bench1")
	contains(t, stderr, "go99.1")
	// It refuses BEFORE the merges, so no member was ever merged on that machine.
	absent(t, stderr, "BATCH MERGED")
	absent(t, stdout, "BATCH OK")
}

// WHAT COMES BACK IS CHECKED AT THE SEAM. A `cat` over ssh answers with whatever the far
// side wrote on its stdout -- an empty file, a shell's complaint, half a transfer -- and
// every one of those is a bundle that fails much later with a message about the wrong thing.
func TestBatchOnRefusesSomethingThatIsNotABundle(t *testing.T) {
	o := newOnLab(t)
	// The bench answers the bundle command without making one, so the file that comes back
	// is whatever was there: nothing.
	o.bench.refuse = map[string]string{"git bundle create": ""}

	exit, stdout, stderr := o.on("integration-nobundle", "--pr", "1")

	if exit != 2 {
		t.Fatalf("a bundle that did not come back is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	absent(t, stdout, "BATCH OK")
}
