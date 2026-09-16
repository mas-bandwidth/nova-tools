package edges

// Class J's red tests (#828): a forced refusal writes ONE row, a second identical refusal
// writes none, and `edges` opens one issue per distinct row and marks those rows filed.
// The issue creator is a fake and nothing here opens a network connection.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCreator is the whole network this package's tests have.
type fakeCreator struct {
	opened []Issue
	repos  []string
	fail   bool
}

func (f *fakeCreator) Create(repo string, issue Issue) (string, error) {
	if f.fail {
		return "", os.ErrPermission
	}
	f.opened = append(f.opened, issue)
	f.repos = append(f.repos, repo)
	return "https://example.invalid/issues/" + itoa(len(f.opened)), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestRecordIsDeduplicatedByLine is the rule that keeps four hundred occurrences of one edge
// from becoming four hundred issues.
func TestRecordIsDeduplicatedByLine(t *testing.T) {
	queue := t.TempDir()
	line := "nova-pulse reap: flag provided but not defined: -queu"
	if err := Record(queue, "nova-pulse", "reap", line, "the flag to exist"); err != nil {
		t.Fatal(err)
	}
	rows, err := Read(queue)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one refusal wrote %d rows, want 1", len(rows))
	}
	if rows[0].Tool != "nova-pulse" || rows[0].Verb != "reap" || rows[0].State != StateOpen {
		t.Errorf("the row is %+v", rows[0])
	}
	if !strings.Contains(rows[0].Line, "not defined") {
		t.Errorf("the row does not carry the line verbatim: %q", rows[0].Line)
	}

	// The SECOND identical refusal writes nothing.
	if err := Record(queue, "nova-pulse", "reap", line, "a differently worded expectation"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := Read(queue); len(rows) != 1 {
		t.Fatalf("a second identical refusal wrote a second row (%d rows)", len(rows))
	}

	// A DIFFERENT line is a different edge.
	if err := Record(queue, "nova-pulse", "reap", "nova-pulse reap: --queue is required", "the verb to say what it wants"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := Read(queue); len(rows) != 2 {
		t.Fatalf("a different refusal did not write its own row (%d rows)", len(rows))
	}
}

// TestRecordOutsideAQueueFilesNothing: a tool run outside a queue has nowhere to file, and
// inventing a directory would be a worse bug than the one being recorded.
func TestRecordOutsideAQueueFilesNothing(t *testing.T) {
	if err := Record("", "nova-pulse", "reap", "a line", "something"); err != nil {
		t.Fatalf("recording outside a queue is an error: %v", err)
	}
	queue := t.TempDir()
	if err := Record(queue, "nova-pulse", "reap", "   ", "something"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := Read(queue); len(rows) != 0 {
		t.Errorf("an empty line wrote %d rows", len(rows))
	}
}

// TestReportOpensOneIssuePerDistinctRow is the second half of class J.
func TestReportOpensOneIssuePerDistinctRow(t *testing.T) {
	queue := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Record(queue, "nova-pulse", "reap", "nova-pulse reap: --queue is required", "the verb to say what it wants"))
	must(Record(queue, "nova-pulse", "reap", "nova-pulse reap: --queue is required", "the verb to say what it wants"))
	must(Record(queue, "nova-bus", "wake", "nova-bus wake: --as is required", "a default of the bench's own name"))

	fake := &fakeCreator{}
	var out, errs strings.Builder
	code := Report(ReportInput{Queue: queue, Repo: "mas-bandwidth/nova-tools", Creator: fake, Stdout: &out, Stderr: &errs})
	if code != 0 {
		t.Fatalf("Report = %d: %s", code, errs.String())
	}
	if len(fake.opened) != 2 {
		t.Fatalf("opened %d issues, want one per distinct row (2)", len(fake.opened))
	}
	if fake.repos[0] != "mas-bandwidth/nova-tools" {
		t.Errorf("the issue went to %q", fake.repos[0])
	}
	// The dogfood shape: tool and verb, the line verbatim, expected, the smallest fix.
	body := fake.opened[0].Body
	for _, want := range []string{"TOOL: nova-pulse", "VERB: reap", "--queue is required", "EXPECTED:", "SMALLEST FIX:"} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue body has no %q:\n%s", want, body)
		}
	}
	if !strings.Contains(fake.opened[0].Title, "nova-pulse reap") {
		t.Errorf("the issue title does not name the tool and verb: %q", fake.opened[0].Title)
	}
	if !strings.Contains(out.String(), "rows=2") || !strings.Contains(out.String(), "filed=2") {
		t.Errorf("the EDGES line does not carry the counts:\n%s", out.String())
	}

	// THE ROWS ARE MARKED FILED, so the second run opens nothing.
	rows, err := Read(queue)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.State != StateFiled {
			t.Errorf("row %+v was not marked filed", r)
		}
	}
	second := &fakeCreator{}
	var out2, errs2 strings.Builder
	if code := Report(ReportInput{Queue: queue, Repo: "mas-bandwidth/nova-tools", Creator: second, Stdout: &out2, Stderr: &errs2}); code != 0 {
		t.Fatalf("the second Report = %d: %s", code, errs2.String())
	}
	if len(second.opened) != 0 {
		t.Errorf("the second run opened %d issues; every row was already filed", len(second.opened))
	}
}

