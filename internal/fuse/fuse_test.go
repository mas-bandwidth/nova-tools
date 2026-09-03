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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

func boxIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "fuses.json")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("fixture write: %v", err)
	}
}

// ------------------------------------------------- 1. THE READ HAS THREE ANSWERS, NOT TWO

// TestAbsentBoxIsVerifiedClearNotAnError: a box that was never created holds no blown
// fuses -- that is a VERIFIED FACT: the read failed with the one error that means
// NONEXISTENT rather than UNREADABLE. (That error cannot say whether the missing thing
// was the file or its parent directory; the collapse is accepted -- see note 1 in the
// package comment -- and pinned separately in cmd/nova-fuse's tests.)
func TestAbsentBoxIsVerifiedClearNotAnError(t *testing.T) {
	b, err := ReadBox(boxIn(t))
	if err != nil {
		t.Fatalf("absent box must be VERIFIED CLEAR, got error: %v", err)
	}
	if b.Lockdown != nil {
		t.Error("absent box must have no lockdown")
	}
	if b.Quarantine == nil {
		t.Error("Quarantine must be non-nil so callers can range without a nil check")
	}
}

// TestUnreadableBoxIsAnErrorNotClear is the fail-closed half, and it is the one that
// matters: an exists-style probe answers false both when the file is absent AND when it
// cannot be read, so a permissions change would read as "nothing blown".
func TestUnreadableBoxIsAnErrorNotClear(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fuses.json")
	write(t, path, `{"lockdown":null,"quarantine":{}}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := ReadBox(path); err == nil {
		t.Fatal("an unreadable box must be CANNOT TELL, never clear -- this is the fail-open")
	}
}

// TestMalformedIsUnreadable: every shape that is not an object comes back as CANNOT TELL,
// which callers treat as BLOWN. Reaching the safe answer by a crash deep inside a caller
// is not a design; refusing at the read is.
func TestMalformedIsUnreadable(t *testing.T) {
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
			if _, err := ReadBox(path); err == nil {
				t.Errorf("%s must be unreadable, but it parsed", name)
			}
		})
	}
}

// TestNullQuarantineStillYieldsAUsableMap: `{"lockdown":null,"quarantine":null}` is a box
// your person could plausibly hand-edit into existence, and it is READABLE -- so it must
// not hand back a nil map that panics the first caller to write to it.
func TestNullQuarantineStillYieldsAUsableMap(t *testing.T) {
	path := boxIn(t)
	write(t, path, `{"lockdown":null,"quarantine":null}`)
	b, err := ReadBox(path)
	if err != nil {
		t.Fatalf("a null quarantine is readable: %v", err)
	}
	if b.Quarantine == nil {
		t.Fatal("Quarantine must be normalised to an empty map, not left nil")
	}
	b.Quarantine["x"] = Fuse{} // must not panic
}

// --------------------------------------------------------------- 2. MATCHING FAILS CLOSED

// TestSurfaceMatchingIgnoresCaseAndSpace pins the fail-OPEN reached by a capital letter:
// quarantine "Discord" then check "discord" must not answer CLEAR.
func TestSurfaceMatchingIgnoresCaseAndSpace(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"discord": {At: "t", Reason: "many the same way"}}}
	for _, probe := range []string{"discord", "Discord", "DISCORD", "  discord  ", "\tDiscord\n"} {
		if _, _, ok := b.Quarantined(probe); !ok {
			t.Errorf("%q must match the stored surface -- normalising can only ever block MORE", probe)
		}
	}
}

// TestQuarantinedReturnsTheStoredSpelling so a refusal can quote the file rather than the
// caller's spelling of it. Quoting the caller back at themselves hides a mismatch.
func TestQuarantinedReturnsTheStoredSpelling(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"Discord": {Reason: "r"}}}
	name, _, ok := b.Quarantined("discord")
	if !ok {
		t.Fatal("expected a match")
	}
	if name != "Discord" {
		t.Errorf("want the stored key %q, got %q", "Discord", name)
	}
}

// TestEmptySurfaceMatchesNothing. A check with no surface has verified only that there is
// no lockdown. If an empty string matched anything, a bare check would report a quarantine
// it never looked for -- a claim outrunning the measurement.
func TestEmptySurfaceMatchesNothing(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"discord": {Reason: "r"}, "": {Reason: "r"}}}
	for _, probe := range []string{"", "   ", "\t"} {
		if _, _, ok := b.Quarantined(probe); ok {
			t.Errorf("empty surface %q must match nothing", probe)
		}
	}
}

// TestSurfacesAreSorted: map iteration is randomized, so an unsorted listing prints a
// different order every run, and a status output that reorders itself between runs is one
// a reader stops diffing.
func TestSurfacesAreSorted(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"zulip": {}, "discord": {}, "matrix": {}}}
	got := strings.Join(b.Surfaces(), ",")
	if got != "discord,matrix,zulip" {
		t.Errorf("want sorted order, got %q", got)
	}
}

// ------------------------------------------------------------------- 3. THE WRITE IS SAFE

// TestWriteThenReadRoundTrips is the control. A store that cannot round-trip its own data
// is broken in a way no adversarial test would report.
func TestWriteThenReadRoundTrips(t *testing.T) {
	path := boxIn(t)
	in := Box{
		Lockdown:   &Fuse{At: "2026-08-03T00:00:00Z", Reason: "suspected compromise"},
		Quarantine: map[string]Fuse{"discord": {At: "2026-08-03T00:01:00Z", Reason: "many the same way"}},
	}
	if err := WriteBox(path, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := ReadBox(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if out.Lockdown == nil || out.Lockdown.Reason != "suspected compromise" {
		t.Errorf("lockdown did not survive the round trip: %+v", out.Lockdown)
	}
	if f, ok := out.Quarantine["discord"]; !ok || f.Reason != "many the same way" {
		t.Errorf("quarantine did not survive the round trip: %+v", out.Quarantine)
	}
}

// TestWriteLeavesNoTempLitter. The temp file must be renamed away, not left beside the
// box. A litter of .fuses-*.tmp is the only trace a failed write leaves, so it must be
// absent on the success path or it means nothing on the failure path.
func TestWriteLeavesNoTempLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fuses.json")
	if err := WriteBox(path, Box{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fuses-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// TestWrittenBoxIsWorldReadable. CreateTemp makes 0600; the box is not a secret and other
// tools must be able to read it. A fuse nobody else can see is a fuse that stops nothing.
func TestWrittenBoxIsWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: unix permission bits are not faithfully reported here")
	}
	path := boxIn(t)
	if err := WriteBox(path, Box{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("want mode 0644 so other tools can read the box, got %#o", perm)
	}
}

// TestWriteNormalisesNilQuarantine so a box written from a zero value reads back as an
// empty map rather than a JSON null that the next reader has to special-case.
func TestWriteNormalisesNilQuarantine(t *testing.T) {
	path := boxIn(t)
	if err := WriteBox(path, Box{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), `"quarantine": null`) {
		t.Errorf("nil quarantine must be written as {}, got:\n%s", data)
	}
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
	b := Box{Quarantine: map[string]Fuse{
		"Discord":   {At: "t1", Reason: "r1"},
		" discord ": {At: "t2", Reason: "r2"},
		"bsky":      {At: "t3", Reason: "r3"},
	}}
	removed := b.LiftQuarantine("DISCORD")
	if len(removed) != 2 {
		t.Fatalf("want both spellings removed, got %v", removed)
	}
	if f, ok := removed["Discord"]; !ok || f.Reason != "r1" {
		t.Errorf("removed entries must come back with their reasons intact, got %v", removed)
	}
	if _, ok := removed[" discord "]; !ok {
		t.Errorf("the second spelling must be removed too, got %v", removed)
	}
	if _, _, ok := b.Quarantined("discord"); ok {
		t.Error("a lifted surface must not still answer as quarantined")
	}
	if _, _, ok := b.Quarantined("bsky"); !ok {
		t.Error("lifting one surface must not lift another")
	}
}

// TestLiftQuarantineRemovesNothingWhenNothingMatches: a miss is reported as a miss, and
// the box is untouched -- the caller decides what to say about it.
func TestLiftQuarantineRemovesNothingWhenNothingMatches(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"bsky": {Reason: "r"}}}
	if removed := b.LiftQuarantine("discord"); len(removed) != 0 {
		t.Errorf("nothing matches, so nothing may be removed: %v", removed)
	}
	if len(b.Quarantine) != 1 {
		t.Errorf("a miss must leave the box alone, got %v", b.Quarantine)
	}
}

// TestLiftQuarantineEmptySurfaceRemovesNothing mirrors Quarantined: an empty surface
// matches nothing, so it must also LIFT nothing -- an accidental bare lift that emptied
// the box would be a fail-open reached by a missing argument.
func TestLiftQuarantineEmptySurfaceRemovesNothing(t *testing.T) {
	b := Box{Quarantine: map[string]Fuse{"": {Reason: "r"}, "discord": {Reason: "r"}}}
	for _, probe := range []string{"", "   ", "\t"} {
		if removed := b.LiftQuarantine(probe); len(removed) != 0 {
			t.Errorf("empty surface %q must lift nothing, got %v", probe, removed)
		}
	}
	if len(b.Quarantine) != 2 {
		t.Errorf("the box must be untouched, got %v", b.Quarantine)
	}
}

// ----------------------------------------------------------- 4. EVIDENCE IS NOT DESTROYED

// TestPreserveUnreadableKeepsTheBytes. A corrupt fuse box is evidence -- of a torn write,
// a bad hand-edit, or something worse -- and blowing an emergency power must not destroy
// it.
func TestPreserveUnreadableKeepsTheBytes(t *testing.T) {
	path := boxIn(t)
	write(t, path, `{"lockdown":{"at":`)
	dst, err := PreserveUnreadable(path)
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if dst != path+UnreadableSuffix {
		t.Errorf("want %q, got %q", path+UnreadableSuffix, dst)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read preserved: %v", err)
	}
	if string(got) != `{"lockdown":{"at":` {
		t.Errorf("the preserved bytes are not the original: %q", got)
	}
}

// TestPreserveUnreadableNamesTheDestinationEvenWhenItFails. Reporting where the bytes went
// and reporting that they could not be saved are both better than silence, so the caller
// must get a name to print either way.
func TestPreserveUnreadableNamesTheDestinationEvenWhenItFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	dst, err := PreserveUnreadable(path)
	if err == nil {
		t.Fatal("expected an error preserving a file that is not there")
	}
	if dst != path+UnreadableSuffix {
		t.Errorf("the destination must be named even on failure, got %q", dst)
	}
}

// ------------------------------------------------- 5. ONE LINE, WHATEVER THE BOX CONTAINS

// TestOneLineEscapesEveryControlCharacter. The box is hand-editable and world-readable by
// design, so the strings printed from it are authored by whoever can write the file. One
// line per event is a promise to every caller scanning the grammar, and a control
// character in a reason is what breaks it -- a newline forges a second event, an ESC
// repaints an operator's terminal. Escaping is done at PRINT time and covers both.
func TestOneLineEscapesEveryControlCharacter(t *testing.T) {
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
		{"a byte that is not valid UTF-8 at all", "a\xffb", `a\xffb`},
		{"printable non-ascii passes through", "café — 日本語", "café — 日本語"},
		{"empty stays empty", "", ""},
		{"only control characters, still not shortened to nothing", "\n\n", `\x0a\x0a`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OneLine(tc.in)
			if got != tc.want {
				t.Errorf("OneLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsFunc(got, unicode.IsControl) {
				t.Errorf("OneLine(%q) = %q still holds a control character", tc.in, got)
			}
			if tc.in != "" && got == "" {
				t.Errorf("OneLine(%q) emptied the text; a reason must never vanish", tc.in)
			}
			if again := OneLine(tc.in); again != got {
				t.Errorf("OneLine(%q) is not deterministic: %q then %q", tc.in, got, again)
			}
		})
	}
}

// TestFoldCollapsesControlCharactersToSpaces pins the WRITE half: this tool's own writes
// stay tidy, and nothing is ever refused for what it contains -- a fuse you cannot blow is
// not a fuse. Folding is not the defense (a box written by another hand still arrives with
// anything in it); OneLine is.
func TestFoldCollapsesControlCharactersToSpaces(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"line one\nline two", "line one line two"},
		{"  spaced   out  ", "spaced out"},
		{"\x1bdiscord\n", "discord"},
		{"a\t\t\tb", "a b"},
		{"\n\r\t", ""},
		{"", ""},
		{"café — 日本語", "café — 日本語"},
	} {
		if got := Fold(tc.in); got != tc.want {
			t.Errorf("Fold(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSurfaceFoldsControlCharactersOutOfAName. Folding a name can only ever collapse two
// names into one, which blocks MORE and never less -- the same argument as note 4's
// lower-casing, so it is safe in a safety control.
func TestSurfaceFoldsControlCharactersOutOfAName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\x1bDiscord\n", "discord"},
		{"dis\ncord", "dis cord"},
		{"  BSKY\t", "bsky"},
		{"\n\t", ""},
	} {
		if got := Surface(tc.in); got != tc.want {
			t.Errorf("Surface(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// And a folded name still matches the box entry it collapses onto.
	b := Box{Quarantine: map[string]Fuse{"dis\ncord": {At: "t", Reason: "r"}}}
	if name, _, ok := b.Quarantined("dis cord"); !ok || name != "dis\ncord" {
		t.Errorf("Quarantined(%q) = %q, %v -- want the stored spelling", "dis cord", name, ok)
	}
}
