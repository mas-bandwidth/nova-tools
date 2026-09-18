package pulse

// watch is SPEC-PULSE's "## Watch" verb (#1142): one bounded process that waits on the
// merge queue, the bus and the job roots at once and prints one line per change. It is the
// waiter a coordinator used to hand-write a dozen times a night. It is deliberately NOT
// the daemon "What this draft does not do" forbids: a named --until event or the --cap
// ends it, it writes no state of its own, and it holds its last snapshot in memory only,
// so it reports changes and never a backlog. Every path comes from a flag, a refusal is
// exit 2 with one remedy, and every line is one line -- the reasons docs/nova-lessons.md
// records.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// watchPollFloor is the fastest watch ever polls: the 30 s floor the spec names, and the
// smallest --cap that is not refused.
const watchPollFloor = 30 * time.Second

// WatchInput is everything the watch verb needs, held apart from flag parsing so a test
// can drive it against a fake queue, a fake bus checkout, a fake bench tree and a fake
// clock.
type WatchInput struct {
	Queue  string        // the merge queue: entry rows, the MERGED line and removal rows
	Bus    string        // the nova-bus checkout whose addressed notes are watched
	Jobs   string        // the job roots: <root>/<slot>/jobs/<label>/RESULT.md
	Until  string        // the event that ends the watch: pr=<n> merged, note-from=<name>, job=<label> done
	Cap    time.Duration // the wall it never runs past
	Now    func() time.Time
	Sleep  func(time.Duration)
	Stdout io.Writer
	Stderr io.Writer
}

// Watch waits on the queue, the bus and the job roots and prints one line per change. It
// starts from the state of its first poll, so a backlog is never printed; a named --until
// event ends it at the poll that sees it, before the cap, with WATCH OK and exit 0; the cap
// prints WATCH CAP and exits 3. Every unusable invocation is WATCH REFUSED, exit 2, one
// remedy, and makes no poll.
func Watch(in WatchInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}

	for _, r := range []struct{ val, flag string }{
		{in.Queue, "queue"}, {in.Bus, "bus"}, {in.Jobs, "jobs"}, {in.Until, "until"},
	} {
		if strings.TrimSpace(r.val) == "" {
			return watchRefuse(in.Stderr, fmt.Sprintf("WATCH REFUSED: refusing to guess (%s is required)", r.flag))
		}
	}
	if in.Cap <= 0 {
		return watchRefuse(in.Stderr, "WATCH REFUSED: refusing to guess (cap is required)")
	}
	untilKind, untilValue, ok := parseWatchUntil(in.Until)
	if !ok {
		return watchRefuse(in.Stderr, fmt.Sprintf("WATCH REFUSED until=%s (name one of pr=<n> merged, note-from=<name>, job=<label> done)", oneline.Escape(in.Until)))
	}
	if in.Cap < watchPollFloor {
		return watchRefuse(in.Stderr, fmt.Sprintf("WATCH REFUSED cap=%s (the cap is at least the 30s poll floor)", in.Cap))
	}
	if !isBusCheckout(in.Bus) {
		return watchRefuse(in.Stderr, fmt.Sprintf("WATCH REFUSED bus=%s (name a nova-bus checkout)", oneline.Field(in.Bus)))
	}
	if !isReadableDir(in.Jobs) {
		return watchRefuse(in.Stderr, fmt.Sprintf("WATCH REFUSED jobs=%s (name a readable jobs root)", oneline.Field(in.Jobs)))
	}

	start := in.Now()
	caller := readWatchOwner(in.Queue)
	prev := readWatchSnapshot(in, caller)
	polls := 1
	changes := 0

	if watchUntilSeen(untilKind, untilValue, prev) {
		return watchOK(in, changes, polls, start)
	}
	for {
		in.Sleep(watchPollFloor)
		if in.Now().Sub(start) >= in.Cap {
			fmt.Fprintf(in.Stdout, "WATCH CAP until=%s changes=%d polls=%d cap=%s\n",
				oneline.Escape(in.Until), changes, polls, in.Cap)
			return 3
		}
		cur := readWatchSnapshot(in, caller)
		polls++
		changes += emitWatchChanges(in.Stdout, prev, cur)
		if watchUntilSeen(untilKind, untilValue, cur) {
			return watchOK(in, changes, polls, start)
		}
		prev = cur
	}
}

