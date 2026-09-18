package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The plan's own tests, against a REAL git and a bare fixture repository in t.TempDir().
// They reach no network: the clone URL comes from Deps.RepoURL, which the lab points at
// its own bare repository, and the only forge read -- each candidate's head and base
// branch -- comes from merge.FakeHost.

// planRepo builds the fixture: a bare remote holding dev, and SIX pull request heads under
// refs/pull/<n>/head.
//
//	#1 changes base/one.go            -- CONFLICTS with #3
//	#2 changes base/two.go            -- CONFLICTS with #4
//	#3 changes base/one.go, same line
//	#4 changes base/two.go, same line
//	#5 adds pkg/e                     -- clean, and #6 is STACKED on it
//	#6 adds pkg/f on top of #5's branch, with base=rowan/pr5
//
// Two conflicting pairs and one stack: the shape the candidate lists really had on
// 2026-09-18, where the gate found one drop per round because it merges in order.
//
// THE CONFLICTING PAIRS ARE NOT NEIGHBOURS IN THE LIST, and that is deliberate. Dealt
// alternately into two halves, #1 and #3 land in the SAME half -- so a plan that split
// them only because it was balancing the two lists would pass a test built on #1 and #2,
// and this fixture fails it.
func planRepo(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("go.mod", "module example.com/plan\n\ngo 1.21\n")
	l.write("base/one.go", "package base\n\nvar One = \"dev\"\n")
	l.write("base/two.go", "package base\n\nvar Two = \"dev\"\n")
	dev := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	heads := map[int]string{}
	branch := map[int]string{}
	one := func(n int, name string, edit func()) {
		l.git(l.work, "checkout", "-q", "-B", name, dev)
		edit()
		heads[n] = l.commit("pr " + strconv.Itoa(n))
		branch[n] = name
	}
	one(1, "rowan/pr1", func() { l.write("base/one.go", "package base\n\nvar One = \"pr1\"\n") })
	one(2, "rowan/pr2", func() { l.write("base/two.go", "package base\n\nvar Two = \"pr2\"\n") })
	one(3, "rowan/pr3", func() { l.write("base/one.go", "package base\n\nvar One = \"pr3\"\n") })
	one(4, "rowan/pr4", func() { l.write("base/two.go", "package base\n\nvar Two = \"pr4\"\n") })
	one(5, "rowan/pr5", func() { l.write("pkg/e/e.go", "package e\n\nfunc E() int { return 5 }\n") })
	// #6 is cut from #5's branch and declares #5's branch as its base, which is what a
	// stacked pull request is on the forge.
	l.git(l.work, "checkout", "-q", "-B", "rowan/pr6", heads[5])
	l.write("pkg/f/f.go", "package f\n\nfunc F() int { return 6 }\n")
	heads[6] = l.commit("pr 6")
	branch[6] = "rowan/pr6"

	for n, sha := range heads {
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	}
	l.git(l.work, "checkout", "-q", "main")
	l.heads = heads
	for n, sha := range heads {
		base := "dev"
		if n == 6 {
			base = "rowan/pr5"
		}
		l.host.PRs[n] = merge.PR{Number: n, HeadOID: sha, HeadRef: branch[n], Base: base}
	}
	return l
}

