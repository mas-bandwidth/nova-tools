package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdDraft prints the header a note needs and nothing else.
//
// WHY A VERB AND NOT A PARAGRAPH IN THE README. A line's first send is a header they are
// writing from memory of some other bus, and the tool's answer to a header written from
// memory was a refusal per mistake. The skeleton is the same header send writes, with the
// names already checked against the roster, so the first draft cannot be wrong about the
// two things a first draft is always wrong about: what the keys are, and how a name is
// spelled here.
//
// Its standard output is a FILE: the skeleton, alone, with no OK line under it, so
// `nova-bus draft ... > draft.md` is a draft. Refusals go to stderr like every other
// verb's, and every one of them is printed rather than the first.
func cmdDraft(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("draft")
	busDir := f.fs.String("bus", "", "the bus's repository root (required: the roster lives in it)")
	as := f.fs.String("as", "", "which participant you are (required)")
	to := f.fs.String("to", "", "who the note is to, as a To line: names, aliases or a group, separated by ; (required)")
	cc := f.fs.String("cc", "", "who else is to see it, as a Cc line")
	subject := f.fs.String("subject", "", "the subject line (default: a placeholder you must replace)")
	var re stringList
	f.fs.Var(&re, "re", "an id, a path, or the SUBJECT of a note on your open list that this note answers, or `new` to start a thread (repeatable)")
	out := f.fs.String("out", "", "write the draft skeleton to this file instead of standard output")
	overwrite := f.fs.Bool("overwrite", false, "allow replacing an existing file named by --out")
	dryRun := f.fs.Bool("dry-run", false, "with --out, check the path and print it, and write nothing (the reply form, --reply-to, fetches the bus and has no dry run)")
	f.fs.String("file", "", "retired: use --out instead")
	// The reply form's flags. Every one of them is inert without --reply-to, which is what
	// keeps the released form byte-identical: see cmd/nova-bus/reply.go.
	replyTo := f.fs.String("reply-to", "", "an id, a path, or the SUBJECT of a note on your live listing to ANSWER: the reply form, which refreshes the bus and writes the whole header for you")
	bodyFile := f.fs.String("body-file", "", "the reply's body, as a file: body text and never a header (--reply-to only)")
	draftDir := f.fs.String("draft-dir", "", "where the reply is written, OUTSIDE the bus checkout (--reply-to only)")
	remote := f.fs.String("remote", "", "the remote the reply is resolved against, after a fetch (--reply-to only)")
	branch := f.fs.String("branch", "", "the branch the reply is resolved against, after a fetch (--reply-to only)")
	maxBodyBytes := f.fs.Int("max-body-bytes", defaultMaxBodyBytes, "the budget --body-file is read under")
	gitTimeout := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long the reply form's fetch may take (--reply-to only)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as}) {
		return 2
	}
	given := map[string]bool{}
	f.fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
	if given["file"] {
		fmt.Fprint(stderr, "DRAFT REFUSED: --file is retired because --file means input on send; use --out <path> (or --out <path> --overwrite); run: nova-bus draft -h\n")
		return 2
	}
	if *overwrite && strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "DRAFT REFUSED: --overwrite requires --out; run: nova-bus draft -h")
		return 2
	}
	if !given["reply-to"] {
		// A reply-only flag without the flag that means the reply form: this form runs no
		// git and writes no file, so there is nothing for it to do. Exit 2, which is what
		// an undefined flag already costs, with a sentence in place of `not defined`.
		refused := false
		for _, name := range replyOnlyFlags {
			if given[name] {
				fmt.Fprintf(stderr, "DRAFT REFUSED: --%s belongs to --reply-to; without it draft runs no git, and without --out it writes no file; run: nova-bus draft -h\n", name)
				refused = true
			}
		}
		if refused {
			return 2
		}
		if strings.TrimSpace(*to) == "" {
			fmt.Fprintf(stderr, "DRAFT REFUSED: --to is required; refusing to guess; run: nova-bus draft -h\n")
			return 2
		}
	} else {
		if *dryRun {
			fmt.Fprint(stderr, "DRAFT REFUSED: --dry-run is for --out; the reply form fetches and fast-forwards the checkout to resolve --reply-to, so it has no run that writes nothing; drop --dry-run; run: nova-bus draft -h\n")
			return 2
		}
		return cmdDraftReply(replyOpts{
			busDir: *busDir, as: *as, to: *to, cc: *cc, subject: *subject,
			replyTo: *replyTo, bodyFile: *bodyFile, draftDir: *draftDir,
			remote: *remote, branch: *branch, maxBodyBytes: *maxBodyBytes,
			gitTimeout: *gitTimeout,
			reGiven:    len(re) > 0, toGiven: given["to"], ccGiven: given["cc"],
			subjectGiven: given["subject"],
		}, f, stdout, stderr, now)
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
		return 2
	}
	// Collected, like send's: a draft asked for with a misspelled name and a Re that is
	// not on the bus is two mistakes and one run.
	var problems []error
	if *out != "" {
		cur := filepath.Dir(*out)
		for {
			if fi, err := os.Stat(cur); err == nil && fi.IsDir() {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
		curResolved := resolveForCompare(cur)
		root := resolveForCompare(*busDir)
		if curResolved == root || strings.HasPrefix(curResolved, root+string(filepath.Separator)) {
			problems = append(problems, fmt.Errorf("--out %s is inside the bus checkout at %s; drafts go outside the bus, because send needs its tree clean", *out, root))
		}
	}
	if err := bus.OneLine("--subject", *subject); err != nil {
		problems = append(problems, err)
	}
	me, known := c.Lookup(*as)
	switch {
	case !known:
		problems = append(problems, fmt.Errorf("--as %q names no one on this bus (known: %s)", *as, strings.Join(c.KnownNames(), "; ")))
	case me.Lane == "":
		problems = append(problems, fmt.Errorf("--as %q has no lane on this bus, so has nowhere to send from", me.Name))
	}
	for _, line := range []struct{ flag, value string }{{"--to", *to}, {"--cc", *cc}} {
		if strings.TrimSpace(line.value) == "" {
			continue
		}
		var names, unknown []string
		if line.flag == "--to" {
			// A To line may name the broadcast aliases "all" and "table"; they are
			// vouched for here, unexpanded, and resolved from the roster at send time.
			names, unknown = c.ResolveBroadcast(line.value, me)
		} else {
			names, unknown = c.ResolveList(line.value)
		}
		if len(unknown) > 0 {
			problems = append(problems, fmt.Errorf("%s: %s names no one on this bus (known: %s)", line.flag, strings.Join(bus.UnknownNames(unknown), ", "), strings.Join(c.KnownNames(), "; ")))
			continue
		}
		if len(names) == 0 {
			problems = append(problems, fmt.Errorf("%s: no recipients", line.flag))
		}
	}
	// A Re is checked against the BUS and not the roster, so this is the one thing here
	// that opens the notes -- and only when a --re was given.
	//
	// IT ALSO TAKES A SUBJECT, which is the whole reason this flag is worth reaching for.
	// A line answering a note has the note in front of it: its sender, its subject, its
	// text. What it does not have is the id, which lives on a header line it has to go and
	// find. So `--re "the merge queue"` resolves against this line's own open list and the
	// skeleton comes back carrying `Re: <id>` -- the id written by the tool that knows it,
	// into the draft, once, instead of by a person, into every reply, from memory. The
	// refusals below and the notices go to stderr, because stdout here is a FILE.
	if len(re) > 0 {
		t, terr := bus.ReadBus(*busDir, c)
		if terr != nil {
			fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(terr), "nova-bus draft -h"))
			return 2
		}
		var open []bus.OpenEntry
		var openErr error
		if known && me.Lane != "" {
			open, openErr = bus.ReadOpen(*busDir, me.Lane)
		}
		openErrReported := false
		for i, r := range re {
			if r == "new" {
				continue
			}
			if _, found := t.Resolve(r); found {
				continue
			}
			if openErr != nil {
				if !openErrReported {
					// Resolve the actual inbox threshold for this bus and environment before
					// suggesting a recovery command. If none is configured, the diagnostic
					// keeps an explicit placeholder instead of guessing.
					maxWords, haveMaxWords := f.receiptMaxWords(0, false, *busDir, io.Discard)
					problems = append(problems, draftOpenReadFailure(*busDir, me, maxWords, haveMaxWords, openErr))
					openErrReported = true
				}
				continue
			}
			matches := bus.MatchOpenSubject(open, r)
			if len(matches) == 0 {
				problems = append(problems, bus.UnresolvedRe(t, "--re", r))
				continue
			}
			re[i] = matches[0].Target()
			if len(matches) > 1 {
				fmt.Fprintf(stderr, "DRAFT NOTE --re: subject matched %d notes; the skeleton names the newest %s; name the id to be exact\n", len(matches), oneline.Field(re[i]))
				continue
			}
			fmt.Fprintf(stderr, "DRAFT NOTE --re named the subject %s rather than an id; the skeleton names the open note %s from %s\n",
				oneline.Quote(r), oneline.Field(re[i]), oneline.Field(dash(matches[0].From)))
		}
	}
	if len(problems) > 0 {
		for _, reason := range problems {
			fmt.Fprintf(stderr, "DRAFT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(reason), "nova-bus draft -h"))
		}
		return 2
	}
	skeleton := bus.Skeleton{From: me.Name, To: *to, Cc: *cc, Re: re, Subject: *subject}.Render()
	if *out != "" {
		return writeDraftOut(*out, *overwrite, *dryRun, skeleton, stdout, stderr)
	}
	fmt.Fprint(stdout, skeleton)
	fmt.Fprintf(stderr, "DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>\n")
	return 0
}

