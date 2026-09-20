package pulse

// T05 (#1650), SPEC-TOOLWORK §1 rule 1 and §4 rules 1-5: the harvest runs `accept`
// between rule 12's line-1 verify and its push, writes the typed OUTCOME line itself,
// and never asks a model what the gate already decided.
//
// Every test here drives Harvest with the gate seam held by a fake, so no test builds a
// repository, runs a wall or reaches a network: what is under test is the HARVEST half
// of the boundary -- which cards are gated, what the verdict does to the push, the row,
// seen.tsv, retry.tsv, bench.tsv, the quarantine, OUTCOME and outcomes.jsonl.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

const (
	testBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	// testHeadSHA is what the job's clone answers for HEAD: the object the gate judges
	// and the object the push must name.
	testHeadSHA = "89abcdef0123456789abcdef0123456789abcdef"
)

// gateCall is one recorded call of the accept seam.
type gateCall struct {
	in AcceptInput
}

// fakeGate answers every call with one canned ACCEPT line and its exit code, and records
// what it was handed. It is the seam pulse.Accept sits behind, so nothing here runs a gate.
type fakeGate struct {
	line  string
	code  int
	calls []gateCall
}

func (g *fakeGate) fn() func(AcceptInput) int {
	return func(in AcceptInput) int {
		g.calls = append(g.calls, gateCall{in: in})
		if in.Stdout != nil && g.line != "" {
			fmt.Fprintln(in.Stdout, g.line)
		}
		return g.code
	}
}

func gateOK(label string) *fakeGate {
	return &fakeGate{code: 0, line: "ACCEPT OK label=" + label + " kind=fix-red head=89abcdef0123 base=0123456789ab tests=3 red_without=1 edits=- control=c0ffee bench=space cert=cert1 took=9s"}
}

func gateReject(label, reason, at string) *fakeGate {
	return &fakeGate{code: 1, line: "ACCEPT REJECT label=" + label + " kind=fix-red head=89abcdef0123 reason=" + reason + " at=" + at + " control=c0ffee bench=space cert=cert1 took=2s"}
}

func gateAbstain(label, reason string) *fakeGate {
	return &fakeGate{code: 2, line: "ACCEPT ABSTAIN label=" + label + " kind=fix-red reason=" + reason + " bench=space took=1s"}
}

// countingDecider records every typed decision asked of it and answers `fixed` at 1.00.
type countingDecider struct{ n int }

func (d *countingDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	d.n++
	return map[string]decide.Answer{"class": {Type: "choice", Choice: "fixed", Confidence: 1}}, decide.Usage{}, nil
}

// addGatedCard writes a card whose typed header names a kind, its RESULT.md and its row.
func addGatedCard(t *testing.T, root, label, slot, kind, contract, result string) {
	t.Helper()
	addCard(t, root, label, slot, "flash", contract, result)
	cardPath := filepath.Join(root, "cardsrc", label+".md")
	body := contract + "\nKIND: " + kind + "\nPATHS: internal/**\nTEST: internal/pulse TestSomething\n\nREPO mas-bandwidth/nova-tools\n\nSTEP 1. go\n"
	if err := os.WriteFile(cardPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// doneResult is a RESULT.md whose line 1 is the contract and whose branch is harvestable.
func doneResult(contract string) string {
	return contract + "\nDONE\nBRANCH rowan/fix-1\nREPO mas-bandwidth/nova-tools\nred: TestSomething -- it failed\n"
}

// harvestGated runs a harvest with the gate seam and the queue wired.
func harvestGated(t *testing.T, root, queue string, gate *fakeGate, tweak func(*HarvestInput)) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	in := HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		GateBench: "space", Cert: filepath.Join(root, "cert.txt"), Queue: queue,
	}
	if gate != nil {
		in.Gate = gate.fn()
	}
	if tweak != nil {
		tweak(&in)
	}
	code := Harvest(in)
	return out.String(), errs.String(), code
}

