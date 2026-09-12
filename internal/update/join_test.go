package update

// The join with the real prepared-send binary. Every other delivery test in this
// package uses a fake bus and proves the CALLER's state machine only; nothing
// there is evidence that a note reached a Git remote, that a refused push
// recovers, or that a child killed mid-write leaves one contribution behind.
// These tests use a disposable bare Git remote and the nova-bus built from this
// same tree, so the thing under test is the pair.
//
// Until #138 lands in this tree, the nova-bus here has no prepared verbs and
// every test in this file skips with that reason named. Nothing is stubbed to
// make them pass: a skip says the gate is unproven, which is the truth.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- the binaries

var (
	joinOnce     sync.Once
	joinDir      string
	joinBuildErr error
	joinPrepared bool
)

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// joinBinaries builds nova-bus and nova-update from THIS tree once per package
// run and reports whether that nova-bus understands the prepared verbs. The
// binaries come from the tree rather than from PATH so that a version somebody
// happens to have installed can never be what a gate was proven against.
func joinBinaries(t *testing.T) string {
	t.Helper()
	joinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-join-")
		if err != nil {
			joinBuildErr = err
			return
		}
		joinDir = dir
		for _, pkg := range []string{"./cmd/nova-bus", "./cmd/nova-update"} {
			build := exec.Command("go", "build", "-o", filepath.Join(dir, exeName(filepath.Base(pkg))), pkg)
			build.Dir = filepath.Join("..", "..")
			if out, err := build.CombinedOutput(); err != nil {
				joinBuildErr = fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
				return
			}
		}
		out, _ := exec.Command(filepath.Join(dir, exeName("nova-bus")), "help").CombinedOutput()
		joinPrepared = bytes.Contains(out, []byte("--prepared-stdin")) && bytes.Contains(out, []byte("prepare --bus"))
	})
	if joinBuildErr != nil {
		t.Fatal(joinBuildErr)
	}
	if !joinPrepared {
		t.Skip("waits on #138: nova-bus in this tree has no prepared verbs (no --prepared-stdin in help)")
	}
	return joinDir
}
func removeJoinBinaries() {
	if joinDir != "" {
		os.RemoveAll(joinDir)
	}
}

// ------------------------------------------------------------------ the fixture

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

type busFixture struct {
	checkout, bare, lane, as, to string
}

// hermeticGit keeps a person's real Git configuration, credentials and hooks out
// of a test that runs Git for real.
func hermeticGit(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Ada\n\temail = ada@example.com\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
}

// realBus builds a bare remote and a checkout of it holding a roster and one
// existing note from another participant, which is the shape a live bus has.
func realBus(t *testing.T) busFixture {
	t.Helper()
	hermeticGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "bus.git")
	git(t, root, "init", "--bare", "--quiet", "--initial-branch=main", bare)
	checkout := filepath.Join(root, "checkout")
	git(t, root, "clone", "--quiet", bare, checkout)
	git(t, checkout, "checkout", "-q", "-B", "main")
	write := func(rel, content string) {
		full := filepath.Join(checkout, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("participants.json", `{"participants":[`+
		`{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},`+
		`{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}],`+
		`"groups":[{"name":"Everybody on the bus","members":["Ada","Bo"]}]}`)
	write("from-bo/2026-09-07T0002Z-heard-111111111111.md",
		"From: Bo\nTo: Ada\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: bo-111111111111\nSubject: Heard\n\nHeard, thank you.\n")
	write("from-bo/INDEX", "bo-111111111111\tfrom-bo/2026-09-07T0002Z-heard-111111111111.md\t2026-09-07T00:02:00Z\tAda\t-\n")
	git(t, checkout, "add", "-A")
	git(t, checkout, "commit", "-q", "-m", "the bus")
	git(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	return busFixture{checkout: checkout, bare: bare, lane: "from-ada", as: "Ada", to: "Bo"}
}

// published reads what actually reached the REMOTE: the notes in the reporter's
// own lane and the lane's INDEX rows. A remote INDEX row on its own is not proof
// of a note, and a note on its own is not a complete contribution, so both are
// counted and the tests assert on the pair.
func (b busFixture) published(t *testing.T) (notes []string, index []string) {
	t.Helper()
	out := git(t, b.bare, "ls-tree", "-r", "--name-only", "main")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, b.lane+"/") && strings.HasSuffix(line, ".md") {
			notes = append(notes, line)
		}
	}
	if strings.Contains(out, b.lane+"/INDEX") {
		for _, line := range strings.Split(strings.TrimSpace(git(t, b.bare, "show", "main:"+b.lane+"/INDEX")), "\n") {
			if strings.TrimSpace(line) != "" {
				index = append(index, line)
			}
		}
	}
	return notes, index
}
func (b busFixture) noteBytes(t *testing.T, path string) string {
	t.Helper()
	return git(t, b.bare, "show", "main:"+path)
}

