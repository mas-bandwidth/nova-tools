package pulse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func cutKind(t *testing.T, in CutKindInput) (int, string, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	code := CutKind(in)
	card := ""
	if matches, _ := filepath.Glob(filepath.Join(in.Out, "card-*.md")); len(matches) == 1 {
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		card = string(raw)
	}
	return code, out.String(), errs.String(), card
}

// cut-kind-read-line-one: a read card's line 1 is the contract the harvest matches, and its
// number comes from the queue's state file, never from a hand (issue #828, classes B and F).
func TestCutKindReadWritesTheContractLine(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "read", Repo: "mas-bandwidth/nova-tools", PR: 812, Head: "abc123def456",
		Title: "reap kills the orphans", Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	want := "RESULT: CARD-1 read of nova-tools PR812 at abc123def456 (reap kills the orphans)"
	if got := strings.SplitN(card, "\n", 2)[0]; got != want {
		t.Errorf("line 1 = %q\nwant      %q", got, want)
	}
	if !strings.Contains(card, "PR812: APPROVE|HOLD head=abc123def456") {
		t.Errorf("the read card asks for no verdict line:\n%s", card)
	}
	if !strings.Contains(strings.ToLower(card), "do not run go build") {
		t.Errorf("a read card is text-only and must say so:\n%s", card)
	}
	if !strings.Contains(line, "CUT CARD card=card-1.md") || !strings.Contains(line, "kind=read") {
		t.Errorf("the one line = %q", line)
	}
	if n := len(strings.Split(strings.TrimRight(line, "\n"), "\n")); n != 1 {
		t.Errorf("stdout is %d lines, want 1: %q", n, line)
	}
}