// fakeGitWithBase is the git fake every gated test wants: it records argv, answers
// rev-parse with a full sha and succeeds at everything else.
func fakeGitWithBase(t *testing.T, specs, arglog, sha string) {
	t.Helper()
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 4, Equals: "HEAD^{commit}", Stdout: testHeadSHA},
		{Arg: 1, Equals: "rev-parse", Stdout: sha},
		// The job clone always has an origin, because a card that pushes is a card that
		// cloned: dev's resolveDestination (#1809, Johnny's holds) compares the launch
		// record against it and REFUSES when nothing but the worker's RESULT.md can name
		// the destination. A fixture without it tests that refusal, not the gate.
		originRule("mas-bandwidth/nova-tools"),
	}})
}

func TestHarvestRunsAcceptBeforeAnyPush(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/7")
	contract := "RESULT card-1 done"
	addGatedCard(t, root, "card-1", "0", "fix-red", contract, doneResult(contract))

	g := gateReject("card-1", "vacuous-test", "TestSomething")
	out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), g, nil)

	if len(g.calls) != 1 {
		t.Fatalf("the gate ran %d times, want 1 (a gated card is judged before the push)\n%s", len(g.calls), out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.Contains(l, "gh pr create") {
			t.Fatalf("a REJECTed card was pushed: %q\nargv log:\n%s", l, strings.Join(arglogLines(t, arglog), "\n"))
		}
	}
	if !strings.Contains(out, "rejected=1") {
		t.Errorf("HARVEST line has no rejected=1:\n%s", out)
	}
	if !strings.Contains(out, "reason=vacuous-test") {
		t.Errorf("the card's row does not carry the reject token:\n%s", out)
	}
	seen, _ := os.ReadFile(filepath.Join(root, "seen.tsv"))
	if !strings.Contains(string(seen), "card-1\trejected") {
		t.Errorf("seen.tsv does not mark the card rejected: %q", seen)
	}
	retry, _ := os.ReadFile(filepath.Join(root, "retry.tsv"))
	if !strings.Contains(string(retry), "vacuous-test") {
		t.Errorf("retry.tsv does not carry the reason token as the requeue's evidence: %q", retry)
	}
}

func TestHarvestRowSaysGateNoneForARead(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/8")
	contract := "RESULT read-1 done"
	addGatedCard(t, root, "read-1", "0", "read", contract, doneResult(contract))

	g := gateOK("read-1")
	out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), g, nil)

	if len(g.calls) != 0 {
		t.Fatalf("the gate ran on an ungated kind (%d calls); a read declares gate none", len(g.calls))
	}
	if !strings.Contains(out, "gate=none") {
		t.Errorf("the row does not say gate=none, so a green row claims a check that did not run:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST PR") {
		t.Errorf("an ungated card was not pushed:\n%s", out)
	}
}

