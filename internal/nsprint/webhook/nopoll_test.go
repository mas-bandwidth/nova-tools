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
var pollAllowed = map[string]string{
	// `ci compare`: one manual REST read per invocation, never on a loop, the
	// parity evidence for retiring Actions; the lander never calls it.
	"internal/nsprint/ci/compare.go": "one budgeted parity read",
}

// TestNoPollingPathsRemain: no nova-sprint source (cmd/nova-sprint,
// internal/nsprint) reads a check state from GitHub or asks it to rerun one;
// the check state is ci:<repo>:<sha>:gh, written from the webhook stream, and
// a rerun is `ci request --again`. Tests and fixtures are not scanned.
func TestNoPollingPathsRemain(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var hits []string
	for _, dir := range []string{"cmd/nova-sprint", "internal/nsprint"} {
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
			if _, ok := pollAllowed[rel]; ok {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if pollRx.MatchString(line) {
					hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hits {
		t.Errorf("GitHub check-state read left in nova-sprint: %s", h)
	}
	for rel := range pollAllowed {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("allowed file %s is gone: drop its row", rel)
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
