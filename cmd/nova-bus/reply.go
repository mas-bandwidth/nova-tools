package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE REPLY FORM of `draft`: one bounded operation that refreshes the bus, resolves the
// note you are answering against what the fetch left, writes every header mechanically,
// puts the draft outside the protected checkout at a path it prints, and returns one line.
//
// It is reached only by `--reply-to`. Without that flag `draft` is the verb it has always
// been -- the same skeleton on stdout, the same refusals, the same exit 2, and no git at
// all -- which is the rule this whole file is written under: nova-bus is released, and a
// slice that changed a working invocation would break a loop nobody here can see.
//
// The contract is docs/SPEC-BUS-REPLY.md (nova-tools#267). The READ half of that document
// -- `--bodies` and its frame -- is a separate contract and is not this file's business.

// replyOnlyFlags are accepted ONLY in the reply form. Given without `--reply-to` they are
// exit 2 with a sentence, which is the same exit code an undefined flag already costs.
var replyOnlyFlags = []string{"remote", "branch", "body-file", "draft-dir", "git-timeout"}

// defaultMaxBodyBytes is the budget a reply body is read under when the caller names none.
// It is a bound and not a policy: a body is a file somebody wrote, and the number is there
// so that a mistyped path at a huge file is a refusal rather than a read.
const defaultMaxBodyBytes = 1 << 20

// replyRecipientCap is how many names the receipt's `to=` and `cc=` fields print before
// `+<k>` stands for the rest. A cap, a count and a remedy on one field: `path=` names the
// file where all of them are.
const replyRecipientCap = 8

// replyNoteCap is how many DRAFT NOTE lines this form can print, whatever the bus holds.
// The set is enumerated in the contract and there is no input that grows it.
const replyNoteCap = 5

// publishDraft is the create-exclusive publish as this verb reaches it. It is a var so a
// test can stand in for a filesystem that offers neither of the two, which is a property of
// the disk under --draft-dir and not of any invocation; nothing else replaces it.
var publishDraft = bus.PublishNoReplace

// replyOpts is one reply invocation after the flag set has been read.
type replyOpts struct {
	busDir, as                  string
	to, cc, subject             string
	replyTo, bodyFile, draftDir string
	remote, branch              string
	maxBodyBytes                int
	gitTimeout                  int
	reGiven                     bool
	toGiven, ccGiven            bool
	subjectGiven                bool
}

