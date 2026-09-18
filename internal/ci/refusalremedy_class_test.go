package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// refusalremedy_class_test.go is the class rule behind "tell them what to do"
// (Glenn, 2026-09-17): a prompt, and a refusal, gives POSITIVE instructions with
// exact paths. A refusal that says only what is wrong hands the reader a second
// job -- work out what would have been right -- and that job is the one the tool
// was built to do.
//
// The hurt is the whole loop's, not one bug's. `nova-pulse wake` refused with
// `--registry <path>: ...; refusing to guess` and never said which file it
// wanted; `fleet registry` refused an unknown role and never listed the roles.
// Each time, a person or a child went and read the source to find out what to
// type -- and a child that has to read the source to answer a refusal is a card
// that stalls, which is the defect class behind "fix the prompt, not retry".
//
// The rule this test enforces mechanically:
//
//	every string literal in non-test Go under cmd/ and internal/ that BEGINS a
//	refusal line must carry a remedy: the thing to do, written the way it would
//	be typed.
//
// **What begins a refusal line** -- three shapes, read off the tree as it stands:
//
//  1. `<TOOL> <VERB> REFUSED`, upper-case words then REFUSED, at the start of the
//     literal: `FILL REFUSED bench=...`, `DELETE-SLOT REFUSED: ...`,
//     `SECRETS REFUSED: ...`.
//  2. `REFUSED:` at the start of the literal.
//  3. `REFUSED reason=` anywhere in it, which is how the refusal helpers write
//     their own format string: `"%s REFUSED reason=%s %s\n"`.
//
// **What counts as a remedy** -- four shapes, derived by reading twenty real
// refusals across nova-pulse, nova-bus, nova-secrets, nova-sandbox, nova-merge,
// nova-decide and internal/swarm, and not invented here:
//
//   - `remedy=` in the same format string. The machine-readable shape, written
//     by internal/fleet's Refusal.Line and printed by `nova-pulse fill`:
//     `FILL REFUSED bench=batman reason=runner-host remedy="..."`.
//   - an IMPERATIVE OPENING A CLAUSE: one of `run`, `give`, `name`, `pass`,
//     `set`, `fix`, `add`, `use`, `check`, `delete`, `drop`, `want`, `leave`,
//     `rerun`, `read`, `ask`, `write`, `open`, `take`, `remove`, `rename`,
//     `retry`, `choose`, `list`, `move`, `make`, `put`, `try`, `start`, `raise`,
//     `lower`, `quote`, `wait`, `send`, `hold`, `keep`, `point`, `build`,
//     `install`, `close`, `end`, `split`, `narrow`, `widen`, `wire`, `fill`,
//     `repair`, `edit`, `copy`, `link`, `unset`, `clear`, `shrink`, `land`,
//     `push`, `pull`, `merge`, `cut`, `stop`, `kill`, `finalize`, `reseal`,
//     `seal` -- after a `;`, a `:`, a `(`, a comma, an `and`/`or`/`then`, or at
//     the start of the line. The clause boundary is what keeps this tight: a
//     bracket that opens with a NOUN, `(the queue is held)`, is the trouble said
//     twice and not an answer. The commonest shape in the tree:
//     `internal/pulse/hygiene.go`'s
//     `"DELETE-SLOT REFUSED: %s still holds jobs (delete its jobs first)"`, and
//     `internal/merge/rebase.go`'s `"...; pass a writable --markers directory"`.
//   - a COMMAND written the way it would be typed: one of this repository's own
//     tools with a verb after it, `nova-<tool> <verb>`, or any backticked
//     command. `cmd/nova-merge/sweep.go`:
//     `"SWEEP REFUSED: %s; run: nova-merge sweep --repo %s --branch %s --once\n"`,
//     and `cmd/nova-swarm/main.go`'s
//     `"RECLAIM REFUSED id=%s: %s; nova-swarm finalize --pool %s --task %s"`.
//   - a PASS-THROUGH: the literal is the prefix and nothing but format verbs,
//     punctuation and space -- `"SEND REFUSED: %s\n"`, `"%s REFUSED reason=%s
//     %s\n"`. The whole message is the value it formats, and the remedy is in
//     THAT value, at each call site. A hundred and forty of the tree's refusal
//     literals are this shape; reading the value would mean becoming a type
//     checker, so this test reads the literal that holds the words and leaves
//     the one that holds none alone. It is the biggest narrowing here and it is
//     written down in SPEC-CI as one.
//
// Its allowlist is the usual shape: one `file:function:token` per line with its
// reason, checked in BOTH directions, so it can only ever shrink. It holds the
// refusals whose remedy is on the NEXT line -- a multi-line usage block printed
// after the reason -- and the handful that honestly have no remedy at all.

