// roadmap writes ROADMAP.md from docs/roadmap.sexp (and, with --kind fixes,
// FIXES.md from docs/fixes.sexp), so neither page is ever written by hand: the
// sexp is the data, pkg/roadmap decodes it through pkg/sexp
// (nothing is evaluated) and renders the page, and
// internal/docs's TestRoadmapIsGeneratedFromTheSexp and
// TestFixesIsGeneratedFromTheSexp hold the committed pages to what this writes.
// It is retiring: cmd/nova-roadmap (render, check) is the generator now, over the
// same roadmap.Page, and this command stays only until nova-sprint's make roadmap
// stops running it (`go tool roadmap`); then it is deleted by name.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/roadmap"
)

const usage = `roadmap renders ROADMAP.md from docs/roadmap.sexp; never edit the page by hand.

usage:
  roadmap [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--check]
  roadmap --kind fixes [--file docs/fixes.sexp] [--out FIXES.md] [--check]

The data is read as data (pkg/sexp: no evaluation, bounded) and the page is the
same bytes for the same data. --check writes nothing: it exits 0 when the page
on disk is what the data renders, 1 on drift (run: make roadmap), 2 when the
data is refused, every problem named.

The roadmap file takes one of two forms, told apart by its first token:

  Feature tables, the standard for every ROADMAP.md (owner, 2026-10-17):
  one ## section per area, a line saying what the area is, then a table with
  one row per feature a user would recognise and ✅/❌ cells, a complete row
  and a link to the data. Most areas have one "done" column; an area may
  instead declare an axis (languages, platforms, drivers, modes) as columns.

    (:schema 1
     :root "goal"
     :page (:title "netcode.go roadmap" :roadmaps ("protocol")
            :closing ((:title "..." :text "...")))        ; optional
     :nodes
     ((:id "goal" :type :work-set :children ("protocol"))
      (:id "protocol" :type :roadmap :title "Protocol"
       :text "The netcode 1.02 wire protocol, client and server."
       :rows (("connect" "Connect tokens") ("ipv6" "IPv6" :later t))
       :columns (("done" "done"))                         ; or (("go" "Go") ("cs" "C#") ...)
       :cells (("connect" "done" "t-connect") ("ipv6" "done" "t-ipv6")))
      (:id "t-connect" :type :task :title "Connect tokens" :state :done
       :evidence ("token_test.go: TestConnectToken" "commit 1a2b3c4"))
      (:id "t-ipv6" :type :task :title "IPv6" :state :todo)))

  A cell names a task, or a :work-set of tasks; it is ✅ only when every task
  under it is :done, and a :done task must carry :evidence (a test or a commit).
  States are :unknown, :todo, :doing and :done. A row with :later t is left for
  later and shows "(later)". Every row needs a cell in every column. Nodes may
  carry other keys (notes, card metadata); they are kept, not rendered.
  Evidence overlay: --evidence DIR (default: roadmap-evidence/ beside --file),
  when it exists, holds *.sexp files of (:id "<task>" :state :done :evidence
  (...)) forms, applied in file-name order and held to the same rule.

  Card list (the older form, until each repository converts):
    (roadmap "v1" :title "..." :text "..." :groups (...) :items (...))
  See pkg/roadmap's package documentation for its shape.

flags:
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// errRefused carries a refusal's one line out of run.
type errRefused string

// run is the command: 0 written or up to date, 1 drift under --check, 2 refused.
func run(args []string, stdout, stderr io.Writer) (code int) {
	defer func() {
		r := recover()
		if why, ok := r.(errRefused); ok {
			fmt.Fprintf(stderr, "roadmap REFUSED: %s; run: make roadmap\n", string(why))
			code = 2
		} else if r != nil {
			panic(r)
		}
	}()
	refuse := func(why string) { panic(errRefused(why)) }
	fl := flag.NewFlagSet("roadmap", flag.ContinueOnError)
	fl.SetOutput(stderr)
	kind := fl.String("kind", "roadmap", "what the file holds: roadmap (docs/roadmap.sexp) or fixes (docs/fixes.sexp)")
	file := fl.String("file", "", "the data to read (default docs/roadmap.sexp, or docs/fixes.sexp with --kind fixes)")
	out := fl.String("out", "", "the page to write or check (default ROADMAP.md, or FIXES.md with --kind fixes)")
	evidence := fl.String("evidence", "", "feature tables only: the evidence overlay directory (default roadmap-evidence/ beside --file)")
	check := fl.Bool("check", false, "write nothing; exit 1 when the page on disk differs from what the data renders")
	fl.Usage = func() {
		fmt.Fprint(fl.Output(), usage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fl.NArg() > 0 {
		refuse("takes no positional words: " + strings.Join(fl.Args(), " "))
	}
	if *file == "" {
		*file = map[bool]string{true: "docs/fixes.sexp", false: "docs/roadmap.sexp"}[*kind == "fixes"]
	}
	if *out == "" {
		*out = map[bool]string{true: "FIXES.md", false: "ROADMAP.md"}[*kind == "fixes"]
	}
	page, items, err := roadmap.Page(*kind, *file, *evidence)
	if err != nil {
		refuse(oneLine(err.Error()))
	}
	if *check {
		have, err := os.ReadFile(*out)
		if err != nil || string(have) != page {
			fmt.Fprintf(stderr, "roadmap DRIFT: %s is not what %s renders; run: make roadmap\n", *out, *file)
			return 1
		}
		fmt.Fprintf(stdout, "%s OK items=%d file=%s up to date\n", *kind, items, *out)
		return 0
	}
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		refuse(fmt.Sprintf("writing %s: %v", *out, err))
	}
	fmt.Fprintf(stdout, "%s OK items=%d file=%s\n", *kind, items, *out)
	return 0
}

// oneLine joins a multi-problem error onto one line.
func oneLine(s string) string { return strings.ReplaceAll(s, "\n", "; ") }
