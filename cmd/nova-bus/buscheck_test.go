package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// strangers writes n notes that each fail check (a To naming nobody), uncommitted, so a
// --full walk over the checkout finds at least n findings.
func strangers(t *testing.T, checkout string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		writeFile(t, checkout, fmt.Sprintf("from-ada/stranger-%02d.md", i), "From: Ada\nTo: Boe\nSubject: s\n\nbody\n")
	}
}

var checkCountRe = regexp.MustCompile(`(?m)^BUS CHECK findings=(\d+) fail=(\d+) warn=(\d+)((?: [a-z]+=\d+)*)$`)

// checkCounts reads the BUS CHECK line: findings, fail, and the per-class counts summed.
func checkCounts(t *testing.T, stderr string) (findings, fail, classSum int) {
	t.Helper()
	m := checkCountRe.FindAllStringSubmatch(stderr, -1)
	if len(m) != 1 {
		t.Fatalf("want exactly one BUS CHECK line on stderr, got %d:\n%s", len(m), stderr)
	}
	findings, _ = strconv.Atoi(m[0][1])
	fail, _ = strconv.Atoi(m[0][2])
	for _, kv := range strings.Fields(m[0][4]) {
		_, v, _ := strings.Cut(kv, "=")
		n, _ := strconv.Atoi(v)
		classSum += n
	}
	return findings, fail, classSum
}

// THE DONE-WHEN (#2574): a full check over a bus with more findings than a screen prints
// at most --max of them (default 20), one BUS MORE line names what the cap held back and
// the flag that lifts it, and one BUS CHECK line counts EVERY finding by class. The cap
// governs printing only: the exit code and the counts are the whole walk's. And --since
// takes a date as well as a revision.
func TestBusCheckFullIsCapped(t *testing.T) {
	t.Parallel()

	t.Run("default cap is 20 and the count is never capped", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		strangers(t, checkout, 30)
		r := invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1)
		if n := strings.Count(r.stderr, "BUS FAIL "); n != 20 {
			t.Fatalf("the default cap printed %d BUS FAIL lines, want 20:\n%s", n, r.stderr)
		}
		findings, fail, classSum := checkCounts(t, r.stderr)
		if findings < 30 || fail != findings || classSum != findings {
			t.Fatalf("BUS CHECK findings=%d fail=%d classes sum to %d over 30 broken notes; want findings>=30 and all three equal:\n%s",
				findings, fail, classSum, r.stderr)
		}
		r.mustContain(t, "stderr", fmt.Sprintf(`BUS MORE shown=20 total=%d remedy="--max 0"`, findings))
		// A failing run's stdout is still only its scope: no count a caller could read as a pass.
		if got := strings.TrimSpace(r.stdout); got != "BUS SCOPE mode=full cursor=- changed=0" {
			t.Fatalf("a capped failing check wrote more than its scope to stdout: %q", r.stdout)
		}
	})

	t.Run("--max n prints n", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		strangers(t, checkout, 10)
		r := invoke(t, "", "check", "--bus", checkout, "--full", "--max", "3").mustCode(t, 1)
		if n := strings.Count(r.stderr, "BUS FAIL "); n != 3 {
			t.Fatalf("--max 3 printed %d BUS FAIL lines:\n%s", n, r.stderr)
		}
		findings, _, _ := checkCounts(t, r.stderr)
		r.mustContain(t, "stderr", fmt.Sprintf("BUS MORE shown=3 total=%d ", findings))
	})

	t.Run("--max 0 prints every finding and no MORE line", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		strangers(t, checkout, 30)
		r := invoke(t, "", "check", "--bus", checkout, "--full", "--max", "0").mustCode(t, 1)
		findings, _, _ := checkCounts(t, r.stderr)
		if n := strings.Count(r.stderr, "BUS FAIL "); n != findings {
			t.Fatalf("--max 0 printed %d of %d findings:\n%s", n, findings, r.stderr)
		}
		if strings.Contains(r.stderr, "BUS MORE") {
			t.Fatalf("an uncapped run printed a BUS MORE line:\n%s", r.stderr)
		}
	})

	t.Run("under the cap there is no MORE line", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		strangers(t, checkout, 2)
		r := invoke(t, "", "check", "--bus", checkout, "--full", "--max", "100").mustCode(t, 1)
		findings, _, _ := checkCounts(t, r.stderr)
		if n := strings.Count(r.stderr, "BUS FAIL "); n != findings || strings.Contains(r.stderr, "BUS MORE") {
			t.Fatalf("a run under its cap printed %d of %d findings or a MORE line:\n%s", n, findings, r.stderr)
		}
	})

	t.Run("a negative --max is a bad invocation", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		invoke(t, "", "check", "--bus", checkout, "--full", "--max", "-1").
			mustCode(t, 2).mustContain(t, "stderr", "--max counts entries, so it is 0 or more, got -1")
	})

	t.Run("a clean bus prints neither line", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		r := invoke(t, "", "check", "--bus", checkout, "--full", "--max", "1").mustCode(t, 0).
			mustContain(t, "stdout", "BUS OK")
		if r.stderr != "" {
			t.Fatalf("a clean check wrote to stderr: %q", r.stderr)
		}
	})

	t.Run("--since takes a date", func(t *testing.T) {
		t.Parallel()
		hermetic(t)
		checkout, _ := busDir(t)
		// One broken note committed at a known date, well after the fixture's own commit.
		writeFile(t, checkout, "from-bo/dated-stranger.md", "From: Bo\nTo: Boe\nSubject: s\n\nbody\n")
		gitIn(t, checkout, "add", "-A")
		cmd := exec.Command("git", "-C", checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "dated")
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2099-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2099-01-02T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v\n%s", err, out)
		}
		// Since the day before it: the last commit before that day is the fixture's, so
		// the change set holds the broken note and the check fails on it.
		invoke(t, "", "check", "--bus", checkout, "--since", "2099-01-01").mustCode(t, 1).
			mustContain(t, "stdout", "BUS SCOPE mode=since").
			mustContain(t, "stderr", "BUS FAIL from-bo/dated-stranger.md")
		// Since the day after it: nothing changed.
		invoke(t, "", "check", "--bus", checkout, "--since", "2099-01-03").mustCode(t, 0).
			mustContain(t, "stdout", "changed=0")
		// Since before the bus existed: a refusal, never an empty change set.
		invoke(t, "", "check", "--bus", checkout, "--since", "2000-01-01").mustCode(t, 2).
			mustContain(t, "stderr", "no commit in this checkout is dated before 2000-01-01T00:00:00Z")
	})
}
