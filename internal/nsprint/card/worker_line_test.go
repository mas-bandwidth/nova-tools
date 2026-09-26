package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCopyCardWorkerLine (seat-keeps-beat, invariant B): a copy whose
// record names who works it (model, harness, child: what card work and
// friend pull record) renders one WORKER header line with the named ones;
// a copy that names none renders no WORKER line.
func TestCopyCardWorkerLine(t *testing.T) {
	t.Parallel()
	rec := map[string]string{"primary": "p1", "leg": "work", "kind": "build", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": strings.Repeat("ab", 20), "paths": "internal/x.go", "done_when": "go test ./internal/x passes",
		"title": "one verb", "consumer": "friend:rowan"}
	body, err := card.RenderCopy(card.CopyCardFrom("p1~1", rec))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "WORKER:") {
		t.Fatalf("a copy with no worker renders a WORKER line:\n%s", body)
	}
	for _, c := range []struct {
		fields map[string]string
		want   string
	}{
		{map[string]string{"model": "opus-5.5", "harness": "claude-code", "child": "c7"}, "\nWORKER: model=opus-5.5 harness=claude-code child=c7\n"},
		{map[string]string{"model": "opus-5.5"}, "\nWORKER: model=opus-5.5\n"},
		{map[string]string{"child": "c7"}, "\nWORKER: child=c7\n"},
	} {
		r := map[string]string{}
		for k, v := range rec {
			r[k] = v
		}
		for k, v := range c.fields {
			r[k] = v
		}
		body, err := card.RenderCopy(card.CopyCardFrom("p1~1", r))
		if err != nil {
			t.Fatal(err)
		}
		head, _, _ := strings.Cut(string(body), "\n\n")
		if !strings.Contains(head+"\n", c.want) {
			t.Fatalf("%v: header lacks %q:\n%s", c.fields, c.want, head)
		}
	}
}
