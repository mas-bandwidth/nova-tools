package pulse

// SPEC-PULSE ## Fleet, rule R (pit stop 4): nothing enters the loop untested. `fleet add`
// is the only verb that admits a bench to the loop: it reads the fleet-probe record (the
// ci.yml fleet-probe job, one job per runner slot, read back by runner_name) and refuses a
// bench unless every runner name is green, printing the run id. PULSE_ROOTS and the runner
// labels are written only by that verb.
//
// The mutation this pins: a bench whose probe carries one failed job must not be admitted.
// Before the fix `fleet add` admitted it and the failed runner name never reached a line.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetAddRefusesABenchWithAFailedProbeJob(t *testing.T) {
	queue := t.TempDir()
	probe := filepath.Join(queue, "fleet-probe.tsv")
	write(t, probe, "run\t4242\n"+
		"bench\tvision\n"+
		"runner\tvision-nova-1\tsuccess\n"+
		"runner\tvision-nova-2\tfailure\n")

	var stdout, stderr bytes.Buffer
	code := FleetAdd(FleetAddInput{
		Bench:  "vision",
		Queue:  queue,
		Roots:  "/home/rowan-working/swarm-root",
		Probe:  probe,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("fleet add exit=%d want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	line := stdout.String()
	if !strings.Contains(line, "vision-nova-2") {
		t.Errorf("the refused runner name is not on the line: %q", line)
	}
	if !strings.Contains(line, "4242") {
		t.Errorf("the run id is not on the line: %q", line)
	}
	if _, err := os.Stat(filepath.Join(queue, "PULSE_ROOTS")); !os.IsNotExist(err) {
		t.Errorf("a refused bench must write no PULSE_ROOTS; stat err=%v", err)
	}
}

func TestFleetAddAdmitsAnAllGreenBenchAndWritesTheRecord(t *testing.T) {
	queue := t.TempDir()
	probe := filepath.Join(queue, "fleet-probe.tsv")
	write(t, probe, "run\t4242\n"+
		"bench\tvision\n"+
		"runner\tvision-nova-1\tsuccess\n"+
		"runner\tvision-nova-2\tsuccess\n")

	var stdout, stderr bytes.Buffer
	code := FleetAdd(FleetAddInput{
		Bench:  "vision",
		Queue:  queue,
		Roots:  "/home/rowan-working/swarm-root",
		Probe:  probe,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("fleet add exit=%d want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	roots, err := os.ReadFile(filepath.Join(queue, "PULSE_ROOTS"))
	if err != nil {
		t.Fatalf("the admitted bench must write PULSE_ROOTS: %v", err)
	}
	if got := strings.TrimSpace(string(roots)); got != "/home/rowan-working/swarm-root" {
		t.Errorf("PULSE_ROOTS=%q want the roots the verb was given", got)
	}
	labels, err := os.ReadFile(filepath.Join(queue, "runner-labels.tsv"))
	if err != nil {
		t.Fatalf("the admitted bench must write its runner labels: %v", err)
	}
	for _, want := range []string{"vision-nova-1", "vision-nova-2"} {
		if !strings.Contains(string(labels), want) {
			t.Errorf("runner-labels.tsv is missing %q: %q", want, labels)
		}
	}
}