// cmdDraftReply is the whole transaction. Every refusal it makes is one `DRAFT REFUSED`
// line per problem, and no refusal writes a partial draft: the file is written once,
// complete, into a unique temporary and published onto its final name only after every
// check below has passed.
func cmdDraftReply(o replyOpts, f *flags, stdout, stderr io.Writer, now time.Time) int {
	// (1) THE WHOLE INVOCATION, COLLECTED, and printed once.
	//
	// Every problem in one run is this verb's existing rule and it is kept for its existing
	// reason: a refusal that names the first of three mistakes costs the writer three runs
	// to be told what the tool knew on the first. The first version of this form checked in
	// three passes and returned at the end of whichever pass first found something, so a
	// bad --remote and a bad --subject were one line and two runs -- the rule kept within a
	// pass and broken between them. There is one pass now.
	//
	// A check whose flag is missing is SKIPPED rather than run against "": a cascade of
	// consequences of one missing flag is not five problems, it is one.
	var problems []error
	if o.reGiven {
		problems = append(problems, errors.New("--reply-to and --re both name a thread; name it once, because the two flags say different things"))
	}
	missing := map[string]bool{}
	for _, m := range []struct{ flag, value string }{
		{"--body-file", o.bodyFile}, {"--draft-dir", o.draftDir},
		{"--remote", o.remote}, {"--branch", o.branch},
	} {
		if strings.TrimSpace(m.value) == "" {
			missing[m.flag] = true
			problems = append(problems, fmt.Errorf("%s is required with --reply-to; refusing to guess", m.flag))
		}
	}
	// The two flags that become git's own argv, by the existing rule and with its existing
	// words. A value that is missing is already named above.
	for _, c := range []struct{ what, value string }{{"remote", o.remote}, {"branch", o.branch}} {
		if missing["--"+c.what] {
			continue
		}
		if err := bus.ValidGitArg(c.what, c.value); err != nil {
			problems = append(problems, err)
		}
	}
	if err := gitTimeoutProblem(o.gitTimeout); err != nil {
		problems = append(problems, err)
	}
	if o.maxBodyBytes < 1 {
		problems = append(problems, fmt.Errorf("--max-body-bytes is a budget in bytes and is at least 1, got %d; a budget of zero is not %q", o.maxBodyBytes, "unlimited"))
	}
	// EVERY caller-supplied text flag, and not the subject alone.
	//
	// THE DEFECT THIS CLOSES. --to and --cc go onto header lines of a file this verb writes,
	// and the roster's own splitter trims each token, so `--to "Bo\n"` RESOLVED: the name
	// was found, the newline survived onto the To: line, and the blank line it made ended
	// the header block -- demoting the Re: and Subject: lines this form exists to write into
	// body prose, under a receipt that said the reply was addressed correctly. A form whose
	// whole claim is that the caller writes no header line may not let a caller write one.
	for _, line := range []struct{ flag, value string }{
		{"--subject", o.subject}, {"--to", o.to}, {"--cc", o.cc},
	} {
		if err := bus.OneLine(line.flag, line.value); err != nil {
			problems = append(problems, err)
		}
	}
	c, err := bus.LoadConfig(o.busDir)
	if err != nil {
		// The roster is what the checks below are made AGAINST, so this is where the
		// collection ends -- with everything found so far printed beside it, rather than
		// instead of it.
		return refuseDraft(stderr, append(problems, err))
	}
	me, known := c.Lookup(o.as)
	switch {
	case !known:
		problems = append(problems, fmt.Errorf("--as %q names no one on this bus (known: %s)", o.as, strings.Join(c.KnownNames(), "; ")))
	case me.Lane == "":
		problems = append(problems, fmt.Errorf("--as %q has no lane on this bus, so has nowhere to send from", me.Name))
	}
	toNames := replyResolve(c, "--to", o.to, o.toGiven, &problems)
	ccNames := replyResolve(c, "--cc", o.cc, o.ccGiven, &problems)
	if !missing["--draft-dir"] {
		problems = append(problems, replyDraftDirProblems(o.busDir, o.draftDir)...)
	}
	if !missing["--body-file"] {
		problems = append(problems, replyBodyFileProblems(o.bodyFile)...)
	}
	if len(problems) > 0 {
		return refuseDraft(stderr, problems)
	}

	// (3) The body, read at the budget and one byte past it and no further.
	body, code := replyBody(o.bodyFile, o.maxBodyBytes, stderr)
	if code != 0 {
		return code
	}

	// (4) The checkout, held for the fetch and the listing exactly as `wait`'s poll holds
	// it, and the refresh itself -- which is that poll, and not a second spelling of it.
	release, code := lockCheckout("DRAFT", o.busDir, stderr)
	if code != 0 {
		return code
	}
	defer release()
	before, err := bus.HeadCommit(o.busDir)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	moved, err := refreshCheckout(o.busDir, o.remote, o.branch)
	if err != nil {
		// Never a fall back to the checkout. A refusal costs the caller one turn; a wrong
		// Re: line costs a thread, and is wrong exactly when nobody is watching.
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	at, err := bus.HeadCommit(o.busDir)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return 1
	}

	// (5) The listing, computed read-only against the tree the fetch left: the carried OPEN
	// entries plus every note new since the cursor addressed to --as. Nothing is written
	// back -- no CURSOR, no OPEN, no RECEIPTS, no INDEX.
	listing, legacy, hasCursor, code := replyListing(o.busDir, c, me, stderr)
	if code != 0 {
		return code
	}
	t, err := bus.ReadBus(o.busDir, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus draft: %s\n", oneline.Err(err))
		return 2
	}

	// (6) The target, resolved after the refresh and held to the listing.
	var notes []string
	target, found := t.Resolve(o.replyTo)
	if !found {
		matches := bus.MatchOpenSubject(listing, o.replyTo)
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %q is not an id on this bus, not a note that exists, and not the subject of a note on your listing; threads are named by id, and a slug is not a thread; give the id your inbox listing printed\n", oneline.Cap(o.replyTo, oneline.TailBytes))
			return 1
		}
		if target, found = t.Resolve(matches[0].Target()); !found {
			fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %q names a note on your listing that is not on the bus this run read; read the inbox again, then reply\n", oneline.Cap(o.replyTo, oneline.TailBytes))
			return 1
		}
		if len(matches) > 1 {
			notes = append(notes, fmt.Sprintf("--reply-to: subject matched %d notes; this draft names the newest %s from %s; name the id to be exact",
				len(matches), oneline.Field(matches[0].Target()), oneline.Field(dash(matches[0].From))))
		}
	}
	// The resolved sender, which the default To: is and which the listing rule below is
	// asked about second: a note whose sender is you is refused for THAT, and not for being
	// absent from a listing your own lane is never on.
	from := me.Name
	sender := target.Header.From
	if p, ok := c.ResolveOne(target.Header.From); ok {
		sender = p.Name
	}
	if !o.toGiven && sender == me.Name {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %s is your own note, so the default To: would be you; a reply to your own note needs an explicit --to; give --to\n", oneline.Field(replyTargetName(target)))
		return 1
	}
	// THE LISTING IS MATCHED BY PATH, and the name the draft writes comes from the entry it
	// matched.
	//
	// Every entry has a path and a path identifies a note exactly; an entry's ID does not.
	// `openEntryFor` BLANKS an id that `ValidOpenID` refuses, so a note carrying an `Id:`
	// line this tool would never write is on the listing under its path -- and the first
	// version, looking for it by its header id, missed it and refused a note the reader was
	// carrying. Taking the name back from the entry also means the `Re:` line this draft
	// writes is the name the open list will look for when the reply lands, which is the
	// whole of what closes the note.
	entry, onList := listingEntry(listing, target.Path)
	if !onList {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", offTheListingReason(c, t, target, me, legacy, hasCursor, replyTargetName(target)))
		return 1
	}
	re := entry.Target()

	// (7) The headers. The caller writes none of them, and nothing in the body routes
	// anything: --body-file is body text and is never parsed for a header line.
	toLine := o.to
	if !o.toGiven {
		toLine = sender
		toNames = []string{sender}
	} else if len(toNames) != 1 || toNames[0] != sender {
		notes = append(notes, fmt.Sprintf("--to names %d recipients rather than the sender of %s; this reply goes where you said", len(toNames), oneline.Field(re)))
	}
	subject := o.subject
	if !o.subjectGiven {
		subject = target.Header.Subject
		if bus.IsReplySubject(subject) {
			notes = append(notes, fmt.Sprintf("the subject is the target's own, unstacked: %s", oneline.Quote(subject)))
		} else {
			subject = bus.ReplyPrefix + subject
		}
	}
	ccLine := ""
	if o.ccGiven {
		ccLine = o.cc
	} else {
		ccNames = nil
	}
	content := bus.Skeleton{From: from, To: toLine, Cc: ccLine, Re: []string{re}, Subject: subject}.RenderWith(string(body))

	// (8) The publish, no-replace, onto a name the tool composes. A legacy target -- one
	// with no Id: line -- is named on the bench by its derived id and on the bus by its
	// path, which is what the Re: line above already carries.
	name := re
	if entry.ID == "" {
		name = bus.LegacyDraftID(target.Path)
	}
	path, err := publishDraft(o.draftDir, now.UTC().Format(bus.FileTimeLayout)+"-re-"+name+".md", []byte(content))
	switch {
	case errors.Is(err, bus.ErrDraftExists):
		fmt.Fprintf(stderr, "DRAFT REFUSED: a draft already exists at %s; this tool never overwrites a draft; move that draft, or name another --draft-dir\n",
			oneline.Field(filepath.Join(o.draftDir, now.UTC().Format(bus.FileTimeLayout)+"-re-"+name+".md")))
		return 1
	case errors.Is(err, bus.ErrNoExclusivePublish):
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s; name a --draft-dir on a filesystem that has a create-exclusive publish\n", oneline.Err(err))
		return 2
	case err != nil:
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return 1
	}

	// (9) The notes this run decided for you, then the one line that is the receipt.
	notes = append(notes, replyMovedNote(o.busDir, before, at, moved))
	if len(notes) > replyNoteCap {
		notes = notes[:replyNoteCap]
	}
	for _, note := range notes {
		fmt.Fprintf(stderr, "DRAFT NOTE %s\n", note)
	}
	fmt.Fprintf(stdout, "DRAFT OK path=%s re=%s from=%s to=%s cc=%s at=%s moved=%t bytes=%d\n",
		oneline.Field(path), oneline.Field(re), oneline.Field(from),
		cappedList(toNames), cappedList(ccNames), oneline.Field(at), moved, len(content))
	return 0
}

