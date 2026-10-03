package cardtree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The verdict words of a step line.
const (
	OK      = "ok"
	Broken  = "broken"
	NotDone = "not-done"
	Skipped = "skipped"
)

// Result is one work step's verdict: `step <n>: <verdict> <sha|-> <words>`, one line per work
// step in the result, in walk order (docs/SPEC-CARD-CONTRACT.md section 3, the verdict per
// step). Sha is the step's own commit, "" when it made none.
type Result struct {
	Num     string
	Verdict string
	Sha     string
	Words   string
}

// Line is the step's verdict line as the result carries it.
func (r Result) Line() string {
	sha := r.Sha
	if sha == "" {
		sha = "-"
	}
	return strings.TrimSpace(fmt.Sprintf("step %s: %s %s %s", r.Num, r.Verdict, sha, oneline.Cap(strings.Join(strings.Fields(r.Words), " "), 300)))
}

var verdictRE = regexp.MustCompile(`(?i)^[ \t*-]*step[ \t-]?([0-9]+(?:\.[0-9]+)*):[ \t]*(ok|broken|not-done|skipped)\b[ \t]*(\S*)[ \t]*(.*)$`)

// ParseVerdicts reads every step line of a result (its header block or its body), by step
// number; the first line for a step wins.
func ParseVerdicts(text string) map[string]Result {
	out := map[string]Result{}
	for _, l := range strings.Split(text, "\n") {
		m := verdictRE.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			continue
		}
		if _, dup := out[m[1]]; dup {
			continue
		}
		sha := strings.ToLower(m[3])
		if sha == "-" {
			sha = ""
		}
		out[m[1]] = Result{Num: m[1], Verdict: strings.ToLower(m[2]), Sha: sha, Words: strings.TrimSpace(m[4])}
	}
	return out
}

// Land is where a tree card's result lands (TREE-CARD-RULES rule 1; the owner, 2026-10-02:
// a failed step n "lands 1..n-1 and redeals n.. as a new card"): the first work step whose
// verdict is not ok (a step with no line is not-done), and the commit of the last ok step
// before it that made one. failed is nil when every step is ok; land is "" when no step
// before the failed one committed anything.
func Land(t Tree, v map[string]Result) (failed *Result, land string) {
	for _, s := range t.Work() {
		r, ok := v[s.Num]
		if !ok {
			r = Result{Num: s.Num, Verdict: NotDone, Words: "the result carries no line for this step"}
		}
		if r.Verdict != OK {
			return &r, land
		}
		if r.Sha != "" {
			land = r.Sha
		}
	}
	return nil, land
}

// Remainder is the card a failed step leaves: the brief as it is, with `From: STEP <n>` (the
// walk starts there) and `Needs: <id>` (it lands after the card it continues) as header lines
// after line 1. An existing From: line is replaced, an existing Needs: line extended.
func Remainder(card, id, from string) string {
	lines := strings.Split(card, "\n")
	if len(lines) == 0 {
		return card
	}
	needs := false
	head := []string{lines[0]}
	rest := lines[1:]
	for i, l := range rest {
		if strings.TrimSpace(l) == "" {
			break // the header block ends at its first blank line
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "FROM":
			rest[i] = "From: STEP " + from
			from = ""
		case "NEEDS":
			if !needs {
				needs = true
				if v = strings.TrimSpace(v); v == "" || v == "-" || strings.EqualFold(v, "none") {
					rest[i] = "Needs: " + id
				} else {
					rest[i] = "Needs: " + v + ", " + id
				}
			}
		}
	}
	if from != "" {
		head = append(head, "From: STEP "+from)
	}
	if !needs {
		head = append(head, "Needs: "+id)
	}
	return strings.Join(append(head, rest...), "\n")
}

// RemainderID is the id of the card the step n of card id leaves: `<id>-r<n>`.
func RemainderID(id, from string) string { return id + "-r" + from }

// Sys is what a script step needs of the machine: run a program in a directory (no shell),
// commit a step's paths with its message (the sha, "" when nothing changed), and a directory
// the programs are written into. The executor's is the real one; a test's is its own.
type Sys struct {
	Run    func(dir string, argv ...string) error
	Commit func(dir string, paths []string, message string) (sha string, err error)
	Work   string
}

