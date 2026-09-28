package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ProblemClass is a display rule, and the BUS CHECK line is only as good as it: every
// class it names is pinned here to the Where or Reason shape it reads, including the
// shapes that must NOT be swallowed by a neighbouring class.
func TestProblemClassNamesEveryClass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		p    Problem
		want string
	}{
		{"a RECEIPTS line", Problem{Where: "from-ada/RECEIPTS:3", Reason: "names no note"}, "receipt"},
		{"the RECEIPTS file itself", Problem{Where: "from-ada/RECEIPTS", Reason: "cannot be read"}, "receipt"},
		{"an INDEX line", Problem{Where: "from-ada/INDEX:2", Reason: "names a note that is not on disk"}, "catalogue"},
		{"a note no INDEX line holds", Problem{Where: "from-ada/a.md", Reason: "no line in from-ada/INDEX holds this note"}, "catalogue"},
		{"a bare INDEX file", Problem{Where: "from-ada/INDEX", Reason: "is not sorted"}, "state"},
		{"a CURSOR", Problem{Where: "from-ada/CURSOR", Reason: "is not a commit"}, "state"},
		{"an OPEN", Problem{Where: "from-ada/OPEN", Reason: "cannot be parsed"}, "state"},
		{"a BEAT", Problem{Where: "from-ada/BEAT", Reason: "cannot be parsed"}, "state"},
		{"an Id finding", Problem{Where: "from-ada/a.md", Reason: KeyID + ": is not this lane's"}, "id"},
		{"a Re finding", Problem{Where: "from-ada/a.md", Reason: KeyRe + ": names no note"}, "re"},
		{"a stray file", Problem{Where: "from-ada/x.txt", Reason: "a lane holds notes (*.md) and nothing else"}, "stray"},
		{"an unowned lane", Problem{Where: "from-zed", Reason: "no participant owns this lane"}, "lane"},
		{"a parse error", Problem{Where: "from-ada/a.md", Reason: "line 2: is not a header"}, "parse"},
		{"a header finding", Problem{Where: "from-ada/a.md", Reason: `To: "Boe" names no one on this bus`}, "header"},
		{"no Where at all", Problem{Reason: "no Subject"}, "header"},
		// A NOTE named like a state file is still a note: the match is on the whole
		// last segment, never a prefix of it.
		{"a note named RECEIPTS.md", Problem{Where: "from-ada/RECEIPTS.md", Reason: "no Subject"}, "header"},
		{"a note named INDEX-notes.md", Problem{Where: "from-ada/INDEX-notes.md:4", Reason: "no Subject"}, "header"},
		// "line" only counts as a parse error in the one shape ParseNoteAll writes.
		{"prose that starts with line", Problem{Where: "from-ada/a.md", Reason: "line breaks in a Subject"}, "header"},
	} {
		if got := ProblemClass(tc.p); got != tc.want {
			t.Errorf("%s: ProblemClass(%+v) = %q, want %q", tc.name, tc.p, got, tc.want)
		}
	}
}

