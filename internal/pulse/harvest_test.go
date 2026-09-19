package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupPulse makes a root, puts the fakes at the front of PATH and hands back the spec
// directory this test teaches them in and the argv log they all record into.
//
// The fixtures here were POSIX sh scripts logging to $ARGLOG. Windows executes neither the
// script nor the `$@`, so the real git and gh answered instead and harvest pushed nothing:
// pushed=0 where the test wants pushed=1. Every one of them is now a Go fake.
func setupPulse(t *testing.T) (root, specs, arglog string) {
	t.Helper()
	root = t.TempDir()
	specs = fakePATH(t)
	arglog = filepath.Join(root, "argv.log")
	// The pulse table `launch` writes beside every pulse: its id, its card count, and
	// the width and deadline it ran with. Rule 15's relaunch reads the last two and
	// passes them to the launch subprocess, which requires both (issue #1819).
	writePulseTable(t, root, "p1", 1, 4, "300")
	return root, specs, arglog
}

// writePulseTable writes <root>/pulses/<id>.tsv exactly as launch's record() does.
func writePulseTable(t *testing.T, root, id string, cards, slots int, deadline string) {
	t.Helper()
	dir := filepath.Join(root, "pulses")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("pulse-%s\t%d\t%d\t%s\n", id, cards, slots, deadline)
	if err := os.WriteFile(filepath.Join(dir, id+".tsv"), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
}

// queueCard writes a card file and the queue.tsv row `launch --queue` writes for its
// overflow: label<TAB>model<TAB>card, three fields (issue #1820).
func queueCard(t *testing.T, root, label string) string {
	t.Helper()
	dir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(dir, label+".md")
	if err := os.WriteFile(card, []byte("RESULT "+label+" sha=000000000000\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "queue.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(label + "\tflash\t" + card + "\n"); err != nil {
		t.Fatal(err)
	}
	return card
}

// fakeGit records every git invocation and succeeds, and answers `remote get-url origin`
// with the repository the fixtures' cards are all for.
//
// It used to answer NOTHING to that question, and the harvest pushed anyway -- which is the
// defect Johnny held #1809 for, modelled in the fixture: a real job's clone always has an
// origin, because a card that pushes is a card that cloned. A harvest whose destination
// nothing but the worker's RESULT.md can name now refuses (resolveDestination,
// HARVEST REFUSED repo-unknown), so a fixture with no origin tests the refusal and not the
// push. Tests that want the refusal leave this rule out on purpose and say so.
func fakeGit(t *testing.T, specs, arglog string) {
	t.Helper()
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{originRule("owner/repo")}})
}

// originRule teaches a fake git to answer `git -C <dir> remote get-url origin` -- argument 3
// is the verb -- with a repository, the way every clone a card made does.
func originRule(repo string) fakeRule {
	return fakeRule{Arg: 3, Equals: "remote", Stdout: "https://forge.invalid/" + repo + ".git"}
}

// fakeGH records every gh invocation, refuses `gh pr view` (no PR exists yet) and answers
// `gh pr create` with the URL the test wants.
func fakeGH(t *testing.T, specs, arglog, prURL string) {
	t.Helper()
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "view", Exit: 1},
		{Arg: 2, Equals: "create", Stdout: prURL},
	}})
}

