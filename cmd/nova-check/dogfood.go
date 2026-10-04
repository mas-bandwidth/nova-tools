package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The dogfood verb answers the one question a tool's own tests cannot: has
// somebody who did not write it run it, on real work, with the edges filed?
//
// Three sub-verbs, and they are deliberately small: `ledger` reads the verb
// list against a directory of receipts and prints one row per verb; `record`
// appends one receipt; `gate` is the same read with an exit code, for the
// release lane to call. There is no state anywhere else — the receipts ARE the
// record, and every path comes from a flag.

const (
	cliHint      = `--cli <file> is the command reference the verbs are read from, usually docs/CLI.md; it is the list this ledger is about, so it is never guessed from the working directory`
	toolsHint    = `--tools <dir> is a directory of built nova-* binaries, each asked for its own help: the authoritative verb list, with --cli as the fallback for the tools it does not hold`
	receiptsHint = `--receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench`
	toolHint     = `--tool <t> is the binary you ran, spelled as the verb list spells it (nova-check)`
	verbHint     = `--verb <v> is the verb you ran, spelled as the verb list spells it (links, or "lift quarantine", or - for a tool that takes no verb)`
	byHint       = `--by <name> is who ran it; the ledger's whole question is whether that is somebody other than the author, so a receipt with no name is not a receipt`
	notesHint    = `--notes <text> is the real work you ran it on, in one line: what you were doing, what the verb did about it, and "Edges:" before anything you found`
	cliShapeHint = "a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), " +
		"or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`"
)

const (
	gitTimeoutDefault   = 60
	toolsTimeoutDefault = 60
)

const sourceRemedy = "name a verb list: --cli <docs/CLI.md>, or --tools <dir of built nova-* binaries>, or both; refusing to guess"

var (
	dogfoodClock      = time.Now
	dogfoodGitRunner  dogfood.Runner
	dogfoodHelpRunner dogfood.HelpRunner
)

func addDogfoodSourceFlags(f *tool.Flags) {
	f.String("cli", "", "the command reference the verbs are read from, usually docs/CLI.md")
	f.String("tools", "", "directory of built nova-* binaries, each asked for its own verbs (authoritative)")
	f.Int("tools-timeout", toolsTimeoutDefault, "seconds the whole --tools read may take before it is killed and named")
}

func addDogfoodReadFlags(f *tool.Flags) {
	addDogfoodSourceFlags(f)
	f.Required("receipts", receiptsHint)
	f.String("authors", "", "file mapping `<tool> <verb> = <who wrote it>`, one per line")
	f.String("repo", "", "repository to read authorship from when there is no --authors file")
	f.Int("git-timeout", gitTimeoutDefault, "seconds one --repo authorship read may take before it is killed and named")
	f.Max()
}

func dogfoodReadFlags(f *tool.Flags) {
	f.Prints()
	addDogfoodReadFlags(f)
}

func dogfoodGateFlags(f *tool.Flags) {
	f.Prints()
	addDogfoodReadFlags(f)
	f.Bool("require-all", false, "every verb in the list must have been run by a non-author, not only the ones with receipts")
	f.Bool("allow-empty", false, "pass on an empty receipt set; without it, no receipts is a refusal and not a green line")
	f.String("shipped", "", "a checkout's cmd/ directory: the gate judges only the tools under it, the set a release ships")
}

func dogfoodRecordFlags(f *tool.Flags) {
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
	f.Bool("dry-run", false, "make every check and print the receipt; write nothing")
	f.Max()
}

// dogfoodSources is where the verb list comes from: the binaries, the
// reference, or both.
type dogfoodSources struct {
	cli     string
	tools   string
	timeout int
}

func dogfoodSourcesOf(c *tool.Call) *dogfoodSources {
	return &dogfoodSources{cli: c.Str("cli"), tools: c.Str("tools"), timeout: c.Int("tools-timeout")}
}

