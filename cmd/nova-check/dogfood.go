package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The dogfood verb answers the one question a tool's own tests cannot: has
// somebody who did not write it run it, on real work, with the edges filed? A
// tool is not finished until that is so, and without a record the claim is
// whatever the last person said.
//
// Three sub-verbs, and they are deliberately small: `ledger` reads the verb
// list against a directory of receipts and prints one row per verb; `record`
// appends one receipt; `gate` is the same read with an exit code, for the
// release lane to call. There is no state anywhere else — the receipts ARE the
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
// is killed and named. It is one minute, the budget a git
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

// dogfoodSeams are the process-wide resources one dogfood run reads: the clock
// that stamps a receipt, the runner that reads authorship from git, and the
// runner that asks each binary for its help. The zero value is the production
// defaults; a test fills one field so it runs beside its neighbours instead of
// assigning a package variable, which would race every parallel test reading
// it.
type dogfoodSeams struct {
	clock      func() time.Time
	gitRunner  dogfood.Runner
	helpRunner dogfood.HelpRunner
}

// now is the receipt stamp: the injected clock when a test supplied one, and
// time.Now on a real run.
func (s dogfoodSeams) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// dogfoodVerb is one of the three sub-verbs as a tool.Verb of the group
// `dogfood`. Each prints its own lines (Prints): the rows, summaries and
// findings are the lines pkg/dogfood builds (Row.Line, Summary.Line,
// Finding.Line, Receipt.RecordLine), the DOGFOOD NOTE lines it adds while it
// works, and the DOGFOOD FAILED lines of an unreadable receipt set. Its refusals
// and the gate's "no receipts" answer are the skeleton's.
func dogfoodVerb(sub string, s seams) tool.Verb {
	v := tool.Verb{
		Name:      "dogfood " + sub,
		Detail:    "  " + cliShapeHint,
		ExitTable: exitCodes,
	}
	switch sub {
	case "ledger":
		v.Usage = "dogfood ledger (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--max <n>]"
		v.Effect = tool.Effect("inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing")
		v.Flags = func(f *tool.Flags) {
			f.Prints()
			addDogfoodSourceFlags(f)
			addDogfoodReadFlags(f)
			addMax(f)
		}
		v.Run = func(c *tool.Call) *tool.Out { return dogfoodLedger(c, s.dogfood) }
	case "record":
		v.Usage = "dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) " +
			"--notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--max <n>] [--dry-run]"
		v.Effect = tool.Effect("local write: appends one receipt file to --receipts (--dry-run writes none)")
		v.DryRun = true
		v.Flags = func(f *tool.Flags) {
			f.Prints()
			addDogfoodSourceFlags(f)
			f.Required("tool", toolHint)
			f.Required("verb", verbHint)
			f.Required("by", byHint)
			f.Required("notes", notesHint)
			f.Required("receipts", receiptsHint)
			f.Bool("ok", false, "the verb did what the run needed")
			f.Bool("not-ok", false, "it did not; file the edge and name it with --issue")
			f.Int("issue", 0, "the issue number of the edge filed, when there is one")
			f.String("closes", "", "the id of the finding this run answers, as the gate prints it")
			addMax(f)
			f.Check(func(c *tool.Call) {
				// The verdict is stated, never defaulted: a receipt whose ok= came
				// from the absence of a flag would be a record of what somebody
				// forgot to type.
				if c.Bool("ok") == c.Bool("not-ok") {
					c.Problem("state the verdict exactly once: --ok when the verb did what the run needed, --not-ok when it did not; refusing to guess")
				}
				if n := c.Int("issue"); n < 0 {
					c.Problem(fmt.Sprintf("--issue must be an issue number, got %d; leave it out when no edge was filed", n))
				}
			})
		}
		v.Run = func(c *tool.Call) *tool.Out { return dogfoodRecord(c, s.dogfood) }
	default:
		v.Usage = "dogfood gate (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] " +
			"[--shipped <cmd dir>] [--require-all] [--allow-empty] [--max <n>]"
		v.Effect = tool.Effect("inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing")
		v.Flags = func(f *tool.Flags) {
			f.Prints()
			addDogfoodSourceFlags(f)
			addDogfoodReadFlags(f)
			cmdDogfoodGate(f)
			addMax(f)
		}
		v.Run = func(c *tool.Call) *tool.Out { return dogfoodGate(c, s.dogfood) }
	}
	return v
}

