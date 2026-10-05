package sprint

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// A red check after a promotion cuts its own fix cards (docs/SPEC-SPRINT.md section 11,
// promote; the owner, 2026-10-05: "every step the coordinator did by hand today is a missing
// instruction"). The coordinator read failing CI logs by hand and wrote one fix card per
// failing test into a promote-red stream; this is that instruction. ParseFailedLog reads the
// log `gh run view <id> --log-failed` prints and finds each failing Go test, its package, its
// test file, the first line it failed on and the runners it failed on. CIFix admits one card
// per failing test that no open card already names, heavy tier, ranked in front of every card
// on the table. Nothing here reads a store, a clock or a forge.

// CIFixTier is the tier a fix card is cut on: a red test on the development branch is
// heavy work, whatever the card that broke it was.
const CIFixTier = "heavy"

// CIFixPrefix is the prefix of a fix card's id: red-<the test's name>.
const CIFixPrefix = "red-"

// CIFailure is one failing test, as many runners as it failed on, once.
type CIFailure struct {
	Test    string   // the top-level test, TestX (a failing subtest is its parent's failure)
	Package string   // the package's import path, from its FAIL line; "" when the log names none
	Dir     string   // the package's directory in the repository, the module path cut off
	File    string   // the test file, as the failing line names it (wall_test.go)
	Line    string   // the first line the test failed on, file:line: message
	Runners []string // the jobs it failed on, in the order the log names them
}

// CIFixID is the id of the fix card for a test.
func CIFixID(test string) string {
	id := CIFixPrefix + test
	if len(id) > MaxIDLen {
		id = id[:MaxIDLen]
	}
	return id
}