// cut-kind-fix: the fix card's line 1 names the issue and the red test first, and a prior
// attempt rides on the card so the worker never repeats it — "fix the prompt, not retry".
func TestCutKindFixCarriesTheRedTestAndPriorAttempts(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	body := filepath.Join(dir, "body.md")
	if err := os.WriteFile(body, []byte("STEP 1. reproduce the refusal\nSTEP 2. write the red test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 601, Title: "sweep never revisits a queued check",
		BodyFile: body, Prior: "card-590 abstained: reason=idle=300", Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	wantRE := regexp.MustCompile(`^RESULT: CARD-1 sha=[0-9a-f]{12} nova-tools #601 fixed with its red test first: sweep never revisits a queued check$`)
	line1 := strings.SplitN(card, "\n", 2)[0]
	if !wantRE.MatchString(line1) {
		t.Errorf("line 1 = %q\nwant to match %s", line1, wantRE.String())
	}
	if !strings.Contains(card, "Prior attempts: card-590 abstained: reason=idle=300") {
		t.Errorf("the fix card carries no Prior attempts line:\n%s", card)
	}
	if !strings.Contains(card, "STEP 2. write the red test") {
		t.Errorf("the --body-file is not on the card:\n%s", card)
	}

	// without --prior there is no Prior attempts line at all.
	_, _, _, plain := cutKind(t, CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 601, Title: "t",
		BodyFile: body, Out: filepath.Join(dir, "pending2"), Queue: queue,
	})
	if strings.Contains(plain, "Prior attempts:") {
		t.Errorf("a first attempt carries a Prior attempts line:\n%s", plain)
	}
}

// cut-kind-fix-hash: a fix card's line 1 carries the sha-12 of every line below it, the
// same binding renderValidated gives a validated card, so the contract line cannot drift
// from the steps it names (#1852 item 1).
func TestCutKindFixContractLineIsHashedOverEverythingBelowIt(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 601, Title: "sweep never revisits a queued check",
		Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	m := regexp.MustCompile(`^RESULT: CARD-1 sha=([0-9a-f]{12}) `).FindStringSubmatch(card)
	if m == nil {
		t.Fatalf("line 1 carries no sha=<sha12> token, so the contract line is unhashed:\n%s", card)
	}
	lines := strings.Split(card, "\n")
	sum := sha256.Sum256([]byte(strings.Join(lines[1:], "\n")))
	want := hex.EncodeToString(sum[:])[:12]
	if m[1] != want {
		t.Errorf("sha=%s, want %s (the sha-12 of every line below line 1)", m[1], want)
	}
}

// cut-kind-rebase: the rebase card's line 1 is the contract the harvest matches, and its
// steps carry the branch the worker checks out and the base it rebases onto.
func TestCutKindRebaseNamesTheBranchAndTheBase(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "rebase", Repo: "mas-bandwidth/nova-tools", PR: 1142,
		Branch: "rowan/impl-merge-rebase", Base: "dev",
		Title: "the rebase cutter", Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	want := "RESULT: CARD-1 nova-tools PR #1142 rebased onto dev with its conflicts resolved and its tests green: the rebase cutter"
	if got := strings.SplitN(card, "\n", 2)[0]; got != want {
		t.Errorf("line 1 = %q\nwant      %q", got, want)
	}
	if !strings.Contains(card, "git fetch -q origin dev rowan/impl-merge-rebase") || !strings.Contains(card, "git rebase origin/dev") {
		t.Errorf("the rebase steps do not fetch the base and the branch and rebase onto the base:\n%s", card)
	}
	if !strings.Contains(line, "kind=rebase") {
		t.Errorf("the one line = %q", line)
	}
}

// replay and spec cards are cut from the same numberer and carry their own shapes.
func TestCutKindReplayAndSpec(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	_, _, errs, replay := cutKind(t, CutKindInput{
		Kind: "replay", Repo: "mas-bandwidth/nova-tools", Names: "sweep-enqueues-once,reap-requeues-once",
		SpecLines: "505-560", Out: filepath.Join(dir, "a"), Queue: queue,
	})
	if !strings.HasPrefix(replay, "RESULT: CARD-1 nova-tools replays sweep-enqueues-once,reap-requeues-once named at spec lines 505-560, red first") {
		t.Errorf("replay line 1 = %q (stderr %s)", strings.SplitN(replay, "\n", 2)[0], errs)
	}
	_, _, _, spec := cutKind(t, CutKindInput{
		Kind: "spec", Repo: "mas-bandwidth/nova-tools", Title: "the six rules of pit stop 3",
		Out: filepath.Join(dir, "b"), Queue: queue,
	})
	if !strings.HasPrefix(spec, "RESULT: CARD-2 nova-tools spec: the six rules of pit stop 3") {
		t.Errorf("spec line 1 = %q", strings.SplitN(spec, "\n", 2)[0])
	}
}

// every refusal names its remedy, and an unknown kind is never guessed at.
func TestCutKindRefusalsNameTheirRemedy(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		in   CutKindInput
		want string
	}{
		{"unknown kind", CutKindInput{Kind: "probe", Repo: "o/n", Out: dir, Queue: dir}, "--kind"},
		{"read without a pr", CutKindInput{Kind: "read", Repo: "o/n", Head: "h", Out: dir, Queue: dir}, "--pr"},
		{"read without a head", CutKindInput{Kind: "read", Repo: "o/n", PR: 1, Out: dir, Queue: dir}, "--head"},
		{"fix without an issue", CutKindInput{Kind: "fix", Repo: "o/n", Title: "t", Out: dir, Queue: dir}, "--issue"},
		{"fix without a title", CutKindInput{Kind: "fix", Repo: "o/n", Issue: 1, Out: dir, Queue: dir}, "--title"},
		{"no repo", CutKindInput{Kind: "read", PR: 1, Head: "h", Out: dir, Queue: dir}, "--repo"},
		{"no queue", CutKindInput{Kind: "read", Repo: "o/n", PR: 1, Head: "h", Out: dir}, "--queue"},
		{"rebase without a branch", CutKindInput{Kind: "rebase", Repo: "o/n", PR: 1, Base: "dev", Title: "t", Out: dir, Queue: dir}, "--branch"},
		{"rebase without a base", CutKindInput{Kind: "rebase", Repo: "o/n", PR: 1, Branch: "rowan/x", Title: "t", Out: dir, Queue: dir}, "--base"},
	} {
		var out, errs bytes.Buffer
		in := c.in
		in.Stdout, in.Stderr = &out, &errs
		if code := CutKind(in); code != 2 {
			t.Errorf("%s: exit = %d, want 2", c.name, code)
		}
		if !strings.Contains(errs.String(), "CUT REFUSED") || !strings.Contains(errs.String(), c.want) || !strings.Contains(errs.String(), "(") {
			t.Errorf("%s: refusal = %q, want CUT REFUSED naming %s and a remedy", c.name, errs.String(), c.want)
		}
	}
}

// cut writes queue/lanes/{red,green,small,next}/ (docs/SPEC-JOBS.md section 5):
// all four directories are visible, a fix lands in red and a small,
// already-approved read lands in green.
func TestCutKindWritesPriorityLanes(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	code, _, errs, _ := cutKind(t, CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 601, Title: "the lanes exist",
		Out: filepath.Join(dir, "a"), Queue: queue,
	})
	if code != 0 {
		t.Fatalf("fix exit = %d, stderr=%s", code, errs)
	}
	for _, lane := range []string{"red", "green", "small", "next"} {
		if st, err := os.Stat(filepath.Join(queue, "lanes", lane)); err != nil || !st.IsDir() {
			t.Fatalf("cut did not write queue/lanes/%s/: %v", lane, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(queue, "lanes", "red", "*.card")); len(matches) != 1 {
		t.Fatalf("a fix card is not in the red lane: %v", matches)
	}
	code, _, errs, _ = cutKind(t, CutKindInput{
		Kind: "read", Repo: "mas-bandwidth/nova-tools", PR: 812, Head: "abc123def456", Title: "an approved read",
		Out: filepath.Join(dir, "b"), Queue: queue,
	})
	if code != 0 {
		t.Fatalf("read exit = %d, stderr=%s", code, errs)
	}
	if matches, _ := filepath.Glob(filepath.Join(queue, "lanes", "green", "*.card")); len(matches) != 1 {
		t.Fatalf("a read card is not in the green lane: %v", matches)
	}
}
