package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// The tests the amendment of 2026-09-13 demands: 13 to 18 of docs/SPEC-WAKE.md's
// "Tests this spec demands". Test 19 is in probe_test.go.
//
// Every clock is injected and every program the tool starts is a fake on PATH,
// for the reasons main_test.go's header gives.

// forgeArgs is the shape every watch here takes: a state file, a deadline, a
// word and the two clocks the amendment's sources run on.
func watchArgs(state string, rest ...string) []string {
	return append([]string{"watch", "--state", state, "--max", "5s",
		"--on-deadline", "report", "--interval", "5s"}, rest...)
}

// prJSON writes one standing `gh api graphql` answer for a pull request.
func prJSON(t *testing.T, ghDir, owner, repo, number string, updated, head string, comments, reviews, threads int, newest string) {
	t.Helper()
	write(t, filepath.Join(ghDir, fmt.Sprintf("pr-%s-%s-%s.json", owner, repo, number)),
		fmt.Sprintf(`{"data":{"repository":{"pullRequest":{
  "updatedAt":%q,"headRefOid":%q,"url":"https://forge/%s/%s/pull/%s",
  "comments":{"totalCount":%d,"nodes":%s},
  "reviews":{"totalCount":%d,"nodes":%s},
  "reviewThreads":{"totalCount":%d,"nodes":[]}}}}}`,
			updated, head, owner, repo, number, comments, newest, reviews, `[]`, threads))
}

// ---------------------------------------------------------------------------
// 13. Every wait the window has is a source here, so no wait is a poll.

