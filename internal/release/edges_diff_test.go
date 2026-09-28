package release

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

func diffTestCaptures() (*diffCapture, *diffCapture) {
	return &diffCapture{Capture: bounded.NewCapture(localDiffCap, nil)},
		&diffCapture{Capture: bounded.NewCapture(childCap, nil)}
}

func TestDiffNamesRefusesOverflowEvenWhenTheChildExitsCleanly(t *testing.T) {
	t.Parallel()
	for _, runErr := range []error{nil, errors.New("signal: killed")} {
		stdout, stderr := diffTestCaptures()
		// Include a valid prefix: accepting it would silently omit the tail.
		data := []byte("README.md\n" + strings.Repeat("x", localDiffCap) + "\n")[:localDiffCap+1]
		if _, err := stdout.Write(data); err != nil {
			t.Fatal(err)
		}
		files, err := diffNamesResult(stdout, stderr, runErr)
		var limit *diffOutputLimitError
		if files != nil || !errors.As(err, &limit) {
			t.Fatalf("run error %v: files=%v error=%v", runErr, files, err)
		}
		if limit.limit != localDiffCap || limit.seen != localDiffCap+1 || limit.stream != "path list" {
			t.Fatalf("wrong limit diagnostic: %+v", limit)
		}
		for _, want := range []string{fmt.Sprint(localDiffCap), fmt.Sprint(localDiffCap + 1), "refusing to classify partial output"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("missing %q in %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "signal: killed") {
			t.Fatalf("cancellation hid the capture limit: %v", err)
		}
	}
}

func TestDiffNamesKeepsTheCompleteLargeListAndExcludesWarnings(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	var want []string
	for i := 0; i < 5000; i++ {
		want = append(want, fmt.Sprintf("docs/ordinary-%04d.md", i))
	}
	want = append(want, "internal/secrets/seal.go")
	data := strings.Join(want, "\n") + "\n"
	if len(data) <= childCap {
		t.Fatal("fixture must exceed the old child capture cap")
	}
	if _, err := stdout.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Write([]byte("warning: exhaustive rename detection was skipped\n")); err != nil {
		t.Fatal(err)
	}
	files, err := diffNamesResult(stdout, stderr, nil)
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("complete list lost or warning became a path: got=%d want=%d error=%v", len(files), len(want), err)
	}
}

func TestDiffNamesRefusesDiagnosticOverflow(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	if _, err := stderr.Write(bytes.Repeat([]byte("w"), childCap+1)); err != nil {
		t.Fatal(err)
	}
	files, err := diffNamesResult(stdout, stderr, nil)
	var limit *diffOutputLimitError
	if files != nil || !errors.As(err, &limit) || limit.stream != "diagnostics" || limit.seen != childCap+1 {
		t.Fatalf("diagnostic overflow accepted: files=%v error=%v", files, err)
	}
}

func TestDiffNamesFailureReportsStderrWithoutReturningPaths(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	if _, err := stdout.Write([]byte("README.md\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Write([]byte("fatal: bad revision\n")); err != nil {
		t.Fatal(err)
	}
	runErr := errors.New("exit status 128")
	files, err := diffNamesResult(stdout, stderr, runErr)
	if files != nil || !errors.Is(err, runErr) || !strings.Contains(err.Error(), "fatal: bad revision") || strings.Contains(err.Error(), "README.md") {
		t.Fatalf("failed read: files=%v error=%v", files, err)
	}
}

func TestCutLocalDiffOverflowDoesNotWriteOrSuggestFetchingTags(t *testing.T) {
	t.Parallel()
	f := truncatedForge()
	dir := t.TempDir()
	changelog, paths := filepath.Join(dir, "CHANGELOG.md"), filepath.Join(dir, "paths.txt")
	deps := cutDeps(t, f)
	deps.Git = &fakeGit{fail: &diffOutputLimitError{"path list", localDiffCap, localDiffCap + 1}}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v0.16.0", "--changelog", changelog, "--local-diff", dir,
		"--paths-from", paths, "--security-read", "review-123"}, &out, &errs, deps)
	if code != 2 || !strings.Contains(errs.String(), "capture limit") || strings.Contains(errs.String(), "fetch --tags") {
		t.Fatalf("wrong refusal: code=%d out=%s err=%s", code, out.String(), errs.String())
	}
	if len(f.tagged) != 0 || strings.Contains(out.String(), "RELEASE CUT PATHS") {
		t.Fatalf("overflow was classified or tagged: out=%s tags=%v", out.String(), f.tagged)
	}
	for _, path := range []string{changelog, paths} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("refused cut wrote %s: %v", path, err)
		}
	}
}