// exactlyOneContribution is the invariant every recovery case shares: whatever
// died and whenever it died, the remote ends with one note in the reporter's
// lane and one INDEX row for it, and the unrelated lane is untouched.
func (b busFixture) exactlyOneContribution(t *testing.T, id string) string {
	t.Helper()
	notes, index := b.published(t)
	if len(notes) != 1 || len(index) != 1 {
		t.Fatalf("want one note and one INDEX row, got notes=%v index=%v", notes, index)
	}
	if !strings.Contains(index[0], id) {
		t.Fatalf("INDEX row %q does not carry the prepared id %q", index[0], id)
	}
	if !strings.Contains(index[0], notes[0]) {
		t.Fatalf("INDEX row %q does not name the note %q", index[0], notes[0])
	}
	if body := b.noteBytes(t, notes[0]); !strings.Contains(body, "Id: "+id) {
		t.Fatalf("note does not carry the prepared id %q", id)
	}
	other := git(t, b.bare, "show", "main:from-bo/INDEX")
	if !strings.Contains(other, "bo-111111111111") {
		t.Fatal("the other participant's lane did not survive")
	}
	return notes[0]
}

// -------------------------------------------------------------- the reporter

type reporter struct {
	bus      busFixture
	bin      string
	manifest string
	snapshot string
}

