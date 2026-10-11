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
    second). Lstat refuses a link or non-regular path before open; SameFile then
    proves the opened descriptor is the file that was inspected. The read is
    limited to 1 MiB and refuses a file that reaches the limit: a box is only a
    few lines, and an unbounded read lets a directory writer exhaust memory.
    errors.Is with fs.ErrNotExist distinguishes absent from unreadable; it does
    not say which part of the path is missing, and it does not need to, since
    both answers refuse. A box comes into being by
    CreateBox, which never replaces one, or by a write verb on a box that was read.

 2. MALFORMED IS UNREADABLE. A JSON array, a bare string, a truncated file, a
    lockdown whose value is not an object -- every one fails the decode and comes
    back as CANNOT TELL, which every caller must treat as BLOWN. A bare JSON null
    fails no decode -- a struct ignores it with no error -- so the read requires a
    JSON OBJECT at the top level before it decodes anything, and names the kind it
    found instead: a null, an array, a string, a number or a boolean is refused,
    because a wrong-shaped value is CANNOT TELL, never the VERIFIED CLEAR a
    normalised empty map would make of it. Its remedy is a hand restoration, not
    init: init is the remedy for a path with no box and never replaces one. An
    UNKNOWN top-level member is refused too, which is the policy for a gate: a key
    the reader does not know is a key it cannot account for, and answering CLEAR
    over a box someone meant to block with it is the fail-open this package exists
    to prevent. `"quarantine": null` stays deliberately supported -- a box a person
    hand-edits into that shape is a readable empty box. Reaching the
    fail-closed answer by a crash deep inside a caller is not a design; this is.

 3. THE WRITE IS TEMP-FILE + RENAME, PUBLISHED UNDER THE BOX'S LOCK. The file
    whose corruption means PERMANENT LOCKDOWN must never be left torn: a
    truncating write can leave half a file if the process dies, and a half file
    is an unreadable box that only a person can clear, by hand, live. Rename
    within one directory is atomic, so a reader sees the old box or the new one
    and never a fragment. A box mutation (MutateBox) holds the lock file beside
    the box -- <box>.lock, pkg/filelock -- across its read, its change and
    its publish, so two copies of the tool blowing fuses at once land BOTH
    writes, and neither can produce a corrupt box. The one deliberate exception
    is a lockdown whose lock cannot be taken: it blows unserialized rather than
    not at all -- a fuse you cannot blow is not a fuse -- and cmd/nova-fuse says
    so on a NOTE line (BoxLockUntaken tells that failure from a box that cannot
    be written at all).

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
    such string through pkg/oneline's Escape, which escapes every control
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

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
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

// Fold tidies text this tool is about to WRITE: every control character becomes a blank,
// then runs of whitespace collapse to a single ASCII blank and the ends are trimmed. The
// collapse is Unicode-aware, so a non-breaking whitespace character or a line separator inside the text
// becomes an ordinary blank too. It is not the defense (oneline.Escape at print time is), because a box
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

// maxBoxBytes bounds the bytes ReadBox accepts (docs/SPEC.md, nova-fuse: The box).
const maxBoxBytes = 1 << 20

