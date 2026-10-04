package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE DOGFOOD'S OWN TRANSCRIPT TAIL, 2026-09-22 (rowan-new reports/dogfood-runtime-2026-09-22.md,
// (b)). The fixture card runs `echo` at STEP 1 and at STEP 2 forbids writing `RESULT.md`
// until an operator approves, telling the model to ask and wait. What the two benches
// captured, byte for byte: hulk ended on two lines, the Studio on one.
const (
	dogfoodHulkTail   = "Step 1 output: dogfood-question\nStep 2: May I write RESULT.md?\n"
	dogfoodStudioTail = "May I write RESULT.md?\n"
)

// THE FOUR ENDINGS. The question test has to separate the card that asked from the three
// cards it would otherwise be mistaken for, and each row here is one of them.
func TestAskedSeparatesTheCardThatAskedFromTheThreeItIsNot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  AskedRun
		want string // the question, or "" for a run that did not end by asking
	}{
		{
			// 1. The dogfood's own ending, from hulk. Exit 0, no report, no commit,
			// and the last thing the card said is a question.
			name: "the dogfood question, hulk",
			run:  AskedRun{Capture: dogfoodHulkTail},
			want: "Step 2: May I write RESULT.md?",
		},
		{
			// The Studio's capture of the same card ended on the question alone.
			name: "the dogfood question, studio",
			run:  AskedRun{Capture: dogfoodStudioTail},
			want: "May I write RESULT.md?",
		},
		{
			// The canary's own ending (#2548's body), which ends with the mark and
			// opens with one of the phrases as well.
			name: "the canary question",
			run: AskedRun{Capture: "wrote jobs/card-cell-canary/test/x_test.go\n" +
				"Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?\n"},
			want: "Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?",
		},
		{
			// A question with no mark at all is still a question to the operator.
			name: "an opener with no question mark",
			run:  AskedRun{Capture: "Should I open the pull request now\n"},
			want: "Should I open the pull request now",
		},
		{
			// 2. A NORMAL DONE RUN: the card worked and published. Its last line is
			// prose, and the run found its report.
			name: "a normal DONE run",
			run: AskedRun{
				Capture:   "STEP 4: wrote RESULT.md\nDone: 3 tests green, nothing left owed.\n",
				Published: true,
			},
			want: "",
		},
		{
			// A published card whose last line IS a question is done, not asking: the
			// report it owns is the answer to everything it said.
			name: "a DONE run whose last line is a question",
			run: AskedRun{
				Capture:   "Wrote RESULT.md. Want me to open the PR as well?\n",
				Published: true,
			},
			want: "",
		},
		{
			// 3. A CRASH. A non-zero exit is already its own end (`rc=<n>`), and a
			// model that asked and then fell over is not waiting for an answer.
			name: "a crash after a question",
			run: AskedRun{
				Capture: "Should I retry the build?\nError: Cannot connect to API\n",
				RC:      1,
			},
			want: "",
		},
		{
			name: "a crash whose very last line is the question",
			run:  AskedRun{Capture: dogfoodStudioTail, RC: 2},
			want: "",
		},
		{
			// 4. A QUESTION FOLLOWED BY A COMMIT: the model asked rhetorically and
			// went on and did the work. The commits are the answer.
			name: "a question and then a commit",
			run: AskedRun{
				Capture:   "Should I also update the docs?\n",
				Committed: true,
			},
			want: "",
		},
		{
			// A silent harness said nothing at all. That is `harness-silent`, a
			// different fault with a different remedy, and never a question.
			name: "a silent harness",
			run:  AskedRun{Capture: ""},
			want: "",
		},
		{
			// The wall's own lines are the machinery's words in the child's file.
			// They are skipped here exactly as `harness=silent` skips them.
			name: "the wall's lines are not the child speaking",
			run: AskedRun{Capture: "Shall i push the branch?\n" +
				"SANDBOX OK backend=landlock cwd=/tmp/job\n"},
			want: "Shall i push the branch?",
		},
		{
			// The terminal's paint and a model's bold are decoration, not an answer.
			name: "a painted and bolded question",
			run:  AskedRun{Capture: "\x1b[33;1m**Would you like me to proceed?**\x1b[0m\n"},
			want: "**Would you like me to proceed?**",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, asked := Asked(tc.run)
			if tc.want == "" {
				require.False(t, asked, "this run did not end by asking, and the test claimed the question %q", got)
				return
			}
			require.True(t, asked, "a card that ended on %q was not read as asking", tc.want)
			require.Equal(t, tc.want, got, "the question carried is %q, want %q", got, tc.want)
		})
	}
}

// THE REPORT ITSELF. Line 1 carries the verdict word and the question; the written-by line
// says the machinery wrote it; and there is no findings head, so no fold can ever count it
// as work a worker did.
func TestWriteAskedResultNamesTheQuestionAndItsAuthor(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	question := "Step 2: May I write RESULT.md?"

	path, wrote, err := WriteAskedResult(job, "dogfood-question", question)
	require.NoError(t, err, "the asked report was not written: wrote=%v err=%v", wrote, err)
	require.True(t, wrote, "the asked report was not written: wrote=%v err=%v", wrote, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	want := "RESULT: ASKED " + question
	require.Equal(t, want, lines[0], "line 1 is %q, want %q", lines[0], want)
	require.Contains(t, string(raw), AskedWrittenBy, "the report does not say who wrote it:\n%s", raw)
	require.Contains(t, string(raw), "dogfood-question", "the report does not name the card:\n%s", raw)
	r := ParseReport(raw)
	require.Equal(t, ClassPlanOnly, r.Class, "a report the machinery wrote classed %q; it must be %q, never counted as work done", r.Class, ClassPlanOnly)
	require.True(t, AskedReport(path), "the report this file wrote is not recognised as one the machinery wrote")
}

// A CARD THAT PUBLISHED OWNS ITS REPORT. The write refuses rather than overwriting it, on
// the same terms as the blocked report.
func TestWriteAskedResultNeverOverwritesACardsOwnReport(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	own := []byte("RESULT: dogfood-question sha=abc123def456\n\n## Head\nfindings: 0\n")
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), own, 0o644))

	path, wrote, err := WriteAskedResult(job, "dogfood-question", "May I write RESULT.md?")
	require.NoError(t, err)
	require.False(t, wrote, "a card's own report was overwritten (path=%q)", path)
	require.Empty(t, path, "a card's own report was overwritten (path=%q)", path)
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err)
	require.Equal(t, string(own), string(raw), "the card's own report was changed:\n%s", raw)
	require.False(t, AskedReport(filepath.Join(job, "RESULT.md")), "a card's own report was read as one the machinery wrote")
}