func newReporter(t *testing.T, version string) reporter {
	t.Helper()
	bin := joinBinaries(t)
	b := realBus(t)
	dir := t.TempDir()
	m := filepath.Join(dir, "versions.tsv")
	if err := os.WriteFile(m, []byte(Header+"\n"+row("x", "tool", printer(t, version), "npm:unused", "none")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_UPDATE_HELPER", "1")
	return reporter{bus: b, bin: bin, manifest: m, snapshot: filepath.Join(dir, "s.json")}
}
func (r reporter) args() []string {
	return []string{"report", "--file", r.manifest, "--send", "--snapshot", r.snapshot,
		"--as", r.bus.as, "--to", r.bus.to, "--bus", r.bus.checkout,
		"--remote", "origin", "--branch", "main", "--timeout", "60s", "--budget", "120s"}
}

// send runs the caller in process with the named directory first on PATH, which
// is how the real nova-bus -- or a wrapper standing in front of it -- is found.
func (r reporter) send(t *testing.T, pathDir string) (int, string, string) {
	t.Helper()
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return run(t, Environment{}, r.args()...)
}
func (r reporter) pendingID(t *testing.T) string {
	t.Helper()
	s, err := readSnapshot(r.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 1 {
		t.Fatalf("want one pending report, got %d (delivered %d)", len(s.Pending), len(s.Delivered))
	}
	for _, p := range s.Pending {
		if p.ID == "" {
			t.Fatal("pending report retained no identity")
		}
		return p.ID
	}
	return ""
}
func (r reporter) deliveredID(t *testing.T) string {
	t.Helper()
	s, err := readSnapshot(r.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Delivered) != 1 || len(s.Pending) != 0 {
		t.Fatalf("want one confirmed delivery and no pending, got delivered=%d pending=%d", len(s.Delivered), len(s.Pending))
	}
	for _, d := range s.Delivered {
		return d.ID
	}
	return ""
}

// ------------------------------------------------------------------- the cases

// The join at its plainest: a real prepare, a real push, and the two deciding
// facts -- one note plus one INDEX row on the remote, and zero bus invocations
// on the next run of a report that has not changed.
func TestJoinRealBusPublishesOneNoteAndOneIndexRow(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	code, out, errs := r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	id := r.deliveredID(t)
	note := r.bus.exactlyOneContribution(t, id)
	if !strings.Contains(out, "REPORT SENT") {
		t.Fatalf("no REPORT SENT line: %s", out)
	}
	before := git(t, r.bus.bare, "rev-parse", "main")
	code, out, errs = r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "nothing sent") {
		t.Fatalf("a confirmed unchanged report was sent again: %s", out)
	}
	if after := git(t, r.bus.bare, "rev-parse", "main"); after != before {
		t.Fatal("a confirmed unchanged report moved the remote")
	}
	if n, _ := r.bus.published(t); len(n) != 1 || n[0] != note {
		t.Fatalf("the note changed under an unchanged report: %v", n)
	}
}

// A refused push must not cost the report its identity. The remote refuses every
// push while the hook is installed; the caller keeps the prepared artifact; the
// hook comes off and the SAME snapshot is sent again, and exactly one note with
// the original identity lands.
func TestJoinRefusedPushRecoversToOneNoteWithTheSameID(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	refuse := r.bus.hookPath(t, "pre-receive")
	r.bus.writeHook(t, refuse, "#!/bin/sh\necho 'synthetic refusal' >&2\nexit 1\n")
	code, out, errs := r.send(t, r.bin)
	if code != 1 {
		t.Fatalf("a refused push was not reported as a failure: %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(errs, "sent=uncertain") && !strings.Contains(out, "sent=uncertain") {
		t.Fatalf("a refused push was not reported as uncertain:\n%s\n%s", out, errs)
	}
	id := r.pendingID(t)
	if notes, index := r.bus.published(t); len(notes) != 0 || len(index) != 0 {
		t.Fatalf("a refused push published something: %v %v", notes, index)
	}
	if err := os.Remove(refuse); err != nil {
		t.Fatal(err)
	}
	code, out, errs = r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if got := r.deliveredID(t); got != id {
		t.Fatalf("the retry delivered %q, not the prepared %q", got, id)
	}
	r.bus.exactlyOneContribution(t, id)
}

func (b busFixture) hookPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(b.bare, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}
func (b busFixture) writeHook(t *testing.T, path, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell hook on the bare remote is the refusal seam, and it is not portable to Windows")
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------- a real death, really observed
//
// The deaths below are staged by a wrapper that stands in front of the real
// nova-bus on PATH, watches the checkout for the write boundary it was told to
// wait for, and then SIGKILLs the real binary's whole process group. Nothing is
// injected into the production code: there is no kill switch, no test-only
// environment variable read by anything that ships, and the barrier is an
// observation of the bus's own writes rather than a hook inside them.
//
// The wrapper is this test binary under the name nova-bus, the same trick the
// fake bus uses, so no third program has to be built and shipped.

const (
	killBeforeNote = "before-note"      // killed at once, before any write
	killAfterNote  = "after-note"       // killed once the note file exists in the lane
	killAfterIndex = "after-index"      // killed once the lane's INDEX names the id
	killAfterAttrs = "after-attributes" // killed once the merge-attributes change is STAGED
	killAfterCmt   = "after-commit"
	killLostResult = "lost-result" // allowed to finish; its answer is discarded
)

// joinWrapper runs as the nova-bus on PATH. prepare always passes straight
// through, because the caller needs a real artifact; send is where a death is
// staged.
func joinWrapper() {
	real := os.Getenv("NOVA_UPDATE_JOIN_REAL")
	boundary := os.Getenv("NOVA_UPDATE_JOIN_KILL")
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "send" || boundary == "" {
		c := exec.Command(real, args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				os.Exit(e.ExitCode())
			}
			fmt.Fprintln(os.Stderr, "JOIN WRAP could not run the bus")
			os.Exit(2)
		}
		os.Exit(0)
	}
	artifact, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "JOIN WRAP could not read the artifact")
		os.Exit(2)
	}
	var a struct{ ID string }
	_ = json.Unmarshal(artifact, &a)
	checkout := os.Getenv("NOVA_UPDATE_JOIN_CHECKOUT")
	lane := filepath.Join(checkout, os.Getenv("NOVA_UPDATE_JOIN_LANE"))
	baseNotes := laneNotes(lane)
	baseHead := headOf(checkout)
	c := exec.Command(real, args...)
	c.Stdin = bytes.NewReader(artifact)
	// The caller must not see a confirmation it never received. What the bus
	// said is kept for the record, because a staged death whose refusal nobody
	// can read teaches nothing.
	var said bytes.Buffer
	c.Stdout, c.Stderr = &said, &said
	setGroup(c)
	if err := c.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "JOIN WRAP could not start the bus")
		os.Exit(2)
	}
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	reached := func() bool {
		switch boundary {
		case killBeforeNote:
			return true
		case killAfterNote:
			return len(laneNotes(lane)) > len(baseNotes)
		case killAfterIndex:
			b, _ := os.ReadFile(filepath.Join(lane, "INDEX"))
			return a.ID != "" && bytes.Contains(b, []byte(a.ID))
		case killAfterAttrs:
			out, err := exec.Command("git", "-C", checkout, "diff", "--cached", "--name-only").Output()
			return err == nil && bytes.Contains(out, []byte(".gitattributes"))
		case killAfterCmt:
			h := headOf(checkout)
			return h != "" && h != baseHead
		case killLostResult:
			return false
		}
		return false
	}
	observed, alive := false, false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if reached() {
			observed = true
			break
		}
		select {
		case <-done:
			deadline = time.Now() // the bus finished on its own
		default:
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
	default:
		if observed || boundary == killLostResult {
			alive = killGroup(c) == nil
		}
	}
	<-done
	if boundary == killLostResult {
		observed, alive = true, false
	}
	if rec := os.Getenv("NOVA_UPDATE_JOIN_RECORD"); rec != "" {
		_ = os.WriteFile(rec, []byte(fmt.Sprintf("boundary=%s observed=%t killed-alive=%t\nbus said: %s\n", boundary, observed, alive, clip(strings.TrimSpace(said.String()), 2000))), 0600)
	}
	// Whatever happened, this process says nothing a caller could read as a
	// confirmation, which is the whole point of a lost answer.
	fmt.Fprintf(os.Stderr, "JOIN WRAP staged %s\n", boundary)
	os.Exit(1)
}
func laneNotes(lane string) []string {
	m, _ := filepath.Glob(filepath.Join(lane, "*.md"))
	return m
}
func headOf(checkout string) string {
	out, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// wrapperOnPath copies this test binary to <dir>/nova-bus and points it at the
// real one, so the caller's own exec.LookPath finds the wrapper.
func (r reporter) wrapperOnPath(t *testing.T, boundary string) (dir, record string) {
	t.Helper()
	dir = t.TempDir()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, exeName("nova-bus")), self, 0700); err != nil {
		t.Fatal(err)
	}
	record = filepath.Join(dir, "record")
	t.Setenv("NOVA_UPDATE_JOIN_REAL", filepath.Join(r.bin, exeName("nova-bus")))
	t.Setenv("NOVA_UPDATE_JOIN_KILL", boundary)
	t.Setenv("NOVA_UPDATE_JOIN_CHECKOUT", r.bus.checkout)
	t.Setenv("NOVA_UPDATE_JOIN_LANE", r.bus.lane)
	t.Setenv("NOVA_UPDATE_JOIN_RECORD", record)
	return dir, record
}

