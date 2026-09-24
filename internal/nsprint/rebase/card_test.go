package rebase_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/rebase"
)

// TestRebaseCard is the #3094 done-when: a CONFLICTING landable fixture gets
// one rebase card; a clean rebase whose range-diff is empty keeps the reads;
// a conflict yields exactly one fix task and no second card on the same key.
func TestRebaseCard(t *testing.T) {
	t.Parallel()
	t.Run("conflicting_landable", testConflictingLandable)
	t.Run("clean_empty_range_diff", testCleanEmptyRangeDiff)
	t.Run("conflict_one_fix", testConflictOneFix)
}

func testConflictingLandable(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	rec := fixture(2522, head, "CONFLICTING", false, nil)
	book := rebase.NewBook()

	card, ok, err := consume.RebaseOnEval(book, rec)
	if err != nil || !ok {
		t.Fatalf("cut = ok %v err %v; want one card", ok, err)
	}
	if _, ok, err = consume.RebaseOnEval(book, rec); err != nil || ok {
		t.Fatalf("second cut = ok %v err %v; want the existing card and no second", ok, err)
	}
	if got := book.Cards(); len(got) != 1 || got[0].Key != card.Key {
		t.Fatalf("cards = %+v; want one %s", keysOf(got), card.Key)
	}
	wantKey := "rebase-2522-aaaaaaaa"
	if card.Key != wantKey {
		t.Fatalf("key = %s; want %s", card.Key, wantKey)
	}
	if card.Kind != rebase.KindScript || card.ModelCalls != 0 {
		t.Fatalf("kind=%s model_calls=%d; want script and 0", card.Kind, card.ModelCalls)
	}
	if !strings.HasPrefix(card.Script, "#!/bin/sh\n") ||
		!strings.Contains(card.Script, "KIND: script\n") ||
		!strings.Contains(card.Script, "MODEL_CALLS: 0\n") ||
		!strings.Contains(card.Script, "NO-SUBAGENTS\n") ||
		!strings.Contains(card.Script, "KEY: "+wantKey+"\n") {
		t.Fatalf("script header is not a zero-model card:\n%s", card.Script)
	}
	if !strings.Contains(card.Script, "git rebase ") || !strings.Contains(card.Script, "git range-diff ") {
		t.Fatalf("script does not rebase and range-diff:\n%s", card.Script)
	}
	if strings.Contains(card.Script, "You are a") || strings.Contains(card.Script, "[[") {
		t.Fatalf("script still addresses a model or left a token:\n%s", card.Script)
	}

	// A merged stack parent is the other cut, still one card, still a script.
	parent := fixture(2692, strings.Repeat("b", 40), "MERGEABLE", true, nil)
	pbook := rebase.NewBook()
	pc, ok, err := consume.RebaseOnEval(pbook, parent)
	if err != nil || !ok || pc.Key != "rebase-2692-bbbbbbbb" || pc.Kind != rebase.KindScript || len(pbook.Cards()) != 1 {
		t.Fatalf("stack parent card = %+v ok %v err %v cards %d", pc.Key, ok, err, len(pbook.Cards()))
	}

	// Not a rebase: clean, the old DIRTY word, or not landable. A ref that
	// cannot be quoted is an error and stores nothing.
	for _, rec := range []rebase.Record{
		fixture(1, strings.Repeat("c", 40), "MERGEABLE", false, nil),
		fixture(1, strings.Repeat("d", 40), "DIRTY", false, nil),
		notLandable(fixture(1, strings.Repeat("e", 40), "CONFLICTING", false, nil)),
	} {
		b := rebase.NewBook()
		_, ok, err := consume.RebaseOnEval(b, rec)
		if err != nil || ok || len(b.Cards()) != 0 {
			t.Fatalf("ineligible %+v cut ok %v err %v cards %d", rec.Mergeable, ok, err, len(b.Cards()))
		}
	}
	bad := fixture(3, strings.Repeat("f", 40), "CONFLICTING", false, nil)
	bad.Branch = "feature;touch"
	b := rebase.NewBook()
	if _, _, err := consume.RebaseOnEval(b, bad); err == nil || len(b.Cards()) != 0 {
		t.Fatalf("metacharacter branch stored a card, err=%v cards=%d", err, len(b.Cards()))
	}
}

