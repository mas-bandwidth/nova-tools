package cardtree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fence is the program fence, spelled ”' inside the raw strings below.
func fenced(s string) string { return strings.ReplaceAll(s, "'''", "```") }

const flatCard = `RESULT: flat-1 sha=0123456789ab
REPO: o/r
BASE: dev
Deadline: finish within 30 minutes.

THE TASK. Fix internal/x/x.go.

STEP 1. Enter your worktree with cd repo && git log --oneline -1.
STEP 2. Write the red test TestX in internal/x/x_test.go.
STEP 3. Run go test -count=1 ./internal/x/ and commit.
STEP 4. End as JOB.md says; write RESULT.md.
`

// upper is the script step's program: the generated file, upper-cased in place.
const upper = `package main

import (
	"bytes"
	"os"
)

func main() {
	b, err := os.ReadFile("gen/table.txt")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("gen/table.txt", bytes.ToUpper(b), 0o644); err != nil {
		panic(err)
	}
}
`

const fixture = "row one\nrow two\n"

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// treeCard is two model steps (2 and 3.2) and a Go script step (3.1) under a parent, with
// post the sha256 its POST line asserts.
func treeCard(post string) string {
	var prog strings.Builder
	for _, l := range strings.Split(strings.TrimRight(upper, "\n"), "\n") {
		prog.WriteString("  " + l + "\n")
	}
	return fenced(fmt.Sprintf(`RESULT: tree-1 sha=0123456789ab
REPO: o/r
BASE: dev
PATHS: a.txt, b.txt, gen/table.txt
Deadline: finish within 30 minutes.

THE TASK. Write a.txt, regenerate gen/table.txt, write b.txt.

STEP 1. Enter your worktree with cd repo && git log --oneline -1.
STEP 2. Write a.txt.
  PATHS: a.txt
  COMMIT: a: write a
  VERDICT: ok when a.txt says a
STEP 3. Regenerate the table.
STEP 3.1. Upper-case the generated file.
  PATHS: gen/table.txt
  COMMIT: gen: upper-case the table
  VERDICT: the post-condition holds
  SCRIPT: go
  '''go
%s  '''
  POST: sha256 gen/table.txt %s
STEP 3.2. Write b.txt.
  PATHS: b.txt
  COMMIT: b: write b
  VERDICT: ok when b.txt says b
STEP 4. End as JOB.md says; write RESULT.md.
`, prog.String(), post))
}

// ledger is an in-memory commit log: each commit its message and paths, its sha c<n>.
type ledger struct{ commits []string }

func (l *ledger) commit(_ string, paths []string, message string) (string, error) {
	l.commits = append(l.commits, message+" "+strings.Join(paths, ","))
	return fmt.Sprintf("c%d", len(l.commits)), nil
}

func TestAFlatCardLintsAndRunsAsBefore(t *testing.T) {
	t.Parallel()
	tr := Parse(flatCard)
	assert.False(t, tr.IsTree(), "a card with no dotted step and no COMMIT: line is flat")
	assert.Empty(t, tr.Work())
	assert.Nil(t, Lint(flatCard), "a flat card has no tree finding")
	assert.Empty(t, Guide(tr), "a flat card's child is told nothing new")
}

func TestATreeCardParsesItsStepsDepthFirst(t *testing.T) {
	t.Parallel()
	tr := Parse(treeCard(sum(strings.ToUpper(fixture))))
	require.True(t, tr.IsTree())
	var nums []string
	for _, s := range tr.Work() {
		nums = append(nums, s.Num)
	}
	assert.Equal(t, []string{"2", "3.1", "3.2"}, nums, "the work steps, depth first in card order")
	s, ok := tr.Step("3.1")
	require.True(t, ok)
	assert.True(t, s.Script())
	assert.Equal(t, upper, s.Program, "the program is the fenced block, dedented")
	assert.Equal(t, []string{"gen/table.txt"}, s.Paths)
	assert.Equal(t, []string{"a.txt", "b.txt", "gen/table.txt"}, tr.Paths)
	assert.False(t, tr.AllScript())
	assert.Nil(t, Lint(treeCard(sum(fixture))), "the tree card lints clean")
	assert.Contains(t, Guide(tr), "nova-step <n>", "a child with a script step is told to run it through the machine")
}

func TestATreeCardRunsItsScriptStepByTheMember(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, post, verdict string
		commits             []string
	}{
		{"the hash holds", sum(strings.ToUpper(fixture)), OK, []string{"a: write a a.txt", "gen: upper-case the table gen/table.txt", "b: write b b.txt"}},
		{"the hash differs", sum(fixture), Broken, []string{"a: write a a.txt"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "gen", "table.txt"), []byte(fixture), 0o644))
			l := &ledger{}
			sys := OSSys(t.TempDir())
			sys.Commit = l.commit
			var model []string
			tr := Parse(treeCard(c.post))
			got := Walk(tr, func(s Step) Result {
				if s.Script() {
					return RunScript(dir, s, sys)
				}
				model = append(model, s.Num) // the model's step: its file, its commit
				require.NoError(t, os.WriteFile(filepath.Join(dir, s.Paths[0]), []byte(s.Num), 0o644))
				sha, err := l.commit(dir, s.Paths, s.Commit)
				require.NoError(t, err)
				return Result{Num: s.Num, Verdict: OK, Sha: sha}
			})
			assert.Equal(t, c.commits, l.commits, "one commit per ok step, the script step's by the member")
			require.Len(t, got, len(c.commits)+map[bool]int{true: 0, false: 1}[c.verdict == OK])
			assert.Equal(t, c.verdict, got[1].Verdict, got[1].Line())
			b, err := os.ReadFile(filepath.Join(dir, "gen", "table.txt"))
			require.NoError(t, err)
			assert.Equal(t, strings.ToUpper(fixture), string(b), "the program ran in the checkout")
			if c.verdict == OK {
				assert.Equal(t, []string{"2", "3.2"}, model, "the model never ran the script step")
				assert.Equal(t, "c2", got[1].Sha)
			} else {
				assert.Equal(t, []string{"2"}, model, "the walk stops at the broken step")
				assert.Contains(t, got[1].Words, "sha256 gen/table.txt is "+sum(strings.ToUpper(fixture)))
			}
		})
	}
}

