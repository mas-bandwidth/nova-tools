// nova-roadmap writes the feature tables in a repository's ROADMAP.md from its
// docs/roadmap.sexp (and the evidence overlay docs/roadmap-evidence/), and checks
// the committed tables against the data. Only the bytes between each area's
// nova-work markers are generated; everything else on the page is written by
// hand. The data is read as data, never evaluated, and the same data makes the
// same bytes. `nova-roadmap help` is the manual.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// version is set at build time (-ldflags "-X main.version=v1.2.3"); otherwise
// the module version go install recorded, or "dev".
var version string

func main() {
	os.Exit(run("", os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes.
const (
	exitOK      = 0
	exitDrift   = 1
	exitRefused = 2
)

// app is one invocation: dir is the directory relative paths are read against
// ("" is the working directory), so tests run in a directory of their own.
type app struct {
	dir         string
	stdout, err io.Writer
}

func (a *app) path(p string) string {
	if a.dir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.dir, p)
}

func (a *app) refuse(format string, args ...any) int {
	fmt.Fprintf(a.err, "nova-roadmap: REFUSED: %s\nrun: nova-roadmap help\n", fmt.Sprintf(format, args...))
	return exitRefused
}

func run(dir string, args []string, stdout, stderr io.Writer) int {
	a := &app{dir: dir, stdout: stdout, err: stderr}
	if len(args) == 0 {
		fmt.Fprint(stdout, banner)
		return exitOK
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "-help", "--help":
		return a.help(rest)
	case "version", "--version":
		fmt.Fprintln(stdout, "nova-roadmap", versionString())
		return exitOK
	case "example":
		return a.example(rest)
	case "render":
		return a.render(rest, false)
	case "check":
		return a.render(rest, true)
	}
	return a.refuse("%q is no verb; the verbs are example, render, check, version and help", verb)
}

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func (a *app) help(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stdout, banner)
		return exitOK
	}
	if len(args) > 1 {
		return a.refuse("help takes one topic: a verb, or form")
	}
	if args[0] == "form" {
		fmt.Fprint(a.stdout, formTopic)
		return exitOK
	}
	text, ok := verbHelp[args[0]]
	if !ok {
		return a.refuse("help %s: no such topic; the topics are example, render, check and form", args[0])
	}
	fmt.Fprint(a.stdout, text)
	return exitOK
}

// flags parses a verb's flags; a positional argument is refused.
func (a *app) flags(verb string, fset *flag.FlagSet, args []string) (int, bool) {
	fset.SetOutput(io.Discard)
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(a.stdout, verbHelp[verb])
			return exitOK, false
		}
		return a.refuse("%s: %v", verb, err), false
	}
	if fset.NArg() > 0 {
		return a.refuse("%s takes no arguments, only flags; got %q", verb, fset.Args()), false
	}
	return 0, true
}

func (a *app) example(args []string) int {
	fset := flag.NewFlagSet("example", flag.ContinueOnError)
	out := fset.String("out", "docs/roadmap.sexp", "")
	dry := fset.Bool("dry-run", false, "")
	if code, ok := a.flags("example", fset, args); !ok {
		return code
	}
	p := a.path(*out)
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		return a.refuse("%s is there already; example never writes over data", *out)
	}
	if *dry {
		fmt.Fprintf(a.stdout, "DRY RUN: would write %s (%d bytes)\n", *out, len(exampleData))
		return exitOK
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return a.refuse("%v", err)
	}
	if err := os.WriteFile(p, []byte(exampleData), 0o644); err != nil {
		return a.refuse("%v", err)
	}
	fmt.Fprintf(a.stdout, "OK: wrote %s\nnext: nova-roadmap render --file %s\n", *out, *out)
	return exitOK
}

