package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #525: watch --adopt runs the coordinator's own adoption pass after every
// rebuild and escalates refusals.
func TestWatchAdoptRunsPassEscalatesAndPostsReceipt(t *testing.T) {
	log := fakeBusPath(t)
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
		t.Fatal(err)
	}
	c, out, errs := run(t, Environment{}, "watch", "--adopt", checks,
		"--bus", bus, "--remote", "origin", "--branch", "main",
		"--as", "coordinator", "--to", "duty")
	combined := out + "\n" + errs
	if c != 1 {
		t.Fatalf("want exit 1 with one refusal, got %d:\n%s", c, combined)
	}
	need(t, combined, "ADOPT OK check=versions-agree")
	need(t, combined, "ADOPT OK check=known-answer-flat")
	need(t, combined, "ADOPT OK check=known-answer-local")
	need(t, combined, "ADOPT OK check=known-answer-remote-bench")
	need(t, combined, "ADOPT REFUSED check=snapshot-report")
	need(t, combined, "ADOPT ESCALATE check=snapshot-report")
	need(t, combined, "ADOPT DONE sha=", "ok=4 refused=1")
	need(t, combined, "ADOPT SENT")
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("coordinator posted no bus receipt: %v", err)
	}
	if strings.Count(string(b), "prepare\n") != 1 || strings.Count(string(b), "send\n") != 1 {
		t.Fatalf("adoption receipt was not posted once via prepare+send:\n%s", string(b))
	}
}

// The contract lives in SPEC-UPDATE.md rule 27; a paragraph renamed out of the
// doc is red the same way the verbs block is.
func TestSpecUpdateNamesAdoptPass(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	if err != nil {
		t.Fatal(err)
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
			t.Errorf("SPEC-UPDATE.md does not name the adoption pass keyed by %q", phrase)
		}
	}
}