// clearWrapper takes the staged death back off, so the retry runs against the
// real binary with nothing in front of it.
func clearWrapper(t *testing.T) {
	t.Helper()
	t.Setenv("NOVA_UPDATE_JOIN_KILL", "")
	t.Setenv("NOVA_UPDATE_JOIN_REAL", "")
}

// A child killed at each of the named write boundaries, for real, with SIGKILL.
// The saved artifact has to recover the SAME identity, and each final remote has
// to hold one complete contribution -- one note and one INDEX row -- no matter
// which boundary the death landed on.
func TestJoinChildDeathAtWriteBoundariesRecoversOneContribution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged deaths use SIGKILL on a process group; the Windows join is a separate gate")
	}
	for _, boundary := range []string{killBeforeNote, killAfterNote, killAfterIndex, killAfterAttrs, killAfterCmt, killLostResult} {
		t.Run(boundary, func(t *testing.T) {
			r := newReporter(t, "v1.2.3")
			wrap, record := r.wrapperOnPath(t, boundary)
			code, out, errs := r.send(t, wrap)
			if code != 1 {
				t.Fatalf("a killed bus was not reported as a failure: %d\n%s\n%s", code, out, errs)
			}
			if !strings.Contains(errs+out, "sent=uncertain") {
				t.Fatalf("a killed bus was not reported as uncertain:\n%s\n%s", out, errs)
			}
			id := r.pendingID(t)
			b, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("the wrapper left no record of what it staged: %v", err)
			}
			staged := strings.TrimSpace(string(b))
			if !strings.Contains(staged, "observed=true") {
				t.Fatalf("the boundary was never observed, so no death was staged: %s", staged)
			}
			if boundary != killLostResult && !strings.Contains(staged, "killed-alive=true") {
				t.Skipf("the bus finished before the kill landed, so this run staged a lost answer rather than %s: %s", boundary, staged)
			}
			leftover := r.leftInTheLane(t)
			// A SIGKILL inside git's own index update leaves .git/index.lock
			// behind, and no tool may delete a lock it cannot prove is stale --
			// the bus names it and stops, which is its documented repair. The
			// test performs that named repair, out loud, because the alternative
			// is a gate that measures git's lock discipline instead of recovery.
			// Whether the bus should be allowed to remove a provably stale lock
			// itself is an open question for the spec; see the pull request.
			if repaired := r.removeStaleIndexLock(t); repaired {
				t.Logf("%s: the killed git left .git/index.lock; the test performed the bus's named repair before retrying", boundary)
			}
			clearWrapper(t)
			code, out, errs = r.send(t, r.bin)
			if code != 0 {
				t.Fatalf("recovery failed: %d\n%s\n%s\nstaged: %s\ncheckout: %s\nwhat the kill left: %s\nlocks: %v\nthe bus, asked directly: %s",
					code, out, errs, staged, git(t, r.bus.checkout, "status", "--porcelain"), leftover, r.locks(), r.busAskedDirectly(t))
			}
			if got := r.deliveredID(t); got != id {
				t.Fatalf("recovery delivered %q, not the retained %q", got, id)
			}
			r.bus.exactlyOneContribution(t, id)
			if boundary == killLostResult && !strings.Contains(out, "already-published") {
				t.Fatalf("a lost answer was recovered by publishing again rather than by finding the note: %s", out)
			}
			before := git(t, r.bus.bare, "rev-parse", "main")
			if code, out, errs = r.send(t, r.bin); code != 0 || !strings.Contains(out, "nothing sent") {
				t.Fatalf("the recovered report did not settle: %d\n%s\n%s", code, out, errs)
			}
			if after := git(t, r.bus.bare, "rev-parse", "main"); after != before {
				t.Fatal("a settled report moved the remote again")
			}
		})
	}
}

