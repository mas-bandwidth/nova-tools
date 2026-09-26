package webhook_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pollRx matches a GitHub REST path that reads or reruns a check state: the
// commit check-runs list, the workflow-runs list or a run, and any rerun.
var pollRx = regexp.MustCompile(`/check-runs\b|actions/runs|/rerun`)

// pollAllowed are the files that may name such a path, each with its reason.
// Inside nova-sprint only `ci compare` may; the rest are the older tools
// outside nova-sprint (#4343 widened the scan to every package), each not a
// nova-sprint verb and outside that card's PATHS, to move onto ev:github or
// retire in a follow-up. A row whose file is gone fails, so the list only
// shrinks.
var pollAllowed = map[string]string{
	// `ci compare`: one manual REST read per invocation, never on a loop, the
	// parity evidence for retiring Actions; the lander never calls it.
	"internal/nsprint/ci/compare.go": "one budgeted parity read",
	"cmd/nova-decide/review.go":      "nova-decide reads check-runs by gh; not a nova-sprint verb",
	"internal/merge/flaky.go":        "nova-merge reads a workflow run's jobs by gh; not a nova-sprint verb",
	"internal/ci/failed_forge.go":    "the old ci failed-run reader; not a nova-sprint verb",
	"internal/release/edges.go":      "nova-release reads check-runs by gh; not a nova-sprint verb",
	"internal/wake/run.go":           "nova-wake polls check-runs by gh; not a nova-sprint verb",
}

// TestNoPollingPathsRemain: no source in the module (every package under
// cmd and internal, #4343) reads a check state from GitHub or asks it to
// rerun one, except the named files; the check state is
// ci:<repo>:<sha>:gh, written from the webhook stream, a wait on it is an
// XREAD of ev:github (internal/gh Await), and a rerun is `ci request
// --again`. Tests and fixtures are not scanned.
func TestNoPollingPathsRemain(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var hits []string
	n := 0
	allowedHit := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !(strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".lua")) || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			_, allowed := pollAllowed[rel]
			n++
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if !pollRx.MatchString(line) {
					continue
				}
				if allowed {
					allowedHit[rel] = true
					continue
				}
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 100 {
		t.Fatalf("read %d files; the walk is broken", n)
	}
	for _, h := range hits {
		t.Errorf("GitHub check-state read left outside the allowlist: %s", h)
	}
	for rel := range pollAllowed {
		if !allowedHit[rel] {
			t.Errorf("allowed file %s no longer reads a check state (or is gone): drop its row", rel)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}
