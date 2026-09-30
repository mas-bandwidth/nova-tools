package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// view_set_flags_test.go pins the `view set` usage line of each nova-table
// reference to the flags the verb registers: a flag on the verb and not in a
// usage line is a flag a reader of that page cannot find. The flags are read
// from the `if sub == "set"` block of cmdView in cmd/nova-table/table.go.
func TestTheViewSetUsageLinesNameEveryFlag(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../cmd/nova-table/table.go")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(src), `if sub == "set" {`)
	if !ok {
		t.Fatal(`cmd/nova-table/table.go: no if sub == "set" block`)
	}
	block, _, _ = strings.Cut(block, "\n\t}\n")
	flags := regexp.MustCompile(`fs\.StringVar\(&\w+, "([a-z-]+)"`).FindAllStringSubmatch(block, -1)
	if len(flags) < 4 {
		t.Fatalf("read %d flags of view set, want at least 4", len(flags))
	}
	for _, doc := range []string{"../../docs/nova-table/README.md", "../../docs/CLI.md", "../../docs/SPEC-NOVA-TABLE.md"} {
		text, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, line := range strings.Split(string(text), "\n") {
			if !strings.Contains(line, "nova-table view set ") && !strings.Contains(line, "`view set <name>") {
				continue
			}
			if !strings.Contains(line, "--tables <") {
				continue // an example line, not the usage line
			}
			found++
			for _, f := range flags {
				if !strings.Contains(line, "--"+f[1]) {
					t.Errorf("%s: the view set usage line does not name --%s: %s", doc, f[1], line)
				}
			}
		}
		if found == 0 {
			t.Errorf("%s: no view set usage line found", doc)
		}
	}
}