func TestEveryWaitIsASource(t *testing.T) {
	t.Run("one source is enough", func(t *testing.T) {
		_, ghDir := fakes(t)
		write(t, filepath.Join(ghDir, "ref-o-r-x.json"), `{"ref":"refs/heads/x","object":{"sha":"a1"}}`)
		write(t, filepath.Join(ghDir, "runs-deadbeef.json"), `{"total_count":0,"check_runs":[]}`)
		write(t, filepath.Join(ghDir, "status-deadbeef.json"), `{"statuses":[]}`)
		prJSON(t, ghDir, "o", "r", "7", "2026-09-13T10:00:00Z", "aa11", 0, 0, 0, "[]")
		lockPath := filepath.Join(t.TempDir(), "lane.lock")
		write(t, lockPath, "")
		for _, only := range [][]string{
			{"--lock", lockPath},
			{"--ref", "o/r:x", "--forge-interval", "30s"},
			{"--run", "o/r@deadbeef", "--entry-interval", "30s"},
			{"--pr", "o/r#7", "--forge-interval", "30s"},
		} {
			state := filepath.Join(t.TempDir(), "wake.state")
			r := wakeRun(t, watchArgs(state, only...)...)
			if r.exit != 0 {
				t.Errorf("%v alone: exit %d, want 0\n%s", only, r.exit, r.all())
			}
			if !strings.Contains(r.stdout, "WAKE QUIET") {
				t.Errorf("%v alone did not run to its deadline:\n%s", only, r.all())
			}
		}
	})

	t.Run("none of the eight", func(t *testing.T) {
		r := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"))...)
		if r.exit != 2 {
			t.Fatalf("exit %d, want 2:\n%s", r.exit, r.all())
		}
		for _, flag := range []string{"--bus", "--entry", "--reports", "--pr", "--owned-prs", "--run", "--ref", "--lock"} {
			if !strings.Contains(r.stderr, flag) {
				t.Errorf("the refusal does not name %s:\n%s", flag, r.stderr)
			}
		}
	})

	t.Run("the forge clock", func(t *testing.T) {
		_, ghDir := fakes(t)
		write(t, filepath.Join(ghDir, "ref-o-r-x.json"), `{"ref":"refs/heads/x","object":{"sha":"a1"}}`)
		r := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--ref", "o/r:x")...)
		if r.exit != 2 || !strings.Contains(r.stderr, "--forge-interval") {
			t.Errorf("--ref without --forge-interval: exit %d\n%s", r.exit, r.stderr)
		}
		r = wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--ref", "o/r:x", "--forge-interval", "10s")...)
		if r.exit != 2 || !strings.Contains(r.stderr, "30s") {
			t.Errorf("--forge-interval 10s is refused naming the 30s floor: exit %d\n%s", r.exit, r.stderr)
		}
	})

	t.Run("the run floor", func(t *testing.T) {
		_, ghDir := fakes(t)
		write(t, filepath.Join(ghDir, "runs-deadbeef.json"), `{"total_count":0,"check_runs":[]}`)
		write(t, filepath.Join(ghDir, "status-deadbeef.json"), `{"statuses":[]}`)
		r := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--run", "o/r@deadbeef")...)
		if r.exit != 2 || !strings.Contains(r.stderr, "--entry-interval") {
			t.Errorf("--run without --entry-interval: exit %d\n%s", r.exit, r.stderr)
		}
		r = wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--run", "o/r@deadbeef", "--entry-interval", "5s")...)
		if r.exit != 2 || !strings.Contains(r.stderr, "30s") {
			t.Errorf("--run with --entry-interval 5s is refused naming the 30s floor: exit %d\n%s", r.exit, r.stderr)
		}
		entryJSON(t, ghDir, "942", "OPEN", [2]string{"build", "SUCCESS"})
		r = wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--entry", "o/r#942", "--entry-interval", "5s")...)
		if r.exit != 0 {
			t.Errorf("--entry alone keeps the 5s floor: exit %d\n%s", r.exit, r.all())
		}
	})

	t.Run("the flag is --ref", func(t *testing.T) {
		busDir, ghDir := fakes(t)
		_ = busDir
		write(t, filepath.Join(ghDir, "ref-o-r-main.json"), `{"ref":"refs/heads/main","object":{"sha":"a1"}}`)
		checkout := newBusCheckout(t)
		commitAs(t, checkout, "Ada", at.Add(-time.Minute))
		r := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"),
			"--bus", checkout, "--as", "Rowan", "--receipt-max-words", "12",
			"--refresh", "--remote", "origin", "--branch", "main",
			"--ref", "o/r:main", "--forge-interval", "30s")...)
		if r.exit != 0 {
			t.Errorf("--branch is the bus branch and --ref the forge source: exit %d\n%s", r.exit, r.all())
		}
		bad := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--branch", "o/r:x", "--forge-interval", "30s")...)
		if bad.exit != 2 || !strings.Contains(bad.stderr, "--ref") {
			t.Errorf("--branch as a source is a refusal naming --ref: exit %d\n%s", bad.exit, bad.stderr)
		}
		if strings.Contains(bad.stderr, "--refresh") {
			t.Errorf("neither refusal suggests the other:\n%s", bad.stderr)
		}
	})

	t.Run("--to-only", func(t *testing.T) {
		busDir, _ := fakes(t)
		checkout := newBusCheckout(t)
		commitAs(t, checkout, "Ada", at.Add(-time.Minute))
		write(t, filepath.Join(busDir, "out"),
			"INBOX OK\nINBOX NOTE id=cc1 from=Bo addr=cc at=2026-09-11T10:01:00Z path=from-bo/two.md: a copy\n")
		state := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, watchArgs(state, "--bus", checkout, "--as", "Rowan",
			"--receipt-max-words", "12", "--to-only")...)
		if strings.Contains(r.stdout, "WAKE BUS id=cc1") {
			t.Errorf("an addr=cc note is not printed under --to-only:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "cc=1") {
			t.Errorf("the cc note is counted cc=1 on WAKE SOURCE bus:\n%s", r.stdout)
		}
		if strings.Contains(read(t, state), "bus:note:cc1") {
			t.Errorf("a note held back carries no printed= mark:\n%s", read(t, state))
		}
		for _, line := range strings.Split(r.stdout, "\n") {
			if !strings.HasPrefix(line, "WAKE SOURCE bus ") {
				continue
			}
			read, sup, rel, standing := countField(line, "read="), countField(line, "suppressed="),
				countField(line, "relayed="), countField(line, "standing=")
			if read != sup+rel+standing {
				t.Errorf("read=%d is not suppressed=%d + relayed=%d + standing=%d: %s", read, sup, rel, standing, line)
			}
		}

		plain := filepath.Join(t.TempDir(), "wake.state")
		r2 := wakeRun(t, watchArgs(plain, "--bus", checkout, "--as", "Rowan", "--receipt-max-words", "12")...)
		if !strings.Contains(r2.stdout, "WAKE BUS id=cc1") {
			t.Errorf("without the flag the same note is one WAKE BUS line and a change:\n%s", r2.stdout)
		}

		bad := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--bus", checkout, "--as", "Rowan",
			"--receipt-max-words", "12", "--to-only", "--advance-cursor", "--remote", "origin", "--branch", "main")...)
		if bad.exit != 2 {
			t.Errorf("--to-only --advance-cursor is exit 2, got %d:\n%s", bad.exit, bad.all())
		}
	})

	t.Run("the two id files", func(t *testing.T) {
		_, ghDir := fakes(t)
		prJSON(t, ghDir, "o", "r", "7", "2026-09-13T10:00:00Z", "aa11", 0, 0, 0, "[]")
		dir := t.TempDir()
		good := filepath.Join(dir, "mine")
		write(t, good, "\n# a comment\nPRRC_kwDOA\nIC_kwDOB\n")
		r := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--pr", "o/r#7",
			"--forge-interval", "30s", "--not-mine", good)...)
		if r.exit != 0 {
			t.Errorf("a blank line, a # comment and two node ids parse: exit %d\n%s", r.exit, r.all())
		}
		bad := filepath.Join(dir, "bad")
		write(t, bad, "PRRC_kwDOA\nnot an id at all\n")
		r = wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--pr", "o/r#7",
			"--forge-interval", "30s", "--not-mine", bad)...)
		if r.exit != 2 || !strings.Contains(r.stderr, "line 2") || !strings.Contains(r.stderr, bad) {
			t.Errorf("a line that is not a node id is exit 2 naming the file and the line number: exit %d\n%s", r.exit, r.stderr)
		}
		r = wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), "--pr", "o/r#7",
			"--forge-interval", "30s", "--not-mine", filepath.Join(dir, "nothing-here"))...)
		if r.exit != 2 {
			t.Errorf("a --not-mine that cannot be read is exit 2 and never an empty set: exit %d\n%s", r.exit, r.all())
		}
	})

	t.Run("a quiet cold poll of the four new sources", func(t *testing.T) {
		_, ghDir := fakes(t)
		prJSON(t, ghDir, "o", "r", "7", "2026-09-13T10:00:00Z", "aa11", 4, 0, 0,
			`[{"id":"IC_1","author":{"login":"login-a"},"createdAt":"2026-09-13T09:00:00Z","url":"https://forge/c/1"}]`)
		write(t, filepath.Join(ghDir, "runs-head1.json"),
			`{"total_count":5,"check_runs":[{"name":"a","status":"completed","conclusion":"success"},{"name":"b","status":"completed","conclusion":"success"},{"name":"c","status":"completed","conclusion":"success"},{"name":"d","status":"completed","conclusion":"success"},{"name":"e","status":"completed","conclusion":"success"}]}`)
		write(t, filepath.Join(ghDir, "status-head1.json"), `{"statuses":[]}`)
		write(t, filepath.Join(ghDir, "ref-o-r-one.json"), `{"ref":"refs/heads/one","object":{"sha":"a1"}}`)
		write(t, filepath.Join(ghDir, "ref-o-r-two.json"), `{"ref":"refs/heads/two","object":{"sha":"b2"}}`)
		write(t, filepath.Join(ghDir, "ref-o-r-gone.exit"), "1")
		write(t, filepath.Join(ghDir, "ref-o-r-gone.stderr"), "gh: Not Found (HTTP 404)")
		free := filepath.Join(t.TempDir(), "free.lock")
		write(t, free, "")
		held := filepath.Join(t.TempDir(), "held.lock")
		write(t, held, "")
		release := holdLock(t, held)
		defer release()

		source := []string{"--pr", "o/r#7", "--run", "o/r@head1", "--ref", "o/r:one",
			"--ref", "o/r:two", "--ref", "o/r:gone", "--lock", free, "--lock", held,
			"--forge-interval", "30s", "--entry-interval", "30s"}
		cold := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), source...)...)
		for _, kind := range []string{"WAKE PR", "WAKE RUN", "WAKE BRANCH", "WAKE LOCK"} {
			if strings.Contains(cold.stdout, kind) {
				t.Errorf("a cold first poll records and reports nothing; found %s:\n%s", kind, cold.stdout)
			}
		}
		if !strings.Contains(cold.stdout, "cold=true") {
			t.Errorf("cold=true says the run is in this state:\n%s", cold.stdout)
		}
		if !strings.Contains(cold.stdout, "WAKE QUIET") {
			t.Errorf("a cold watch runs to its deadline:\n%s", cold.stdout)
		}
		baseline := wakeRun(t, watchArgs(filepath.Join(t.TempDir(), "s"), append(append([]string{}, source...), "--baseline")...)...)
		for _, kind := range []string{"WAKE PR", "WAKE RUN", "WAKE BRANCH", "WAKE LOCK"} {
			if !strings.Contains(baseline.stdout, kind) {
				t.Errorf("--baseline lists the world once; no %s:\n%s", kind, baseline.stdout)
			}
		}
	})
}