// render is render (check false) and check (check true).
func (a *app) render(args []string, check bool) int {
	verb := map[bool]string{false: "render", true: "check"}[check]
	fset := flag.NewFlagSet(verb, flag.ContinueOnError)
	file := fset.String("file", "docs/roadmap.sexp", "")
	out := fset.String("out", "ROADMAP.md", "")
	evidence := fset.String("evidence", "", "")
	var dry *bool
	if !check {
		dry = fset.Bool("dry-run", false, "")
	}
	if code, ok := a.flags(verb, fset, args); !ok {
		return code
	}
	ev := *evidence
	if ev != "" {
		ev = a.path(ev)
	}
	rm, files, entries, err := Load(a.path(*file), ev)
	if err != nil {
		return a.refuse("%v", err)
	}
	// The [Source data] link is the data file as seen from the page, so the
	// page is the same wherever the tool is run from.
	source, err := filepath.Rel(filepath.Dir(absPath(a.path(*out))), absPath(a.path(*file)))
	if err != nil {
		return a.refuse("%v", err)
	}
	source = filepath.ToSlash(source)
	stats := fmt.Sprintf("%d areas, %d rows; %d evidence files, %d entries", len(rm.Areas), rm.Rows(), files, entries)

	have, err := os.ReadFile(a.path(*out))
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return a.refuse("reading %s: %v", *out, err)
	}
	if check {
		if missing {
			fmt.Fprintf(a.err, "DRIFT: %s is missing; run: nova-roadmap render, then commit %s\n", *out, *out)
			return exitDrift
		}
		page, err := rm.Splice(string(have), source)
		if err != nil {
			return a.refuse("%s: %v", *out, err)
		}
		if page != string(have) {
			fmt.Fprintf(a.err, "DRIFT: a table in %s differs from what %s renders (areas: %s); run: nova-roadmap render, then commit %s\n", *out, *file, strings.Join(rm.Changed(string(have), page), ", "), *out)
			return exitDrift
		}
		fmt.Fprintf(a.stdout, "OK: the tables in %s are up to date with %s (%s)\n", *out, *file, stats)
		return exitOK
	}

	var page, did string
	if missing {
		page, err = rm.Starter(source)
		did = "wrote a starter " + *out + "; everything outside the nova-work markers is yours to edit"
	} else {
		page, err = rm.Splice(string(have), source)
		did = "spliced the tables into " + *out
	}
	if err != nil {
		return a.refuse("%s: %v", *out, err)
	}
	if *dry {
		fmt.Fprintf(a.stdout, "DRY RUN: would have %s (%s); wrote nothing\n", did, stats)
		return exitOK
	}
	if !missing && page == string(have) {
		fmt.Fprintf(a.stdout, "OK: %s is up to date (%s)\n", *out, stats)
		return exitOK
	}
	if err := os.WriteFile(a.path(*out), []byte(page), 0o644); err != nil {
		return a.refuse("writing %s: %v", *out, err)
	}
	fmt.Fprintf(a.stdout, "OK: %s (%s)\n", did, stats)
	return exitOK
}

func absPath(p string) string {
	if q, err := filepath.Abs(p); err == nil {
		return q
	}
	return p
}

const banner = `nova-roadmap: writes the feature tables in ROADMAP.md from docs/roadmap.sexp, and checks them for drift

how it works: the data (docs/roadmap.sexp, plus the evidence overlay docs/roadmap-evidence/*.sexp)
is a graph of tasks, read as data and never evaluated. Each area of the roadmap is one table:
a row per feature, a ✅/❌ cell per column, a complete row and a link to the data. A cell is ✅
only when every task under it is :done with :evidence (a test or a commit).
In ROADMAP.md each table sits between two marker lines,
  <!-- nova-work:<area id>:start -->
  <!-- nova-work:<area id>:end -->
and only the bytes between them are generated. Everything else on the page (title, headings,
prose) is written by hand and kept exactly as it is. When ROADMAP.md does not exist, render
writes a starter page with a heading and a marker pair per area.

usage:
  nova-roadmap example [--out docs/roadmap.sexp] [--dry-run]
  nova-roadmap render [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--dry-run]
  nova-roadmap check [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR]
  nova-roadmap version
  nova-roadmap help [example | render | check | form]

The data's shape, with a worked example: nova-roadmap help form

exit codes: 0 done (render) or up to date (check); 1 drift (check: a table is not what the
data renders, or the page is missing); 2 refused (the data or the page is malformed, or a
bad invocation; the first problem found is named, with what to fix).

example (a first run, in a repository's root):
  nova-roadmap example
  nova-roadmap render
  nova-roadmap check
`

var verbHelp = map[string]string{
	"example": `usage: nova-roadmap example [--out docs/roadmap.sexp] [--dry-run]

Writes a starter data file (the worked example of nova-roadmap help form), making its
directory. It never writes over a file that is there.

flags:
  --out <path>  the data file to create (default docs/roadmap.sexp)
  --dry-run     say what would be written, and write nothing
`,
	"render": `usage: nova-roadmap render [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--dry-run]

Renders each area's table and writes it between the area's markers in the page. Every
byte outside the markers is kept. The page must hold exactly one marker pair per area of
:page :roadmaps, in that order; when the page does not exist, a starter page is written.

flags:
  --file <path>      the data (default docs/roadmap.sexp)
  --out <path>       the page (default ROADMAP.md)
  --evidence <dir>   the evidence overlay (default roadmap-evidence/ beside --file, when it exists)
  --dry-run          render, and write nothing
`,
	"check": `usage: nova-roadmap check [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR]

The drift gate, for CI: renders the tables and compares the page on disk with the result,
byte for byte. Writes nothing. Exit 1 when they differ or the page is missing; the remedy
is nova-roadmap render, then commit the page.

flags:
  --file <path>      the data (default docs/roadmap.sexp)
  --out <path>       the page (default ROADMAP.md)
  --evidence <dir>   the evidence overlay (default roadmap-evidence/ beside --file, when it exists)
`,
}

