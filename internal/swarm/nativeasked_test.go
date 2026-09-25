package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
				if asked {
					t.Fatalf("this run did not end by asking, and the test claimed the question %q", got)
				}
				return
			}
			if !asked {
				t.Fatalf("a card that ended on %q was not read as asking", tc.want)
			}
			if got != tc.want {
				t.Fatalf("the question carried is %q, want %q", got, tc.want)
			}
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
	if err != nil || !wrote {
		t.Fatalf("the asked report was not written: wrote=%v err=%v", wrote, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if want := "RESULT: ASKED " + question; lines[0] != want {
		t.Fatalf("line 1 is %q, want %q", lines[0], want)
	}
	if !strings.Contains(string(raw), AskedWrittenBy) {
		t.Fatalf("the report does not say who wrote it:\n%s", raw)
	}
	if !strings.Contains(string(raw), "dogfood-question") {
		t.Fatalf("the report does not name the card:\n%s", raw)
	}
	if r := ParseReport(raw); r.Class != ClassPlanOnly {
		t.Fatalf("a report the machinery wrote classed %q; it must be %q, never counted as work done", r.Class, ClassPlanOnly)
	}
	if !AskedReport(path) {
		t.Fatal("the report this file wrote is not recognised as one the machinery wrote")
	}
}

// A CARD THAT PUBLISHED OWNS ITS REPORT. The write refuses rather than overwriting it, on
// the same terms as the blocked report.
func TestWriteAskedResultNeverOverwritesACardsOwnReport(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	own := []byte("RESULT: dogfood-question sha=abc123def456\n\n## Head\nfindings: 0\n")
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), own, 0o644); err != nil {
		t.Fatal(err)
	}

	path, wrote, err := WriteAskedResult(job, "dogfood-question", "May I write RESULT.md?")
	if err != nil {
		t.Fatal(err)
	}
	if wrote || path != "" {
		t.Fatalf("a card's own report was overwritten (path=%q)", path)
	}
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(own) {
		t.Fatalf("the card's own report was changed:\n%s", raw)
	}
	if AskedReport(filepath.Join(job, "RESULT.md")) {
		t.Fatal("a card's own report was read as one the machinery wrote")
	}
}

// AskedEnd asks the question of a job on disk: the capture's tail, the gather's own result
// lookup, and ./repo's own commit count -- the last of these through git, which is the one
// half the table test cannot reach.
func TestAskedEndReadsTheJobTheRunLeftBehind(t *testing.T) {
	t.Parallel()

	t.Run("a question, no result, no commit", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, dogfoodHulkTail)

		got, asked := AskedEnd(job, 0)
		if !asked {
			t.Fatal("a job whose capture ends with a question was not read as asking")
		}
		if want := "Step 2: May I write RESULT.md?"; got != want {
			t.Fatalf("the question read off the job is %q, want %q", got, want)
		}
	})

	t.Run("a question the card then committed past", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, "Should I also update the docs?\n")
		aRepoWithACommitPastItsBase(t, filepath.Join(job, "repo"))

		if got, asked := AskedEnd(job, 0); asked {
			t.Fatalf("a card that asked and then committed the work was read as asking: %q", got)
		}
	})

	t.Run("a question beside a result the card published under repo/", func(t *testing.T) {
		job := t.TempDir()
		writeCapture(t, job, dogfoodStudioTail)
		if err := os.MkdirAll(filepath.Join(job, "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(job, "repo", "RESULT.md"), []byte("RESULT: x sha=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got, asked := AskedEnd(job, 0); asked {
			t.Fatalf("a card that published under repo/ was read as asking: %q", got)
		}
	})

	t.Run("a job with no capture at all", func(t *testing.T) {
		if got, asked := AskedEnd(t.TempDir(), 0); asked {
			t.Fatalf("a job whose harness said nothing was read as asking: %q", got)
		}
	})
}

func writeCapture(t *testing.T, job, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(job, "harness-output.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// aRepoWithACommitPastItsBase builds what repoCommits counts, the way wall_test.go already
// builds it: a repository on a branch, a base its remote ref names, and one commit past it.
func aRepoWithACommitPastItsBase(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "work")
	git(t, dir, "config", "user.email", "card@example.invalid")
	git(t, dir, "config", "user.name", "card")
	base := commit(t, dir, "base")
	git(t, dir, "update-ref", "refs/remotes/origin/main", base)
	commit(t, dir, "the-work-the-card-did-after-it-asked")
}
