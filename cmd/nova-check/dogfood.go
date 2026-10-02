package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The dogfood verb answers the one question a tool's own tests cannot: has
// somebody who did not write it run it, on real work, with the edges filed? A
// tool is not finished until that is so, and without a record the claim is
// whatever the last person said.
//
// Three sub-verbs, and they are deliberately small: `ledger` reads the verb
// list against a directory of receipts and prints one row per verb; `record`
// appends one receipt; `gate` is the same read with an exit code, for a
// release to call. There is no state anywhere else — the receipts ARE the
// record, and every path comes from a flag.
//
// The verb list comes from the binaries when `--tools` names them, and from the
// command reference otherwise. Both are here because each closes a failure the
// other has: a tool documented in a shape the reader does not read contributes
// no rows and can never be gated on, and a verb that exists in the binary but
// not in the reference strands its receipts against a stale document.
const (
	cliHint      = `--cli <file> is the command reference the verbs are read from, usually docs/CLI.md; it is the list this ledger is about, so it is never guessed from the working directory`
	toolsHint    = `--tools <dir> is a directory of built nova-* binaries, each asked for its own help: the authoritative verb list, with --cli as the fallback for the tools it does not hold`
	receiptsHint = `--receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench`
	toolHint     = `--tool <t> is the binary you ran, spelled as the verb list spells it (nova-check)`
	verbHint     = `--verb <v> is the verb you ran, spelled as the verb list spells it (links, or "lift quarantine", or - for a tool that takes no verb)`
	byHint       = `--by <name> is who ran it; the ledger's whole question is whether that is somebody other than the author, so a receipt with no name is not a receipt`
	notesHint    = `--notes <text> is the real work you ran it on, in one line: what you were doing, what the verb did about it, and "Edges:" before anything you found`
	// cliShapeHint is the shape a command reference declares a verb in, for a
	// reader with an empty or a new one.
	cliShapeHint = "a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), " +
		"or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`"
)

// gitTimeoutDefault is the budget one `--repo` authorship read gets before it
// is killed and named. It is the same minute nova-bus gives one git
// subprocess: long enough for a cold repository, short enough that a wait has
// an end somebody can see. toolsTimeoutDefault is that budget for the whole
// `--tools` read, which is one `help` per binary.
const (
	gitTimeoutDefault   = 60
	toolsTimeoutDefault = 60
)

// sourceRemedy is the one line a run with nothing to read against gets. Both
// sources are named, because either one answers and neither is ever guessed.
const sourceRemedy = "name a verb list: --cli <docs/CLI.md>, or --tools <dir of built nova-* binaries>, or both; refusing to guess"

// Seams for tests. On a real run these stay nil/default and the production
// clock and runners are used.
var (
	dogfoodClock      = time.Now
	dogfoodGitRunner  dogfood.Runner
	dogfoodHelpRunner dogfood.HelpRunner
)

func cmdDogfood(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		verbflag.HelpIfAsked(args[:1], "dogfood")
	}
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

// dogfoodSources is where the verb list comes from: the binaries, the
// reference, or both. All three sub-verbs use the same list so record cannot
// accept a verb that ledger and gate would leave unmatched.
type dogfoodSources struct {
	cli     string
	tools   string
	timeout int
}

func addDogfoodSourceFlags(fs *flag.FlagSet) *dogfoodSources {
	var s dogfoodSources
	fs.StringVar(&s.cli, "cli", "", "the command reference the verbs are read from, usually docs/CLI.md")
	fs.StringVar(&s.tools, "tools", "", "directory of built nova-* binaries, each asked for its own verbs (authoritative)")
	fs.IntVar(&s.timeout, "tools-timeout", toolsTimeoutDefault, "seconds the whole --tools read may take before it is killed and named")
	return &s
}

