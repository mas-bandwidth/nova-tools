// Package card is what a brief's writers hold every brief to before it leaves: its
// PATHS computed from its START files, never typed by a hand, and the card checks
// (docs/SPEC-CARD-CONTRACT.md section 6). nova-card generate computes and checks
// every brief it renders, nova-card lint checks a brief on file, and nova-sprint add
// holds every brief it admits to the same checks.
package card

import (
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// PackagePaths is a card's PATHS computed from what it starts from: every directory a
// START file lives in, as that package's Go files and its tests (<dir>/*.go,
// <dir>/*_test.go); a START entry that is a directory, the same; and each doc the card
// names, as itself. A START file that is no Go file (a ledger, a manifest) is kept as
// itself, and a bare word (no slash and no suffix: a tool name, a namedpaths word)
// names no path. A package is the unit a change lives in: a typed file list one file
// short holds the card at land (E12).
func PackagePaths(start, docs []string) []string {
	var paths []string
	for _, s := range start {
		s = path.Clean(strings.TrimPrefix(strings.TrimSpace(s), "./"))
		switch {
		case s == "" || s == ".":
		case strings.HasSuffix(s, ".go"):
			paths = append(paths, packageGlobs(path.Dir(s))...)
		case path.Ext(s) == "" && strings.Contains(s, "/"):
			paths = append(paths, packageGlobs(s)...)
		case strings.Contains(s, "/") || path.Ext(s) != "":
			paths = append(paths, s)
		}
	}
	paths = append(paths, docs...)
	return cardgen.MergePaths(paths)
}

// packageGlobs is a package directory's Go files and its tests.
func packageGlobs(dir string) []string {
	if dir == "." {
		return []string{"*.go", "*_test.go"}
	}
	return []string{dir + "/*.go", dir + "/*_test.go"}
}

// Docs are the entries of a planned PATHS that are neither Go nor a glob nor a
// directory: the ledger, docs/CLI.md. They ride beside the START packages.
func Docs(paths []string) []string {
	var docs []string
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") || strings.ContainsAny(p, "*?") || path.Ext(p) == "" {
			continue
		}
		docs = append(docs, p)
	}
	return docs
}

// Start is a brief's START: line, its entries split on commas and blanks; nil when the
// brief has none.
func Start(brief string) []string {
	return headerList(brief, "START")
}

// Paths is the PATHS of the brief c renders to under h, computed: PackagePaths of the
// START line that brief carries, and the docs the planner named. No hand types the
// PATHS of a generated brief.
func Paths(h cardgen.Header, c cardgen.Card) []string {
	return PackagePaths(Start(cardgen.Render(h, c)), Docs(c.Paths))
}

// Answered says a PATHS entry names nothing at the base because the card creates it
// (its NEW: line). A package's Go files are not answered by a test file the card
// creates: a package with no Go file at the base is not there.
func Answered(c cardgen.Card, glob string) bool {
	for _, n := range c.New {
		if strings.HasSuffix(glob, "/*.go") && strings.HasSuffix(n, "_test.go") {
			continue
		}
		if ok, _ := path.Match(glob, n); ok {
			return true
		}
	}
	return false
}

// Shared says two cards name one PATHS entry and neither needs the other: the add
// wants --allow-shared-paths.
func Shared(cards []cardgen.Card) bool {
	owner := map[string][]int{}
	for i, c := range cards {
		for _, p := range c.Paths {
			for _, j := range owner[p] {
				if !needs(cards[i], cards[j].ID) && !needs(cards[j], c.ID) {
					return true
				}
			}
			owner[p] = append(owner[p], i)
		}
	}
	return false
}

func needs(c cardgen.Card, id string) bool {
	for _, d := range c.Deps {
		if d == id {
			return true
		}
	}
	return false
}

// Options are what the checks read from outside the brief: the names of the people,
// friends and machines of the deployment (none is written in this tree, internal/ci
// TestGeneralityText) and the ids of the cards dropped off the table.
type Options struct {
	Names   []string
	Dropped []string
	// Contract is the contract text of a version, read from the repository's
	// ContractPath (ReadContract); nil reads none, and a brief by reference is then the
	// finding contract-unread (docs/SPEC-CARD-CONTRACT.md section 7).
	Contract func(version string) (string, error)
}

// Lint is cardgen.Lint and Checks under o: everything a brief is held to before it
// leaves its writer. A brief by reference is linted as the lane reads it, the contract
// in place of its Contract: line (section 7).
func Lint(id, brief string, o Options) []cardgen.LintFinding {
	read, out := lintContract(id, brief, o)
	if len(out) > 0 {
		return append(out, Checks(id, brief, o)...)
	}
	return append(cardgen.Lint(id, read), Checks(id, brief, o)...)
}