// leftInTheLane describes what the killed bus left in the checkout, measured
// against the prepared bytes the caller retained. The distinction the contract
// turns on is whether an interrupted write left a PREFIX of the prepared note
// (a partial write of the right note) or different content altogether.
func (r reporter) leftInTheLane(t *testing.T) string {
	t.Helper()
	st, err := readSnapshot(r.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var want string
	for _, p := range st.Pending {
		var a struct{ Note, Path string }
		if err := json.Unmarshal(p.Artifact, &a); err != nil {
			t.Fatal(err)
		}
		want = a.Note
		full := filepath.Join(r.bus.checkout, filepath.FromSlash(a.Path))
		b, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			return "no note at the prepared path"
		}
		if err != nil {
			t.Fatal(err)
		}
		got := string(b)
		switch {
		case got == want:
			return fmt.Sprintf("the whole prepared note, %d bytes, byte for byte; %s", len(want), r.indexShape(t))
		case strings.HasPrefix(want, got):
			return fmt.Sprintf("a PREFIX of the prepared note: %d of %d bytes", len(got), len(want))
		default:
			return fmt.Sprintf("content that is not the prepared note: %d bytes against %d", len(got), len(want))
		}
	}
	return "nothing pending"
}

// locks names any lock file a killed process could have left behind, because a
// retry that refuses forever is usually a lock nobody owns.
func (r reporter) locks() []string {
	var found []string
	for _, pattern := range []string{".git/index.lock", ".git/*.lock", "*.lock", ".nova-bus*"} {
		m, _ := filepath.Glob(filepath.Join(r.bus.checkout, filepath.FromSlash(pattern)))
		found = append(found, m...)
	}
	return found
}

