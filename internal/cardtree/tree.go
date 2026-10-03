// Package cardtree is a card as a tree of steps (docs/SPEC-SPRINT.md, "A card is a tree of
// steps"; nova-tools#5174 rule 7): the grammar of numbered step blocks, the lint of a tree,
// the script step the member runs with no model, the verdict per step, and the remainder a
// failed step leaves. The owner, 2026-10-02: "any card can be a tree"; "a batch card is just
// nomenclature". A card with no tree in it is a flat card, and nothing here changes it.
package cardtree

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Step is one `STEP <n>.` block of a card: its line and every line under it to the next
// STEP line. A step carrying COMMIT: is a work step (one commit, one verdict); a work step
// carrying SCRIPT: is a script step, which the member runs and no model does.
type Step struct {
	Num     string // "2", "3.1"
	Line    int    // the STEP line, 1-based
	Text    string // the STEP line itself
	Paths   []string
	Commit  string
	Verdict string
	Lang    string // SCRIPT:'s value, lower case; "" for a model step
	Fence   string // the program fence's info string, lower case
	Fenced  bool   // a fenced block was found in the body
	Program string
	Post    []Post
	fields  map[string]int // field name -> its line, for findings
}

// Work says the step is a work step: it carries its own commit line.
func (s Step) Work() bool { return s.Commit != "" }

// Script says the step is a script step: a program the member runs, no model.
func (s Step) Script() bool { return s.Work() && s.Lang != "" }

// Post is one post-condition line of a script step: `POST: sha256 <path> <hex>` (the file's
// hash after the program) or `POST: exit0 <argv>` (a command run in the checkout, no shell,
// that must exit 0). Kind is "" for a line that is neither.
type Post struct {
	Line int
	Raw  string
	Kind string
	Path string
	Sum  string
	Argv []string
}

// Tree is a card read as a tree: every step in card order (which is the walk's order,
// depth first), the header's PATHS: and NEW: globs, and its From: step.
type Tree struct {
	Steps    []Step
	Paths    []string
	From     string // the header's `From: STEP <n>`: the walk starts there; "" when none
	FromLine int
}

// The languages a script step may be written in (docs/SPEC-SPRINT.md, a card is a tree of
// steps: "preference: lisp, or golang obv.", a regex at simplest), and the interpreters
// refused by name, as a SCRIPT: language and as an exit0 command's first word.
var (
	Langs   = []string{"regex", "go", "lisp"}
	refused = []string{"bash", "sh", "zsh", "python", "python3", "perl"}
)

var (
	// StepRE is a STEP line: `STEP 3.` or a dotted child `STEP 3.1.`.
	StepRE  = regexp.MustCompile(`^STEP[ \t]+([0-9]+(?:\.[0-9]+)*)`)
	fromRE  = regexp.MustCompile(`^(?i:STEP)[ \t]+([0-9]+(?:\.[0-9]+)*)[.]?$`)
	hexRE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fenceRE = regexp.MustCompile("^(`{3,})[ \t]*([A-Za-z0-9_+-]*)")
)

