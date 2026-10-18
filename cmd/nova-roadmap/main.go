// nova-roadmap renders a repository's ROADMAP.md from its docs/roadmap.sexp and
// checks the committed page against the data: the one roadmap generator, used by
// every repository's `make roadmap` and `make roadmap-check` from a pinned
// release, so a repository in any language needs only this binary. The data is
// read through pkg/sexp (never evaluated) and pkg/roadmap renders it; the same
// data makes the same bytes. It depends on nothing of the sprint.
//
//	example:
//	  nova-roadmap example --out ./docs/roadmap.sexp
//	  nova-roadmap render --file ./docs/roadmap.sexp --out ./ROADMAP.md
//	  nova-roadmap check --file ./docs/roadmap.sexp --out ./ROADMAP.md
package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var version string

func main() { os.Exit(novaRoadmap().Main()) }

const exitCodes = "0 done (render) or up to date (check), 1 drift (check: the page is not what the data renders), 2 could not run (the data refused, every problem named, or a bad invocation)"

func novaRoadmap() *tool.Tool {
	return &tool.Tool{
		Name:  "nova-roadmap",
		What:  "renders ROADMAP.md from docs/roadmap.sexp and checks the committed page against the data",
		Stamp: version,
		How: "the data is read as data (no evaluation, bounded); the same data makes the same bytes.\n" +
			"render writes the page; check writes nothing and exits 1 when the page on disk differs.\n" +
			"A repository runs both from its Makefile (make roadmap, make roadmap-check) and CI runs the check.\n" +
			"The standard form is feature tables: `nova-roadmap help form` prints it with a worked example.",
		ExitTable: exitCodes,
		Topics:    []tool.Topic{{Name: "form", Text: formTopic}},
		Verbs:     []tool.Verb{exampleVerb(), renderVerb(), checkVerb()},
	}
}

func pageFlags(f *tool.Flags) {
	f.String("file", "", "the data to read (default docs/roadmap.sexp, or docs/fixes.sexp with --kind fixes)")
	f.String("out", "", "the page (default ROADMAP.md, or FIXES.md with --kind fixes)")
	f.String("kind", "roadmap", "what the data holds: roadmap (feature tables or the card list) or fixes (FIXES.md)")
	f.String("evidence", "", "feature tables only: the evidence overlay directory (default roadmap-evidence/ beside --file, when it exists)")
	f.Check(func(c *tool.Call) {
		if k := c.Str("kind"); k != "roadmap" && k != "fixes" {
			c.Problem("--kind " + k + " is not roadmap or fixes")
		}
	})
}

// paths returns the data and page paths, the defaults filled in by --kind.
func paths(c *tool.Call) (kind, file, out string) {
	kind, file, out = c.Str("kind"), c.Str("file"), c.Str("out")
	if file == "" {
		file = map[bool]string{true: "docs/fixes.sexp", false: "docs/roadmap.sexp"}[kind == "fixes"]
	}
	if out == "" {
		out = map[bool]string{true: "FIXES.md", false: "ROADMAP.md"}[kind == "fixes"]
	}
	return kind, file, out
}

func exampleVerb() tool.Verb {
	return tool.Verb{
		Name:    "example",
		Usage:   "example --out <docs/roadmap.sexp>",
		Example: "example --out ./docs/roadmap.sexp",
		Effect:  tool.LocalWrite + ": writes a starter data file (--out), never over one that is there",
		DryRun:  true,
		Detail:  "Writes a starter docs/roadmap.sexp in the feature-table form (nova-roadmap help form), making its directory.",
		Flags:   func(f *tool.Flags) { f.Required("out", "the data file to create, usually ./docs/roadmap.sexp") },
		Run: func(c *tool.Call) *tool.Out {
			out := c.Want("out", "the data file to create, usually ./docs/roadmap.sexp")
			dry := c.DryRun()
			if r := c.Refused(); r != nil {
				return r
			}
			if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
				return tool.Refuse(out + " is there already; example never writes over data")
			}
			if !dry {
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					return tool.Refuse(err.Error())
				}
				if err := os.WriteFile(out, []byte(exampleData), 0o644); err != nil {
					return tool.Refuse(err.Error())
				}
			}
			return tool.Done().Fact("file", out).Note("next: nova-roadmap render --file " + out)
		},
	}
}

