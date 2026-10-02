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
    can name the right remedy (CreateBox for the first, a hand repair for the
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
    the process dies, and a half file is an unreadable box that only a person can
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
    sequence does the same to an operator's terminal. cmd/nova-fuse prints every
    such string through internal/oneline's Escape, which escapes every control
    character as the text is printed and so holds for a box this tool never
    wrote; Fold only tidies what this tool writes itself, and is never a
    refusal, because a fuse you cannot blow is not a fuse. Constraining writes
    alone would defend exactly the case that needs no defending. The escaped set
    is category Cc plus U+2028 and U+2029, which break a line for readers that
    follow Unicode rather than counting newlines. Folding also widens the surface
    classes of note 4, in both directions -- see there.
*/
package fuse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// UnreadableSuffix names where the bytes of an unreadable box are kept when a lockdown has
// to clobber it. Back up before mutating what cannot be reconstructed: a corrupt fuse box is
// evidence -- of a torn write, a bad hand-edit, or something worse -- and blowing an
// emergency power must not be the thing that destroys it.
const UnreadableSuffix = ".unreadable"

// Fuse is one blown fuse: when, and why. Both are recorded so a fuse found at 2am can be
// audited without asking anyone, and both are read back defensively because a person
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

// Fold tidies text this tool is about to WRITE: every control character becomes a space,
// then runs of whitespace collapse to a single ASCII space and the ends are trimmed. The
// collapse is Unicode-aware, so a non-breaking space or a line separator inside the text
// becomes an ordinary space too. It is not the defense (oneline.Escape at print time is), because a box
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
// is not reset, it is REPLACED, and only in a live conversation with the person you work with -- so no
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
	return slices.Sorted(maps.Keys(b.Quarantine))
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
	b, err := decodeBox(data)
	if err != nil {
		return Box{}, fmt.Errorf("%s is not a box: %w", path, err)
	}
	return b, nil
}

// decodeBox accepts exactly a box: one JSON object whose keys are only
// "lockdown" (absent, null or a fuse) and "quarantine" (absent, null or an
// object of fuses), each at most once, with nothing after it. A hand-edited box
// may leave a key out, and the existing tests pin that, so `{}` is an empty box.
// Anything else is CANNOT TELL (note 2): json.Unmarshal reads `null`, `[]` or an
// object of misspelled keys into a zero Box with no error, and a zero Box is
// VERIFIED CLEAR, so the shape is checked before the value is trusted.
func decodeBox(data []byte) (Box, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return Box{}, errors.New("not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return Box{}, err
		}
		k, _ := tok.(string)
		if seen[k] {
			return Box{}, fmt.Errorf("key %q given twice", k)
		}
		seen[k] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return Box{}, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return Box{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Box{}, errors.New("bytes after the object")
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	var b Box
	if err := strict.Decode(&b); err != nil {
		return Box{}, err
	}
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	return b, nil
}

// CreateBox makes an empty box at path, only where nothing is: it NEVER replaces a
// box, because replacing one is the lockdown reset this package does not have. The
// write uses atomicfile.NoReplace for atomic, exclusive creation. A box that
// appears between the check and publication is kept, and the error is fs.ErrExist.
// See atomicfile.NoReplace for its publication mechanism and filesystem requirements.
func CreateBox(path string) error {
	return writeBox(path, Box{Quarantine: map[string]Fuse{}}, atomicfile.NoReplace())
}

// WriteBox replaces the fuse box atomically. See note 3.
// A symlink at the cleaned path is refused and is not followed.
func WriteBox(path string, b Box) error {
	return writeBox(path, b)
}

// PlanCreateBox is CreateBox with nothing written: every check the creation
// makes (the parent as MkdirAll would make it, no symlink parent, a directory
// this process can create in, nothing at the path), and the same error.
func PlanCreateBox(path string) error { return planBox(path, atomicfile.NoReplace()) }

// PlanWriteBox is WriteBox with nothing written, refusing where WriteBox would.
func PlanWriteBox(path string) error { return planBox(path) }

func planBox(path string, opts ...atomicfile.Option) error {
	target := path
	if target != "" {
		target = filepath.Clean(target)
	}
	return atomicfile.CheckAfterMkdirAll(target, 0o644, append(opts, atomicfile.ExactMode())...)
}

// writeBox shares validation, exact mode and sync ordering between creation and
// replacement. NoReplace makes creation exclusive even if another caller wins
// after validation.
func writeBox(path string, b Box, opts ...atomicfile.Option) error {
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(target, data, 0o644, append(opts, atomicfile.ExactMode())...)
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
