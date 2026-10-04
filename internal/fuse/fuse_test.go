/*
Tests for the STATE half.

The tool's own suite drives these behaviours through run(), which is the right place to
pin the exit contract. This file exists because internal/fuse is a seam of its own: a
future caller can ask "is a fuse blown?" without spawning a subprocess, and that caller
will reach these functions directly. A seam with no tests of its own is one whose
contract is only accidentally true.

ORDER: the read's three answers first, because collapsing them into two is the original
defect this package exists to prevent and is the one that fails OPEN.
*/
package fuse

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boxIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "fuses.json")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err, "fixture write: %v", err)
}

// ------------------------------------------------- 1. THE READ HAS ONE YES AND TWO NOES

// TestAnAbsentBoxIsErrNoBoxNeverClear: nothing at the path -- the file or a directory
// above it -- is CANNOT TELL, an error every caller treats as BLOWN, told apart from an
// unreadable box by ErrNoBox so a refusal can name CreateBox.
func TestAnAbsentBoxIsErrNoBoxNeverClear(t *testing.T) {
	t.Parallel()

	for _, path := range []string{boxIn(t), filepath.Join(t.TempDir(), "no", "such", "dir", "fuses.json")} {
		_, err := ReadBox(path)
		assert.ErrorIs(t, err, ErrNoBox, "ReadBox(%s) = %v, want ErrNoBox", path, err)
	}
}

// TestCreateBoxIsEmptyExclusiveAndNeverReplaces: CreateBox makes a readable empty box
// once, and anything already at the path is left byte for byte with fs.ErrExist.
func TestCreateBoxIsEmptyExclusiveAndNeverReplaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "fuses.json")
	err := CreateBox(path)
	require.NoError(t, err)
	b, err := ReadBox(path)
	require.NoError(t, err, "a created box reads %+v, %v; want empty, non-nil Quarantine", b, err)
	require.Nil(t, b.Lockdown, "a created box reads %+v; want empty", b)
	require.NotNil(t, b.Quarantine, "a created box must have non-nil Quarantine")
	require.Empty(t, b.Quarantine, "a created box reads %+v; want empty", b)
	write(t, path, `{"lockdown":{"at":"t","reason":"r"}}`)
	err = CreateBox(path)
	assert.ErrorIs(t, err, fs.ErrExist, "CreateBox over a box = %v, want fs.ErrExist", err)
	got, _ := os.ReadFile(path)
	assert.Equal(t, `{"lockdown":{"at":"t","reason":"r"}}`, string(got), "CreateBox replaced a box: %q", got)
	entries, _ := os.ReadDir(filepath.Dir(path))
	assert.Len(t, entries, 1, "CreateBox left litter: %v", entries)
}

// TestUnreadableBoxIsAnErrorNotClear is the fail-closed half, and it is the one that
// matters: an exists-style probe answers false both when the file is absent AND when it
// cannot be read, so a permissions change would read as "nothing blown".
func TestUnreadableBoxIsAnErrorNotClear(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	path := boxIn(t)
	write(t, path, `{"lockdown":null,"quarantine":{}}`)
	err := os.Chmod(path, 0o000)
	require.NoError(t, err, "chmod: %v", err)
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err = ReadBox(path)
	require.Error(t, err, "an unreadable box must be CANNOT TELL, never clear -- this is the fail-open")
}

