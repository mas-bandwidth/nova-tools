package main

import (
	"strings"
	"testing"
)

// ISSUE #103, END TO END, WITH THE PROVIDER'S OWN SENTENCE: the Freddy swarm at n=64 on
// Mercury 2.5 (OpenCode) reading nova-tools v0.12.0, 2026-09-12. Two of forty jobs read a
// whole spec and a second spec; OpenCode printed
// `Error: Rate limit reached: input token limit exceeded` and exited 1 after ~215s. The
// launcher (run-freddy.sh) and the dispatcher both saw rc=1 and nothing else: the pool said
// `failed`, and the one fact that explains it -- the task was too big for the model -- was
// in a log nobody reads forty of.
//
// Worse, the words `rate limit` are in that sentence, so the death was read as a 429: the
// slot was held for the backoff and the SAME task was launched again, to spend another 215
// seconds proving the same spec still does not fit.
//
// The fake harness prints that line, with its paint, and exits 1 (FAKE-INPUT-LIMIT).
func TestAnInputLimitIsNamedAndNotRetried(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a read of two whole specs\nFAKE-INPUT-LIMIT\nFAKE-LAUNCHES\nFAKE-USAGE 10 5 - - -\n")

	// A FAILED TASK IS NOT A FAILED RUN (SPEC-SWARM.md, exit codes): the pass goes on and
	// `RUN OK` carries `failed=<n>`. What was missing was never the exit code -- it was the
	// CLASS on the line, and the retry that class must not get.
	exit, stdout, stderr := b.run("--backoff", "1")
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN OK started=1 done=0 failed=1")
	// ONE ATTEMPT. A 429 is retried once; an input that did not fit is not, because the
	// second run is the same input.
	if n := strings.Count(stdout, "RUN INPUT-LIMIT id="); n != 1 {
		t.Fatalf("an input limit wants exactly one RUN INPUT-LIMIT line, got %d:\n%s", n, stdout)
	}
	if strings.Contains(stdout, "rc=429") {
		t.Errorf("an input limit is not a 429:\n%s", stdout)
	}
	// THE PROVIDER'S OWN WORDS ON THE LINE, so triage can say what happened without
	// opening a log.
	mustContain(t, "the run", stdout, "input token limit exceeded")
	mustContain(t, "the run", stdout, "dest=failed")

	sc := b.sidecar(id)
	if sc.End != "input-limit" {
		t.Errorf("the sidecar wants end=input-limit, got %q", sc.End)
	}
	if sc.Requeued != 0 || sc.From != "" {
		t.Errorf("the task was re-queued: requeued=%d from=%q", sc.Requeued, sc.From)
	}
	if got := b.usageRow(id)["end"]; got != "input-limit" {
		t.Errorf("the `end` column is what the token ledger reads, got %q", got)
	}
	// AND TRIAGE SAYS IT, from the sidecar, with no log opened.
	exit, stdout, stderr = b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "triage", stdout, "TRIAGE INPUT-LIMIT id="+id)
	mustContain(t, "triage", stdout, "input token limit exceeded")
}

// THE TASK BUDGET NAMES THE WINDOW IT FITS (#103, the second half). Freddy's own TEAM-SPEC
// asks a task for exact files and a hard budget; `max_input` is that budget for the one
// thing this tool measures -- the prompt it hands the harness -- and the dispatcher checks
// it BEFORE the launch, so a task that cannot fit is refused for nothing rather than paid
// for. The refusal carries the class and the MEASURED size.
func TestATaskOverItsMaxInputIsRefusedBeforeTheLaunch(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a task whose prompt cannot fit the window it names\nFAKE-LAUNCHES\n", "--max-input", "200")

	exit, stdout, stderr := b.run()
	if exit == 0 {
		t.Errorf("a task that was never launched is not a green run:\n%s%s", stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN INPUT-LIMIT id="+id)
	mustContain(t, "the run", stdout, "max=200")
	if strings.Contains(stdout, "RUN START id="+id) {
		t.Errorf("the check is BEFORE the launch; nothing is paid for a task that cannot fit:\n%s", stdout)
	}
	if sc := b.sidecar(id); sc.End != "input-limit" {
		t.Errorf("the sidecar wants end=input-limit, got %q", sc.End)
	}
}
