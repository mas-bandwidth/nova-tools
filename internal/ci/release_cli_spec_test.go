package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// LESSON 10 of docs/SPEC-RELEASE.md. The fourth release dogfood went looking
// for `nova-update release` in the command reference and found nothing: the
// five verbs that put binaries on every bench in the fleet were declared in
// SPEC-UPDATE, in the help string and in nobody's reference. docs/CLI.md is
// what the dogfood ledger reads, so a verb missing from it is a verb nothing
// asks to have been run by a non-author.
//
// The five are held against internal/release.Verbs rather than against a list
// written here, so a sixth release verb is a test failure on the day it is
// added rather than on the day somebody notices the reference is short.
func TestTheCommandReferenceDeclaresEveryReleaseVerb(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "CLI.md")
	declared, err := dogfood.ParseCLI(path)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, v := range declared {
		have[v.Key()] = true
	}
	for _, line := range strings.Split(release.Verbs, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("a usage line is not `nova-update release <verb> ...`: %q", line)
		}
		key := fields[0] + " " + fields[1] + " " + fields[2]
		if !have[key] {
			t.Errorf("docs/CLI.md declares no %q; the release verbs are the last mile and the reference is where a person looks for them", key)
		}
	}
	// And the section is found by its heading, so a reader scanning the
	// reference for the release verbs has something to scan for.
	text := readFile(t, path)
	if !strings.Contains(text, "### The release verb") {
		t.Error("docs/CLI.md has no `### The release verb` heading under nova-update")
	}
	// The flags a person cannot get through a release without, named where
	// they will meet them.
	for _, want := range []string{"--security-read", "--paths-from", "--local-diff", "--expect-sums-from", "--platform", "--receipts", "--no-dogfood-gate"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/CLI.md does not name %s", want)
		}
	}
}

// LESSON 11. The definition of done is a gate in front of the tag, and the
// gate's whole worth is that somebody meeting its refusal can find out what it
// is. A refusal a person cannot look up is a refusal they route around.
func TestTheDogfoodGateIsInTheReleaseSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-RELEASE.md"))
	for _, want := range []string{
		"## 11. ",
		"RELEASE CUT REFUSED reason=dogfood-gate",
		"RELEASE BUILD REFUSED",
		"--no-dogfood-gate",
		"--receipts",
		release.DogfoodWaiverPrefix,
		"dogfood-gate=skipped",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md does not carry %q", want)
		}
	}
	// And the remedy the refusal hands somebody is the remedy the spec
	// prints, composed from the one string rather than retyped beside it.
	if !strings.Contains(spec, release.DogfoodRemedy) {
		t.Errorf("docs/SPEC-RELEASE.md does not carry the remedy %q", release.DogfoodRemedy)
	}
}

// Every edge the fourth release dogfood found is a NUMBERED lesson in
// docs/SPEC-RELEASE.md, because a lesson that lives only in a commit message is
// a lesson the next person re-learns. The numbers continue Johnny's three
// decisions rather than restarting, so a reference to "lesson 7" means one
// thing forever.
func TestTheFourthDogfoodsLessonsAreInTheReleaseSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-RELEASE.md"))
	for _, want := range []string{
		"## 4. ",
		"## 5. ",
		"## 6. ",
		"## 7. ",
		"## 8. ",
		"## 9. ",
		"## 10. ",
		"RELEASE CUT REFUSED reason=compare-truncated",
		"--paths-from",
		"--local-diff",
		"RELEASE BUILD OK",
		"SUMS.digest",
		"--expect-sums-from",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md does not carry %q", want)
		}
	}
}