// addCard writes a card file (line 1 = contract) and its RESULT.md and a cards.tsv row.
func addCard(t *testing.T, root, label, slot, model, contract, result string) {
	t.Helper()
	cardDir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(cardDir, label+".md")
	// The card names its own repository: a harvest checks the RESULT.md's REPO line
	// against the card before it pushes anywhere, because a RESULT is a report and not
	// an instruction (issue #1824). Every card these tests fold is for owner/repo.
	if err := os.WriteFile(cardPath, []byte(contract+"\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, slot, "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if result != "" {
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(filepath.Join(root, "cards.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(label + "\t" + slot + "\t" + model + "\t" + cardPath + "\n")
}

func runHarvest(t *testing.T, root string) (string, string) {
	t.Helper()
	return runHarvestWithClones(t, root, nil)
}

// runHarvestWithClones is runHarvest with the COORDINATOR's own `--clone`, which is where
// the destination comes from when the root has no cards.tsv and so no launch record at all
// -- a bare swarm root (Johnny's HOLD of #1809 at 7f692ef6). A root whose cards name their
// REPO needs none of it.
func runHarvestWithClones(t *testing.T, root string, clones []string) (string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20, Clones: clones,
		Stdout: &out, Stderr: &errs,
	})
	_ = code
	return out.String(), errs.String()
}

func arglogLines(t *testing.T, arglog string) []string {
	t.Helper()
	raw, err := os.ReadFile(arglog)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestHarvestPushesOnlyOnLine1Match(t *testing.T) {
	root, specs, arglog := setupPulse(t)

	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/42")

	addCard(t, root, "a", "1", "flash", "RESULT a sha=aaa",
		"RESULT a sha=aaa\nDONE\nBRANCH rowan/br1\nREPO owner/repo\n")
	addCard(t, root, "b", "1", "flash", "RESULT b sha=bbb",
		"RESULT b sha=bbc\nDONE\nBRANCH rowan/br2\nREPO owner/repo\n")
	addCard(t, root, "c", "1", "flash", "RESULT c sha=ccc",
		"RESULT c sha=ccc\nDONE\nBRANCH main\nREPO owner/repo\n")

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") {
		t.Fatalf("want pushed=1, got:\n%s", out)
	}
	if !strings.Contains(out, "prs=1") {
		t.Fatalf("want prs=1, got:\n%s", out)
	}
	if !strings.Contains(out, "mismatch=2") {
		t.Fatalf("want mismatch=2, got:\n%s", out)
	}

	alls := arglogLines(t, arglog)
	pushes := 0
	for _, l := range alls {
		if strings.HasPrefix(l, "git push ") {
			pushes++
			if !strings.Contains(l, "rowan/br1:rowan/br1") {
				t.Fatalf("push must name the branch by explicit refspec, got: %s", l)
			}
			if !strings.Contains(l, "https://") {
				t.Fatalf("push must be to an explicit https url, got: %s", l)
			}
		}
		if l == "git push" {
			t.Fatalf("bare git push in argv log: %s", l)
		}
	}
	if pushes != 1 {
		t.Fatalf("want exactly one push, got %d:\n%s", pushes, strings.Join(alls, "\n"))
	}
	created := 0
	for _, l := range alls {
		if strings.HasPrefix(l, "gh pr create") {
			created++
			if !strings.Contains(l, "--draft") {
				t.Fatalf("PR must be a draft, got: %s", l)
			}
		}
	}
	if created != 1 {
		t.Fatalf("want exactly one draft PR, got %d", created)
	}
}

func TestHarvestAbstainGoesToRetry(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/9")

	// One ABSTAIN with a refusal in the harness log, one card with no RESULT.md at all.
	addCard(t, root, "x", "1", "flash", "RESULT x sha=xxx",
		"RESULT x sha=xxx\nABSTAIN -- idle 300s\nBRANCH rowan/bx\nREPO owner/repo\n")
	job := filepath.Join(root, "1", "jobs", "x")
	os.WriteFile(filepath.Join(job, "harness.log"), []byte("running\npermission denied: /etc\n"), 0o644)

	addCard(t, root, "y", "1", "flash", "RESULT y sha=yyy", "")

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "abstain=2") {
		t.Fatalf("want abstain=2, got:\n%s", out)
	}
	if !strings.Contains(out, "retry=2") {
		t.Fatalf("want retry=2, got:\n%s", out)
	}

	rty, err := os.ReadFile(filepath.Join(root, "retry.tsv"))
	if err != nil || !strings.Contains(string(rty), "permission denied") {
		t.Fatalf("retry.tsv must carry the harness log's last refusal line:\n%s", rty)
	}
	if len(nonempty(string(rty))) != 2 {
		t.Fatalf("want two retry rows, got:\n%s", rty)
	}

	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("an abstain must not push or open a PR, got: %s", l)
		}
	}

	seen, err := os.ReadFile(filepath.Join(root, "seen.tsv"))
	if err != nil || strings.Count(string(seen), "\tretry\n") != 2 {
		t.Fatalf("seen.tsv must mark both cards retry:\n%s", seen)
	}
}

func TestHarvestRelaunchesQueueFirst(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/1")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE OK id=p2 n=5 free-before=5 queued=1 batches=1 deadline=300",
	}})

	// One done card to fold, then a queue of three, a read card, and two source items.
	addCard(t, root, "done", "1", "flash", "RESULT done sha=ddd",
		"RESULT done sha=ddd\nDONE\nBRANCH rowan/bd\nREPO owner/repo\n")

	// The queue is what `launch --queue` actually writes: three-field card rows, not
	// the five-field candidate table this test used to plant (issue #1820).
	queueCard(t, root, "q1")
	queueCard(t, root, "q2")
	queueCard(t, root, "q3")
	writeTSV(t, filepath.Join(root, "next.tsv"), []string{
		"pr\towner/repo#42\tread\ttitle\tread\n",
	})
	writeTSV(t, filepath.Join(root, "pool.tsv"), []string{
		"issues\t10\tfix\ts1\tfix\n",
		"issues\t11\tfix\ts2\tfix\n",
	})

	out, _ := runHarvest(t, root)

	hIdx := strings.Index(out, "HARVEST OK")
	pIdx := strings.Index(out, "PULSE OK")
	if hIdx < 0 || pIdx < 0 || hIdx > pIdx {
		t.Fatalf("HARVEST OK must precede the PULSE line:\n%s", out)
	}

	launched := false
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "nova-pulse launch") {
			launched = true
			if !strings.Contains(l, "--queue") {
				t.Fatalf("relaunch must pass --queue, got: %s", l)
			}
		}
	}
	if !launched {
		t.Fatalf("relaunch must invoke nova-pulse launch, got:\n%s", strings.Join(arglogLines(t, arglog), "\n"))
	}

	// The freshly cut cards.tsv must hold queue rows first, then the read card, then the source.
	cards, _ := os.ReadFile(filepath.Join(root, "cards.tsv"))
	rows := nonempty(string(cards))
	if len(rows) != 7 {
		t.Fatalf("want 7 cut cards (3 queue + 2 read + 2 source), got %d:\n%s", len(rows), cards)
	}
	labels := make([]string, 0, 7)
	for _, r := range rows {
		labels = append(labels, strings.Split(r, "\t")[0])
	}
	want := []string{"q1", "q2", "q3", "owner-repo-42", "owner-repo-1", "10", "11"}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("cards must be ordered queue-first then read then source, got %v", labels)
		}
	}
}

