package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdReply is `nova-bus reply`: one note, answering one note, with every header the tool
// fills taken from the original. It reads the body from --file, resolves --re against the
// notes in the checkout after fetching --remote/--branch, and writes one reply note into
// the caller's lane. Without --advance it writes nothing else; with it, the caller's cursor
// moves in the same commit as the reply.
func cmdReply(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("reply")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	re := f.fs.String("re", "", "the note being answered, by id (required)")
	file := f.fs.String("file", "", "the reply body, as a file (required)")
	remote := f.fs.String("remote", "", "the git remote the note is resolved against and pushed to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD in the same commit as the reply")
	dryRun := f.fs.Bool("dry-run", false, "shape and report the reply and write nothing")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "file": file, "remote": remote, "branch": branch}) {
		return 2
	}
	if strings.TrimSpace(*re) == "" {
		fmt.Fprint(stderr, "nova-bus reply: --re is required; name the note being answered\n")
		return 2
	}
	if *advance && *dryRun {
		fmt.Fprint(stderr, "nova-bus reply: --advance moves the cursor and --dry-run writes nothing; drop one\n")
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus reply: %s\n", oneline.Err(err))
		return 2
	}
	body := string(raw)
	if key, ok := replyFilledHeader(body); ok {
		fmt.Fprintf(stderr, "nova-bus reply: reply fills From, To, Re and Subject; delete the %s: line from %s\n",
			oneline.Field(key), oneline.Field(*file))
		return 2
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus reply: %s\n", oneline.Err(err))
		return 2
	}
	release, lockErr := bus.LockCheckout(*busDir, checkoutLockWait)
	if lockErr != nil {
		fmt.Fprintf(stderr, "REPLY REFUSED: %s\n", oneline.Err(lockErr))
		return 1
	}
	defer release()
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus reply: %s\n", oneline.Err(err))
		return 2
	}
	me, found := c.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus reply: --as %s names no one on this bus (known: %s)\n",
			oneline.Field(*as), oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "nova-bus reply: --as %s has no lane on this bus, so has nowhere to send from\n", oneline.Field(me.Name))
		return 2
	}
	if !*dryRun {
		if err := checkoutReady(*busDir, *branch, []string{bus.BeatPath(me.Lane)}); err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL: %s\n", oneline.Err(err))
			return 1
		}
		if _, err := refreshCheckout(*busDir, *remote, *branch); err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL: %s\n", oneline.Err(err))
			printTranscript(stderr, err)
			return 1
		}
		if err := levelWithRemote(*busDir, *remote, *branch, false); err != nil {
			fmt.Fprintf(stderr, "REPLY REFUSED: %s\n", oneline.Err(err))
			return 1
		}
	}
	t, err := bus.ReadBus(*busDir, c)
	if err != nil {
		fmt.Fprintf(stderr, "nova-bus reply: %s\n", oneline.Err(err))
		return 2
	}
	original, ok := t.Resolve(*re)
	if !ok {
		fmt.Fprintf(stderr, "nova-bus reply: --re %s names no note; run nova-bus inbox --open and name one\n", oneline.Field(*re))
		return 2
	}
	prepared, err := bus.PrepareReply(t, me, original, body, now)
	if err != nil {
		for _, reason := range bus.Reasons(err) {
			fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(me.Name), oneline.Err(reason))
		}
		return 1
	}
	if *dryRun {
		replyOK(stdout, prepared, "-", false, false, 0)
		return 0
	}
	if err := prepared.Save(*busDir); err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		return 1
	}
	if err := prepared.AppendIndex(*busDir); err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		return 1
	}
	paths := []string{prepared.Path, bus.IndexPath(me.Lane)}
	if *advance {
		head, err := bus.HeadCommit(*busDir)
		if err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL: %s\n", oneline.Err(err))
			return 1
		}
		held, err := bus.ReadCursor(*busDir, me.Lane)
		if err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
			return 1
		}
		open, err := bus.ReadOpen(*busDir, me.Lane)
		if err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(bus.OpenPath(me.Lane)), oneline.Err(err))
			return 1
		}
		if err := bus.WriteCursor(*busDir, me.Lane, head, len(open), held.Legacy, now); err != nil {
			fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
			return 1
		}
		paths = append(paths, bus.CursorPath(me.Lane))
	}
	wroteAttrs, err := bus.EnsureMergeAttributes(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	staged, err := bus.StagePaths(*busDir, paths)
	if err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		return 1
	}
	res, err := commit(*busDir, me, staged,
		bus.WithTrailer(prepared.Message, bus.TrailerSend+" "+prepared.Note.Header.ID),
		*remote, *branch, *attempts, false)
	if err != nil {
		fmt.Fprintf(stderr, "REPLY FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	replyOK(stdout, prepared, res.Commit, res.Pushed, *advance, res.Attempts)
	return 0
}

// replyFilledHeader is the first line of a reply draft that carries a header the verb
// fills, or false when the draft is body text. Only the header block at the top is read: a
// blank line ends it, and a body that happens to quote a header line is not a header.
func replyFilledHeader(body string) (string, bool) {
	fills := map[string]bool{bus.KeyFrom: true, bus.KeyTo: true, bus.KeyRe: true, bus.KeySubject: true}
	for _, line := range bus.SplitDraft(body) {
		if strings.TrimSpace(line) == "" {
			return "", false
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if fills[key] {
			return key, true
		}
	}
	return "", false
}

// replyOK is the one line the verb prints, naming every field.
func replyOK(stdout io.Writer, p bus.Prepared, commit string, pushed, advanced bool, attempts int) {
	if commit == "" {
		commit = "-"
	}
	fmt.Fprintf(stdout, "REPLY OK id=%s re=%s path=%s to=%s subject=%s commit=%s pushed=%t advanced=%t attempts=%d\n",
		oneline.Field(p.Note.Header.ID), oneline.Field(p.Note.Header.Re[0]), oneline.Field(p.Path),
		oneline.Field(p.Note.Header.To), oneline.Field(p.Note.Header.Subject),
		oneline.Field(commit), pushed, advanced, attempts)
}