func TestOutcomeIsWrittenByHarvestAndNeverByTheCard(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/9")
	contract := "RESULT card-2 done"
	addGatedCard(t, root, "card-2", "0", "fix-red", contract, doneResult(contract))
	job := filepath.Join(root, "0", "jobs", "card-2")
	if err := os.WriteFile(filepath.Join(job, "OUTCOME"), []byte("OUTCOME label=card-2 accept=ok class=fixed conf=1.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := gateReject("card-2", "no-test", "-")
	harvestGated(t, root, filepath.Join(root, "queue"), g, nil)

	raw, err := os.ReadFile(filepath.Join(job, "OUTCOME"))
	if err != nil {
		t.Fatalf("harvest wrote no OUTCOME: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if strings.Count(line, "\n") != 0 {
		t.Errorf("OUTCOME is not one line: %q", line)
	}
	if !strings.HasPrefix(line, "OUTCOME ") {
		t.Fatalf("OUTCOME does not start with its token: %q", line)
	}
	if !strings.Contains(line, "accept=reject") || !strings.Contains(line, "reason=no-test") {
		t.Errorf("the card's own OUTCOME survived the harvest's: %q", line)
	}
	for _, want := range []string{"label=card-2", "kind=fix-red", "gather=done", "class=rejected", "base=" + testBaseSHA[:12], "bench=space"} {
		if !strings.Contains(line, want) {
			t.Errorf("OUTCOME has no %s: %q", want, line)
		}
	}
}

func TestNoDecideCallWhenAcceptDecided(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gate  func(string) *fakeGate
		class string
	}{
		{"ok is fixed", gateOK, "fixed"},
		{"reject is rejected", func(l string) *fakeGate { return gateReject(l, "vacuous-test", "T") }, "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, specs, arglog := setupPulse(t)
			fakeGitWithBase(t, specs, arglog, testBaseSHA)
			fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/10")
			contract := "RESULT card-3 done"
			addGatedCard(t, root, "card-3", "0", "fix-red", contract, doneResult(contract))

			d := &countingDecider{}
			out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), tc.gate("card-3"), func(in *HarvestInput) {
				in.Decide, in.Floor, in.Decider = true, 0.9, d
			})
			if d.n != 0 {
				t.Errorf("the provider was asked %d times about a card the gate already decided", d.n)
			}
			if !strings.Contains(out, "class="+tc.class) {
				t.Errorf("the row does not carry class=%s:\n%s", tc.class, out)
			}
			if !strings.Contains(out, "conf=-") {
				t.Errorf("a class no model was asked for must carry conf=-:\n%s", out)
			}
		})
	}
}

func TestAHarvestClassNeverPushesARejectedCard(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/11")
	contract := "RESULT card-4 done"
	addGatedCard(t, root, "card-4", "0", "fix-red", contract, doneResult(contract))

	d := &countingDecider{}
	harvestGated(t, root, filepath.Join(root, "queue"), gateReject("card-4", "out-of-path", "x/y.go"), func(in *HarvestInput) {
		in.Decide, in.Floor, in.Decider = true, 0.9, d
	})
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.Contains(l, "gh pr create") {
			t.Fatalf("a classification turned a rejected card into a push: %q", l)
		}
	}
}

func TestHarvestAbstainWritesBenchTSVAndRequeuesNothing(t *testing.T) {
	for _, tc := range []struct {
		reason   string
		wantRow  bool
		wantWhat string
	}{
		{"toolchain", true, "the bench's fault is one line for a person"},
		{"paused", false, "a paused kind is nobody's fault and writes no bench row"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			root, specs, arglog := setupPulse(t)
			fakeGitWithBase(t, specs, arglog, testBaseSHA)
			fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/12")
			contract := "RESULT card-5 done"
			addGatedCard(t, root, "card-5", "0", "fix-red", contract, doneResult(contract))

			out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), gateAbstain("card-5", tc.reason), nil)

			for _, l := range arglogLines(t, arglog) {
				if strings.HasPrefix(l, "git push") {
					t.Fatalf("an ABSTAIN pushed: %q", l)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "retry.tsv")); err == nil {
				t.Errorf("an ABSTAIN requeued the card; it requeues nothing")
			}
			raw, err := os.ReadFile(filepath.Join(root, "bench.tsv"))
			got := err == nil && strings.Contains(string(raw), tc.reason)
			if got != tc.wantRow {
				t.Errorf("bench.tsv row=%v want %v (%s); file=%q\n%s", got, tc.wantRow, tc.wantWhat, raw, out)
			}
			if !strings.Contains(out, "gate=abstain") || !strings.Contains(out, "reason="+tc.reason) {
				t.Errorf("the row does not name the abstain:\n%s", out)
			}
		})
	}
}