// verbList reads the verb list from whichever sources were named. The binaries
// win and the reference fills in the tools they do not cover; a binary that
// cannot answer is one NOTE and its tool falls back to the reference, because a
// half-built directory should cost that tool's rows and not the whole ledger.
func (s *dogfoodSources) verbList(verb string, failMax int, stderr io.Writer) ([]dogfood.Verb, int) {
	if s.cli == "" && s.tools == "" {
		refuse(stderr, " dogfood "+verb, sourceRemedy)
		fmt.Fprintf(stderr, "  %s\n  %s\n", cliHint, toolsHint)
		return nil, 2
	}
	var fromTools []dogfood.Verb
	if s.tools != "" {
		if s.timeout <= 0 {
			return nil, refuse(stderr, " dogfood "+verb, "--tools-timeout must be positive; a read with no time budget will hang forever")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.timeout)*time.Second)
		defer cancel()
		// A program says what it is doing when what it is doing takes long
		// enough to look like a hang: twenty binaries answering `help` is a
		// second or two, and silence for a second or two reads as a stall.
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE asking the binaries for their verbs: %d/%d\n", done, total)
		})
		verbs, failures, err := dogfood.VerbsFromTools(ctx, s.tools, dogfoodHelpRunner, progress)
		if err != nil {
			return nil, refuse(stderr, " dogfood "+verb, oneline.Err(err))
		}
		list := bounded.Capped(stderr, failMax, "DOGFOOD", "binary", failMaxRemedy)
		for _, f := range failures {
			list.Line(fmt.Sprintf("DOGFOOD NOTE %s: %s",
				oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fromTools = verbs
	}
	var fromCLI []dogfood.Verb
	if s.cli != "" {
		verbs, err := dogfood.ParseCLI(s.cli)
		if err != nil {
			refuse(stderr, " dogfood "+verb, oneline.Err(err))
			fmt.Fprintf(stderr, "  %s\n", cliShapeHint)
			return nil, 2
		}
		fromCLI = verbs
	}
	merged := dogfood.MergeVerbs(fromTools, fromCLI)
	if len(merged) == 0 {
		refuse(stderr, " dogfood "+verb, "the sources named declare no verbs at all; a ledger over no verbs would say OK about nothing")
		fmt.Fprintf(stderr, "  %s\n", cliShapeHint)
		return nil, 2
	}
	return merged, 0
}

// dogfoodRead is the half `ledger` and `gate` share: the verbs, the receipts,
// and who wrote what.
type dogfoodRead struct {
	verbs    []dogfood.Verb
	receipts []dogfood.Receipt
	authors  dogfood.Authors
}

func dogfoodGather(verb string, src *dogfoodSources, receiptsDir, authorsFile, repo string, gitTimeout, failMax int, stderr io.Writer) (dogfoodRead, int) {
	var read dogfoodRead

	verbs, code := src.verbList(verb, failMax, stderr)
	if code != 0 {
		return read, code
	}
	read.verbs = verbs

	receipts, failures, err := dogfood.ReadReceipts(receiptsDir)
	if err != nil {
		return read, refuse(stderr, " dogfood "+verb, oneline.Err(err))
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
		if gitTimeout <= 0 {
			return read, refuse(stderr, " dogfood "+verb, "--git-timeout must be positive; a read with no time budget will hang forever")
		}
		fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git over %d verbs; this can take seconds\n", len(verbs))
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(gitTimeout)*time.Second)
		defer cancel()
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git: %d/%d verbs\n", done, total)
		})
		fromGit, err := dogfood.AuthorsFromGit(ctx, repo, verbs, dogfoodGitRunner, progress)
		if err != nil {
			return read, refuse(stderr, " dogfood "+verb, oneline.Err(err))
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
			return read, refuse(stderr, " dogfood "+verb, oneline.Err(err))
		}
		for key, name := range fromFile {
			authors.Set(key, name)
		}
	}
	read.authors = authors
	return read, 0
}

// reportStranded names every receipt that matched no verb: the file, what it
// claimed, and the verb it was probably meant to be. Both `ledger` and `gate`
// call it, on every outcome: a release must not pass or fail without learning
// that evidence it read was thrown away, and which, and how to spell it.
func reportStranded(read dogfoodRead, failMax int, stderr io.Writer) {
	strands := dogfood.Stranded(read.verbs, read.receipts)
	if len(strands) == 0 {
		return
	}
	list := bounded.Capped(stderr, failMax, "DOGFOOD", "receipt", failMaxRemedy)
	for _, s := range strands {
		list.Line(oneline.Escape(oneline.Cap(s.Line(), oneline.TailBytes)))
	}
	list.More()
	fmt.Fprintf(stderr, "DOGFOOD NOTE stranded=%d shown=%d: these receipts name no verb the list declares, so they count for nothing; fix the spelling, or the documentation they were checked against\n",
		list.Total(), list.Shown())
}