// refuseDraft prints every problem in one run, one line each, and is exit 2's door.
func refuseDraft(stderr io.Writer, problems []error) int {
	for _, reason := range problems {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(reason))
	}
	return 2
}

// replyResolve is one caller-supplied recipient line, resolved against the roster by the
// rules the rest of this tool already uses. A flag that was not given resolves to nothing
// and is not a problem.
func replyResolve(c *bus.Config, flagName, value string, given bool, problems *[]error) []string {
	if !given {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		*problems = append(*problems, fmt.Errorf("nova-bus draft: %s is required; refusing to guess", flagName))
		return nil
	}
	names, unknown := c.ResolveList(value)
	if len(unknown) > 0 {
		*problems = append(*problems, fmt.Errorf("%s: %s names no one on this bus (known: %s)", flagName, strings.Join(bus.UnknownNames(unknown), ", "), strings.Join(c.KnownNames(), "; ")))
		return nil
	}
	if len(names) == 0 {
		*problems = append(*problems, fmt.Errorf("%s: no recipients", flagName))
	}
	return names
}

// replyDraftDirProblems is the in-checkout rule, moved from the end of the job to the
// start of it. `send` needs the bus's tree clean but for the note it is about to write, and
// today a draft written into the checkout is refused AFTER the body, the headers and the
// turns have been spent. Both paths are resolved and both sides follow their links, which
// is the test `--bus` already makes.
func replyDraftDirProblems(busDir, draftDir string) []error {
	info, err := os.Stat(draftDir)
	if err != nil || !info.IsDir() {
		return []error{fmt.Errorf("--draft-dir %s is not a directory on this bench; this tool creates no directories", draftDir)}
	}
	dir := resolveForCompare(draftDir)
	root := resolveForCompare(busDir)
	if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return []error{fmt.Errorf("--draft-dir %s is the bus checkout at %s, or inside it; drafts go outside the bus, because send needs its tree clean", draftDir, root)}
	}
	return nil
}

