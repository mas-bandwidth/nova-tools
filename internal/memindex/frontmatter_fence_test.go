package memindex

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFrontmatterRequiresClosingFenceLine pins the closing-fence rule the
// frontmatter parser implements: the closing delimiter must be a complete
// "---" line, with end of input counting as a line ending. A closing search
// that matches only the prefix "\n---" closes a block on "---suffix" or
// "----", so a malformed file is read as carrying frontmatter it does not
// have — evidence Wikilinks then resolves against and a --frontmatter gate
// passes on. The corpus files are the truth and the index is a derivation;
// a name must come from a file that actually closes its frontmatter block
// (docs/SPEC.md, nova-memory: frontmatter name:/type: are surfaced and never
// invented).
func TestFrontmatterRequiresClosingFenceLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		want [2]string // frontmatter name and type; empty pair = no frontmatter
	}{
		{"exact fence with newline", "---\nname: lantern\ntype: reference\n---\n\nbody prose\n", [2]string{"lantern", "reference"}},
		{"exact fence at EOF", "---\nname: lantern\ntype: reference\n---", [2]string{"lantern", "reference"}},
		{"suffix fence without a later exact fence", "---\nname: phantom\n---suffix\nbody prose has enough tokens\n", [2]string{"", ""}},
		{"four-dash fence without a later exact fence", "---\nname: phantom\n----\nbody prose has enough tokens\n", [2]string{"", ""}},
		{"suffix fence at EOF is still no fence", "---\nname: phantom\n---suffix", [2]string{"", ""}},
		{"malformed line before a later exact fence", "---\nname: phantom\n---suffix\nname: second\n---\nbody prose\n", [2]string{"phantom", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotType := frontmatter(tc.src)
			assert.Equal(t, tc.want, [2]string{gotName, gotType},
				"%s: frontmatter of %q", tc.name, tc.src)
		})
	}

	// Line endings are folded before the fence check, so a CRLF or lone-CR
	// file's exact "---\r\n" fence is still a complete "---" line; the fix
	// for the prefix fence must not take the tolerance away again.
	t.Run("line endings still folded", func(t *testing.T) {
		t.Parallel()
		lf := "---\nname: lantern\ntype: reference\n---\n\nbody prose\n"
		wantName, wantType := frontmatter(lf)
		require.Equal(t, "lantern", wantName, "LF baseline broken: name=%q type=%q", wantName, wantType)
		require.Equal(t, "reference", wantType, "LF baseline broken: name=%q type=%q", wantName, wantType)
		for _, tw := range []struct{ name, ending string }{
			{"CRLF", "\r\n"},
			{"CR", "\r"},
		} {
			gotName, gotType := frontmatter(strings.ReplaceAll(lf, "\n", tw.ending))
			assert.Equal(t, [2]string{wantName, wantType}, [2]string{gotName, gotType},
				"%s: name=%q type=%q, want %q/%q", tw.name, gotName, gotType, wantName, wantType)
		}
	})

	// The public consequence on the --frontmatter gate: an unclosed malformed
	// block reports as missing a name, never gates green on a name it
	// invented.
	t.Run("frontmatter gate reports the malformed block", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"notes/phantom-claim.md": {Data: []byte("---\nname: phantom\n---suffix\nbody prose has enough tokens\n")},
		}
		fnds, err := FrontmatterPresent(fsys, "notes/phantom-claim.md", nil)
		require.NoError(t, err, "findings: %v", fnds)
		require.Len(t, fnds, 1, "an unclosed malformed block gated green; findings: %v", fnds)
		assert.Contains(t, fnds[0].Detail, "no name: in frontmatter",
			"finding = %+v, want the missing-name finding", fnds[0])
	})

	// The public consequence on wikilink resolution: [[phantom]] has no file
	// and its only claim to a name comes from the malformed block, so the
	// link is reported unresolved instead of silently resolving.
	t.Run("wikilink to the malformed block's name is unresolved", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"notes/phantom-claim.md": {Data: []byte("---\nname: phantom\n---suffix\nbody prose has enough tokens\n")},
			"notes/linker.md":        {Data: []byte("a linking paragraph that cites [[phantom]] and carries enough tokens\n")},
		}
		c, err := Build(fsys, nil)
		require.NoError(t, err, "Build")
		fnds, err := Wikilinks(fsys, c)
		require.NoError(t, err)
		var hit bool
		for _, f := range fnds {
			if strings.Contains(f.Detail, "[[phantom]]") {
				hit = true
			}
		}
		assert.True(t, hit, "[[phantom]] resolved through the malformed block's invented name; findings: %v", fnds)
	})
}