func testCleanEmptyRangeDiff(t *testing.T) {
	t.Parallel()
	dir, bare, old := makeRepo(t, "clean")
	reads := []rebase.Read{
		{Who: "stella", Verdict: "APPROVE", Score: 9},
		{Who: "emma", Verdict: "APPROVE", Score: 8},
	}
	rec := fixture(2522, old, "CONFLICTING", false, reads)
	book := rebase.NewBook()
	card, ok, err := consume.RebaseOnEval(book, rec)
	if err != nil || !ok {
		t.Fatalf("cut: ok %v err %v", ok, err)
	}
	work := filepath.Join(filepath.Dir(dir), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rebase.Run(book, card, dir, work); err != nil {
		t.Fatal(err)
	}
	newHead, pushed := book.PushedHead(2522)
	if !pushed || newHead == "" || newHead == old {
		t.Fatalf("pushed head = %q; want a new head", newHead)
	}
	if remote := runGit(t, bare, "rev-parse", "refs/heads/feature"); remote != newHead {
		t.Fatalf("origin/feature = %s; want pushed %s", remote, newHead)
	}
	got := book.ReadsAt(2522, newHead)
	if len(got) != 2 || got[0].Who != "stella" || got[0].Verdict != "APPROVE" || got[0].Score != 9 || got[0].Head != newHead ||
		got[1].Who != "emma" || got[1].Verdict != "APPROVE" || got[1].Score != 8 || got[1].Head != newHead {
		t.Fatalf("reads at new head = %+v; want both approvals carried", got)
	}
	prev := book.ReadsAt(2522, old)
	if len(prev) != 2 || prev[0].Head != old || prev[1].Who != "emma" {
		t.Fatalf("old head lost its reads: %+v", prev)
	}
	if len(book.Fixes()) != 0 || len(book.Cards()) != 1 {
		t.Fatalf("clean rebase cards=%d fixes=%d; want 1 and 0", len(book.Cards()), len(book.Fixes()))
	}
	carry, err := rebase.Classify(card, work, filepath.Join(work, "range-diff.txt"))
	if err != nil || !carry {
		t.Fatalf("the rebase's own range-diff did not classify empty: carry %v err %v", carry, err)
	}

	// The other half of "only if": a real range-diff with a difference, and
	// a blank one, do not carry. A clean rebase that rewrites the message
	// pushes and drops the reads.
	dropDir, dropBare, dropOld := makeRepo(t, "drop")
	dropRec := fixture(2661, dropOld, "CONFLICTING", false, reads)
	dropBook := rebase.NewBook()
	dropCard, ok, err := consume.RebaseOnEval(dropBook, dropRec)
	if err != nil || !ok {
		t.Fatalf("drop cut: ok %v err %v", ok, err)
	}
	dropWork := filepath.Join(filepath.Dir(dropDir), "work")
	if err := os.MkdirAll(dropWork, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rebase.Run(dropBook, dropCard, dropDir, dropWork); err != nil {
		t.Fatal(err)
	}
	dropHead, pushed := dropBook.PushedHead(2661)
	if !pushed || dropHead == "" || dropHead == dropOld {
		t.Fatalf("non-empty range-diff did not push: %q", dropHead)
	}
	if remote := runGit(t, dropBare, "rev-parse", "refs/heads/feature"); remote != dropHead {
		t.Fatalf("origin/feature = %s; want %s", remote, dropHead)
	}
	if got := dropBook.ReadsAt(2661, dropHead); len(got) != 0 {
		t.Fatalf("non-empty range-diff carried reads: %+v", got)
	}
	if prev := dropBook.ReadsAt(2661, dropOld); len(prev) != 2 || prev[0].Who != "stella" {
		t.Fatalf("drop lost the old reads: %+v", prev)
	}
	carry, err = rebase.Classify(dropCard, dropWork, filepath.Join(dropWork, "range-diff.txt"))
	if err != nil || carry {
		t.Fatalf("changed range-diff classified carry=%v err %v", carry, err)
	}
	blank := filepath.Join(dropWork, "blank.txt")
	if err := os.WriteFile(blank, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	carry, err = rebase.Classify(dropCard, dropWork, blank)
	if err != nil || carry {
		t.Fatalf("blank range-diff classified carry=%v err %v; a blank file is not evidence", carry, err)
	}
}

func testConflictOneFix(t *testing.T) {
	t.Parallel()
	dir, bare, old := makeRepo(t, "conflict")
	reads := []rebase.Read{{Who: "stella", Verdict: "APPROVE", Score: 9, Head: old}}
	rec := fixture(2141, old, "CONFLICTING", false, reads)
	book := rebase.NewBook()
	card, ok, err := consume.RebaseOnEval(book, rec)
	if err != nil || !ok {
		t.Fatalf("cut: ok %v err %v", ok, err)
	}
	work := filepath.Join(filepath.Dir(dir), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rebase.Run(book, card, dir, work); err != nil {
		t.Fatal(err)
	}
	// The same conflict again must not open a second task or a second card.
	if err := rebase.Run(book, card, dir, work); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = consume.RebaseOnEval(book, rec); err != nil || ok {
		t.Fatalf("eval after conflict = ok %v err %v; want no second card", ok, err)
	}
	if len(book.Cards()) != 1 || book.Cards()[0].Key != card.Key {
		t.Fatalf("cards = %d; want the one card %s", len(book.Cards()), card.Key)
	}
	want := fmt.Sprintf("2141:%s:conflict", old)
	fixes := book.Fixes()
	if len(fixes) != 1 || fixes[0].Key != want || fixes[0].Author != "johnny" || fixes[0].Head != old || fixes[0].Number != 2141 {
		t.Fatalf("fixes = %+v; want one task %s for johnny", fixes, want)
	}
	if _, pushed := book.PushedHead(2141); pushed {
		t.Fatal("a conflict pushed a head")
	}
	if remote := runGit(t, bare, "rev-parse", "refs/heads/feature"); remote != old {
		t.Fatalf("origin/feature moved on conflict: %s want %s", remote, old)
	}
	if got := book.ReadsAt(2141, old); len(got) != 1 || got[0].Who != "stella" || got[0].Head != old {
		t.Fatalf("conflict changed reads: %+v", got)
	}
	if head := runGit(t, dir, "rev-parse", "HEAD"); head != old {
		t.Fatalf("HEAD after abort = %s; want %s", head, old)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("checkout left dirty:\n%s", status)
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", name)); !os.IsNotExist(err) {
			t.Fatalf("rebase left %s behind", name)
		}
	}
}

func fixture(pr int, head, mergeable string, parent bool, reads []rebase.Read) rebase.Record {
	return rebase.Record{
		Repo:              "example.test/nova-tools",
		Number:            pr,
		Head:              head,
		Base:              "dev",
		Branch:            "feature",
		Author:            "johnny",
		Mergeable:         mergeable,
		WouldLand:         true,
		StackParentMerged: parent,
		Reads:             reads,
	}
}

func notLandable(rec rebase.Record) rebase.Record {
	rec.WouldLand = false
	return rec
}

func keysOf(cards []rebase.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Key
	}
	return out
}

// makeRepo builds a local origin and a checkout. kind clean rebases without
// a patch change; drop rebases cleanly but a commit hook rewrites the
// message so range-diff is not empty; conflict edits the same line on dev.
func makeRepo(t *testing.T, kind string) (dir, bare, oldHead string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	dir = filepath.Join(root, "repo")
	runGit(t, root, "init", "--bare", "-q", bare)
	runGit(t, root, "init", "-q", "-b", "dev", dir)
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "t")
	if kind == "drop" {
		hook := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
		body := "#!/bin/sh\nprintf '\\nnote\\n' >> \"$1\"\n"
		if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f", "base\n")
	runGit(t, dir, "add", "f")
	runGit(t, dir, "commit", "-q", "-m", "base")
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-q", "origin", "dev")
	runGit(t, dir, "checkout", "-q", "-b", "feature")
	if kind == "conflict" {
		write("f", "feature\n")
		runGit(t, dir, "add", "f")
	} else {
		write("g", "feature\n")
		runGit(t, dir, "add", "g")
	}
	runGit(t, dir, "commit", "-q", "-m", "feature work")
	oldHead = runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "push", "-q", "origin", "feature")
	runGit(t, dir, "checkout", "-q", "dev")
	if kind == "conflict" {
		write("f", "landed\n")
		runGit(t, dir, "add", "f")
		runGit(t, dir, "commit", "-q", "-m", "landed on dev")
	} else {
		write("h", "more\n")
		runGit(t, dir, "add", "h")
		runGit(t, dir, "commit", "-q", "-m", "more on dev")
	}
	runGit(t, dir, "push", "-q", "origin", "dev")
	return dir, bare, oldHead
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