// TestMalformedIsUnreadable: every shape that is not an object comes back as CANNOT TELL,
// which callers treat as BLOWN. Reaching the safe answer by a crash deep inside a caller
// is not a design; refusing at the read is.
func TestMalformedIsUnreadable(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"a JSON array":              `[]`,
		"a bare string":             `"lockdown"`,
		"a number":                  `7`,
		"truncated":                 `{"lockdown":{"at":"x"`,
		"empty file":                ``,
		"lockdown is not an object": `{"lockdown":"blown","quarantine":{}}`,
		"quarantine is not a map":   `{"lockdown":null,"quarantine":[1,2]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := boxIn(t)
			write(t, path, content)
			_, err := ReadBox(path)
			assert.Error(t, err, "%s must be unreadable, but it parsed", name)
		})
	}
}

// TestNullQuarantineStillYieldsAUsableMap: `{"lockdown":null,"quarantine":null}` is a box
// your person could plausibly hand-edit into existence, and it is READABLE -- so it must
// not hand back a nil map that panics the first caller to write to it.
func TestNullQuarantineStillYieldsAUsableMap(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	write(t, path, `{"lockdown":null,"quarantine":null}`)
	b, err := ReadBox(path)
	require.NoError(t, err, "a null quarantine is readable: %v", err)
	require.NotNil(t, b.Quarantine, "Quarantine must be normalised to an empty map, not left nil")
	b.Quarantine["x"] = Fuse{} // must not panic
}

// --------------------------------------------------------------- 2. MATCHING FAILS CLOSED

// TestSurfaceMatchingIgnoresCaseAndSpace pins the fail-OPEN reached by a capital letter:
// quarantine "Discord" then check "discord" must not answer CLEAR.
func TestSurfaceMatchingIgnoresCaseAndSpace(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"discord": {At: "t", Reason: "many the same way"}}}
	for _, probe := range []string{"discord", "Discord", "DISCORD", "  discord  ", "\tDiscord\n"} {
		_, _, ok := b.Quarantined(probe)
		assert.True(t, ok, "%q must match the stored surface -- equivalent spellings are ONE surface", probe)
	}
}

// TestQuarantinedReturnsTheStoredSpelling so a refusal can quote the file rather than the
// caller's spelling of it. Quoting the caller back at themselves hides a mismatch.
func TestQuarantinedReturnsTheStoredSpelling(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"Discord": {Reason: "r"}}}
	name, _, ok := b.Quarantined("discord")
	require.True(t, ok, "expected a match")
	assert.Equal(t, "Discord", name, "want the stored key %q, got %q", "Discord", name)
}

// TestEmptySurfaceMatchesNothing. A check with no surface has verified only that there is
// no lockdown. If an empty string matched anything, a bare check would report a quarantine
// it never looked for -- a claim outrunning the measurement.
func TestEmptySurfaceMatchesNothing(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"discord": {Reason: "r"}, "": {Reason: "r"}}}
	for _, probe := range []string{"", "   ", "\t"} {
		_, _, ok := b.Quarantined(probe)
		assert.False(t, ok, "empty surface %q must match nothing", probe)
	}
}

// TestQuarantinedIsDeterministicAcrossFoldEquivalentKeys: two stored spellings of one
// surface must always yield the SAME one, or every caller's refusal text is a coin flip.
func TestQuarantinedIsDeterministicAcrossFoldEquivalentKeys(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{
		"dis cord":  {At: "t", Reason: "one"},
		"dis\tcord": {At: "t", Reason: "two"},
	}}
	first, _, ok := b.Quarantined("dis cord")
	require.True(t, ok, "want a match")
	for i := 0; i < 30; i++ {
		name, _, _ := b.Quarantined("dis cord")
		require.Equal(t, first, name, "Quarantined answered %q then %q for the same box", first, name)
	}
}

// TestSurfacesAreSorted: map iteration is randomized, so an unsorted listing prints a
// different order every run, and a status output that reorders itself between runs is one
// a reader stops diffing.
func TestSurfacesAreSorted(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"zulip": {}, "discord": {}, "matrix": {}}}
	got := strings.Join(b.Surfaces(), ",")
	assert.Equal(t, "discord,matrix,zulip", got, "want sorted order, got %q", got)
}

// ------------------------------------------------------------------- 3. THE WRITE IS SAFE

// TestWriteThenReadRoundTrips is the control. A store that cannot round-trip its own data
// is broken in a way no adversarial test would report.
func TestWriteThenReadRoundTrips(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	in := Box{
		Lockdown:   &Fuse{At: "2026-08-03T00:00:00Z", Reason: "suspected compromise"},
		Quarantine: map[string]Fuse{"discord": {At: "2026-08-03T00:01:00Z", Reason: "many the same way"}},
	}
	err := WriteBox(path, in)
	require.NoError(t, err, "write: %v", err)
	out, err := ReadBox(path)
	require.NoError(t, err, "read back: %v", err)
	if assert.NotNil(t, out.Lockdown, "lockdown did not survive the round trip: %+v", out.Lockdown) {
		assert.Equal(t, "suspected compromise", out.Lockdown.Reason, "lockdown did not survive the round trip: %+v", out.Lockdown)
	}
	f, ok := out.Quarantine["discord"]
	assert.True(t, ok, "quarantine did not survive the round trip: %+v", out.Quarantine)
	assert.Equal(t, "many the same way", f.Reason, "quarantine did not survive the round trip: %+v", out.Quarantine)
}

// TestWriteLeavesNoTempLitter. The temp file must be renamed away, not left beside the
// box. A litter of .fuses-*.tmp is the only trace a failed write leaves, so it must be
// absent on the success path or it means nothing on the failure path.
func TestWriteLeavesNoTempLitter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "fuses.json")
	err := WriteBox(path, Box{})
	require.NoError(t, err, "write: %v", err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "readdir: %v", err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".fuses-"), "temp file left behind: %s", e.Name())
	}
}

// TestWrittenBoxIsWorldReadable. The box is not a secret and other tools must be able to read
// it (a fuse nobody else can see is a fuse that stops nothing). The written box has 0644
// permissions strictly via ExactMode.
func TestWrittenBoxIsWorldReadable(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: unix permission bits are not faithfully reported here")
	}
	path := boxIn(t)
	err := WriteBox(path, Box{})
	require.NoError(t, err, "write: %v", err)
	fi, err := os.Stat(path)
	require.NoError(t, err, "stat: %v", err)
	perm := fi.Mode().Perm()
	assert.Equal(t, os.FileMode(0o644), perm, "want mode 0644, got %04o", perm)
}

// TestWriteBoxResolvesSymlink asserts that WriteBox refuses a symlink at the
// cleaned path. The link stays a link and the target bytes do not change.
func TestWriteBoxResolvesSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: symlink creation requires special privileges")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	err := WriteBox(target, Box{})
	require.NoError(t, err, "WriteBox(target) failed: %v", err)
	before, err := os.ReadFile(target)
	require.NoError(t, err, "ReadFile(target) failed: %v", err)

	link := filepath.Join(dir, "link.json")
	err = os.Symlink(target, link)
	require.NoError(t, err, "Symlink failed: %v", err)

	b := Box{
		Quarantine: map[string]Fuse{
			"discord": {At: "2026-09-28T00:00:00Z", Reason: "must not be written"},
		},
	}
	err = WriteBox(link, b)
	require.Error(t, err, "WriteBox(link) succeeded; want refusal")
	require.Contains(t, err.Error(), "symlink", "WriteBox(link) error %q does not refuse a symlink", err)

	lst, err := os.Lstat(link)
	require.NoError(t, err, "Lstat(%q) failed: %v", link, err)
	require.NotZero(t, lst.Mode()&os.ModeSymlink, "link %q is no longer a symlink", link)

	after, err := os.ReadFile(target)
	require.NoError(t, err, "ReadFile(target) failed: %v", err)
	require.Equal(t, string(before), string(after), "target bytes changed:\nbefore: %s\nafter: %s", before, after)
}

// TestWriteBoxRefusesParentSymlink asserts that WriteBox refuses writing when
// the parent directory is a symlink. The real target bytes remain unchanged.
func TestWriteBoxRefusesParentSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: symlink creation requires special privileges")
	}

	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	intended := filepath.Join(root, "intended")
	err := os.Mkdir(outside, 0o755)
	require.NoError(t, err)
	err = os.Mkdir(intended, 0o755)
	require.NoError(t, err)
	target := filepath.Join(outside, "fuses.json")
	err = WriteBox(target, Box{})
	require.NoError(t, err, "WriteBox(target) failed: %v", err)
	before, err := os.ReadFile(target)
	require.NoError(t, err)

	linkdir := filepath.Join(intended, "linkdir")
	err = os.Symlink(outside, linkdir)
	require.NoError(t, err)

	b := Box{
		Quarantine: map[string]Fuse{
			"discord": {At: "2026-09-28T00:00:00Z", Reason: "must not be written"},
		},
	}
	linkBox := filepath.Join(linkdir, "fuses.json")
	err = WriteBox(linkBox, b)
	require.Error(t, err, "WriteBox through parent symlink succeeded; want refusal")
	require.Contains(t, err.Error(), "symlink", "WriteBox error %q does not mention symlink", err)

	after, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "target bytes changed:\nbefore: %s\nafter: %s", before, after)
}

// TestWriteNormalisesNilQuarantine so a box written from a zero value reads back as an
// empty map rather than a JSON null that the next reader has to special-case.
func TestWriteNormalisesNilQuarantine(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	err := WriteBox(path, Box{})
	require.NoError(t, err, "write: %v", err)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read: %v", err)
	assert.NotContains(t, string(data), `"quarantine": null`, "nil quarantine must be written as {}, got:\n%s", data)
}

// ------------------------------------------------- 3b. LIFTING A QUARANTINE IS SOFT
//
// The fuse design: quarantine is your own decision, in both directions. The state half of
// a lift lives here so the tool stays thin and a future direct caller gets the same
// contract.

// TestLiftQuarantineRemovesEveryNormalizedMatch. Blows written by the tool store
// normalized keys, but the box is hand-editable, so two spellings of one surface can
// coexist. A lift that removed only one would verify as "still quarantined" and report
// its own failure -- so a lift removes every entry the surface matches, and returns them
// for announcing.
func TestLiftQuarantineRemovesEveryNormalizedMatch(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{
		"Discord":   {At: "t1", Reason: "r1"},
		" discord ": {At: "t2", Reason: "r2"},
		"bsky":      {At: "t3", Reason: "r3"},
	}}
	removed := b.LiftQuarantine("DISCORD")
	require.Len(t, removed, 2, "want both spellings removed, got %v", removed)
	f, ok := removed["Discord"]
	assert.True(t, ok, "removed entries must come back with their reasons intact, got %v", removed)
	assert.Equal(t, "r1", f.Reason, "removed entries must come back with their reasons intact, got %v", removed)
	_, ok = removed[" discord "]
	assert.True(t, ok, "the second spelling must be removed too, got %v", removed)
	_, _, ok = b.Quarantined("discord")
	assert.False(t, ok, "a lifted surface must not still answer as quarantined")
	_, _, ok = b.Quarantined("bsky")
	assert.True(t, ok, "lifting one surface must not lift another")
}

// TestLiftQuarantineRemovesNothingWhenNothingMatches: a miss is reported as a miss, and
// the box is untouched -- the caller decides what to say about it.
func TestLiftQuarantineRemovesNothingWhenNothingMatches(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"bsky": {Reason: "r"}}}
	removed := b.LiftQuarantine("discord")
	assert.Len(t, removed, 0, "nothing matches, so nothing may be removed: %v", removed)
	assert.Len(t, b.Quarantine, 1, "a miss must leave the box alone, got %v", b.Quarantine)
}

// TestLiftQuarantineEmptySurfaceRemovesNothing mirrors Quarantined: an empty surface
// matches nothing, so it must also LIFT nothing -- an accidental bare lift that emptied
// the box would be a fail-open reached by a missing argument.
func TestLiftQuarantineEmptySurfaceRemovesNothing(t *testing.T) {
	t.Parallel()

	b := Box{Quarantine: map[string]Fuse{"": {Reason: "r"}, "discord": {Reason: "r"}}}
	for _, probe := range []string{"", "   ", "\t"} {
		removed := b.LiftQuarantine(probe)
		assert.Len(t, removed, 0, "empty surface %q must lift nothing, got %v", probe, removed)
	}
	assert.Len(t, b.Quarantine, 2, "the box must be untouched, got %v", b.Quarantine)
}

// ----------------------------------------------------------- 4. EVIDENCE IS NOT DESTROYED

// TestPreserveUnreadableKeepsTheBytes. A corrupt fuse box is evidence -- of a torn write,
// a bad hand-edit, or something worse -- and blowing an emergency power must not destroy
// it.
func TestPreserveUnreadableKeepsTheBytes(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	write(t, path, `{"lockdown":{"at":`)
	dst, err := PreserveUnreadable(path)
	require.NoError(t, err, "preserve: %v", err)
	assert.Equal(t, path+UnreadableSuffix, dst, "want %q, got %q", path+UnreadableSuffix, dst)
	got, err := os.ReadFile(dst)
	require.NoError(t, err, "read preserved: %v", err)
	assert.Equal(t, `{"lockdown":{"at":`, string(got), "the preserved bytes are not the original: %q", got)
}

// TestPreserveUnreadableNamesTheDestinationEvenWhenItFails. Reporting where the bytes went
// and reporting that they could not be saved are both better than silence, so the caller
// must get a name to print either way.
func TestPreserveUnreadableNamesTheDestinationEvenWhenItFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	dst, err := PreserveUnreadable(path)
	require.Error(t, err, "expected an error preserving a file that is not there")
	assert.Equal(t, path+UnreadableSuffix, dst, "the destination must be named even on failure, got %q", dst)
}

// TestPreserveUnreadablePreservesExistingPermissions asserts that when the destination
// unreadable file already exists with 0600 permissions, PreserveUnreadable preserves
// those permissions rather than widening them to 0644.
func TestPreserveUnreadablePreservesExistingPermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: unix permission bits are not faithfully reported here")
	}

	path := boxIn(t)
	write(t, path, `{"corrupt":`)

	dst := path + UnreadableSuffix
	err := os.WriteFile(dst, []byte("existing corrupt bytes\n"), 0o600)
	require.NoError(t, err, "write dst: %v", err)
	err = os.Chmod(dst, 0o600)
	require.NoError(t, err, "chmod dst: %v", err)

	_, err = PreserveUnreadable(path)
	require.NoError(t, err, "PreserveUnreadable: %v", err)

	fi, err := os.Stat(dst)
	require.NoError(t, err, "stat dst: %v", err)
	perm := fi.Mode().Perm()
	require.Equal(t, os.FileMode(0o600), perm, "preserved destination mode = %04o, want 0600 (existing permissions were widened)", perm)
}

// ------------------------------------------------- 5. ONE LINE, WHATEVER THE BOX CONTAINS

// TestOneLineEscapesEveryControlCharacter. The box is hand-editable and world-readable by
// design, so the strings printed from it are authored by whoever can write the file. One
// line per event is a promise to every caller scanning the grammar, and a control
// character in a reason is what breaks it -- a newline forges a second event, an ESC
// repaints an operator's terminal. Escaping is done at PRINT time and covers both.
func TestOneLineEscapesEveryControlCharacter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii is untouched", "lockdown at 3am", "lockdown at 3am"},
		{"newline", "real\nFUSE OK lockdown=clear", `real\x0aFUSE OK lockdown=clear`},
		{"carriage return", "a\rb", `a\x0db`},
		{"tab", "a\tb", `a\x09b`},
		{"nul", "a\x00b", `a\x00b`},
		{"escape", "\x1b[2J", `\x1b[2J`},
		{"delete", "a\x7fb", `a\x7fb`},
		{"C1 next-line", "a\u0085b", `a\u0085b`},
		{"C1 control string introducer", "a\u009bb", `a\u009bb`},
		{"line separator, which str.splitlines and UAX-14 both break on", "a\u2028b", `a\u2028b`},
		{"paragraph separator, the same hole", "a\u2029b", `a\u2029b`},
		{"left-to-right embedding, which rearranges the visible order of what follows", "a\u202ab", `a\u202ab`},
		{"right-to-left override, the classic reorder", "a\u202eb", `a\u202eb`},
		{"pop directional formatting", "a\u202cb", `a\u202cb`},
		{"left-to-right isolate", "a\u2066b", `a\u2066b`},
		{"first strong isolate", "a\u2068b", `a\u2068b`},
		{"pop directional isolate", "a\u2069b", `a\u2069b`},
		{"a zero-width joiner is not a bidi control and passes through", "a\u200db", "a\u200db"},
		{"a byte that is not valid UTF-8 at all", "a\xffb", `a\xffb`},
		{"printable non-ascii passes through", "café — 日本語", "café — 日本語"},
		{"empty stays empty", "", ""},
		{"only control characters, still not shortened to nothing", "\n\n", `\x0a\x0a`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OneLine(tc.in)
			assert.Equal(t, tc.want, got, "OneLine(%q) = %q, want %q", tc.in, got, tc.want)
			assert.False(t, strings.ContainsFunc(got, unicode.IsControl), "OneLine(%q) = %q still holds a control character", tc.in, got)
			assert.True(t, tc.in == "" || got != "", "OneLine(%q) emptied the text; a reason must never vanish", tc.in)
			again := OneLine(tc.in)
			assert.Equal(t, got, again, "OneLine(%q) is not deterministic: %q then %q", tc.in, got, again)
		})
	}
}

// TestFoldCollapsesControlCharactersToSpaces pins the WRITE half: this tool's own writes
// stay tidy, and nothing is ever refused for what it contains -- a fuse you cannot blow is
// not a fuse. Folding is not the defense (a box written by another hand still arrives with
// anything in it); OneLine is.
func TestFoldCollapsesControlCharactersToSpaces(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"line one\nline two", "line one line two"},
		{"  spaced   out  ", "spaced out"},
		{"\x1bdiscord\n", "discord"},
		{"a\t\t\tb", "a b"},
		{"\n\r\t", ""},
		{"", ""},
		{"café — 日本語", "café — 日本語"},
	} {
		got := Fold(tc.in)
		assert.Equal(t, tc.want, got, "Fold(%q) = %q, want %q", tc.in, got, tc.want)
	}
}

// TestSurfaceFoldsControlCharactersOutOfAName. Folding widens the class of spellings that
// count as ONE surface, the same way note 4's lower-casing does -- which makes `check`
// refuse on more spellings and makes `lift quarantine` remove more of them. Both
// directions, deliberately; see note 4.
func TestSurfaceFoldsControlCharactersOutOfAName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"\x1bDiscord\n", "discord"},
		{"dis\ncord", "dis cord"},
		{"  BSKY\t", "bsky"},
		{"\n\t", ""},
	} {
		got := Surface(tc.in)
		assert.Equal(t, tc.want, got, "Surface(%q) = %q, want %q", tc.in, got, tc.want)
	}

	// And a folded name still matches the box entry it collapses onto.
	b := Box{Quarantine: map[string]Fuse{"dis\ncord": {At: "t", Reason: "r"}}}
	name, _, ok := b.Quarantined("dis cord")
	assert.True(t, ok, "Quarantined(%q) = %q, %v -- want the stored spelling", "dis cord", name, ok)
	assert.Equal(t, "dis\ncord", name, "Quarantined(%q) = %q, %v -- want the stored spelling", "dis cord", name, ok)
}
