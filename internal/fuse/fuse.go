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
    errors.Is(err, fs.ErrNotExist) tells the two apart, which is why the read is
    written with the stdlib primitive that distinguishes them rather than the one
    that does not.

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

 4. SURFACE NAMES ARE MATCHED NORMALIZED. Raw string comparison lets
    `quarantine Discord` then `check discord` answer CLEAR -- a fail-OPEN in a
    safety control, reached by a capital letter. Normalizing can only ever collapse
    two names into one, which blocks more and never less.
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

// Surface normalizes a surface name for storage and for matching. See note 4.
func Surface(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

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
	for k, v := range b.Quarantine {
		if Surface(k) == want {
			return k, v, true
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
			// The box was never created. That is a fact, not a guess: the read reached the
			// directory and the directory said the file is not there.
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