// The counts are the whole walk's, split fail/warn and by class, and the class list is
// sorted so the same findings print the same count line whatever order they were met in.
func TestCountCheckFindingsCountsEveryFindingByClass(t *testing.T) {
	t.Parallel()
	ps := []Problem{
		{Where: "from-ada/a.md", Reason: "no Subject"},
		{Where: "from-ada/RECEIPTS:1", Reason: "names no note"},
		{Where: "from-bo/b.md", Reason: "no Subject", Warn: true},
		{Where: "from-ada/c.md", Reason: KeyID + ": wrong lane"},
		{Where: "from-ada/d.md", Reason: "no Subject"},
	}
	got := CountCheckFindings(ps)
	want := CheckCounts{
		Findings: 5, Fail: 4, Warn: 1,
		Class: []ClassCount{{"header", 3}, {"id", 1}, {"receipt", 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CountCheckFindings = %+v, want %+v", got, want)
	}
	// Same findings, reversed: the same counts, in the same order.
	rev := make([]Problem, len(ps))
	for i, p := range ps {
		rev[len(ps)-1-i] = p
	}
	if again := CountCheckFindings(rev); !reflect.DeepEqual(again, want) {
		t.Fatalf("the count line depends on walk order: %+v, want %+v", again, want)
	}
	// No findings, no classes, and zero everywhere.
	if empty := CountCheckFindings(nil); empty.Findings != 0 || empty.Fail != 0 || empty.Warn != 0 || len(empty.Class) != 0 {
		t.Fatalf("CountCheckFindings(nil) = %+v, want all zero", empty)
	}
}

// datedRepo is a repository holding three commits at fixed committer dates, so a test of
// --since <date> reads commits whose dates it wrote rather than the clock's.
func datedRepo(t *testing.T) (dir string, shas []string) {
	t.Helper()
	hermetic(t)
	dir = t.TempDir()
	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(nil, "init", "-q", "--initial-branch=main")
	for i, when := range []string{"2026-09-01T12:00:00Z", "2026-09-05T12:00:00Z", "2026-09-10T12:00:00Z"} {
		write(t, dir, filepath.Join("from-ada", "n"+string(rune('a'+i))+".md"), "x\n")
		run(nil, "add", "-A")
		run([]string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "commit", "-q", "-m", when)
		shas = append(shas, run(nil, "rev-parse", "HEAD"))
	}
	return dir, shas
}

// LastCommitBefore is the newest commit written before the moment, and a moment before
// every commit is a refusal rather than an empty change set.
func TestLastCommitBefore(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)
	for _, tc := range []struct {
		when string
		want string
	}{
		{"2026-09-02T00:00:00Z", shas[0]},
		{"2026-09-05T12:00:01Z", shas[1]},
		{"2026-09-09T00:00:00Z", shas[1]},
		{"2027-01-01T00:00:00Z", shas[2]},
	} {
		got, err := LastCommitBefore(dir, at(tc.when))
		if err != nil {
			t.Fatalf("LastCommitBefore(%s): %v", tc.when, err)
		}
		if got != tc.want {
			t.Errorf("LastCommitBefore(%s) = %s, want %s", tc.when, got, tc.want)
		}
	}
	_, err := LastCommitBefore(dir, at("2026-08-01T00:00:00Z"))
	if err == nil || !strings.Contains(err.Error(), "no commit in this checkout is dated before 2026-08-01T00:00:00Z") {
		t.Fatalf("a date before every commit was not refused by name: %v", err)
	}
}

// ResolveSinceCommit: a revision wins, even one spelled like a date; a date or instant
// that is not a revision names the last commit before it; anything else is the revision
// refusal it always was.
func TestResolveSinceCommit(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)

	if got, err := ResolveSinceCommit(dir, shas[0]); err != nil || got != shas[0] {
		t.Fatalf("a sha: got %s, %v; want %s", got, err, shas[0])
	}
	if got, err := ResolveSinceCommit(dir, "2026-09-06"); err != nil || got != shas[1] {
		t.Fatalf("a date: got %s, %v; want %s (the last commit before 2026-09-06)", got, err, shas[1])
	}
	if got, err := ResolveSinceCommit(dir, "2026-09-05T11:00:00Z"); err != nil || got != shas[0] {
		t.Fatalf("an instant: got %s, %v; want %s", got, err, shas[0])
	}
	// A tag that looks like a date is a revision, and a revision wins.
	cmd := exec.Command("git", "-C", dir, "tag", "2026-09-06", shas[2])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tag: %v %s", err, out)
	}
	if got, err := ResolveSinceCommit(dir, "2026-09-06"); err != nil || got != shas[2] {
		t.Fatalf("a tag spelled like a date: got %s, %v; want the tag's commit %s", got, err, shas[2])
	}
	// A date before the history refuses; neither a revision nor a date is the revision
	// refusal, unchanged.
	if _, err := ResolveSinceCommit(dir, "2020-01-01"); err == nil || !strings.Contains(err.Error(), "no commit in this checkout is dated before") {
		t.Fatalf("a date before every commit: %v", err)
	}
	_, err := ResolveSinceCommit(dir, "nosuchref")
	_, revErr := ResolveCommit(dir, "nosuchref")
	if err == nil || revErr == nil || err.Error() != revErr.Error() {
		t.Fatalf("a value that is neither: got %v, want the revision refusal %v", err, revErr)
	}
}
