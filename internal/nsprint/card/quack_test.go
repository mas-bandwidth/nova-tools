package card_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestQuackRowVerdicts: a stage over its bar fails there, a card ended
// failed fails at the first stage it never reached with its reason, a row
// still moving waits, and the missing stage is named only once the probe
// has timed out.
func TestQuackRowVerdicts(t *testing.T) {
	t.Parallel()

	bars := card.DefaultQuackBars
	full := card.QuackProgress{At: map[string]int64{"push": 1000, "deal": 3000, "launch": 4000, "end": 60000, "harvest": 70000, "read": 80000}}
	if _, ok, v := card.QuackRow(full, 1000, bars, false); !ok || v != "PASS" {
		t.Fatalf("full row: %v %q, want PASS", ok, v)
	}
	slow := card.QuackProgress{At: map[string]int64{"push": 1000, "deal": 3000, "launch": 4000, "end": 1000 + 121000}}
	if cols, _, v := card.QuackRow(slow, 1000, bars, false); v != "FAIL end over bar=120s" || !strings.Contains(cols, "end=121 harvest=-") {
		t.Fatalf("slow end: %q %q", cols, v)
	}
	moving := card.QuackProgress{At: map[string]int64{"push": 1000, "deal": 3000}}
	if _, _, v := card.QuackRow(moving, 1000, bars, false); v != "WAIT" {
		t.Fatalf("moving row: %q, want WAIT", v)
	}
	if _, _, v := card.QuackRow(moving, 1000, bars, true); v != "FAIL launch missing" {
		t.Fatalf("timed out: %q, want FAIL launch missing", v)
	}
	failed := card.QuackProgress{At: moving.At, Failed: "outcome=BLOCKED reason=wall"}
	if !failed.Done() {
		t.Fatal("a failed card is done")
	}
	if _, _, v := card.QuackRow(failed, 1000, bars, false); v != "FAIL launch outcome=BLOCKED reason=wall" {
		t.Fatalf("failed card: %q", v)
	}
}

// TestMirrorBranchSHA reads the base-sha from this host's mirror with git,
// and refuses a repo with no mirror.
func TestMirrorBranchSHA(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NOVA_MIRROR_ROOT", root)
	if _, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "dev"); err == nil {
		t.Fatal("no mirror: want a refusal")
	}
	work := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "dev", work},
		{"-C", work, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "base"},
		{"clone", "-q", "--bare", work, root + "/nova-tools.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	want, _ := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	got, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "dev")
	if err != nil || got != strings.TrimSpace(string(want)) {
		t.Fatalf("got %q %v, want %s", got, err, want)
	}
	if _, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "nope"); err == nil {
		t.Fatal("missing branch: want a refusal")
	}
}
