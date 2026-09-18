package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The dogfood verb answers the one question a tool's own tests cannot: has
// somebody who did not write it run it? Glenn, 2026-09-18: a tool is not
// finished until it is tested, dogfooded by a non-author on real work with the
// edges filed, the feedback applied, documented and released. Nothing tracked
// that, so the claim was whatever the last person said it was.
//
// Three sub-verbs, and they are deliberately small: `ledger` reads the command
// reference against a directory of receipts and prints one row per verb;
// `record` appends one receipt; `gate` is the same read with an exit code, for
// the release lane to call. There is no state anywhere else — the receipts ARE
// the record, and every path comes from a flag.
const (
	cliHint      = `--cli <file> is the command reference the verbs are read from, usually docs/CLI.md; it is the list this ledger is about, so it is never guessed from the working directory`
	receiptsHint = `--receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench`
	toolHint     = `--tool <t> is the binary you ran, spelled as the command reference spells it (nova-check)`
	verbHint     = `--verb <v> is the verb you ran, spelled as the command reference spells it (links, or "lift quarantine")`
	byHint       = `--by <name> is who ran it; the ledger's whole question is whether that is somebody other than the author, so a receipt with no name is not a receipt`
	notesHint    = `--notes <text> is the real work you ran it on, in one line: what you were doing, and what the verb did about it`
)

// gitTimeoutDefault is the budget one `--repo` authorship read gets before it
// is killed and named. It is the same minute nova-bus gives one git
// subprocess: long enough for a cold repository, short enough that a wait has
// an end somebody can see.
const gitTimeoutDefault = 60

func cmdDogfood(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " dogfood", "no sub-verb given; ledger reads it, record writes one receipt, gate is the one with an exit code")
	}
	switch args[0] {
	case "ledger":
		return cmdDogfoodLedger(args[1:], stdout, stderr)
	case "record":
		return cmdDogfoodRecord(args[1:], stdout, stderr)
	case "gate":
		return cmdDogfoodGate(args[1:], stdout, stderr)
	default:
		return refuse(stderr, " dogfood", fmt.Sprintf("unknown sub-verb %q; the three are ledger, record and gate", args[0]))
	}
}

// dogfoodRead is the half `ledger` and `gate` share: the verbs the reference
// declares, the receipts, and who wrote what. A failure here is a refusal (2)
// or a broken record (1); both are reported before anything else is printed,
// because a ledger read from records it could not parse would understate the
// truth in the direction that lets a tool ship.
type dogfoodRead struct {
	verbs    []dogfood.Verb
	receipts []dogfood.Receipt
	authors  dogfood.Authors
}

func dogfoodGather(verb string, cli, receiptsDir, authorsFile, repo string, gitTimeout, failMax int, stderr io.Writer) (dogfoodRead, int) {
	var read dogfoodRead

	verbs, err := dogfood.ParseCLI(cli)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check dogfood %s: %s\n", oneline.Escape(verb), oneline.Err(err))
		return read, 2
	}
	read.verbs = verbs

	receipts, failures, err := dogfood.ReadReceipts(receiptsDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check dogfood %s: %s\n", oneline.Escape(verb), oneline.Err(err))
		return read, 2
	}
	if len(failures) > 0 {
		list := bounded.Capped(stderr, failMax, "DOGFOOD", "record", failMaxRemedy)
		for _, f := range failures {
			list.Line(fmt.Sprintf("DOGFOOD FAIL %s: %s",
				oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "DOGFOOD FAIL records=%d shown=%d receipts=%s\n",
			list.Total(), list.Shown(), oneline.Field(receiptsDir))
		return read, 1
	}
	read.receipts = receipts

	authors := dogfood.Authors{}
	if repo != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(gitTimeout)*time.Second)
		defer cancel()
		// A program says what it is doing when what it is doing takes long
		// enough to look like a hang: one git call per verb over a repository
		// with history is seconds, and silence for seconds reads as a stall.
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git: %d/%d verbs\n", done, total)
		})
		fromGit, err := dogfood.AuthorsFromGit(ctx, repo, verbs, nil, progress)
		if err != nil {
			fmt.Fprintf(stderr, "nova-check dogfood %s: %s\n", oneline.Escape(verb), oneline.Err(err))
			return read, 2
		}
		for key, name := range fromGit {
			authors.Set(key, name)
		}
	}
	if authorsFile != "" {
		// The mapping file is exact and git is evidence, so the file wins
		// wherever both have an opinion.
		fromFile, err := dogfood.ParseAuthors(authorsFile)
		if err != nil {
			fmt.Fprintf(stderr, "nova-check dogfood %s: %s\n", oneline.Escape(verb), oneline.Err(err))
			return read, 2
		}
		for key, name := range fromFile {
			authors.Set(key, name)
		}
	}
	read.authors = authors
	return read, 0
}

