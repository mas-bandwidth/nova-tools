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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
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
		_, err := stdout.Write(data)
		require.NoError(t, err)
		files, err := diffNamesResult(stdout, stderr, runErr)
		var limit *diffOutputLimitError
		require.Nil(t, files, "run error %v: files=%v error=%v", runErr, files, err)
		require.True(t, errors.As(err, &limit), "run error %v: files=%v error=%v", runErr, files, err)
		require.Falsef(t, limit.limit != localDiffCap || limit.seen != localDiffCap+1 || limit.stream != "path list", "wrong limit diagnostic: %+v", limit)
		for _, want := range []string{fmt.Sprint(localDiffCap), fmt.Sprint(localDiffCap + 1), "refusing to classify partial output"} {
			assert.Contains(t, err.Error(), want, "missing %q in %v", want, err)
		}
		require.NotContains(t, err.Error(), "signal: killed", "cancellation hid the capture limit: %v", err)
	}
}

func TestDiffNamesKeepsTheCompleteLargeListAndExcludesWarnings(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	var want []string
	for i := 0; i < 5000; i++ {
		want = append(want, fmt.Sprintf("docs/ordinary-%04d.md", i))
	}
	want = append(want, "pkg/secrets/seal.go")
	data := strings.Join(want, "\n") + "\n"
	require.False(t, len(data) <= childCap, "fixture must exceed the old child capture cap")
	_, err := stdout.Write([]byte(data))
	require.NoError(t, err)
	_, err = stderr.Write([]byte("warning: exhaustive rename detection was skipped\n"))
	require.NoError(t, err)
	files, err := diffNamesResult(stdout, stderr, nil)
	require.NoError(t, err, "complete list lost or warning became a path: got=%d want=%d error=%v", len(files), len(want), err)
	require.True(t, reflect.DeepEqual(files, want), "complete list lost or warning became a path: got=%d want=%d error=%v", len(files), len(want), err)
}

func TestDiffNamesRefusesDiagnosticOverflow(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	_, err := stderr.Write(bytes.Repeat([]byte("w"), childCap+1))
	require.NoError(t, err)
	files, err := diffNamesResult(stdout, stderr, nil)
	var limit *diffOutputLimitError
	require.Falsef(t, files != nil || !errors.As(err, &limit) || limit.stream != "diagnostics" || limit.seen != childCap+1, "diagnostic overflow accepted: files=%v error=%v", files, err)
}

func TestDiffNamesFailureReportsStderrWithoutReturningPaths(t *testing.T) {
	t.Parallel()
	stdout, stderr := diffTestCaptures()
	_, err := stdout.Write([]byte("README.md\n"))
	require.NoError(t, err)
	_, err = stderr.Write([]byte("fatal: bad revision\n"))
	require.NoError(t, err)
	runErr := errors.New("exit status 128")
	files, err := diffNamesResult(stdout, stderr, runErr)
	require.Nil(t, files, "failed read: files=%v error=%v", files, err)
	require.True(t, errors.Is(err, runErr), "failed read: files=%v error=%v", files, err)
	require.Contains(t, err.Error(), "fatal: bad revision", "failed read: files=%v error=%v", files, err)
	require.NotContains(t, err.Error(), "README.md", "failed read: files=%v error=%v", files, err)
}

func TestCutLocalDiffOverflowDoesNotWriteOrSuggestFetchingTags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stream string
		limit  int
		remedy string
	}{
		{"path list", localDiffCap, "report this range and observed byte count to the release tool maintainer"},
		{"diagnostics", childCap, "inspect Git's diagnostics for this range"},
	} {
		t.Run(tc.stream, func(t *testing.T) {
			t.Parallel()
			f := truncatedForge()
			dir := t.TempDir()
			changelog, paths := filepath.Join(dir, "CHANGELOG.md"), filepath.Join(dir, "paths.txt")
			deps := cutDeps(t, f)
			deps.Git = &fakeGit{fail: &diffOutputLimitError{tc.stream, tc.limit, int64(tc.limit + 1)}}
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
				"--version", "v0.16.0", "--changelog", changelog, "--local-diff", dir,
				"--paths-from", paths, "--security-read", "review-123"}, &out, &errs, deps)
			require.Equal(t, 2, code, "wrong refusal: code=%d out=%s err=%s", code, out.String(), errs.String())
			require.Contains(t, errs.String(), "capture limit", "wrong refusal: code=%d out=%s err=%s", code, out.String(), errs.String())
			require.NotContains(t, errs.String(), "fetch --tags", "wrong refusal: code=%d out=%s err=%s", code, out.String(), errs.String())
			for _, want := range []string{"no path list, changelog or tag written", tc.remedy} {
				assert.Contains(t, errs.String(), want, "refusal missing %q: %s", want, errs.String())
			}
			assert.Falsef(t, tc.stream == "diagnostics" && !strings.Contains(errs.String(), dir), "diagnostic remedy omits the checkout: %s", errs.String())
			for _, bypass := range []string{"--paths-from", "nearer tag", "hand-written"} {
				assert.NotContains(t, errs.String(), bypass, "overflow remedy suggests %q: %s", bypass, errs.String())
			}
			require.Empty(t, f.tagged, "overflow was classified or tagged: out=%s tags=%v", out.String(), f.tagged)
			require.NotContains(t, out.String(), "RELEASE CUT PATHS", "overflow was classified or tagged: out=%s tags=%v", out.String(), f.tagged)
			for _, path := range []string{changelog, paths} {
				_, err := os.Stat(path)
				require.Truef(t, os.IsNotExist(err), "refused cut wrote %s: %v", path, err)
			}
		})
	}
}
