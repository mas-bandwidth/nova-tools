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

// replyOpts is one reply invocation after the flag set has been read.
type replyOpts struct {
	busDir, as                  string
	to, cc, subject             string
	replyTo, bodyFile, draftDir string
	remote, branch              string
	maxBodyBytes                int
	reGiven                     bool
	toGiven, ccGiven            bool
	subjectGiven                bool
}

// cmdDraftReply is the whole transaction. Every refusal it makes is one `DRAFT REFUSED`
// line per problem, and no refusal writes a partial draft: the file is written once,
// complete, into a unique temporary and published onto its final name only after every
// check below has passed.
func cmdDraftReply(o replyOpts, f *flags, stdout, stderr io.Writer, now time.Time) int {
	// (1) The invocation, before anything is read. A missing flag is refusing to guess, and
	// the two flags that become git's own argv are checked by the existing rule.
	var problems []error
	if o.reGiven {
		problems = append(problems, errors.New("--reply-to and --re both name a thread; name it once, because the two flags say different things"))
	}
	for _, m := range []struct{ flag, value string }{
		{"--body-file", o.bodyFile}, {"--draft-dir", o.draftDir},
		{"--remote", o.remote}, {"--branch", o.branch},
	} {
		if strings.TrimSpace(m.value) == "" {
			problems = append(problems, fmt.Errorf("%s is required with --reply-to; refusing to guess", m.flag))
		}
	}
	if len(problems) > 0 {
		return refuseDraft(stderr, problems)
	}
	if !f.gitArgs(o.remote, o.branch, stderr) {
		return 2
	}

	// (2) The rest of the invocation, collected: a reply asked for with a misspelled
	// recipient, a subject holding a line break and a budget of zero is three mistakes and
	// one run, which is this verb's existing rule and is kept for its existing reason.
	if o.maxBodyBytes < 1 {
		problems = append(problems, fmt.Errorf("--max-body-bytes is a budget in bytes and is at least 1, got %d; a budget of zero is not %q", o.maxBodyBytes, "unlimited"))
	}
	if err := bus.OneLine("--subject", o.subject); err != nil {
		problems = append(problems, err)
	}
	c, err := bus.LoadConfig(o.busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus draft: %s\n", oneline.Err(err))
		return 2
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
	problems = append(problems, replyDraftDirProblems(o.busDir, o.draftDir)...)
	problems = append(problems, replyBodyFileProblems(o.bodyFile)...)
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
			fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %q is not an id on this bus, not a note that exists, and not the subject of a note on your listing; threads are named by id, and a slug is not a thread\n", oneline.Cap(o.replyTo, oneline.TailBytes))
			return 1
		}
		if target, found = t.Resolve(matches[0].Target()); !found {
			fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %q names a note on your listing that is not on the bus this run read\n", oneline.Cap(o.replyTo, oneline.TailBytes))
			return 1
		}
		if len(matches) > 1 {
			notes = append(notes, fmt.Sprintf("--reply-to: subject matched %d notes; this draft names the newest %s from %s; name the id to be exact",
				len(matches), oneline.Field(matches[0].Target()), oneline.Field(dash(matches[0].From))))
		}
	}
	re := target.Header.ID
	if re == "" {
		re = target.Path
	}
	if !onTheListing(listing, re) {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", offTheListingReason(c, t, target, me, legacy, hasCursor, re))
		return 1
	}

	// (7) The headers. The caller writes none of them, and nothing in the body routes
	// anything: --body-file is body text and is never parsed for a header line.
	from := me.Name
	sender := target.Header.From
	if p, ok := c.ResolveOne(target.Header.From); ok {
		sender = p.Name
	}
	toLine := o.to
	if !o.toGiven {
		if sender == me.Name {
			fmt.Fprintf(stderr, "DRAFT REFUSED: --reply-to %s is your own note, so the default To: would be you; a reply to your own note needs an explicit --to\n", oneline.Field(re))
			return 1
		}
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
	if target.Header.ID == "" {
		name = bus.LegacyDraftID(target.Path)
	}
	path, err := bus.PublishNoReplace(o.draftDir, now.UTC().Format(bus.FileTimeLayout)+"-re-"+name+".md", []byte(content))
	switch {
	case errors.Is(err, bus.ErrDraftExists):
		fmt.Fprintf(stderr, "DRAFT REFUSED: a draft already exists at %s; this tool never overwrites a draft\n",
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
	if !given || strings.TrimSpace(value) == "" {
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
	dir, derr := filepath.EvalSymlinks(draftDir)
	root, rerr := filepath.EvalSymlinks(busDir)
	if derr != nil || rerr != nil {
		return nil
	}
	if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return []error{fmt.Errorf("--draft-dir %s is the bus checkout at %s, or inside it; drafts go outside the bus, because send needs its tree clean", draftDir, root)}
	}
	return nil
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
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s cannot be read: %s\n", oneline.Field(path), oneline.Err(err))
		return nil, 2
	}
	defer f.Close()
	buf := make([]byte, budget+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s cannot be read: %s\n", oneline.Field(path), oneline.Err(err))
		return nil, 2
	}
	if n == 0 {
		fmt.Fprintf(stderr, "DRAFT REFUSED: --body-file %s is empty; a reply with no body is not a reply\n", oneline.Field(path))
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

// onTheListing reports whether the resolved target is one of the entries a listing would
// show, by the name a Re line uses for it.
func onTheListing(listing []bus.OpenEntry, target string) bool {
	for _, e := range listing {
		if e.Target() == target {
			return true
		}
	}
	return false
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
