/*
Package fuse is the STATE half of the ingestion fuse: reading and writing the box,
the JSON file the two emergency powers live in. cmd/nova-fuse is the thin command
on top of it. The box's path always comes from the caller — there is no default
location and no environment variable, per this repo's no-guessed-paths rule.

WHAT IS ACTUALLY DECIDED HERE, and why each one is not arbitrary:

 1. THE READ HAS THREE ANSWERS, NEVER TWO. "no box" is a VERIFIED FACT -- a fuse box
    that was never created holds no blown fuses. "cannot read the box" is a THIRD
    answer and it is never the reassuring one. A reader that collapses them (an
    exists-style probe answers false both when the file is absent AND when the
    directory above it cannot be stat'd) turns a permissions change into "nothing
    blown" -- a fail-open in a safety control. os.ReadFile +
    errors.Is(err, fs.ErrNotExist) tells UNREADABLE apart from NONEXISTENT, which is
    why the read is written with the stdlib primitive that distinguishes those two
    rather than the one that does not. What that primitive does NOT distinguish: a
    missing box file from a missing parent directory -- both come back
    fs.ErrNotExist, so `--box /no/such/dir/fuses.json` also answers VERIFIED CLEAR.
    Accepted, deliberately, not overlooked: --box is a locator, the caller's
    statement of where the box lives, and a caller that names the wrong box gets
    that box's truth -- here, an empty one. The case is pinned by test
    (TestCheckIntoANonexistentDirectoryIsAlsoClear in cmd/nova-fuse), so changing
    this answer is a decision, never a drive-by.

 2. MALFORMED IS UNREADABLE. A JSON array, a bare string, a truncated file, a
    lockdown whose value is not an object -- every one fails the unmarshal and comes
    back as CANNOT TELL, which every caller must treat as BLOWN. Reaching the
    fail-closed answer by a crash deep inside a caller is not a design; this is.

 3. THE WRITE IS TEMP-FILE + RENAME. The file whose corruption means PERMANENT
    LOCKDOWN must never be left torn: a truncating write can leave half a file if
    the process dies, and a half file is an unreadable box that only your person can
    clear, by hand, live. Rename within one directory is atomic, so a reader sees
    the old box or the new one and never a fragment. Two copies of the tool blowing
    fuses at once lose one WRITE, but neither can produce a corrupt box.

 4. SURFACE NAMES ARE MATCHED NORMALIZED, AND THAT CUTS BOTH WAYS. Raw string
    comparison lets `quarantine Discord` then `check discord` answer CLEAR -- a
    fail-OPEN in a safety control, reached by a capital letter. Normalizing collapses
    spellings into one surface, and the honest statement of what that does is not
    "it can only ever block more": it is that EQUIVALENT SPELLINGS ARE ONE SURFACE,
    IN BOTH DIRECTIONS. `check` refuses on any spelling, so the class blocks more.
    LiftQuarantine removes EVERY spelling in the class, so a lift lifts more too --
    deliberately, because a lift that left one spelling behind would verify its own
    failure, and because an operator who lifts a surface means the surface. It is
    not silent either way: each removal is returned under its stored spelling so the
    caller can announce it on its own line. The consequence to know: a box holding
    two keys that fold together holds ONE surface, not two.

 5. THE ESCAPE LIVES AT PRINT TIME, because the tool cannot trust what it did not
    write. The box is world-readable on purpose (a fuse nobody else can see is a
    fuse that stops nothing) and hand-editable on purpose (that is the only
    lockdown-replacement mechanism there is), so on a shared machine the reason,
    the `at` stamp and the stored surface names are authored by whoever can write
    the file. Echoed raw into a one-line output grammar, a newline forges a SECOND
    event line beneath a real one -- a `FUSE OK lockdown=clear` under a `FUSE
    FAIL`, which a caller scanning the grammar reads as permission -- and an ESC
    sequence does the same to an operator's terminal. OneLine escapes every
    control character as the text is printed, which holds for a box this tool
    never wrote; Fold only tidies what this tool writes itself, and is never a
    refusal, because a fuse you cannot blow is not a fuse. Constraining writes
    alone would defend exactly the case that needs no defending. The escaped set
    is category Cc plus U+2028 and U+2029, which break a line for readers that
    follow Unicode rather than counting newlines. Folding also widens the surface
    classes of note 4, in both directions -- see there.
*/
package fuse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// UnreadableSuffix names where the bytes of an unreadable box are kept when a lockdown has
// to clobber it. Back up before mutating what cannot be reconstructed: a corrupt fuse box is
// evidence -- of a torn write, a bad hand-edit, or something worse -- and blowing an
// emergency power must not be the thing that destroys it.
const UnreadableSuffix = ".unreadable"