// Parse reads a card's steps and the header lines a tree needs. It never fails: what it
// cannot read, Lint names.
func Parse(card string) Tree {
	lines := strings.Split(strings.ReplaceAll(card, "\r\n", "\n"), "\n")
	var t Tree
	var cur *Step
	fence, indent := "", ""
	inProgram := false
	var prog []string
	for i, raw := range lines {
		n := i + 1
		trim := strings.TrimSpace(raw)
		if fence != "" { // inside a fenced block: nothing is a field or a step
			if strings.HasPrefix(trim, fence) && strings.Trim(trim, "`") == "" {
				fence = ""
				if inProgram {
					cur.Program = strings.Join(prog, "\n") + "\n"
					inProgram = false
				}
				continue
			}
			if inProgram {
				prog = append(prog, strings.TrimPrefix(raw, indent))
			}
			continue
		}
		if m := StepRE.FindStringSubmatch(raw); m != nil {
			t.Steps = append(t.Steps, Step{Num: m[1], Line: n, Text: raw, fields: map[string]int{}})
			cur = &t.Steps[len(t.Steps)-1]
			continue
		}
		if m := fenceRE.FindStringSubmatch(trim); m != nil {
			fence = m[1]
			if cur != nil && !cur.Fenced {
				cur.Fenced, cur.Fence, inProgram = true, strings.ToLower(m[2]), true
				indent, prog = raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))], nil
			}
			continue
		}
		k, v, ok := cardhdr.KeyValue(trim)
		if !ok {
			continue
		}
		key := k        // a step's field is upper case, as written: an indented `verdict:` is prose
		if cur == nil { // the header, above the first STEP line
			key = strings.ToUpper(k)
			if raw != trim { // a header key sits at column 0
				continue
			}
			switch key {
			case "PATHS", "NEW":
				t.Paths = append(t.Paths, globs(v)...)
			case "FROM":
				if t.FromLine == 0 {
					t.FromLine = n
					if m := fromRE.FindStringSubmatch(v); m != nil {
						t.From = m[1]
					} else {
						t.From = "?"
					}
				}
			}
			continue
		}
		if _, seen := cur.fields[key]; seen {
			continue
		}
		switch key {
		case "PATHS":
			cur.Paths = globs(v)
		case "COMMIT":
			cur.Commit = v
		case "VERDICT":
			cur.Verdict = v
		case "SCRIPT":
			cur.Lang = strings.ToLower(v)
		case "POST":
			cur.Post = append(cur.Post, parsePost(n, v))
			continue // a step may carry several POST lines
		default:
			continue
		}
		cur.fields[key] = n
	}
	return t
}

// globs is a PATHS: value: comma- or blank-separated globs; `none` is none.
func globs(v string) []string {
	var out []string
	for _, g := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if !strings.EqualFold(g, "none") {
			out = append(out, g)
		}
	}
	return out
}

func parsePost(line int, v string) Post {
	p := Post{Line: line, Raw: v}
	f := strings.Fields(v)
	switch {
	case len(f) == 3 && strings.EqualFold(f[0], "sha256") && hexRE.MatchString(strings.ToLower(f[2])):
		p.Kind, p.Path, p.Sum = "sha256", f[1], strings.ToLower(f[2])
	case len(f) >= 2 && strings.EqualFold(f[0], "exit0"):
		p.Kind, p.Argv = "exit0", f[1:]
	}
	return p
}

// IsTree says the card is a tree: a dotted step, a step with a tree field (COMMIT:, PATHS:,
// VERDICT:, SCRIPT:, POST:) or a From: line. A flat card has none, and every rule of today
// applies to it unchanged.
func (t Tree) IsTree() bool {
	if t.FromLine > 0 {
		return true
	}
	for _, s := range t.Steps {
		if strings.Contains(s.Num, ".") || len(s.fields) > 0 || len(s.Post) > 0 {
			return true
		}
	}
	return false
}

// Work is the work steps the walk visits, in card order (depth first), from the From: step
// on when the card names one.
func (t Tree) Work() []Step {
	start := 0
	if t.From != "" {
		start = len(t.Steps)
		for i, s := range t.Steps {
			if s.Num == t.From {
				start = i
				break
			}
		}
	}
	var out []Step
	for _, s := range t.Steps[start:] {
		if s.Work() {
			out = append(out, s)
		}
	}
	return out
}

// AllScript says every work step the walk visits is a script step: the card runs with no
// model at all (native runs the executor in place of the harness).
func (t Tree) AllScript() bool {
	w := t.Work()
	for _, s := range w {
		if !s.Script() {
			return false
		}
	}
	return len(w) > 0
}

// HasScript says some work step the walk visits is a script step.
func (t Tree) HasScript() bool {
	return slices.ContainsFunc(t.Work(), Step.Script)
}

// Step is the step numbered n, and whether there is one.
func (t Tree) Step(n string) (Step, bool) {
	for _, s := range t.Steps {
		if s.Num == n {
			return s, true
		}
	}
	return Step{}, false
}