func TestEveryOutcomeIsOneAppendedJSONLRow(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	queue := filepath.Join(root, "queue")
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/13")
	contract := "RESULT card-6 done"
	addGatedCard(t, root, "card-6", "0", "fix-red", contract, doneResult(contract))

	harvestGated(t, root, queue, gateOK("card-6"), nil)

	path := filepath.Join(queue, "decide", "outcomes.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one appended row, got %d: %q", len(lines), raw)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("the row is not JSON: %v (%q)", err, lines[0])
	}
	for _, k := range []string{"label", "kind", "gather", "accept", "class", "base", "bench"} {
		if _, ok := row[k]; !ok {
			t.Errorf("the row has no %q field: %q", k, lines[0])
		}
	}
	if row["accept"] != "ok" || row["class"] != "fixed" {
		t.Errorf("the row does not carry the gate's verdict: %q", lines[0])
	}
}

func TestHarvestPassesAcceptAFullBaseSHANeverARef(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/14")
	contract := "RESULT card-7 done"
	addGatedCard(t, root, "card-7", "0", "fix-red", contract, doneResult(contract))

	g := gateOK("card-7")
	harvestGated(t, root, filepath.Join(root, "queue"), g, func(in *HarvestInput) { in.Base = "dev" })
	if len(g.calls) != 1 {
		t.Fatalf("the gate ran %d times, want 1", len(g.calls))
	}
	if got := g.calls[0].in.Base; got != testBaseSHA {
		t.Errorf("accept was handed --base %q; a worker can move a ref, so harvest resolves it in the job's own clone and passes the full sha %q", got, testBaseSHA)
	}
}

func TestHarvestAbstainsWhenTheBaseWillNotResolve(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	// rev-parse answers a branch name: not a sha, so there is nothing to judge against.
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "rev-parse", Stdout: "dev"},
	}})
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/15")
	contract := "RESULT card-8 done"
	addGatedCard(t, root, "card-8", "0", "fix-red", contract, doneResult(contract))

	g := gateOK("card-8")
	out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), g, nil)
	if len(g.calls) != 0 {
		t.Errorf("the gate was run with a base that is not a sha (%d calls)", len(g.calls))
	}
	if !strings.Contains(out, "gate=abstain") || !strings.Contains(out, "reason=toolchain") {
		t.Errorf("an unresolvable base is the bench's, and it abstains:\n%s", out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("an ungated card was pushed: %q", l)
		}
	}
}

func TestSecretQuarantinesAndNeverDeletes(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	queue := filepath.Join(root, "queue")
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/16")
	contract := "RESULT card-9 done"
	addGatedCard(t, root, "card-9", "0", "fix-red", contract, doneResult(contract))
	job := filepath.Join(root, "0", "jobs", "card-9")

	out, errs, _ := harvestGated(t, root, queue, gateReject("card-9", "secret", "internal/x.go:12"), nil)

	quarantine := filepath.Join(root, "quarantine", "card-9")
	if _, err := os.Stat(quarantine); err != nil {
		t.Fatalf("the job was not quarantined at %s: %v\n%s%s", quarantine, err, out, errs)
	}
	if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err == nil {
		t.Errorf("the job is still in place; a secret's job is MOVED, not copied")
	}
	if _, err := os.Stat(filepath.Join(quarantine, "RESULT.md")); err != nil {
		t.Errorf("the quarantined job lost its contents (it is moved, never deleted): %v", err)
	}
	human, err := os.ReadFile(filepath.Join(queue, "HUMAN"))
	if err != nil {
		t.Fatalf("no HUMAN line for a key that reached a worker: %v", err)
	}
	if !strings.Contains(string(human), "card-9") || !strings.Contains(string(human), "secret") {
		t.Errorf("the HUMAN line does not name the card and the shape: %q", human)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("a quarantined card was pushed: %q", l)
		}
	}
}

