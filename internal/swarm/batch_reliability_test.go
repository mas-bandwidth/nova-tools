package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The reliability tests for the three pit-stop faults of 2026-09-15: a batch that lost 34
// cards to one card's admission refusal (#529), two batches that shared a slot and lost both
// cards (#457), and an abstain row that named no reason, which cost the coordinator a RESULT
// read per card (#461). Each test is named after the sentence the fault broke.

// writeCardsModels writes a TSV of label, slot, model, card-path, one line per card, and
// returns its path. Unlike writeCards it takes the model per card, because an admission
// refusal is a property of the card's model as well as its text.
func writeCardsModels(t *testing.T, dir string, cards [][3]string) string {
	t.Helper()
	var b strings.Builder
	for i, c := range cards {
		path := writeCard(t, dir, c[0]+".card", c[2])
		b.WriteString(c[0] + "\t" + itoa(i+1) + "\t" + c[1] + "\t" + path + "\n")
	}
	path := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// markingRunner is fakeRunner plus one marker file per label it was started for, so a test
// can prove a refused card's runner never ran while the others did.
func markingRunner(t *testing.T, dir, marks string) string {
	t.Helper()
	if err := os.MkdirAll(marks, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "marking.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; card=\"$4\"; root=\"$5\"\n" +
		"touch " + strconv.Quote(marks) + "/\"$label\"\n" +
		"path=\"$root/$slot/jobs/$label/RESULT.md\"\n" +
		"mkdir -p \"$(dirname \"$path\")\"\n" +
		"line1=$(sed -n 1p \"$card\")\n" +
		"line2=$(sed -n 2p \"$card\")\n" +
		"printf '%s\\n%s\\n' \"$line1\" \"$line2\" > \"$path\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// deepSeekCard is a well-shaped DeepSeek card: a lowercase contract line, a role line and
// STEP 1 with the clone, then whatever body the test hands it.
func deepSeekCard(label, body string) string {
	return "RESULT: " + label + "\n" +
		"You are a worker on nova-tools.\n" +
		"STEP 1 cd /tmp/work && git clone https://example.invalid/repo.git\n" +
		body
}

// ISSUE #529: batch admission is per card. One card refused at admission is one ABSTAIN row
// on the packet; the other cards run. The fault: one card whose quoted issue text held the
// word "launcher" made ADMIT REFUSED for the whole batch, and 34 cards never ran.
func TestBatchAdmissionIsPerCard(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	marks := filepath.Join(dir, "marks")
	tsv := writeCardsModels(t, dir, [][3]string{
		{"a", "model", "RESULT: a\nall green"},
		// A capitalised contract block under a DeepSeek model: refused at admission.
		{"b", "opencode/deepseek-v4-flash", "THIS IS A CAPITALISED CONTRACT BLOCK\nSTEP 1 clone the repo\n"},
		{"c", "model", "RESULT: c\ndone and clean"},
	})
	runner := markingRunner(t, dir, marks)
	code, out, errs := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 1 {
		t.Fatalf("a batch with one refused card exits 1, got %d:\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=3 done=2 abstain=1") {
		t.Fatalf("the refused card is one abstain and the other cards run:\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: ABSTAIN reason=admission card-shape: capitalised contract block") {
		t.Fatalf("the refused card is scored on its own line, naming the admission reason:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") || !strings.Contains(out, "c slot=3: done and clean") {
		t.Fatalf("every other card runs and is folded:\n%s", out)
	}
	if !strings.Contains(errs, "ADMIT REFUSED b card-shape: capitalised contract block") {
		t.Fatalf("the refusal is still said once, by name, on stderr:\n%s", errs)
	}
	for _, label := range []string{"a", "c"} {
		if _, err := os.Stat(filepath.Join(marks, label)); err != nil {
			t.Fatalf("card %s must have run: %v", label, err)
		}
	}
	if _, err := os.Stat(filepath.Join(marks, "b")); err == nil {
		t.Fatal("a refused card is never launched")
	}
}

// ISSUE #529: the practice-17 card-shape word check applies to lines 1-3 -- the contract
// line, the role line and STEP 1 -- not to quoted text further down the card. A card that
// quotes an issue mentioning a launcher is admitted; a card that tells the worker to use a
// launcher in its contract lines is still refused.
func TestCardShapeCheckCoversContractLinesOnly(t *testing.T) {
	quoted := deepSeekCard("q",
		"STEP 2 read the issue below and make it true.\n"+
			"STEP 3 the issue says: \"the launcher shim is not the tool; fix the card\"\n"+
			"STEP last write RESULT.md: line 1 is line 1 of this card.\n")
	if why := admitWhyOf(t, t.TempDir(), "q", "opencode/deepseek-v4-flash", quoted); why != "" {
		t.Fatalf("a card whose quoted text mentions a launcher below line 3 is admitted, got %q", why)
	}
	inContract := "RESULT: r\n" +
		"Run the card through the launcher.\n" +
		"STEP 1 cd /tmp/work && git clone https://example.invalid/repo.git\n"
	why := admitWhyOf(t, t.TempDir(), "r", "opencode/deepseek-v4-flash", inContract)
	if !strings.Contains(why, "'launcher' in the contract lines") {
		t.Fatalf("a launcher named in lines 1-3 is still refused, got %q", why)
	}
	if !strings.Contains(why, "docs/WORKER-CARDS.md practice 17") {
		t.Fatalf("the refusal cites practice 17: %q", why)
	}
}

// writeBatchLock writes a slot's BATCH lock the way a running batch holds it.
func writeBatchLock(t *testing.T, root string, slot int, id string, pid int) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(slot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "BATCH")
	body := "id=" + id + " pid=" + strconv.Itoa(pid) + " at=" + time.Now().UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// deadPID returns a pid no process holds: a child that has already been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("starting a throwaway child: %v", err)
	}
	return cmd.Process.Pid
}

// ISSUE #457: a slot whose BATCH lock carries a live pid belongs to that batch. The card
// that named it is refused -- that card alone, per issue #529 -- and the slot is not shared.
// The fault: two concurrent batches took the same slot and both cards were lost.
func TestBatchRefusesLiveSlot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	marks := filepath.Join(dir, "marks")
	writeBatchLock(t, root, 1, "OTHER", os.Getpid())
	tsv := writeCardsModels(t, dir, [][3]string{
		{"a", "model", "RESULT: a\nall green"},
		{"b", "model", "RESULT: b\ndone and clean"},
	})
	runner := markingRunner(t, dir, marks)
	code, out, errs := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 1 {
		t.Fatalf("a batch that met a held slot exits 1, got %d:\n%s\n%s", code, out, errs)
	}
	want := "ADMIT REFUSED slot=1 held-by=OTHER pid=" + strconv.Itoa(os.Getpid())
	if !strings.Contains(errs, want) {
		t.Fatalf("the refusal names the slot, the holding batch and its pid:\nwant %q\ngot  %q", want, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1") {
		t.Fatalf("only the card on the held slot abstains; the other card runs:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=admission slot=1 held-by=OTHER pid="+strconv.Itoa(os.Getpid())) {
		t.Fatalf("the held card is scored on its own line:\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: done and clean") {
		t.Fatalf("the card on a free slot runs:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(marks, "a")); err == nil {
		t.Fatal("a card whose slot is held is never launched")
	}
	if _, err := os.Stat(filepath.Join(root, "1", "BATCH")); err != nil {
		t.Fatalf("another batch's live lock is left where it was: %v", err)
	}
}

// ISSUE #457: a BATCH lock whose pid is dead is stale, and the batch takes the slot over,
// saying so once. The lock it writes is its own, and it is gone when the slot ends.
func TestBatchTakesOverStaleSlotLock(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	lock := writeBatchLock(t, root, 1, "GONE", deadPID(t))
	tsv := writeCardsModels(t, dir, [][3]string{
		{"a", "model", "RESULT: a\nall green"},
	})
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 0 {
		t.Fatalf("a batch that took over a stale lock exits 0, got %d:\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(errs, "BATCH NOTE slot=1 stale-lock id=GONE taken") {
		t.Fatalf("the takeover is said once, naming the slot and the dead batch:\n%s", errs)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("the card runs on the slot it took over:\n%s", out)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the batch's own lock is removed at slot end, got err=%v", err)
	}
}

// reasonRunner is one runner that stands in for every way a card can end, switched on the
// card's own line 2: MISSING publishes nothing and exits clean, RC<n> exits <n> with no
// result, WRONG publishes a stranger's line 1, SLEEP never ends, and anything else is
// published as a result whose line 1 is the contract.
func reasonRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "reason.sh")
	body := "#!/bin/sh\n" +
		"label=\"$1\"; slot=\"$2\"; card=\"$4\"; root=\"$5\"\n" +
		"job=\"$root/$slot/jobs/$label\"\n" +
		"mkdir -p \"$job\"\n" +
		"line1=$(sed -n 1p \"$card\")\n" +
		"line2=$(sed -n 2p \"$card\")\n" +
		"case \"$line2\" in\n" +
		"  MISSING) exit 0 ;;\n" +
		"  RC7) exit 7 ;;\n" +
		"  WRONG) printf '%s\\n%s\\n' 'RESULT: someone-else' 'all green' > \"$job/RESULT.md\" ;;\n" +
		"  SLEEP) sleep 30 ;;\n" +
		"  DONE-RC5) printf '%s\\n%s\\n' \"$line1\" 'all green' > \"$job/RESULT.md\"; exit 5 ;;\n" +
		"  *) printf '%s\\n%s\\n' \"$line1\" \"$line2\" > \"$job/RESULT.md\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ISSUE #461: every abstain names one reason token, and a RESULT.md whose line 1 is the
// card's line 1 is done whatever the harness exit code, unless the card abstained itself.
// The fault: `abstain log=<n>` named no reason, so the coordinator read a RESULT per card
// to learn why.
func TestBatchAbstainNamesReason(t *testing.T) {
	for _, tc := range []struct {
		name     string
		model    string
		body     string
		deadline time.Duration
		idle     time.Duration
		want     string
	}{
		{
			name: "line1-mismatch", model: "model", body: "RESULT: x\nWRONG",
			deadline: 10 * time.Second, want: "x slot=1: ABSTAIN reason=line1-mismatch log=0",
		},
		{
			name: "no-result", model: "model", body: "RESULT: x\nMISSING",
			deadline: 10 * time.Second, want: "x slot=1: ABSTAIN reason=no-result log=0",
		},
		{
			name: "rc", model: "model", body: "RESULT: x\nRC7",
			deadline: 10 * time.Second, want: "x slot=1: ABSTAIN reason=rc=7 log=0",
		},
		{
			name: "idle", model: "model", body: "RESULT: x\nSLEEP",
			deadline: 30 * time.Second, idle: time.Second,
			want: "x slot=1: ABSTAIN reason=idle=1 log=0",
		},
		{
			name: "deadline", model: "model", body: "RESULT: x\nSLEEP",
			deadline: time.Second, want: "x slot=1: ABSTAIN reason=deadline log=0",
		},
		{
			name: "card-abstain", model: "model", body: "RESULT: x\nABSTAIN the repo needs credentials",
			deadline: 10 * time.Second, want: "x slot=1: ABSTAIN reason=card-abstain log=0",
		},
		{
			name: "admission", model: "opencode/deepseek-v4-flash",
			body:     "THIS IS A CAPITALISED CONTRACT BLOCK\nSTEP 1 clone the repo\n",
			deadline: 10 * time.Second,
			want:     "x slot=1: ABSTAIN reason=admission card-shape: capitalised contract block",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "root")
			tsv := writeCardsModels(t, dir, [][3]string{{"x", tc.model, tc.body}})
			runner := reasonRunner(t, dir)
			var code int
			var out, errs string
			if tc.idle > 0 {
				code, out, errs = runBatchIdle(t, tsv, root, runner, tc.deadline, tc.idle)
			} else {
				code, out, errs = runBatch(t, tsv, root, runner, tc.deadline)
			}
			if code != 1 {
				t.Fatalf("a batch whose only card abstains exits 1, got %d:\n%s\n%s", code, out, errs)
			}
			if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1") {
				t.Fatalf("the card abstains:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("the abstain names one reason token:\nwant %q\ngot\n%s", tc.want, out)
			}
		})
	}

	// And the other half of the sentence: a card that published its contract line is done
	// whatever the harness exit code was.
	t.Run("done-whatever-the-exit-code", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "root")
		tsv := writeCardsModels(t, dir, [][3]string{{"x", "model", "RESULT: x\nDONE-RC5"}})
		code, out, errs := runBatch(t, tsv, root, reasonRunner(t, dir), 10*time.Second)
		if code != 0 {
			t.Fatalf("a published result is done whatever the exit code, exits 0, got %d:\n%s\n%s", code, out, errs)
		}
		if !strings.Contains(out, "BATCH B1 n=1 done=1 abstain=0") || !strings.Contains(out, "x slot=1: all green") {
			t.Fatalf("a result whose line 1 is the contract line is done, rc=5 and all:\n%s", out)
		}
	})
}
