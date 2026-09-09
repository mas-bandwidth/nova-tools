package bus

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hermetic makes git in this test process ignore the machine's own configuration. Setting
// an environment variable here is not the tool reading the environment to decide behavior
// -- the tool never does -- it is the test refusing to depend on whatever the person
// running it has in ~/.gitconfig, including a commit.gpgsign that would otherwise make
// these tests hang on a key.
func hermetic(t *testing.T) {
	t.Helper()
	none := filepath.Join(t.TempDir(), "no-such-gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", none)
	t.Setenv("GIT_CONFIG_SYSTEM", none)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

var testIdentity = map[string]Identity{
	"Ada": {Name: "Ada", Email: "ada@example.com"},
	"Bo":  {Name: "Bo", Email: "bo@example.com"},
}

// bareTable builds a bare repository holding a bus with a roster on branch main, and
// returns its path. It is the remote every clone below pushes to.
func bareBus(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "bus.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := git(bare, "init", "--bare", "--initial-branch=main"); err != nil {
		t.Fatalf("init --bare: %v %s", err, out)
	}
	seed := cloneBus(t, bare)
	write(t, seed, ConfigName, rosterJSON)
	if _, err := CommitAndPush(seed, testIdentity["Ada"], []string{ConfigName}, "the roster", "origin", "main", 3); err != nil {
		t.Fatalf("seeding the bus: %v", err)
	}
	return bare
}

func cloneBus(t *testing.T, bare string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	if out, err := git(filepath.Dir(dir), "clone", "--quiet", bare, dir); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	if out, err := git(dir, "checkout", "-q", "-B", "main"); err != nil {
		t.Fatalf("checkout main: %v %s", err, out)
	}
	return dir
}

// noteText is a minimal valid note, so the git tests are about the transport and nothing
// else.
func noteText(from, subject, body string) string {
	return "From: " + from + "\nTo: Bo\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: " + subject + "\n\n" + body + "\n"
}

func TestCommitAndPushLandsANote(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "from-ada/a.md", noteText("Ada", "one", "body"))
	res, err := CommitAndPush(clone, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: one", "origin", "main", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.Attempts != 1 {
		t.Fatalf("res = %+v, want pushed on the first attempt", res)
	}
	out, err := git(bare, "show", "main:from-ada/a.md")
	if err != nil {
		t.Fatalf("the note is not on the remote: %v", err)
	}
	if !strings.Contains(out, "Subject: one") {
		t.Fatalf("the remote holds %q", out)
	}
	// The identity is the sender's, from the roster, and was passed with git -c rather
	// than written into any config.
	who, err := git(bare, "log", "-1", "--format=%an <%ae>", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(who) != "Ada <ada@example.com>" {
		t.Fatalf("the commit is authored by %q", strings.TrimSpace(who))
	}
}

// THE TEST THIS TOOL EXISTS FOR. Two senders push in the same moment. On the night the
// issue records, one of them was rejected and lost. Here, both must land, and neither may
// lose its note.
func TestTwoSendersPushingAtOnceBothLand(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	ada := cloneBus(t, bare)
	bo := cloneBus(t, bare)
	write(t, ada, "from-ada/a.md", noteText("Ada", "from ada", "body"))
	write(t, bo, "from-bo/b.md", noteText("Bo", "from bo", "body"))

	// Both commits are made BEFORE either push, so the two pushes genuinely race: whichever
	// arrives second is rejected by the remote and must recover inside the tool.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]PushResult, 2)
	errs := make([]error, 2)
	senders := []struct {
		dir, who, path string
	}{
		{ada, "Ada", "from-ada/a.md"},
		{bo, "Bo", "from-bo/b.md"},
	}
	for i, s := range senders {
		wg.Add(1)
		go func(i int, dir, who, path string) {
			defer wg.Done()
			<-start
			results[i], errs[i] = CommitAndPush(dir, testIdentity[who], []string{path}, who+": a note", "origin", "main", 8)
		}(i, s.dir, s.who, s.path)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s: %v", senders[i].who, err)
		}
		if !results[i].Pushed {
			t.Fatalf("%s: %+v, want pushed", senders[i].who, results[i])
		}
	}
	// Both notes are on the remote. Neither lost.
	for _, s := range senders {
		if _, err := git(bare, "cat-file", "-e", "main:"+s.path); err != nil {
			t.Fatalf("%s lost its note: %s is not on the remote", s.who, s.path)
		}
	}
	// And the loser retried rather than forcing: the winner's commit is still an ancestor.
	log, err := git(bare, "log", "--format=%s", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(log, ": a note") != 2 {
		t.Fatalf("the remote log holds:\n%s\nwant both notes", log)
	}
}