// busAskedDirectly gets the refusal in the bus's OWN words. The caller reports
// only an exit code, which is the smallest thing a person repairing this would
// want to know -- see the diagnostics note in the pull request.
func (r reporter) busAskedDirectly(t *testing.T) string {
	t.Helper()
	st, err := readSnapshot(r.snapshot)
	if err != nil {
		return "no snapshot: " + err.Error()
	}
	for _, p := range st.Pending {
		c := exec.Command(filepath.Join(r.bin, exeName("nova-bus")), "send", "--bus", r.bus.checkout,
			"--remote", "origin", "--branch", "main", "--as", r.bus.as, "--prepared-stdin")
		c.Stdin = bytes.NewReader(p.Artifact)
		out, _ := c.CombinedOutput()
		return clip(strings.TrimSpace(string(out)), 1200)
	}
	return "nothing pending"
}

// removeStaleIndexLock performs the repair the bus's own diagnostic asks a person
// for. It is deliberately the TEST doing this and never the tool.
func (r reporter) removeStaleIndexLock(t *testing.T) bool {
	t.Helper()
	lock := filepath.Join(r.bus.checkout, ".git", "index.lock")
	if _, err := os.Stat(lock); err != nil {
		return false
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	return true
}

// indexShape describes the lane INDEX the killed attempt left: a torn append of
// the tool's own row is a partial write, which the contract says can be
// completed; a row with different content is not.
func (r reporter) indexShape(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.bus.checkout, r.bus.lane, "INDEX"))
	if os.IsNotExist(err) {
		return "no lane INDEX"
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	last := lines[len(lines)-1]
	return fmt.Sprintf("lane INDEX: %d bytes, %d line(s), ends-with-LF=%t, last line has %d tab-separated fields: %q",
		len(text), len(lines), strings.HasSuffix(text, "\n"), len(strings.Split(last, "\t")), clip(last, 200))
}

// ------------------------------------------------ the reporter itself dying
//
// The bus surviving its own death is half the gate. The other half is the
// caller: the snapshot is what carries an identity across processes, so the
// reporter is killed for real at the two moments that matter -- once its
// pending artifact is on disk but nothing is confirmed, and once the note IS on
// the remote but the confirmation has not been recorded. A fresh reporter has to
// finish the same report, and the remote has to end with one contribution.

