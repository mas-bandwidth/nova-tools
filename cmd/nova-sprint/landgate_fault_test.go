package main

// The tree gate tells a bench fault from a red test (docs/SPEC-SPRINT.md section 7, the
// gate's two outcomes; the-tree-gate-tells-a-bench-fault-from-a-red-test-bb). Every bench is
// the land loop's gate seam, a fake that answers by host; the clock is the test app's.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Each fault text classifies to its kind, the first line that says it quoted; a FAIL line
// from go test, a build error and a vet finding are a red tree; a bench's failure wins over
// the FAIL line it caused. A fault is the bench's own (marked) only outside a test's FAIL
// output, and git's only from the build or vet; a phrase inside a FAIL block, or git's from
// go test's output, is the run's alone (the class tests print "git <args>: exit status 128:
// ..."; a test may quote "no space left on device"); a bench's own phrase anywhere wins.
func TestEachGateFaultTextClassifiesToItsKind(t *testing.T) {
	t.Parallel()
	faults := []struct {
		name, out, kind, what string
		own                   bool
	}{
		{"git exit 128 in a FAIL block", "GATE RUN: go test ./internal/ci/\n--- FAIL: TestClasses (0.01s)\n    classes_test.go:40: git ls-files from the repository root: exit status 128\nFAIL", bench.FaultGit,
			"classes_test.go:40: git ls-files from the repository root: exit status 128", false},
		{"git exit 128 in go test's output", "GATE RUN: go test ./internal/ci/\ngit ls-tree -r HEAD: exit status 128: fatal: not a tree object\nFAIL\texample.com/m/internal/ci\t0.1s", bench.FaultGit,
			"git ls-tree -r HEAD: exit status 128: fatal: not a tree object", false},
		{"a quoted enospc in a FAIL block", "GATE RUN: go test ./internal/docs/\n--- FAIL: TestGuardSays (0.00s)\n    guard_test.go:9: want \"write /tmp/x: no space left on device\"\nFAIL", bench.FaultTmp,
			`guard_test.go:9: want "write /tmp/x: no space left on device"`, false},
		{"the bench's own after a FAIL block", "GATE RUN: go test ./internal/ci/\n--- FAIL: TestClasses (0.01s)\n    git ls-files: exit status 128\nFAIL\tm/internal/ci\t0.1s\nopen /tmp/go-build9/x.a: disk quota exceeded", bench.FaultTmp,
			"open /tmp/go-build9/x.a: disk quota exceeded", true},
		{"not a git repository", "fatal: not a git repository (or any of the parent directories): .git", bench.FaultGit, "fatal: not a git repository (or any of the parent directories): .git", true},
		{"vcs stamping", "GATE RUN: go build ./...\nerror obtaining VCS status: exit status 128\n\tUse -buildvcs=false to disable VCS stamping.", bench.FaultGit, "error obtaining VCS status: exit status 128", true},
		{"enospc on the root", "GATE RUN: go build ./...\ngo: writing stat cache: write /home/n/nova-bench/cache/go-build/ab: no space left on device", bench.FaultDisk,
			"go: writing stat cache: write /home/n/nova-bench/cache/go-build/ab: no space left on device", true},
		{"ENOSPC", "copy: ENOSPC while writing the tree", bench.FaultDisk, "copy: ENOSPC while writing the tree", true},
		{"quota in tmp", "GATE RUN: go build ./...\nopen /tmp/go-build1234/b001/_pkg_.a: disk quota exceeded", bench.FaultTmp, "open /tmp/go-build1234/b001/_pkg_.a: disk quota exceeded", true},
		{"no go", "GATE RUN: go build ./...\nsh: 1: go: not found", bench.FaultToolchain, "sh: 1: go: not found", true},
		{"go command not found", "bash: go: command not found", bench.FaultToolchain, "bash: go: command not found", true},
		{"toolchain download", "go: downloading go1.26 (linux/amd64)\ngo: download go1.26 for linux/amd64: toolchain not available", bench.FaultToolchain,
			"go: download go1.26 for linux/amd64: toolchain not available", true},
		{"refused stage", "copy refused: vision after 2.0s: staging 0123456789ab exit 3: no mirror", bench.FaultCopy, "copy refused: vision after 2.0s: staging 0123456789ab exit 3: no mirror", true},
	}
	for _, c := range faults {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f, ok := bench.ClassifyGate("vision", 1, c.out)
			require.True(t, ok, "a fault: %q", c.out)
			assert.Equal(t, bench.Fault{Host: "vision", Kind: c.kind, What: c.what, Bench: c.own}, f)
			assert.Equal(t, "GATE FAULT bench=vision kind="+c.kind+" what="+c.what, f.Line())
		})
	}
	f, ok := bench.ClassifyGate("space", bench.NoAnswer, "GATE RUN: go test ./internal/ci/\nConnection to space closed by remote host.")
	require.True(t, ok, "ssh's own exit is the bench's")
	assert.Equal(t, bench.Fault{Host: "space", Kind: bench.FaultSSH, What: "Connection to space closed by remote host.", Bench: true}, f)
	f, ok = bench.ClassifyGate("space", 127, "")
	require.True(t, ok, "a command not found is the bench's")
	assert.Equal(t, bench.FaultToolchain, f.Kind)

	reds := map[string]string{
		"a FAIL line": "GATE RUN: go test ./internal/docs/\n--- FAIL: TestDocsNameEveryVerb (0.00s)\n    docs_test.go:12: land is not documented\nFAIL\nFAIL\texample.com/m/internal/docs\t0.1s",
		"a build":     "GATE RUN: go build ./...\n# example.com/m\n./main.go:3:2: undefined: nope",
		"a vet":       "GATE RUN: go vet ./...\n# example.com/m\n./main.go:5:2: fmt.Printf format %d has arg s of wrong type string",
		"nothing":     "",
	}
	for name, out := range reds {
		_, ok := bench.ClassifyGate("vision", 1, out)
		assert.False(t, ok, "%s is a red tree: %q", name, out)
	}
}