func TestThenGatedOnVerdict(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/5")

	addCard(t, root, "m", "1", "flash", "RESULT m sha=mmm",
		"RESULT m sha=mmm\nDONE\nBRANCH rowan/bm\nREPO owner/repo\n")
	addCard(t, root, "n", "1", "flash", "RESULT n sha=nnn",
		"RESULT n sha=nnn\nBLOCKED head=abc123\nBRANCH rowan/bn\nREPO owner/repo\n")

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("a merge-unready but done card is still pushed and opened, got:\n%s", out)
	}
	if !strings.Contains(out, "mismatch=1") {
		t.Fatalf("a BLOCKED card is a mismatch, got:\n%s", out)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "nova-merge") {
			t.Fatalf("nova-merge must appear in no argv log, got: %s", l)
		}
	}
}

// harvest-cuts-read-card-per-pr (SPEC-PULSE rule 13): every PR harvest opens
// yields one row in next.tsv — template read, or tone for a seed page — carrying
// the bench cost table's cheapest model that can hold it (local first), and the
// relaunch cuts it first with that model.
func TestHarvestCutsReadCardPerPR(t *testing.T) {
	harvestOne := func(t *testing.T, label string) (root, next, cards, out string) {
		t.Helper()
		root, specs, arglog := setupPulse(t)
		fakeGit(t, specs, arglog)
		fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/416")
		fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
			Stdout: "PULSE OK id=p2 n=1 free-before=1 queued=0 batches=1 deadline=300",
		}})

		// The bench cost table beside --templates (runHarvest passes
		// Templates=root): local is zero-cost and read-capable, so every read
		// card routes there, never to a paid route.
		writeTSV(t, filepath.Join(root, "benches.tsv"), []string{
			"model\tlocal-zero\tgo-flat\tzen-metered\n",
			"cost\tzero 0\tflat 0\tmetered 0.18\n",
			"capability\tread\tcode\treplay\n",
		})

		addCard(t, root, label, "1", "flash", "RESULT "+label+" sha=aaa",
			"RESULT "+label+" sha=aaa\nDONE\nBRANCH rowan/br1\nREPO owner/repo\n")

		out, _ = runHarvest(t, root)
		if !strings.Contains(out, "prs=1") {
			t.Fatalf("want prs=1, got:\n%s", out)
		}
		raw, err := os.ReadFile(filepath.Join(root, "next.tsv"))
		if err != nil {
			t.Fatalf("harvest must cut a read card into next.tsv: %v", err)
		}
		next = string(raw)
		raw, err = os.ReadFile(filepath.Join(root, "cards.tsv"))
		if err != nil {
			t.Fatalf("relaunch must cut cards.tsv: %v", err)
		}
		cards = string(raw)
		return root, next, cards, out
	}

	// One card after #416: a normal PR yields one read row routed local-first.
	_, next, cards, _ := harvestOne(t, "a")
	rows := nonempty(next)
	if len(rows) != 1 {
		t.Fatalf("want one read card in next.tsv after #416, got %d:\n%s", len(rows), next)
	}
	p := strings.Split(rows[0], "\t")
	if len(p) != 5 || p[0] != "pr" || p[1] != "owner/repo#416" || p[2] != "read" || p[4] != "read" {
		t.Fatalf("next.tsv row = %q, want pr<TAB>owner/repo#416<TAB>read<TAB><title><TAB>read", rows[0])
	}
	if model := cardsModel(cards, "owner-repo-416"); model != "local-zero" {
		t.Fatalf("read card model = %q, want local-zero (the bench cost table's cheapest capable route, local first)", model)
	}

	// A seed page yields the tone template, still routed local-first.
	_, next, cards, _ = harvestOne(t, "seed-front")
	rows = nonempty(next)
	if len(rows) != 1 {
		t.Fatalf("want one read card in next.tsv after #416, got %d:\n%s", len(rows), next)
	}
	p = strings.Split(rows[0], "\t")
	if len(p) != 5 || p[4] != "tone" {
		t.Fatalf("seed page next.tsv row = %q, want template tone", rows[0])
	}
	if model := cardsModel(cards, "owner-repo-416"); model != "local-zero" {
		t.Fatalf("seed read card model = %q, want local-zero (the bench cost table's cheapest capable route, local first)", model)
	}
}

// cardsModel returns the model column of the cards.tsv row with the given label.
func cardsModel(cards, label string) string {
	for _, r := range nonempty(cards) {
		p := strings.Split(r, "\t")
		if len(p) == 4 && p[0] == label {
			return p[2]
		}
	}
	return "<missing>"
}

func writeTSV(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nonempty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
