package definition

import (
	"github.com/mas-bandwidth/nova-tools/internal/card"
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
	if pr := kindProblemIn(p, "read"); pr != nil {
		t.Fatalf("read: %+v", pr)
	}
	pr := kindProblemIn(p, "fix-red")
	if pr == nil || pr.cause != CauseUnclassifiedKind || !strings.Contains(pr.found, "fix-red") {
		t.Fatalf("declared but unclassified: %+v", pr)
	}
	if pr := kindProblemIn(p, "nonsense"); pr == nil || pr.cause != CauseInvalidKind {
		t.Fatalf("unknown: %+v", pr)
	}
	if pr := kindProblem("nonsense"); pr == nil || pr.cause != CauseInvalidKind {
		t.Fatalf("unknown: %+v", pr)
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

// The completion policy's version is tied to its content: the digest of
// completion.txt is held here beside the version, so a classification cannot change
// without the version changing. When this fails, bump `version` in completion.txt,
// then update both constants below.
func TestPolicyVersionIsTiedToItsContent(t *testing.T) {
	t.Parallel()
	const (
		version = 1
		digest  = "43c072ba6665212ab480530128ee18401968ae7439eb7be624f3633a4e9f16f8"
	)
	if PolicyVersion() != version || PolicyDigest() != digest {
		t.Fatalf("completion.txt is version %d with digest %s; this test holds version %d with digest %s: a change to a classification needs a new version", PolicyVersion(), PolicyDigest(), version, digest)
	}
	// The digest really is of the file's bytes, and a one-byte change to a
	// classification changes it.
	if got := card.Sum([]byte(policyData)); string(got) != PolicyDigest() {
		t.Fatalf("PolicyDigest is not the digest of the embedded bytes")
	}
	changed := strings.Replace(policyData, "fix-red\tpr", "fix-red\tnon-pr", 1)
	if changed == policyData || card.Sum([]byte(changed)) == card.Sum([]byte(policyData)) {
		t.Fatal("a reclassification does not change the digest")
	}
}