// countField reads a key=<int> field off a line, answering -1 where it is absent.
func countField(line, key string) int {
	for _, tok := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(tok, key); ok {
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// 15. A run is an entry without a state.

func TestARunIsAnEntryWithoutAState(t *testing.T) {
	_, ghDir := fakes(t)
	write(t, filepath.Join(ghDir, "runs-h1.json"),
		`{"total_count":5,"check_runs":[{"name":"a","status":"completed","conclusion":"success"},{"name":"b","status":"completed","conclusion":"success"},{"name":"race","status":"in_progress"},{"name":"d","status":"queued"},{"name":"e","status":"queued"}]}`)
	write(t, filepath.Join(ghDir, "status-h1.json"), `{"statuses":[]}`)
	state := filepath.Join(t.TempDir(), "wake.state")
	args := watchArgs(state, "--run", "o/r@h1", "--entry-interval", "30s")
	first := wakeRun(t, args...)
	if first.exit != 0 {
		t.Fatalf("the cold poll: exit %d\n%s", first.exit, first.all())
	}
	write(t, filepath.Join(ghDir, "runs-h1.json"),
		`{"total_count":5,"check_runs":[{"name":"a","status":"completed","conclusion":"success"},{"name":"b","status":"completed","conclusion":"success"},{"name":"race","status":"completed","conclusion":"failure"},{"name":"d","status":"completed","conclusion":"success"},{"name":"e","status":"completed","conclusion":"success"}]}`)
	second := wakeRun(t, append(append([]string{}, args...), "--final-only")...)
	if !strings.Contains(second.stdout, "WAKE RUN o/r@h1 fail=1 pending=0 pass=4 final=true failing=race") {
		t.Errorf("a head that went final prints one WAKE RUN line:\n%s", second.stdout)
	}

	t.Run("no checks is not final", func(t *testing.T) {
		write(t, filepath.Join(ghDir, "runs-h2.json"), `{"total_count":0,"check_runs":[]}`)
		write(t, filepath.Join(ghDir, "status-h2.json"), `{"statuses":[]}`)
		s := filepath.Join(t.TempDir(), "wake.state")
		wakeRun(t, watchArgs(s, "--run", "o/r@h2", "--entry-interval", "30s")...)
		r := wakeRun(t, watchArgs(s, "--run", "o/r@h2", "--entry-interval", "30s", "--final-only")...)
		if strings.Contains(r.stdout, "final=true") {
			t.Errorf("pending=0 pass=0 fail=0 is not green:\n%s", r.stdout)
		}
	})

	t.Run("101 check runs", func(t *testing.T) {
		var runs []string
		for i := 0; i < 101; i++ {
			runs = append(runs, fmt.Sprintf(`{"name":"c%d","status":"completed","conclusion":"success"}`, i))
		}
		write(t, filepath.Join(ghDir, "runs-h3.json"),
			fmt.Sprintf(`{"total_count":101,"check_runs":[%s]}`, strings.Join(runs, ",")))
		write(t, filepath.Join(ghDir, "status-h3.json"), `{"statuses":[]}`)
		s := filepath.Join(t.TempDir(), "wake.state")
		wakeRun(t, watchArgs(s, "--run", "o/r@h3", "--entry-interval", "30s", "--baseline")...)
		r := wakeRun(t, watchArgs(s, "--run", "o/r@h3", "--entry-interval", "30s")...)
		if strings.Contains(r.stdout, "WAKE RUN o/r@h3 unreadable:") {
			t.Errorf("the same reason on the next poll does not re-wake:\n%s", r.stdout)
		}
		s2 := filepath.Join(t.TempDir(), "wake.state")
		first := wakeRun(t, watchArgs(s2, "--run", "o/r@h3", "--entry-interval", "30s", "--baseline")...)
		if !strings.Contains(first.stdout, "unreadable: more than 100 check runs on this head") {
			t.Errorf("101 check runs is unreadable, visibly:\n%s", first.stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// 16. A branch moving is its two shas.

func TestABranchMovingIsItsTwoShas(t *testing.T) {
	_, ghDir := fakes(t)
	state := filepath.Join(t.TempDir(), "wake.state")
	args := watchArgs(state, "--ref", "o/r:x", "--forge-interval", "30s")
	write(t, filepath.Join(ghDir, "ref-o-r-x.json"), `{"ref":"refs/heads/x","object":{"sha":"a1"}}`)
	wakeRun(t, args...)
	write(t, filepath.Join(ghDir, "ref-o-r-x.json"), `{"ref":"refs/heads/x","object":{"sha":"b2"}}`)
	moved := wakeRun(t, args...)
	if !strings.Contains(moved.stdout, "WAKE BRANCH o/r:x head=b2 was=a1") {
		t.Errorf("a branch moving says both ends:\n%s", moved.stdout)
	}
	write(t, filepath.Join(ghDir, "ref-o-r-x.exit"), "1")
	write(t, filepath.Join(ghDir, "ref-o-r-x.stderr"), "gh: Not Found (HTTP 404)")
	gone := wakeRun(t, args...)
	if !strings.Contains(gone.stdout, "WAKE BRANCH o/r:x head=- was=b2") {
		t.Errorf("a deletion prints head=- was=b2:\n%s", gone.stdout)
	}
	os.Remove(filepath.Join(ghDir, "ref-o-r-x.exit"))
	write(t, filepath.Join(ghDir, "ref-o-r-x.json"), `{"ref":"refs/heads/x","object":{"sha":"a1"}}`)
	back := wakeRun(t, args...)
	if !strings.Contains(back.stdout, "WAKE BRANCH o/r:x head=a1 was=-") {
		t.Errorf("a creation prints was=-:\n%s", back.stdout)
	}

	t.Run("an array answer is unreadable and never absent", func(t *testing.T) {
		s := filepath.Join(t.TempDir(), "wake.state")
		write(t, filepath.Join(ghDir, "ref-o-r-y.json"), `[{"ref":"refs/heads/y","object":{"sha":"c3"}}]`)
		r := wakeRun(t, watchArgs(s, "--ref", "o/r:y", "--forge-interval", "30s", "--baseline")...)
		if !strings.Contains(r.stdout, "WAKE BRANCH o/r:y unreadable:") {
			t.Errorf("an array is unreadable and never absent:\n%s", r.stdout)
		}
	})

	t.Run("one call per branch per forge tick", func(t *testing.T) {
		_, gh2 := fakes(t)
		write(t, filepath.Join(gh2, "ref-o-r-z.json"), `{"ref":"refs/heads/z","object":{"sha":"a1"}}`)
		s := filepath.Join(t.TempDir(), "wake.state")
		wakeRun(t, "watch", "--state", s, "--max", "90s", "--on-deadline", "report",
			"--interval", "5s", "--ref", "o/r:z", "--forge-interval", "30s")
		n := 0
		for _, c := range calls(t, gh2) {
			if strings.Contains(c, "git/ref/heads/z") {
				n++
			}
		}
		if n < 1 || n > 4 {
			t.Errorf("%d calls over 90s at a 30s forge tick, want 3 or so:\n%v", n, calls(t, gh2))
		}
	})
}

// ---------------------------------------------------------------------------
// 17. The lock source probes and never holds.

func TestALockReleasedIsAChangeAndTheProbeNeverHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lane.lock")
	write(t, path, "")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := watchArgs(state, "--lock", path)

	release := holdLock(t, path)
	first := wakeRun(t, append(append([]string{}, args...), "--baseline")...)
	if !strings.Contains(first.stdout, "WAKE LOCK path="+path+" state=held") {
		t.Errorf("a lock the test holds reads held:\n%s", first.stdout)
	}
	release()
	freed := wakeRun(t, args...)
	if !strings.Contains(freed.stdout, "state=free was=held") {
		t.Errorf("released, the next poll says so:\n%s", freed.stdout)
	}
	release2 := holdLock(t, path)
	retaken := wakeRun(t, args...)
	if !strings.Contains(retaken.stdout, "state=held was=free") {
		t.Errorf("re-taken, the line carries both ends:\n%s", retaken.stdout)
	}
	release2()

	t.Run("the bytes and the mtime are unchanged", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "quiet.lock")
		write(t, p, "the holder's own bytes\n")
		before, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		s := filepath.Join(t.TempDir(), "wake.state")
		for i := 0; i < 50; i++ {
			wakeRun(t, watchArgs(s, "--lock", p)...)
		}
		after, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if read(t, p) != "the holder's own bytes\n" {
			t.Errorf("the probe wrote to a file another process owns: %q", read(t, p))
		}
		if !before.ModTime().Equal(after.ModTime()) {
			t.Errorf("the mtime moved: %s then %s", before.ModTime(), after.ModTime())
		}
	})

	t.Run("never created when missing", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "absent.lock")
		s := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, watchArgs(s, "--lock", p, "--baseline")...)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the probe created the lock file it was asked about")
		}
		if !strings.Contains(r.stdout, "state=free") && !strings.Contains(r.stdout, "state=absent") {
			t.Errorf("a missing file is a reading and not a failure:\n%s", r.stdout)
		}
	})

	t.Run("a symlink at the lock path", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "somebody-elses")
		write(t, target, "x")
		link := filepath.Join(d, "link.lock")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		s := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, watchArgs(s, "--lock", link, "--baseline")...)
		if !strings.Contains(r.stdout, "unreadable: symlink at the lock path") {
			t.Errorf("a symlink at the lock path is unreadable:\n%s", r.stdout)
		}
	})

	t.Run("a directory at the lock path", func(t *testing.T) {
		d := filepath.Join(t.TempDir(), "dir.lock")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		s := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, watchArgs(s, "--lock", d, "--baseline")...)
		if !strings.Contains(r.stdout, "not a regular file") {
			t.Errorf("a directory at the lock path is refused as a reading:\n%s", r.stdout)
		}
	})

	t.Run("absent clears the streak", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "gone.lock")
		s := filepath.Join(t.TempDir(), "wake.state")
		r := wakeRun(t, "watch", "--state", s, "--max", "30s", "--on-deadline", "report",
			"--interval", "5s", "--lock", p)
		if r.exit != 0 || strings.Contains(r.stdout, "WAKE BROKEN") {
			t.Errorf("absent is a state and never a failure: exit %d\n%s", r.exit, r.all())
		}
	})
}

