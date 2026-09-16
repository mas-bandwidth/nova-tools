// Package edges is class J of pit stop 3 (#828): A TOOL FILES ITS OWN EDGES.
//
// Dogfood edges were filed by people, in prose, hours after the stumble and from memory:
// "nova-pulse wouldn't take the queue flag, I think". A person filing an edge has to notice
// it, remember it, and be willing to stop what they were doing -- three chances to lose it,
// and the estate lost most of them. The tool hitting the edge has none of those problems:
// it is there, it knows the verb, and it has the line verbatim.
//
// So every verb, at every refusal, on a timeout, and when its own output goes over its
// bound, appends ONE row:
//
//	<queue>/EDGES.tsv:  tool  verb  line  expected  state
//
// Deduplicated by (tool, verb, line). A refusal a bench hits four hundred times a day is
// one row, because the row is the EDGE and not the occurrence -- a counter would turn one
// bug into four hundred issues.
//
// `nova-check edges --queue <dir> --repo <o/n>` then turns the open rows into issues in the
// dogfood shape (tool and verb, the line verbatim, expected, the smallest fix) and marks
// each filed row filed, so the second run files nothing. The issue creator is an interface:
// the tests drive a fake and open no network.
//
// Recording is BEST EFFORT and never fails a caller. A verb that could not file its edge
// still has to make its refusal: the edge is a note about the tool, and a note about the
// tool is not worth the tool's own answer.
package edges

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// File is the name of the ledger inside a queue directory.
const File = "EDGES.tsv"

// Header names the five fields, so the file reads without this source beside it.
const Header = "# tool\tverb\tline\texpected\tstate\n"

// The two states a row is in. A row is open until an issue carries it.
const (
	StateOpen  = "open"
	StateFiled = "filed"
)

// lineMax is the ceiling on the verbatim line a row carries. An edge's line is a refusal or
// a bound, both of which are one line by rule; a longer one is a tool that pasted a file
// into its own error, and the row keeps its head and says how much it dropped.
const lineMax = 400

// Row is one edge: the tool and verb that refused, the line it printed verbatim, what the
// caller expected instead, and whether an issue carries it yet.
type Row struct {
	Tool     string
	Verb     string
	Line     string
	Expected string
	State    string
}

// key is what makes a row unique: the tool, the verb and the line. The expected text is the
// caller's reading of the same edge and is not part of its identity -- two callers wording
// the expectation differently have still hit one bug.
func (r Row) key() string { return r.Tool + "\x00" + r.Verb + "\x00" + r.Line }

