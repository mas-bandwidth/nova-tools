package pulse

import (
	"bytes"
	"os"
	"path/filepath"
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
		Kind: "read", Repo: "mas-bandwidth/nova-tools", PR: 812, Head: "abc123def456", Base: "dev",
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
	want := "RESULT: CARD-1 nova-tools #601 fixed with its red test first: sweep never revisits a queued check"
	if got := strings.SplitN(card, "\n", 2)[0]; got != want {
		t.Errorf("line 1 = %q\nwant      %q", got, want)
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

// cut-kind-read-diffs-against-the-pr-base: today's read of #848 held a hunk of ci.yml that
// was already on dev, because nothing on the card said which branch the PR merges into and
// the reader took main. The base is on the card, the diff command names it, and a read card
// cannot be cut without it.
func TestCutKindReadDiffsAgainstThePRBase(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errs, card := cutKind(t, CutKindInput{
		Kind: "read", Repo: "mas-bandwidth/nova-tools", PR: 848, Head: "5f544272a1b0", Base: "dev",
		Title: "pulse.toml is the configuration", Out: out, Queue: queue,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if !strings.Contains(card, "BASE: dev") {
		t.Errorf("the card does not carry its base branch:\n%s", card)
	}
	if !strings.Contains(card, "git diff origin/dev...5f544272a1b0") {
		t.Errorf("the card does not name the diff against its base:\n%s", card)
	}
	if strings.Contains(card, "origin/main...") {
		t.Errorf("the card diffs against main, which is not this PR's base:\n%s", card)
	}

	// A read card with no base is refused at the cutter, which is the only place a card is
	// made: no card, and no reader guessing.
	code, _, errs, _ = cutKind(t, CutKindInput{
		Kind: "read", Repo: "mas-bandwidth/nova-tools", PR: 848, Head: "5f544272a1b0",
		Title: "no base", Out: out, Queue: queue,
	})
	if code != 2 || !strings.Contains(errs, "--base is required for a read card") {
		t.Errorf("a read cut with no base: exit %d, stderr %q", code, errs)
	}
}

// cut-rewrites-the-model-line: the route is the queue's configuration, and a card that
// names another model is rewritten at cut and says so on its one line (Glenn 2026-09-16
// 17:55Z; class M, #828).
func TestCutKindRewritesTheModelLine(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	body := filepath.Join(dir, "body.md")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body, []byte("MODEL: opencode/kimi-k2.7-code\nSTEP 1. do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, line, errs, card := cutKind(t, CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 828, Title: "a card on the wrong route",
		BodyFile: body, Out: out, Queue: queue, RewriteModelTo: DefaultRoute,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if !strings.Contains(card, "MODEL: "+DefaultRoute) || strings.Contains(card, "kimi") {
		t.Errorf("the MODEL line was not rewritten:\n%s", card)
	}
	if !strings.Contains(line, "rewrote="+DefaultRoute) {
		t.Errorf("the CUT line does not say it rewrote the route: %q", line)
	}
	if !CheckStamp(card).Valid {
		t.Errorf("the rewritten card's stamp does not match its body: a rewrite after the stamp is an edited card")
	}
}
