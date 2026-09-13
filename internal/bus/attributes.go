package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttributesName is the file at the bus ROOT that tells git how to merge the two lane
// files this tool only ever appends to.
const AttributesName = ".gitattributes"

// attributeLines are the two rules, and they are the whole of the file this tool writes.
//
// `merge=union` is git's own built-in low-level driver and it does exactly what an
// append-only file wants: on a conflict it keeps BOTH sides' lines instead of writing
// conflict markers. An INDEX is a lane's catalogue and a RECEIPTS is a lane's log; two
// benches of one line each add a line at the end of one of them over one base, which is an
// edit/edit at that spot and used to stop the rebase and wedge the bench.
//
// The pattern is `from-*/INDEX` and not `INDEX`, because a gitattributes pattern holding a
// slash is anchored at the file's own directory and `*` does not cross one -- so this is
// exactly "a file called INDEX one level inside a directory whose name begins with from-",
// which is a lane's catalogue and nothing else on the bus.
//
// It is written by `send` and not by `check`, and only when the rule is not already there,
// because it is a change to a shared file: a verb that reports has no business editing the
// bus, and a verb that publishes a note is already committing and pushing under an
// identity the roster names.
var attributeLines = []string{
	"# nova-bus: INDEX and RECEIPTS are append-only line files. Union merge keeps both",
	"# sides' lines, so two benches of one lane appending at once do not conflict.",
	"from-*/INDEX merge=union",
	"from-*/RECEIPTS merge=union",
}

// EnsureMergeAttributes puts the union rules on the bus if they are not there, and
// reports whether it wrote anything -- which is the caller's cue to add AttributesName to
// the paths its commit names.
//
// A bus that already has a .gitattributes for its own reasons is APPENDED to rather than
// replaced: the file belongs to the bus and this tool owns two lines of it. A bus that
// already carries both rules is left exactly as it is, so this is a no-op on every send but
// the first.
// ExpectedMergeAttributes calculates the expected content of .gitattributes after
// ensuring the required merge attribute rules are present. It returns the expected
// content and whether any changes were made.
func ExpectedMergeAttributes(existing string) (string, bool) {
	var missing []string
	for _, line := range attributeLines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !hasAttributeLine(existing, line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return existing, false
	}
	var b strings.Builder
	if existing != "" {
		b.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			b.WriteString("\n")
		}
	}
	for _, line := range attributeLines {
		if strings.HasPrefix(line, "#") && existing != "" && strings.Contains(existing, "nova-bus:") {
			continue
		}
		if strings.HasPrefix(line, "#") || contains(missing, line) {
			b.WriteString(line + "\n")
		}
	}
	return b.String(), true
}

func EnsureMergeAttributes(root string) (bool, error) {
	full := filepath.Join(root, AttributesName)
	if err := insideRoot(root, full); err != nil {
		return false, err
	}
	raw, err := os.ReadFile(full)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	expected, changed := ExpectedMergeAttributes(string(raw))
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(full, []byte(expected), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureMergeAttributesFrom reconciles .gitattributes against a verified base: the bytes
// the remote tracking ref currently holds. The expected content derives from that base,
// never from whatever partial bytes a killed recovery left on disk, so a truncated comment
// or rule is completed to the standard bytes rather than kept. The on-disk file is
// completed only when it is an exact prefix of the expected bytes; any other content is
// the caller's foreign-byte refusal (verifyPermittedDeltas already refuses it, and the
// check is repeated here so this helper is safe on its own).
//
// It reports whether the base lacks the complete standard contribution -- which is whether
// the caller must stage the attributes delta -- even when the working tree already held
// the complete bytes. A remote that already carries both rules, whatever else it carries,
// is a no-op.
func EnsureMergeAttributesFrom(root, base string) (bool, error) {
	expected, changed := ExpectedMergeAttributes(base)
	full := filepath.Join(root, AttributesName)
	if err := insideRoot(root, full); err != nil {
		return false, err
	}
	raw, err := os.ReadFile(full)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	disk := string(raw)
	if disk == expected {
		return changed, nil
	}
	if !changed {
		// The base already carries the complete contribution, so there is no delta to
		// stage. An on-disk file that is not already the complete bytes is either absent
		// (fine: the remote holds it) or a foreign/partial leftover the caller refuses in
		// verifyPermittedDeltas; either way this helper must not leave an unstaged
		// .gitattributes behind in the working tree.
		return false, nil
	}
	if !strings.HasPrefix(expected, disk) {
		return false, fmt.Errorf("%s has unrelated content on disk; refusing to overwrite", AttributesName)
	}
	if err := os.WriteFile(full, []byte(expected), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// hasAttributeLine reports whether the file already carries a rule, exactly, on a line of
// its own. It is an equality test and not a substring one: a `from-*/INDEX -merge` would
// contain the shorter text and mean the opposite.
func hasAttributeLine(content, want string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
