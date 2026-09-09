package bus

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
	"Rowan":  {Name: "Rowan", Email: "rowan@mas-bandwidth.com"},
	"Stella": {Name: "Stella", Email: "stella@mas-bandwidth.com"},
}

// bareTable builds a bare repository holding a table with a roster on branch main, and
// returns its path. It is the remote every clone below pushes to.
func bareTable(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "table.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := git(bare, "init", "--bare", "--initial-branch=main"); err != nil {
		t.Fatalf("init --bare: %v %s", err, out)
	}
	seed := cloneTable(t, bare)
	write(t, seed, ConfigName, rosterJSON)
	if _, err := CommitAndPush(seed, testIdentity["Rowan"], []string{ConfigName}, "the roster", "origin", "main", 3); err != nil {
		t.Fatalf("seeding the table: %v", err)
	}
	return bare
}

func cloneTable(t *testing.T, bare string) string {
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
	return "From: " + from + "\nTo: Stella\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: " + subject + "\n\n" + body + "\n"
}

func TestCommitAndPushLandsANote(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	write(t, clone, "from-rowan/a.md", noteText("Rowan", "one", "body"))
	res, err := CommitAndPush(clone, testIdentity["Rowan"], []string{"from-rowan/a.md"}, "rowan: one", "origin", "main", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.Attempts != 1 {
		t.Fatalf("res = %+v, want pushed on the first attempt", res)
	}
	out, err := git(bare, "show", "main:from-rowan/a.md")
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
	if strings.TrimSpace(who) != "Rowan <rowan@mas-bandwidth.com>" {
		t.Fatalf("the commit is authored by %q", strings.TrimSpace(who))
	}
}

// THE TEST THIS TOOL EXISTS FOR. Two senders push in the same moment. On the night the
// issue records, one of them was rejected and lost. Here, both must land, and neither may
// lose its note.
func TestTwoSendersPushingAtOnceBothLand(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	rowan := cloneTable(t, bare)
	stella := cloneTable(t, bare)
	write(t, rowan, "from-rowan/a.md", noteText("Rowan", "from rowan", "body"))
	write(t, stella, "from-stella/b.md", noteText("Stella", "from stella", "body"))

	// Both commits are made BEFORE either push, so the two pushes genuinely race: whichever
	// arrives second is rejected by the remote and must recover inside the tool.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]PushResult, 2)
	errs := make([]error, 2)
	senders := []struct {
		dir, who, path string
	}{
		{rowan, "Rowan", "from-rowan/a.md"},
		{stella, "Stella", "from-stella/b.md"},
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

func TestPushGivesUpInsideTheBudgetAndSaysTheNoteIsNotOnTheTable(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	mine := cloneTable(t, bare)
	theirs := cloneTable(t, bare)

	write(t, mine, "from-rowan/a.md", noteText("Rowan", "mine", "body"))
	// Somebody else pushes first, so my push is rejected.
	write(t, theirs, "from-stella/b.md", noteText("Stella", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Stella"], []string{"from-stella/b.md"}, "stella: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	// One attempt is one push and no recovery.
	res, err := CommitAndPush(mine, testIdentity["Rowan"], []string{"from-rowan/a.md"}, "rowan: mine", "origin", "main", 1)
	if err == nil {
		t.Fatal("a push that could not land reported success")
	}
	if !strings.Contains(err.Error(), "was NOT pushed") {
		t.Fatalf("the refusal does not say the note is not on the table: %v", err)
	}
	if res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if _, err := git(bare, "cat-file", "-e", "main:from-rowan/a.md"); err == nil {
		t.Fatal("the note reached the remote after a reported failure")
	}
}

// The one case the rebase must not paper over: a real conflict. On this layout that is
// always two sessions of ONE line from two benches -- appending to that line's own
// RECEIPTS, as here, or writing the same note path when both benches sent the same note in
// the same second. The tool aborts, says so, and leaves the checkout usable.
func TestARebaseConflictAbortsAndSaysSo(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	a := cloneTable(t, bare)
	b := cloneTable(t, bare)

	write(t, a, "from-rowan/RECEIPTS", "2026-09-07T00:01:00Z stella-aaaaaaaaaaaa\n")
	if _, err := CommitAndPush(a, testIdentity["Rowan"], []string{"from-rowan/RECEIPTS"}, "rowan: receipt", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, b, "from-rowan/RECEIPTS", "2026-09-07T00:02:00Z stella-bbbbbbbbbbbb\n")
	_, err := CommitAndPush(b, testIdentity["Rowan"], []string{"from-rowan/RECEIPTS"}, "rowan: another receipt", "origin", "main", 3)
	if err == nil {
		t.Fatal("a conflicting rebase reported success")
	}
	if !strings.Contains(err.Error(), "conflicted") {
		t.Fatalf("the refusal does not name the conflict: %v", err)
	}
	// The rebase was aborted, so the checkout is on a branch and not mid-rebase.
	if _, err := CurrentBranch(b); err != nil {
		t.Fatalf("the checkout was left mid-rebase: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(b, ".git", "rebase-merge")); !os.IsNotExist(statErr) {
		t.Fatal("a rebase is still in progress after the abort")
	}
}

// The conflict surface the catalogue ADDED, pinned so nothing claims it away again. Two
// benches of one lane sending DIFFERENT notes rebased clean before INDEX existed: the note
// paths differ and nothing else was touched. Now both append a line at the end of one file
// over one base, which is an edit/edit, and the rebase stops. Append-only is not
// conflict-free, and the outcome is the one the protocol already states: abort, the commit
// left on the branch, exit 1, a person decides.
func TestTwoBenchesOfOneLaneConflictOnTheCatalogue(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	a := cloneTable(t, bare)
	b := cloneTable(t, bare)

	first := IndexEntry{ID: "rowan-aaaaaaaaaaaa", Path: "from-rowan/a.md", Date: "2026-09-07T00:01:00Z", To: []string{"Stella"}, Lane: "from-rowan"}
	write(t, a, first.Path, noteText("Rowan", "one", "body"))
	if err := AppendIndexLine(a, first); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitAndPush(a, testIdentity["Rowan"], []string{first.Path, IndexPath("from-rowan")}, "rowan: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}

	// The second bench cannot see the first: a DIFFERENT note, at a different path, whose
	// only shared file is the lane's catalogue.
	second := IndexEntry{ID: "rowan-bbbbbbbbbbbb", Path: "from-rowan/b.md", Date: "2026-09-07T00:02:00Z", To: []string{"Stella"}, Lane: "from-rowan"}
	write(t, b, second.Path, noteText("Rowan", "two", "body"))
	if err := AppendIndexLine(b, second); err != nil {
		t.Fatal(err)
	}
	res, err := CommitAndPush(b, testIdentity["Rowan"], []string{second.Path, IndexPath("from-rowan")}, "rowan: two", "origin", "main", 3)
	if err == nil {
		t.Fatal("two benches of one lane appending different catalogue lines did not conflict; if this is now true the SPEC's list of conflict surfaces is wrong in the other direction")
	}
	if !strings.Contains(err.Error(), "conflicted") || !strings.Contains(err.Error(), "was NOT pushed") {
		t.Fatalf("the refusal does not name the conflict and say the note is not on the table: %v", err)
	}
	// The abort is CLEAN: on a branch, no rebase in progress, no conflict markers left in
	// the working tree, and this bench's own commit still there to be dealt with.
	if _, berr := CurrentBranch(b); berr != nil {
		t.Fatalf("the checkout was left mid-rebase: %v", berr)
	}
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		if _, statErr := os.Stat(filepath.Join(b, ".git", dir)); !os.IsNotExist(statErr) {
			t.Fatalf("a rebase is still in progress after the abort (.git/%s)", dir)
		}
	}
	if err := EnsureClean(b, nil); err != nil {
		t.Fatalf("the abort left the checkout dirty: %v", err)
	}
	if res.Commit == "" {
		t.Fatal("the refusal names no commit, so a person has nothing to look at")
	}
	if _, cerr := git(b, "cat-file", "-e", res.Commit+"^{commit}"); cerr != nil {
		t.Fatalf("the commit named in the refusal is not on the branch: %v", cerr)
	}
	// And the first bench's note is still the only one on the table: nothing was lost and
	// nothing was overwritten.
	if _, cerr := git(bare, "cat-file", "-e", "main:"+second.Path); cerr == nil {
		t.Fatal("the conflicting note reached the remote")
	}
}

func TestEnsureCleanRefusesAnUnrelatedChange(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	write(t, clone, "stray.txt", "something else in flight\n")
	err := EnsureClean(clone, []string{"from-rowan/a.md"})
	if err == nil {
		t.Fatal("a checkout holding unrelated work passed; a rebase over it would sweep somebody's work into a note")
	}
	if !strings.Contains(err.Error(), "stray.txt") {
		t.Fatalf("the refusal does not name the file in the way: %v", err)
	}
	if err := os.Remove(filepath.Join(clone, "stray.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-rowan/a.md", noteText("Rowan", "one", "body"))
	if err := EnsureClean(clone, []string{"from-rowan/a.md"}); err != nil {
		t.Fatalf("the note being sent was treated as unrelated work: %v", err)
	}
}

func TestCommitOnlyDoesNotPush(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	write(t, clone, "from-rowan/a.md", noteText("Rowan", "one", "body"))
	res, err := CommitOnly(clone, testIdentity["Rowan"], []string{"from-rowan/a.md"}, "rowan: one")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || res.Commit == "" {
		t.Fatalf("res = %+v, want a commit and pushed=false", res)
	}
	if _, err := git(bare, "cat-file", "-e", "main:from-rowan/a.md"); err == nil {
		t.Fatal("--no-push pushed")
	}
}

func TestCommitRefusesWithoutAnIdentityOrPaths(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	if _, err := CommitOnly(clone, Identity{}, []string{"x"}, "m"); err == nil {
		t.Fatal("a commit with no identity was allowed")
	}
	if _, err := CommitOnly(clone, testIdentity["Rowan"], nil, "m"); err == nil {
		t.Fatal("a commit with no paths was allowed")
	}
	if _, err := CommitAndPush(clone, testIdentity["Rowan"], []string{"x"}, "m", "origin", "main", 0); err == nil {
		t.Fatal("an attempt budget of zero was accepted")
	}
}

func TestIsRepoRootAndCurrentBranch(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
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

// THE BLOCKER, at the level it is decided. A table one directory down inside a bigger
// repository is INSIDE a work tree, so the old test passed it -- and then `git diff
// --name-only` reported `table/from-stella/x.md`, the from- guard dropped it, and the run
// said "nothing new" over unread notes. The refusal names the root git found, because that
// is the one thing the caller needs in order to fix the invocation.
func TestATableThatIsNotTheRepositoryRootIsRefused(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	nested := filepath.Join(clone, "table")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, nested, ConfigName, rosterJSON)
	err := IsRepoRoot(nested)
	if err == nil {
		t.Fatal("a table one directory inside a repository passed; a diff from its cursor would name paths it then drops, and report nothing new over unread notes")
	}
	if !strings.Contains(err.Error(), "is not its root") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	// And it names the root, so the caller can point --table at it.
	top, gerr := resolved(clone)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if !strings.Contains(err.Error(), top) {
		t.Fatalf("the refusal does not name the repository root %q: %v", top, err)
	}
	// The change set the old code would have reported over that table, for the record: git
	// names the path from the repository root, and ChangedSince keeps only from-* paths.
	write(t, nested, "from-stella/a.md", noteText("Stella", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Stella"], []string{"table"}, "stella: a nested note", "origin", "main", 3); err != nil {
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
	bare := bareTable(t)
	mine := cloneTable(t, bare)
	theirs := cloneTable(t, bare)

	write(t, theirs, "from-stella/b.md", noteText("Stella", "theirs", "body"))
	if _, err := CommitAndPush(theirs, testIdentity["Stella"], []string{"from-stella/b.md"}, "stella: theirs", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	write(t, mine, "from-rowan/a.md", noteText("Rowan", "mine", "body"))
	res, err := CommitAndPush(mine, testIdentity["Rowan"], []string{"from-rowan/a.md"}, "rowan: mine", "origin", "main", 3)
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
	for _, path := range []string{"from-rowan/a.md", "from-stella/b.md"} {
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
	if want := "Rowan <rowan@mas-bandwidth.com>|Rowan <rowan@mas-bandwidth.com>"; strings.TrimSpace(who) != want {
		t.Fatalf("the replayed commit is author|committer %q, want %q: the identity comes from the roster on every invocation that records one", strings.TrimSpace(who), want)
	}
}

// A push publishes the BRANCH. A checkout carrying commits this tool did not make would
// send those to the table under a note's name, so the run refuses before it stages
// anything and says how many are in the way.
func TestSendRefusesABranchAheadOfTheRemote(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)

	// Level with the remote: nothing to refuse.
	if err := EnsureLevelWith(clone, "origin", "main"); err != nil {
		t.Fatalf("a checkout level with its remote was refused: %v", err)
	}
	// Somebody's unrelated work, committed here and not pushed.
	write(t, clone, "notes-to-self.txt", "half a thought\n")
	if _, err := CommitOnly(clone, testIdentity["Rowan"], []string{"notes-to-self.txt"}, "wip"); err != nil {
		t.Fatal(err)
	}
	err := EnsureLevelWith(clone, "origin", "main")
	if err == nil {
		t.Fatal("a branch holding an unrelated local commit passed; a push would have published it")
	}
	for _, want := range []string{"ahead of origin/main", "by 1 commits", "push or drop them first"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
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
			t.Fatalf("%q is a name a table uses and was refused: %v", s, err)
		}
	}
	// And the package refuses it too, at the last place these become argv.
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	write(t, clone, "from-rowan/a.md", noteText("Rowan", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Rowan"], []string{"from-rowan/a.md"}, "m", "--upload-pack=id", "main", 3); err == nil {
		t.Fatal("CommitAndPush accepted a remote that is an option to git")
	}
}

// A rename is TWO records in `git status --porcelain -z`: the new path with its status
// bytes, and then the old path bare. A reader that strips three characters off every
// record reports a file that does not exist -- or, when the old path is short, drops the
// change entirely and calls a dirty checkout clean.
func TestEnsureCleanReadsARenameAsThePairItIs(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)
	write(t, clone, "from-rowan/before.md", noteText("Rowan", "one", "body"))
	if _, err := CommitAndPush(clone, testIdentity["Rowan"], []string{"from-rowan/before.md"}, "rowan: one", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if out, err := git(clone, "mv", "from-rowan/before.md", "from-rowan/after.md"); err != nil {
		t.Fatalf("git mv: %v %s", err, out)
	}
	err := EnsureClean(clone, []string{"from-rowan/a-note-being-sent.md"})
	if err == nil {
		t.Fatal("a checkout holding a rename passed as clean")
	}
	// One change, named as the pair it is: both halves, in one entry, with nothing eaten
	// off the front of the old path.
	if !strings.HasSuffix(err.Error(), ": from-rowan/before.md -> from-rowan/after.md") {
		t.Fatalf("the refusal does not name the rename as one change over two paths: %v", err)
	}
	if strings.Contains(err.Error(), ",") {
		t.Fatalf("the old path was read as a second, separate change: %v", err)
	}
	// A short old path is the case that used to vanish: a record under four characters is
	// skipped, so the rename was reported as clean.
	if out, err := git(clone, "mv", "from-rowan/after.md", "from-rowan/before.md"); err != nil {
		t.Fatalf("git mv back: %v %s", err, out)
	}
	write(t, clone, "x.md", "x\n")
	if _, err := CommitAndPush(clone, testIdentity["Rowan"], []string{"x.md"}, "rowan: x", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if out, err := git(clone, "mv", "x.md", "y.md"); err != nil {
		t.Fatalf("git mv: %v %s", err, out)
	}
	if err := EnsureClean(clone, nil); err == nil {
		t.Fatal("a rename whose old path is three characters long was reported as a clean checkout")
	}
}