// addDogfoodReadFlags puts the four flags `ledger` and `gate` share on a flag
// set, so the two verbs cannot drift apart in what they read.
func addDogfoodReadFlags(fs *flag.FlagSet) (cli, receipts, authors, repo *string, gitTimeout *int) {
	cli = fs.String("cli", "", "the command reference the verbs are read from, usually docs/CLI.md (required)")
	receipts = fs.String("receipts", "", "directory of receipts, one file per receipt (required)")
	authors = fs.String("authors", "", "file mapping `<tool> <verb> = <who wrote it>`, one per line")
	repo = fs.String("repo", "", "repository to read authorship from when there is no --authors file")
	gitTimeout = fs.Int("git-timeout", gitTimeoutDefault, "seconds one --repo authorship read may take before it is killed and named")
	return
}

func cmdDogfoodLedger(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood ledger", flag.ContinueOnError)
	cli, receipts, authors, repo, gitTimeout := addDogfoodReadFlags(fs)
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"cli": cli, "receipts": receipts}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	read, code := dogfoodGather("ledger", *cli, *receipts, *authors, *repo, *gitTimeout, *failMax, stderr)
	if code != 0 {
		return code
	}
	rows, summary := dogfood.Ledger(read.verbs, read.receipts, read.authors)
	// Every row prints. A ledger that elided verbs under a ceiling would be a
	// ledger that lies by omission about exactly the verbs nobody has run; the
	// summary line is the bounded read of the same thing.
	for _, row := range rows {
		fmt.Fprintln(stdout, oneline.Escape(row.Line()))
	}
	fmt.Fprintln(stdout, oneline.Escape(summary.Line()))
	if summary.Unknown > 0 {
		fmt.Fprintf(stderr, "DOGFOOD NOTE receipts=%d name a verb %s does not declare: the reference and the receipts disagree, and one of them is wrong\n",
			summary.Unknown, oneline.Field(*cli))
	}
	return 0
}

func cmdDogfoodGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood gate", flag.ContinueOnError)
	cli, receipts, authors, repo, gitTimeout := addDogfoodReadFlags(fs)
	requireAll := fs.Bool("require-all", false, "every verb in the reference must have been run by a non-author, not only the ones with receipts")
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"cli": cli, "receipts": receipts}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	read, code := dogfoodGather("gate", *cli, *receipts, *authors, *repo, *gitTimeout, *failMax, stderr)
	if code != 0 {
		return code
	}
	findings, summary := dogfood.Gate(read.verbs, read.receipts, read.authors, *requireAll)
	if len(findings) == 0 {
		fmt.Fprintln(stdout, oneline.Escape(summary.GateLine(*requireAll)))
		return 0
	}
	// The token is one word: bounded escapes what it is given, and a token with
	// a space in it came back as `DOGFOOD\x20GATE MORE` the first time this verb
	// was run against this repository's own reference.
	list := bounded.Capped(stderr, *failMax, "DOGFOOD", "verb", failMaxRemedy)
	for _, f := range findings {
		list.Line(oneline.Escape(oneline.Cap(f.Line(), oneline.TailBytes)))
	}
	list.More()
	fmt.Fprintln(stderr, oneline.Escape(summary.GateCountLine(list.Total(), list.Shown())))
	return 1
}

func cmdDogfoodRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood record", flag.ContinueOnError)
	tool := fs.String("tool", "", "the binary you ran (required)")
	verb := fs.String("verb", "", "the verb you ran (required)")
	by := fs.String("by", "", "who ran it (required)")
	notes := fs.String("notes", "", "the real work you ran it on, in one line (required)")
	receipts := fs.String("receipts", "", "directory the receipt is appended to (required)")
	ok := fs.Bool("ok", false, "the verb did what the run needed")
	notOK := fs.Bool("not-ok", false, "it did not; file the edge and name it with --issue")
	issue := fs.Int("issue", 0, "the issue number of the edge filed, when there is one")
	if !parse(fs, args, stderr, map[string]*string{
		"tool": tool, "verb": verb, "by": by, "notes": notes, "receipts": receipts,
	}) {
		return 2
	}
	// The verdict is stated, never defaulted: a receipt whose ok= came from the
	// absence of a flag would be a record of what somebody forgot to type.
	if *ok == *notOK {
		fmt.Fprintln(stderr, "nova-check dogfood record: state the verdict exactly once: --ok when the verb did what the run needed, --not-ok when it did not; refusing to guess")
		return 2
	}
	if *issue < 0 {
		fmt.Fprintf(stderr, "nova-check dogfood record: --issue must be an issue number, got %d; leave it out when no edge was filed\n", *issue)
		return 2
	}
	receipt := dogfood.Receipt{
		Tool:  *tool,
		Verb:  *verb,
		By:    *by,
		At:    time.Now().UTC().Format(time.RFC3339),
		OK:    *ok,
		Notes: *notes,
		Issue: *issue,
	}
	path, err := dogfood.Record(*receipts, receipt)
	if err != nil {
		fmt.Fprintf(stderr, "nova-check dogfood record: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintln(stdout, oneline.Escape(receipt.RecordLine(path)))
	return 0
}
