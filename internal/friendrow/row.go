// Package friendrow reads one friend's sprint-table row from that friend's
// own beat.
//
// nova-tools #2686. A friend-row is declared per friend the way a bench-row
// is one row per bench: the caller names the friends, and each name is one
// row or it is nothing. Presence is the beat key friend:<name>, the stamp
// nova-wake beat writes. Width is friend:<name>:width, queue is
// friend:<name>:queue and done is friend:<name>:done: the three counts
// beside that beat, the cells sprint-table-redis prints as working, queue
// and done. All four keys are one MGET.
//
// A declared friend whose beat key is absent is not a row. A width, queue
// or done key left behind, a friend:<name>:last stamp, and a beat for a
// name nobody declared are not a presence. This package does not invent
// one, and it does not render the table.
package friendrow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Prefix is the beat's namespace. The key is friend:<name>, lower case.
	Prefix = "friend:"
	// WidthSuffix is the child-count key beside the beat: friend:<name>:width.
	WidthSuffix = ":width"
	// QueueSuffix is the open-task count: friend:<name>:queue.
	QueueSuffix = ":queue"
	// DoneSuffix is the done count: friend:<name>:done.
	DoneSuffix = ":done"
	// keysPerFriend is the MGET stride: beat, width, queue, done.
	keysPerFriend = 4
)

// Store is the one read a friend-row does. A missing key comes back as "".
// The fleet client implements it; a test can too.
type Store interface {
	MGet(ctx context.Context, keys ...string) ([]string, error)
}

// Row is one declared friend's row. A row is returned only when that
// friend's beat key is present, so a row is a presence and not a guess.
type Row struct {
	Name string
	// Beat is the stamp a caller would show: the beat key's value with
	// surrounding space removed. Empty means that value was blank, not
	// that the key was absent. Presence is the raw value.
	Beat string
	// Width is friend:<name>:width when that key is a whole number,
	// including zero. WidthOK is false when the key is absent or not a
	// count: a missing width is not zero children. Queue and Done are
	// friend:<name>:queue and friend:<name>:done on that same rule.
	Width   int
	WidthOK bool
	Queue   int
	QueueOK bool
	Done    int
	DoneOK  bool
}

// BeatKey is friend:<name> for a declared name. WidthKey, QueueKey and
// DoneKey are the count keys beside it. All four normalize the name, so
// Johnny and johnny are one friend.
func BeatKey(name string) (string, error) {
	n, err := slug(name)
	if err != nil {
		return "", err
	}
	return Prefix + n, nil
}

// WidthKey is friend:<name>:width.
func WidthKey(name string) (string, error) {
	return suffixedKey(name, WidthSuffix)
}

// QueueKey is friend:<name>:queue.
func QueueKey(name string) (string, error) {
	return suffixedKey(name, QueueSuffix)
}

// DoneKey is friend:<name>:done.
func DoneKey(name string) (string, error) {
	return suffixedKey(name, DoneSuffix)
}

func suffixedKey(name, suffix string) (string, error) {
	n, err := slug(name)
	if err != nil {
		return "", err
	}
	return Prefix + n + suffix, nil
}

// Read returns one row per declared friend whose beat key is present, in
// declaration order. A name with no beat is absent from the result. An
// empty declaration reads nothing, including keys the store happens to hold.
// Duplicate names collapse to the first. A store error is returned as given:
// an empty result is not invented in its place. The one MGET asks for the
// beat and then the three counts, width, queue and done.
func Read(ctx context.Context, st Store, names []string) ([]Row, error) {
	if st == nil {
		return nil, fmt.Errorf("friend-row: no store")
	}
	declared := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name, err := slug(raw)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		declared = append(declared, name)
	}
	if len(declared) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(declared)*keysPerFriend)
	for _, name := range declared {
		keys = append(keys,
			Prefix+name,
			Prefix+name+WidthSuffix,
			Prefix+name+QueueSuffix,
			Prefix+name+DoneSuffix)
	}
	vals, err := st.MGet(ctx, keys...)
	if err != nil {
		return nil, err
	}
	if len(vals) != len(keys) {
		return nil, fmt.Errorf("friend-row: store answered %d values for %d keys", len(vals), len(keys))
	}
	rows := make([]Row, 0, len(declared))
	for i, name := range declared {
		base := i * keysPerFriend
		// Presence is the raw value. Any nonempty beat is a friend who
		// is here, even whitespace or a value that is not a stamp.
		// Trim only what a caller shows.
		raw := vals[base]
		if raw == "" {
			// Do not read the counts. A width, a queue or a done with no
			// beat is not a friend who is here.
			continue
		}
		row := Row{Name: name, Beat: strings.TrimSpace(raw)}
		if n, ok := parseCount(vals[base+1]); ok {
			row.Width = n
			row.WidthOK = true
		}
		if n, ok := parseCount(vals[base+2]); ok {
			row.Queue = n
			row.QueueOK = true
		}
		if n, ok := parseCount(vals[base+3]); ok {
			row.Done = n
			row.DoneOK = true
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// slug is the one spelling of a declared friend: lower case, a single
// token of letters, digits and internal hyphens. Anything else is refused
// rather than turned into a key.
func slug(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return "", fmt.Errorf("friend-row: a declared name is empty")
	}
	for i, c := range n {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(n)-1:
		default:
			return "", fmt.Errorf("friend-row: %q is not a friend name", name)
		}
	}
	return n, nil
}

// parseCount accepts a whole number, including zero. Empty, a sign, and
// anything that is not a count are not a width, a queue or a done.
func parseCount(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil {
		return 0, false
	}
	return int(n), true
}
