package read_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
)

// TestReadTargetOfEveryTaskShape: the three ways a read task names its PR
// resolve to the same target; a task that is not a read of one PR at one
// head is refused.
func TestReadTargetOfEveryTaskShape(t *testing.T) {
	t.Parallel()

	h := strings.Repeat("a", 40)
	for _, f := range []map[string]string{
		{"kind": "read", "repo": "mas-bandwidth/nova-tools", "pr": "7", "head": h},
		{"kind": "read", "ref": "https://forge.test/mas-bandwidth/nova-tools/pull/7", "head": h},
		{"kind": "review", "repo": "nova-tools", "pr": "0", "ref": "nova-tools#7", "head": h},
		{"kind": "read", "ref": "mas-bandwidth/nova-tools#7", "head": h},
	} {
		got, err := read.TargetOf(f)
		if err != nil || got != (read.Target{Repo: "nova-tools", N: "7", Head: h}) {
			t.Fatalf("%v: %+v, %v", f, got, err)
		}
	}
	for _, f := range []map[string]string{
		{"kind": "work", "repo": "nova-tools", "pr": "7", "head": h},
		{"kind": "read", "ref": "#3599", "head": h},
		{"kind": "read", "repo": "nova-tools", "pr": "7"},
		{"kind": "read", "ref": "https://forge.test/mas-bandwidth/nova-tools/issues/7", "head": h},
	} {
		if got, err := read.TargetOf(f); err == nil {
			t.Fatalf("%v: resolved %+v, want a refusal", f, got)
		}
	}
}

// TestReadTargetOfACardInReview (#4399): a card whose child ended with a PR
// (where review or merging, kind as cut) is read by its id, the way the
// cold session typed `read brief --ids <card>`; a card still waiting or
// working is not a read.
func TestReadTargetOfACardInReview(t *testing.T) {
	t.Parallel()
	h := strings.Repeat("b", 40)
	for _, w := range []string{"review", "merging"} {
		got, err := read.TargetOf(map[string]string{"where": w, "repo": "nova-tools", "pr": "4399", "head": h})
		if err != nil || got != (read.Target{Repo: "nova-tools", N: "4399", Head: h}) {
			t.Fatalf("where %s: %+v, %v", w, got, err)
		}
	}
	for _, w := range []string{"waiting", "ready", "working", ""} {
		if got, err := read.TargetOf(map[string]string{"where": w, "repo": "nova-tools", "pr": "4399", "head": h}); err == nil {
			t.Fatalf("where %q: resolved %+v, want a refusal", w, got)
		}
	}
}
