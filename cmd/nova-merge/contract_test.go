package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Work list 10: the contract tests. Every exit code, every refusal sentence, the
// structural refusals, the capped listing, and the properties a source test is the only
// way to assert.

// Demanded test 20: init is the ONE creation verb, and every other verb refuses a
// directory that is not a lane -- with the init command in the refusal, and nothing
// written on the way past.
func TestEveryOtherVerbRefusesADirectoryThatIsNotALane(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	empty := filepath.Join(l.dir, "not-a-lane")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "--lane", empty, "--pr", "1"},
		{"add-branch", "--lane", empty, "--branch", "b"},
		{"read", "--lane", empty, "--pr", "1", "--who", "emma", "--head", strings.Repeat("a", 40), "--verdict", "approve"},
		{"run", "--lane", empty, "--once"},
		{"status", "--lane", empty},
		{"dry-run", "--lane", empty},
		{"packet", "--lane", empty, "--who", "emma", "--all"},
		{"stop", "--lane", empty},
	} {
		t.Run(args[0], func(t *testing.T) {
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, "refusing to guess: this is not a lane")
			contains(t, stderr, "nova-merge init --lane")
			if entries, _ := os.ReadDir(empty); len(entries) != 0 {
				t.Errorf("a refusal writes nothing on the way past; the directory holds %v", entries)
			}
		})
	}
}

func TestASecondInitIsRefusedAndTheStateIsByteIdentical(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	before, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := l.run("init", "--lane", l.lane, "--repo", "o/n", "--base", "other", "--lane-branch", "nova-merge/lane")
	if exit != 1 {
		t.Fatalf("an init of a lane that exists is exit 1 (the verb ran and said NO), got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "INIT REFUSED")
	after, _ := os.ReadFile(merge.StatePath(l.lane))
	if string(before) != string(after) {
		t.Error("a refused init leaves the state byte-identical")
	}
}

// Demanded test 20: the lane's three properties are refused off the verb that owns them,
// and --base-sha and --base are never one word.
func TestAFlagIsRefusedOffTheVerbThatOwnsIt(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"--repo on add", []string{"add", "--lane", l.lane, "--pr", "1", "--repo", "o/n"}, "--repo belongs to `init`"},
		{"--base on add", []string{"add", "--lane", l.lane, "--pr", "1", "--base", "main"}, "--base belongs to `init`"},
		{"--lane-branch on run", []string{"run", "--lane", l.lane, "--once", "--lane-branch", "x"}, "--lane-branch belongs to `init`"},
		{"--base on gate", []string{"gate", "--lane", l.lane, "--pr", "1", "--base", sha}, "the base SHA a gate was taken against is --base-sha"},
		{"--base-sha on read", []string{"read", "--lane", l.lane, "--pr", "1", "--base-sha", sha}, "--base-sha names the base a GATE was taken against"},
		{"--base-sha on status", []string{"status", "--lane", l.lane, "--base-sha", sha}, "belongs to `gate`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := l.run(tc.args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, tc.want)
		})
	}
}