// faultRig is the sprint over the in-memory store with two benches up, vision and space,
// and the gate's seam answering each by its own func; the land loop's flight set so the
// gate goes to the benches, never a socket or a real clock.
type faultRig struct {
	*testApp
	mu     sync.Mutex
	asked  []string
	answer map[string]func() (string, int, error)
	guards []string
}

func newFaultRig(t *testing.T, ta *testApp) *faultRig {
	t.Helper()
	r := &faultRig{testApp: ta, answer: map[string]func() (string, int, error){}}
	for _, m := range []string{"vision", "space"} {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && name != "space" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.flight = &landFlight{}
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		r.mu.Lock()
		r.asked = append(r.asked, host)
		answer := r.answer[host]
		r.mu.Unlock()
		if answer == nil {
			return "", 0, nil
		}
		return answer()
	}
	b.guardBench = func(ctx context.Context, host string) error {
		r.mu.Lock()
		r.guards = append(r.guards, host)
		r.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	return r
}

// ring is the stream's ring over the up benches, in the fleet's order.
func (r *faultRig) ring(stream string) []string {
	r.t.Helper()
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(r.t, err)
	s, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(r.t, err)
	hosts := s.UpMembers()
	require.ElementsMatch(r.t, []string{"vision", "space"}, hosts)
	return benchRing(stream, hosts)
}

// took is the hosts the gate asked since the last call.
func (r *faultRig) took() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := r.asked
	r.asked = nil
	return got
}

// lander is a stream's lander over the rig's store, keyed on stream.
func (r *faultRig) lander(stream string) *lander {
	r.t.Helper()
	st, err := r.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(r.t, err)
	return &lander{a: r.a, c: common{actor: "coordinator"}, st: st, gateKey: stream, laneAs: landLaneWho + "/" + stream,
		baseGateCache: map[string]string{}, baseGateFails: map[string]*baseGateFail{}, cureTried: map[string]bool{}}
}

// moduleDir is a tree the gate gates: one with a go.mod.
func moduleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o600))
	return dir
}