// refusalRemedyAllowlistPath is the shrink-only list of the refusal lines that
// carry no remedy in their own literal. It lives in testdata so a reader sees
// the whole exception set without reading the test.
const refusalRemedyAllowlistPath = "testdata/refusalremedy_allowlist.txt"

// refusalRemedyRemedy is the one thing to do about a finding. (A class test
// about remedies that printed a finding with no remedy would be the joke it
// deserves.)
const refusalRemedyRemedy = "put the thing to do in the literal, written the way it would be typed: `remedy=\"...\"`, a parenthesised imperative such as `(run nova-pulse fleet registry --machines <file>)`, or a `; run: ...` tail"

// refusalStart matches shape 1: upper-case tool and verb words, then REFUSED, at
// the start of the literal. `%s REFUSED` is shape 3 and is matched separately.
var refusalStart = regexp.MustCompile(`^[A-Z][A-Z0-9-]*( [A-Z][A-Z0-9-]*)* REFUSED\b`)

// refusalReason matches shape 3: the refusal helpers' own format strings.
var refusalReason = regexp.MustCompile(`REFUSED reason=`)

// passThroughLabel is a machine-readable `key=` or `key=token` field: a label,
// never a word of prose.
var passThroughLabel = regexp.MustCompile(`[a-z_]+=[A-Za-z0-9_.:/-]*`)

// refusalToken is the leading words of the line, which the allowlist keys on --
// stable under a merge that shifts lines, unlike a line number.
var refusalToken = regexp.MustCompile(`^([A-Z][A-Z0-9-]*( [A-Z][A-Z0-9-]*)*)? ?REFUSED`)

// remedyClause is an IMPERATIVE opening a clause: after a `;`, a `:`, a `(`, a
// comma, or an `and`/`or`/`then`, and at the very start of the line. The verbs
// are the ones the tree actually uses, plus their obvious neighbours. The clause
// boundary is what makes this tight: a refusal that opens a bracket with a NOUN
// -- `(the queue is held)` -- is describing the trouble again, not answering it,
// and an imperative buried mid-sentence is prose.
var remedyClause = regexp.MustCompile(`(?i)(^|[;:(,\n]|\bthen\b|\band\b|\bor\b)[ ]*(run|give|name|pass|set|fix|add|use|check|delete|drop|want|leave|rerun|read|ask|write|open|take|remove|rename|retry|choose|list|move|make|put|try|start|raise|lower|quote|wait|send|hold|keep|point|build|install|close|end|split|narrow|widen|wire|fill|repair|edit|copy|link|unset|clear|shrink|widen|land|push|pull|merge|cut|stop|kill|finalize|reseal|seal)[ :]`)

// remedyCommand is a command written the way it would be typed: one of this
// repository's own tools with a verb after it. `nova-swarm finalize --pool ...`
// in a refusal IS the answer, whether or not a `run:` introduces it.
var remedyCommand = regexp.MustCompile(`\bnova-[a-z]+ [a-z][a-z-]*`)

// remedyTails are the ways a refusal hands over the answer without an imperative
// at a clause boundary.
var remedyTails = []string{
	"; the repair is", "; the fix is", "rerun without", "refusing to guess (",
	"--anyway",
}