var formTopic = `The data, docs/roadmap.sexp: one (:key value ...) list. A worked example, two areas,
the first with one "done" column, the second with an axis (here, languages) as its columns:

` + exampleIndented + `

The nodes:
  :task      a piece of work, with :state :unknown, :todo, :doing or :done. A :done task
             must carry :evidence, a list naming the test or commit that proves it.
  :work-set  a group of nodes, :children ("<id>" ...): a cell that needs several tasks.
  :roadmap   an area: one table. :title is its heading on a starter page.
             :rows     (("<row id>" "<feature>") ...); a row may end with :later t, which
                       renders "(later)" after the feature.
             :columns  (("<column id>" "<column title>") ...): ("done" "done"), or one per
                       language, platform, driver or mode.
             :cells    (("<row id>" "<column id>" "<task or work-set id>") ...): one for
                       every row and column.
:root names the node every area is reached from (a work-set holding the areas).
:page (:roadmaps (...)) lists the areas in page order, and holds nothing else.

The rule: a cell is ✅ only when every task under it is :done with non-blank :evidence.
:unknown, :todo and :doing all render ❌; the difference is for people reading the data.
The complete row is the share of ✅ cells in each column, rounded down; a :later row counts.
Every node is checked, whether or not a cell reaches it (a :done task anywhere needs
:evidence), but only the areas in :page are rendered.
Nodes may carry other keys (notes, links, metadata); they are kept and not rendered.
Prose is never data: a :text on an area, or a :page key other than :roadmaps, is refused.
Titles may not hold | or a newline. Comments run from ; to the end of the line.
The reader refuses #, ', ` + "`" + `, , and \ outside strings: the file is data, never a program.

The evidence overlay, docs/roadmap-evidence/*.sexp (--evidence names another directory):
any number of entries, one after another with no list around them, applied in file-name
order (files starting with . are skipped). An entry sets the task's :state, and its
:evidence when given; other keys (:note) are kept and not rendered. One bad entry (an id
that is no task, a :done without :evidence) refuses the whole run, and nothing is written.

  ; docs/roadmap-evidence/protocol.sexp
  (:id "t-ipv6" :state :done :evidence ("ipv6_test.go: TestIPv6"))
  (:id "cs-connect" :state :done :evidence ("ClientTests.cs: Connect"))

The page, ROADMAP.md: everything is written by hand except what sits between each area's
marker lines, which render writes. For the example above:

  # Example roadmap

  What this project is, in your own words.

  ## Protocol

  <!-- nova-work:protocol:start -->

  | feature | done |
  |---|:---:|
  | Connect tokens | ✅ |
  | IPv6 (later) | ❌ |
  | complete | 50% |

  [Source data](docs/roadmap.sexp)

  <!-- nova-work:protocol:end -->

  ## Clients
  ...

To start: nova-roadmap example, then nova-roadmap render (which writes a starter page),
then edit the page's text by hand. After a data change: nova-roadmap render, and commit
both. In CI: nova-roadmap check.

A page you already have: render refuses it until it holds the marker pairs. Paste one
pair per area, each marker on a line of its own, in :page order, where each table goes;
render's refusal prints the lines to paste. Then nova-roadmap render fills them.
`

// exampleData is the starter docs/roadmap.sexp: `example` writes it and the form
// topic prints it, so the help and the starter cannot drift.
const exampleData = `; docs/roadmap.sexp: the data the tables in ROADMAP.md are generated from (nova-roadmap render).
(:schema 1
 :root "goal"
 :page (:roadmaps ("protocol" "clients"))
 :nodes
 ((:id "goal" :type :work-set :children ("protocol" "clients"))
  (:id "protocol" :type :roadmap :title "Protocol"
   :rows (("connect" "Connect tokens") ("ipv6" "IPv6" :later t))
   :columns (("done" "done"))
   :cells (("connect" "done" "t-connect") ("ipv6" "done" "t-ipv6")))
  (:id "t-connect" :type :task :title "Connect tokens" :state :done
   :evidence ("token_test.go: TestConnectToken" "commit 1a2b3c4"))
  (:id "t-ipv6" :type :task :title "IPv6" :state :todo)
  (:id "clients" :type :roadmap :title "Clients"
   :rows (("connect" "Connect") ("send" "Send and receive"))
   :columns (("go" "Go") ("cs" "C#"))
   :cells (("connect" "go" "go-connect") ("connect" "cs" "cs-connect")
           ("send" "go" "go-send") ("send" "cs" "cs-send")))
  (:id "go-connect" :type :task :title "Go client connects" :state :done :evidence ("client_test.go: TestConnect"))
  (:id "cs-connect" :type :task :title "C# client connects" :state :doing)
  (:id "go-send" :type :work-set :children ("go-send-reliable" "go-send-unreliable"))
  (:id "go-send-reliable" :type :task :title "Go reliable send" :state :done :evidence ("send_test.go: TestReliable"))
  (:id "go-send-unreliable" :type :task :title "Go unreliable send" :state :todo)
  (:id "cs-send" :type :task :title "C# send and receive" :state :unknown)))
`

var exampleIndented = "  " + strings.ReplaceAll(strings.TrimRight(exampleData, "\n"), "\n", "\n  ")