func watchRefuse(w io.Writer, line string) int {
	fmt.Fprintln(w, line)
	return 2
}

func watchOK(in WatchInput, changes, polls int, start time.Time) int {
	wall := int64(in.Now().Sub(start) / time.Second)
	fmt.Fprintf(in.Stdout, "WATCH OK until=%s changes=%d polls=%d wall=%ds\n",
		oneline.Escape(in.Until), changes, polls, wall)
	return 0
}

// parseWatchUntil reads the one event form --until accepts: pr=<n> merged, note-from=<name>
// or job=<label> done. Anything else is a refusal and makes no poll.
func parseWatchUntil(s string) (kind, value string, ok bool) {
	s = strings.TrimSpace(s)
	if rest, found := strings.CutPrefix(s, "pr="); found {
		rest, done := strings.CutSuffix(rest, " merged")
		rest = strings.TrimSpace(rest)
		if !done || rest == "" {
			return "", "", false
		}
		if _, err := strconv.Atoi(rest); err != nil {
			return "", "", false
		}
		return "pr", rest, true
	}
	if rest, found := strings.CutPrefix(s, "note-from="); found {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", "", false
		}
		return "note-from", rest, true
	}
	if rest, found := strings.CutPrefix(s, "job="); found {
		rest, done := strings.CutSuffix(rest, " done")
		rest = strings.TrimSpace(rest)
		if !done || rest == "" {
			return "", "", false
		}
		return "job", rest, true
	}
	return "", "", false
}

// isBusCheckout is a bus a reader can list: a directory holding at least one from-* lane,
// which is the only lane shape bus.ReadBus walks.
func isBusCheckout(root string) bool {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	lanes, err := filepath.Glob(filepath.Join(root, "from-*"))
	return err == nil && len(lanes) > 0
}

// isReadableDir is the --jobs root's check: it exists and is a directory.
func isReadableDir(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// readWatchOwner is the caller: the name on the queue's OWNER row, first tab-separated
// field. A queue with no OWNER has no caller and skips no note.
func readWatchOwner(queue string) string {
	raw, err := os.ReadFile(filepath.Join(queue, "OWNER"))
	if err != nil {
		return ""
	}
	line := strings.SplitN(strings.TrimRight(string(raw), "\n"), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// watchSnapshot is the state of the three waits at one poll, in memory and nowhere else.
type watchSnapshot struct {
	enqueued map[int]string // pr -> at
	merged   map[int]string // pr -> at
	removed  map[int]string // pr -> at
	notes    map[string]watchNote
	jobs     map[string]string // RESULT.md path -> line 2 verdict
}

type watchNote struct{ from, to, id string }

func readWatchSnapshot(in WatchInput, caller string) watchSnapshot {
	return watchSnapshot{
		enqueued: readWatchRows(filepath.Join(in.Queue, "ENQUEUED")),
		merged:   readWatchRows(filepath.Join(in.Queue, "MERGED")),
		removed:  readWatchRows(filepath.Join(in.Queue, "REMOVED")),
		notes:    readWatchNotes(in.Bus, caller),
		jobs:     readWatchJobs(in.Jobs),
	}
}

// readWatchRows reads one append-only queue file into pr -> at. Two shapes are read: the
// loop's own `<stamp>\t<KIND>\t<owner/repo>#<n>` row (ledger.go's appendRow, which already
// writes MERGED) and the plain `pr=<n> at=<utc>` row.
func readWatchRows(path string) map[int]string {
	rows := map[int]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return rows
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if pr, at, ok := parseWatchRow(line); ok {
			rows[pr] = at
		}
	}
	return rows
}

func parseWatchRow(line string) (int, string, bool) {
	if f := strings.Split(line, "\t"); len(f) >= 3 {
		at := strings.TrimSpace(f[0])
		ref := strings.TrimSpace(f[len(f)-1])
		if i := strings.LastIndex(ref, "#"); i >= 0 {
			ref = ref[i+1:]
		}
		if pr, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil {
			return pr, at, true
		}
	}
	pr, at, found := 0, "", false
	for _, tok := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(tok, "pr="):
			if n, err := strconv.Atoi(strings.TrimPrefix(tok, "pr=")); err == nil {
				pr, found = n, true
			}
		case strings.HasPrefix(tok, "at="):
			at = strings.TrimPrefix(tok, "at=")
		}
	}
	return pr, at, found
}