// refusalFinding is one refusal literal with no remedy in it.
type refusalFinding struct {
	File  string // repo-relative, slash-separated
	Func  string // the enclosing function, part of the allowlist's key
	Token string // the leading `FILL REFUSED` / `REFUSED:`, the rest of the key
	Line  int
	Text  string // the literal, capped, so the finding can be read without opening the file
}

func (f refusalFinding) key() string { return f.File + ":" + f.Func + ":" + f.Token }

func (f refusalFinding) String() string {
	return fmt.Sprintf("%s:%d: %s in %s refuses and does not say what to do: %q; %s",
		f.File, f.Line, f.Token, f.Func, f.Text, refusalRemedyRemedy)
}

// TestEveryRefusalLineCarriesARemedy walks the two trees and refuses a refusal
// line that carries no remedy and is not allowlisted, and an allowlist entry
// that no longer names one.
func TestEveryRefusalLineCarriesARemedy(t *testing.T) {
	root := repoRoot(t)
	allow := readRefusalRemedyAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// testdata holds this test's own fixtures and the fake binaries the
				// swarm and wake suites drive; neither ships, and walking the first
				// would find the offenders this test is meant to find.
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found, err := refusalsWithoutARemedy(rel, raw)
			if err != nil {
				return err
			}
			for _, f := range found {
				seen[f.key()] = true
				if !allow[f.key()] {
					violations = append(violations, f.String()+
						"\n  (or add "+f.key()+" to internal/ci/"+refusalRemedyAllowlistPath+" with a reason)")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no refusal without a remedy is there any more; delete the stale entry (the list only shrinks)",
				refusalRemedyAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestRefusalRemedyScannerReadsTheFixtures is the red-test contract: the scanner
// flags the pre-fix text and says nothing about the fixed text. Without it, a
// scanner that had quietly stopped matching would keep the tree green by finding
// nothing at all.
func TestRefusalRemedyScannerReadsTheFixtures(t *testing.T) {
	before := readFile(t, filepath.Join("testdata", "refusalremedy", "before.go.txt"))
	found, err := refusalsWithoutARemedy("internal/fixture/verb.go", []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ fn, token string }{
		{"cmdBare", "FILL REFUSED"},
		{"cmdColon", "REFUSED"},
		{"cmdReason", "PROBE REFUSED"},
		{"cmdNounBracket", "GATE REFUSED"},
	}
	if len(found) != len(want) {
		t.Fatalf("the pre-fix fixture holds %d refusals with no remedy, the scanner found %d: %v", len(want), len(found), found)
	}
	for i, w := range want {
		got := found[i]
		if got.Func != w.fn || got.Token != w.token {
			t.Errorf("finding %d = %s %q, want %s %q", i, got.Func, got.Token, w.fn, w.token)
		}
		if got.Line == 0 {
			t.Errorf("finding %d carries no line; a finding a reader cannot open is half a finding", i)
		}
	}

	after := readFile(t, filepath.Join("testdata", "refusalremedy", "after.go.txt"))
	found, err = refusalsWithoutARemedy("internal/fixture/verb.go", []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("the fixed fixture answers every refusal and must pass, the scanner found %v", found)
	}
}

// refusalsWithoutARemedy reads one .go file and returns every refusal literal in
// it that carries no remedy. rel is the name the findings carry.
//
// It reads the syntax tree, never the text: a refusal line quoted in a doc
// comment -- and this repository's comments quote them constantly -- is
// documentation, not output, and a text scan cannot tell the two apart.
func refusalsWithoutARemedy(rel string, src []byte) ([]refusalFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var found []refusalFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		name := "(package level)"
		var node ast.Node = decl
		if ok {
			if fn.Body == nil {
				continue
			}
			name = fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				name = receiverName(fn.Recv.List[0].Type) + "." + name
			}
			node = fn.Body
		}
		ast.Inspect(node, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if !beginsARefusal(text) || hasARemedy(text) {
				return true
			}
			found = append(found, refusalFinding{
				File:  rel,
				Func:  name,
				Token: refusalTokenOf(text),
				Line:  fset.Position(lit.Pos()).Line,
				Text:  capLiteral(text),
			})
			return true
		})
	}
	return found, nil
}

