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

// TestEveryCutKindCarriesItsReviewedExemplar holds #2498 S6 at the real cutter
// boundary. Both renderer families must put the one matching reviewed PR in the
// generated card; a catalog that only tells a human to add the link is not the
// card-linked contract.
func TestEveryCutKindCarriesItsReviewedExemplar(t *testing.T) {
	exemplarPRs := map[string]int{
		"read":       3483,
		"fix":        3061,
		"replay":     2792,
		"spec":       3162,
		"rebase":     2794,
		"guard":      2543,
		"recut":      2918,
		"port":       2751,
		"docs-guard": 3140,
		"report":     3337,
	}
	exemplars := make(map[string]string, len(exemplarPRs))
	for kind, pr := range exemplarPRs {
		exemplars[kind] = exemplarURL(pr)
	}
	v2 := map[string]bool{"recut": true, "fix": true, "port": true, "docs-guard": true, "report": true, "read": true}
	if len(exemplars) != len(CutKinds) || len(cardExemplars) != len(CutKinds) {
		t.Fatalf("exemplar coverage: test=%d source=%d kinds=%d", len(exemplars), len(cardExemplars), len(CutKinds))
	}

	for _, kind := range CutKinds {
		t.Run(kind, func(t *testing.T) {
			exemplar, ok := exemplars[kind]
			if !ok || exemplar == "" {
				t.Fatalf("CutKinds entry %q has no reviewed exemplar", kind)
			}
			dir := t.TempDir()
			in := CutKindInput{
				Kind: kind, Repo: "mas-bandwidth/nova-tools", Title: "exemplar contract",
				Out: filepath.Join(dir, "pending"), Queue: filepath.Join(dir, "queue"),
			}
			switch kind {
			case "read":
				in.PR, in.Head = 3553, "cf0e6d6765d4"
			case "fix":
				in.Issue = 2498
			case "replay":
				in.Names, in.SpecLines = "exemplar-link", "1-2"
			case "rebase":
				in.PR, in.Branch, in.Base = 3553, "stella/example", "dev"
			case "guard":
				in.Head = "cf0e6d6765d4"
			case "recut":
				in.ReviewerLine = "DISPOSITION who=reader verdict=HOLD reason=missing-link"
			}
			if v2[kind] {
				in.V2 = true
				in.Location = "internal/pulse/cutkind.go:1"
				in.TestPackage = "./internal/pulse"
				in.TestFunction = "TestEveryCutKindCarriesItsReviewedExemplar"
				in.TestCommand = "go test ./internal/pulse -run TestEveryCutKindCarriesItsReviewedExemplar"
				in.Paths = "internal/pulse/cutkind.go internal/pulse/cutkind_test.go"
				in.PreflightCmd = "make preflight"
				in.Symbol = "CutKind"
				in.RedWhen = "the generated card omits or mismatches Example to follow"
			}

			code, _, errs, card := cutKind(t, in)
			if code != 0 {
				t.Fatalf("cut %s: exit=%d stderr=%s", kind, code, errs)
			}
			want := "Example to follow: " + exemplar
			if strings.Count(card, want) != 1 {
				t.Fatalf("%s card has %d matching exemplar lines, want 1:\n%s", kind, strings.Count(card, want), card)
			}
			for otherKind, otherURL := range exemplars {
				if otherKind != kind && strings.Contains(card, "Example to follow: "+otherURL) {
					t.Fatalf("%s card carries %s exemplar:\n%s", kind, otherKind, card)
				}
			}
		})
	}
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

// cut --kind report writes a card. A nonsense kind is still not one of the kinds.
func TestCutKindAcceptsReportAndRefusesANonsenseKind(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "report", Repo: "mas-bandwidth/nova-tools", Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("KIND report is a kind this cutter accepts: exit %d, stderr=%s", code, errs)
	}
	if !strings.Contains(line, "kind=report") {
		t.Errorf("the one line = %q", line)
	}
	if got := strings.SplitN(card, "\n", 2)[0]; got != "RESULT: CARD-1 report of nova-tools" {
		t.Errorf("line 1 = %q", got)
	}
	if !strings.Contains(card, "Do not run go build") {
		t.Errorf("a report card is text-only and must say so:\n%s", card)
	}

	code, _, errs, card = cutKind(t, CutKindInput{
		Kind: "not-a-real-kind", Repo: "mas-bandwidth/nova-tools",
		Out: filepath.Join(dir, "nope"), Queue: queue,
	})
	if code != 2 || !strings.Contains(errs, "CUT REFUSED") || !strings.Contains(errs, "not-a-real-kind") {
		t.Fatalf("a nonsense kind is refused: exit %d stderr=%q", code, errs)
	}
	if card != "" {
		t.Fatalf("a refused kind wrote a card:\n%s", card)
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
		{"guard without a head", CutKindInput{Kind: "guard", Repo: "o/n", Out: dir, Queue: dir}, "--head"},
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

func TestCutKindGuardCardForbidsJudgingTheVerdict(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "guard", Repo: "mas-bandwidth/nova-tools", Head: "ddce356eabcd",
		Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if got := strings.SplitN(card, "\n", 2)[0]; got != "RESULT: CARD-1 guard of nova-tools at ddce356eabcd" {
		t.Errorf("line 1 = %q", got)
	}
	if !strings.Contains(card, "nova-review guard --repo ./repo --head ddce356eabcd") {
		t.Errorf("the card does not name the mechanical verb:\n%s", card)
	}
	if !strings.Contains(card, "COMPUTED, never judged") {
		t.Errorf("the card does not forbid judging:\n%s", card)
	}
	if strings.Contains(card, "last token") {
		t.Errorf("N/A and ABSTAIN end in reason/prose; the card must not copy the last token:\n%s", card)
	}
	if !strings.Contains(card, "status=") {
		t.Errorf("the card must name the status= field to copy, not a last token:\n%s", card)
	}
	if !strings.Contains(line, "kind=guard") {
		t.Errorf("the one line = %q", line)
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

func TestCutKindV2Templates(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")

	// 1. Recut kind generates Card Template v2
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind:          "recut",
		Repo:          "mas-bandwidth/nova-tools",
		Title:         "re-cut boundary failure",
		Location:      "internal/pulse/cut.go:42",
		TestPackage:   "./internal/pulse",
		TestFunction:  "TestBoundary",
		TestCommand:   "go test ./internal/pulse -run TestBoundary",
		Paths:         "internal/pulse/cut.go",
		ReviewerLine:  "DISPOSITION who=Rowan verdict=HOLD reason=test",
		PriorDiff:     "--- a/old\n+++ b/new\n",
		FailingOutput: "panic: nil pointer",
		PreflightCmd:  "make preflight",
		Symbol:        "TestBoundary",
		RedWhen:       "panic: nil pointer",
		Out:           filepath.Join(dir, "recut"),
		Queue:         queue,
	})
	if code != 0 {
		t.Fatalf("recut failed (code %d): %s", code, errs)
	}
	if !strings.Contains(line, "kind=recut") {
		t.Errorf("line = %q, want kind=recut", line)
	}
	if !strings.Contains(card, "COMMAND: go test ./internal/pulse -run TestBoundary") {
		t.Errorf("card missing COMMAND:\n%s", card)
	}
	if !strings.Contains(card, "make preflight") {
		t.Errorf("card missing preflight:\n%s", card)
	}
	if !strings.Contains(card, "## Inlined Evidence") {
		t.Errorf("card missing inlined evidence:\n%s", card)
	}
	if !strings.Contains(card, "## Exemplar (recut)") {
		t.Errorf("card missing exemplar:\n%s", card)
	}

	// 2. Fix with V2=true generates Card Template v2
	code, _, errs, fixCard := cutKind(t, CutKindInput{
		Kind:         "fix",
		V2:           true,
		Repo:         "mas-bandwidth/nova-tools",
		Issue:        123,
		Title:        "v2 fix card",
		Location:     "internal/pulse/queue.go:10",
		TestCommand:  "go test ./internal/pulse -run TestQueue",
		Paths:        "internal/pulse/queue.go",
		PreflightCmd: "make preflight",
		Symbol:       "QueuePop",
		RedWhen:      "empty queue panics",
		Out:          filepath.Join(dir, "fix-v2"),
		Queue:        queue,
	})
	if code != 0 {
		t.Fatalf("fix v2 failed (code %d): %s", code, errs)
	}
	if !strings.Contains(fixCard, "COMMAND: go test ./internal/pulse -run TestQueue") {
		t.Errorf("fix v2 missing COMMAND:\n%s", fixCard)
	}
	if !strings.Contains(fixCard, "SYMBOL: QueuePop") {
		t.Errorf("fix v2 missing SYMBOL:\n%s", fixCard)
	}
	if !strings.Contains(fixCard, "RED-WHEN: empty queue panics") {
		t.Errorf("fix v2 missing RED-WHEN:\n%s", fixCard)
	}
	if !strings.Contains(fixCard, "## Exemplar (fix)") {
		t.Errorf("fix v2 missing exemplar:\n%s", fixCard)
	}

	// 3. V2=true without Symbol or RedWhen is refused by cutter lint
	codeNoSym, _, errsNoSym, _ := cutKind(t, CutKindInput{
		Kind:        "fix",
		V2:          true,
		Repo:        "mas-bandwidth/nova-tools",
		Issue:       124,
		Title:       "missing symbol v2",
		Location:    "internal/pulse/queue.go:10",
		TestCommand: "go test ./internal/pulse -run TestQueue",
		Paths:       "internal/pulse/queue.go",
		RedWhen:     "some red condition",
		Out:         filepath.Join(dir, "fix-v2-nosym"),
		Queue:       queue,
	})
	if codeNoSym != 2 {
		t.Errorf("CutKind without --symbol returned %d, want 2", codeNoSym)
	}
	if !strings.Contains(errsNoSym, "--symbol is required for a v2 card") {
		t.Errorf("expected error about --symbol, got %q", errsNoSym)
	}

	codeNoRed, _, errsNoRed, _ := cutKind(t, CutKindInput{
		Kind:        "fix",
		V2:          true,
		Repo:        "mas-bandwidth/nova-tools",
		Issue:       125,
		Title:       "missing red-when v2",
		Location:    "internal/pulse/queue.go:10",
		TestCommand: "go test ./internal/pulse -run TestQueue",
		Paths:       "internal/pulse/queue.go",
		Symbol:      "QueuePop",
		Out:         filepath.Join(dir, "fix-v2-nored"),
		Queue:       queue,
	})
	if codeNoRed != 2 {
		t.Errorf("CutKind without --red-when returned %d, want 2", codeNoRed)
	}
	if !strings.Contains(errsNoRed, "--red-when is required for a v2 card") {
		t.Errorf("expected error about --red-when, got %q", errsNoRed)
	}

	// 4. Auto-v2 selection: port without --v2 flag automatically requires Symbol and RedWhen
	codeAutoNoSym, _, errsAutoNoSym, _ := cutKind(t, CutKindInput{
		Kind:    "port",
		V2:      false,
		Repo:    "mas-bandwidth/nova-tools",
		Title:   "port card auto-v2 missing symbol",
		RedWhen: "port missing target symbol",
		Out:     filepath.Join(dir, "port-auto-nosym"),
		Queue:   queue,
	})
	if codeAutoNoSym != 2 {
		t.Errorf("Auto-v2 cut without --symbol returned %d, want 2", codeAutoNoSym)
	}
	if !strings.Contains(errsAutoNoSym, "--symbol is required for a v2 card") {
		t.Errorf("expected auto-v2 refusal for missing --symbol, got %q", errsAutoNoSym)
	}

	// Auto-v2 selection with Symbol and RedWhen succeeds and emits Card Template v2
	codeAuto, _, errsAuto, autoCard := cutKind(t, CutKindInput{
		Kind:    "port",
		V2:      false,
		Repo:    "mas-bandwidth/nova-tools",
		Title:   "port card auto-v2 present symbol",
		Symbol:  "PortTarget",
		RedWhen: "port target missing",
		Out:     filepath.Join(dir, "port-auto-ok"),
		Queue:   queue,
	})
	if codeAuto != 0 {
		t.Fatalf("Auto-v2 cut with declarations failed (code %d): %s", codeAuto, errsAuto)
	}
	if !strings.Contains(autoCard, "SCHEMA: v2") {
		t.Errorf("Auto-v2 card missing SCHEMA: v2:\n%s", autoCard)
	}
	if !strings.Contains(autoCard, "SYMBOL: PortTarget") {
		t.Errorf("Auto-v2 card missing SYMBOL:\n%s", autoCard)
	}
	if !strings.Contains(autoCard, "RED-WHEN: port target missing") {
		t.Errorf("Auto-v2 card missing RED-WHEN:\n%s", autoCard)
	}

	// 5. Preserved legacy behavior: read card with V2=false and no auto-v2 triggers emits legacy card
	codeLegacy, _, errsLegacy, legacyCard := cutKind(t, CutKindInput{
		Kind:  "read",
		V2:    false,
		Repo:  "mas-bandwidth/nova-tools",
		PR:    101,
		Head:  "1234567890ab",
		Title: "legacy read card",
		Out:   filepath.Join(dir, "read-legacy"),
		Queue: queue,
	})
	if codeLegacy != 0 {
		t.Fatalf("Legacy read card failed (code %d): %s", codeLegacy, errsLegacy)
	}
	if strings.Contains(legacyCard, "SCHEMA: v2") {
		t.Errorf("Legacy read card should not contain SCHEMA: v2:\n%s", legacyCard)
	}
	if !strings.Contains(legacyCard, "RESULT: CARD-") || !strings.Contains(legacyCard, "read of nova-tools PR101") {
		t.Errorf("Legacy card missing legacy contract line:\n%s", legacyCard)
	}
}

// TestCutKindV2OperativeRegionBoundary runs Stella's A4 boundary through the real cutter
// (#2522): a card whose task body QUOTES broad staging is cut, a card whose body quotes a
// whole prior RUN block is cut with that block retained as data, the SUPPLIED operative
// command is what the lint reads — broad staging there is refused with no card written —
// and a body that declares a second operative region is refused naming the position.
func TestCutKindV2OperativeRegionBoundary(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	bodyFile := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	quoted := "Repair the deploy script.\n" +
		"The card under repair said: STEP: git add -A && git commit # do not run again\n" +
		"> Reviewer verdict: never run `git add --all`; stage declared PATHS only."
	code, _, errs, card := cutKind(t, CutKindInput{
		Kind:         "fix",
		V2:           true,
		Repo:         "mas-bandwidth/nova-tools",
		Issue:        2522,
		Title:        "quote the bypass as evidence",
		Location:     "internal/pulse/cut_template.go:1",
		TestCommand:  "go test ./internal/pulse -run TestValidateCardV2OperativeRegionBoundary",
		Paths:        "internal/pulse/cut_template.go",
		PreflightCmd: "make preflight",
		Symbol:       "ValidateCardV2",
		RedWhen:      "an operative git add -A passes the cutter lint",
		BodyFile:     bodyFile("quoted-body.md", quoted),
		Out:          filepath.Join(dir, "quoted"),
		Queue:        queue,
	})
	if code != 0 {
		t.Fatalf("cut refused a card that only quotes broad staging (code %d): %s", code, errs)
	}
	if !strings.Contains(card, "STEP: git add -A && git commit # do not run again") {
		t.Errorf("cut did not preserve the quoted evidence complete:\n%s", card)
	}
	region, err := OperativeRegion(card)
	if err != nil {
		t.Fatalf("cut card has no usable operative region: %v\n%s", err, card)
	}
	if len(region) != 2 {
		t.Errorf("operative region wants the test command and the preflight, got %+v", region)
	}

	// A whole prior RUN block quoted in the body is retained as data: the position the
	// template owns is the only region, so the quoted block declares nothing.
	quotedRegion := "The card under repair said:\n" + OperativeRegionMarker + "\n```sh\nSTEP: git add -A && git commit # do not run again\n```"
	codeQ, _, errsQ, quotedCard := cutKind(t, CutKindInput{
		Kind:         "fix",
		V2:           true,
		Repo:         "mas-bandwidth/nova-tools",
		Issue:        2523,
		Title:        "retain the prior RUN block",
		Location:     "internal/pulse/cut_template.go:1",
		TestCommand:  "go test ./internal/pulse -run TestValidateCardV2OperativeRegionBoundary",
		Paths:        "internal/pulse/cut_template.go",
		PreflightCmd: "make preflight",
		Symbol:       "ValidateCardV2",
		RedWhen:      "a quoted prior RUN block is read as a second region",
		BodyFile:     bodyFile("quoted-region-body.md", quotedRegion),
		Out:          filepath.Join(dir, "quoted-region"),
		Queue:        queue,
	})
	if codeQ != 0 {
		t.Fatalf("cut refused a card quoting a whole prior RUN block (code %d): %s", codeQ, errsQ)
	}
	if !strings.Contains(quotedCard, OperativeRegionMarker+"\n```sh\nSTEP: git add -A && git commit # do not run again\n```") {
		t.Errorf("cut did not retain the quoted prior RUN block complete:\n%s", quotedCard)
	}
	quotedReg, err := OperativeRegion(quotedCard)
	if err != nil {
		t.Fatalf("cut card has no usable operative region: %v\n%s", err, quotedCard)
	}
	for _, l := range quotedReg {
		if strings.Contains(l.Text, "git add -A") {
			t.Errorf("quoted body text entered the operative region: %+v", quotedReg)
		}
	}

	// The supplied operative command is what the lint reads: broad staging there is
	// refused through CutKind, and no card is written.
	outDir := filepath.Join(dir, "operative")
	codeBad, _, errsBad, badCard := cutKind(t, CutKindInput{
		Kind:         "fix",
		V2:           true,
		Repo:         "mas-bandwidth/nova-tools",
		Issue:        2524,
		Title:        "run the bypass",
		Location:     "internal/pulse/cut_template.go:1",
		TestCommand:  "git add -A && go test ./internal/pulse",
		Paths:        "internal/pulse/cut_template.go",
		PreflightCmd: "make preflight",
		Symbol:       "ValidateCardV2",
		RedWhen:      "an operative git add -A passes the cutter lint",
		BodyFile:     bodyFile("operative-body.md", "Repair the deploy script."),
		Out:          outDir,
		Queue:        queue,
	})
	if codeBad != 2 {
		t.Fatalf("cut accepted an operative git add -A (code %d), want 2", codeBad)
	}
	if !strings.Contains(errsBad, "forbidden operative broad staging") {
		t.Errorf("refusal does not name the broad staging: %q", errsBad)
	}
	if badCard != "" {
		t.Errorf("cut wrote a card it refused:\n%s", badCard)
	}
	if matches, _ := filepath.Glob(filepath.Join(outDir, "card-*.md")); len(matches) != 0 {
		t.Errorf("cut left a refused card behind: %v", matches)
	}

	// A body that declares the position itself is a second operative region: refused,
	// naming where the one region goes, and no card written.
	secondRegion := OperativeRegionHeading + "\n" + OperativeRegionMarker + "\n```sh\nSTEP: git add -A && git commit # do not run again\n```"
	secondDir := filepath.Join(dir, "second-region")
	codeSecond, _, errsSecond, secondCard := cutKind(t, CutKindInput{
		Kind:         "fix",
		V2:           true,
		Repo:         "mas-bandwidth/nova-tools",
		Issue:        2525,
		Title:        "declare a second region",
		Location:     "internal/pulse/cut_template.go:1",
		TestCommand:  "go test ./internal/pulse -run TestValidateCardV2OperativeRegionBoundary",
		Paths:        "internal/pulse/cut_template.go",
		PreflightCmd: "make preflight",
		Symbol:       "ValidateCardV2",
		RedWhen:      "a second operative region is obeyed instead of refused",
		BodyFile:     bodyFile("second-region-body.md", secondRegion),
		Out:          secondDir,
		Queue:        queue,
	})
	if codeSecond != 2 {
		t.Fatalf("cut accepted a body-declared second operative region (code %d), want 2", codeSecond)
	}
	if !strings.Contains(errsSecond, "declares a second operative region") {
		t.Errorf("refusal does not name the second region: %q", errsSecond)
	}
	if !strings.Contains(errsSecond, "first line of its single `## Run` section") {
		t.Errorf("refusal does not name the one position: %q", errsSecond)
	}
	if secondCard != "" {
		t.Errorf("cut wrote a card it refused:\n%s", secondCard)
	}
	if matches, _ := filepath.Glob(filepath.Join(secondDir, "card-*.md")); len(matches) != 0 {
		t.Errorf("cut left a refused card behind: %v", matches)
	}
}