// Fuse is one blown fuse: when, and why. Both are recorded so a fuse found at 2am can be
// audited without asking anyone, and both are read back defensively because your person
// HAND-EDITS this file -- that is the only lockdown-replacement mechanism there is.
type Fuse struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

// Box is the whole fuse box. Lockdown is a pointer so that absent, null and present are
// three distinguishable states rather than one zero value.
type Box struct {
	Lockdown   *Fuse           `json:"lockdown"`
	Quarantine map[string]Fuse `json:"quarantine"`
}

// Surface normalizes a surface name for storage and for matching. See notes 4 and 5:
// control characters fold to spaces before the lower-casing, which widens the class of
// spellings that count as one surface exactly as the lower-casing does. That is not a
// one-way "blocks more" guarantee -- it makes `check` refuse on more spellings AND makes
// `lift quarantine` remove more of them, because they are one surface in both directions.
func Surface(s string) string { return strings.ToLower(Fold(s)) }

// OneLine renders free text for an event line. See note 5: the box is hand-editable and
// world-readable by design, so a reason, a stored surface name or an `at` stamp is
// authored by whoever can write the file -- and one line per event is a promise this
// tool makes to every caller scanning the grammar in SPEC.md.
//
// Every control character (Unicode category Cc: the C0 range including \n, \r and \t,
// DEL, and the C1 range) becomes a visible escape: \xNN for a code point below U+0080,
// \uNNNN above it, both in lower-case hex. So do U+2028 and U+2029, the Unicode line and
// paragraph separators, which are Zl and Zp rather than Cc: they break a line for
// Python's str.splitlines and for every UAX-14 line breaker, which is a forged line for
// those readers and for no others. Everything else printable passes through untouched,
// including non-ASCII.
//
// A byte that is not valid UTF-8 is escaped by its own value in the same \xNN form. Note
// where that is and is not reachable: box content arrives through the JSON decoder, which
// substitutes U+FFFD for an invalid byte before this function ever sees it, and Fold does
// the same to this tool's own writes through strings.Map. So the byte form is reached only
// by text that never passed through the decoder, such as the text of an error.
//
// It is deterministic, and it never shortens text to nothing: a reason stays readable,
// which is the whole point of recording one.
func OneLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\u2028' || r == '\u2029':
			// Not Cc, and not caught by IsControl. They still break a line for readers
			// that follow Unicode rather than counting \n, so they are escaped here.
			fmt.Fprintf(&b, `\u%04x`, r)
		case !unicode.IsControl(r):
			b.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// Fold tidies text this tool is about to WRITE: every control character becomes a space,
// then runs of whitespace collapse to a single ASCII space and the ends are trimmed. The
// collapse is Unicode-aware, so a non-breaking space or a line separator inside the text
// becomes an ordinary space too. It is not the defense -- OneLine is, because a box
// written by another hand still arrives holding anything at all (note 5). And it is never
// a REFUSAL: a fuse you cannot blow is not a fuse, so a reason is accepted whatever it
// contains and only its spelling in the file is tidied.
func Fold(s string) string {
	folded := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(folded), " ")
}

// Quarantined answers whether this surface is blocked, returning the key AS STORED so a
// refusal can quote the file rather than the caller's spelling of it.
//
// An empty surface matches NOTHING, and the caller must say so out loud: a check with no
// surface has verified only that there is no lockdown, and a printed claim must never
// outrun what was measured.
func (b Box) Quarantined(surface string) (string, Fuse, bool) {
	want := Surface(surface)
	if want == "" {
		return "", Fuse{}, false
	}
	// SORTED, never map order. A hand-edited box can hold two spellings of ONE surface
	// (note 4), and map iteration is randomized -- so answering with whichever match came
	// first made the quoted name, the timestamp and the reason a coin flip between runs.
	// Status was already pinned deterministic; the gate's own refusal was not.
	for _, k := range b.Surfaces() {
		if Surface(k) == want {
			return k, b.Quarantine[k], true
		}
	}
	return "", Fuse{}, false
}