func renderVerb() tool.Verb {
	return tool.Verb{
		Name:    "render",
		Usage:   "render [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--kind roadmap|fixes]",
		Example: "render --file ./docs/roadmap.sexp --out ./ROADMAP.md",
		Effect:  tool.LocalWrite + ": writes the page (--out)",
		DryRun:  true,
		Detail:  "Renders the data into the page and writes it. --dry-run renders and writes nothing.",
		Flags:   pageFlags,
		Run: func(c *tool.Call) *tool.Out {
			kind, file, out := paths(c)
			dry := c.DryRun()
			page, items, err := roadmap.Page(kind, file, c.Str("evidence"))
			if err != nil {
				return tool.Refuse(err.Error())
			}
			if !dry {
				if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
					return tool.Refuse("writing " + out + ": " + err.Error())
				}
			}
			return tool.Done().Fact("kind", kind).Fact("items", items).Fact("file", out)
		},
	}
}

func checkVerb() tool.Verb {
	return tool.Verb{
		Name:    "check",
		Usage:   "check [--file docs/roadmap.sexp] [--out ROADMAP.md] [--evidence DIR] [--kind roadmap|fixes]",
		Example: "check --file ./docs/roadmap.sexp --out ./ROADMAP.md",
		Effect:  tool.Inspection,
		Detail: "The drift gate: renders the data and compares it with the page on disk, byte for byte.\n" +
			"A missing page is drift. The remedy for exit 1 is nova-roadmap render, then commit the page.",
		Flags: pageFlags,
		Run: func(c *tool.Call) *tool.Out {
			kind, file, out := paths(c)
			page, items, err := roadmap.Page(kind, file, c.Str("evidence"))
			if err != nil {
				return tool.Refuse(err.Error())
			}
			have, err := os.ReadFile(out)
			if err != nil || string(have) != page {
				return tool.Fail(out + " is not what " + file + " renders; run: nova-roadmap render, then commit " + out)
			}
			return tool.Done().Fact("kind", kind).Fact("items", items).Fact("file", out)
		},
	}
}

var formTopic = `The feature-table form, the standard for every ROADMAP.md: one ## section per
area, a line saying what the area is, then a table with one row per feature a
user would recognise, ✅/❌ cells, a complete row and a link to the data. Most
areas have one "done" column; an area may instead declare an axis (languages,
platforms, drivers, modes) as its columns.

` + indent(exampleData) + `

A cell names a task, or a :work-set of tasks; it is ✅ only when every task under
it is :done, and a :done task must carry :evidence (a test or a commit). States
are :unknown, :todo, :doing and :done. A row with :later t is left for later and
shows "(later)". Every row needs a cell in every column. :page may also carry
:closing ((:title "..." :text "...") ...), sections after the tables. Nodes may carry other
keys (notes, card metadata); they are kept, not rendered.

Evidence overlay: --evidence DIR (default roadmap-evidence/ beside --file), when
it exists, holds *.sexp files of (:id "<task>" :state :done :evidence (...))
forms, applied in file-name order, each file whole or not at all.

The card list, (roadmap "v1" :title ... :groups (...) :items (...)), and
docs/fixes.sexp (--kind fixes) render as before; pkg/roadmap documents both.`

// exampleData is a starter docs/roadmap.sexp: one area with one done column, a
// done feature with its evidence and a later one. `example` writes it and the
// form topic prints it, so the help and the starter cannot drift.
const exampleData = `; docs/roadmap.sexp: the data ROADMAP.md is generated from (nova-roadmap render).
(:schema 1
 :root "goal"
 :page (:title "Example roadmap" :roadmaps ("protocol"))
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
`

// indent prefixes every line of s with two blanks, for the form topic.
func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
