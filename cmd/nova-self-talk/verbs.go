package main

import (
	"bytes"
	"embed"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// shapeCatalogue is what `shapes -h` prints above its flags: every shape the
// scan names, in the words a reader meets, so the help and the scan cannot
// disagree about what a sentence is.
const shapeCatalogue = `STANDING is a first-person claim with a word of failure: cannot check, bad at, worst, terrible at, cannot ever, fallible.
RANKING, an INSTALLATION, is a self-superlative: I am the best, or my weakest instrument.
FORECLOSURE, an INSTALLATION, is a door stated shut: I will never be a good planner, or I have no recall.
VERDICT-IDIOM, an INSTALLATION, is a verdict on a practice: dead as a practice.
TRAIT, an INSTALLATION, is a habit: I always overpromise, or I tend to rush.
A dated claim, an instrument, an aspiration, an imperative and a quotation are licensed.`

// cmdShapes prints the detector table the scan uses, row for row (selftalk.Rules): what each
// row finds, how, one sentence it reports and one near miss it passes. It is the same table the
// scan walks and the one its tests run, so the listing cannot claim a shape the scan misses.
func cmdShapes(c *tool.Call) *tool.Out {
	rules := selftalk.Rules()
	count := map[string]int{}
	for _, r := range rules {
		count[r.Class]++
	}
	o := tool.Done().Fact("rows", len(rules)).Fact("standing", count["standing"]).
		Fact("installation", count["installation"]).Fact("licensed", count["licensed"])
	for _, r := range rules {
		o.Item("row", "class", r.Class, "shape", r.Name,
			"says", tool.Text(r.Says), "finds", tool.Text(r.Finds),
			"passes", tool.Text(r.Passes), "pattern", tool.Text(r.Pattern))
	}
	return o.Note(shapesNote)
}

// shapesNote says how to read a row, and where to try one.
const shapesNote = "a standing or installation row reports its finds= sentence and not its passes= one; " +
	"a licensed row's finds= is reported and its passes= is not, because of the licence. " +
	"Try them: nova-self-talk example --dir ./pages, then nova-self-talk ./pages/journal.md"

// pages are the two example pages, inside the binary, so a first run needs nothing but it.
//
//go:embed testdata/example-pages/*.md
var pages embed.FS

// cmdExample writes the example pages into a directory of the caller's: a local write and the
// only write this tool makes. A page already there with the same bytes is kept; one with other
// bytes is never replaced, and the run refuses before writing anything.
func cmdExample(c *tool.Call) *tool.Out {
	dry := c.DryRun()
	dir := c.Str("dir")
	if oneline.Escape(dir) != dir {
		return tool.Refuse("directory path contains characters the one-line output must escape, so its follow-up command would not name the same path")
	}
	entries, err := pages.ReadDir("testdata/example-pages")
	if err != nil {
		return tool.Refuse("the pages built into this binary cannot be read: " + err.Error())
	}
	var write, kept, problems []string
	bodies := map[string][]byte{}
	for _, e := range entries {
		body, err := pages.ReadFile(path.Join("testdata/example-pages", e.Name()))
		if err != nil {
			return tool.Refuse("the pages built into this binary cannot be read: " + err.Error())
		}
		target := filepath.Join(dir, e.Name())
		switch have, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(have, body):
			kept = append(kept, e.Name())
		case err == nil:
			problems = append(problems, oneline.Quote(target)+" exists with other content and is never replaced; name an empty directory")
		case os.IsNotExist(err):
			write, bodies[e.Name()] = append(write, e.Name()), body
		default:
			problems = append(problems, "cannot read "+oneline.Quote(target)+" to compare: "+reason(err))
		}
	}
	if len(problems) > 0 {
		return tool.Refuse(problems...)
	}
	if !dry {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return tool.Refuse("cannot make " + oneline.Quote(dir) + ": " + reason(err))
		}
		for _, name := range write {
			if err := os.WriteFile(filepath.Join(dir, name), bodies[name], 0o644); err != nil {
				return tool.Refuse("cannot write " + oneline.Quote(filepath.Join(dir, name)) + ": " + reason(err))
			}
		}
	}
	wrote, next := "wrote", exampleNext("", filepath.Join(dir, "journal.md"))
	if dry {
		wrote, next = "would-write", exampleNext("example", dir)
	}
	o := tool.Done().Fact("dir", dir).Fact(wrote, strings.Join(write, ",")).Fact("kept", strings.Join(kept, ","))
	o.Remedy = next
	return o
}

// exampleNext implements SPEC.md §2's runnable next-command rule: it keeps the path one
// POSIX-shell argument, and ends flags when the path begins with a dash. The example
// verb's directory is --dir, because a verb that is not the default takes no positionals.
func exampleNext(verb, file string) string {
	if verb == "example" {
		return "nova-self-talk example --dir=" + shellQuote(file)
	}
	command := "nova-self-talk"
	if verb != "" {
		command += " " + verb
	}
	if strings.HasPrefix(file, "-") {
		command += " --"
	}
	return command + " " + shellQuote(file)
}

// shellQuote renders one path for exampleNext (SPEC.md §2). Ordinary paths stay unchanged;
// adjacent quote segments preserve shell syntax and apostrophes literally.
func shellQuote(s string) string {
	if s != "" {
		safe := true
		for _, c := range s {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '/' || c == '.' || c == '_' || c == '-' || c == ':') {
				safe = false
				break
			}
		}
		if safe {
			return s
		}
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
