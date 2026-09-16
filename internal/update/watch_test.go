package update

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkCmd(t *testing.T, action, text string) string {
	t.Helper()
	return command(t, action, base64.StdEncoding.EncodeToString([]byte(text+"\n")))
}
func writeChecks(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "checks.tsv")
	if err := os.WriteFile(p, []byte("name\targv\n"+strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Rule 27: after a rebuild the coordinator's own adoption is a mechanical step.
// Each check the file names runs bounded; an exit 0 with an answer line is
// ADOPT OK, everything else ADOPT REFUSED naming its remedy, and every REFUSED
// is handed to the duty tier as one ADOPT ESCALATE line. The receipt closes
// with ADOPT DONE sha= ok= refused=.
func TestWatchAdoptRunsTheCoordinatorsOwnAdoptionPass(t *testing.T) {
	p := writeChecks(t,
		"versions-agree\t"+checkCmd(t, "print", "versions agree: 5/5 current"),
		"bus-roundtrip\t"+checkCmd(t, "print", "bus round trip: note came back"),
		"wake-awake\t"+checkCmd(t, "print", "nova-wake awake from config"),
		"swarm-card-flat\t"+checkCmd(t, "print", "known-answer flat: match"),
		"swarm-card-local\t"+checkCmd(t, "print", "known-answer local: match"),
		"swarm-card-remote\t"+checkCmd(t, "print", "known-answer remote bench: match"),
		"snapshot-report\t"+checkCmd(t, "checkfail", "#372 snapshot kind"),
		"tokens-sum\t"+checkCmd(t, "checkfail", "#523 keyless/unused provider config"),
	)
	c, out, errs := run(t, Environment{}, "watch", "--adopt", p, "--rebuild", "8f3714d")
	if c != 1 {
		t.Fatalf("exit = %d, want 1 (a refusal makes adoption fail):\n%s%s", c, out, errs)
	}
	need(t, out,
		"ADOPT at=", "rebuild=8f3714d", "checks=8",
		"ADOPT OK versions-agree versions agree: 5/5 current",
		"ADOPT OK bus-roundtrip bus round trip: note came back",
		"ADOPT OK wake-awake nova-wake awake from config",
		"ADOPT OK swarm-card-flat known-answer flat: match",
		"ADOPT OK swarm-card-local known-answer local: match",
		"ADOPT OK swarm-card-remote known-answer remote bench: match",
		"ADOPT REFUSED snapshot-report #372 snapshot kind",
		"ADOPT REFUSED tokens-sum #523 keyless/unused provider config",
		"ADOPT DONE sha=8f3714d ok=6 refused=2 took=")
	if !strings.Contains(errs, "ADOPT ESCALATE ") {
		t.Fatalf("no escalation was handed to the duty tier:\n%s", errs)
	}
	if !strings.Contains(errs, "adopt snapshot-report: #372 snapshot kind") {
		t.Fatalf("the snapshot refusal was not escalated:\n%s", errs)
	}
	if !strings.Contains(errs, "adopt tokens-sum: #523 keyless/unused provider config") {
		t.Fatalf("the tokens refusal was not escalated:\n%s", errs)
	}
}

// Rule 27's receipt is the coordinator's own: --draft prints it as a bus note
// body with ADOPT DONE last, and --send posts it through rule 24's prepared
// delivery, one prepare and one confirmed send.
func TestWatchAdoptPostsTheReceiptToTheBus(t *testing.T) {
	log := fakeBusPath(t)
	p := writeChecks(t, "versions-agree\t"+checkCmd(t, "print", "versions agree: 5/5 current"))
	c, out, errs := run(t, Environment{}, "watch", "--adopt", p, "--rebuild", "beef",
		"--send", "--as", "rowan", "--to", "duty", "--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
	if c != 0 {
		t.Fatalf("send exit %d:\n%s%s", c, out, errs)
	}
	if np, ns := calls(t, log); np != 1 || ns != 1 {
		t.Fatalf("prepare/send = %d/%d, want 1/1", np, ns)
	}
	need(t, out, "ADOPT SENT to=duty", "ADOPT DONE sha=beef ok=1 refused=0")
	var o, e strings.Builder
	if code := Run("nova-update", []string{"watch", "--adopt", p, "--rebuild", "beef",
		"--draft", "--as", "rowan", "--to", "duty"}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("draft exit %d:\n%s%s", code, o.String(), e.String())
	}
	draft := o.String()
	if !strings.HasPrefix(draft, "From: rowan\nTo: duty\nSubject: adoption at ") {
		t.Fatalf("the receipt is not the coordinator's own note:\n%s", draft)
	}
	lines := strings.Split(strings.TrimSpace(draft), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "ADOPT DONE sha=beef ok=1 refused=0 took=") {
		t.Fatalf("the receipt does not end with ADOPT DONE:\n%s", draft)
	}
}

// The checks file is named by a flag, keeps the header byte for byte, and a
// malformed line is a refusal naming the line, like rule 1 and rule 2.
func TestWatchAdoptNamesItsChecksFile(t *testing.T) {
	if c, _, errs := run(t, Environment{}, "watch"); c != 2 {
		t.Fatalf("watch with no --adopt = %d, want 2:\n%s", c, errs)
	} else if !strings.Contains(errs, "--adopt") {
		t.Fatalf("the refusal does not name --adopt:\n%s", errs)
	}
	bad := filepath.Join(t.TempDir(), "checks.tsv")
	if err := os.WriteFile(bad, []byte("name\tonly-one-field\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if c, _, errs := run(t, Environment{}, "watch", "--adopt", bad); c != 2 {
		t.Fatalf("a bad header = %d, want 2:\n%s", c, errs)
	} else if !strings.Contains(errs, "line 1") {
		t.Fatalf("the refusal does not name line 1:\n%s", errs)
	}
}
