package onboarding

import "testing"

// The document this package parses is docs/TESTS.md, and the failure that put
// these tests here was not a parse error: it was a lookup that could not fail.
// docs/TESTS.md carried `## nova-work` twice; Section cut to the first match, so
// cmd/nova-work/firstrun_test.go executed the first section and the second one --
// which held a refusal sentence the binary had stopped printing and an events
// line that reached the real forge -- was read by no test at all for as long as
// it took two benches to find it by hand.

const twoSections = `# doc

## nova-alpha

### First run

` + "```text" + `
$ nova-alpha go
ALPHA OK n=1
` + "```" + `

### Refusals

` + "```text" + `
$ nova-alpha
nova-alpha: no verb given; run: nova-alpha help
` + "```" + `

## nova-beta

### First run

` + "```text" + `
$ nova-beta go
BETA OK n=1
` + "```" + `

## nova-alpha

### First run

` + "```text" + `
$ nova-alpha stale
ALPHA STALE
` + "```" + `
`

func TestSectionNamesKeepsOrderAndRepeats(t *testing.T) {
	got := SectionNames(twoSections)
	want := []string{"nova-alpha", "nova-beta", "nova-alpha"}
	if len(got) != len(want) {
		t.Fatalf("SectionNames = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SectionNames = %q, want %q", got, want)
		}
	}
}

func TestRepeatedSectionsNamesTheOneWrittenTwice(t *testing.T) {
	got := RepeatedSections(twoSections)
	if len(got) != 1 || got[0] != "nova-alpha" {
		t.Fatalf("RepeatedSections = %q, want [nova-alpha]", got)
	}
	if none := RepeatedSections("## a\n\n## b\n"); len(none) != 0 {
		t.Fatalf("RepeatedSections of a healthy document = %q, want none", none)
	}
}

// A repeated name is invisible to Section, which is the whole danger: it answers
// happily and names the first half. This pins that reading so the next person to
// wonder why a section drifted unwatched finds the answer in a test.
func TestSectionReadsOnlyTheFirstOfTwo(t *testing.T) {
	body, ok := Section(twoSections, "nova-alpha")
	if !ok {
		t.Fatal("Section did not find nova-alpha")
	}
	lines, err := FirstRun(twoSections, "nova-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "$ nova-alpha go" {
		t.Fatalf("FirstRun read %q; it reads the FIRST `## nova-alpha`, and the second is read by nobody", lines)
	}
	if contains(body, "ALPHA STALE") {
		t.Fatal("Section reached into the second `## nova-alpha`; this test's premise is gone")
	}
}

func TestTranscriptReadsANamedSubsection(t *testing.T) {
	lines, err := Transcript(twoSections, "nova-alpha", "Refusals")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "$ nova-alpha" {
		t.Fatalf("Transcript(Refusals) = %q", lines)
	}
	if _, err := Transcript(twoSections, "nova-beta", "Refusals"); err == nil {
		t.Fatal("Transcript found a `### Refusals` that nova-beta does not have")
	}
	if _, err := Transcript(twoSections, "nova-gamma", "First run"); err == nil {
		t.Fatal("Transcript found a section for a tool the document does not name")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