// dogfoodSources is where the verb list comes from: the binaries, the
// reference, or both. All three sub-verbs take them, because a `record` that
// checked a spelling against nothing is what stranded nine receipts.
type dogfoodSources struct {
	cli     string
	tools   string
	timeout int
}

func addDogfoodSourceFlags(f *tool.Flags) {
	f.String("cli", "", "the command reference the verbs are read from, usually docs/CLI.md")
	f.String("tools", "", "directory of built nova-* binaries, each asked for its own verbs (authoritative)")
	f.Int("tools-timeout", toolsTimeoutDefault, "seconds the whole --tools read may take before it is killed and named")
}

func sourcesOf(c *tool.Call) *dogfoodSources {
	return &dogfoodSources{cli: c.Str("cli"), tools: c.Str("tools"), timeout: c.Int("tools-timeout")}
}

// verbList reads the verb list from whichever sources were named. The binaries
// win and the reference fills in the tools they do not cover; a binary that
// cannot answer is one NOTE and its tool falls back to the reference, because a
// half-built directory should cost that tool's rows and not the whole ledger.
// A non-nil Out is the verb's answer and ends the run.
func (s *dogfoodSources) verbList(seams dogfoodSeams, maxFlag int, stderr io.Writer) ([]dogfood.Verb, *tool.Out) {
	if s.cli == "" && s.tools == "" {
		return nil, tool.Refuse(sourceRemedy + "; " + cliHint + "; " + toolsHint)
	}
	var fromTools []dogfood.Verb
	if s.tools != "" {
		if s.timeout <= 0 {
			return nil, tool.Refuse("--tools-timeout must be positive; a read with no time budget will hang forever")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.timeout)*time.Second)
		defer cancel()
		// A program says what it is doing when what it is doing takes long
		// enough to look like a hang: twenty binaries answering `help` is a
		// second or two, and silence for a second or two reads as a stall.
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE asking the binaries for their verbs: %d/%d\n", done, total)
		})
		verbs, failures, err := dogfood.VerbsFromTools(ctx, s.tools, seams.helpRunner, progress)
		if err != nil {
			return nil, tool.Refuse(oneline.Err(err))
		}
		list := bounded.Capped(stderr, maxFlag, "DOGFOOD", "binary", tool.MaxRemedy)
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
			return nil, tool.Refuse(oneline.Err(err) + "; " + cliShapeHint)
		}
		fromCLI = verbs
	}
	merged := dogfood.MergeVerbs(fromTools, fromCLI)
	if len(merged) == 0 {
		return nil, tool.Refuse("the sources named declare no verbs at all; a ledger over no verbs would say OK about nothing; " + cliShapeHint)
	}
	return merged, nil
}

// dogfoodRead is the half `ledger` and `gate` share: the verbs, the receipts,
// and who wrote what.
type dogfoodRead struct {
	verbs    []dogfood.Verb
	receipts []dogfood.Receipt
	authors  dogfood.Authors
}

func dogfoodGather(seams dogfoodSeams, c *tool.Call) (dogfoodRead, *tool.Out) {
	var read dogfoodRead
	receiptsDir, maxFlag, stderr := c.Str("receipts"), c.Int("max"), c.Stderr

	verbs, out := sourcesOf(c).verbList(seams, maxFlag, stderr)
	if out != nil {
		return read, out
	}
	read.verbs = verbs

	receipts, failures, err := dogfood.ReadReceipts(receiptsDir)
	if err != nil {
		return read, tool.Refuse(oneline.Err(err))
	}
	if len(failures) > 0 {
		list := bounded.Capped(stderr, maxFlag, "DOGFOOD", "record", tool.MaxRemedy)
		for _, f := range failures {
			list.Line(fmt.Sprintf("DOGFOOD FAILED %s: %s",
				oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "DOGFOOD FAILED records=%d shown=%d receipts=%s\n",
			list.Total(), list.Shown(), oneline.Field(receiptsDir))
		return read, tool.Exit(1)
	}
	read.receipts = receipts

	authors := dogfood.Authors{}
	if repo := c.Str("repo"); repo != "" {
		gitTimeout := c.Int("git-timeout")
		if gitTimeout <= 0 {
			return read, tool.Refuse("--git-timeout must be positive; a read with no time budget will hang forever")
		}
		fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git over %d verbs; this can take seconds\n", len(verbs))
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(gitTimeout)*time.Second)
		defer cancel()
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git: %d/%d verbs\n", done, total)
		})
		fromGit, err := dogfood.AuthorsFromGit(ctx, repo, verbs, seams.gitRunner, progress)
		if err != nil {
			return read, tool.Refuse(oneline.Err(err))
		}
		for key, name := range fromGit {
			authors.Set(key, name)
		}
	}
	if authorsFile := c.Str("authors"); authorsFile != "" {
		// The mapping file is exact and git is evidence, so the file wins
		// wherever both have an opinion.
		fromFile, err := dogfood.ParseAuthors(authorsFile)
		if err != nil {
			return read, tool.Refuse(oneline.Err(err))
		}
		for key, name := range fromFile {
			authors.Set(key, name)
		}
	}
	read.authors = authors
	return read, nil
}

