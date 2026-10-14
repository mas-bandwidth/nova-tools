package diffcheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Covers is the one reading of a PATHS entry: a directory covers every file under it
// however it is spelled, `...` is the tree beside `**`, a glob covers its matches and a
// literal names one file.
func TestCoversReadsAnEntryOneWay(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		entry, file string
		want        bool
	}{
		{"pkg/thing/", "pkg/thing/new.go", true},
		{"pkg/thing", "pkg/thing/sub/deep.go", true},
		{"./pkg/thing/", "pkg/thing/new.go", true},
		{" pkg/thing/ ", "pkg/thing/new.go", true},
		{"pkg/thing/", "pkg/thingy/new.go", false},
		{"pkg/thing/...", "pkg/thing/sub/deep.go", true},
		{"pkg/thing/...", "pkg/other/x.go", false},
		{"pkg/thing/*.go", "pkg/thing/a.go", true},
		{"pkg/thing/*.go", "pkg/other/a.go", false},
		{"docs/A.md", "docs/A.md", true},
		{"docs/A.md", "docs/B.md", false},
		{"", "docs/A.md", false},
		{"./", "docs/A.md", false},
		{"docs/A.md", "", false},
	} {
		assert.Equal(t, c.want, Covers(c.entry, c.file), "Covers(%q, %q)", c.entry, c.file)
	}
}

// A directory entry admits a new file under it to Outside: before Covers, Outside
// matched globs only and a directory entry covered nothing.
func TestOutsideHoldsADirectoryEntrysFiles(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/pkg/thing/new.go b/pkg/thing/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/pkg/thing/new.go\n@@ -0,0 +1 @@\n+package thing\n"
	assert.Empty(t, Outside([]string{"pkg/thing/"}, diff, nil))
	assert.Equal(t, []string{"pkg/thing/new.go"}, Outside([]string{"pkg/other/"}, diff, nil))
}

// ExpectedEntry names the entry nearest a refused file by shared leading segments, the
// shorter entry on a tie, cleaned as Covers cleans it, and "" when paths names nothing.
func TestExpectedEntryNamesTheNearestEntry(t *testing.T) {
	t.Parallel()
	paths := []string{"docs/", "pkg/thing/a.go", "pkg/thing/sub/", "./pkg/"}
	assert.Equal(t, "pkg/thing/sub", ExpectedEntry(paths, "pkg/thing/sub/x/y.go"))
	assert.Equal(t, "pkg/thing/sub", ExpectedEntry(paths, "pkg/thing/b.go"), "a tie at two segments goes to the shorter entry")
	assert.Equal(t, "pkg", ExpectedEntry(paths, "pkg/other/c.go"))
	assert.Equal(t, "docs", ExpectedEntry(paths, "docs/x.md"))
	assert.Equal(t, "", ExpectedEntry(nil, "docs/x.md"))
	assert.Equal(t, "", ExpectedEntry([]string{"", " / "}, "docs/x.md"))
}