var (
	// a gh --log-failed line: job, step, then a timestamped line of the step's output (the
	// stamp tells it from go test's own FAIL<tab>package<tab>time line)
	ghLogLine = regexp.MustCompile(`^([^\t]*)\t([^\t]*)\t(\x{FEFF}?\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d.*)$`)
	ghStamp   = regexp.MustCompile(`^\x{FEFF}?\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)
	failHead  = regexp.MustCompile(`^\s*--- FAIL: (Test[A-Za-z0-9_]*)(/\S*)? \(`)
	failAt    = regexp.MustCompile(`^\s+([A-Za-z0-9_.-]+_test\.go):(\d+):\s?(.*)$`)
	errorText = regexp.MustCompile(`^\s+Error:\s+(.*)$`)
	pkgLine   = regexp.MustCompile(`^(FAIL|ok)\s+(\S+)(\s|$)`)
)

// ParseFailedLog is every failing test of a failed run's log, in the order the log first
// names each, deduplicated by test name across jobs and shards. module is the repository's
// module path (go.mod), cut off a package's import path to give its directory. A failed
// check with no `--- FAIL:` line (a build, a vet, a gate that is not a test) names no test.
func ParseFailedLog(log, module string) []CIFailure {
	var out []CIFailure
	at := map[string]int{} // test -> its index in out
	// pending is each job's failing tests not yet given their package: go test prints a
	// package's failing tests before its FAIL line
	pending := map[string][]int{}
	// open is each job's test whose first failing line is still to be read
	open := map[string]int{}
	for _, raw := range strings.Split(log, "\n") {
		raw = strings.TrimRight(raw, "\r")
		job, text := "", raw
		if m := ghLogLine.FindStringSubmatch(raw); m != nil {
			job, text = m[1], m[3]
		}
		text = ghStamp.ReplaceAllString(text, "")
		if m := failHead.FindStringSubmatch(text); m != nil {
			test := m[1]
			i, seen := at[test]
			if !seen {
				i = len(out)
				at[test] = i
				out = append(out, CIFailure{Test: test})
			}
			if job != "" && !slices.Contains(out[i].Runners, job) {
				out[i].Runners = append(out[i].Runners, job)
			}
			if out[i].Package == "" && !slices.Contains(pending[job], i) {
				pending[job] = append(pending[job], i)
			}
			open[job] = i
			continue
		}
		if m := pkgLine.FindStringSubmatch(text); m != nil {
			for _, i := range pending[job] {
				if out[i].Package == "" && m[1] == "FAIL" {
					out[i].Package = m[2]
					out[i].Dir = pkgDir(m[2], module)
				}
			}
			delete(pending, job)
			delete(open, job)
			continue
		}
		i, ok := open[job]
		if !ok {
			continue
		}
		f := &out[i]
		if m := failAt.FindStringSubmatch(text); m != nil && f.Line == "" {
			f.File = m[1]
			f.Line = m[1] + ":" + m[2] + ":"
			if msg := strings.TrimSpace(m[3]); msg != "" {
				f.Line += " " + msg
			}
			continue
		}
		if m := errorText.FindStringSubmatch(text); m != nil && f.Line != "" && !strings.Contains(f.Line, ": ") {
			f.Line += " " + strings.TrimSpace(m[1])
		}
	}
	return out
}

// pkgDir is a package's directory in its repository: the import path with the module path
// cut off, "." for the module's root package.
func pkgDir(pkg, module string) string {
	if module == "" {
		return pkg
	}
	if pkg == module {
		return "."
	}
	return strings.TrimPrefix(pkg, module+"/")
}

// CIFixReq is what a red check cuts: the stream its cards go into, the repository and the
// branch a fix is cut on, the run the log came from, the failing tests, and the RULES
// paragraph every card quotes (the sprint's child rules).
type CIFixReq struct {
	Stream string
	Repo   string // owner/name
	Base   string // the live sprint branch: a fix lands there and reaches dev by promotion
	Source string // what went red: "the pull request #42 of promo/2026-10-05-1", "dev at <sha>"
	Rules  string
	// Held is the held rules file the member injects into each card (CardAdd.Rules), "" when
	// the brief's own RULES paragraph is the whole of its rules.
	Held     string
	Failures []CIFailure
	Who      string
}

// CIFixBrief is the brief of a test's fix card, in the shape the coordinator wrote them by
// hand: START the test and its failing line, STOP the test green on that runner, PATHS the
// test file and the package, TEST the test.
func CIFixBrief(r CIFixReq, f CIFailure) string {
	dir := f.Dir
	if dir == "" {
		dir = "."
	}
	pkg := "./" + strings.TrimPrefix(dir, "./")
	if dir == "." {
		pkg = "."
	}
	runners := "the runner that ran it"
	if len(f.Runners) > 0 {
		runners = strings.Join(f.Runners, ", ")
	}
	line := f.Line
	if line == "" {
		line = "the log names no file line; read the failing job's log"
	}
	paths := path.Join(dir, "*.go")
	if f.File != "" {
		paths = path.Join(dir, f.File) + "," + paths
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tier: %s\n", CIFixTier)
	fmt.Fprintf(&b, "REPO: %s\n", r.Repo)
	fmt.Fprintf(&b, "BASE: %s\n", r.Base)
	fmt.Fprintf(&b, "START: %s fails on %s (%s): %s\n", f.Test, runners, r.Source, line)
	fmt.Fprintf(&b, "STOP: %s passes on %s, with its package green\n", f.Test, runners)
	fmt.Fprintf(&b, "PATHS: %s\n", paths)
	fmt.Fprintf(&b, "TEST: %s %s\n", pkg, f.Test)
	b.WriteString("You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.\n\n")
	b.WriteString(strings.TrimRight(r.Rules, "\n"))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "THE TASK. %s went red: %s failed on %s, and the machine cut this card from the run's failed log. The failing line: %s. Find why it fails there and fix what is wrong, the code or the test; a fix that skips the test, or loosens what it asserts, is not a fix.\n\n", r.Source, f.Test, runners, line)
	b.WriteString("STEP 1. Enter the worktree your job names and run git log --oneline -1.\n")
	fmt.Fprintf(&b, "STEP 2. Run the test and see it fail as the log shows: go test -count=1 -run '^%s$' %s\n", f.Test, pkg)
	b.WriteString("STEP 3. Make it pass; run it three times with -shuffle=on.\n")
	fmt.Fprintf(&b, "STEP 4. Run the gate: go test -count=1 %s, and read its last line.\n", pkg)
	b.WriteString("STEP 5. Commit on your own branch with the trailer.\n")
	b.WriteString("STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): the gate's lines in the report.\n")
	return b.String()
}

// CIFixOpen is every test an open card names, to the card: a card on the table and not
// landed whose id is a fix card's (red-<test>) or whose brief has a TEST: line naming the
// test, the machine's cards and the ones written by hand alike.
func CIFixOpen(s *Snapshot) map[string]string {
	out := map[string]string{}
	for _, c := range s.Work.Cards() {
		if !c.Placed() || c.Col == Landed || IsSentinel(c) {
			continue
		}
		if t, ok := strings.CutPrefix(c.ID, CIFixPrefix); ok && t != "" {
			out[t] = c.ID
		}
		for _, l := range strings.Split(c.F("brief"), "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(l), "TEST:")
			if !ok {
				continue
			}
			for _, w := range strings.Fields(v) {
				if strings.HasPrefix(w, "Test") {
					if _, had := out[w]; !had {
						out[w] = c.ID
					}
				}
			}
		}
	}
	return out
}

// CIFixCut is the failures a red check cuts a card for, and the open card each other one is
// already on (test -> card), in the order of r.Failures.
func CIFixCut(s *Snapshot, r CIFixReq) (cut []CIFailure, open map[string]string) {
	named := CIFixOpen(s)
	open = map[string]string{}
	for _, f := range r.Failures {
		if id, ok := named[f.Test]; ok {
			open[f.Test] = id
			continue
		}
		cut = append(cut, f)
		named[f.Test] = CIFixID(f.Test)
	}
	return cut, open
}

// CIFixScore is the score of the first of n cards ranked in front of every card on the
// work table: one below the lowest, less n.
func CIFixScore(s *Snapshot, n int) float64 {
	low, found := 0.0, false
	for _, c := range s.Work.Cards() {
		if !c.Placed() {
			continue
		}
		if !found || c.Score < low {
			low, found = c.Score, true
		}
	}
	if !found {
		return 1
	}
	return low - float64(n) - 1
}

// CIFix admits the fix cards of a red check: one per failing test no open card names,
// heavy, in front of every card on the table, into r.Stream. A plan with no card says why.
func CIFix(s *Snapshot, r CIFixReq) Plan {
	cut, open := CIFixCut(s, r)
	var said []string
	for _, f := range r.Failures {
		if id, ok := open[f.Test]; ok {
			said = append(said, fmt.Sprintf("%s failed again, and %s is open on it: no card cut", f.Test, id))
		}
	}
	if len(cut) == 0 {
		return Plan{Said: said}
	}
	cards := CIFixCards(s, r)
	score := CIFixScore(s, len(cards))
	p := Add(s, AddReq{Stream: r.Stream, Cards: cards, Score: &score, Who: r.Who})
	p.Said = append(p.Said, said...)
	return p
}

// CIFixCards is the cards CIFix would admit on s, with their briefs: for the card lint, which
// holds them before the step writes.
func CIFixCards(s *Snapshot, r CIFixReq) []CardAdd {
	cut, _ := CIFixCut(s, r)
	out := make([]CardAdd, 0, len(cut))
	for _, f := range cut {
		out = append(out, CardAdd{ID: CIFixID(f.Test), Brief: CIFixBrief(r, f), Rules: r.Held, Base: r.Base})
	}
	return out
}
