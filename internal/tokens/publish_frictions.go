package tokens

import (
	"fmt"
	"strings"
)

// FrictionsPublication is the on-disk shape of a published frictions file: one record per
// line, each record the text a `friction add` call produced, in the order it was recorded.
// The file is append-only for the same reason a bus lane is: a record once published is a
// claim that was made, and editing, deleting or reordering it in place would rewrite the
// history the sums are read from. A later publication may only add records at the end.

// FormatFrictionsPublication renders a list of friction records as the file's full byte
// content: each entry on its own line, and a trailing newline after the last. An empty
// list renders the empty string, so a first publication has no phantom leading record.
func FormatFrictionsPublication(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return strings.Join(entries, "\n") + "\n"
}

// ValidateFrictionsAppendTransition reports whether newContent is a strict append-only
// extension of existingContent: every record already present must survive byte-for-byte in
// the same order, and the only permitted change is zero or more new records at the end.
//
// It returns nil when newContent can be reached from existingContent by appending alone,
// and otherwise an error naming the first record that was changed, removed, or moved.
func ValidateFrictionsAppendTransition(existingContent, newContent string) error {
	existing := splitFrictionRecords(existingContent)
	added := splitFrictionRecords(newContent)

	// A deletion, or an insertion that did not keep the existing tail, shows as the new
	// record list being shorter than the old one: records that were there are gone.
	if len(added) < len(existing) {
		for i := len(added); i < len(existing); i++ {
			return fmt.Errorf("friction record %d (%q) was removed; frictions are append-only and may not be deleted", i+1, existing[i])
		}
	}

	// Compare the shared prefix record by record. The first divergence is either a
	// mutation of one record or a reorder that moved a different record into this slot;
	// both are the same refusal, because an append-only file permits neither.
	for i := 0; i < len(existing); i++ {
		if added[i] != existing[i] {
			return fmt.Errorf("friction record %d changed from %q to %q; frictions may not be reordered or changed, only appended", i+1, existing[i], added[i])
		}
	}

	return nil
}

// splitFrictionRecords splits a frictions file's content into its records, one per line,
// dropping the single trailing newline the formatter writes. An empty content has no
// records; a blank line between records is its own (empty) record and is rejected like any
// other mutation, so it cannot silently be used to hide a change.
func splitFrictionRecords(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
