//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// the sprint binary, built once for the package from this repository.
var (
	sprintOnce sync.Once
	sprintBin  string
	sprintErr  error
)

func builtSprint(t *testing.T) string {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	sprintOnce.Do(func() { sprintBin, sprintErr = build(builtDir, "nova-sprint", "./cmd/nova-sprint") })
	require.NoError(t, sprintErr, "building nova-sprint")
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

// memberDrive is a sprint on a twin store, driven through the nova-sprint binary
// as the coordinator.
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
	} else {
		require.NoError(d.t, err, "nova-sprint %s", strings.Join(args, " "))
	}
	return code, out.String(), errb.String()
}

// worker is a member's sprint as the sprint's server answers it, with no server: each
// verb of a batch runs as the built nova-sprint on the drive's store, as the worker the
// verb names (`<verb> --as <worker>`, `fleet beat <member>`), which is what `nova-sprint run
// --listen` does with it (cmd/nova-sprint serve.go). A twin is one command at a time, so
// the test's ticks and the members' passes take turns on the test's own goroutine.
func (d *memberDrive) worker() *sprintwire.Worker {
	return &sprintwire.Worker{Failed: sprintFailureOutput, Send: func(_ context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
		out := make([]sprintwire.Result, len(verbs))
		for i, argv := range verbs {
			if len(argv) < 3 {
				return nil, fmt.Errorf("not a worker's verb: %q", argv)
			}
			code, o, e := d.sprint(argv[2], argv...)
			out[i] = sprintwire.Result{Code: code, Stdout: o, Stderr: e}
		}
		return out, nil
	}}
}

// must runs a verb that has to succeed.
func (d *memberDrive) must(args ...string) string {
	d.t.Helper()
	code, out, errb := d.sprint("coordinator", args...)
	require.Equal(d.t, 0, code, "nova-sprint %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errb)
	return out
}

func (d *memberDrive) where() sprintWhere {
	d.t.Helper()
	var w sprintWhere
	out := d.must("where", "--json")
	require.NoError(d.t, json.Unmarshal([]byte(out), &w), "where --json\n%s", out)
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
	require.NoError(d.t, json.Unmarshal([]byte(out), &q), "queue --as %s --json\n%s", member, out)
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

// member is one fleet member (a reader with reader) in this process: its verbs go
// through worker, each card it takes is one native child of the built binary under
// harness, in its own root (with the pool identity file native wants), and a work
// card's commit is pushed by the member's own pusher. Its output is returned.
func (d *memberDrive) member(as, harness string, reader bool) (*member.Member, *lockedBuf) {
	d.t.Helper()
	root := filepath.Join(d.t.TempDir(), as)
	require.NoError(d.t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(d.t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	rn := &nativeRunner{self: builtTool, harness: harness, model: "fake/fake-model", root: root, slots: filepath.Join(root, "slots"),
		resultsRoot: filepath.Join(root, "results"), deadline: time.Minute, tokens: "unmetered", noWall: true, stderr: io.Discard}
	var pu member.Pusher
	if !reader {
		pu = newGitPusher(root, rn.slots)
	}
	out := &lockedBuf{}
	d.t.Cleanup(func() {
		if d.t.Failed() {
			d.t.Logf("member %s output:\n%s", as, out.String())
		}
	})
	return member.New(member.Config{As: as, Width: 2, Reader: reader}, d.worker(), rn, pu, out), out
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

// TestMemberFunctionalDriveWithFakeHarness is the member loop against the
// real sprint: one member of width 2, two readers, three cards; every verb is
// the built nova-sprint, as the sprint's server runs it (memberDrive.worker),
// and every card one native child of the built nova-swarm. The member takes the
// work (never more than 2 working at once), pushes it and finishes it with the
// head and report the child's RESULT.md holds; the two readers read every card;
// the coordinator accepts the reads and the merge lands all three.
func TestMemberFunctionalDriveWithFakeHarness(t *testing.T) {
	t.Parallel()
	testMemberFunctionalDrive(t)
}

func testMemberFunctionalDrive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := &memberDrive{t: t, addr: "mem:" + filepath.Join(t.TempDir(), "sprint.twin"), bin: builtSprint(t)}
	harness := filepath.Join(t.TempDir(), "harness.sh")
	require.NoError(t, testbin.WriteExecutable(harness, []byte(fakeHarness), 0o755))

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
	m1, mOut := d.member("m1", harness, false)
	ra, aOut := d.member("reader-a", harness, true)
	rb, bOut := d.member("reader-b", harness, true)

	maxWorking := 0
	var w sprintWhere
	for {
		require.NoError(t, ctx.Err(), "the sprint did not reach 3 done and 6 reads ok in time: %+v\nm1:\n%s\nreader-a:\n%s\nreader-b:\n%s", w, mOut, aOut, bOut)
		d.must("tick")
		for _, m := range []*member.Member{m1, ra, rb} {
			_, err := m.Tick(time.Now())
			require.NoError(t, err, "a member's pass")
		}
		if n := d.working("m1"); n > maxWorking {
			maxWorking = n
		}
		require.LessOrEqual(t, maxWorking, 2, "m1 has %d cards working at once, its width is 2", maxWorking)
		w = d.where()
		if cellInt(w, "fleet", "m1", "done") == 3 && cellInt(w, "readers", "reader-a", "ok")+cellInt(w, "readers", "reader-b", "ok") == 6 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Equal(t, 2, maxWorking, "the most cards m1 had working at once was %d, want 2 (width 2, three ready)", maxWorking)
	// Each card ran once, and every report was taken: a verb refused (exit 1)
	// is a fault of the loop.
	for _, tc := range []struct{ name, out, verb string }{{"m1", "\n" + mOut.String(), "finish"}, {"reader-a", "\n" + aOut.String(), "read"}, {"reader-b", "\n" + bOut.String(), "read"}} {
		if strings.Contains(tc.out, "refused") || strings.Contains(tc.out, "exit=1") {
			t.Errorf("%s: a verb was refused:\n%s", tc.name, tc.out)
		}
		n := strings.Count(tc.out, "\nstart ")
		assert.Equal(t, 3, n, "%s started %d children, want 3 (one a card):\n%s", tc.name, n, tc.out)
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
	got := cellInt(w, "merge", "a", "queued")
	require.Equal(t, 3, got, "after accept --read-ok the merge queue holds %d, want 3: %+v", got, w.Tables["merge"])
	d.must("merge", "--stream", "a", "--batch", "3", "--epoch", fmt.Sprint(w.Epoch))
	d.must("tick") // the landing reaches the work table at the next tick's pump (tla/DirtyTick.tla)
	w = d.where()
	require.Equal(t, int64(3), w.Landed, "landed %d of %d, want 3 of 3: %+v", w.Landed, w.All, w.Tables)
	require.Equal(t, int64(3), w.All, "landed %d of %d, want 3 of 3: %+v", w.Landed, w.All, w.Tables)
}