// A fault on the stream's slot steps the base's gate to the next slot, where it runs
// green: the base is not red (cached green, no failure counted), the fault is said as a
// GATE FAULT line, and the bench's disk-guard runs at once. A FAIL line on the same slot is
// a red base, as before.
func TestABenchFaultStepsToTheNextSlotAndNeverMarksTheBaseRed(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	r := newFaultRig(t, ta)
	ring := r.ring("s1")
	bad, good := ring[0], ring[1]
	r.answer[bad] = func() (string, int, error) {
		return "GATE RUN: go build ./...\ngo: writing stat cache: no space left on device", 1, nil
	}
	l := r.lander("s1")
	dir := moduleDir(t)

	why, stop := l.treeGateBase(context.Background(), dir, "1111111111111111111111111111111111111111")
	assert.Empty(t, why, "the next slot's gate is green")
	assert.False(t, stop)
	assert.Equal(t, []string{bad, good}, r.took(), "the fault stepped to the next slot")
	assert.Nil(t, l.deferral, "a slot was left: nothing deferred")
	assert.Equal(t, "", l.baseGateCache["1111111111111111111111111111111111111111"], "the base is green")
	assert.Empty(t, l.baseGateFails, "no failure of the base counted")
	assert.Equal(t, good, l.gateHost, "the gate ran on the next slot")
	require.Len(t, l.gateFaults, 1)
	assert.Equal(t, "GATE FAULT bench="+bad+" kind=disk what=go: writing stat cache: no space left on device", l.gateFaults[0].Line())
	l.keep(landBatch{Stream: "s1", Status: "ok"})
	disk := bench.Fault{Host: bad, Kind: bench.FaultDisk, What: "go: writing stat cache: no space left on device"}
	assert.Equal(t, []string{disk.Line()}, l.out[0].Faults, "the batch carries the fault's line")

	red := r.lander("s2")
	r.answer[bad] = func() (string, int, error) {
		return "GATE RUN: go test ./internal/docs/\n--- FAIL: TestDocs (0.00s)\nFAIL", 1, nil
	}
	r.answer[good] = r.answer[bad]
	why, _ = red.treeGateBase(context.Background(), dir, "2222222222222222222222222222222222222222")
	assert.Contains(t, why, "--- FAIL: TestDocs", "a FAIL line is a red base")
	assert.Nil(t, red.deferral)
	assert.Empty(t, red.gateFaults, "a red tree is no fault")
	require.NotNil(t, red.baseGateFails["2222222222222222222222222222222222222222"], "the red base is counted")
}

// The faulted bench is marked on its fleet row for sprint.BenchFaultFor: where and its JSON
// show it, the ring skips it for that time (the next gates ask only the other slot), and once
// the bound has passed it is asked again.
func TestAFaultedBenchIsSkippedForTheBoundAndShowsOnItsRow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	r := newFaultRig(t, ta)
	ring := r.ring("s1")
	bad, good := ring[0], ring[1]
	r.answer[bad] = func() (string, int, error) {
		return "GATE RUN: go build ./...\nerror obtaining VCS status: exit status 128", 1, nil
	}
	dir := moduleDir(t)
	assert.Empty(t, r.lander("s1").treeGate(context.Background(), dir, true))
	assert.Equal(t, []string{bad, good}, r.took())

	var w whereView
	r.json("where", &w)
	row := w.Tables[sprint.Fleet][bad]
	assert.Equal(t, bench.FaultGit, row[sprint.FieldBenchFault], "where --json shows the fault on its row: %v", row)
	until := r.a.now().UTC().Add(sprint.BenchFaultFor).Truncate(time.Second).Format(time.RFC3339)
	assert.Equal(t, until, row[sprint.FieldBenchFaultUntil])
	assert.NotContains(t, w.Tables[sprint.Fleet][good], sprint.FieldBenchFault, "the other bench is not marked")
	text := r.ok("where")
	assert.Contains(t, text, "bench fault: "+bad+" git until "+until+": error obtaining VCS status: exit status 128")

	for i := 0; i < 2; i++ {
		assert.Empty(t, r.lander("s1").treeGate(context.Background(), dir, true))
		assert.Equal(t, []string{good}, r.took(), "the marked bench is skipped")
	}
	r.mu.Lock()
	assert.Empty(t, r.guards, "a git fault runs no disk-guard")
	r.mu.Unlock()

	r.testApp.mu.Lock()
	r.testApp.now = r.testApp.now.Add(sprint.BenchFaultFor + time.Second)
	r.testApp.mu.Unlock()
	delete(r.answer, bad)
	assert.Empty(t, r.lander("s1").treeGate(context.Background(), dir, true))
	assert.Equal(t, []string{bad}, r.took(), "past the bound the bench is asked again")
	r.json("where", &w)
	assert.NotContains(t, w.Tables[sprint.Fleet][bad], sprint.FieldBenchFault, "the mark has ended")
}