// RunScript runs one script step in the checkout dir, with no model (TREE-CARD-RULES rule 3:
// "the member runs it, the gate checks the result"): the program, then every POST line, then
// the step's commit. A program or a POST that fails is broken and commits nothing; a program
// that changed nothing while its POST holds is ok with no commit.
func RunScript(dir string, s Step, sys Sys) Result {
	r := Result{Num: s.Num, Verdict: Broken}
	if err := runProgram(dir, s, sys); err != nil {
		r.Words = "program: " + err.Error()
		return r
	}
	for _, p := range s.Post {
		if err := check(dir, p, sys); err != nil {
			r.Words = "post: " + err.Error()
			return r
		}
	}
	sha, err := sys.Commit(dir, s.Paths, s.Commit)
	if err != nil {
		r.Words = "commit: " + err.Error()
		return r
	}
	r.Verdict, r.Sha, r.Words = OK, sha, "post holds"
	if sha == "" {
		r.Words = "post holds; nothing changed"
	}
	return r
}

func runProgram(dir string, s Step, sys Sys) error {
	if s.Lang == "regex" {
		return applyRegex(dir, s)
	}
	work := filepath.Join(sys.Work, "step-"+s.Num)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	switch s.Lang {
	case "go":
		if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(s.Program), 0o644); err != nil {
			return err
		}
		// built where it is written (no module around it), run where the checkout is
		bin := filepath.Join(work, "step")
		if err := sys.Run(work, "go", "build", "-o", bin, "main.go"); err != nil {
			return fmt.Errorf("go build: %w", err)
		}
		return sys.Run(dir, bin)
	case "lisp":
		file := filepath.Join(work, "main.lisp")
		if err := os.WriteFile(file, []byte(s.Program), 0o644); err != nil {
			return err
		}
		return sys.Run(dir, "sbcl", "--script", file)
	}
	return fmt.Errorf("SCRIPT: %s is not one of %s", s.Lang, strings.Join(Langs, ", "))
}

// applyRegex is the regex program, in process: every edit over every file the step's PATHS
// globs match in the checkout.
func applyRegex(dir string, s Step) error {
	edits, err := ParseRegex(s.Program)
	if err != nil {
		return err
	}
	matched := 0
	for _, g := range s.Paths {
		files, err := filepath.Glob(filepath.Join(dir, g))
		if err != nil {
			return err
		}
		for _, f := range files {
			if fi, err := os.Stat(f); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			matched++
			out := b
			for _, e := range edits {
				out = e.RE.ReplaceAll(out, []byte(e.Repl))
			}
			if !bytes.Equal(out, b) {
				if err := os.WriteFile(f, out, 0o644); err != nil {
					return err
				}
			}
		}
	}
	if matched == 0 {
		return fmt.Errorf("PATHS: %s matches no file", strings.Join(s.Paths, ", "))
	}
	return nil
}

func check(dir string, p Post, sys Sys) error {
	switch p.Kind {
	case "sha256":
		b, err := os.ReadFile(filepath.Join(dir, p.Path))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != p.Sum {
			return fmt.Errorf("sha256 %s is %s, want %s", p.Path, got, p.Sum)
		}
		return nil
	case "exit0":
		if err := sys.Run(dir, p.Argv...); err != nil {
			return fmt.Errorf("exit0 %s: %w", strings.Join(p.Argv, " "), err)
		}
		return nil
	}
	return fmt.Errorf("POST: %s is neither sha256 nor exit0", p.Raw)
}

// Walk is the tree walked depth first: do runs each work step the walk visits, and the walk
// stops at the first step that is not ok, since the steps after it build on it.
func Walk(t Tree, do func(Step) Result) []Result {
	var out []Result
	for _, s := range t.Work() {
		r := do(s)
		out = append(out, r)
		if r.Verdict != OK {
			break
		}
	}
	return out
}

// Guide is what a tree card's child is told beside its brief (member.CardText): walk the
// work steps depth first, one commit per step, a step line each in the pull request body,
// stop at the first that is not ok; and run each script step through the machine.
func Guide(t Tree) string {
	if !t.IsTree() {
		return ""
	}
	var b strings.Builder
	b.WriteString("This card is a tree of steps. Walk its work steps (the steps with COMMIT:) depth first in card order")
	if t.From != "" {
		fmt.Fprintf(&b, ", from STEP %s (the steps before it have landed)", t.From)
	}
	b.WriteString(", one commit per step, its COMMIT: line the message, touching only that step's PATHS:. ")
	b.WriteString("The pull request body carries one line per work step: `step <n>: <ok|broken|not-done|skipped> <commit sha|-> <one line>`. ")
	b.WriteString("A step that is not ok ends the walk: say why on its line and finish with the steps before it committed; the member lands those and the rest becomes a new card.")
	if t.HasScript() {
		b.WriteString(" A script step (SCRIPT:) is the machine's: in its turn run `nova-step <n>`, which runs its program, checks its POST lines and commits; copy the line it prints after `STEP OK ` or `STEP FAILED ` into the body, and do nothing else for that step.")
	}
	return b.String() + "\n\n"
}