// Finding is one defect of a tree: the lint's rule token, the 1-based line, the excerpt.
type Finding struct {
	Check   string
	Line    int
	Excerpt string
}

// The rule tokens a tree's findings carry, and what each wants (the lint prints the remedy).
const (
	CheckNested = "steps-nested"
	CheckStep   = "tree-step"
	CheckScript = "script-step"
)

// Remedies is the remedy of each rule token, for `nova-swarm lint --rules`.
var Remedies = map[string]string{
	CheckNested: "a dotted step `STEP <n>.<k>.` sits under its parent `STEP <n>.`, and the children of one parent are numbered 1, 2, 3 with no gap and no repeat",
	CheckStep:   "a work step (one with COMMIT:) carries its own PATHS:, COMMIT: and VERDICT: lines, every glob of its PATHS: is a relative path inside the checkout and one of the card's PATHS: or NEW: globs, a step with PATHS:, VERDICT:, SCRIPT: or POST: and no COMMIT: is missing its COMMIT:, and From: STEP <n> names a step of the card",
	CheckScript: "a script step says `SCRIPT: regex|go|lisp` (never bash or python), carries its program in one fenced block under it, and at least one `POST: sha256 <path> <64 hex>` or `POST: exit0 <command>` line the gate asserts (a path inside the checkout; a command that is no interpreter: never bash, sh, zsh, python, python3, perl or env), and every work step of its card is a script step; a regex program is lines of `s/<re>/<replacement>/` in Go regexp syntax",
}

// Lint is every defect of a tree card, in card order; nothing for a flat card. The top-level
// numbering (1, 2, 3) is the card lint's own `steps-numbered`; this holds the dotted numbers.
func Lint(card string) []Finding {
	t := Parse(card)
	if !t.IsTree() {
		return nil
	}
	var out []Finding
	add := func(check string, line int, format string, a ...any) {
		out = append(out, Finding{Check: check, Line: line, Excerpt: fmt.Sprintf(format, a...)})
	}
	seen := map[string]bool{}
	last := map[string]int{} // parent number -> its last child's index
	for _, s := range t.Steps {
		if parent, k, ok := splitNum(s.Num); ok {
			switch {
			case !seen[parent]:
				add(CheckNested, s.Line, "%s: no STEP %s above it", s.Text, parent)
			case k != last[parent]+1:
				add(CheckNested, s.Line, "%s: after %s.%d", s.Text, parent, last[parent])
			}
			last[parent] = k
		}
		seen[s.Num] = true
		out = append(out, lintStep(t, s)...)
	}
	if t.HasScript() && !t.AllScript() {
		for _, s := range t.Work() {
			if s.Script() {
				add(CheckScript, s.Line, "STEP %s is a script step in a card with model steps: a script step runs in its own wall, outside any child's, so a card with one is script steps only; put the model steps in a card of their own, Needs: between the two", s.Num)
				break
			}
		}
	}
	if t.FromLine > 0 {
		if s, ok := t.Step(t.From); !ok {
			add(CheckStep, t.FromLine, "From: names no step of this card (want From: STEP <n>)")
		} else if len(t.Work()) == 0 {
			add(CheckStep, t.FromLine, "From: STEP %s leaves no work step to walk", s.Num)
		}
	}
	return out
}

// splitNum splits a dotted step number into its parent and its last part.
func splitNum(num string) (parent string, k int, ok bool) {
	i := strings.LastIndexByte(num, '.')
	if i < 0 {
		return "", 0, false
	}
	k, err := strconv.Atoi(num[i+1:])
	return num[:i], k, err == nil
}