// THE RUN A CALLER MAKES. Six candidates, two conflicting pairs and one stack, in one
// clone: both pairs are named, both are split across the halves, the stack is whole, and
// nothing is pushed.
func TestBatchPlanNamesEveryConflictingPairAndSplitsThem(t *testing.T) {
	l := planRepo(t)
	before := len(l.pushes())

	exit, stdout, stderr := l.run("batch", "--plan", "--name", "plan-1", "--pr", "1,2,3,4,5,6",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "plan"), "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("a plan that placed members is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH PLAN CONFLICT #1 #3 files=base/one.go")
	contains(t, stdout, "BATCH PLAN CONFLICT #2 #4 files=base/two.go")
	contains(t, stdout, "BATCH PLAN OK halves=2 members=6 conflicts=2 dropped=none")

	halves := planHalves(t, stdout)
	if len(halves) != 2 {
		t.Fatalf("want two halves, got %v", halves)
	}
	for _, pair := range [][2]int{{1, 3}, {2, 4}} {
		if halfOf(halves, pair[0]) == halfOf(halves, pair[1]) {
			t.Errorf("#%d and #%d conflict and share a half: %v", pair[0], pair[1], halves)
		}
	}
	if halfOf(halves, 5) != halfOf(halves, 6) {
		t.Errorf("#6 is based on #5's branch and they are apart: %v", halves)
	}
	if at5, at6 := indexIn(halves, 5), indexIn(halves, 6); at5 > at6 {
		t.Errorf("#6 lands before the #5 it is based on: %v", halves)
	}
	// The stack is said out loud while it runs, so a reader knows why those two travelled
	// together rather than having to infer it from the halves.
	contains(t, stderr, "BATCH PLAN STACK members=5,6")
	contains(t, stderr, "BATCH PLAN START name=plan-1")
	contains(t, stderr, "BATCH PLAN ROW #1 conflicts=#3")

	// IT PUSHES NOTHING AND IT OPENS NOTHING.
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; a plan pushes nothing", got-before)
	}
	absent(t, remoteRefs(l), "rowan/plan-1")
}