// Every slot faulting defers the landing one tick: LAND DEFERRED with the faults, nothing
// pushed, no card blamed, the stream not stopped, the base not marked red, its cards still
// queued; and the pass raises ONE judgment, however many streams it deferred, and the same
// faults on the next pass raise none.
func TestEverySlotFaultingDefersTheLandingWithOneJudgment(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	r.ok("add --stream s2 --count 1 --one")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{"one.go": "package main\n\nfunc one() {}\n"}),
		"s2-1": r.card("s2-1", map[string]string{"two.go": "package main\n\nfunc two() {}\n"}),
	}
	r.queued(heads, "s1-1", "s2-1")
	f := newFaultRig(t, r.testApp)
	f.answer["vision"] = func() (string, int, error) {
		return "--- FAIL: TestClasses (0.01s)\n    classes_test.go:40: git ls-files from the repository root: exit status 128\nFAIL", 1, nil
	}
	f.answer["space"] = func() (string, int, error) {
		return "open /tmp/go-build99/b001/x.a: disk quota exceeded", 1, nil
	}
	before := r.mainLog()

	code, res := r.landJSON("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, "a deferred landing lands nothing: %+v", res)
	require.Len(t, res.Items, 2, "%+v", res)
	for _, b := range res.Items {
		assert.Equal(t, landDeferred, b.Status, "%+v", b)
		assert.NotEmpty(t, b.Faults, "%+v", b)
		assert.Empty(t, b.Fact, "no card is blamed: %+v", b)
		assert.Contains(t, b.Reason, "every bench faulted: ")
		assert.Contains(t, b.line(), "LAND DEFERRED stream="+b.Stream+" faults=")
	}
	assert.Equal(t, before, r.mainLog(), "nothing pushed")
	assert.Equal(t, "merging", r.streamState("s1"), "the stream is not stopped")
	assert.Equal(t, "merging", r.streamState("s2"))
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s2-1": "merging/queued"}, r.places("s1-1", "s2-1"), "the cards stay queued")
	assert.Empty(t, r.a.baseGateFails, "the base is not marked red")
	f.mu.Lock()
	assert.Contains(t, f.guards, "space", "the tmp fault ran the bench's disk-guard at once")
	f.mu.Unlock()

	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, "every bench faulted: git, tmp"), "one judgment for the pass:\n%s", inbox)

	code, res = r.landJSON("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code)
	for _, b := range res.Items {
		assert.Equal(t, landDeferred, b.Status, "every bench still marked: deferred again: %+v", b)
	}
	inbox = r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, "every bench faulted: git, tmp"), "the same faults raise no second judgment:\n%s", inbox)
	var w whereView
	r.json("where", &w)
	assert.NotContains(t, w.Tables[sprint.Fleet]["vision"], sprint.FieldBenchFault, "git's words inside a test's FAIL output mark no bench")
	assert.Equal(t, bench.FaultTmp, w.Tables[sprint.Fleet]["space"][sprint.FieldBenchFault], "the quota outside any FAIL output is the bench's")
}

// A red test that prints git's exit status 128 inside its FAIL output, on both slots, while
// another stream's tree gates green on the same slots, is the tree's red (the cold read of
// PR 5443, H1): the benches ran gates green this pass, so the fault is the tree's, the head
// is blamed with the finding and the fault's line, no bench is marked, and the other stream
// lands.
func TestAGitLineInATestsFailOutputIsTheTreesRedWhenTheBenchRunsGreen(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	r.ok("add --stream s2 --count 1 --one")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{"red.txt": "the tree whose test fails\n"}),
		"s2-1": r.card("s2-1", map[string]string{"two.go": "package main\n\nfunc two() {}\n"}),
	}
	r.queued(heads, "s1-1", "s2-1")
	f := newFaultRig(t, r.testApp)
	red := "GATE RUN: go test ./internal/docs/\n--- FAIL: TestTreeLs (0.01s)\n    tree_test.go:20: git ls-tree -r HEAD: exit status 128: fatal: not a tree object\nFAIL\nFAIL\texample.com/m/internal/docs\t0.1s"
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		f.mu.Lock()
		f.asked = append(f.asked, host)
		f.mu.Unlock()
		if _, err := os.Stat(filepath.Join(dir, "red.txt")); err == nil {
			return red, 1, nil // s1's tree, on whichever slot
		}
		return "", 0, nil
	}
	b.mu.Unlock()

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --land-parallel 1")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-1 ", "s1 is refused:\n%s", errs)
	assert.Contains(t, errs, " fact=conflict reason=the head "+heads["s1-1"]+" of s1-1 fails the tree gate: GATE FAULT bench=", "the head is blamed:\n%s", errs)
	assert.Contains(t, errs, "git ls-tree -r HEAD: exit status 128", "the finding carries the fault's line:\n%s", errs)
	assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main", "the other stream lands:\n%s%s", out, errs)
	assert.NotContains(t, out+errs, "LAND DEFERRED")
	assert.Equal(t, map[string]string{"s1-1": "ready/returned", "s2-1": "landed/merged"}, r.places("s1-1", "s2-1"))
	assert.Empty(t, r.a.baseGateFails, "the base is not red")
	var w whereView
	r.json("where", &w)
	for _, m := range []string{"vision", "space"} {
		assert.NotContains(t, w.Tables[sprint.Fleet][m], sprint.FieldBenchFault, "%s is not marked", m)
	}
	assert.NotContains(t, r.ok("inbox"), "every bench faulted", "no fault judgment")
}