// LiftQuarantine removes EVERY quarantine entry matching surface (normalized, see note 4)
// and returns the removed entries under their stored keys, so the caller can announce
// exactly what was lifted and why it had been blown. An empty surface matches NOTHING,
// mirroring Quarantined: a missing argument must never empty the box.
//
// Every match is removed, not just the first: the box is hand-editable, so two spellings
// of one surface can coexist, and a lift that removed only one would leave the surface
// still answering as quarantined -- a lift that verifies its own failure.
//
// So the normalization in note 4 WIDENS this: the coarser the equivalence, the more
// spellings one lift removes. `lift "dis cord"` on a box holding "dis cord", "dis\tcord"
// and "DIS\x01CORD" removes all three, where a build without the fold removed one. That
// is the same design case already had (lifting "discord" removes "Discord"), it is what
// "they are one surface" means, and nothing is hidden by it: every removed entry is
// returned here under its stored spelling for the caller to announce.
//
// THE SOFT HALF ONLY. The fuse design separates the powers: quarantine is your own
// decision in both directions, so this function exists; lockdown is hard -- a blown fuse
// is not reset, it is REPLACED, and only in a live conversation with your person -- so no
// LiftLockdown exists here, and none may be added.
func (b Box) LiftQuarantine(surface string) map[string]Fuse {
	removed := map[string]Fuse{}
	want := Surface(surface)
	if want == "" {
		return removed
	}
	for k, v := range b.Quarantine {
		if Surface(k) == want {
			removed[k] = v
			delete(b.Quarantine, k)
		}
	}
	return removed
}

// Surfaces lists the quarantined surfaces in a fixed order. Map iteration is randomized,
// so an unsorted listing would print a different order every run -- and a status output
// that reorders itself is one a reader stops diffing.
func (b Box) Surfaces() []string {
	names := make([]string, 0, len(b.Quarantine))
	for k := range b.Quarantine {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ReadBox returns the fuse box, or the reason it could not be read. See note 1.
//
// A nil error with an empty Box means VERIFIED CLEAR. A non-nil error means CANNOT TELL,
// and the only correct treatment of that is BLOWN.
func ReadBox(path string) (Box, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nonexistent, verified: the read failed with the one error that means "not
			// there" rather than "could not look". fs.ErrNotExist cannot say WHICH part of
			// the path is missing -- the box file or a parent directory -- so a path into a
			// directory that does not exist also lands here and answers CLEAR. That is
			// accepted, per note 1 in the package comment, and pinned by test.
			return Box{Quarantine: map[string]Fuse{}}, nil
		}
		return Box{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var b Box
	if err := json.Unmarshal(data, &b); err != nil {
		return Box{}, fmt.Errorf("%s is not readable JSON: %w", path, err)
	}
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	return b, nil
}

// WriteBox replaces the fuse box atomically. See note 3.
func WriteBox(path string, b Box) error {
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// The temp file is created in the SAME directory, because rename is only atomic within
	// one filesystem and the system temp dir is not guaranteed to be on this one.
	tmp, err := os.CreateTemp(dir, ".fuses-*.json.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// Harmless once the rename has taken the file away; the point is the failure paths,
	// where a litter of .fuses-*.tmp beside the box would be the only trace left.
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// Sync before rename: a rename that lands while the CONTENT is still in the page cache
	// gives a crash the chance to leave an empty file under the real name, which is the
	// torn write this whole dance exists to prevent.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes 0600. The box is not a secret and other tools must be able to read
	// it; a fuse nobody else can see is a fuse that stops nothing.
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// PreserveUnreadable copies an unreadable box aside before it is replaced. It returns the
// destination it TRIED, so the caller can name it either way -- reporting where the bytes
// went and reporting that they could not be saved are both better than silence.
func PreserveUnreadable(path string) (string, error) {
	dst := path + UnreadableSuffix
	data, err := os.ReadFile(path)
	if err != nil {
		return dst, err
	}
	return dst, os.WriteFile(dst, data, 0o644)
}
