package bus

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// NO CONFLICT MAY WEDGE A LINE.
//
// THE FAILURE, from the scenario run over a copy of a real table. Two benches of
// one lane sent at once. Their commits touched one file the other could not see -- the
// lane's INDEX, appended at the same end over one base -- so the rebase stopped, the tool
// aborted it cleanly and said so, and the bench was then stuck: the obvious repair, a
// person typing `git pull --rebase`, landed in a HALF-DONE REBASE with `UU
// from-<lane>/INDEX` in the tree and nothing on the table saying what to do next. A tool
// that hands a person a conflict it could have settled, in a file it invented, is a tool
// that has moved its own cost onto them.
//
// So there are two answers here and they are deliberately both:
//
//  1. THE ATTRIBUTE. `send` writes `.gitattributes` at the table root marking
//     `from-*/INDEX` and `from-*/RECEIPTS` as `merge=union` (see attributes.go). Those two
//     are append-only line files, and union is exactly right for them: it keeps both sides'
//     lines. With the attribute in place git resolves them itself, in the tool's rebase and
//     in a person's own `git pull --rebase`, which is the half that helps somebody who is
//     not running this tool at all.
//
//  2. THE TOOL'S OWN RESOLUTION, below, because the attribute is not enough on its own. It
//     reaches a checkout only once it has been committed and pulled, so the first send on a
//     table -- and every bench that has not pulled since -- rebases WITHOUT it. A fix that
//     works only after everybody has it is a fix that does not work on the day it is
//     needed.
//
// What is settled, and what is not:
//
//	INDEX, RECEIPTS, .gitattributes  union: both sides' lines, ours first, identical
//	                                 lines once. They are append-only and order-free.
//	CURSOR                           the further read wins: the cursor whose commit is a
//	                                 DESCENDANT of the other, and failing that the later
//	                                 stamp. A cursor is a claim that everything up to a
//	                                 commit has been read, and of two such claims from two
//	                                 benches of one reader the further one is true of both.
//	OPEN                             re-derived from the winning cursor's side, never
//	                                 merged: an open list is the list that BELONGS to a
//	                                 cursor, and unioning two of them would carry notes the
//	                                 winning read has already closed.
//	a note                           NOT settled. Two benches wrote the same path, which
//	                                 means they sent the same note in the same second and
//	                                 were assigned one id. Which of the two is the note is
//	                                 a person's decision and this tool will not make it.
//
// Every one of these is a file this tool wrote, in the layout this tool chose. Nothing here
// resolves a conflict in a NOTE, which is the table's own record and belongs to whoever
// wrote it.

// maxRebaseSteps bounds the settle loop. A rebase replays one commit per step and this
// tool's branch is ahead by the notes it has not landed; the bound is a guard against a
// rebase that stops forever on one commit, not a budget anybody is meant to reach.
const maxRebaseSteps = 200

// ConflictError is a refusal whose one actionable line and whose git transcript are
// separate values.
//
// THE FAILURE IT CLOSES: `SEND FAIL` used to carry git's whole rebase transcript inline,
// rendered through the one-line escape, so a person got forty lines of git as one line of
// `\x0d\x0a` and could read none of it. The one-line guarantee is about the EVENT line, and
// a transcript is not an event: the line above says what happened and what to do, escaped
// like every other, and the transcript follows it on stderr as git wrote it.
type ConflictError struct {
	// Reason is the one line, already free of line breaks.
	Reason string
	// Output is git's own text, verbatim.
	Output string
}

func (e *ConflictError) Error() string      { return e.Reason }
func (e *ConflictError) Transcript() string { return e.Output }

// Transcript is the transcript an error carries, or "" for an error that carries none. It
// is how a caller prints git's own output beside a refusal without knowing which error type
// produced it.
func Transcript(err error) string {
	var carrier interface{ Transcript() string }
	if errors.As(err, &carrier) {
		return carrier.Transcript()
	}
	return ""
}

