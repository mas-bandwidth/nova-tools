//go:build functional

package card_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestReadCopyDealtToBenchRendersALintedCard is the #3929 read-leg case: a
// primary whose work copy ended ok with a PR is review; card deal cuts a
// READ copy onto bench:b; the copy's record renders a KIND: read card that
// the card linter (the one card push runs) accepts, and that names the one
// way it ends.
func TestReadCopyDealtToBenchRendersALintedCard(t *testing.T) {
	mirror := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirror, "nova-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", mirror) // the repository check reads the mirror, never the network
	_, c := wstest.Start(t)
	ctx := context.Background()
	author, _ := taskcard.ParseConsumer("friend:f")
	bench, _ := taskcard.ParseConsumer("bench:b")
	c.HSet(ctx, author.DesiredKey(), "slots", "1")
	c.HSet(ctx, bench.DesiredKey(), "slots", "1")
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "r1", Where: "waiting", Stream: "swarm: cards", Kind: "build",
		Title: "one verb", Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#3929",
		By: "rowan", Fields: []string{"base", "dev", "base_sha", strings.Repeat("ab", 20), "paths", "cmd/nova-sprint/card_moves.go",
			"done_when", "go test ./internal/nsprint/taskcard -run TestTableMoves passes"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: author, N: 1, By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	// the work copy's card lints too
	work, err := card.RenderCopy(card.CopyCardFrom(d[0].Copy, c.HGetAll(ctx, taskcard.Key(d[0].Copy)).Val()))
	if err != nil {
		t.Fatal(err)
	}
	if err := card.LintCard(ctx, work); err != nil {
		t.Fatalf("work copy card refused: %v\n%s", err, work)
	}
	if _, err := taskcard.Work(ctx, c, author, "f", 1, false); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("cd", 20)
	c.HSet(ctx, "pr:nova-tools:3950", "head", head, "base", "dev")
	// the work copy's ok with a PR moves the primary to review (no reader
	// enrolled: no read copy yet, the deal cuts it)
	if e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, Repo: "nova-tools", PR: "3950",
		Head: head, By: "f"}); err != nil || len(e) != 1 || e[0].To != "review" || e[0].Next != "" {
		t.Fatalf("end ok with a PR %v %v", e, err)
	}
	r, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 1, By: "reconciler"})
	if err != nil || len(r) != 1 {
		t.Fatalf("read deal to the bench %v %v", r, err)
	}
	body, err := card.RenderCopy(card.CopyCardFrom(r[0].Copy, c.HGetAll(ctx, taskcard.Key(r[0].Copy)).Val()))
	if err != nil {
		t.Fatal(err)
	}
	if err := card.LintCard(ctx, body); err != nil {
		t.Fatalf("read copy card refused: %v\n%s", err, body)
	}
	// #4270: the read card asks for the SCORE line in RESULT.md and never
	// for nova-sprint (a sandboxed swarm model cannot run it)
	for _, want := range []string{"RESULT: r1.c2 ", "\nKIND: read\n", "\nROUTE: pro\n", "\nHEAD: " + strings.Repeat("cd", 20) + "\n",
		"line 2 is exactly\n  " + card.ScoreLine + "\n", "\n  ABSTAIN <why>\n", "A read edits nothing inside PATHS",
		"against dev@" + strings.Repeat("ab", 20) + ": CI at head, base, scope, then a score 1-10"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("read card lacks %q:\n%s", want, body)
		}
	}
	for _, never := range []string{"card end", "nova-sprint card", "--score"} {
		if strings.Contains(string(body), never) {
			t.Fatalf("read card tells the model to run %q:\n%s", never, body)
		}
	}
	// a record missing what the card needs is refused, never guessed
	if _, err := card.RenderCopy(card.CopyCard{ID: "x~1", Leg: "read", Repo: "nova-tools"}); err == nil {
		t.Fatal("rendered a read copy with no base, base_sha, paths, pr or head")
	}
}
