package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #525: watch --adopt runs the coordinator's own adoption pass after every
// rebuild and escalates refusals.
func TestWatchAdoptRunsPassEscalatesAndPostsReceipt(t *testing.T) {
	var mu sync.Mutex
	var calls [][]string
	fake := Environment{Process: func(_ context.Context, args []string, _ io.Reader, _ int) ProcessResult {
		mu.Lock()
		calls = append(calls, append([]string(nil), args...))
		mu.Unlock()
		return ProcessResult{Stdout: "SEND OK id=01ABC to=duty cc=- at=2026-10-04T17:00:00Z\n"}
	}}
	bus := t.TempDir()
	checks := filepath.Join(t.TempDir(), "checks.tsv")
	rows := []string{
		"check\tcommand\towner",
		"versions-agree\t" + printer(t, "adopt-ok 1.0.0") + "\trowan",
		"known-answer-flat\t" + printer(t, "flat-ok") + "\trowan",
		"known-answer-local\t" + printer(t, "local-ok") + "\trowan",
		"known-answer-remote-bench\t" + printer(t, "remote-ok") + "\trowan",
		"snapshot-report\t" + command(t, "fail") + "\trowan",
	}
	if err := os.WriteFile(checks, []byte(strings.Join(rows, "\n")+"\n"), 0600); err != nil {
		require.NoError(t, err, err)
	}
	c, out, errs := run(t, fake, "watch", "--adopt", checks,
		"--bus", bus, "--remote", "origin", "--branch", "main",
		"--as", "coordinator", "--to", "duty")
	combined := out + "\n" + errs
	if c != 1 {
		require.EqualValuesf(t, 1, c, "want exit 1 with one refusal, got %d:\n%s", c, combined)
	}
	need(t, combined, "ADOPT OK check=versions-agree")
	need(t, combined, "ADOPT OK check=known-answer-flat")
	need(t, combined, "ADOPT OK check=known-answer-local")
	need(t, combined, "ADOPT OK check=known-answer-remote-bench")
	need(t, combined, "ADOPT REFUSED check=snapshot-report")
	need(t, combined, "ADOPT ESCALATE check=snapshot-report")
	need(t, combined, "ADOPT DONE sha=", "ok=4 refused=1")
	need(t, combined, "ADOPT SENT")
	mu.Lock()
	defer mu.Unlock()
	var sent [][]string
	for _, argv := range calls {
		if len(argv) > 0 && argv[0] == "nova-bus" {
			sent = append(sent, argv)
		}
	}
	if len(sent) != 1 {
		require.Failf(t, "", "adoption receipt was not posted with exactly one bus child, got %q", sent)
	}
	joined := strings.Join(sent[0], " ")
	if !strings.HasPrefix(joined, "nova-bus send --as ") {
		require.Failf(t, "", "adoption receipt was not one Redis send: %q", sent[0])
	}
	for _, want := range []string{"--to", "--subject", "--stdin"} {
		if !strings.Contains(joined, want) {
			require.Failf(t, "", "the send carries no %s: %q", want, sent[0])
		}
	}
	if strings.Contains(joined, "prepare") {
		require.Failf(t, "", "the retired prepare step still runs: %q", sent[0])
	}
}

// The contract lives in SPEC-UPDATE.md rule 27; a paragraph renamed out of the
// doc is red the same way the verbs block is.
func TestSpecUpdateNamesAdoptPass(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	if err != nil {
		require.NoError(t, err, err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"coordinator's own adoption pass",
		"ADOPT DONE",
		"ADOPT ESCALATE",
		"known-answer",
		"as the coordinator's own",
	} {
		if !strings.Contains(doc, phrase) {
			assert.Containsf(t, doc, phrase, "SPEC-UPDATE.md does not name the adoption pass keyed by %q", phrase)
		}
	}
}
