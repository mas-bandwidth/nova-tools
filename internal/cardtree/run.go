package cardtree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// step in the result's body, in walk order (docs/SPEC-CARD-CONTRACT.md section 3, the verdict
// per step). Sha is the step's own commit, "" when it made none.
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

var (
	verdictRE = regexp.MustCompile(`(?i)^[ \t*-]*step[ \t-]?([0-9]+(?:\.[0-9]+)*):[ \t]*(ok|broken|not-done|skipped)\b[ \t]*(\S*)[ \t]*(.*)$`)
	// stepShaRE is a step line's commit: a full sha or the twelve-digit short one, never a word
	stepShaRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{12})$`)
)

// ParseVerdicts reads every step line of a result's body, by step number; the first line for
// a step wins. A line whose commit is neither `-` nor a sha (40 or 12 hex) is a defect: the
// step is not-done, its words say why, so no word is ever pushed as a head.
func ParseVerdicts(body string) map[string]Result {
	out := map[string]Result{}
	for _, l := range strings.Split(body, "\n") {
		m := verdictRE.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			continue
		}
		if _, dup := out[m[1]]; dup {
			continue
		}
		r := Result{Num: m[1], Verdict: strings.ToLower(m[2]), Sha: strings.ToLower(m[3]), Words: strings.TrimSpace(m[4])}
		switch {
		case r.Sha == "-" || r.Sha == "":
			r.Sha = ""
		case !stepShaRE.MatchString(r.Sha):
			r = Result{Num: r.Num, Verdict: NotDone, Words: "the step line's commit " + m[3] + " is no sha (40 or 12 hex, or -): " + strings.TrimSpace(m[3]+" "+m[4])}
		}
		out[m[1]] = r
	}
	return out
}

// Land is where a tree card's result lands (docs/SPEC-SPRINT.md, a card is a tree of steps:
// the coordinator's failed-step rule of 2026-10-02): the first work step whose verdict is not
// ok (a step with no line is not-done), and the commit of the last ok step before it that made
// one. failed is nil when every step is ok; land is "" when no step before the failed one
// committed anything.
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

var (
	contractShaRE = regexp.MustCompile(`\bsha=[0-9A-Fa-f]+`)
	fullShaRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Remainder is the card a failed step leaves (docs/SPEC-SPRINT.md, a card is a tree of steps):
// the brief as it is, staged at land, the full commit steps 1..n-1 landed at, with
// `From: STEP <n>` (the walk starts there) and `Needs: <id>` (it is dealt after the card it
// continues). In the header: line 1's `sha=` becomes land's first twelve, `BASE: <ref>[@<sha>]`
// becomes `BASE: <ref>@<land>` and `base-sha:` becomes land (a card with neither gains a
// `base-sha:` line); an existing From: line is replaced, an existing Needs: line extended.
func Remainder(card, id, from, land string) (string, error) {
	if !fullShaRE.MatchString(land) {
		return "", fmt.Errorf("the land commit %q is no full sha (40 hex)", land)
	}
	lines := strings.Split(card, "\n")
	head := []string{contractShaRE.ReplaceAllString(lines[0], "sha="+land[:12])}
	rest := lines[1:]
	needs, pinned := false, false
	for i, l := range rest {
		if strings.TrimSpace(l) == "" {
			break // the header block ends at its first blank line
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "FROM":
			rest[i] = "From: STEP " + from
			from = ""
		case "NEEDS":
			if !needs {
				needs = true
				if v == "" || v == "-" || strings.EqualFold(v, "none") {
					rest[i] = "Needs: " + id
				} else {
					rest[i] = "Needs: " + v + ", " + id
				}
			}
		case "BASE":
			ref, _, _ := strings.Cut(v, "@")
			rest[i], pinned = "BASE: "+strings.TrimSpace(ref)+"@"+land, true
		case "BASE-SHA":
			rest[i], pinned = "base-sha: "+land, true
		}
	}
	if from != "" {
		head = append(head, "From: STEP "+from)
	}
	if !needs {
		head = append(head, "Needs: "+id)
	}
	if !pinned {
		head = append(head, "base-sha: "+land)
	}
	return strings.Join(append(head, rest...), "\n"), nil
}

// RemainderID is the id of the card the step n of card id leaves: `<id>-r<n>`, a dotted
// step's dots as dashes (`c1-r3-2`), so the id is one the sprint takes (sprint.ValidID).
func RemainderID(id, from string) string { return id + "-r" + strings.ReplaceAll(from, ".", "-") }

// Sys is what a script step needs of the machine (docs/SPEC-SPRINT.md, a card is a tree of
// steps): Build runs the toolchain over a program's source, never the card's code; Run runs
// the card's code (a built program, sbcl, an exit0 command) in the step's own wall; Commit
// stages the step's paths and commits them, in that wall too, and says the sha ("" when
// nothing changed). Work is where the programs are written and built. The executor's is
// OSSys; a test's is its own.
type Sys struct {
	Build  func(dir string, argv ...string) error
	Run    func(dir string, argv ...string) error
	Commit func(dir string, paths []string, message string) (sha string, err error)
	Work   string
}

// RunScript runs one script step in the checkout dir with no model: the program, then every
// POST line, then the step's commit (docs/SPEC-SPRINT.md, a card is a tree of steps). A
// program or a POST that fails is broken and commits nothing; a program that changed nothing
// while its POST holds is ok with no commit. A path that is not local to the checkout, and a
// POST command an interpreter would run, are refused here as the lint refuses them.
func RunScript(dir string, s Step, sys Sys) Result {
	r := Result{Num: s.Num, Verdict: Broken}
	if err := localPaths(s); err != nil {
		r.Words = err.Error()
		return r
	}
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

// localPaths refuses a step whose PATHS glob or POST path is not a relative path inside the
// checkout: absolute, or climbing with `..`.
func localPaths(s Step) error {
	for _, g := range s.Paths {
		if !filepath.IsLocal(g) {
			return fmt.Errorf("PATHS: %s is not a relative path inside the checkout", g)
		}
	}
	for _, p := range s.Post {
		if p.Kind == "sha256" && !filepath.IsLocal(p.Path) {
			return fmt.Errorf("POST: sha256 %s is not a relative path inside the checkout", p.Path)
		}
	}
	return nil
}

// RefusedCommand says why an exit0 command is refused, "" when it is not: its first word is
// an interpreter (or env, which runs one), the "never bash or python" rule a POST line could
// otherwise walk round.
func RefusedCommand(argv []string) string {
	if len(argv) == 0 {
		return "exit0 names no command"
	}
	if w := filepath.Base(argv[0]); slices.Contains(refused, w) || w == "env" {
		return "exit0 " + argv[0] + ": never bash or python, nor an interpreter run by another name; name the checking command itself (go vet, go test, a built tool)"
	}
	return ""
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
		// built where it is written (no module around it) by the toolchain, run in the
		// step's wall where the checkout is
		bin := filepath.Join(work, "step")
		if err := sys.Build(work, "go", "build", "-o", bin, "main.go"); err != nil {
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

// applyRegex is the regex program, in process: every edit over every regular file the step's
// PATHS globs match in the checkout, each read and written through the checkout's os.Root, so
// a link out of the checkout is refused, never followed.
func applyRegex(dir string, s Step) error {
	edits, err := ParseRegex(s.Program)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	matched := 0
	for _, g := range s.Paths {
		files, err := filepath.Glob(filepath.Join(dir, g))
		if err != nil {
			return err
		}
		for _, f := range files {
			rel, err := filepath.Rel(dir, f)
			if err != nil {
				return err
			}
			if fi, err := root.Lstat(rel); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			b, err := root.ReadFile(rel)
			if err != nil {
				return err
			}
			matched++
			out := b
			for _, e := range edits {
				out = e.RE.ReplaceAll(out, []byte(e.Repl))
			}
			if !bytes.Equal(out, b) {
				if err := root.WriteFile(rel, out, 0o644); err != nil {
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
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		defer root.Close()
		b, err := root.ReadFile(p.Path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != p.Sum {
			return fmt.Errorf("sha256 %s is %s, want %s", p.Path, got, p.Sum)
		}
		return nil
	case "exit0":
		if why := RefusedCommand(p.Argv); why != "" {
			return errors.New(why)
		}
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
// stop at the first that is not ok. A card with a script step never reaches a child: every
// work step of it is a script step (the lint), which the executor runs.
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
	b.WriteString("The pull request body carries one line per work step: `step <n>: <ok|broken|not-done|skipped> <commit sha|-> <one line>`, the sha the step's full commit or its first twelve. ")
	b.WriteString("A step that is not ok ends the walk: say why on its line and finish with the steps before it committed; the member lands those and the rest becomes a new card.")
	return b.String() + "\n\n"
}