func draftOpenReadFailure(busDir string, me bus.Participant, maxWords int, haveMaxWords bool, err error) error {
	wordLimit := "'<receipt-word-limit>'"
	wordLimitNote := "replace the receipt-word-limit placeholder with a positive word-count threshold for classifying short receipts; configure it in <bus>/.nova-bus/defaults or NOVA_BUS_RECEIPT_MAX_WORDS"
	if haveMaxWords {
		wordLimit = fmt.Sprintf("%d", maxWords)
		wordLimitNote = fmt.Sprintf("--receipt-max-words %d is the resolved positive word-count threshold for classifying short receipts", maxWords)
	}
	recovery := fmt.Sprintf("nova-bus inbox --bus %s --as %s --receipt-max-words %s --full --carry-history --advance --remote '<your-remote>' --branch '<your-branch>'", oneline.Escape(shellQuote(busDir)), oneline.Escape(shellQuote(me.Name)), oneline.Escape(wordLimit))
	recoveryNote := "--carry-history preserves existing history, avoids first-advance refusal or discarding prior notes, and --advance moves and pushes the cursor"
	placeholders := "replace the remote and branch placeholders; " + wordLimitNote
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("cannot read %s (%v); repair access to that OPEN path first, then rebuild it from the bus; %s; run: %s; %s", bus.OpenPath(me.Lane), err, recoveryNote, recovery, placeholders)
	}
	detail := strings.TrimSuffix(err.Error(), "; read once with --full --advance, which writes the list again from the whole bus")
	return fmt.Errorf("%s is invalid (%s); rebuild it from the bus; %s; run: %s; %s", bus.OpenPath(me.Lane), detail, recoveryNote, recovery, placeholders)
}

