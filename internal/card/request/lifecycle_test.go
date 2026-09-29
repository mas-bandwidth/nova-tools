package request

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The lifecycle lives in lifecycle.go and nowhere else: no other file of the request
// or definition package names a state or a lifecycle input type as an identifier.
// The vocabularies the lifecycle is written in (notification kinds, evidence kinds)
// have their own names and are not the states.
func TestTheLifecycleLivesInOneFile(t *testing.T) {
	t.Parallel()
	state := regexp.MustCompile(`\b(Waiting|Ready|Working|Review|Merging|Landed)\b`)
	input := regexp.MustCompile(`\bIn(Start|Result|Verdict\w*|Head|QueueRejected|Cancel|Landing|ExternalLanding|DependencyFailed)\b`)
	stateLit := regexp.MustCompile(`"(waiting|ready|working|review|merging|landed|done)"`)
	for _, dir := range []string{".", "../definition"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no files in %s: %v", dir, err)
		}
		for _, f := range files {
			base := filepath.Base(f)
			if strings.HasSuffix(base, "_test.go") || base == "lifecycle.go" {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for n, line := range strings.Split(string(b), "\n") {
				code := line
				if i := strings.Index(code, "//"); i >= 0 {
					code = code[:i]
				}
				for _, re := range []*regexp.Regexp{state, input, stateLit} {
					if m := re.FindString(code); m != "" {
						// a notification kind is spelled with its own word ("ready", "landed", "merging")
						if (strings.Contains(code, "Notification") || strings.Contains(code, "Disposition")) && re == stateLit {
							continue
						}
						t.Errorf("%s:%d names %s outside lifecycle.go: %s", base, n+1, m, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

// lifecycle.go names the model that checks it, and says what a change means.
func TestLifecycleFileNamesTheModel(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	head := string(b)
	if i := strings.Index(head, "package request"); i > 0 {
		head = head[:i]
	}
	for _, want := range []string{"tla/CardManager.tla", "the model changes in the same PR", "TLC runs", "PR #4599"} {
		if !strings.Contains(head, want) {
			t.Errorf("the header of lifecycle.go does not say %q", want)
		}
	}
}