func TestPushGivesUpInsideTheBudgetAndSaysTheNoteIsNotOnTheBus(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	mine := cloneBus(t, bare)
	theirs := cloneBus(t, bare)

	write(t, mine, "from-ada/a.md", noteText("Ada", "mine", "body"))
	// Somebody else pushes first, so my push is rejected.
	write(t, theirs, "from-bo/b.md", noteText("Bo", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Bo"], []string{"from-bo/b.md"}, "bo: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	// One attempt is one push and no recovery.
	res, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: mine", "origin", "main", 1)
	if err == nil {
		t.Fatal("a push that could not land reported success")
	}
	if !strings.Contains(err.Error(), "was NOT pushed") {
		t.Fatalf("the refusal does not say the note is not on the bus: %v", err)
	}
	if res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if _, err := git(bare, "cat-file", "-e", "main:from-ada/a.md"); err == nil {
		t.Fatal("the note reached the remote after a reported failure")
	}
}

// NO CONFLICT MAY WEDGE A LINE, at the file the whole tool used to stop on. Two benches of
// one line append to that line's own RECEIPTS at the same end over one base -- an edit/edit
// -- and the rebase stops. The tool settles it by union, because a RECEIPTS is a log and
// two lines appended to a log are both true, and BOTH receipts land.
func TestTwoBenchesReceiptingAtOnceBothLand(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	a := cloneBus(t, bare)
	b := cloneBus(t, bare)

	write(t, a, "from-ada/RECEIPTS", "2026-09-07T00:01:00Z bo-aaaaaaaaaaaa\n")
	if _, err := CommitAndPush(a, testIdentity["Ada"], []string{"from-ada/RECEIPTS"}, "ada: receipt", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	// The second bench cannot see the first.
	write(t, b, "from-ada/RECEIPTS", "2026-09-07T00:02:00Z bo-bbbbbbbbbbbb\n")
	res, err := CommitAndPush(b, testIdentity["Ada"], []string{"from-ada/RECEIPTS"}, "ada: another receipt", "origin", "main", 3)
	if err != nil {
		t.Fatalf("a conflict this tool settles wedged the line instead: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	landed, err := git(bare, "show", "main:from-ada/RECEIPTS")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bo-aaaaaaaaaaaa", "bo-bbbbbbbbbbbb"} {
		if !strings.Contains(landed, want) {
			t.Fatalf("the union dropped a receipt; the remote holds:\n%s", landed)
		}
	}
	if strings.Contains(landed, "<<<") {
		t.Fatalf("conflict markers reached the bus:\n%s", landed)
	}
	assertSettled(t, b)
}

// The conflict surface the catalogue ADDED, and the one the scenario run found wedging a
// bench. Two benches of one lane sending DIFFERENT notes rebased clean before INDEX
// existed; once the catalogue was written in the note's own commit they collided on it, the
// tool aborted, and the person's own `git pull --rebase` then landed in a half-done rebase
// with `UU from-ada/INDEX` and nothing saying what to do. Both notes and both catalogue
// lines now land, and the checkout is left in a state a person can keep working in.
func TestTwoBenchesOfOneLaneSettleTheCatalogue(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	a := cloneBus(t, bare)
	b := cloneBus(t, bare)

	first := IndexEntry{ID: "ada-aaaaaaaaaaaa", Path: "from-ada/a.md", Date: "2026-09-07T00:01:00Z", To: []string{"Bo"}, Lane: "from-ada"}
	write(t, a, first.Path, noteText("Ada", "one", "body"))
	if err := AppendIndexLine(a, first); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitAndPush(a, testIdentity["Ada"], []string{first.Path, IndexPath("from-ada")}, "ada: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}

	// The second bench cannot see the first: a DIFFERENT note, at a different path, whose
	// only shared file is the lane's catalogue.
	second := IndexEntry{ID: "ada-bbbbbbbbbbbb", Path: "from-ada/b.md", Date: "2026-09-07T00:02:00Z", To: []string{"Bo"}, Lane: "from-ada"}
	write(t, b, second.Path, noteText("Ada", "two", "body"))
	if err := AppendIndexLine(b, second); err != nil {
		t.Fatal(err)
	}
	res, err := CommitAndPush(b, testIdentity["Ada"], []string{second.Path, IndexPath("from-ada")}, "ada: two", "origin", "main", 3)
	if err != nil {
		t.Fatalf("the catalogue conflict wedged the bench instead of being settled: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	// Both notes are on the bus, and the catalogue names both.
	for _, p := range []string{first.Path, second.Path} {
		if _, cerr := git(bare, "cat-file", "-e", "main:"+p); cerr != nil {
			t.Fatalf("%s is not on the remote", p)
		}
	}
	landed, err := git(bare, "show", "main:"+IndexPath("from-ada"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{first.ID, second.ID} {
		if !strings.Contains(landed, want) {
			t.Fatalf("the catalogue lost %s:\n%s", want, landed)
		}
	}
	assertSettled(t, b)
}

// A CURSOR is a REPLACE and not an append, so a union of two of them would be nonsense.
// Two benches of one reader advancing at once are settled by taking the further read -- the
// cursor whose commit is a descendant of the other -- and the OPEN list is taken from that
// same side, because an open list belongs to a cursor and merging two would carry notes the
// winning read has already closed.
func TestTwoBenchesOfOneReaderSettleTheCursor(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	a := cloneBus(t, bare)
	b := cloneBus(t, bare)

	// A commit on the bus both benches can see, and a second only the first has read to.
	write(t, a, "from-bo/a.md", noteText("Bo", "one", "body"))
	if _, err := CommitAndPush(a, testIdentity["Bo"], []string{"from-bo/a.md"}, "bo: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	behind, err := HeadCommit(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git(a, "fetch", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	ahead, err := HeadCommit(a)
	if err != nil {
		t.Fatal(err)
	}

	// The FURTHER read, pushed first.
	if err := WriteCursor(a, "from-ada", ahead, 1, "", at("2026-09-09T12:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := WriteOpen(a, "from-ada", []OpenEntry{{ID: "bo-aaaaaaaaaaaa", Kind: OpenNote, From: "Bo", Addr: "to", Path: "from-bo/a.md", Subject: "one"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitAndPush(a, testIdentity["Ada"], []string{CursorPath("from-ada"), OpenPath("from-ada")}, "ada: read to "+ahead[:12], "origin", "main", 3); err != nil {
		t.Fatal(err)
	}

	// The bench that is BEHIND, which cannot see any of that, pushes its own read second.
	if err := WriteCursor(b, "from-ada", behind, 0, "", at("2026-09-09T11:00:00Z")); err != nil {
		t.Fatal(err)
	}
	res, err := CommitAndPush(b, testIdentity["Ada"], []string{CursorPath("from-ada")}, "ada: read to "+behind[:12], "origin", "main", 3)
	if err != nil {
		t.Fatalf("two benches of one reader wedged the line: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	landedCursor, err := git(bare, "show", "main:"+CursorPath("from-ada"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(landedCursor, ahead) {
		t.Fatalf("the settlement kept the SHORTER read; a cursor that goes backwards re-opens everything between the two:\n%s", landedCursor)
	}
	// And the open list came with it, rather than being merged with the other side's.
	landedOpen, err := git(bare, "show", "main:"+OpenPath("from-ada"))
	if err != nil {
		t.Fatalf("the winning cursor's open list is not on the bus: %v", err)
	}
	if !strings.Contains(landedOpen, "from-bo/a.md") {
		t.Fatalf("the open list does not belong to the cursor beside it:\n%s", landedOpen)
	}
	assertSettled(t, b)
}

// The one conflict this tool will NOT settle, and the refusal it gives instead. Two benches
// wrote the SAME note path, which means they sent the same note in the same second and were
// assigned one id; which of the two is the note is a person's decision. The rebase is
// aborted, the commit is left on the branch, and the checkout is usable.
func TestAConflictOnANoteIsRefusedAndTheAbortIsClean(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	a := cloneBus(t, bare)
	b := cloneBus(t, bare)

	write(t, a, "from-ada/same.md", noteText("Ada", "one", "the first bench wrote this"))
	if _, err := CommitAndPush(a, testIdentity["Ada"], []string{"from-ada/same.md"}, "ada: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, b, "from-ada/same.md", noteText("Ada", "one", "the second bench wrote this"))
	res, err := CommitAndPush(b, testIdentity["Ada"], []string{"from-ada/same.md"}, "ada: one again", "origin", "main", 3)
	if err == nil {
		t.Fatal("two benches writing one note path was settled; which of the two is the note is not this tool's decision")
	}
	for _, want := range []string{"conflicted", "from-ada/same.md", "was NOT pushed", "git pull --rebase"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// The reason is ONE LINE and the transcript is carried beside it, not inside it.
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("the refusal is more than one line: %q", err.Error())
	}
	if Transcript(err) == "" {
		t.Fatal("the refusal carries no transcript, so a person has git's own words nowhere")
	}
	if res.Commit == "" {
		t.Fatal("the refusal names no commit, so a person has nothing to look at")
	}
	if _, cerr := git(b, "cat-file", "-e", res.Commit+"^{commit}"); cerr != nil {
		t.Fatalf("the commit named in the refusal is not on the branch: %v", cerr)
	}
	assertSettled(t, b)
	// And the first bench's note is untouched on the bus.
	landed, err := git(bare, "show", "main:from-ada/same.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(landed, "the first bench wrote this") {
		t.Fatalf("the conflicting note overwrote the one on the bus:\n%s", landed)
	}
}

// assertSettled is the state every one of the tests above ends in: on a branch, no rebase
// in progress under either backend, and a clean tree. A bench left in any other state is a
// bench a person has to rescue, which is the wedge these tests are about.
func assertSettled(t *testing.T, dir string) {
	t.Helper()
	if _, err := CurrentBranch(dir); err != nil {
		t.Fatalf("the checkout was left mid-rebase: %v", err)
	}
	gd, err := GitDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, statErr := os.Stat(filepath.Join(gd, name)); !os.IsNotExist(statErr) {
			t.Fatalf("a rebase is still in progress (%s)", name)
		}
	}
	if err := EnsureClean(dir, nil); err != nil {
		t.Fatalf("the checkout was left dirty: %v", err)
	}
}

func TestEnsureCleanRefusesAnUnrelatedChange(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "stray.txt", "something else in flight\n")
	err := EnsureClean(clone, []string{"from-ada/a.md"})
	if err == nil {
		t.Fatal("a checkout holding unrelated work passed; a rebase over it would sweep somebody's work into a note")
	}
	if !strings.Contains(err.Error(), "stray.txt") {
		t.Fatalf("the refusal does not name the file in the way: %v", err)
	}
	if err := os.Remove(filepath.Join(clone, "stray.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-ada/a.md", noteText("Ada", "one", "body"))
	if err := EnsureClean(clone, []string{"from-ada/a.md"}); err != nil {
		t.Fatalf("the note being sent was treated as unrelated work: %v", err)
	}
}

func TestCommitOnlyDoesNotPush(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "from-ada/a.md", noteText("Ada", "one", "body"))
	res, err := CommitOnly(clone, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: one")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || res.Commit == "" {
		t.Fatalf("res = %+v, want a commit and pushed=false", res)
	}
	if _, err := git(bare, "cat-file", "-e", "main:from-ada/a.md"); err == nil {
		t.Fatal("--no-push pushed")
	}
}

func TestCommitRefusesWithoutAnIdentityOrPaths(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	if _, err := CommitOnly(clone, Identity{}, []string{"x"}, "m"); err == nil {
		t.Fatal("a commit with no identity was allowed")
	}
	if _, err := CommitOnly(clone, testIdentity["Ada"], nil, "m"); err == nil {
		t.Fatal("a commit with no paths was allowed")
	}
	if _, err := CommitAndPush(clone, testIdentity["Ada"], []string{"x"}, "m", "origin", "main", 0); err == nil {
		t.Fatal("an attempt budget of zero was accepted")
	}
}

func TestIsRepoRootAndCurrentBranch(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	if err := IsRepoRoot(clone); err != nil {
		t.Fatal(err)
	}
	if b, err := CurrentBranch(clone); err != nil || b != "main" {
		t.Fatalf("CurrentBranch = %q %v", b, err)
	}
	if err := IsRepoRoot(t.TempDir()); err == nil {
		t.Fatal("a directory that is not a repository passed IsRepoRoot")
	}
}

// THE BLOCKER, at the level it is decided. A bus one directory down inside a bigger
// repository is INSIDE a work tree, so the old test passed it -- and then `git diff
// --name-only` reported `bus/from-bo/x.md`, the from- guard dropped it, and the run
// said "nothing new" over unread notes. The refusal names the root git found, because that
// is the one thing the caller needs in order to fix the invocation.
func TestABusThatIsNotTheRepositoryRootIsRefused(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	nested := filepath.Join(clone, "bus")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, nested, ConfigName, rosterJSON)
	err := IsRepoRoot(nested)
	if err == nil {
		t.Fatal("a bus one directory inside a repository passed; a diff from its cursor would name paths it then drops, and report nothing new over unread notes")
	}
	if !strings.Contains(err.Error(), "is not its root") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	// And it names the root, so the caller can point --bus at it.
	top, gerr := resolved(clone)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if !strings.Contains(err.Error(), top) {
		t.Fatalf("the refusal does not name the repository root %q: %v", top, err)
	}
	// The change set the old code would have reported over that bus, for the record: git
	// names the path from the repository root, and ChangedSince keeps only from-* paths.
	write(t, nested, "from-bo/a.md", noteText("Bo", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Bo"], []string{"bus"}, "bo: a nested note", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	base, err := ResolveCommit(clone, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ChangedSince(clone, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Fatalf("the nested note was reported as a lane path: %v", changed)
	}
}

// THE SECOND TEST THIS TOOL EXISTS FOR, and the one the concurrent test above cannot
// promise. Two goroutines racing may serialize, in which case neither ever recovers and a
// broken retry loop still passes. Here the collision is arranged: somebody else's commit
// is already on the remote before mine is made, so my first push MUST be rejected and my
// second MUST succeed, and the attempt count is the proof that the recovery ran.
func TestARejectedPushRecoversOnTheSecondAttempt(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	mine := cloneBus(t, bare)
	theirs := cloneBus(t, bare)

	write(t, theirs, "from-bo/b.md", noteText("Bo", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Bo"], []string{"from-bo/b.md"}, "bo: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, mine, "from-ada/a.md", noteText("Ada", "mine", "body"))
	res, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: mine", "origin", "main", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	if res.Attempts != 2 {
		t.Fatalf("res.Attempts = %d, want 2: the first push must be rejected and the second must land, or the retry loop is not what landed this", res.Attempts)
	}
	// Both notes are on the remote, and the rebase replayed mine on top rather than over.
	for _, path := range []string{"from-ada/a.md", "from-bo/b.md"} {
		if _, err := git(bare, "cat-file", "-e", "main:"+path); err != nil {
			t.Fatalf("%s is not on the remote", path)
		}
	}
	// A rebase REPLAYS a commit, which writes a new commit object, which git will not do
	// without a committer. That identity must be the sender's, from the roster -- not the
	// machine's, and not an error on a bench whose git cannot guess one at all, which is
	// what a CI runner is and how this was found.
	who, err := git(bare, "log", "-1", "--format=%an <%ae>|%cn <%ce>", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Ada <ada@example.com>|Ada <ada@example.com>"; strings.TrimSpace(who) != want {
		t.Fatalf("the replayed commit is author|committer %q, want %q: the identity comes from the roster on every invocation that records one", strings.TrimSpace(who), want)
	}
}

// A push publishes the BRANCH. A checkout carrying commits this tool did not make would
// send those to the bus under a note's name, so the run refuses before it stages
// anything and says how many are in the way -- and the advice it gives is advice that
// WORKS. A bare `git push` was what it used to say, and a bare push against a remote that
// has moved is rejected exactly as this tool's own was.
func TestSendRefusesABranchAheadOfTheRemoteWithSomebodyElsesWork(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	// Level with the remote: nothing to refuse.
	if err := EnsureLevelWith(clone, "origin", "main"); err != nil {
		t.Fatalf("a checkout level with its remote was refused: %v", err)
	}
	// Somebody's unrelated work, committed here by hand -- so it carries no trailer -- and
	// not pushed.
	write(t, clone, "notes-to-self.txt", "half a thought\n")
	commitByHand(t, clone, "notes-to-self.txt", "wip")
	err := EnsureLevelWith(clone, "origin", "main")
	if err == nil {
		t.Fatal("a branch holding an unrelated local commit passed; a push would have published it")
	}
	for _, want := range []string{"ahead of origin/main", "by 1 commits", "1 of which the tool did not make", "git pull --rebase && git push"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// THE ADVICE HAS TO WORK. Somebody else pushes while this checkout is ahead, which is
	// the state a bare `git push` cannot get out of -- and the sentence's own recovery does.
	other := cloneBus(t, bare)
	write(t, other, "from-bo/b.md", noteText("Bo", "theirs", "body"))
	if _, err := CommitAndPush(other, testIdentity["Bo"], []string{"from-bo/b.md"}, "bo: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if out, perr := git(clone, "push", "origin", "HEAD:refs/heads/main"); perr == nil {
		t.Fatalf("the fixture is not the state the advice is about: a bare push succeeded\n%s", out)
	}
	if out, rerr := git(clone, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "pull", "--rebase", "origin", "main"); rerr != nil {
		t.Fatalf("`git pull --rebase`, which the refusal recommends, failed: %v\n%s", rerr, out)
	}
	if out, perr := git(clone, "push", "origin", "HEAD:refs/heads/main"); perr != nil {
		t.Fatalf("`git push` after the rebase, which the refusal recommends, failed: %v\n%s", perr, out)
	}
	if err := EnsureLevelWith(clone, "origin", "main"); err != nil {
		t.Fatalf("the advice ran to the end and the checkout is still refused: %v", err)
	}
}

// THE WEDGE, closed. A push this tool could not land leaves its own commit on the branch,
// and the branch-ahead guard used to refuse the next run over it: "1 commits the tool did
// not make", about a commit the tool had made. Three of five lines were stuck there in the
// scenario run, each needing a person to push by hand -- which is the failure the whole
// tool exists to end, arriving from inside it. The trailer is how a run knows its own work,
// and the next push CARRIES it.
func TestAnUnpushedCommitOfOurOwnIsCarriedRatherThanRefused(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	mine := cloneBus(t, bare)
	theirs := cloneBus(t, bare)

	// A push that cannot land: somebody else got there first and the budget is one attempt.
	write(t, theirs, "from-bo/b.md", noteText("Bo", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Bo"], []string{"from-bo/b.md"}, "bo: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, mine, "from-ada/a.md", noteText("Ada", "mine", "body"))
	lost, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/a.md"},
		WithTrailer("ada: mine", TrailerSend+" ada-aaaaaaaaaaaa"), "origin", "main", 1)
	if err == nil {
		t.Fatal("the fixture did not lose its push")
	}
	if !strings.Contains(err.Error(), "git pull --rebase && git push") {
		t.Fatalf("the refusal offers no recovery that works: %v", err)
	}
	if lost.Commit == "" {
		t.Fatal("the lost push named no commit")
	}

	// The next run. This is where the tool used to refuse to run at all.
	if err := EnsureLevelWith(mine, "origin", "main"); err != nil {
		t.Fatalf("the guard refused this tool's OWN unpushed commit, which is the wedge: %v", err)
	}
	write(t, mine, "from-ada/c.md", noteText("Ada", "next", "body"))
	res, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/c.md"},
		WithTrailer("ada: next", TrailerSend+" ada-cccccccccccc"), "origin", "main", 25)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	// BOTH notes are on the bus: the one whose push was lost was carried by this one.
	for _, p := range []string{"from-ada/a.md", "from-ada/c.md", "from-bo/b.md"} {
		if _, cerr := git(bare, "cat-file", "-e", "main:"+p); cerr != nil {
			t.Fatalf("%s is not on the remote; the lost note was not carried", p)
		}
	}
}

// commitByHand makes a commit the way a PERSON does: raw git, no trailer, so the
// branch-ahead guard sees work this tool did not make.
func commitByHand(t *testing.T, dir, path, message string) {
	t.Helper()
	if out, err := git(dir, "add", "--", path); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := git(dir, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit", "-q", "-m", message); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
}

// Every commit this tool makes carries the trailer, whether or not the caller named one --
// which is what makes the guard above able to tell its own work from a person's.
func TestEveryCommitCarriesTheTrailer(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "from-ada/a.md", noteText("Ada", "one", "body"))
	if _, err := CommitOnly(clone, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: one"); err != nil {
		t.Fatal(err)
	}
	body, err := git(clone, "log", "-1", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	if !HasTrailer(body) {
		t.Fatalf("a commit made through this tool carries no %s trailer:\n%s", TrailerKey, body)
	}
	// A caller that named its own keeps it, and it is not doubled.
	write(t, clone, "from-ada/b.md", noteText("Ada", "two", "body"))
	if _, err := CommitOnly(clone, testIdentity["Ada"], []string{"from-ada/b.md"}, WithTrailer("ada: two", TrailerSend+" ada-bbbbbbbbbbbb")); err != nil {
		t.Fatal(err)
	}
	body, err = git(clone, "log", "-1", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(body, TrailerKey+":"); n != 1 {
		t.Fatalf("the message carries %d trailers, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, TrailerSend+" ada-bbbbbbbbbbbb") {
		t.Fatalf("the caller's own trailer was replaced:\n%s", body)
	}
	// And a message with no trailer is not one of ours.
	if HasTrailer("ada: a note somebody wrote by hand") {
		t.Fatal("a message with no trailer was read as this tool's own")
	}
}

// The two flags that become git's own argv. A value beginning with a dash is an OPTION to
// git, not a name, and this tool would run it.
func TestRemoteAndBranchAreRefusedWhenTheyCouldBeOptions(t *testing.T) {
	hermetic(t)
	bad := []string{"--upload-pack=touch /tmp/pwned", "-o", "origin;rm -rf /", "origin main", "ori\ngin", ""}
	for _, s := range bad {
		if err := ValidGitArg("remote", s); err == nil {
			t.Fatalf("--remote %q was accepted", s)
		}
		if err := ValidGitArg("branch", s); err == nil {
			t.Fatalf("--branch %q was accepted", s)
		}
	}
	for _, s := range []string{"origin", "main", "release/2026.09", "a_b-c.d", "upstream2"} {
		if err := ValidGitArg("remote", s); err != nil {
			t.Fatalf("%q is a name a bus uses and was refused: %v", s, err)
		}
	}
	// And the package refuses it too, at the last place these become argv.
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "from-ada/a.md", noteText("Ada", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Ada"], []string{"from-ada/a.md"}, "m", "--upload-pack=id", "main", 3); err == nil {
		t.Fatal("CommitAndPush accepted a remote that is an option to git")
	}
}

// A rename is TWO records in `git status --porcelain -z`: the new path with its status
// bytes, and then the old path bare. A reader that strips three characters off every
// record reports a file that does not exist -- or, when the old path is short, drops the
// change entirely and calls a dirty checkout clean.
func TestEnsureCleanReadsARenameAsThePairItIs(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	write(t, clone, "from-ada/before.md", noteText("Ada", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Ada"], []string{"from-ada/before.md"}, "ada: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if out, err := git(clone, "mv", "from-ada/before.md", "from-ada/after.md"); err != nil {
		t.Fatalf("git mv: %v %s", err, out)
	}
	err := EnsureClean(clone, []string{"from-ada/a-note-being-sent.md"})
	if err == nil {
		t.Fatal("a checkout holding a rename passed as clean")
	}
	// One change, named as the pair it is: both halves, in one entry, with nothing eaten
	// off the front of the old path.
	if !strings.HasSuffix(err.Error(), ": from-ada/before.md -> from-ada/after.md") {
		t.Fatalf("the refusal does not name the rename as one change over two paths: %v", err)
	}
	if strings.Contains(err.Error(), ",") {
		t.Fatalf("the old path was read as a second, separate change: %v", err)
	}
	// A short old path is the case that used to vanish: a record under four characters is
	// skipped, so the rename was reported as clean.
	if out, err := git(clone, "mv", "from-ada/after.md", "from-ada/before.md"); err != nil {
		t.Fatalf("git mv back: %v %s", err, out)
	}
	write(t, clone, "x.md", "x\n")
	if _, err := CommitAndPush(clone, testIdentity["Ada"], []string{"x.md"}, "ada: x", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if out, err := git(clone, "mv", "x.md", "y.md"); err != nil {
		t.Fatalf("git mv: %v %s", err, out)
	}
	if err := EnsureClean(clone, nil); err == nil {
		t.Fatal("a rename whose old path is three characters long was reported as a clean checkout")
	}
}

// THE RETRY LOOP WAITS BETWEEN ATTEMPTS, and it did not before. Every retry here was
// started by somebody else's push landing first, so the two lines are in step by
// construction: they fetch, rebase and push again together. A loop with no wait in it
// turns one lost race into a run of them at whatever rate the machine can fetch, and two
// benches that wait the SAME amount are still in step -- so the wait is randomised as
// well as bounded.
//
// The sleeper is injected because the assertion is about spacing and not about waiting: a
// test that actually slept would be a test that takes a second to say what a recorded
// duration says at once.
func TestThePushRetryWaitsBetweenAttempts(t *testing.T) {
	hermetic(t)
	var slept []time.Duration
	real := sleepBetweenAttempts
	sleepBetweenAttempts = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { sleepBetweenAttempts = real })

	bare := bareBus(t)
	mine := cloneBus(t, bare)
	theirs := cloneBus(t, bare)
	write(t, theirs, "from-bo/b.md", noteText("Bo", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Bo"], []string{"from-bo/b.md"}, "bo: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, mine, "from-ada/a.md", noteText("Ada", "mine", "body"))
	res, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/a.md"}, "ada: mine", "origin", "main", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.Attempts != 2 {
		t.Fatalf("res = %+v, want pushed on the second attempt", res)
	}
	// One rejected push, so one wait: before the fetch that takes what arrived, and never
	// after the push that lands.
	if len(slept) != 1 {
		t.Fatalf("the loop waited %d times for one rejected push, want 1: %v", len(slept), slept)
	}
	if slept[0] < backoffStep || slept[0] > backoffStep+backoffJitter {
		t.Fatalf("waited %v after the first attempt, want between %v and %v", slept[0], backoffStep, backoffStep+backoffJitter)
	}
}

// The backoff itself: it grows with the attempt, it is never the same twice in a row for
// long, and it is capped so that a retry budget stays a person's wait rather than a
// schedule.
func TestPushBackoffGrowsIsJitteredAndIsCapped(t *testing.T) {
	for _, attempt := range []int{1, 2, 3, 8} {
		lo := time.Duration(attempt) * backoffStep
		for range 50 {
			d := pushBackoff(attempt)
			if d < lo || d > lo+backoffJitter || d > backoffCap {
				t.Fatalf("pushBackoff(%d) = %v, want between %v and %v and at most %v", attempt, d, lo, lo+backoffJitter, backoffCap)
			}
		}
	}
	// The cap holds however many attempts a caller asks for.
	for _, attempt := range []int{20, 1000} {
		if d := pushBackoff(attempt); d != backoffCap {
			t.Fatalf("pushBackoff(%d) = %v, want the cap %v", attempt, d, backoffCap)
		}
	}
	// And it is jittered: fifty draws at one attempt are not one value. (The chance of a
	// false failure is the chance that fifty draws from 200ms of nanoseconds coincide.)
	seen := map[time.Duration]bool{}
	for range 50 {
		seen[pushBackoff(1)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("fifty draws gave %d distinct waits; a fixed delay leaves two benches that collided colliding again", len(seen))
	}
}