func TestAStateFileThisBinaryDoesNotKnowIsRefusedByNumber(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	raw, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(merge.StatePath(l.lane), []byte(strings.Replace(string(raw), `"version": 1`, `"version": 2`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("status", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("exit %d, want 2: %s", exit, stderr)
	}
	contains(t, stderr, "version 2")
	contains(t, stderr, "version 1")
}

// Demanded test 13: every loop ends on its own, and it requires a written deadline.
// Demanded test 16: the loop steps aside for a newer binary at the pass boundary.
func TestTheLoopEndsOnItsOwnAndStepsAsideForANewerBinary(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.SetChecks(l.baseSHA(), 1, 0)
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--loop", "5m", "--hours", "0.25")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if n := strings.Count(stdout, "RUN PASS"); n != 3 {
		t.Errorf("a --loop 5m --hours 0.25 runs its passes and then ENDS BY ITSELF; got %d passes:\n%s", n, stdout)
	}
	contains(t, stdout, "build=aaaaaaaaaaaa")

	// The binary at this tool's own path changes mid-loop.
	l.now = time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	passes := 0
	deps := l.deps()
	deps.BuildID = func() string {
		passes++
		if passes >= 2 {
			return "bbbbbbbbbbbb"
		}
		return "aaaaaaaaaaaa"
	}
	var out, errb strings.Builder
	exit = run([]string{"run", "--lane", l.lane, "--loop", "5m", "--hours", "4"}, &out, &errb, deps)
	if exit != 0 {
		t.Fatalf("a loop that steps aside exits 0, got %d\n%s", exit, errb.String())
	}
	contains(t, out.String(), "RUN NEWER build=aaaaaaaaaaaa on_disk=bbbbbbbbbbbb")
	if n := strings.Count(out.String(), "RUN PASS"); n != 1 {
		t.Errorf("it finishes THAT pass and never runs the old code past the pass boundary; got %d passes", n)
	}
}

// Demanded test 12: fifty entries, measured. The listing is a prefix of 20 with one MORE
// line, RUN NOTE is exactly one line, and the counts say 50.
func TestFiftyEntriesAreACappedPrefixAndTheCountsSayFifty(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.SetChecks(l.baseSHA(), 1, 0)
	for i := 1; i <= 50; i++ {
		n := 900 + i
		l.host.PRs[n] = merge.PR{Number: n, Author: "pat", Base: "main", HeadRef: "b" + strconv.Itoa(n),
			HeadOID: strings.Repeat(fmt.Sprintf("%02x", i%256), 20), Mergeable: "MERGEABLE"}
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", strconv.Itoa(n)); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	for _, verb := range []string{"run", "status"} {
		args := []string{verb, "--lane", l.lane}
		if verb == "run" {
			args = append(args, "--once")
		}
		_, stdout, _ := l.run(args...)
		token := strings.ToUpper(verb)
		if n := strings.Count(stdout, token+" ENTRY"); n != bounded.Default {
			t.Errorf("%s printed %d entry lines, want the ceiling of %d", verb, n, bounded.Default)
		}
		contains(t, stdout, token+" MORE kind=entry shown=20 total=50")
		if n := strings.Count(stdout, "lane=50"); verb == "run" && n != 1 {
			t.Errorf("RUN OK must say the truth about the LANE, not about the output:\n%s", stdout)
		}
		if verb == "status" {
			contains(t, stdout, "prs=50")
		}
		// The bound, measured, and written down: a lane of 50 in lines and bytes.
		t.Logf("%s at 50 entries: %d lines, %d bytes", verb, strings.Count(stdout, "\n"), len(stdout))
		if lines := strings.Count(stdout, "\n"); lines > 26 {
			t.Errorf("%s printed %d lines at 50 entries; the bound is the ceiling plus a handful of summary lines", verb, lines)
		}
	}
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	if n := strings.Count(stdout, "RUN NOTE"); n != 1 {
		t.Errorf("RUN NOTE is exactly one line per pass, got %d", n)
	}
	// 0 means all: a ceiling a caller cannot lift is a tool deciding what its user sees.
	_, stdout, _ = l.run("status", "--lane", l.lane, "--max", "0")
	if n := strings.Count(stdout, "STATUS ENTRY"); n != 50 {
		t.Errorf("--max 0 prints all of them, got %d", n)
	}
	absent(t, stdout, "STATUS MORE")
	exit, _, stderr := l.run("status", "--lane", l.lane, "--max", "-1")
	if exit != 2 {
		t.Errorf("a negative ceiling is a typo with two readings and is refused, got %d", exit)
	}
	contains(t, stderr, "0 means all")
}

// Demanded test 22, the last part: dry-run is a SNAPSHOT. It performs every read run
// performs, prints the whole plan, and leaves state.json and the checkout byte-identical.
func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	_ = oid
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	before, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := l.run("dry-run", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("dry-run reports and exits 0, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "DRY PLAN lane_tip=")
	contains(t, stdout, "DRY PLAN pos=1 entry=951")
	contains(t, stdout, "DRY OK surveyed=1")
	absent(t, stdout, "MERGE OK")
	after, _ := os.ReadFile(merge.StatePath(l.lane))
	if string(before) != string(after) {
		t.Error("dry-run's fold is in memory over the tip it fetched: state.json is byte-identical afterwards")
	}
	// And the checkout's tracked tree is clean: a survey commits nothing.
	if out := l.git(l.lane, "status", "--porcelain"); out != "" {
		t.Errorf("a git status in a lane that is not mid-verb is clean, got:\n%s", out)
	}
}

// Demanded test 11: reads survive a restart AND a lost state, because they were never in
// state.json to lose.
func TestReadsSurviveARestartAndALostState(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	_, stdout, _ := l.run("status", "--lane", l.lane)
	contains(t, stdout, "reads=1a/0h")

	// state.json is deleted. The order goes; nothing else does.
	if err := os.Remove(merge.StatePath(l.lane)); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("status", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("a lane with no state.json is not a lane, got %d: %s", exit, stderr)
	}
	// init on the same branch, and add of the same entry, gives the same counts.
	if exit, _, errb := l.run("init", "--lane", l.lane, "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane"); exit != 0 {
		t.Fatalf("a second lane on the same branch joins it: %s", errb)
	}
	_, stdout, _ = l.run("status", "--lane", l.lane)
	contains(t, stdout, "reads=0a/0h") // no entries yet, so nothing is folded onto one
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "951", "--needs-read"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	_, stdout, _ = l.run("status", "--lane", l.lane)
	contains(t, stdout, "reads=1a/0h")
}

// Demanded test 11, the stale half, and rule 19: a read is keyed to the head the reader
// READ, and a push between the reading and the recording cannot move it.
func TestAStaleApproveIsKeptCountedAndAuthorizesNothing(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	h1 := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", h1, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	// The author pushes H2.
	l.git(l.work, "checkout", "-q", "feature-a")
	l.write("a.txt", "the second try\n")
	h2 := l.commit("a second commit")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/feature-a")
	l.git(l.work, "checkout", "-q", "main")
	pr := l.host.PRs[951]
	pr.HeadOID = h2
	l.host.PRs[951] = pr
	l.host.SetChecks(h2, 3, 0)

	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "read=0a/0h")
	contains(t, stdout, "state=NEEDS-READ")
	_, stdout, _ = l.run("status", "--lane", l.lane)
	contains(t, stdout, "stale=1")

	// READ OK says current=false at once, so a reader who is already stale hears it.
	_, stdout, _ = l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", h1, "--verdict", "approve")
	contains(t, stdout, "current=false")

	// A second read for H2 satisfies it, and the H1 record is still in the branch.
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", h2, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	_, stdout, _ = l.run("status", "--lane", l.lane, "--reads", "951")
	if n := strings.Count(stdout, "STATUS READ entry=951"); n != 3 {
		t.Errorf("the tool deletes no record; want the three files, got %d:\n%s", n, stdout)
	}
	contains(t, stdout, "head="+merge.Short(h1))
	contains(t, stdout, "head="+merge.Short(h2))
}

// A hold blocks, and nothing outvotes it.
func TestAHoldBeatsThreeApproves(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	for _, who := range []string{"emma", "stella", "freddy"} {
		if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", who,
			"--head", oid, "--verdict", "approve"); exit != 0 {
			t.Fatalf("read: %s", errb)
		}
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "johnny",
		"--head", oid, "--verdict", "hold", "--note", "the wire's shape is not what the spec says"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a hold never merges: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "state=HOLD")
	contains(t, stdout, "read=3a/1h")
	absent(t, stdout, "MERGE OK")
	// It is removed by the line that recorded it recording an approve for the same head.
	l.now = l.now.Add(time.Minute)
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "johnny",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	_, stdout, _ = l.run("run", "--lane", l.lane, "--once")
	absent(t, stdout, "state=HOLD")
}