// reportStranded names every receipt that matched no verb: the file, what it
// claimed, and the verb it was probably meant to be. Both `ledger` and `gate`
// call it, on every outcome: a lane must not pass or fail without learning
// that evidence it read was thrown away, and which, and how to spell it.
func reportStranded(read dogfoodRead, maxFlag int, stderr io.Writer) {
	strands := dogfood.Stranded(read.verbs, read.receipts)
	if len(strands) == 0 {
		return
	}
	list := bounded.Capped(stderr, maxFlag, "DOGFOOD", "receipt", tool.MaxRemedy)
	for _, s := range strands {
		list.Line(oneline.Escape(oneline.Cap(s.Line(), oneline.TailBytes)))
	}
	list.More()
	fmt.Fprintf(stderr, "DOGFOOD NOTE stranded=%d shown=%d: these receipts name no verb the list declares, so they count for nothing; fix the spelling, or the documentation they were checked against\n",
		list.Total(), list.Shown())
}

func addDogfoodReadFlags(f *tool.Flags) {
	f.Required("receipts", receiptsHint)
	f.String("authors", "", "file mapping `<tool> <verb> = <who wrote it>`, one per line")
	f.String("repo", "", "repository to read authorship from when there is no --authors file")
	f.Int("git-timeout", gitTimeoutDefault, "seconds one --repo authorship read may take before it is killed and named")
}

func dogfoodLedger(c *tool.Call, seams dogfoodSeams) *tool.Out {
	aliasNote(c)
	read, out := dogfoodGather(seams, c)
	if out != nil {
		return out
	}
	rows, summary := dogfood.Ledger(read.verbs, read.receipts, read.authors)
	// Every row prints. A ledger that elided verbs under a ceiling would be a
	// ledger that lies by omission about exactly the verbs nobody has run; the
	// summary line is the bounded read of the same thing.
	for _, row := range rows {
		fmt.Fprintln(c.Stdout, oneline.Escape(row.Line()))
	}
	fmt.Fprintln(c.Stdout, oneline.Escape(summary.Line()))
	reportStranded(read, c.Int("max"), c.Stderr)
	return tool.Exit(0)
}

// cmdDogfoodGate declares the flags only the gate takes; the ones it shares with
// ledger and record come from the helpers above. The name is the one the
// command-reference test reads: internal/docs' TestTheCLIReferenceNamesEveryDogfoodGateFlag
// cuts this body out of the source to hold docs/CLI.md's line for the verb to them.
func cmdDogfoodGate(fs *tool.Flags) {
	fs.Bool("require-all", false, "every verb in the list must have been run by a non-author, not only the ones with receipts")
	fs.Bool("allow-empty", false, "pass on an empty receipt set; without it, no receipts is a refusal and not a green line")
	fs.String("shipped", "", "a checkout's cmd/ directory: the gate judges only the tools under it, the set a release ships")
}

