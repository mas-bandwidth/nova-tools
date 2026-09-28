/*
Package fuse is the STATE half of the ingestion fuse: reading and writing the box,
the JSON file the two emergency powers live in. cmd/nova-fuse is the thin command
on top of it. The box's path always comes from the caller — there is no default
location and no environment variable, per this repo's no-guessed-paths rule.

WHAT IS ACTUALLY DECIDED HERE, and why each one is not arbitrary:

 1. THE READ HAS ONE YES AND TWO NOES. A readable box says what it says. "cannot
    read the box" is CANNOT TELL, and "no box at the path" is CANNOT TELL too: a
    fuse box that is not where the caller said can prove nothing is blown, and a
    reader that answered it with an empty box turned a mistyped --box, a box
    moved or deleted, or a second --box pointing somewhere empty into VERIFIED
    CLEAR -- a fail-open in a safety control. Both noes are errors, and every
    caller must treat an error as BLOWN; ErrNoBox tells them apart, so a refusal
    can name the right remedy (CreateBox for the first, your person for the
    second). os.ReadFile + errors.Is(err, fs.ErrNotExist) finds the absent case,
    which is why the read is written with the stdlib primitive that distinguishes
    absent from unreadable; it does not say which part of the path is missing, and
    it does not need to, since both answers refuse. A box comes into being by
    CreateBox, which never replaces one, or by a write verb on a box that was read.

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

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
// The escape itself lives in internal/oneline, because the promise is made by every
// binary in this repo and has to be met the same way by each: OneLine is oneline.Escape
// under the name this package has always used, and the table test here pins that the two
// never drift. See oneline.Escape for the escaped set (category Cc, U+2028 and U+2029,
// and the bidi controls) and the escape form.
func OneLine(s string) string { return oneline.Escape(s) }

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

// ErrNoBox is ReadBox's answer when nothing is at the path: CANNOT TELL, like an
// unreadable box, and told apart from it only so a refusal can name its remedy
// (CreateBox). See note 1.
var ErrNoBox = errors.New("no box")

// ReadBox returns the fuse box, or the reason it could not be read. See note 1.
//
// A nil error means the box was read, and an empty Box then means VERIFIED CLEAR. A
// non-nil error -- ErrNoBox included -- means CANNOT TELL, and the only correct
// treatment of that is BLOWN.
func ReadBox(path string) (Box, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing at the path, the box file or a directory above it: no box, so
			// nothing can be proven clear.
			return Box{}, fmt.Errorf("%w at %s", ErrNoBox, path)
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

// CreateBox makes an empty box at path, only where nothing is: it NEVER replaces a
// box, because replacing one is the lockdown reset this package does not have. The
// write is note 3's temp file, linked into place, so the create is atomic and
// exclusive at once: a box that appears between the check and the write is kept, and
// the error is fs.ErrExist.
func CreateBox(path string) error {
	name, err := writeTemp(path, Box{Quarantine: map[string]Fuse{}})
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(name) }()
	return os.Link(name, path)
}

// WriteBox replaces the fuse box atomically. See note 3.
// A symlink at the cleaned path is refused and is not followed.
func WriteBox(path string, b Box) error {
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := path
	if target != "" {
		target = filepath.Clean(target)
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Atomic write per internal/atomicfile model: temporary file created
	// exclusively in parent directory, exact 0o644 mode via ExactMode(),
	// fsync to media, and atomic rename over target path. The box is not
	// a secret and other tools must be able to read it; a fuse nobody else can
	// see is a fuse that stops nothing.
	return atomicfile.WriteFile(target, data, 0o644, atomicfile.ExactMode())
}

// writeTemp writes b, synced and world-readable, to a temp file beside path and returns
// its name; the caller renames or links it into place and removes it.
func writeTemp(path string, b Box) (string, error) {
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// The temp file is created in the SAME directory, because rename is only atomic within
	// one filesystem and the system temp dir is not guaranteed to be on this one.
	tmp, err := os.CreateTemp(dir, ".fuses-*.json.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		_ = os.Remove(name)
		return "", err
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	// Sync before rename: a rename that lands while the CONTENT is still in the page cache
	// gives a crash the chance to leave an empty file under the real name, which is the
	// torn write this whole dance exists to prevent.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	// CreateTemp makes 0600. The box is not a secret and other tools must be able to read
	// it; a fuse nobody else can see is a fuse that stops nothing.
	if err := os.Chmod(name, 0o644); err != nil {
		return fail(err)
	}
	return name, nil
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
	cleanDst := dst
	if cleanDst != "" {
		cleanDst = filepath.Clean(cleanDst)
	}
	mode := os.FileMode(0o644)
	var opts []atomicfile.Option
	if fi, err := os.Lstat(cleanDst); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
		opts = append(opts, atomicfile.ExactMode())
	}
	// Atomic write per internal/atomicfile model: writes dst atomically, preserving
	// any existing destination permissions (or defaulting to 0o644 subject to umask)
	// via temporary file and rename so preserved unreadable box evidence is never
	// left torn or mode-widened.
	return dst, atomicfile.WriteFile(cleanDst, data, mode, opts...)
}