// resolveForCompare is the test `--bus` already makes, both halves of it: ABSOLUTIZE, then
// follow every link.
//
// THE DEFECT THE FIRST HALF CLOSES. Comparing two EvalSymlinks outputs without absolutizing
// compares a relative path against an absolute one, so `--bus . --draft-dir scratch` -- the
// shape a session already sitting in the bus types -- looked like a directory outside the
// checkout and the draft was written INSIDE it, at exit 0. The refusal then arrived at
// `send`, from the clean-tree guard, after the body, the headers and the turns had been
// spent, which is the cost this row exists to move to the start of the job.
//
// A path that cannot be resolved falls back to the absolute form rather than to no check at
// all: the comparison is then lexical, which is weaker than following the links and is
// still a comparison.
func resolveForCompare(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return resolved
}

// replyBodyFileProblems refuses a body file that is not there or is not a file. It is exit
// 2 and not 1: an unreadable input is an invocation that could not run.
func replyBodyFileProblems(path string) []error {
	info, err := os.Stat(path)
	if err != nil {
		return []error{fmt.Errorf("--body-file %s cannot be read: %v", path, err)}
	}
	if !info.Mode().IsRegular() {
		return []error{fmt.Errorf("--body-file %s is not a file (%s)", path, info.Mode().Type())}
	}
	return nil
}

// replyBody reads the body at the budget and ONE byte past it and no further, so that a
// mistyped path at a very large file costs the budget and not the file.
func replyBody(path string, budget int, stderr io.Writer) ([]byte, int) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s cannot be read: %s; name a file this user may read\n", oneline.Field(path), oneline.Err(err))
		return nil, 2
	}
	defer f.Close()
	buf := make([]byte, budget+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s cannot be read: %s; name a file this user may read\n", oneline.Field(path), oneline.Err(err))
		return nil, 2
	}
	if n == 0 {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s is empty; a reply with no body is not a reply; write the body, then draft again\n", oneline.Field(path))
		return nil, 1
	}
	if n > budget {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s is over --max-body-bytes %d; name a larger budget or a smaller body\n", oneline.Field(path), budget)
		return nil, 1
	}
	return buf[:n], 0
}

