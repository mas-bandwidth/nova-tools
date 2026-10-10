package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover for nativeasked.go's AskedEnd (nativeasked.go:81): the job read on
// disk. Asked itself is the table test's (nativeasked_test.go); what only AskedEnd
// adds is the job layout -- the capture's tail, the gather's own result lookup and
// ./repo's own commit count. Every test is named TestNativeaskedCover* so
// `-run TestNativeaskedCover` selects them, and every row lays the job out with plain
// files in the test's own temp dir: repoCommits refuses a job whose repo/ has no .git
// before any child runs, so no row needs a git process. The committed-past-base half
// needs real git history and stays with the functional tier
// (TestAskedEndReadsTheJobTheRunLeftBehind) beside the Asked seam's own unit row
// ("a question and then a commit", nativeasked_test.go).

// writeAskedCapture writes the card's own capture, <job>/harness-output.log, the file
// AskedEnd reads the capture's tail from.
func writeAskedCapture(t *testing.T, job, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(job, "harness-output.log"), []byte(body), 0o644))
}

// TestNativeaskedCoverAskedEndAsksTheJobOnDisk: AskedEnd reports the question a job
// ended on from what the run left on disk, and refuses a job that exited non-zero,
// published a result, ended on prose or said nothing.
func TestNativeaskedCoverAskedEndAsksTheJobOnDisk(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		job  func(t *testing.T) string
		rc   int
		want string // the question, or "" for a job that did not end by asking
	}{
		{
			// The main path: a finished job whose capture ends on the dogfood's
			// own question, no report anywhere the gather looks, no repo at all.
			name: "a question with no result and no commits",
			job: func(t *testing.T) string {
				t.Helper()
				job := t.TempDir()
				writeAskedCapture(t, job, dogfoodHulkTail)
				return job
			},
			want: "Step 2: May I write RESULT.md?",
		},
		{
			// The rc is the caller's: a non-zero exit is already its own end even
			// when the very last line is the question.
			name: "a non-zero rc is the run's own end",
			job: func(t *testing.T) string {
				t.Helper()
				job := t.TempDir()
				writeAskedCapture(t, job, dogfoodStudioTail)
				return job
			},
			rc:   1,
			want: "",
		},
		{
			// A published card owns its report: the result the gather finds at the
			// job root is the answer to everything the capture says.
			name: "a published result owns the end",
			job: func(t *testing.T) string {
				t.Helper()
				job := t.TempDir()
				writeAskedCapture(t, job, dogfoodStudioTail)
				require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("RESULT: x sha=1\n"), 0o644))
				return job
			},
			want: "",
		},
		{
			// A repo/ directory with no .git is no commits -- repoCommits refuses
			// it before any child runs -- so the question still stands.
			name: "a bare repo directory is not commits",
			job: func(t *testing.T) string {
				t.Helper()
				job := t.TempDir()
				writeAskedCapture(t, job, dogfoodStudioTail)
				require.NoError(t, os.MkdirAll(filepath.Join(job, "repo"), 0o755))
				return job
			},
			want: "May I write RESULT.md?",
		},
		{
			// The last thing the card said is prose: it did not ask.
			name: "a capture that ends on prose",
			job: func(t *testing.T) string {
				t.Helper()
				job := t.TempDir()
				writeAskedCapture(t, job, "STEP 4: wrote RESULT.md\nDone: 3 tests green, nothing left owed.\n")
				return job
			},
			want: "",
		},
		{
			// A job with no capture at all said nothing: never a question.
			name: "a job with no capture at all",
			job: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, asked := AskedEnd(tc.job(t), tc.rc)
			if tc.want == "" {
				assert.False(t, asked, "this job did not end by asking, and the test claimed the question %q", got)
				return
			}
			assert.True(t, asked, "a job that ended on %q was not read as asking", tc.want)
			assert.Equal(t, tc.want, got, "the question carried is %q, want %q", got, tc.want)
		})
	}
}
