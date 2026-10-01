//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/redis/go-redis/v9"
)

// the sprint binary, built once for the package from this repository.
var (
	sprintOnce sync.Once
	sprintBin  string
	sprintErr  error
)

func builtSprint(t *testing.T) string {
	t.Helper()
	if err := buildShared(); err != nil {
		t.Fatalf("building the binaries these tests run: %v", err)
	}
	sprintOnce.Do(func() { sprintBin, sprintErr = build(builtDir, "nova-sprint", "./cmd/nova-sprint") })
	if sprintErr != nil {
		t.Fatalf("building nova-sprint: %v", sprintErr)
	}
	return sprintBin
}

// lockedBuf is a buffer a child process writes to while the test reads it.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// sprintWhere is the part of `nova-sprint where --json` this test reads.
type sprintWhere struct {
	Landed int64                                   `json:"landed"`
	All    int64                                   `json:"all"`
	Epoch  uint64                                  `json:"epoch"`
	Tables map[string]map[string]map[string]string `json:"tables"`
}

// memberDrive is a sprint on a throwaway store, driven through the nova-sprint
// binary as the coordinator.
type memberDrive struct {
	t    *testing.T
	addr string
	bin  string
}

// sprint runs one nova-sprint verb as actor and returns its exit, stdout and stderr.
func (d *memberDrive) sprint(actor string, args ...string) (int, string, string) {
	d.t.Helper()
	cmd := exec.Command(d.bin, args...)
	cmd.Env = append(os.Environ(), "NOVA_SPRINT_REDIS="+d.addr, "NOVA_SPRINT_ACTOR="+actor)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		d.t.Fatalf("nova-sprint %s: %v", strings.Join(args, " "), err)
	}
	return code, out.String(), errb.String()
}

// must runs a verb that has to succeed.
func (d *memberDrive) must(args ...string) string {
	d.t.Helper()
	code, out, errb := d.sprint("coordinator", args...)
	if code != 0 {
		d.t.Fatalf("nova-sprint %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errb)
	}
	return out
}

func (d *memberDrive) where() sprintWhere {
	d.t.Helper()
	var w sprintWhere
	out := d.must("where", "--json")
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		d.t.Fatalf("where --json: %v\n%s", err, out)
	}
	return w
}

// working is how many work cards a member has working, by its queue.
func (d *memberDrive) working(member string) int {
	d.t.Helper()
	var q struct {
		Cards []struct {
			Col string `json:"col"`
		} `json:"cards"`
	}
	out := d.must("queue", "--as", member, "--json")
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		d.t.Fatalf("queue --as %s --json: %v\n%s", member, err, out)
	}
	n := 0
	for _, c := range q.Cards {
		if c.Col == "working" {
			n++
		}
	}
	return n
}

func cellInt(w sprintWhere, table, row, col string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(w.Tables[table][row][col]))
	return n
}

// fakeHarness is the shell script a card runs under, in the plain profile's
// frame (docs/SPEC-CARD-CONTRACT.md): it works for a second; a work card
// commits in the staged checkout, and either kind writes RESULT.md in the
// contract's shape in its job directory.
const fakeHarness = `#!/bin/sh
set -e
sleep 1
if ! grep -q '^# JOB: read' JOB.md; then
	(cd repo && echo "$(pwd)" >> f && git commit -q -am "the fake harness's change")
fi
printf 'head: %s\nbranch: %s\nverdict: ok\ngate: -\noutput: -\nreport: checked by the fake harness\n' "$(git -C repo rev-parse HEAD)" "$(git -C repo symbolic-ref --short HEAD)" > RESULT.md
echo "fake harness: wrote RESULT.md in $(pwd)"
`

// startMember starts `nova-swarm member` as a subprocess of the built binary
// with its own root (the pool identity file native wants) and returns its
// output. The process is killed when the test ends.
func (d *memberDrive) startMember(as, harness string, reader bool) *lockedBuf {
	d.t.Helper()
	root := filepath.Join(d.t.TempDir(), as)
	if err := os.MkdirAll(root, 0o755); err != nil {
		d.t.Fatal(err)
	}
	write(d.t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	args := []string{"member", "--as", as, "--width", "2", "--harness", harness, "--model", "fake/fake-model",
		"--root", root, "--tokens", "unmetered", "--deadline", "60s", "--every", "200ms", "--ticks", "1500",
		"--no-wall", "--sprint", d.bin}
	if reader {
		args = append(args, "--reader")
	}
	cmd := exec.Command(builtTool, args...)
	cmd.Env = append(os.Environ(), "NOVA_SPRINT_REDIS="+d.addr)
	out := &lockedBuf{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		d.t.Fatalf("starting member %s: %v", as, err)
	}
	d.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if d.t.Failed() {
			d.t.Logf("member %s output:\n%s", as, out.String())
		}
	})
	return out
}

