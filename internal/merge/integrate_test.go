package merge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The write side's own tests. They drive GHIntegrate against a runner that records the
// argv and answers what a test scripted, so what is under test is THE COMMAND THIS TOOL
// WOULD RUN -- which is the only thing that matters about a shell-out.

// recordingGH is a Runner that records every invocation and answers from a script.
type recordingGH struct {
	calls [][]string
	reply map[string]string
	err   error
}

func (r *recordingGH) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.err != nil {
		return "", r.err
	}
	for key, out := range r.reply {
		if strings.Contains(strings.Join(args, " "), key) {
			return out, nil
		}
	}
	return "", nil
}

func (r *recordingGH) argv(i int) string { return strings.Join(r.calls[i], " ") }

// THE BODY IS AN ARGUMENT AND THE PACKAGE WRITES NO FILE FOR IT.
//
// rule 7 of docs/SPEC-MERGE.md holds internal/merge to four writing sites, and a temp
// file for a pull request body would be a fifth (TestNothingWritesIntoTheClonesWorkTree
// is the rule; this is the behaviour that keeps it true).
func TestTheWriteSidePassesEveryBodyAsAnArgumentAndNeverAsAFile(t *testing.T) {
	r := &recordingGH{reply: map[string]string{
		"pr view": `{"number": 9001, "url": "https://example.invalid/pull/9001"}`,
	}}
	g := NewGHIntegrate("o/n", time.Minute, r)

	ref, err := g.CreatePR(NewPR{Base: "dev", Head: "rowan/integration-17", Title: "t",
		Body: "- a basis line\n\n```\nBATCH OK name=integration-17\n```\n", Draft: true})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if ref.Number != 9001 {
		t.Errorf("the number is read back off the forge, got %d", ref.Number)
	}
	if err := g.Comment(1749, "the cause, named"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if err := g.ClosePR(1749, "Landed in batch #9001"); err != nil {
		t.Fatalf("ClosePR: %v", err)
	}
	if len(r.calls) != 4 {
		t.Fatalf("one create (create + view), one comment, one close is four calls, got %d", len(r.calls))
	}
	for i, want := range []string{
		"gh pr create --repo o/n --base dev --head rowan/integration-17 --title t --body ",
		"gh pr view rowan/integration-17 --repo o/n --json number,url",
		"gh pr comment 1749 --repo o/n --body the cause, named",
		"gh pr close 1749 --repo o/n --comment Landed in batch #9001",
	} {
		if !strings.HasPrefix(r.argv(i), want) {
			t.Errorf("call %d is %q, wanted it to begin %q", i, r.argv(i), want)
		}
	}
	for i := range r.calls {
		for _, forbidden := range []string{"--body-file", "--comment-file", "--force", "--auto"} {
			if strings.Contains(r.argv(i), forbidden) {
				t.Errorf("call %d holds %q: %s", i, forbidden, r.argv(i))
			}
		}
	}
	// THE BODY SURVIVES WHOLE: newlines, fences and all, as one argv element.
	body := r.calls[0][len(r.calls[0])-2]
	if !strings.Contains(body, "BATCH OK name=integration-17") || !strings.Contains(body, "\n") {
		t.Errorf("the body reached gh mangled: %q", body)
	}
}

// A HEAD OR BASE THAT GIT COULD READ AS AN OPTION NEVER REACHES THE FORGE (lesson 48):
// the check is at the arrival point, before a single call is made.
func TestTheWriteSideRefusesABranchNameAForgeCouldReadAsAnOption(t *testing.T) {
	r := &recordingGH{}
	g := NewGHIntegrate("o/n", time.Minute, r)

	if _, err := g.CreatePR(NewPR{Base: "dev", Head: "--upload-pack=touch /tmp/x", Title: "t"}); err == nil {
		t.Error("a head branch that is an option was accepted")
	}
	if _, err := g.CreatePR(NewPR{Base: "-dev", Head: "rowan/integration-17", Title: "t"}); err == nil {
		t.Error("a base branch that is an option was accepted")
	}
	if len(r.calls) != 0 {
		t.Errorf("a refused pull request still made %d calls: %v", len(r.calls), r.calls)
	}
}

// The fake is the one cmd/nova-merge's tests drive, so its own accounting is held here:
// what it records, in what order, and that an error is returned rather than recorded.
func TestTheFakeWriteSideRecordsEveryWriteInOrder(t *testing.T) {
	f := NewFakeIntegrateForge()

	first, err := f.CreatePR(NewPR{Base: "dev", Head: "rowan/integration-1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.CreatePR(NewPR{Base: "dev", Head: "rowan/integration-2"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 9001 || second.Number != 9002 {
		t.Errorf("the fake's numbers are 9001 upward, got %d and %d", first.Number, second.Number)
	}
	if err := f.Comment(7, "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.ClosePR(7, "b"); err != nil {
		t.Fatal(err)
	}
	if len(f.Created) != 2 || len(f.Comments) != 1 || len(f.Closed) != 1 {
		t.Fatalf("the fake recorded %d created, %d comments, %d closed", len(f.Created), len(f.Comments), len(f.Closed))
	}
	if f.Closed[0].PR != 7 || f.Closed[0].Body != "b" {
		t.Errorf("the close is recorded with its comment, got %+v", f.Closed[0])
	}

	f.CloseErr = fmt.Errorf("the forge said no")
	if err := f.ClosePR(8, "c"); err == nil {
		t.Error("a scripted error was swallowed")
	}
	if len(f.Closed) != 1 {
		t.Errorf("a failed close was recorded as a close: %d", len(f.Closed))
	}
}
