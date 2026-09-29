package definition

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

func TestPolicyClassifiesEveryDeclaredKind(t *testing.T) {
	t.Parallel()
	p, err := loadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	missing, stale := unclassified(hygiene.Kinds(), p)
	if len(missing) > 0 {
		t.Errorf("internal/hygiene/kinds.txt declares %v, which internal/card/definition/completion.txt does not classify: add a `<kind>\\tpr` or `<kind>\\tnon-pr` row for each", missing)
	}
	if len(stale) > 0 {
		t.Errorf("completion.txt classifies %v, which kinds.txt no longer declares: remove the rows", stale)
	}
	if PolicyVersion() < 1 {
		t.Errorf("the policy has no version")
	}
}

func TestPolicyClasses(t *testing.T) {
	t.Parallel()
	nonPR := map[string]bool{"read": true, "probe": true, "text": true, "tone": true, "report": true}
	kinds := hygiene.Kinds()
	cs, oks := Classify(kinds)
	for i, k := range kinds {
		want := CompletionPR
		if nonPR[k] {
			want = CompletionNoPR
		}
		if !oks[i] || cs[i] != want {
			t.Errorf("%s is %q (classified %v), want %q", k, cs[i], oks[i], want)
		}
	}
	for k := range nonPR {
		if !hygiene.KindDeclared(k) {
			t.Errorf("the non-PR kind %s is not declared", k)
		}
	}
}

// The coverage check must fail the day kinds.txt gains a kind: it is run here
// against a declared list with one more kind, and a policy with one fewer.
func TestPolicyCoverageFailsWhenKindsGainsAKind(t *testing.T) {
	t.Parallel()
	p, err := loadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	declared := append(hygiene.Kinds(), "brand-new-kind")
	missing, stale := unclassified(declared, p)
	if len(missing) != 1 || missing[0] != "brand-new-kind" || len(stale) != 0 {
		t.Fatalf("missing %v stale %v", missing, stale)
	}
	short := append([]string(nil), hygiene.Kinds()[1:]...)
	if missing, stale := unclassified(short, p); len(missing) != 0 || len(stale) != 1 {
		t.Fatalf("missing %v stale %v", missing, stale)
	}
}

func TestKindWithoutClassificationRefuses(t *testing.T) {
	t.Parallel()
	p, err := parsePolicy("version\t1\nread\tnon-pr\n")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := kindWhyIn(p, "read"); c != "" {
		t.Fatalf("read: %s", c)
	}
	c, why := kindWhyIn(p, "fix-red")
	if c != CauseUnclassifiedKind || !strings.Contains(why, "fix-red") {
		t.Fatalf("declared but unclassified: %s %s", c, why)
	}
	if c, _ := kindWhyIn(p, "nonsense"); c != CauseInvalidKind {
		t.Fatalf("unknown: %s", c)
	}
	if c, _ := kindWhy("nonsense"); c != CauseInvalidKind {
		t.Fatalf("unknown: %s", c)
	}
}

func TestPolicyIsNotInferredFromTheGatedColumn(t *testing.T) {
	t.Parallel()
	// The file states each kind's class itself; a kind row that carries the gated
	// or ungated word instead of a class is a refusal, not a synonym.
	for _, bad := range []string{
		"version\t1\nread\tungated\n",
		"version\t1\nread\tgated\n",
		"version\t1\nread\tPR\n",
	} {
		if _, err := parsePolicy(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, bad := range []string{
		"read\tnon-pr\n",                       // no version
		"version\t1\nversion\t2\nread\tpr\n",   // two versions
		"version\t0\nread\tpr\n",               // not positive
		"version\t1\nread\tpr\nread\tnon-pr\n", // twice
		"version\t1\nread\n",                   // no class
	} {
		if _, err := parsePolicy(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
