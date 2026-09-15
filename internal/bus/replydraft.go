package bus

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// This file is the BENCH SIDE of the reply transaction: the name a generated reply lands
// under, and the publish that refuses to overwrite one.
//
// Nothing here touches the bus. A draft is a file on a bench, outside the checkout, and
// the whole of what this file guarantees is that two runs composing one name leave one
// file and one refusal -- never one file and a silent loss.

// LegacyIDPrefix is what a target with no `Id:` line is named by on the bench, and never on
// the bus: the `Re:` line still carries the path.
const LegacyIDPrefix = "legacy-"

// legacyIDHex is how much of the digest the derived id carries.
const legacyIDHex = 12

// LegacyDraftID is the derived id of a note written before ids: the literal `legacy-`
// followed by the first 12 lowercase hex digits of the SHA-256 of the note's repo-relative
// path, exactly as that path appears on the `Re:` line the draft writes.
//
// WHY A DERIVED ID AND NOT THE PATH. An id is a lane name and hex, so it is one path
// segment by construction; a repo-relative path holds `/`, and a filename built from one
// would put the draft in a directory nobody named, or in none at all. The derived form is
// one segment, it is deterministic, and it is the BENCH's name for the target and not the
// bus's.
//
// It is a name and not a proof: a truncated digest can in principle collide, and what
// stands behind the file is the create-exclusive publish below, which refuses an existing
// path rather than claiming two targets cannot reach one.
func LegacyDraftID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return LegacyIDPrefix + hex.EncodeToString(sum[:])[:legacyIDHex]
}

// ErrDraftExists is the refusal a publish makes when something already stands at the final
// name. The one thing that can be there is a draft of this same reply that somebody is
// editing in another window.
var ErrDraftExists = errors.New("a draft already exists at this path")

// ErrNoExclusivePublish is the refusal when the draft directory's filesystem offers neither
// of the two create-exclusive publishes. It is a refusal and never a fall back to a
// replacing rename, because that is the race the rule exists to close.
var ErrNoExclusivePublish = errors.New("this filesystem offers no create-exclusive publish")

// linkFile and noReplacePublish are the two create-exclusive publishes, as vars so that a
// test can stand in for a filesystem that offers one, the other or neither. A filesystem
// with no hard links is not a thing a test can ask a disk for on the machines this builds
// on, and the row of the refusal table that covers it has to be asserted somewhere.
var (
	linkFile         = os.Link
	noReplacePublish = noReplaceRename
)

// PublishNoReplace writes one complete draft into dir and publishes it onto name.
//
// Two steps, and the second is the one that matters. First the whole file goes into a
// unique temporary inside dir -- a name drawn from the OS random source, created
// exclusively, refusing to follow a symlink, written, flushed and closed -- which is what
// keeps a kill from leaving half a reply that looks sendable. Then it is published
// NO-REPLACE: hard-link onto the final name and unlink the temporary, which is atomic and
// fails with an already-exists error rather than replacing on every POSIX filesystem and on
// NTFS; where the filesystem refuses hard links, the operating system's own no-replace
// rename; and where neither is available, a refusal.
//
// THE REFUSAL IS MADE BY THE PUBLISH AND NEVER BY A CHECK IN FRONT OF IT. Two bus checkouts
// on one bench can be pointed at one draft directory, and two readers of one lane compose
// the same name by construction: a check that finds nothing, followed by an ordinary
// rename, replaces whatever was created in the gap and the loser's draft is gone with no
// line printed anywhere. There is no pre-check here at all, advisory or otherwise, so there
// is nothing that could speak in the publish's place.
//
// The temporary is removed on every failing path, so a refused run leaves dir holding
// exactly what it held before and no stray temporary beside it. A process KILLED between
// the create and the publish is a different thing and is not promised against: what it can
// leave is a temporary, which is never a name `send` will read.
func PublishNoReplace(dir, name string, content []byte) (string, error) {
	final := filepath.Join(dir, name)
	temp, err := draftTempPath(dir)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(temp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(temp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(temp)
		return "", err
	}
	linkErr := linkFile(temp, final)
	switch {
	case linkErr == nil:
		os.Remove(temp)
		return final, nil
	case errors.Is(linkErr, os.ErrExist):
		os.Remove(temp)
		return "", fmt.Errorf("%s: %w", final, ErrDraftExists)
	}
	// The filesystem refuses hard links -- some network mounts, some container overlays,
	// FAT. The operating system's own no-replace rename is the second way, and where the
	// platform has none this refuses rather than falling back to a rename that replaces.
	//
	// THE LINK'S ERROR IS CARRIED HERE and not thrown away. The refusal has to name "the
	// call it tried and what the call said", and the first version reported only the
	// SECOND call's words -- so a reader was told what the fallback said about a
	// filesystem whose actual complaint came from the call before it.
	switch renameErr := noReplacePublish(temp, final); {
	case renameErr == nil:
		return final, nil
	case errors.Is(renameErr, os.ErrExist):
		os.Remove(temp)
		return "", fmt.Errorf("%s: %w", final, ErrDraftExists)
	default:
		os.Remove(temp)
		return "", fmt.Errorf("%s: link said %q and %s said %q: %w", dir, linkErr, noReplaceRenameCall, renameErr, ErrNoExclusivePublish)
	}
}

// draftTempPath is the unique temporary one publish writes through. Unpredictable, because
// a fixed name in a shared directory is a path somebody else's run -- or a planted link --
// can be waiting at, and the O_EXCL above refuses even a lucky one rather than truncating
// it.
func draftTempPath(dir string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the OS random source would not supply a temporary name in %s: %w", dir, err)
	}
	return filepath.Join(dir, "."+hex.EncodeToString(b[:])+TempSuffix), nil
}

// OffTheListing is why a note on the bus is on none of this reader's listing, answered by
// the same two rules the listing itself applies -- so there is one spelling of each and not
// two that could drift.
func OffTheListing(c *Config, n *Note, me Participant, legacy LegacyLine) (addressed, behindTheLine bool) {
	return addressedTo(c, n, me), legacy.covers(n.legacyDay())
}