// verbList reads the verb list from whichever sources were named. The binaries
// win and the reference fills in the tools they do not cover.
func (s *dogfoodSources) verbList(verb string, maxFlag int, stderr io.Writer) ([]dogfood.Verb, *tool.Out) {
	if s.cli == "" && s.tools == "" {
		return nil, tool.Refuse(sourceRemedy, cliHint, toolsHint)
	}
	var fromTools []dogfood.Verb
	if s.tools != "" {
		if s.timeout <= 0 {
			return nil, tool.Refuse("--tools-timeout must be positive; a read with no time budget will hang forever")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.timeout)*time.Second)
		defer cancel()
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE asking the binaries for their verbs: %d/%d\n", done, total)
		})
		verbs, failures, err := dogfood.VerbsFromTools(ctx, s.tools, dogfoodHelpRunner, progress)
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
			return nil, tool.Refuse(oneline.Err(err), cliShapeHint)
		}
		fromCLI = verbs
	}
	merged := dogfood.MergeVerbs(fromTools, fromCLI)
	if len(merged) == 0 {
		return nil, tool.Refuse("the sources named declare no verbs at all; a ledger over no verbs would say OK about nothing", cliShapeHint)
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

func dogfoodGather(verb string, src *dogfoodSources, receiptsDir, authorsFile, repo string, gitTimeout, maxFlag int, stderr io.Writer) (dogfoodRead, *tool.Out) {
	var read dogfoodRead
	verbs, o := src.verbList(verb, maxFlag, stderr)
	if o != nil {
		return read, o
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
	if repo != "" {
		if gitTimeout <= 0 {
			return read, tool.Refuse("--git-timeout must be positive; a read with no time budget will hang forever")
		}
		fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git over %d verbs; this can take seconds\n", len(verbs))
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(gitTimeout)*time.Second)
		defer cancel()
		progress := dogfood.NewProgress(nil, 100*time.Millisecond, 2*time.Second, func(done, total int) {
			fmt.Fprintf(stderr, "DOGFOOD NOTE reading authorship from git: %d/%d verbs\n", done, total)
		})
		fromGit, err := dogfood.AuthorsFromGit(ctx, repo, verbs, dogfoodGitRunner, progress)
		if err != nil {
			return read, tool.Refuse(oneline.Err(err))
		}
		for key, name := range fromGit {
			authors.Set(key, name)
		}
	}
	if authorsFile != "" {
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

func dogfoodLedger(c *tool.Call) *tool.Out {
	src := dogfoodSourcesOf(c)
	receipts := c.Str("receipts")
	authors := c.Str("authors")
	repo := c.Str("repo")
	gitTimeout := c.Int("git-timeout")
	maxFlag := c.Int("max")
	read, o := dogfoodGather("ledger", src, receipts, authors, repo, gitTimeout, maxFlag, c.Stderr)
	if o != nil {
		return o
	}
	rows, summary := dogfood.Ledger(read.verbs, read.receipts, read.authors)
	for _, row := range rows {
		fmt.Fprintln(c.Stdout, oneline.Escape(row.Line()))
	}
	fmt.Fprintln(c.Stdout, oneline.Escape(summary.Line()))
	reportStranded(read, maxFlag, c.Stderr)
	return tool.Exit(0)
}

func dogfoodGate(c *tool.Call) *tool.Out {
	src := dogfoodSourcesOf(c)
	receipts := c.Str("receipts")
	authors := c.Str("authors")
	repo := c.Str("repo")
	gitTimeout := c.Int("git-timeout")
	requireAll := c.Bool("require-all")
	allowEmpty := c.Bool("allow-empty")
	shippedDir := c.Str("shipped")
	maxFlag := c.Int("max")
	read, o := dogfoodGather("gate", src, receipts, authors, repo, gitTimeout, maxFlag, c.Stderr)
	if o != nil {
		return o
	}
	if shippedDir != "" {
		set, err := dogfood.ReadShipped(shippedDir)
		if err != nil {
			return tool.Refuse("--shipped names a cmd/ directory of nova-* programs: " + oneline.Err(err))
		}
		var outside []dogfood.Receipt
		read.verbs, read.receipts, outside = set.Scope(read.verbs, read.receipts)
		fmt.Fprintf(c.Stderr, "DOGFOOD NOTE shipped=%d outside=%d cmd=%s: receipts naming a tool outside the shipped set are set aside\n",
			len(set.Tools()), len(outside), oneline.Field(shippedDir))
	}
	if len(read.receipts) == 0 && !allowEmpty {
		return tool.Fail(fmt.Sprintf("no receipts were read from %s, so the gate has nothing to pass on; add receipts, or pass --allow-empty to say that is deliberate", oneline.Escape(receipts)))
	}
	reportStranded(read, maxFlag, c.Stderr)
	findings, summary := dogfood.Gate(read.verbs, read.receipts, read.authors, requireAll)
	if len(findings) == 0 {
		fmt.Fprintln(c.Stdout, oneline.Escape(summary.GateLine(requireAll)))
		return tool.Exit(0)
	}
	list := bounded.Capped(c.Stderr, maxFlag, "DOGFOOD", "verb", tool.MaxRemedy)
	for _, f := range findings {
		list.Line(oneline.Escape(oneline.Cap(f.Line(), oneline.TailBytes)))
	}
	list.More()
	fmt.Fprintln(c.Stderr, oneline.Escape(summary.GateCountLine(list.Total(), list.Shown())))
	return tool.Exit(1)
}

func dogfoodRecord(c *tool.Call) *tool.Out {
	src := dogfoodSourcesOf(c)
	toolName := c.Str("tool")
	verb := c.Str("verb")
	by := c.Str("by")
	notes := c.Str("notes")
	receipts := c.Str("receipts")
	ok := c.Bool("ok")
	notOK := c.Bool("not-ok")
	issue := c.Int("issue")
	closes := c.Str("closes")
	dryRun := c.Bool("dry-run")
	maxFlag := c.Int("max")

	if ok == notOK {
		return tool.Refuse("state the verdict exactly once: --ok when the verb did what the run needed, --not-ok when it did not; refusing to guess")
	}
	if issue < 0 {
		return tool.Refuse(fmt.Sprintf("--issue must be an issue number, got %d; leave it out when no edge was filed", issue))
	}
	verbs, o := src.verbList("record", maxFlag, c.Stderr)
	if o != nil {
		return o
	}
	if !declares(verbs, toolName, verb) {
		remedy := "no verb of that spelling is declared, and nothing is close enough to suggest"
		if nearest := dogfood.Nearest(verbs, toolName, verb); nearest != "" {
			remedy = "did you mean: " + nearest
		}
		return tool.Refuse(fmt.Sprintf("%s %s is not a verb the list declares; %s",
			oneline.Field(toolName), oneline.Field(verb), oneline.Escape(remedy)))
	}
	if id := strings.TrimSpace(closes); id != "" && !dogfood.IsReceiptID(id) {
		return tool.Refuse(fmt.Sprintf("--closes is a receipt id, the eight hex characters the gate prints as receipt=<id>, got %s", oneline.Field(id)))
	}
	receipt := dogfood.Receipt{
		Tool:   toolName,
		Verb:   verb,
		By:     by,
		At:     dogfoodClock().UTC().Format(time.RFC3339),
		OK:     ok,
		Notes:  notes,
		Issue:  issue,
		Closes: strings.TrimSpace(closes),
	}
	write := dogfood.Record
	if dryRun {
		write = dogfood.PlanRecord
	}
	path, err := write(receipts, receipt)
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
func declares(verbs []dogfood.Verb, toolName, verb string) bool {
	want := dogfood.NormalizeKey(toolName, verb)
	for _, v := range verbs {
		if v.Key() == want {
			return true
		}
	}
	return false
}
