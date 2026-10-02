package cireceipt

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func full() Receipt {
	return Receipt{Repo: "mas-bandwidth/nova-tools", SHA: strings.ToUpper(sha), RunID: "123456789",
		PR: "4493", Workflow: " CI  run ", Conclusion: "success",
		Now: func() time.Time { return time.Date(2026, 9, 27, 22, 0, 0, 0, time.FixedZone("EDT", -4*3600)) }}
}

// TestReceiptFieldsAreTheRowTheReaderReads pins the XADD's field set, exactly:
// the workflow_run row `nova-sprint ci github --from-runner` appended, so
// ghevent's reader (ghevent.Reader) is unchanged by the move.
func TestReceiptFieldsAreTheRowTheReaderReads(t *testing.T) {
	t.Parallel()
	r := full()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := ghevent.Fields(r.Entry())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"repo": "mas-bandwidth/nova-tools", "kind": "workflow_run", "number": "4493", "head": sha,
		"action": "completed", "at": "2026-09-28T02:00:00Z", "sender": "runner", "comment_id": "",
		"run_id": "123456789", "workflow": "CI-run", "status": "completed", "conclusion": "success",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("XADD fields\n got %v\nwant %v", got, want)
	}
	if line := Line(r, "1-0"); line != "CI RECEIPT mas-bandwidth/nova-tools sha="+sha+" run=123456789 workflow=CI-run conclusion=success pr=4493 ev=1-0" {
		t.Fatalf("line %q", line)
	}
}

func TestReceiptWithoutAPullRequestHasAnEmptyNumber(t *testing.T) {
	t.Parallel()
	r := full()
	r.PR, r.At = "", "2026-09-27T12:00:00Z"
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	got, _ := ghevent.Fields(r.Entry())
	if got["number"] != "" || got["at"] != "2026-09-27T12:00:00Z" {
		t.Fatalf("number %q at %q", got["number"], got["at"])
	}
	if !strings.Contains(Line(r, "1-0"), " pr=- ") {
		t.Fatalf("line %q", Line(r, "1-0"))
	}
}

func TestReceiptRefusesEachFieldWithWhatItWants(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		edit func(*Receipt)
		want string
	}{
		{func(r *Receipt) { r.Repo = "nova-tools" }, "--repo wants owner/name"},
		{func(r *Receipt) { r.Repo = "a/b/c" }, "--repo wants owner/name"},
		{func(r *Receipt) { r.Repo = "a/b:c" }, "--repo wants owner/name"},
		{func(r *Receipt) { r.SHA = "abc" }, "--sha wants the 40-hex head"},
		{func(r *Receipt) { r.RunID = "12a" }, "--run-id wants the decimal"},
		{func(r *Receipt) { r.RunID = "" }, "--run-id wants the decimal"},
		{func(r *Receipt) { r.Workflow = "  " }, "--workflow wants"},
		{func(r *Receipt) { r.Conclusion = "skipped" }, "--conclusion wants success, failure or cancelled"},
		{func(r *Receipt) { r.PR = "#12" }, "--pr wants the pull request number"},
		{func(r *Receipt) { r.At = "yesterday" }, "--at wants RFC3339"},
	} {
		r := full()
		c.edit(&r)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
}

// One call names every field a receipt cannot be written from, so a caller
// fixes the invocation once (STANDARD §2: every problem at once).
func TestReceiptRefusesEveryFieldInOneError(t *testing.T) {
	t.Parallel()
	r := Receipt{Repo: "nova-tools", SHA: "abc", RunID: "x", Conclusion: "maybe", PR: "#1", At: "yesterday"}
	err := r.Validate()
	require.Error(t, err)
	for _, want := range []string{"--repo wants", "--sha wants", "--run-id wants", "--workflow wants", "--conclusion wants", "--pr wants", "--at wants"} {
		assert.Contains(t, err.Error(), want)
	}
	assert.NotContains(t, err.Error(), "\n", "the problems are one line, joined with ; ")
}

func TestWriteWithNoClientWritesNothing(t *testing.T) {
	t.Parallel()
	if _, err := Write(context.Background(), nil, full()); err == nil {
		t.Fatal("Write with no client did not refuse")
	}
}