func addDogfoodReadFlags(fs *flag.FlagSet) (receipts, authors, repo *string, gitTimeout *int) {
	receipts = fs.String("receipts", "", "directory of receipts, one file per receipt (required)")
	authors = fs.String("authors", "", "file mapping `<tool> <verb> = <who wrote it>`, one per line")
	repo = fs.String("repo", "", "repository to read authorship from when there is no --authors file")
	gitTimeout = fs.Int("git-timeout", gitTimeoutDefault, "seconds one --repo authorship read may take before it is killed and named")
	return
}

func cmdDogfoodLedger(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood ledger", flag.ContinueOnError)
	src := addDogfoodSourceFlags(fs)
	receipts, authors, repo, gitTimeout := addDogfoodReadFlags(fs)
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"receipts": receipts}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	read, code := dogfoodGather("ledger", src, *receipts, *authors, *repo, *gitTimeout, *failMax, stderr)
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
	reportStranded(read, *failMax, stderr)
	return 0
}

func cmdDogfoodGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dogfood gate", flag.ContinueOnError)
	src := addDogfoodSourceFlags(fs)
	receipts, authors, repo, gitTimeout := addDogfoodReadFlags(fs)
	requireAll := fs.Bool("require-all", false, "every verb in the list must have been run by a non-author, not only the ones with receipts")
	allowEmpty := fs.Bool("allow-empty", false, "pass on an empty receipt set; without it, no receipts is a refusal and not a green line")
	shippedDir := fs.String("shipped", "", "a checkout's cmd/ directory: the gate judges only the tools under it, the set a release ships")
	failMax := addFailMax(fs)
	if !parse(fs, args, stderr, map[string]*string{"receipts": receipts}) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}
	read, code := dogfoodGather("gate", src, *receipts, *authors, *repo, *gitTimeout, *failMax, stderr)
	if code != 0 {
		return code
	}
	// The gate judges what ships. With --shipped, a receipt about a tool that
	// is not under that cmd/ is set aside and COUNTED on its own line: it is
	// true about a tool the release does not contain, and says nothing about
	// the ones it does. This is the read `nova-update release cut` does.
	if *shippedDir != "" {
		set, err := dogfood.ReadShipped(*shippedDir)
		if err != nil {
			return refuse(stderr, " dogfood gate", "--shipped names a cmd/ directory of nova-* programs: "+oneline.Err(err))
		}
		var outside []dogfood.Receipt
		read.verbs, read.receipts, outside = set.Scope(read.verbs, read.receipts)
		fmt.Fprintf(stderr, "DOGFOOD NOTE shipped=%d outside=%d cmd=%s: receipts naming a tool outside the shipped set are set aside\n",
			len(set.Tools()), len(outside), oneline.Field(*shippedDir))
	}
	// No receipts read is an empty evidence set, not a pass: the gate's whole
	// question is whether the verbs in the list have been dogfooded, and with
	// nothing read there is nothing to answer it. The refusal is by name so a
	// release cannot go green on nothing.
	if len(read.receipts) == 0 && !*allowEmpty {
		return refuseRan(stderr, " dogfood gate", fmt.Sprintf("no receipts were read from %s, so the gate has nothing to pass on; add receipts, or pass --allow-empty to say that is deliberate", oneline.Escape(*receipts)))
	}
	// The discarded receipts are said FIRST, and on every outcome.
	reportStranded(read, *failMax, stderr)
	findings, summary := dogfood.Gate(read.verbs, read.receipts, read.authors, *requireAll)
	if len(findings) == 0 {
		fmt.Fprintln(stdout, oneline.Escape(summary.GateLine(*requireAll)))
		return 0
	}
	// The token is one word: bounded escapes what it is given, and a token with
	// a space in it would print as `DOGFOOD\x20GATE MORE`.
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
	src := addDogfoodSourceFlags(fs)
	tool := fs.String("tool", "", "the binary you ran (required)")
	verb := fs.String("verb", "", "the verb you ran, or - for a tool that takes none (required)")
	by := fs.String("by", "", "who ran it (required)")
	notes := fs.String("notes", "", "the real work you ran it on, in one line (required)")
	receipts := fs.String("receipts", "", "directory the receipt is appended to (required)")
	ok := fs.Bool("ok", false, "the verb did what the run needed")
	notOK := fs.Bool("not-ok", false, "it did not; file the edge and name it with --issue")
	issue := fs.Int("issue", 0, "the issue number of the edge filed, when there is one")
	closes := fs.String("closes", "", "the id of the finding this run answers, as the gate prints it")
	dryRun := fs.Bool("dry-run", false, "make every check and print the receipt; write nothing")
	failMax := addFailMax(fs)
	if !parseFlags(fs, args, stderr) {
		return 2
	}
	// Every problem of the run at once: the missing flags, the verdict, the issue.
	good := requireFlags(fs, stderr, map[string]*string{
		"tool": tool, "verb": verb, "by": by, "notes": notes, "receipts": receipts,
	})
	// The verdict is stated, never defaulted: a receipt whose ok= came from the
	// absence of a flag would be a record of what somebody forgot to type.
	if *ok == *notOK {
		refuse(stderr, " dogfood record", "state the verdict exactly once: --ok when the verb did what the run needed, --not-ok when it did not; refusing to guess")
		good = false
	}
	if *issue < 0 {
		refuse(stderr, " dogfood record", fmt.Sprintf("--issue must be an issue number, got %d; leave it out when no edge was filed", *issue))
		good = false
	}
	if !good {
		return 2
	}
	// The spelling is checked against the same list the ledger will read it
	// against, so a receipt for a verb spelled differently is refused when it
	// is written rather than stranded, unread, later.
	verbs, code := src.verbList("record", *failMax, stderr)
	if code != 0 {
		return code
	}
	if !declares(verbs, *tool, *verb) {
		remedy := "no verb of that spelling is declared, and nothing is close enough to suggest"
		if nearest := dogfood.Nearest(verbs, *tool, *verb); nearest != "" {
			remedy = "did you mean: " + nearest
		}
		return refuse(stderr, " dogfood record", fmt.Sprintf("%s %s is not a verb the list declares; %s",
			oneline.Field(*tool), oneline.Field(*verb), oneline.Escape(remedy)))
	}
	// A --closes that answers a finding nobody can point at is a close nobody can
	// check. The id is the eight characters the gate prints beside the edge and
	// the same eight that end the receipt's filename, so it is checked for shape
	// here and matched against the real findings by the ledger: an id that names
	// nothing closes nothing, and says so by leaving the edge open.
	if id := strings.TrimSpace(*closes); id != "" && !dogfood.IsReceiptID(id) {
		fmt.Fprintf(stderr, "nova-check dogfood record REFUSED: --closes is a receipt id, the eight hex characters the gate prints as receipt=<id>, got %s; run: nova-check dogfood record -h\n", oneline.Field(id))
		return 2
	}
	receipt := dogfood.Receipt{
		Tool:   *tool,
		Verb:   *verb,
		By:     *by,
		At:     dogfoodClock().UTC().Format(time.RFC3339),
		OK:     *ok,
		Notes:  *notes,
		Issue:  *issue,
		Closes: strings.TrimSpace(*closes),
	}
	// A dry run is this record's own plan: every check the write makes, the
	// same refusal, the path it would take, and nothing written.
	write := dogfood.Record
	if *dryRun {
		write = dogfood.PlanRecord
	}
	path, err := write(*receipts, receipt)
	if err != nil {
		return refuse(stderr, " dogfood record", oneline.Err(err))
	}
	if *dryRun {
		fmt.Fprintln(stdout, oneline.Escape(receipt.RecordLine(path)+" dry_run=true"))
		return 0
	}
	fmt.Fprintln(stdout, oneline.Escape(receipt.RecordLine(path)))
	return 0
}

// declares reports whether the list holds this tool and verb, as spelled.
func declares(verbs []dogfood.Verb, tool, verb string) bool {
	want := dogfood.NormalizeKey(tool, verb)
	for _, v := range verbs {
		if v.Key() == want {
			return true
		}
	}
	return false
}