func TestHarvestRecordsTheOutcomeWithNovaDecide(t *testing.T) {
	for _, tc := range []struct {
		name   string
		gate   *fakeGate
		result string
	}{
		{"ok is green", gateOK("card-10"), "green"},
		{"reject is red", gateReject("card-10", "no-test", "-"), "red"},
		{"abstain is blocked", gateAbstain("card-10", "toolchain"), "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, specs, arglog := setupPulse(t)
			queue := filepath.Join(root, "queue")
			routeLog := filepath.Join(queue, "decide", "route.jsonl")
			if err := os.MkdirAll(filepath.Dir(routeLog), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(routeLog, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			fakeGitWithBase(t, specs, arglog, testBaseSHA)
			fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/17")
			fakeTool(t, specs, "nova-decide", fakeSpec{Log: arglog})
			contract := "RESULT card-10 done"
			addGatedCard(t, root, "card-10", "0", "fix-red", contract, doneResult(contract))

			harvestGated(t, root, queue, tc.gate, nil)

			var found string
			for _, l := range arglogLines(t, arglog) {
				if strings.HasPrefix(l, "nova-decide outcome") {
					found = l
				}
			}
			if found == "" {
				t.Fatalf("no `nova-decide outcome` call; every return records its outcome\nargv:\n%s", strings.Join(arglogLines(t, arglog), "\n"))
			}
			for _, want := range []string{"--log " + routeLog, "--unit-id card-10", "--result " + tc.result} {
				if !strings.Contains(found, want) {
					t.Errorf("the outcome call has no %q: %q", want, found)
				}
			}
		})
	}
}

// The red team's item 9 (report of T03 at 98e3f3a9), with no live receipt of its own at
// the time because accept was not wired into harvest yet: once it is, pushing
// `branch:branch` re-resolves the branch IN THE WORKER'S CLONE at push time. A worker
// that commits again between the gate's verdict and the push gets the second commit
// published under an ACCEPT OK that never saw it. The push is by the sha the gate
// judged, to the branch's full ref.
func TestHarvestPushesTheShaTheGateJudgedNeverTheBranchRef(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/21")
	contract := "RESULT card-11 done"
	addGatedCard(t, root, "card-11", "0", "fix-red", contract, doneResult(contract))

	harvestGated(t, root, filepath.Join(root, "queue"), gateOK("card-11"), nil)

	var pushes []string
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			pushes = append(pushes, l)
		}
	}
	if len(pushes) != 1 {
		t.Fatalf("want one push, got %d: %v", len(pushes), pushes)
	}
	if strings.Contains(pushes[0], "rowan/fix-1:rowan/fix-1") {
		t.Errorf("the push re-resolves the branch in the worker's clone: %q", pushes[0])
	}
	if !strings.Contains(pushes[0], testHeadSHA+":refs/heads/rowan/fix-1") {
		t.Errorf("the push does not name the sha the gate judged and the branch's full ref: %q", pushes[0])
	}
}

// And when the worker moved HEAD under the gate, nothing is pushed at all.
func TestHarvestPushesNothingWhenTheHeadMovedUnderTheGate(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGitWithBase(t, specs, arglog, testBaseSHA)
	fakeGH(t, specs, arglog, "https://forge.invalid/mas-bandwidth/nova-tools/pull/22")
	contract := "RESULT card-12 done"
	addGatedCard(t, root, "card-12", "0", "fix-red", contract, doneResult(contract))

	// The gate's OK names a head the job's clone no longer has.
	g := &fakeGate{code: 0, line: "ACCEPT OK label=card-12 kind=fix-red head=deadbeefcafe base=0123456789ab tests=3 red_without=1 edits=- control=c0ffee bench=space cert=cert1 took=9s"}
	out, _, _ := harvestGated(t, root, filepath.Join(root, "queue"), g, nil)

	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("a head that moved under the gate was pushed: %q", l)
		}
	}
	if !strings.Contains(out, "gate=abstain") {
		t.Errorf("a head that moved under the gate is not a verdict on anything:\n%s", out)
	}
}