// memberCard is the brief of the test's cards: a card that passes the card lint
// (`add` holds every brief to it), cut from `nova-swarm template --name card` to the
// fake harness's job: the task in place of the placeholder, the RULES paragraph
// with every rule sentence as the template prints it (swarm.ChildRulesParagraph),
// and the steps the harness stands for.
var memberCard = "RESULT: <label> sha=<sha12>\n" +
	"You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.\n" +
	"Deadline: finish within 5 minutes.\n\n" +
	swarm.ChildRulesParagraph() + "\n" +
	"THE TASK. Check the member loop: do nothing to the tree and write RESULT.md, line 1 of this card, then the head, the branch and the report that the member loop carries to the card.\n\n" +
	"STEP 1. Enter your worktree and read this card.\n" +
	"STEP 2. Write RESULT.md: line 1 is line 1 of this card; under it the head and the report, in under 80 lines."

// TestMemberFunctionalDriveWithFakeHarness is the member loop against
// the real sprint: one member of width 2, two readers, three cards; every
// process is the built binary and the store is a real redis-server in the
// container. The member takes the work (never more than 2 working at once),
// finishes it with the head and report the child's RESULT.md holds; the two
// readers read every card; the coordinator accepts the reads and the merge
// lands all three.
func TestMemberFunctionalDriveWithFakeHarness(t *testing.T) {
	t.Parallel()
	testMemberFunctionalDrive(t)
}

func testMemberFunctionalDrive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	d := &memberDrive{t: t, addr: addr, bin: builtSprint(t)}
	harness := filepath.Join(t.TempDir(), "harness.sh")
	if err := testbin.WriteExecutable(harness, []byte(fakeHarness), 0o755); err != nil {
		t.Fatal(err)
	}

	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	first, rest, _ := strings.Cut(memberCard, "\n")
	d.must("init", "--members", "m1:2", "--readers", "reader-a,reader-b")
	d.must("add", "--stream", "a", "--count", "3", "--brief", first+"\nbase-repo: "+origin+"\nBASE: main\n"+rest)
	d.must("start")
	mOut := d.startMember("m1", harness, false)
	aOut := d.startMember("reader-a", harness, true)
	bOut := d.startMember("reader-b", harness, true)

	maxWorking := 0
	var w sprintWhere
	for {
		if ctx.Err() != nil {
			t.Fatalf("the sprint did not reach 3 done and 6 reads ok in time: %+v\nm1:\n%s\nreader-a:\n%s\nreader-b:\n%s", w, mOut, aOut, bOut)
		}
		d.must("tick")
		if n := d.working("m1"); n > maxWorking {
			maxWorking = n
		}
		if maxWorking > 2 {
			t.Fatalf("m1 has %d cards working at once, its width is 2", maxWorking)
		}
		w = d.where()
		if cellInt(w, "fleet", "m1", "done") == 3 && cellInt(w, "readers", "reader-a", "ok")+cellInt(w, "readers", "reader-b", "ok") == 6 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if maxWorking != 2 {
		t.Errorf("the most cards m1 had working at once was %d, want 2 (width 2, three ready)", maxWorking)
	}
	// Each card ran once, and every report was taken: a verb refused (exit 1)
	// is a fault of the loop; one the store could not answer (exit 2, a read
	// that raced a write) is retried, so the reports that landed are counted.
	for _, tc := range []struct{ name, out, verb string }{{"m1", mOut.String(), "finish"}, {"reader-a", aOut.String(), "read"}, {"reader-b", bOut.String(), "read"}} {
		if strings.Contains(tc.out, "refused") || strings.Contains(tc.out, "exit=1") {
			t.Errorf("%s: a verb was refused:\n%s", tc.name, tc.out)
		}
		if n := strings.Count(tc.out, "\nstart "); n != 3 {
			t.Errorf("%s started %d children, want 3 (one a card):\n%s", tc.name, n, tc.out)
		}
		if n := strings.Count(tc.out, "\n"+tc.verb+" "); n < 3 || strings.Count(tc.out, " exit=0\n") != 3 {
			t.Errorf("%s: want %s reported 3 times with exit=0:\n%s", tc.name, tc.verb, tc.out)
		}
	}
	// The head the member pushed, the branch and the report the child's
	// RESULT.md holds reached the card.
	card := d.must("card", "a-1")
	pushed := strings.TrimSpace(runGit(t, origin, "rev-parse", "refs/heads/sprint/a-1.w1.g1.e0"))
	for _, want := range []string{"head " + pushed, "branch sprint/a-1.w1.g1.e0", "checked by the fake harness"} {
		assert.Contains(t, card, want)
	}

	d.must("accept", "--read-ok")
	w = d.where()
	if got := cellInt(w, "merge", "a", "queued"); got != 3 {
		t.Fatalf("after accept --read-ok the merge queue holds %d, want 3: %+v", got, w.Tables["merge"])
	}
	d.must("merge", "--stream", "a", "--batch", "3", "--epoch", fmt.Sprint(w.Epoch))
	d.must("tick") // the landing reaches the work table at the next tick's pump (tla/DirtyTick.tla)
	w = d.where()
	if w.Landed != 3 || w.All != 3 {
		t.Fatalf("landed %d of %d, want 3 of 3: %+v", w.Landed, w.All, w.Tables)
	}
}
