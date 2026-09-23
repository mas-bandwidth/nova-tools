package pulse

// Red tests for nova-pulse triage --dedupe (nova-tools #896). Before the packet card is
// cut, triage asks one typed decision: is this new case the same class as one of the open
// issues in --issues? At or above the floor the card gains the open issue's number on a
// LINKS line and the TRIAGE line reads dedupe=#<n>; on `none`, on a provider error, or
// below the floor, the card is cut unchanged and the line reads dedupe=?. No test reaches
// the network: the fake implements the same TriageDecider seam the shipped *decide.Client
// satisfies.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeSameClass answers triage's dedupe question without a provider, and records the state
// the verb sent so a test can prove the case's title and evidence travelled with it.
type fakeSameClass struct {
	choice string
	conf   float64
	state  string
	asked  int
}

func (f *fakeSameClass) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.asked++
	f.state = state
	if _, ok := qs["same_class"]; !ok {
		return map[string]decide.Answer{}, decide.Usage{}, nil
	}
	return map[string]decide.Answer{
		"same_class": {Type: "choice", Choice: f.choice, Probabilities: map[string]float64{f.choice: f.conf}, Confidence: f.conf},
	}, decide.Usage{}, nil
}

// dedupeFixture writes the issues file (number<TAB>title, one open issue per line) and the
// undecided case's evidence, and returns the issues path.
func dedupeFixture(t *testing.T, queue string) string {
	t.Helper()
	issues := filepath.Join(t.TempDir(), "issues.tsv")
	if err := os.WriteFile(issues, []byte("812\tgate red on windows\n900\tflaky studio read\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(queue, "UNDECIDED"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "RESULT card-892 sha=0123456789ab\nABSTAIN reason=idle=300\nPERMISSION denied: reading outside the job directory\n"
	if err := os.WriteFile(filepath.Join(queue, "UNDECIDED", "fence.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return issues
}

func dedupeRun(t *testing.T, queue, issues string, dec TriageDecider, floor float64) (string, string, string, int) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "card.md")
	var stdout, stderr bytes.Buffer
	code := Triage(TriageInput{
		Case: "fence", Queue: queue, Out: out, Ref: "card-892",
		Dedupe: true, Issues: issues, Floor: floor, Decider: dec,
		Stdout: &stdout, Stderr: &stderr,
	})
	card, _ := os.ReadFile(out)
	return stdout.String(), stderr.String(), string(card), code
}

// triage-dedupe-links-a-same-class-open-issue: a same_class decision naming issue 812 at
// 0.95, at or above the 0.9 floor, puts the LINKS line on the card and dedupe=#812 on the
// TRIAGE line. The state carries the new case's title and evidence, never a transcript.
func TestTriageDedupeLinksASameClassIssue(t *testing.T) {
	queue := t.TempDir()
	issues := dedupeFixture(t, queue)
	dec := &fakeSameClass{choice: "812", conf: 0.95}

	stdout, stderr, card, code := dedupeRun(t, queue, issues, dec, 0.9)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if dec.asked != 1 {
		t.Fatalf("the decider was asked %d times, want 1", dec.asked)
	}
	if !strings.Contains(card, "LINKS #812 (same class, conf=0.95)") {
		t.Fatalf("the card does not carry the LINKS line:\n%s", card)
	}
	if !strings.Contains(stdout, "dedupe=#812") {
		t.Fatalf("the TRIAGE line does not read dedupe=#812: %q", stdout)
	}
	if !strings.Contains(dec.state, "card-892") {
		t.Fatalf("the state does not carry the case's title: %q", dec.state)
	}
	if !strings.Contains(dec.state, "outside the job directory") {
		t.Fatalf("the state does not carry the case's evidence: %q", dec.state)
	}
}

// triage-dedupe-none-files-normally: a `none` decision at 0.9 changes nothing: no LINKS
// line, and the TRIAGE line reads dedupe=?.
func TestTriageDedupeNoneFilesNormally(t *testing.T) {
	queue := t.TempDir()
	issues := dedupeFixture(t, queue)
	dec := &fakeSameClass{choice: "none", conf: 0.9}

	stdout, stderr, card, code := dedupeRun(t, queue, issues, dec, 0.9)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if strings.Contains(card, "LINKS ") {
		t.Fatalf("a none decision linked an issue:\n%s", card)
	}
	if !strings.Contains(stdout, "dedupe=?") {
		t.Fatalf("the TRIAGE line does not read dedupe=?: %q", stdout)
	}
}

// triage-dedupe-below-floor-changes-nothing: a same_class decision naming issue 812 at
// 0.5, under the 0.9 floor, is a suggestion, never an authorization: no LINKS line, no
// link on the TRIAGE line, and the card is cut as it is today.
func TestTriageDedupeBelowFloorChangesNothing(t *testing.T) {
	queue := t.TempDir()
	issues := dedupeFixture(t, queue)
	dec := &fakeSameClass{choice: "812", conf: 0.5}

	stdout, stderr, card, code := dedupeRun(t, queue, issues, dec, 0.9)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if strings.Contains(card, "LINKS ") {
		t.Fatalf("a below-floor decision linked an issue:\n%s", card)
	}
	if !strings.Contains(stdout, "dedupe=?") {
		t.Fatalf("the TRIAGE line does not read dedupe=? below the floor: %q", stdout)
	}
	if !strings.Contains(card, "RESULT triage-fence-card-892 sha=") {
		t.Fatalf("the card was not cut as today:\n%s", card)
	}
}
