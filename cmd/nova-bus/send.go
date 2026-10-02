// prepare and send: a draft shaped, given its id and date, committed and pushed.

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

func cmdPrepare(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("prepare")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	file := f.fs.String("file", "", "the draft to prepare")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as}) {
		return 2
	}
	if (*file == "") == !*useStdin {
		fmt.Fprint(stderr, "PREPARE REFUSED: give exactly one of --file and --stdin; refusing to guess; run: nova-bus prepare -h\n")
		return 2
	}
	source := "(stdin)"
	var text string
	if *useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "PREPARE REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus prepare -h"))
			return 2
		}
		text = string(raw)
	} else {
		source = *file
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "PREPARE REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus prepare -h"))
			return 2
		}
		text = string(raw)
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "PREPARE REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus prepare -h"))
		return 2
	}
	t, ok := openBus("prepare", *busDir, stderr)
	if !ok {
		return 2
	}
	prepared, err := bus.PrepareDraft(t, text, now, *slug, *as)
	if err != nil {
		for _, reason := range bus.Reasons(err) {
			fmt.Fprintf(stderr, "PREPARE FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
		}
		return 1
	}
	for _, notice := range prepared.Notices {
		fmt.Fprintf(stderr, "PREPARE NOTE %s\n", oneline.Escape(notice))
	}
	artifactJSON, err := bus.RenderPreparedArtifact(prepared)
	if err != nil {
		fmt.Fprintf(stderr, "PREPARE FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	fmt.Fprint(stdout, artifactJSON)
	return 0
}

func cmdSend(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("send")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	file := f.fs.String("file", "", "the draft to send")
	useStdin := f.fs.Bool("stdin", false, "read the draft from standard input instead of --file")
	preparedFile := f.fs.String("prepared", "", "the prepared artifact to send or confirm")
	usePreparedStdin := f.fs.Bool("prepared-stdin", false, "read the prepared artifact from standard input instead of --prepared")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	as := f.fs.String("as", "", "which participant you are; supplies the From line when the draft has none, and is refused if the draft's From line names anybody else")
	host := f.fs.String("host", "", "the machine you are posting from; written as the Host line, shown as host= on an inbox line, and read from a `host=` line in <bus>/.nova-bus/defaults when the flag is absent")
	slug := f.fs.String("slug", "", "the human half of the filename (default: from the subject)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the note is NOT on the bus until it is pushed")
	dryRun := f.fs.Bool("dry-run", false, "stop after the preflight and the shaping: commit nothing, push nothing, print the note that would be sent")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "remote": remote, "branch": branch}) {
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
	hostName, ok := f.host(*host, f.set("host"), *busDir, stderr)
	if !ok {
		return 2
	}
	hasDraft := *file != "" || *useStdin
	hasPrepared := *preparedFile != "" || *usePreparedStdin
	if hasDraft && hasPrepared {
		fmt.Fprint(stderr, "SEND REFUSED: --prepared is mutually exclusive with --file and --stdin; run: nova-bus send -h\n")
		return 2
	}
	if hasPrepared && *dryRun {
		fmt.Fprint(stderr, "SEND REFUSED: --dry-run shapes an ordinary draft; drop --prepared or drop --dry-run; run: nova-bus send -h\n")
		return 2
	}
	if hasPrepared {
		if (*preparedFile == "") == !*usePreparedStdin {
			fmt.Fprint(stderr, "SEND REFUSED: give exactly one of --prepared and --prepared-stdin; refusing to guess; run: nova-bus send -h\n")
			return 2
		}
	} else {
		if (*file == "") == !*useStdin {
			fmt.Fprint(stderr, "SEND REFUSED: give exactly one of --file and --stdin; refusing to guess; run: nova-bus send -h\n")
			return 2
		}
	}
	if *preparedFile != "" || *usePreparedStdin {
		if *noPush {
			fmt.Fprint(stderr, "SEND REFUSED: --no-push cannot be used with --prepared; run: nova-bus send -h\n")
			return 2
		}
		if *slug != "" {
			fmt.Fprint(stderr, "SEND REFUSED: --slug cannot be used with --prepared; run: nova-bus send -h\n")
			return 2
		}
		if *as == "" {
			fmt.Fprint(stderr, "SEND REFUSED: --as is required with --prepared; run: nova-bus send -h\n")
			return 2
		}
		source := "(prepared-stdin)"
		var raw []byte
		var err error
		if *usePreparedStdin {
			raw, err = io.ReadAll(stdin)
		} else {
			source = *preparedFile
			raw, err = os.ReadFile(*preparedFile)
		}
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		c, err := bus.LoadConfig(*busDir)
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		art, p, err := bus.ValidatePreparedArtifact(raw, *busDir, c, *as)
		if err != nil {
			for _, reason := range bus.Reasons(err) {
				fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
			}
			return 1
		}
		release, err := bus.LockCheckout(*busDir, checkoutLockWait)
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 1
		}
		defer release()

		res, err := bus.SendPreparedArtifact(*busDir, *remote, *branch, p, art, *attempts)
		if err != nil {
			fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(art.Path), oneline.Err(err))
			printTranscript(stderr, err)
			return 1
		}
		to, _ := p.Note.Header.Recipients(c)
		fmt.Fprintf(stdout, "SEND OK id=%s path=%s commit=%s pushed=%t attempts=%d state=%s wakes=%d body_bytes=%d\n",
			oneline.Field(art.ID), oneline.Field(art.Path), oneline.Field(res.Commit), res.Pushed, res.Attempts, oneline.Field(res.State), len(to), len(p.Note.Body))
		return 0
	}

	source := "(stdin)"
	var text string
	if *useStdin {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		text = string(raw)
	} else {
		source = *file
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		text = string(raw)
	}
	// THE PREFLIGHT, before any commit: two shapes a hand-written draft arrives in that
	// this tool mints rather than reads. A hand-written Id is the tool's to assign, and a
	// Re line names one thread. Both are exit 2 with one remedy line, and both leave the
	// bus, the index and the working tree exactly as they were.
	if kind, ok := preflightDraft(text); !ok {
		switch kind {
		case "id":
			fmt.Fprintf(stderr, "SEND REFUSED: the tool mints the Id; delete the Id: header from %s; run: nova-bus send -h\n", oneline.Field(source))
		case "re":
			fmt.Fprintf(stderr, "SEND REFUSED: Re: names one thread; name one id in %s; run: nova-bus send -h\n", oneline.Field(source))
		}
		return 2
	}
	if *dryRun {
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
			return 2
		}
		t, ok := openBus("send", *busDir, stderr)
		if !ok {
			return 2
		}
		prepared, err := bus.PrepareWith(t, text, now, bus.SendOptions{Slug: *slug, As: *as, Host: hostName})
		if err != nil {
			for _, reason := range bus.Reasons(err) {
				fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
			}
			return 1
		}
		printSendDraft(stdout, prepared, t.Config, now)
		return 0
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
		return 2
	}
	release, err := bus.LockCheckout(*busDir, checkoutLockWait)
	if err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
		return 1
	}
	defer release()
	t, ok := openBus("send", *busDir, stderr)
	if !ok {
		return 2
	}
	prepared, err := bus.PrepareWith(t, text, now, bus.SendOptions{Slug: *slug, As: *as, Host: hostName})
	if err != nil {
		// EVERY reason, one line each. A refusal that named the first of three mistakes in
		// a draft cost the writer three runs to find the other two, and the tool had read
		// all three before it printed anything.
		for _, reason := range bus.Reasons(err) {
			fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(reason))
		}
		return 1
	}
	// What the tolerances did, before anything is written, one line each. A tool that
	// quietly rewrites what a person wrote teaches nobody anything and cannot be checked;
	// these lines are the whole difference between a tolerance and a guess.
	for _, notice := range prepared.Notices {
		fmt.Fprintf(stdout, "SEND NOTE %s\n", oneline.Escape(notice))
	}
	// THE SENDER'S OWN BEAT IS NOT SOMEBODY ELSE'S WORK. `wait` writes
	// from-<me>/BEAT, and a wait killed between its tick and its commit leaves it in the
	// tree. Refusing over it told the writer their checkout held "changes that are not
	// this note" and named a file they had never touched, and their answer did not go out.
	// It is the caller's own machinery: it is allowed past the clean guard here and folded
	// into this note's commit below, so the send carries it out rather than stopping on it.
	// Every other path in the tree is still the refusal it always was.
	beat := bus.BeatPath(prepared.Sender.Lane)
	if err := checkoutReady(*busDir, *branch, []string{beat}); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "SEND REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus send -h"))
		return 1
	}
	if err := prepared.Save(*busDir); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The lane's catalogue is appended in the SAME commit as the note. A catalogue that
	// could lag the notes by a commit is one a reader between the two would resolve
	// wrongly, and a note that landed without its line would be invisible to every later
	// id lookup until somebody ran a rebuild.
	if err := prepared.AppendIndex(*busDir); err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	// The union-merge rules, written once per bus and committed with the note that first
	// needed them. INDEX and RECEIPTS are append-only, and without the rule two benches of
	// one lane appending at the same end over one base conflict and the bench is wedged. It
	// goes on the FIRST send rather than being asked of the bus's owner because a bus
	// that has to be prepared by hand before it is safe is a bus somebody will not
	// prepare. See internal/bus/attributes.go.
	paths := prepared.Paths()
	wroteAttrs, err := bus.EnsureMergeAttributes(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(bus.AttributesName), oneline.Err(err))
		return 1
	}
	if wroteAttrs {
		paths = append(paths, bus.AttributesName)
	}
	// The beat is folded in only when there is one: StagePaths drops a path that is
	// neither on disk nor in the index, because asking git to stage one that is neither is
	// exit 128, and a sender who has never run `wait` has no BEAT at all.
	paths, err = bus.StagePaths(*busDir, append(paths, beat))
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return 1
	}
	res, err := commit(*busDir, prepared.Sender, paths,
		bus.WithTrailer(prepared.Message, bus.TrailerSend+" "+prepared.Note.Header.ID),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "SEND FAIL %s: %s\n", oneline.Escape(prepared.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	to, _ := prepared.Note.Header.Recipients(t.Config)
	fmt.Fprintf(stdout, "SEND OK id=%s path=%s commit=%s pushed=%t attempts=%d wakes=%d body_bytes=%d\n",
		oneline.Field(prepared.Note.Header.ID), oneline.Field(prepared.Path), oneline.Field(res.Commit), res.Pushed, res.Attempts, len(to), len(prepared.Note.Body))
	return 0
}

// preflightDraft is the send --file preflight: the two mistakes a shaped note refuses
// before any commit. It returns which one it found and false, or "" and true.
//
// A hand-written Id is refused because the tool mints it, and a Re line naming more than
// one id is refused because a Re line names one thread. The Re check counts tokens that
// have the shape of a bus id, so a subject holding a comma is still the subject it is.
func preflightDraft(text string) (string, bool) {
	n, _ := bus.ParseNoteAll("", text)
	if strings.TrimSpace(n.Header.ID) != "" {
		return "id", false
	}
	if countBusIDs(n.Header.Re) > 1 {
		return "re", false
	}
	return "", true
}

// countBusIDs counts the id-shaped tokens across a note's Re lines: a Re line may separate
// ids with a comma, a semicolon or a blank, and only a token that is a bus id at all is
// counted.
func countBusIDs(res []string) int {
	count := 0
	for _, r := range res {
		spaced := strings.ReplaceAll(strings.ReplaceAll(r, ",", " "), ";", " ")
		for _, tok := range strings.Fields(spaced) {
			if isBusID(tok) {
				count++
			}
		}
	}
	return count
}

// isBusID reports the shape the tool mints: a sender slug, a hyphen, and twelve lower-case
// hex digits.
func isBusID(tok string) bool {
	i := strings.LastIndexByte(tok, '-')
	if i <= 0 || len(tok)-i-1 != 12 {
		return false
	}
	for _, r := range tok[i+1:] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// printSendDraft is `send --dry-run`: the shaped note as one line naming every field, then
// the note verbatim framed by an id, so a caller can pipe it to a file.
func printSendDraft(stdout io.Writer, p bus.Prepared, c *bus.Config, now time.Time) {
	id := p.Note.Header.ID
	to, cc := p.Note.Header.Recipients(c)
	re := "none"
	if len(p.Note.Header.Re) > 0 {
		re = p.Note.Header.Re[0]
	}
	note := p.Note.Render()
	fmt.Fprintf(stdout, "SEND DRAFT id=%s path=%s to=%d cc=%d re=%s subject=%s date=%s bytes=%d\n",
		oneline.Field(id), oneline.Field(p.Path), len(to), len(cc), oneline.Field(re),
		oneline.Field(p.Note.Header.Subject), oneline.Field(now.UTC().Format(time.RFC3339)), len(note))
	fmt.Fprintf(stdout, "SEND DRAFT id=%s\n", oneline.Field(id))
	fmt.Fprint(stdout, note)
	fmt.Fprintf(stdout, "SEND DRAFT END id=%s\n", oneline.Field(id))
}
