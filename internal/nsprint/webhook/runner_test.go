package webhook_test

// The runner receipt's shape without a store (card gh-ci-receipts): every
// refusal names the field and its remedy in one line, the stream comes from
// the branches the way the card says, and Source folds a record into
// runner, hook or none.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

func goodReceipt() webhook.Receipt {
	return webhook.Receipt{
		Repo: "mas-bandwidth/nova-tools", SHA: strings.Repeat("a", 40), RunID: "36300000001", Event: "pull_request",
		HeadBranch: "rowan/ci-receipts", BaseBranch: "dev", PR: "4350", Workflow: "ci", Conclusion: "success",
		At: "2026-09-26T17:00:00Z", Jobs: []webhook.Job{{Name: "lint", Result: "success"}, {Name: "lisp", Result: "skipped"}},
	}
}

func TestReceiptValidateNamesTheFieldAndRemedy(t *testing.T) {
	t.Parallel()

	if err := func() error { r := goodReceipt(); return r.Validate() }(); err != nil {
		t.Fatalf("a good receipt refused: %v", err)
	}
	cases := []struct {
		name string
		mut  func(r *webhook.Receipt)
		want string
	}{
		{"repo", func(r *webhook.Receipt) { r.Repo = "nova-tools" }, "--repo wants owner/name"},
		{"sha", func(r *webhook.Receipt) { r.SHA = "abc" }, "--sha wants the 40-hex head"},
		{"run-id", func(r *webhook.Receipt) { r.RunID = "x1" }, "--run-id wants the decimal github.run_id"},
		{"event", func(r *webhook.Receipt) { r.Event = "schedule" }, "--event wants pull_request, merge_group, push or workflow_dispatch"},
		{"workflow", func(r *webhook.Receipt) { r.Workflow = " " }, "--workflow wants the workflow's name"},
		{"conclusion", func(r *webhook.Receipt) { r.Conclusion = "green" }, "--conclusion wants success, failure or cancelled"},
		{"pr", func(r *webhook.Receipt) { r.PR = "#4350" }, "--pr wants the pull request number or nothing"},
		{"at", func(r *webhook.Receipt) { r.At = "yesterday" }, "--at wants RFC3339"},
		{"job result", func(r *webhook.Receipt) { r.Jobs[0].Result = "green" }, `--job lint: result "green" is not success, failure, cancelled or skipped`},
		{"job twice", func(r *webhook.Receipt) { r.Jobs = append(r.Jobs, webhook.Job{Name: "lint", Result: "failure"}) }, "--job lint given twice"},
	}
	for _, c := range cases {
		r := goodReceipt()
		c.mut(&r)
		err := r.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want it to name %q", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: a refusal is one line, got %q", c.name, err.Error())
		}
	}

	// Normalisation: refs are stripped, a workflow name with spaces is one
	// token (the hash field is '<word> <id> <at>'), an empty at is the
	// receipt's clock, never the wall clock.
	r := goodReceipt()
	r.HeadBranch, r.BaseBranch, r.Workflow, r.At = "refs/heads/stream/github", "refs/heads/dev", "CI tests", ""
	r.Now = func() time.Time { return time.Date(2026, 9, 26, 12, 38, 0, 0, time.FixedZone("ET", -4*3600)) }
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.HeadBranch != "stream/github" || r.BaseBranch != "dev" || r.Workflow != "CI-tests" || r.At != "2026-09-26T16:38:00Z" {
		t.Fatalf("normalised: %+v", r)
	}
}

func TestParseJob(t *testing.T) {
	t.Parallel()

	j, err := webhook.ParseJob(" test-race-queue=cancelled ")
	if err != nil || j != (webhook.Job{Name: "test-race-queue", Result: "cancelled"}) {
		t.Fatalf("got %+v, %v", j, err)
	}
	for _, bad := range []string{"lint", "=success", "lint=", "a b=success", "lint=ok"} {
		if _, err := webhook.ParseJob(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSourceFoldsARecord(t *testing.T) {
	t.Parallel()

	if got := webhook.Source(webhook.Parse(nil)); got != webhook.SourceNone {
		t.Errorf("nothing recorded: %q", got)
	}
	hook := webhook.Parse(map[string]string{"gh": "green", "ev_id": "1-0", "wf:ci": "green 7 t"})
	if got := webhook.Source(hook); got != webhook.SourceHook {
		t.Errorf("consumer-written record: %q", got)
	}
	runner := webhook.Parse(map[string]string{"gh": "green", "ev_id": "2-0", "source": "runner", "wf:ci": "green 8 t"})
	if got := webhook.Source(runner); got != webhook.SourceRunner {
		t.Errorf("runner-stamped record: %q", got)
	}
	if runner.EvID != "2-0" || runner.Source != "runner" {
		t.Errorf("Parse keeps ev_id and source: %+v", runner)
	}
	for sender, want := range map[string]string{"": webhook.SourceNone, "runner": webhook.SourceRunner, "glenn": webhook.SourceHook, " runner ": webhook.SourceRunner} {
		if got := webhook.SourceOf(sender); got != want {
			t.Errorf("SourceOf(%q) = %q, want %q", sender, got, want)
		}
	}
}

func TestWrittenLine(t *testing.T) {
	t.Parallel()

	w := webhook.Written{Key: "ci:nova-tools:" + strings.Repeat("a", 40) + ":gh", Word: "green", Runs: 8, Applied: 8, EntryID: "1-0"}
	want := "CIGH RUNNER ci:nova-tools:" + strings.Repeat("a", 40) + ":gh gh=green fail=- runs=8 applied=8 ev=1-0"
	if got := w.Line(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