// refusalTokenOf is the line's leading `FILL REFUSED` / `REFUSED`, which the
// allowlist keys on. A helper's own format string opens with a verb rather than
// a word -- `"%s REFUSED reason=%s %s"` -- and answers the bare token.
func refusalTokenOf(text string) string {
	t := strings.TrimSpace(refusalToken.FindString(strings.TrimSpace(text)))
	if t == "" {
		return "REFUSED"
	}
	return t
}

// beginsARefusal is the three shapes above.
func beginsARefusal(text string) bool {
	t := strings.TrimSpace(text)
	return refusalStart.MatchString(t) || strings.HasPrefix(t, "REFUSED:") || refusalReason.MatchString(t)
}

// hasARemedy is the four shapes above.
func hasARemedy(text string) bool {
	if strings.Contains(text, "remedy=") {
		return true
	}
	if strings.Contains(text, "`") { // a backticked command is a command to type
		return true
	}
	for _, tail := range remedyTails {
		if strings.Contains(text, tail) {
			return true
		}
	}
	// The refusal's own leading token is not prose: `CUT REFUSED check=slot` must
	// not read as the imperative `check`.
	body := refusalToken.ReplaceAllString(strings.TrimSpace(text), "")
	if remedyClause.MatchString(body) || remedyCommand.MatchString(body) {
		return true
	}
	return isPassThrough(text)
}

// isPassThrough says the literal is the refusal's PREFIX and nothing else: the
// words are all in the value it formats, so there is nothing here to hold to the
// rule. `SEND REFUSED: %s\n` is one; `SEND REFUSED: %s is held\n` is not.
//
// This is the test's biggest narrowing. Following the formatted value to the
// error that built it means becoming a type checker, and a type checker that
// reads every `fmt.Errorf` in seventy-nine files is a second tool, not a class
// test. The line is drawn where the WORDS are: a literal that carries words
// carries its remedy, and a literal that carries none is a frame.
func isPassThrough(text string) bool {
	t := text
	// The format verbs go FIRST: a helper's own format string opens with one
	// (`"%s REFUSED reason=%s %s"`), and the leading token is only visible once
	// they are gone.
	for _, verb := range []string{"%+v", "%#v", "%s", "%q", "%v", "%d", "%w", "%x", "%t", "%f"} {
		t = strings.ReplaceAll(t, verb, "")
	}
	t = refusalToken.ReplaceAllString(strings.TrimSpace(t), "")
	// `reason=bad_write`, `bench=` and `check=step1` are LABELS, not words: a
	// machine-readable key, with or without its token value. A frame of labels is
	// still a frame.
	t = passThroughLabel.ReplaceAllString(t, "")
	return strings.TrimSpace(strings.Trim(t, "():;,.-!? \t\n")) == ""
}

// capLiteral keeps a finding readable on one line.
func capLiteral(text string) string {
	t := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(t) <= 120 {
		return t
	}
	return t[:117] + "..."
}

// readRefusalRemedyAllowlist reads the shrink-only list: one
// `file:function:token` per line, then a space, then the reason it is there. An
// entry with no reason is a red run, because a narrowing nobody explained is a
// narrowing nobody can ever remove.
func readRefusalRemedyAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(refusalRemedyAllowlistPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v (the list is part of the rule; create it empty rather than deleting it)", refusalRemedyAllowlistPath, err)
	}
	out := map[string]bool{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, _ := strings.Cut(line, "  ")
		key = strings.TrimSpace(key)
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s line %d: %q carries no reason; every narrowing says why it is one (write `<file>:<func>:<token>  <reason>`)",
				refusalRemedyAllowlistPath, i+1, key)
			continue
		}
		if out[key] {
			t.Errorf("%s line %d: %q is listed twice; one line per refusal", refusalRemedyAllowlistPath, i+1, key)
		}
		out[key] = true
	}
	return out
}