// killReporterWhen runs the real nova-update as a child and SIGKILLs its process
// group the moment the named condition is observed from outside it.
func (r reporter) killReporterWhen(t *testing.T, pathDir, what string, reached func() bool) {
	t.Helper()
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := exec.Command(filepath.Join(r.bin, exeName("nova-update")), r.args()...)
	c.Stdout, c.Stderr = io.Discard, io.Discard
	setGroup(c)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	deadline := time.Now().Add(60 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		if reached() {
			observed = true
			break
		}
		select {
		case <-done:
			t.Fatalf("the reporter finished before %s could be observed", what)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	if !observed {
		t.Fatalf("%s was never observed", what)
	}
	if err := killGroup(c); err != nil {
		t.Fatalf("could not kill the reporter: %v", err)
	}
	<-done
}
func (r reporter) snapshotHasPending() bool {
	b, err := os.ReadFile(r.snapshot)
	if err != nil {
		return false
	}
	var s struct {
		Pending map[string]struct {
			ID string `json:"id"`
		} `json:"pending"`
	}
	if json.Unmarshal(b, &s) != nil {
		return false
	}
	for _, p := range s.Pending {
		if p.ID != "" {
			return true
		}
	}
	return false
}
func (r reporter) remoteHasANote(t *testing.T) bool {
	out, err := exec.Command("git", "-C", r.bus.bare, "ls-tree", "-r", "--name-only", "main").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, r.bus.lane+"/") && strings.HasSuffix(line, ".md") {
			return true
		}
	}
	return false
}

// Killed once the prepared artifact is on disk and nothing is confirmed: the
// next reporter must finish THAT report rather than prepare a new one.
func TestJoinReporterDeathWithPendingSavedFinishesTheSameReport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged deaths use SIGKILL on a process group; the Windows join is a separate gate")
	}
	r := newReporter(t, "v1.2.3")
	r.killReporterWhen(t, r.bin, "a pending artifact saved to the snapshot", r.snapshotHasPending)
	id := r.pendingID(t)
	// The snapshot lock is kernel-owned, so a killed holder releases it and the
	// leftover sibling file means nothing. This is that clarification, tested.
	if _, err := os.Stat(r.snapshot + ".lock"); err == nil {
		t.Log("the killed reporter left its sibling lock file behind, as expected; only the kernel lock meant ownership")
	}
	code, out, errs := r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("the report did not finish: %d\n%s\n%s", code, out, errs)
	}
	if got := r.deliveredID(t); got != id {
		t.Fatalf("a new identity was prepared: %q, not the saved %q", got, id)
	}
	r.bus.exactlyOneContribution(t, id)
}

// Killed after the note is ON the remote but before the confirmation is
// recorded: the retry must find that same note and must not publish a second.
func TestJoinReporterDeathAfterRemoteConfirmationDoesNotPublishTwice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged deaths use SIGKILL on a process group; the Windows join is a separate gate")
	}
	r := newReporter(t, "v1.2.3")
	r.killReporterWhen(t, r.bin, "the note reaching the remote", func() bool { return r.remoteHasANote(t) })
	notes, _ := r.bus.published(t)
	if len(notes) != 1 {
		t.Fatalf("want the one published note, got %v", notes)
	}
	id := r.pendingID(t)
	if r.removeStaleIndexLock(t) {
		t.Log("the killed reporter's bus child left .git/index.lock; the test performed the bus's named repair before retrying")
	}
	head := git(t, r.bus.bare, "rev-parse", "main")
	code, out, errs := r.send(t, r.bin)
	if code != 0 {
		t.Fatalf("recovery failed: %d\n%s\n%s", code, out, errs)
	}
	if got := r.deliveredID(t); got != id {
		t.Fatalf("recovery confirmed %q, not the pending %q", got, id)
	}
	r.bus.exactlyOneContribution(t, id)
	if after := git(t, r.bus.bare, "rev-parse", "main"); after != head {
		t.Fatalf("recovery published a second time: %s became %s", head, after)
	}
	if !strings.Contains(out, "already-published") {
		t.Fatalf("recovery did not recognise the note it had already published: %s", out)
	}
}
