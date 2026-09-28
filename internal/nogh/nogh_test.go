package nogh

import (
	"os"
	"strings"
	"testing"
)

func TestPathFirst(t *testing.T) {
	t.Parallel()

	sep := string(os.PathListSeparator)
	got := PathFirst([]string{"A=1", "PATH=/x", "PATH="}, "/s")
	want := []string{"A=1", "PATH=/s" + sep + "/x", "PATH=/s"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("PathFirst = %q, want %q", got, want)
	}
	if got := PathFirst([]string{"A=1"}, "/s"); strings.Join(got, "|") != "A=1|PATH=/s" {
		t.Fatalf("no PATH: %q", got)
	}
	if got := PathFirst([]string{"PATH=/x"}, ""); got[0] != "PATH=/x" {
		t.Fatalf("empty dir changed env: %q", got)
	}
}