// TestReportDryRunOpensNothing: the filing can be read before it is trusted.
func TestReportDryRunOpensNothing(t *testing.T) {
	queue := t.TempDir()
	if err := Record(queue, "nova-pulse", "cut", "nova-pulse cut: --out is required", "a default of <queue>/pending"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCreator{}
	var out, errs strings.Builder
	if code := Report(ReportInput{Queue: queue, Repo: "o/n", DryRun: true, Creator: fake, Stdout: &out, Stderr: &errs}); code != 0 {
		t.Fatalf("Report = %d: %s", code, errs.String())
	}
	if len(fake.opened) != 0 {
		t.Errorf("--dry-run opened %d issues", len(fake.opened))
	}
	if !strings.Contains(out.String(), "open=1") || !strings.Contains(out.String(), "filed=0") {
		t.Errorf("--dry-run does not print the same counts:\n%s", out.String())
	}
	rows, _ := Read(queue)
	if len(rows) != 1 || rows[0].State != StateOpen {
		t.Errorf("--dry-run marked a row: %+v", rows)
	}
}

// TestReportOnAQueueWithNoEdges: a queue that has hit no edge has no ledger, and that is not
// an error.
func TestReportOnAQueueWithNoEdges(t *testing.T) {
	queue := t.TempDir()
	fake := &fakeCreator{}
	var out, errs strings.Builder
	if code := Report(ReportInput{Queue: queue, Repo: "o/n", Creator: fake, Stdout: &out, Stderr: &errs}); code != 0 {
		t.Fatalf("Report on an empty queue = %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "rows=0") {
		t.Errorf("the EDGES line:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(queue, File)); err == nil {
		t.Error("Report created the ledger it found nothing in")
	}
}

// TestReportKeepsGoingWhenOneIssueFails: a creator that refuses one row must not lose the
// rest, and must not mark the row it could not file.
func TestReportKeepsGoingWhenOneIssueFails(t *testing.T) {
	queue := t.TempDir()
	if err := Record(queue, "nova-pulse", "cut", "a line", "something"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCreator{fail: true}
	var out, errs strings.Builder
	if code := Report(ReportInput{Queue: queue, Repo: "o/n", Creator: fake, Stdout: &out, Stderr: &errs}); code != 1 {
		t.Fatalf("Report with a failing creator = %d, want 1", code)
	}
	rows, _ := Read(queue)
	if len(rows) != 1 || rows[0].State != StateOpen {
		t.Errorf("a row that could not be filed was marked filed: %+v", rows)
	}
	if !strings.Contains(errs.String(), "could not be filed") {
		t.Errorf("stderr does not say what happened:\n%s", errs.String())
	}
}

// TestRecordNeverCreatesTheQueueDirectory: a refusal often names a path that is wrong --
// that is frequently the edge -- and a recorder that made the directory would answer a typo
// by writing a queue into somebody's source tree. A run_test case passing `--queue x` left a
// cmd/nova-pulse/x/EDGES.tsv behind in the checkout, which is what this pins.
func TestRecordNeverCreatesTheQueueDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "x")
	if err := Record(missing, "nova-pulse", "run", "nova-pulse run: --roots is required", "something"); err != nil {
		t.Fatalf("recording into a path that is not a queue is an error: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("Record created the directory a refusal named; a typo is not a queue")
	}
}