// readWatchNotes reads every parsed note under every from-* lane and keeps the addressed
// ones the caller did not write. A file that will not parse is stepped over: one bad note
// on a bus must not stop the wait.
func readWatchNotes(root, caller string) map[string]watchNote {
	notes := map[string]watchNote{}
	lanes, err := filepath.Glob(filepath.Join(root, "from-*"))
	if err != nil {
		return notes
	}
	for _, lane := range lanes {
		entries, err := os.ReadDir(lane)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(lane, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			n, perr := bus.ParseNote(e.Name(), string(raw))
			if perr != nil {
				continue
			}
			from := strings.TrimSpace(n.Header.From)
			to := strings.TrimSpace(n.Header.To)
			if to == "" || from == caller {
				continue
			}
			notes[path] = watchNote{from: from, to: to, id: strings.TrimSpace(n.Header.ID)}
		}
	}
	return notes
}

// readWatchJobs walks each --jobs root for <root>/<slot>/jobs/<label>/RESULT.md and reads
// its line 2 verdict.
func readWatchJobs(roots string) map[string]string {
	jobs := map[string]string{}
	for _, root := range strings.Split(roots, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, "*", "jobs", "*", "RESULT.md"))
		if err != nil {
			continue
		}
		for _, path := range matches {
			jobs[path] = readWatchVerdict(path)
		}
	}
	return jobs
}

func readWatchVerdict(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// emitWatchChanges prints exactly one line per change since the last poll and returns how
// many it printed. A poll that found none prints nothing.
func emitWatchChanges(w io.Writer, prev, cur watchSnapshot) int {
	n := 0
	for _, pr := range sortedPRs(cur.enqueued, prev.enqueued) {
		fmt.Fprintf(w, "QUEUE enqueued pr=%d at=%s\n", pr, oneline.Field(cur.enqueued[pr]))
		n++
	}
	for _, pr := range sortedPRs(cur.merged, prev.merged) {
		fmt.Fprintf(w, "QUEUE merged pr=%d at=%s\n", pr, oneline.Field(cur.merged[pr]))
		n++
	}
	for _, pr := range sortedPRs(cur.removed, prev.removed) {
		fmt.Fprintf(w, "QUEUE removed pr=%d at=%s\n", pr, oneline.Field(cur.removed[pr]))
		n++
	}
	for _, path := range sortedPaths(cur.notes, prev.notes) {
		note := cur.notes[path]
		fmt.Fprintf(w, "NOTE from=%s to=%s id=%s\n", oneline.Field(note.from), oneline.Field(note.to), oneline.Field(note.id))
		n++
	}
	for _, path := range sortedJobPaths(cur.jobs, prev.jobs) {
		label := filepath.Base(filepath.Dir(path))
		fmt.Fprintf(w, "JOB label=%s state=%s path=%s\n", oneline.Field(label), oneline.Field(cur.jobs[path]), oneline.Field(path))
		n++
	}
	return n
}

// sortedPRs is the PRs in cur that prev did not hold, in PR order.
func sortedPRs(cur, prev map[int]string) []int {
	var prs []int
	for pr := range cur {
		if _, seen := prev[pr]; !seen {
			prs = append(prs, pr)
		}
	}
	sort.Ints(prs)
	return prs
}

// sortedPaths is the keys cur holds and prev does not, sorted for a stable replay.
func sortedPaths(cur, prev map[string]watchNote) []string {
	var paths []string
	for path := range cur {
		if _, seen := prev[path]; !seen {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func sortedJobPaths(cur, prev map[string]string) []string {
	var paths []string
	for path := range cur {
		if _, seen := prev[path]; !seen {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// watchUntilSeen folds the named event against one snapshot: a merged PR, a note from a
// name, or a job label whose RESULT.md verdict is done.
func watchUntilSeen(kind, value string, snap watchSnapshot) bool {
	switch kind {
	case "pr":
		pr, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		_, ok := snap.merged[pr]
		return ok
	case "note-from":
		for _, note := range snap.notes {
			if note.from == value {
				return true
			}
		}
		return false
	case "job":
		for path, state := range snap.jobs {
			if filepath.Base(filepath.Dir(path)) == value && state == "done" {
				return true
			}
		}
		return false
	}
	return false
}