func TestARegexStepRunsInProcess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x := Foo()\ny := Foo()\n"), 0o644))
	want := "x := Bar()\ny := Bar()\n"
	s := Step{Num: "2", Paths: []string{"*.go"}, Commit: "rename", Lang: "regex", Program: "s/Foo\\(\\)/Bar()/\n",
		Post: []Post{{Kind: "sha256", Path: "a.go", Sum: sum(want)}}}
	l := &ledger{}
	r := RunScript(dir, s, Sys{Commit: l.commit, Run: func(string, ...string) error { t.Fatal("a regex runs no program"); return nil }})
	assert.Equal(t, OK, r.Verdict, r.Line())
	b, err := os.ReadFile(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.Equal(t, want, string(b))
	assert.Equal(t, []string{"rename *.go"}, l.commits)
}

func TestAFailedStepTwoOfThreeLandsStepOneAndWritesTheRemainder(t *testing.T) {
	t.Parallel()
	card := `RESULT: s1-4 sha=0123456789ab
REPO: o/r
Needs: s1-1

STEP 1. Enter your worktree with cd repo.
STEP 2. One.
  PATHS: a.go
  COMMIT: one
  VERDICT: ok
STEP 3. Two.
  PATHS: b.go
  COMMIT: two
  VERDICT: ok
STEP 4. Three.
  PATHS: c.go
  COMMIT: three
  VERDICT: ok
STEP 5. End as JOB.md says; write RESULT.md.
`
	tr := Parse(card)
	sha := strings.Repeat("a", 40)
	failed, land := Land(tr, ParseVerdicts("head: x\n## Body\n- step 2: ok "+sha+" one done\nstep 3: broken - TestB is red\n"))
	require.NotNil(t, failed)
	assert.Equal(t, Result{Num: "3", Verdict: Broken, Words: "TestB is red"}, *failed)
	assert.Equal(t, sha, land, "steps before the failed one land at the last ok step's commit")

	rem := Remainder(card, "s1-4", "3")
	assert.Equal(t, "s1-4-r3", RemainderID("s1-4", "3"))
	assert.True(t, strings.HasPrefix(rem, "RESULT: s1-4 sha=0123456789ab\nFrom: STEP 3\nREPO: o/r\nNeeds: s1-1, s1-4\n"), rem)
	rt := Parse(rem)
	var nums []string
	for _, s := range rt.Work() {
		nums = append(nums, s.Num)
	}
	assert.Equal(t, []string{"3", "4"}, nums, "the remainder walks from the failed step")
	assert.Nil(t, Lint(rem))
	assert.Contains(t, Guide(rt), "from STEP 3")

	none, _ := Land(tr, ParseVerdicts("step 2: ok "+sha+"\nstep 3: ok -\nstep 4: ok "+sha))
	assert.Nil(t, none, "every step ok: nothing failed")
	first, at := Land(tr, ParseVerdicts("step 2: not-done -"))
	require.NotNil(t, first)
	assert.Equal(t, "2", first.Num)
	assert.Empty(t, at, "a failed first step lands nothing")
}

func TestTheTreeLintNamesEachDefect(t *testing.T) {
	t.Parallel()
	head := "RESULT: t sha=0123456789ab\nPATHS: a.go\n\nSTEP 1. Enter with cd repo.\n"
	for _, c := range []struct{ name, steps, check, excerpt string }{
		{"a child with no parent", "STEP 2.1. x\n", CheckNested, "no STEP 2 above it"},
		{"a gap among children", "STEP 2. x\nSTEP 2.1. y\nSTEP 2.3. z\n", CheckNested, "after 2.1"},
		{"a work step with no verdict", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n", CheckStep, "has no VERDICT:"},
		{"a glob outside the card's PATHS", "STEP 2. x\n  PATHS: b.go\n  COMMIT: c\n  VERDICT: v\n", CheckStep, "b.go is not in the card's PATHS:"},
		{"fields with no commit", "STEP 2. x\n  PATHS: a.go\n", CheckStep, "carries PATHS: and no COMMIT:"},
		{"a bash script", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: bash\n", CheckScript, "never bash or python"},
		{"no program", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: go\n  POST: exit0 go vet ./...\n", CheckScript, "no fenced program"},
		{"no post", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n", CheckScript, "no POST:"},
		{"a bad post", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: regex\n  '''\n  s/a/b/\n  '''\n  POST: sha256 a.go nothex\n", CheckScript, "is neither"},
		{"a regex that does not compile", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: regex\n  '''regex\n  s/(/b/\n  '''\n  POST: exit0 go vet ./...\n", CheckScript, "regex line 1"},
		{"a fence of another language", "STEP 2. x\n  PATHS: a.go\n  COMMIT: c\n  VERDICT: v\n  SCRIPT: go\n  '''lisp\n  (print 1)\n  '''\n  POST: exit0 go vet ./...\n", CheckScript, "fence says lisp"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fs := Lint(fenced(head + c.steps))
			require.Len(t, fs, 1, "%v", fs)
			assert.Equal(t, c.check, fs[0].Check)
			assert.Contains(t, fs[0].Excerpt, c.excerpt)
		})
	}
}