func lintStep(t Tree, s Step) []Finding {
	var out []Finding
	add := func(check string, line int, format string, a ...any) {
		out = append(out, Finding{Check: check, Line: line, Excerpt: fmt.Sprintf(format, a...)})
	}
	if !s.Work() {
		for _, k := range []string{"PATHS", "VERDICT", "SCRIPT"} {
			if l, ok := s.fields[k]; ok {
				add(CheckStep, l, "STEP %s carries %s: and no COMMIT:", s.Num, k)
			}
		}
		if len(s.Post) > 0 {
			add(CheckStep, s.Post[0].Line, "STEP %s carries POST: and no COMMIT:", s.Num)
		}
		return out
	}
	if len(s.Paths) == 0 {
		add(CheckStep, s.Line, "work step %s has no PATHS:", s.Num)
	}
	if s.Verdict == "" {
		add(CheckStep, s.Line, "work step %s has no VERDICT:", s.Num)
	}
	for _, g := range s.Paths {
		if !filepath.IsLocal(g) {
			add(CheckStep, s.fields["PATHS"], "STEP %s's glob %s is not a relative path inside the checkout", s.Num, g)
		}
	}
	if len(t.Paths) > 0 {
		for _, g := range s.Paths {
			if filepath.IsLocal(g) && !slices.Contains(t.Paths, g) {
				add(CheckStep, s.fields["PATHS"], "STEP %s's glob %s is not in the card's PATHS: or NEW:", s.Num, g)
			}
		}
	}
	if s.Lang == "" {
		if len(s.Post) > 0 {
			add(CheckScript, s.Post[0].Line, "STEP %s carries POST: and no SCRIPT:", s.Num)
		}
		return out
	}
	line := s.fields["SCRIPT"]
	switch {
	case slices.Contains(refused, s.Lang):
		add(CheckScript, line, "SCRIPT: %s: never bash or python; write it in regex, go or lisp", s.Lang)
		return out
	case !slices.Contains(Langs, s.Lang):
		add(CheckScript, line, "SCRIPT: %s is not one of %s", s.Lang, strings.Join(Langs, ", "))
		return out
	}
	switch {
	case !s.Fenced || strings.TrimSpace(s.Program) == "":
		add(CheckScript, line, "STEP %s has no fenced program under SCRIPT:", s.Num)
	case s.Fence != "" && s.Fence != s.Lang:
		add(CheckScript, line, "STEP %s's fence says %s and SCRIPT: says %s", s.Num, s.Fence, s.Lang)
	case s.Lang == "regex":
		if _, err := ParseRegex(s.Program); err != nil {
			add(CheckScript, line, "STEP %s: %s", s.Num, err)
		}
	}
	if len(s.Post) == 0 {
		add(CheckScript, line, "script step %s has no POST: line for the gate to assert", s.Num)
	}
	for _, p := range s.Post {
		switch {
		case p.Kind == "":
			add(CheckScript, p.Line, "POST: %s is neither `sha256 <path> <64 hex>` nor `exit0 <command>`", p.Raw)
		case p.Kind == "sha256" && !filepath.IsLocal(p.Path):
			add(CheckScript, p.Line, "POST: sha256 %s is not a relative path inside the checkout", p.Path)
		case p.Kind == "exit0" && RefusedCommand(p.Argv) != "":
			add(CheckScript, p.Line, "POST: %s", RefusedCommand(p.Argv))
		}
	}
	return out
}

// Edit is one line of a regex program: every match of RE in a file becomes Repl ($1
// expands as in regexp.Expand).
type Edit struct {
	RE   *regexp.Regexp
	Repl string
}

// ParseRegex reads a regex program: one `s<d><re><d><replacement><d>` per non-blank line,
// any delimiter d, Go regexp syntax; a line beginning `#` is a comment.
func ParseRegex(program string) ([]Edit, error) {
	var out []Edit
	for i, l := range strings.Split(program, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if len(l) < 4 || l[0] != 's' {
			return nil, fmt.Errorf("regex line %d %q is not s/<re>/<replacement>/", i+1, l)
		}
		d := string(l[1])
		parts := strings.Split(l[2:], d)
		if len(parts) != 3 || parts[2] != "" {
			return nil, fmt.Errorf("regex line %d %q is not s%s<re>%s<replacement>%s", i+1, l, d, d, d)
		}
		re, err := regexp.Compile(parts[0])
		if err != nil {
			return nil, fmt.Errorf("regex line %d: %v", i+1, err)
		}
		out = append(out, Edit{RE: re, Repl: parts[1]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the regex program has no s/<re>/<replacement>/ line")
	}
	return out, nil
}