// ---------------------------------------------------------------------------
// 18. A stop is a verdict.

func TestAStopIsAVerdict(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "wake.state")
	lockPath := filepath.Join(dir, "lane.lock")
	write(t, lockPath, "")
	r := wakeRunStopped(t, "watch", "--state", state, "--max", "20m",
		"--on-deadline", "report", "--interval", "5s", "--lock", lockPath)
	last := lastLine(r.stdout)
	if !strings.HasPrefix(last, "WAKE STOPPED ") || !strings.Contains(last, "stopped by the caller") {
		t.Errorf("a stop is the last line and the fourth verdict:\n%s", r.stdout)
	}
	if r.exit != 0 {
		t.Errorf("exit %d, want 0: the stop was the caller's decision", r.exit)
	}
	for _, field := range []string{"after=", "polls=", "pending="} {
		if !strings.Contains(last, field) {
			t.Errorf("WAKE STOPPED carries %s: %s", field, last)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(state)
	for _, e := range entries {
		name := e.Name()
		if name == filepath.Base(lockPath) || name == base ||
			name == filepath.Base(wake.TempName(state)) || name == filepath.Base(wake.LockName(state)) {
			continue
		}
		t.Errorf("the only paths afterwards are --state, its fixed-name temp file and <state>.lock; found %s", name)
	}
	// <state>.lock is free after the exit: a second watch over the same path runs.
	again := wakeRun(t, "watch", "--state", state, "--max", "5s",
		"--on-deadline", "report", "--interval", "5s", "--lock", lockPath)
	if again.exit != 0 {
		t.Errorf("the lock was not released with the process: exit %d\n%s", again.exit, again.all())
	}
}
