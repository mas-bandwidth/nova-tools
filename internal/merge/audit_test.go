package merge

import (
	"context"
	"strings"
	"testing"
)

// THE SWEEP THAT REMOVED 27, AS A VERB.
//
// On 2026-09-18 twenty-seven open pull requests carried GitHub's auto-merge, every one of
// them enabled hours earlier by a `gh pr merge` call on a pull request that was not green.
// Four of them reached the dev queue on their own while nobody was looking. The sweep that
// took them off was a shell loop; this is that loop, so that the state "auto-merge is
// enabled on something" is a thing a verb reports and clears rather than a thing somebody
// notices.

// fakeAuditHost is the forge the audit reads and writes, with no network in it.
type fakeAuditHost struct {
	open     []AutoMergePR
	listErr  error
	failOn   map[int]error
	disabled []int
}

func (f *fakeAuditHost) AutoMergePRs(ctx context.Context) ([]AutoMergePR, error) {
	return f.open, f.listErr
}

func (f *fakeAuditHost) DisableAutoMerge(ctx context.Context, pr int) error {
	if err := f.failOn[pr]; err != nil {
		return err
	}
	f.disabled = append(f.disabled, pr)
	return nil
}

// Every pull request the forge reports with auto-merge on is disabled, and the result
// names them in the order they were read.
func TestAuditDisablesEveryAutoMerge(t *testing.T) {
	h := &fakeAuditHost{open: []AutoMergePR{
		{Number: 1301, HeadRef: "rowan/impl-a"},
		{Number: 1307, HeadRef: "rowan/impl-b"},
	}}
	res, err := Audit(context.Background(), h, false)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if res.Found != 2 || res.Disabled != 2 || res.Failed != 0 {
		t.Fatalf("Audit = %+v, want found=2 disabled=2 failed=0", res)
	}
	if len(h.disabled) != 2 || h.disabled[0] != 1301 || h.disabled[1] != 1307 {
		t.Fatalf("disabled %v, want [1301 1307]", h.disabled)
	}
}

// A dry run reports and touches nothing: the count is the same, the forge is not written.
func TestAuditDryRunWritesNothing(t *testing.T) {
	h := &fakeAuditHost{open: []AutoMergePR{{Number: 1301, HeadRef: "rowan/impl-a"}}}
	res, err := Audit(context.Background(), h, true)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if res.Found != 1 || res.Disabled != 0 {
		t.Fatalf("Audit = %+v, want found=1 disabled=0", res)
	}
	if len(h.disabled) != 0 {
		t.Fatalf("a dry run wrote to the forge: %v", h.disabled)
	}
}

// One refusal does not end the pass: the other pull requests are still cleared, and the
// one that failed is counted and named.
func TestAuditCarriesOnPastOneRefusal(t *testing.T) {
	h := &fakeAuditHost{
		open:   []AutoMergePR{{Number: 1, HeadRef: "a"}, {Number: 2, HeadRef: "b"}, {Number: 3, HeadRef: "c"}},
		failOn: map[int]error{2: context.DeadlineExceeded},
	}
	res, err := Audit(context.Background(), h, false)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if res.Found != 3 || res.Disabled != 2 || res.Failed != 1 {
		t.Fatalf("Audit = %+v, want found=3 disabled=2 failed=1", res)
	}
	if len(res.Refused) != 1 || res.Refused[0] != 2 {
		t.Fatalf("Refused = %v, want [2]", res.Refused)
	}
}

// THE ONE ALLOWED SPELLING, pinned byte for byte. `gh pr merge <n> -R <repo>` with the
// flag missing MERGES the pull request, which is the accident this whole card is about, so
// the argument list is built in one place and asserted here.
func TestDisableAutoMergeArgsCarryTheDisableFlagAndNothingElse(t *testing.T) {
	args := disableAutoMergeArgs("mas-bandwidth/nova-tools", 1301)
	want := []string{"pr", "merge", "1301", "-R", "mas-bandwidth/nova-tools", "--disable-auto"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("disableAutoMergeArgs = %v, want %v", args, want)
	}
	for _, a := range args {
		switch a {
		case "--auto", "--merge", "--squash", "--rebase", "--admin", "--match-head-commit":
			t.Fatalf("the disable call carries %q, which makes it a merge: %v", a, args)
		}
	}
}

// The audit's reads and its one write go through the same guarded gh as everything else,
// and the read asks the forge for exactly the pull requests that carry an auto-merge.
func TestGHAuditReadsAutoMergeRequestsAndDisablesThroughGh(t *testing.T) {
	r := &recordRunner{out: "[]"}
	h := NewGHEnqueue("mas-bandwidth/nova-tools", 0, r)
	if _, err := h.AutoMergePRs(context.Background()); err != nil {
		t.Fatalf("AutoMergePRs: %v", err)
	}
	list := strings.Join(r.calls[0], " ")
	if !strings.Contains(list, "autoMergeRequest") || !strings.Contains(list, "--state open") {
		t.Errorf("the audit's read does not ask for the open pull requests' auto-merge: %s", list)
	}
	if err := h.DisableAutoMerge(context.Background(), 1301); err != nil {
		t.Fatalf("DisableAutoMerge: %v", err)
	}
	if got := strings.Join(r.calls[1], " "); !strings.Contains(got, "--disable-auto") {
		t.Errorf("the disable call is not a disable: %s", got)
	}
}