// --json is the same answer for the landing child: the halves, the pairs with their files,
// and the reasons, as one document rather than fields split off a line.
func TestBatchPlanJSONCarriesTheHalvesTheConflictsAndTheReasons(t *testing.T) {
	l := planRepo(t)
	exit, stdout, stderr := l.run("batch", "--plan", "--json", "--name", "plan-2", "--pr", "1,2,3,4",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "plan"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	var doc struct {
		Name      string  `json:"name"`
		BaseSHA   string  `json:"base_sha"`
		Halves    [][]int `json:"halves"`
		Conflicts []struct {
			A, B  int
			Files []string
		} `json:"conflicts"`
		Dropped []struct {
			PR  int    `json:"pr"`
			Why string `json:"why"`
		} `json:"dropped"`
		Members    int   `json:"members"`
		MaxMembers int   `json:"max_members"`
		Asked      []int `json:"asked"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("--json did not answer one JSON document: %v\n%s", err, stdout)
	}
	// The line shape is NOT also printed: a child told to read a document that has a
	// second grammar wrapped round it reads neither.
	absent(t, stdout, "BATCH PLAN half=")
	if doc.Name != "plan-2" || doc.Members != 4 || doc.MaxMembers != batchPlanMaxMembers {
		t.Errorf("name=%q members=%d max_members=%d", doc.Name, doc.Members, doc.MaxMembers)
	}
	if len(doc.Halves) != 2 {
		t.Fatalf("halves = %v", doc.Halves)
	}
	if len(doc.Conflicts) != 2 || len(doc.Conflicts[0].Files) == 0 {
		t.Fatalf("conflicts = %+v, want both pairs with the files git named", doc.Conflicts)
	}
	if doc.BaseSHA != l.git(l.work, "rev-parse", "dev") {
		t.Errorf("base_sha = %q, want the base this plan was measured against", doc.BaseSHA)
	}
	if len(doc.Asked) != 4 {
		t.Errorf("asked = %v, want the list as it was given", doc.Asked)
	}
	_ = stderr
}

// A CANDIDATE THAT WILL NOT MERGE ONTO THE BASE AT ALL is dropped before the matrix, with
// the reason and the file, rather than reported as a conflict against every partner in
// turn -- which is the same one fact said five times.
func TestBatchPlanDropsAMemberThatDoesNotMergeOntoTheBase(t *testing.T) {
	l := planRepo(t)
	// Move dev under #1: the base now says what #1 says it should not.
	l.git(l.work, "checkout", "-q", "dev")
	l.write("base/one.go", "package base\n\nvar One = \"moved\"\n")
	l.commit("dev moves under #1 and #3")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	l.git(l.work, "checkout", "-q", "main")

	exit, stdout, stderr := l.run("batch", "--plan", "--name", "plan-3", "--pr", "1,2,5",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "plan"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "dropped=1")
	contains(t, stderr, "BATCH PLAN DROP #1 reason=\"it does not merge onto dev on its own: base/one.go\"")
	absent(t, stdout, "CONFLICT #1")
	contains(t, stdout, "BATCH PLAN OK halves=2 members=2 conflicts=0 dropped=1")
}

// --max-members is the ceiling on ONE half, and a plan that could not place everybody says
// which members it could not place.
func TestBatchPlanHoldsAHalfToMaxMembers(t *testing.T) {
	l := planRepo(t)
	exit, stdout, stderr := l.run("batch", "--plan", "--name", "plan-4", "--pr", "1,2,5",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "plan"), "--base", "dev",
		"--halves", "2", "--max-members", "1", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	for _, half := range planHalves(t, stdout) {
		if len(half) > 1 {
			t.Errorf("a half holds %v, over --max-members 1", half)
		}
	}
	contains(t, stdout, "BATCH PLAN OK halves=2 members=2 conflicts=0 dropped=5")
	contains(t, stderr, "BATCH PLAN DROP #5 reason=\"every half is at --max-members\"")
}

// A flag that does nothing is a flag that lied to whoever typed it: --plan refuses every
// flag of the gate and of the landing by name, and the plan's own three are refused
// without it.
func TestBatchPlanRefusesTheFlagsThatWouldDoNothing(t *testing.T) {
	l := planRepo(t)
	root := filepath.Join(l.dir, "plan")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--plan", "--land"}, "--plan and --land do not go together"},
		{[]string{"--plan", "--require-lisp"}, "--plan and --require-lisp do not go together"},
		{[]string{"--plan", "--gomaxprocs", "4"}, "--plan and --gomaxprocs do not go together"},
		{[]string{"--json"}, "--json belongs to --plan"},
		{[]string{"--halves", "2"}, "--halves belongs to --plan"},
		{[]string{"--max-members", "8"}, "--max-members belongs to --plan"},
		{[]string{"--plan", "--halves", "0"}, "--halves is how many lists"},
		{[]string{"--plan", "--max-members", "0"}, "--max-members is the ceiling on ONE half"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			args := append([]string{"batch", "--name", "plan-x", "--pr", "1", "--repo", "o/n", "--root", root}, c.args...)
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", exit, stdout, stderr)
			}
			contains(t, stderr, c.want)
			absent(t, stdout, "BATCH PLAN")
		})
	}
}

// planHalves reads the `BATCH PLAN half=<n> members=<list>` lines back, which is what a
// caller's own shell does with them.
func planHalves(t *testing.T, stdout string) [][]int {
	t.Helper()
	var out [][]int
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "BATCH PLAN half=") {
			continue
		}
		_, list, ok := strings.Cut(line, " members=")
		if !ok {
			t.Fatalf("no members= on %q", line)
		}
		var half []int
		if strings.TrimSpace(list) != "none" {
			for _, field := range strings.Split(strings.TrimSpace(list), ",") {
				n, err := strconv.Atoi(field)
				if err != nil {
					t.Fatalf("members= holds %q on %q", field, line)
				}
				half = append(half, n)
			}
		}
		out = append(out, half)
	}
	return out
}

func halfOf(halves [][]int, pr int) int {
	for i, half := range halves {
		for _, n := range half {
			if n == pr {
				return i
			}
		}
	}
	return -1
}

// indexIn is a member's position across the halves read in order, so a test can say one
// member is printed before another.
func indexIn(halves [][]int, pr int) int {
	at := 0
	for _, half := range halves {
		for _, n := range half {
			if n == pr {
				return at
			}
			at++
		}
	}
	return -1
}