// transcriptOf is git's own output out of an error this package made.
func transcriptOf(err error) string {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.output
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// oneLineOf folds a message onto one line, for the reason slot of a ConflictError, which
// promises to hold no line break of its own.
func oneLineOf(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
	return truncate(strings.TrimSpace(s), 200)
}

// GitDir is the checkout's git directory. It is asked of git rather than assumed to be
// `<table>/.git`, because in a linked worktree and in a submodule `.git` is a FILE naming
// the directory elsewhere, and both are legitimate places to keep a table.
func GitDir(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	got := strings.TrimSpace(out)
	if got == "" {
		return "", fmt.Errorf("%s has no git directory", dir)
	}
	return got, nil
}

// inRebase reports the rebase state directory this checkout is in the middle of, if any.
// Both names are checked because git has two backends and uses a different directory for
// each: `rebase-merge` for the merge backend, which is the default, and `rebase-apply` for
// the apply backend, which `--apply` and an older git still reach.
func inRebase(dir string) (string, bool) {
	gd, err := GitDir(dir)
	if err != nil {
		gd = filepath.Join(dir, ".git")
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		full := filepath.Join(gd, name)
		if st, statErr := os.Stat(full); statErr == nil && st.IsDir() {
			return full, true
		}
	}
	return "", false
}

// abortRebase takes the checkout out of a rebase, and REFUSES when it could not.
//
// THE FAILURE THIS CLOSES, and it is the worst shape a failure in this file can have. The
// abort's own error used to be wrapped and returned, and nothing checked whether the abort
// had actually worked -- so a `git rebase --abort` that failed left the run returning while
// the checkout was STILL IN A REBASE. Every later verb then refuses for a reason that is
// true and unhelpful (a dirty checkout, a detached HEAD), and nothing says the real one.
// The state is checked after the attempt, not inferred from its exit code, and the refusal
// carries the recovery that actually works on the state it found.
func abortRebase(dir string) error {
	_, abortErr := git(dir, "rebase", "--abort")
	where, still := inRebase(dir)
	if !still {
		if abortErr != nil {
			// The abort said something and the rebase is nevertheless over, which is the
			// ordinary "no rebase in progress" case when git resolved everything itself.
			return nil
		}
		return nil
	}
	return fmt.Errorf("the rebase conflicted and could not be aborted: this checkout is STILL in a rebase at %s, and no verb of this tool will run here until it is not -- in %s run `git rebase --abort`; if that fails, make %s writable, remove it, and run `git reset --hard ORIG_HEAD`",
		where, dir, where)
}

// settleRebase resolves the conflicts this tool's own files can produce and carries the
// rebase to its end. It returns an error naming the path it will not settle, which is the
// caller's cue to abort and hand the conflict to a person.
func settleRebase(dir string, id Identity) error {
	for step := 0; step < maxRebaseSteps; step++ {
		if _, still := inRebase(dir); !still {
			// The rebase is over. It may have ended because git settled everything itself
			// (the union attribute is on the table) or because we settled the last stop.
			return nil
		}
		if err := resolveOwnConflicts(dir); err != nil {
			return err
		}
		// A commit whose whole content arrived from the other side leaves nothing to
		// record, and `git rebase --continue` refuses that rather than making an empty
		// commit. Skipping it is right: the change is already on the branch.
		if _, err := git(dir, "diff", "--cached", "--quiet", "HEAD"); err == nil {
			if _, err := git(dir, append(identityArgs(id), "rebase", "--skip")...); err != nil {
				return fmt.Errorf("a commit whose content had already arrived could not be skipped: %w", err)
			}
			continue
		}
		if _, err := git(dir, append(identityArgs(id), "rebase", "--continue")...); err != nil {
			// Not a conflict this pass could see: the rebase stopped for a reason that is
			// not an unmerged path, and that is a person's.
			if _, still := inRebase(dir); still {
				return fmt.Errorf("the rebase would not continue after the settlement: %s", oneLineOf(transcriptOf(err)))
			}
			return nil
		}
	}
	return fmt.Errorf("the rebase did not settle within %d steps", maxRebaseSteps)
}

// resolveOwnConflicts settles every unmerged path that is one of this tool's own files, and
// returns an error naming the first path that is not.
func resolveOwnConflicts(dir string) error {
	paths, err := unmergedPaths(dir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("the rebase stopped with no conflicted path")
	}
	// The cursor is decided first and the open list follows it, so the two are read from
	// ONE side. An open list is the list that belongs to a cursor; taking the further
	// cursor and the other side's open list would carry notes that read has closed.
	side := map[string]int{}
	var opens []string
	for _, p := range paths {
		base := path.Base(p)
		switch base {
		case IndexName, ReceiptsName, AttributesName:
			ours, theirs := conflictSides(dir, p)
			union := UnionLines(ours, theirs)
			if union == "" {
				// Both sides read as nothing over a path git says is conflicted, which is a
				// state this pass does not understand -- and an empty resolution is a
				// DELETION. Refusing here rather than writing it is the difference between
				// a person settling a conflict and a file leaving the table.
				return fmt.Errorf("%s (both sides of the conflict read as empty)", p)
			}
			if err := writeResolved(dir, p, union); err != nil {
				return err
			}
		case CursorName:
			ours, theirs := conflictSides(dir, p)
			won := cursorWinner(dir, ours, theirs)
			side[laneOf(p)] = won
			content := ours
			if won == theirSide {
				content = theirs
			}
			if err := writeResolved(dir, p, content); err != nil {
				return err
			}
		case OpenName:
			opens = append(opens, p)
		default:
			return errors.New(p)
		}
	}
	for _, p := range opens {
		lane := laneOf(p)
		won, decided := side[lane]
		if !decided {
			// The cursors did not themselves conflict, so the two sides are read from the
			// two commits the rebase is between rather than from the index stages.
			won = cursorWinner(dir, showAt(dir, "HEAD", CursorPath(lane)), showAt(dir, "REBASE_HEAD", CursorPath(lane)))
		}
		ours, theirs := conflictSides(dir, p)
		content := ours
		if won == theirSide {
			content = theirs
		}
		if err := writeResolved(dir, p, content); err != nil {
			return err
		}
	}
	return nil
}

// ourSide and theirSide are git's index stages during a rebase: stage 2 is what is already
// on the branch (what arrived from the remote) and stage 3 is the commit being replayed
// (this bench's own). The names are git's and are the wrong way round from how a person
// reads them during a rebase, which is exactly why they are named here rather than written
// as 2 and 3 at the call sites.
const (
	ourSide   = 2
	theirSide = 3
)

// conflictSides reads both sides of one unmerged path out of the index. A side that is
// missing -- one bench deleted the file, the other changed it -- reads as empty, which the
// union treats as no lines and the cursor pick treats as no claim.
func conflictSides(dir, p string) (ours, theirs string) {
	return showAt(dir, ":2", p), showAt(dir, ":3", p)
}

// showAt is one blob's content at a revision or an index stage, or "" when there is none.
//
// Both spellings take the same separator: `HEAD:<path>` names a blob in a commit and
// `:2:<path>` names one in an index stage, and the leading colon of the second is part of
// the STAGE rather than the separator. Getting that wrong is silent -- git says the path
// does not exist, which reads here as "that side has no such file" -- and the settlement
// then wrote an empty resolution, which is a DELETION. A first pass did exactly that and
// took the file off the table.
func showAt(dir, rev, p string) string {
	out, err := git(dir, "show", rev+":"+p)
	if err != nil {
		return ""
	}
	return out
}

// unmergedPaths lists the paths git left conflicted, each once.
func unmergedPaths(dir string) ([]string, error) {
	out, err := git(dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths, nil
}

// UnionLines is the union this tool performs on its own append-only line files: ours in
// order, then the lines of theirs that ours does not already hold. It is a stable
// concatenation with identical lines dropped, which is what git's own `merge=union` driver
// does and is what these files mean -- an INDEX, a RECEIPTS and a .gitattributes are sets of
// lines whose order is history rather than structure.
//
// Blank lines go, because every reader of these files (see records) already skips them and
// keeping them would make two spellings of one file.
func UnionLines(ours, theirs string) string {
	seen := map[string]bool{}
	var out []string
	for _, block := range []string{ours, theirs} {
		for _, line := range strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) == "" || seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// cursorWinner picks between two CURSOR files and reports which side won.
//
// The further read wins, and "further" is asked of the history rather than of the clock
// wherever it can be: a cursor whose commit is a DESCENDANT of the other's has read
// everything the other has and more, so taking it loses nothing. When neither commit
// reaches the other -- two benches on branches that have not met, or a commit this checkout
// does not hold -- the later stamp is the fallback, and a cursor that will not parse at all
// loses to one that will.
func cursorWinner(dir, ours, theirs string) int {
	ourCommit, ourStamp, ourOK := cursorClaim(ours)
	theirCommit, theirStamp, theirOK := cursorClaim(theirs)
	switch {
	case !ourOK && !theirOK:
		return theirSide
	case !ourOK:
		return theirSide
	case !theirOK:
		return ourSide
	}
	if ourCommit != theirCommit {
		if further, err := isAncestorOf(dir, ourCommit, theirCommit); err == nil && further {
			return theirSide
		}
		if further, err := isAncestorOf(dir, theirCommit, ourCommit); err == nil && further {
			return ourSide
		}
	}
	at, aerr := time.Parse(ReceiptStampLayout, ourStamp)
	bt, berr := time.Parse(ReceiptStampLayout, theirStamp)
	if aerr == nil && berr == nil && at.After(bt) {
		return ourSide
	}
	return theirSide
}

// cursorClaim reads the commit and stamp off a CURSOR file's one line, without validating
// the rest: this is a tie-break and not a read, and a cursor whose trailing tokens are
// wrong is still a claim about a commit.
func cursorClaim(content string) (commit, stamp string, ok bool) {
	for _, r := range records(content) {
		fields := strings.Fields(r.text)
		if len(fields) == 0 {
			return "", "", false
		}
		if len(fields) > 1 {
			stamp = fields[1]
		}
		return fields[0], stamp, ValidCommitHex(fields[0]) == nil
	}
	return "", "", false
}

// writeResolved puts the settled content on disk and stages it, so the rebase can carry
// on. An empty resolution is the file's REMOVAL, which is the state an emptied OPEN list is
// already in on a table (see replaceLaneFile), and `git add -A` stages that as the deletion
// it is rather than leaving the conflict entry behind.
func writeResolved(dir, p, content string) error {
	full := filepath.Join(dir, filepath.FromSlash(p))
	if err := insideRoot(dir, full); err != nil {
		return err
	}
	if content == "" {
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if _, err := git(dir, "add", "-A", "--", p); err != nil {
		return err
	}
	return nil
}