// draftOutPreflight is every refusal writeDraftOut can know before it writes, read-only,
// so the real run and the dry run refuse alike: a path that is there without --overwrite,
// a directory, a path Lstat cannot read, and a parent that is missing or is not a
// directory. It prints the refusal and returns its exit code, or 0 when the write may go.
func draftOutPreflight(path string, overwrite bool, stderr io.Writer) int {
	fi, err := os.Lstat(path)
	switch {
	case err == nil && !overwrite:
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s exists; pass --overwrite to replace it\n", oneline.Field(path))
		return 1
	case err == nil && fi.IsDir():
		fmt.Fprintf(stderr, "DRAFT REFUSED: %s is a directory; run: nova-bus draft -h\n", oneline.Field(path))
		return 1
	case err != nil && !errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
		return 2
	}
	parent := filepath.Dir(path)
	if pi, err := os.Stat(parent); err != nil || !pi.IsDir() {
		fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: the directory %s is not there; make it, or name a file in a directory that is; run: nova-bus draft -h\n", oneline.Field(path), oneline.Field(parent))
		return 2
	}
	return 0
}

// writeDraftOut writes the skeleton to path, after the preflight both forms share; a dry
// run stops after the preflight and prints the line the write would.
func writeDraftOut(path string, overwrite, dryRun bool, skeleton string, stdout, stderr io.Writer) int {
	if code := draftOutPreflight(path, overwrite, stderr); code != 0 {
		return code
	}
	if dryRun {
		fmt.Fprintf(stdout, "DRAFT OK path=%s dry_run=true\n", oneline.Field(path))
		return 0
	}
	return writeSkeleton(path, overwrite, skeleton, stdout, stderr)
}

// writeSkeleton is the write itself: atomic creation, or a replacement under --overwrite.
func writeSkeleton(path string, overwrite bool, skeleton string, stdout, stderr io.Writer) int {
	if !overwrite {
		// Atomic creation: refuse if the path already exists (including dangling symlinks).
		// os.Lstat catches existing files, directories, and symlinks on platforms where
		// O_NOFOLLOW is not supported (such as Windows).
		if _, err := os.Lstat(path); err == nil {
			fmt.Fprintf(stderr, "DRAFT REFUSED: %s exists; pass --overwrite to replace it\n", oneline.Field(path))
			return 1
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|bus.ONoFollow, 0o666)
		if err != nil {
			if errors.Is(err, os.ErrExist) || isPathExist(path) {
				fmt.Fprintf(stderr, "DRAFT REFUSED: %s exists; pass --overwrite to replace it\n", oneline.Field(path))
				return 1
			}
			fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
			return 2
		}
		if _, err := fmt.Fprint(f, skeleton); err != nil {
			f.Close()
			fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
			return 2
		}
		if err := f.Close(); err != nil {
			fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
			return 2
		}
		fmt.Fprintf(stdout, "DRAFT OK path=%s\n", oneline.Field(path))
		return 0
	}

	// When --overwrite is allowed:
	// If path exists (including as a dangling or valid symlink):
	if fi, err := os.Lstat(path); err == nil {
		if fi.IsDir() {
			fmt.Fprintf(stderr, "DRAFT REFUSED: %s is a directory; run: nova-bus draft -h\n", oneline.Field(path))
			return 1
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(stderr, "DRAFT REFUSED: overwrite %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
			return 2
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|bus.ONoFollow, 0o666)
	if err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
		return 2
	}
	if _, err := fmt.Fprint(f, skeleton); err != nil {
		f.Close()
		fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
		return 2
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(stderr, "DRAFT REFUSED: write %s: %s\n", oneline.Field(path), oneline.WithRemedy(oneline.Err(err), "nova-bus draft -h"))
		return 2
	}
	fmt.Fprintf(stdout, "DRAFT OK path=%s\n", oneline.Field(path))
	return 0
}

func isPathExist(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