// Checks are the card checks, past the add's own lint:
//
//   - tier-line: line 1 names a tier (`tier: flash|pro|heavy|frontier`);
//   - test-outside-paths: the TEST line's package is a directory PATHS names, so the
//     test the card lands with is one it may edit;
//   - personal-name: no name of o.Names outside a double-quoted span (the owner's
//     words, quoted, keep theirs), the card's own id, or the WHO: line that pins a
//     friend;
//   - dropped-card: no id of o.Dropped but the card's own.
//
// The tier and TEST checks hold a card brief, one with a PATHS: line; a brief with
// none is a free task.
func Checks(id, brief string, o Options) []cardgen.LintFinding {
	var out []cardgen.LintFinding
	add := func(check string, line int, excerpt string) {
		out = append(out, cardgen.LintFinding{ID: id, Check: check, Line: line, Excerpt: excerpt})
	}
	if paths := headerList(brief, "PATHS"); paths != nil {
		if m, _ := cardhdr.ReadModel(brief); m.Tier == "" {
			add("tier-line", 1, "line 1 names no tier; write tier: and one of "+cardhdr.RouteList+" on it")
		}
		if pkg := TestPackage(brief); pkg != "" && !covers(paths, pkg) {
			add("test-outside-paths", headerLine(brief, "TEST"), "the TEST package "+pkg+" is no directory PATHS names ("+strings.Join(paths, ", ")+"); a card lands with a test it may edit")
		}
	}
	for i, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(line, "WHO:") {
			continue // the friend a card is pinned to is named here (cardhdr.ReadWho)
		}
		scan := strings.ToLower(unquoted(line))
		if id != "" {
			scan = strings.ReplaceAll(scan, strings.ToLower(id), " ")
		}
		for _, n := range o.Names {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" && hasWord(scan, n) {
				add("personal-name", i+1, "names "+n+" outside the owner's quoted words; write the role (the owner, a friend, a bench), never the name")
			}
		}
		for _, d := range o.Dropped {
			if d != "" && d != id && hasWord(line, d) {
				add("dropped-card", i+1, "names "+d+", a card dropped off the table; a brief stands alone and owes nothing to a dropped card")
			}
		}
	}
	return out
}

// TestPackage is the package a brief's TEST line names, repository-relative with no
// ./ and no trailing slash: the first field that is a path (`internal/bus TestY`,
// `./internal/bus TestY`, `./tools TestY`, `go test ./internal/bus -run TestY`); "" for
// TEST: none or a line that names none.
func TestPackage(brief string) string {
	value := ""
	for _, line := range strings.Split(brief, "\n") {
		if v, ok := strings.CutPrefix(line, "TEST:"); ok {
			value = v
			break
		}
	}
	for _, f := range strings.Fields(value) {
		dotted := strings.HasPrefix(f, "./")
		f = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(f, "./"), "/..."), "/")
		if strings.HasPrefix(f, "-") || f == "" || !dotted && !strings.Contains(f, "/") {
			continue
		}
		return path.Clean(f)
	}
	return ""
}

// covers says some PATHS entry lies in the directory pkg or under it.
func covers(paths []string, pkg string) bool {
	for _, p := range paths {
		if dir := path.Dir(strings.TrimPrefix(p, "./")); dir == pkg || strings.HasPrefix(dir, pkg+"/") {
			return true
		}
	}
	return false
}

// headerList is a header line's comma- or space-separated entries, nil when the brief
// has no such line.
func headerList(brief, key string) []string {
	value, ok := swarm.CardHeaderValue([]byte(brief), key)
	if !ok {
		return nil
	}
	out := []string{}
	for _, p := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// headerLine is the 1-based line of a brief's KEY: line, 1 when it has none.
func headerLine(brief, key string) int {
	for i, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(line, key+":") {
			return i + 1
		}
	}
	return 1
}

var quotedRE = regexp.MustCompile(`"[^"]*"|“[^”]*”`)

// unquoted is a line with its double-quoted spans blanked: the owner's words, quoted,
// are theirs to keep.
func unquoted(line string) string {
	return quotedRE.ReplaceAllStringFunc(line, func(q string) string { return strings.Repeat(" ", len(q)) })
}

// hasWord says w is in s as a word of its own: no letter, digit or underscore on
// either side, so a name inside another word is no name.
func hasWord(s, w string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(w)
		if !wordRune(s, start-1) && !wordRune(s, end) {
			return true
		}
		from = start + 1
	}
}

func wordRune(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := rune(s[i])
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}
