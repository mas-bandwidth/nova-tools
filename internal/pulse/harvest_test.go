package pulse

import (
	"bytes"
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
	return root, specs, arglog
}

// fakeGit records every git invocation and succeeds; nothing here has a repository.
func fakeGit(t *testing.T, specs, arglog string) {
	t.Helper()
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
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
	if err := os.WriteFile(cardPath, []byte(contract+"\nSTEP 1. go\n"), 0o644); err != nil {
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
	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20, Publish: true,
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
		"RESULT a sha=aaa\nDONE\nBRANCH br1\nREPO owner/repo\n")
	addCard(t, root, "b", "1", "flash", "RESULT b sha=bbb",
		"RESULT b sha=bbc\nDONE\nBRANCH br2\nREPO owner/repo\n")
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
			if !strings.Contains(l, "br1:br1") {
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
		"RESULT x sha=xxx\nABSTAIN -- idle 300s\nBRANCH bx\nREPO owner/repo\n")
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
		"RESULT done sha=ddd\nDONE\nBRANCH bd\nREPO owner/repo\n")

	writeTSV(t, filepath.Join(root, "queue.tsv"), []string{
		"q\tq1\tfix\tt1\tfix\n",
		"q\tq2\tfix\tt2\tfix\n",
		"q\tq3\tfix\tt3\tfix\n",
	})
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
			// Probe issue 11: the relaunch used to pass neither --slots nor --deadline, so
			// launch refused it every cycle and the loop's last step never ran.
			if !strings.Contains(l, "--deadline ") {
				t.Fatalf("relaunch must carry a --deadline, got: %s", l)
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
		"RESULT m sha=mmm\nDONE\nBRANCH bm\nREPO owner/repo\n")
	addCard(t, root, "n", "1", "flash", "RESULT n sha=nnn",
		"RESULT n sha=nnn\nBLOCKED head=abc123\nBRANCH bn\nREPO owner/repo\n")

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
