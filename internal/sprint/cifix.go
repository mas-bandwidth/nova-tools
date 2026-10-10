package sprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// RedTest is one failing test read from a CI run's failed log (RedTests): its
// name, its package as go test printed it, the job (the runner) it failed on,
// its test file and its failing line, and the run (its caller's, not the log's).
type RedTest struct {
	Test, Pkg, Job, File, Line, Run string
}

// RedStreamPrefix is the stream a red promotion cuts its fix cards into, with the
// day: promote-red-<YYYY-MM-DD> (docs/SPEC-SPRINT.md section 11, promote).
const RedStreamPrefix = "promote-red-"

var (
	// ghStamp is the timestamp gh puts in front of each line of a job's log.
	ghStamp = regexp.MustCompile(`^\x{feff}?\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)
	// testFile is a failing line's file and line, as testing prints it.
	testFile = regexp.MustCompile(`([A-Za-z0-9_.-]+_test\.go):\d+`)
)

// RedTests reads `gh run view --log-failed` into its failing tests, one per
// distinct test name, in the order the log names them: each line is
// `<job>\t<step>\t<time> <text>`, each job's text is go test's output
// (decide.ParseGateOutput), and a test failing on two jobs is the first job's.
// A package that failed with no test named (a build failure) is no test, and
// a log with no go test output has none.
func RedTests(log string) []RedTest {
	var jobs []string
	byJob := map[string]*strings.Builder{}
	for _, l := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		job, text := "", l
		if parts := strings.SplitN(l, "\t", 3); len(parts) == 3 {
			job, text = parts[0], parts[2]
		}
		text = ghStamp.ReplaceAllString(text, "")
		b, ok := byJob[job]
		if !ok {
			b = &strings.Builder{}
			byJob[job] = b
			jobs = append(jobs, job)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	var out []RedTest
	for _, job := range jobs {
		for _, f := range decide.ParseGateOutput(byJob[job].String()) {
			if f.Test == "" || slices.ContainsFunc(out, func(r RedTest) bool { return r.Test == f.Test }) {
				continue
			}
			r := RedTest{Test: f.Test, Pkg: f.Pkg, Job: job}
			for _, line := range f.Lines {
				if m := testFile.FindStringSubmatch(line); m != nil {
					r.File, r.Line = m[1], line
					break
				}
			}
			if r.Line == "" && len(f.Lines) > 0 {
				r.Line = f.Lines[0]
			}
			out = append(out, r)
		}
	}
	return out
}

// FixSpec is what every fix card of one red run shares: the repository, the
// branch the fix starts from (the sprint branch), the module the log's
// package paths are under, the stream, and where the run was red.
type FixSpec struct {
	Repo, Base, Module, Stream, Where string
}

// FixCardID is the fix card of a failing test: red-<TestName>.
func FixCardID(test string) string {
	id := "red-" + test
	if len(id) > MaxIDLen {
		id = id[:MaxIDLen]
	}
	return id
}

// RedDir is the package's directory in the repository, from the path go test
// printed: the module's prefix cut, "." for the module's root.
func RedDir(pkg, module string) string {
	switch {
	case module != "" && pkg == module:
		return "."
	case module != "" && strings.HasPrefix(pkg, module+"/"):
		return strings.TrimPrefix(pkg, module+"/")
	}
	return strings.TrimPrefix(pkg, "./")
}

// FixBrief is the fix card's brief for one failing test, the card's own text
// (the rules paragraph is the caller's): line 1 its id and the heavy tier,
// REPO and BASE, START the test and its failing line from the log, STOP the
// test green on the runner it failed on, PATHS the test file and the package,
// TEST the test. A job named functional runs the test under -tags functional.
func FixBrief(r RedTest, sp FixSpec) string {
	id := FixCardID(r.Test)
	dir := RedDir(r.Pkg, sp.Module)
	pkg := "./" + dir
	if dir == "." {
		pkg = "."
	}
	paths := inDir(dir, "*.go")
	file := dir
	if r.File != "" {
		file = inDir(dir, r.File)
		paths = file + "," + paths
	}
	tags := ""
	if strings.Contains(strings.ToLower(r.Job), "functional") {
		tags = "-tags functional "
	}
	job := r.Job
	if job == "" {
		job = "the runner"
	}
	line := r.Line
	if line == "" {
		line = "(the log names no line)"
	}
	return strings.Join([]string{
		fmt.Sprintf("%s: make %s green on %s (%s) tier: heavy", id, r.Test, job, sp.Stream),
		"REPO: " + sp.Repo,
		"BASE: " + sp.Base,
		fmt.Sprintf("START: %s in %s, red on %s in %s (run %s): %s", r.Test, file, job, sp.Where, r.Run, line),
		fmt.Sprintf("STOP: %s green on %s, the runner it failed on, and the package's tests green", r.Test, job),
		"PATHS: " + paths,
		fmt.Sprintf("TEST: %s%s %s", tags, pkg, r.Test),
		"Needs: none",
		"Libraries considered: none new; the tree's own code and testify.",
		"",
		fmt.Sprintf("THE TASK. A promotion's CI was red: %s failed on %s in %s (run %s; read it with gh run view %s --log-failed). The lander's tree gate never runs the hosted shards or the functional tier, so this reached the promotion. Find why it fails on that runner and fix the cause, in the test or in the code it tests; never skip it, never loosen what it asserts.", r.Test, job, sp.Where, r.Run, r.Run),
		"",
		"STEP 1. Enter the staged checkout JOB.md names and run git log --oneline -1.",
		fmt.Sprintf("STEP 2. Reproduce the failure: go test -count=1 -run '^%s$' %s%s, as that runner runs it.", r.Test, tags, pkg),
		"STEP 3. Fix the cause; run the test three times with -shuffle=on.",
		fmt.Sprintf("STEP 4. Run the gate: go test -count=1 %s%s, and read its last line.", tags, pkg),
		"STEP 5. Commit on your own branch with the trailer.",
		"STEP 6. End as JOB.md says: the gate's lines in the report.",
	}, "\n")
}

func inDir(dir, file string) string {
	if dir == "." || dir == "" {
		return file
	}
	return dir + "/" + file
}

// RedTestOf is the test a card's brief names on its TEST line, "" for none.
func RedTestOf(brief string) string {
	for line := range strings.SplitSeq(brief, "\n") {
		if k, v, ok := cardhdr.KeyValue(line); ok && k == "TEST" {
			tl, why := cardhdr.ParseTest(v)
			if why != "" || tl.None {
				return ""
			}
			return tl.Name
		}
	}
	return ""
}

// FixCards is the add of a red run's fix cards against the table: a card whose
// test an open card already names (its TEST line, or its id) is left out and
// named in dup, an id a landed card holds takes the next free -<n>, and the
// cards are ranked first, every one scored below every card on the work
// table. An add with no cards left is r with no cards.
func FixCards(s *Snapshot, r AddReq) (add AddReq, dup []string) {
	open := map[string]bool{}
	taken := map[string]bool{}
	low, seen := 0.0, false
	for _, c := range s.Work.Cards() {
		taken[c.ID] = true
		if !c.Placed() {
			continue
		}
		if !seen || c.Score < low {
			low, seen = c.Score, true
		}
		if c.Col == Landed || IsSentinel(c) {
			continue
		}
		open[c.ID] = true
		if t := RedTestOf(c.F("brief")); t != "" {
			open[FixCardID(t)] = true
		}
	}
	add = r
	add.Cards = nil
	for _, c := range r.Cards {
		test := RedTestOf(c.Brief)
		if open[FixCardID(test)] || open[c.ID] {
			dup = append(dup, test)
			continue
		}
		id := c.ID
		for n := 2; taken[id]; n++ {
			id = fmt.Sprintf("%s-%d", c.ID, n)
		}
		taken[id] = true
		open[FixCardID(test)] = true
		if id != c.ID {
			c.Brief = strings.Replace(c.Brief, c.ID+":", id+":", 1)
			c.ID, c.File = id, id
		}
		add.Cards = append(add.Cards, c)
	}
	if seen && len(add.Cards) > 0 {
		score := low - float64(len(add.Cards))
		add.Score = &score
	}
	return add, dup
}