// Record appends one edge to <queue>/EDGES.tsv, deduplicated by (tool, verb, line). A row
// already present -- open or filed -- writes nothing and is not an error: the second
// occurrence of an edge is the same edge.
//
// It returns nil when there is nothing to do, including an empty queue: a tool run outside a
// queue has nowhere to file, and refusing its own refusal to say so would help nobody.
//
// THE QUEUE DIRECTORY IS NEVER CREATED HERE. A refusal often names a path that is wrong --
// that is frequently the edge -- and a recorder that made the directory would answer a typo
// by writing a queue into somebody's source tree. Found by exactly that: a run_test case
// passing `--queue x` left a cmd/nova-pulse/x/EDGES.tsv behind in the checkout.
func Record(queue, tool, verb, line, expected string) error {
	if strings.TrimSpace(queue) == "" || strings.TrimSpace(tool) == "" || strings.TrimSpace(line) == "" {
		return nil
	}
	if info, err := os.Stat(queue); err != nil || !info.IsDir() {
		return nil
	}
	row := Row{
		Tool:     oneline.Field(tool),
		Verb:     oneline.Field(verb),
		Line:     oneline.Cap(oneline.Escape(strings.TrimSpace(line)), lineMax),
		Expected: oneline.Escape(strings.TrimSpace(expected)),
		State:    StateOpen,
	}
	rows, err := Read(queue)
	if err != nil {
		return err
	}
	for _, have := range rows {
		if have.key() == row.key() {
			return nil
		}
	}
	path := filepath.Join(queue, File)
	fresh := false
	if _, err := os.Stat(path); err != nil {
		fresh = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if fresh {
		if _, err := f.WriteString(Header); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\n", row.Tool, row.Verb, row.Line, row.Expected, row.State)
	return err
}

// Read reads the ledger. A missing file is no rows and not an error: a queue that has hit no
// edge has none, which is the state every queue starts in.
func Read(queue string) ([]Row, error) {
	raw, err := os.ReadFile(filepath.Join(queue, File))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []Row
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 3 {
			continue
		}
		r := Row{Tool: f[0], Verb: f[1], Line: f[2], State: StateOpen}
		if len(f) >= 4 {
			r.Expected = f[3]
		}
		if len(f) >= 5 && strings.TrimSpace(f[4]) != "" {
			r.State = strings.TrimSpace(f[4])
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// Write replaces the ledger with these rows. It is how a filing marks rows filed: the file
// is small by construction (one row per distinct edge) and rewriting it whole is what keeps
// the state of a row in one place rather than in an appended correction.
func Write(queue string, rows []Row) error {
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(Header)
	for _, r := range rows {
		state := r.State
		if state == "" {
			state = StateOpen
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", r.Tool, r.Verb, r.Line, r.Expected, state)
	}
	return os.WriteFile(filepath.Join(queue, File), []byte(b.String()), 0o644)
}

// Issue is one dogfood issue: the title and the body, already in the shape.
type Issue struct {
	Title string
	Body  string
}

// Creator opens an issue on a repo. It is an interface so `edges` can be tested with no
// network at all: the fake records what it was asked to open and opens nothing.
type Creator interface {
	Create(repo string, issue Issue) (ref string, err error)
}

// IssueOf renders one row in the dogfood shape SPEC-PULSE names: the tool and the verb, the
// line verbatim, what was expected, and the smallest fix. The smallest fix is derived from
// the expectation rather than invented: the smallest fix for a tool that did not do what was
// expected is to make it do that, and a guess at the patch would be this tool writing the
// card instead of reading the edge.
func IssueOf(r Row) Issue {
	verb := strings.TrimSpace(r.Verb)
	name := strings.TrimSpace(r.Tool)
	if verb != "" {
		name += " " + verb
	}
	expected := strings.TrimSpace(r.Expected)
	if expected == "" {
		expected = "the verb to accept this invocation, or to refuse it in a line that names the remedy"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TOOL: %s\n", strings.TrimSpace(r.Tool))
	fmt.Fprintf(&b, "VERB: %s\n", verb)
	fmt.Fprintf(&b, "LINE (verbatim):\n\n    %s\n\n", r.Line)
	fmt.Fprintf(&b, "EXPECTED: %s\n", expected)
	fmt.Fprintf(&b, "SMALLEST FIX: make `%s` %s, and lock it in with the red test that reproduces the line above.\n", name, expected)
	fmt.Fprintf(&b, "\nFiled by `nova-check edges` from %s; the tool that hit this edge wrote the row itself (pit stop 3, class J, #828).\n", File)
	return Issue{Title: fmt.Sprintf("dogfood: %s: %s", name, oneline.Cap(r.Line, 120)), Body: b.String()}
}

// ReportInput is the `nova-check edges` verb, apart from flag parsing.
type ReportInput struct {
	Queue   string
	Repo    string
	DryRun  bool
	Creator Creator
	Stdout  io.Writer
	Stderr  io.Writer
}

// Report opens one issue per distinct open row and marks those rows filed. It prints one
// EDGES line: counts, never a list, because a bench with four hundred edges should not need
// four hundred lines to learn it has them.
//
// --dry-run opens nothing and marks nothing, and prints the same counts, so the filing can
// be read before it is trusted.
func Report(in ReportInput) int {
	rows, err := Read(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "EDGES REFUSED: %s could not be read: %s (pass the queue directory the tools file into)\n",
			oneline.Field(filepath.Join(in.Queue, File)), oneline.Err(err))
		return 2
	}
	open, filed, failed := 0, 0, 0
	for i := range rows {
		if rows[i].State != StateOpen {
			continue
		}
		open++
		if in.DryRun {
			continue
		}
		ref, err := in.Creator.Create(in.Repo, IssueOf(rows[i]))
		if err != nil {
			failed++
			fmt.Fprintf(in.Stderr, "EDGES NOTE %s %s could not be filed: %s\n",
				oneline.Field(rows[i].Tool), oneline.Field(rows[i].Verb), oneline.Err(err))
			continue
		}
		filed++
		rows[i].State = StateFiled
		fmt.Fprintf(in.Stdout, "EDGE FILED tool=%s verb=%s ref=%s\n",
			oneline.Field(rows[i].Tool), oneline.Field(rows[i].Verb), oneline.Field(ref))
	}
	if filed > 0 {
		if err := Write(in.Queue, rows); err != nil {
			// The issues exist and the ledger does not know it: say so loudly, because the
			// next run would file them all a second time.
			fmt.Fprintf(in.Stderr, "EDGES REFUSED: %d issues were opened but %s could not be marked filed: %s (mark them by hand before the next run, or it files them again)\n",
				filed, oneline.Field(File), oneline.Err(err))
			return 2
		}
	}
	fmt.Fprintf(in.Stdout, "EDGES queue=%s rows=%d open=%d filed=%d failed=%d dry-run=%t\n",
		oneline.Field(in.Queue), len(rows), open, filed, failed, in.DryRun)
	if failed > 0 {
		return 1
	}
	return 0
}

// SortRows orders rows by tool, verb then line, so two runs over the same ledger file the
// same issues in the same order.
func SortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tool != rows[j].Tool {
			return rows[i].Tool < rows[j].Tool
		}
		if rows[i].Verb != rows[j].Verb {
			return rows[i].Verb < rows[j].Verb
		}
		return rows[i].Line < rows[j].Line
	})
}

// GH is the real creator: one bounded `gh issue create` per row. It is the only thing in
// this package that touches a network, and nothing in the tests reaches it.
type GH struct{ Timeout time.Duration }

func (g GH) Create(repo string, issue Issue) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "create", "--repo", repo,
		"--title", issue.Title, "--body-file", "-")
	cmd.Stdin = strings.NewReader(issue.Body)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh issue create on %s: %w", repo, err)
	}
	return strings.TrimSpace(string(out)), nil
}