// replyListing is what an `inbox` run at this instant would list, computed READ-ONLY: the
// carried OPEN entries plus every note new since this reader's CURSOR addressed to them.
//
// It is the reading verbs' own implementation and not a second one, because two spellings
// of one rule drift and a reply resolved by the drifted one is a reply to the wrong note. A
// reader with no cursor has no listing at all, which is its own refusal with its own door.
func replyListing(busDir string, c *bus.Config, me bus.Participant, stderr io.Writer) ([]bus.OpenEntry, bus.LegacyLine, bool, int) {
	cursor, err := bus.ReadCursor(busDir, me.Lane)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return nil, bus.LegacyLine{}, false, 1
	}
	if cursor.Commit == "" {
		return nil, bus.LegacyLine{}, false, 0
	}
	legacy := effectiveLegacy(bus.LegacyLine{}, cursor)
	ok, err := bus.IsAncestor(busDir, cursor.Commit)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return nil, legacy, false, 1
	}
	if !ok {
		fmt.Fprintf(stderr, "DRAFT REFUSED: the cursor %s is not an ancestor of HEAD, so there is no listing to answer from; read once with inbox --full --advance\n", oneline.Field(cursor.Commit))
		return nil, legacy, false, 1
	}
	changed, err := bus.ChangedSince(busDir, cursor.Commit)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return nil, legacy, false, 1
	}
	open, err := bus.ReadOpen(busDir, me.Lane)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.Err(err))
		return nil, legacy, false, 1
	}
	// The receipt word count decides note-from-receipt on a LISTING, and this verb prints
	// no listing and asks no such question: a receipted note is a legal target and a note
	// is a legal target. Zero is that question not being asked.
	res, err := bus.InboxSince(busDir, c, me, changed, open, 0, legacy)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus draft: %s\n", oneline.Err(err))
		return nil, legacy, false, 2
	}
	return res.Open, legacy, true, 0
}

// listingEntry is the entry a listing would show for this note, found by the one field
// every entry has and that identifies a note exactly: its repo-relative path.
func listingEntry(listing []bus.OpenEntry, path string) (bus.OpenEntry, bool) {
	for _, e := range listing {
		if e.Path == path {
			return e, true
		}
	}
	return bus.OpenEntry{}, false
}

// replyTargetName is what a refusal calls a note it did not put on a listing: its id when it
// has one, its path when it does not. It is the refusal's name for the note and never the
// draft's, which comes from the listing entry.
func replyTargetName(n *bus.Note) string {
	if n.Header.ID != "" {
		return n.Header.ID
	}
	return n.Path
}

// offTheListingReason is which of the four reasons a target that is on the bus is on none
// of this reader's listing, and the door out of each. For the first three the door is the
// existing `draft --re`, which resolves against the whole bus and is untouched; for the
// fourth it is an `inbox` run, because a reader with no cursor needs the listing itself.
func offTheListingReason(c *bus.Config, t *bus.Bus, target *bus.Note, me bus.Participant, legacy bus.LegacyLine, hasCursor bool, re string) string {
	if !hasCursor {
		return fmt.Sprintf("--reply-to %s cannot be on a listing you have not got: this lane has no cursor yet; one inbox --advance run gives you both", oneline.Field(re))
	}
	if _, answered := t.AnsweredBy(target, me.Lane); answered {
		return fmt.Sprintf("--reply-to %s is a note you have already answered, so it is on no listing of yours; draft --re names a closed thread", oneline.Field(re))
	}
	addressed, behind := bus.OffTheListing(c, target, me, legacy)
	if !addressed {
		return fmt.Sprintf("--reply-to %s was never addressed to you, on To: or Cc:, so it is on no listing of yours; draft --re names a note this form will not", oneline.Field(re))
	}
	if behind {
		return fmt.Sprintf("--reply-to %s is behind your switch-day line, so this reader has taken it as read; draft --re names it anyway", oneline.Field(re))
	}
	return fmt.Sprintf("--reply-to %s is on the bus and on no listing of yours; draft --re answers a thread this form will not", oneline.Field(re))
}

// replyMovedNote is the one line that says what the refresh did, which is the fact a reader
// checking an id most wants beside it.
func replyMovedNote(busDir, before, at string, moved bool) string {
	if !moved {
		return fmt.Sprintf("the bus had nothing new; this id was resolved against %s", oneline.Field(at))
	}
	n, err := bus.CommitsBetween(busDir, before, at)
	if err != nil {
		return fmt.Sprintf("the bus moved before this id was resolved; it was resolved against %s", oneline.Field(at))
	}
	return fmt.Sprintf("the bus moved %d commits before this id was resolved", n)
}

// cappedList is quoteList with the receipt's bound on it: the first eight names, then
// `+<k>` for the rest. The count says how many were not printed and `path=` is where they
// all are, which is a cap, a count and a remedy on one field.
func cappedList(names []string) string {
	if len(names) <= replyRecipientCap {
		return quoteList(names)
	}
	return quoteList(names[:replyRecipientCap]) + fmt.Sprintf(";+%d", len(names)-replyRecipientCap)
}