// ReadBox returns the fuse box, or the reason it could not be read. See note 1.
//
// A nil error means the box was read, and an empty Box then means VERIFIED CLEAR. A
// non-nil error -- ErrNoBox included -- means CANNOT TELL, and the only correct
// treatment of that is BLOWN.
func ReadBox(path string) (Box, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing at the path, the box file or a directory above it: no box, so
			// nothing can be proven clear.
			return Box{}, fmt.Errorf("%w at %s", ErrNoBox, path)
		}
		return Box{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Box{}, fmt.Errorf("%s is a symlink; use the real fuse box file path", path)
	}
	if !info.Mode().IsRegular() {
		return Box{}, fmt.Errorf("%s is not a regular file; use the real fuse box file path", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return Box{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // ignored: a read-only close cannot change the parsed bytes
	opened, err := f.Stat()
	if err != nil {
		return Box{}, fmt.Errorf("cannot inspect open fuse box %s: %w", path, err)
	}
	if !os.SameFile(info, opened) {
		return Box{}, fmt.Errorf("%s changed while it was opened; use the real fuse box file path", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBoxBytes))
	if err != nil {
		return Box{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(data) == maxBoxBytes {
		return Box{}, fmt.Errorf("%s reached the %d-byte fuse box limit", path, maxBoxBytes)
	}
	var b Box
	// The top level is required to be the object a box is, before any decoding: a
	// null, an array, a string, a number or a boolean decodes into a Box with no
	// error -- a null leaves it zero -- and a zero box reads as VERIFIED CLEAR, a
	// fail-open in a safety control reached by one hand-edited byte (note 2).
	if kind, object, found := topLevelKind(data); found {
		if !object {
			return Box{}, fmt.Errorf("%s is not a box: top level is %s; %s", path, kind, restoreBoxRemedy)
		}
		if err := rejectDuplicateBoxMembers(data); err != nil {
			return Box{}, fmt.Errorf("%s is not readable JSON: %w", path, err)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// A box is an object with the two members SPEC.md names, and an unknown member
	// is refused: a gate that quietly ignored a key it does not know would answer
	// CLEAR over a box a hand or another tool meant to block with it. The safe
	// reading for a gate is to refuse, and the refusal is CANNOT TELL like every
	// other shape (note 2).
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Box{}, fmt.Errorf("%s is not readable JSON: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		// A decode stops at the end of the first value, so without this a box with
		// anything after it would read as the value before the trailing bytes.
		return Box{}, fmt.Errorf("%s is not readable JSON: data follows the box object", path)
	}
	if b.Quarantine == nil {
		b.Quarantine = map[string]Fuse{}
	}
	return b, nil
}

// rejectDuplicateBoxMembers walks the top-level object before struct decoding. JSON field
// matching is case-insensitive, so spelling aliases of either box member are duplicates
// too; note 2 treats an ambiguous hand-edited box as unreadable, never clear.
func rejectDuplicateBoxMembers(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil { // ReadBox already established the opening object token.
		return err
	}
	seen := make(map[string]struct{}, 2)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("top-level box member name is not a string")
		}
		canonical := ""
		switch {
		case strings.EqualFold(key, "lockdown"):
			canonical = "lockdown"
		case strings.EqualFold(key, "quarantine"):
			canonical = "quarantine"
		}
		if canonical != "" {
			if _, exists := seen[canonical]; exists {
				return fmt.Errorf("duplicate top-level member %q", key)
			}
			seen[canonical] = struct{}{}
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	_, err := dec.Token() // Consume the object's closing brace.
	return err
}

// restoreBoxRemedy is the remedy for a box whose top level is not an object. It
// is a hand restoration, not `nova-fuse init --box <path>`: init is the remedy for
// a path where no box is, and it never replaces a box, so naming it here would
// send the reader to a verb that refuses and leave the bytes that need restoring
// where they are. A lockdown is replaced only in a live conversation with the
// person you work with.
const restoreBoxRemedy = "restore the box file by hand with the person you work with (nova-fuse init makes a box only where none is, so it will not replace this one)"

// topLevelKind names the JSON value at the top of data and answers whether it is
// the object a box is. found is false when data holds no JSON value at all, which
// the decode answers with its own error (a truncated file, an empty file).
func topLevelKind(data []byte) (kind string, object, found bool) {
	tok, err := json.NewDecoder(bytes.NewReader(data)).Token()
	if err != nil {
		return "", false, false
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			return "an object", true, true
		}
		return "an array", false, true
	case nil:
		return "null", false, true
	case bool:
		return "a boolean", false, true
	case string:
		return "a string", false, true
	default:
		return "a number", false, true
	}
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

// BoxLockUntaken reports whether a box mutation failed because the box's LOCK
// could not be taken -- another writer holds it, the wait timed out, or only
// askers stood in the way (pkg/filelock's ErrHeld, ErrTimeout and ErrBusy;
// MutateBox returns that failure verbatim). It tells "the box is being mutated
// elsewhere right now" from "this box cannot be written here at all", and the
// difference is a policy, not a detail: a lockdown answers the first by blowing
// unserialized and saying so (a fuse you cannot blow is not a fuse), while every
// other failure stays a failed blow. See note 3.
func BoxLockUntaken(err error) bool {
	return errors.Is(err, filelock.ErrHeld) ||
		errors.Is(err, filelock.ErrTimeout) ||
		errors.Is(err, filelock.ErrBusy)
}

// PlanCreateBox is CreateBox with nothing written: every check the creation
// makes (the parent as MkdirAll would make it, no symlink parent, a directory
// this process can create in, nothing at the path), and the same error.
func PlanCreateBox(path string) error { return planBox(path, atomicfile.NoReplace()) }

func planBox(path string, opts ...atomicfile.Option) error {
	target := path
	if target != "" {
		target = filepath.Clean(target)
	}
	if err := checkBoxAncestors(target); err != nil {
		return err
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
	if err := checkBoxAncestors(target); err != nil {
		return err
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
	// Security finding 74.5: evidence preservation accepts a regular source
	// only and refuses a source that is a symlink at inspection.
	source, err := os.Lstat(path)
	if err != nil {
		return dst, err
	}
	if !source.Mode().IsRegular() {
		return dst, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return dst, err
	}
	cleanDst := dst
	if cleanDst != "" {
		cleanDst = filepath.Clean(cleanDst)
	}
	mode := source.Mode().Perm()
	opts := []atomicfile.Option{atomicfile.ExactMode()}
	if fi, err := os.Lstat(cleanDst); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	// Atomic write per pkg/atomicfile model: writes dst atomically, preserving
	// any existing destination permissions (or using the regular source's mode)
	// via temporary file and rename so preserved unreadable box evidence is never
	// left torn or mode-widened.
	return dst, atomicfile.WriteFile(cleanDst, data, mode, opts...)
}