func dogfoodGate(c *tool.Call, seams dogfoodSeams) *tool.Out {
	aliasNote(c)
	read, out := dogfoodGather(seams, c)
	if out != nil {
		return out
	}
	maxFlag, stderr, requireAll := c.Int("max"), c.Stderr, c.Bool("require-all")
	// The gate judges what ships. With --shipped, a receipt about a tool that
	// is not under that cmd/ is set aside and COUNTED on its own line: it is
	// true about a tool the release does not contain, and says nothing about
	// the ones it does. This is the read `nova-update release cut` does.
	if shippedDir := c.Str("shipped"); shippedDir != "" {
		set, err := dogfood.ReadShipped(shippedDir)
		if err != nil {
			return tool.Refuse("--shipped names a cmd/ directory of nova-* programs: " + oneline.Err(err))
		}
		var outside []dogfood.Receipt
		read.verbs, read.receipts, outside = set.Scope(read.verbs, read.receipts)
		fmt.Fprintf(stderr, "DOGFOOD NOTE shipped=%d outside=%d cmd=%s: receipts naming a tool outside the shipped set are set aside\n",
			len(set.Tools()), len(outside), oneline.Field(shippedDir))
	}
	// No receipts read is an empty evidence set, not a pass: the gate's whole
	// question is whether the verbs in the list have been dogfooded, and with
	// nothing read there is nothing to answer it. The release lane asks for
	// this refusal by name so it cannot go green on nothing. It is a verdict
	// that the gate ran and said no, so it carries no door.
	if len(read.receipts) == 0 && !c.Bool("allow-empty") {
		return tool.Fail(fmt.Sprintf("no receipts were read from %s, so the gate has nothing to pass on; add receipts, or pass --allow-empty to say that is deliberate", oneline.Escape(c.Str("receipts"))))
	}
	// The discarded receipts are said FIRST, and on every outcome.
	reportStranded(read, maxFlag, stderr)
	findings, summary := dogfood.Gate(read.verbs, read.receipts, read.authors, requireAll)
	if len(findings) == 0 {
		fmt.Fprintln(c.Stdout, oneline.Escape(summary.GateLine(requireAll)))
		return tool.Exit(0)
	}
	// The token is one word: bounded escapes what it is given, and a token with
	// a blank in it came back as `DOGFOOD\x20GATE MORE` the first time this verb
	// was run against this repository's own reference.
	list := bounded.Capped(stderr, maxFlag, "DOGFOOD", "verb", tool.MaxRemedy)
	for _, f := range findings {
		list.Line(oneline.Escape(oneline.Cap(f.Line(), oneline.TailBytes)))
	}
	list.More()
	fmt.Fprintln(stderr, oneline.Escape(summary.GateCountLine(list.Total(), list.Shown())))
	return tool.Exit(1)
}

func dogfoodRecord(c *tool.Call, seams dogfoodSeams) *tool.Out {
	aliasNote(c)
	dryRun := c.DryRun()
	toolName, verb := c.Str("tool"), c.Str("verb")
	// The spelling is checked against the same list the ledger will read it
	// against, so a receipt for a verb spelled differently is refused now
	// rather than stranded, unread, later.
	verbs, out := sourcesOf(c).verbList(seams, c.Int("max"), c.Stderr)
	if out != nil {
		return out
	}
	if !declares(verbs, toolName, verb) {
		remedy := "no verb of that spelling is declared, and nothing is close enough to suggest"
		if nearest := dogfood.Nearest(verbs, toolName, verb); nearest != "" {
			remedy = "did you mean: " + nearest
		}
		return tool.Refuse(fmt.Sprintf("%s %s is not a verb the list declares; %s",
			oneline.Field(toolName), oneline.Field(verb), oneline.Escape(remedy)))
	}
	// A --closes that answers a finding nobody can point at is a close nobody can
	// check. The id is the eight characters the gate prints beside the edge and
	// the same eight that end the receipt's filename, so it is checked for shape
	// here and matched against the real findings by the ledger: an id that names
	// nothing closes nothing, and says so by leaving the edge open.
	if id := strings.TrimSpace(c.Str("closes")); id != "" && !dogfood.IsReceiptID(id) {
		o := tool.Refuse(fmt.Sprintf("--closes is a receipt id, the eight hex characters the gate prints as receipt=<id>, got %s", oneline.Field(id)))
		o.Remedy = "nova-check dogfood record -h"
		return o
	}
	receipt := dogfood.Receipt{
		Tool:   toolName,
		Verb:   verb,
		By:     c.Str("by"),
		At:     seams.now().UTC().Format(time.RFC3339),
		OK:     c.Bool("ok"),
		Notes:  c.Str("notes"),
		Issue:  c.Int("issue"),
		Closes: strings.TrimSpace(c.Str("closes")),
	}
	// A dry run is this record's own plan: every check the write makes, the
	// same refusal, the path it would take, and nothing written.
	write := dogfood.Record
	if dryRun {
		write = dogfood.PlanRecord
	}
	path, err := write(c.Str("receipts"), receipt)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	if dryRun {
		fmt.Fprintln(c.Stdout, oneline.Escape(receipt.RecordLine(path)+" dry_run=true"))
		return tool.Exit(0)
	}
	fmt.Fprintln(c.Stdout, oneline.Escape(receipt.RecordLine(path)))
	return tool.Exit(0)
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
