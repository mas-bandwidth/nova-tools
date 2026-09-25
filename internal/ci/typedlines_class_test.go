package ci

import (
	"go/ast"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// typedLineLogAllow is every file that may name a PR record's typed-line log
// (pr:<name>:<n>:lines, read.LinesKey) or its last_line stamp, with why. Every
// verdict on typed lines (the stream lander's ReadAt, the route duty, the
// reaper's fence, the digest, the lineup's probe read) reads ONE place, the
// record's reads field, which ns_read_post appends to in the same call that
// appends the log (nova-tools#4049). When #3874's read record lands it is the
// one place and this list moves with it.
var typedLineLogAllow = map[string]string{
	"internal/nsprint/fn/lua/03_task_event.lua": "the one writer: ns_read_post and TE.line append the log and the reads field in one call",
	"internal/nsprint/read/read.go":             "defines LinesKey; read brief prints the log to the reader and decides nothing",
	"internal/nsprint/fn/lua/unblock_spec.lua":  "a SPEC line on a spec issue's record: written to the log and counted (LLEN) in its receipt; no read verdict",
	"internal/nsprint/fn/lua/02_card_move.lua":  "card:<id>:lines, a card's own key deleted with the card, not a PR record's log",
}

// TestNoSecondReaderOfTypedLines is the class rule of nova-tools#4049: typed
// lines are read from one place. `read post` wrote the lines log and
// last_line while the stream lander read the record's reads field, so a SCORE
// was invisible to `stream open --dry-run` until Rowan copied the log into the
// field by hand (56 records before stream k, 17 more before the next three
// streams), and the lineup's probe read was a third reader of the log. Any
// non-test Go or Lua file under cmd/ or internal/ that names the log key or
// the last_line stamp and is not on typedLineLogAllow is a second reader.
func TestNoSecondReaderOfTypedLines(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	hits := map[string][]string{}
	oneReaders := 0
	for _, f := range tree.Files {
		if !f.InAnyDir("cmd", "internal") || f.Test || strings.HasSuffix(f.Rel, "_test.lua") {
			continue
		}
		var found []string
		var reads bool
		switch {
		case f.Go:
			if f.AST == nil {
				continue
			}
			found, reads = goTypedLineRefs(tree.FSet, f.AST)
		case strings.HasSuffix(f.Rel, ".lua"):
			src, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatalf("%s: %v", f.Rel, err)
			}
			found, reads = luaTypedLineRefs(string(src))
		default:
			continue
		}
		if reads {
			oneReaders++
		}
		if len(found) > 0 {
			hits[f.Rel] = found
		}
	}
	if oneReaders == 0 {
		t.Fatal("no Go or Lua file under cmd/ or internal/ names the reads field; the one place typed lines are read from has moved, and this rule holds nothing until it names the new place")
	}
	var rels []string
	for rel := range hits {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if _, ok := typedLineLogAllow[rel]; !ok {
			t.Errorf("%s names the typed-line log or last_line (%s): a second reader of typed lines, the #4049 defect (read post and the lander disagreed until a hand copy) — read the pr record's reads field, which ns_read_post writes in the same call, or add the file to typedLineLogAllow with why it decides nothing",
				rel, strings.Join(hits[rel], "; "))
		}
	}
	for rel, why := range typedLineLogAllow {
		if _, ok := hits[rel]; !ok {
			t.Errorf("typedLineLogAllow names %s (%s) and it no longer names the typed-line log; drop the entry so the list cannot hide a new reader", rel, why)
		}
	}
}

// goTypedLineRefs lists the places a Go file names the typed-line log: the
// identifier LinesKey, a string literal holding ":lines", or the literal
// "last_line". reads is true when it names the "reads" field.
func goTypedLineRefs(fset *token.FileSet, file *ast.File) (found []string, reads bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == "LinesKey" {
				found = append(found, "LinesKey at line "+strconv.Itoa(fset.Position(x.Pos()).Line))
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(x.Value)
			if err != nil {
				return true
			}
			switch {
			case v == "reads":
				reads = true
			case v == "last_line" || strings.Contains(v, ":lines"):
				found = append(found, strconv.Quote(v)+" at line "+strconv.Itoa(fset.Position(x.Pos()).Line))
			}
		}
		return true
	})
	return found, reads
}

// luaTypedLineRefs lists the lines of a Lua file, comments cut, that name the
// typed-line log (a ':lines' key suffix) or the 'last_line' field. reads is
// true when a line names the 'reads' field.
func luaTypedLineRefs(src string) (found []string, reads bool) {
	for i, line := range strings.Split(src, "\n") {
		code := luaCode(line)
		for _, q := range []string{"'", `"`} {
			if strings.Contains(code, q+"reads"+q) {
				reads = true
			}
			if strings.Contains(code, q+":lines"+q) || strings.Contains(code, q+"last_line"+q) {
				found = append(found, "line "+strconv.Itoa(i+1))
				break
			}
		}
	}
	return found, reads
}

// luaCode is a Lua line with its -- comment cut, a -- inside a quoted string
// kept.
func luaCode(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		case quote == 0 && c == '-' && i+1 < len(line) && line[i+1] == '-':
			return line[:i]
		}
	}
	return line
}
