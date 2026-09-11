/*
Package wake is nova-wake's machinery: the state file that makes a change a
change, the three sources it polls, the view over a bus checkout's commits that
says whether a line has gone silent, and the serve loop that wakes a harness
from outside a session. docs/SPEC-WAKE.md is normative for all of it.

The whole design is one sentence: poll, compute a state value, compare it to the
stored one BYTE FOR BYTE, and a difference is a change. Nothing here decides
whether a change is important -- a window asked to be woken on a change, and
importance is the window's to judge.

This file is the state, and it carries the second lesson of 2026-09-11 in its
codec. One watched entry was stored as a tab-joined string whose last field was
often empty; the reload dropped the trailing empty field, so the reloaded value
never equalled the freshly computed one, and the watcher woke the window every
interval, forever, with nothing to say. A false wake is worse than a missed one,
because the window learns to stop reading. So: the separator is not a character
a reader can eat, every field is escaped before it is joined, and Compose and
Decompose are inverse over every value including the empty one.
*/
package wake

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LRUMax is the ceiling on the two parts of the state that grow with things
// that HAPPEN rather than with things being watched: the bus:line: sighting
// memory and the delivered bus:note: marks. Three hundred entries, least
// recently seen evicted first, and an evicted entry is a change again -- which
// is the safe direction to be wrong in. The prototype deleted ALL of them once
// the count passed 300, which turns every standing error back into a change at
// once: a thundering false wake at exactly the moment the bus is noisiest.
//
// A key a queue record names is exempt whatever its age. Pending is unbounded
// in the state and bounded in the output, never the other way round.
const LRUMax = 300

// Record is one undelivered observation: an entry in the delivery queue. A
// change is delivered when its line has been written to stdout and nothing
// else, so a record leaves the queue only by being printed (rule 11).
type Record struct {
	N     int // queue:<n>, taken from queue:next and never reused
	ID    string
	Key   string
	Value string
}

// State is nova-wake's own file, in a format nova-wake chose. It is not a
// record of anything: delete it and you get a cold start, which is a correct if
// noisier watch. Nothing else may read it as truth.
type State struct {
	raw  map[string]string // every key, in its stored form
	seen map[string]int    // LRU order for the evictable kinds; not compared, never printed
	cold bool
	tick int
}

func newState() *State {
	return &State{raw: map[string]string{}, seen: map[string]int{}, cold: true}
}

// Load reads a state file. A file that is not there is a cold start; a file
// that is there and cannot be parsed is an ERROR and never a silent cold
// start, because a run that starts cold on an unreadable state has swallowed
// everything that moved while no watcher was running and has a correct-looking
// first poll.
func Load(path string) (*State, error) {
	s := newState()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	defer f.Close()
	s.cold = false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := Decompose(line)
		switch len(parts) {
		case 2, 4:
		default:
			return nil, fmt.Errorf("line %d holds %d fields, want 2 (a plain entry) or 4 (a watched entry): %q", n, len(parts), line)
		}
		key := parts[0]
		if key == "" {
			return nil, fmt.Errorf("line %d has an empty key", n)
		}
		if _, dup := s.raw[key]; dup {
			return nil, fmt.Errorf("line %d repeats the key %q; one entry per watched thing", n, key)
		}
		if len(parts) == 4 {
			seen, err := strconv.Atoi(parts[3])
			if err != nil {
				return nil, fmt.Errorf("line %d: the last-seen counter %q is not a number: %v", n, parts[3], err)
			}
			s.seen[key] = seen
			if seen > s.tick {
				s.tick = seen
			}
			s.raw[key] = Compose(parts[1], printedField(printedIDOf(parts[2])))
			continue
		}
		if watchedRow(key) {
			// A watched row that carries no recency is stored as one field
			// holding the composed pair. Normalise its printed half here, so
			// that a state file written before the label is rewritten with it
			// even if nothing observes that key again.
			inner := Decompose(parts[1])
			for len(inner) < 2 {
				inner = append(inner, "")
			}
			s.raw[key] = Compose(inner[0], printedField(printedIDOf(inner[1])))
			continue
		}
		s.raw[key] = parts[1]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// Cold reports whether there was no state file at all. A first run is not a
// change (see the cold-start rule), and cold=true on the opening line says the
// run is in that state.
func (s *State) Cold() bool { return s.cold }

// Save writes the whole map through a temporary file in the same directory and
// an atomic rename, so a call killed by the harness mid-poll leaves either the
// previous state or the new one and never half of either.
//
// The temp file has a FIXED name beside the state rather than a random one in
// a system scratch directory: rule 4 says the only files this tool touches are
// --state, the temp file beside it and <state>.lock, and a rename is only
// atomic within one filesystem anyway.
func (s *State) Save(path string) error {
	keys := s.Keys()
	var b strings.Builder
	for _, k := range keys {
		if seen, ok := s.seen[k]; ok {
			parts := Decompose(s.raw[k])
			for len(parts) < 2 {
				parts = append(parts, "")
			}
			b.WriteString(Compose(k, parts[0], printedField(printedIDOf(parts[1])), strconv.Itoa(seen)))
		} else {
			b.WriteString(Compose(k, s.raw[k]))
		}
		b.WriteString("\n")
	}
	tmp := TempName(path)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// TempName is the one scratch file this tool writes, beside the state it is
// about to replace. It is named here, in one place, so that the tripwire which
// proves this package reaches no system scratch directory has one function to
// read rather than every write site.
func TempName(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".writing")
}

// LockName is the lock file beside the state, holding the pid of the run that
// took it (rule 4).
func LockName(path string) string { return path + ".lock" }

// Keys returns every key in the state, sorted, so that two saves of one map are
// byte-identical and a diff of two state files is readable.
func (s *State) Keys() []string {
	keys := make([]string, 0, len(s.raw))
	for k := range s.raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Get returns a plain (unwatched) entry: bus:advance, fail:<source>,
// serve:<id>, queue:next and the queue records themselves.
func (s *State) Get(key string) (string, bool) {
	v, ok := s.raw[key]
	return v, ok
}

// Set writes a plain entry.
func (s *State) Set(key, value string) { s.raw[key] = value }

// Delete removes an entry.
func (s *State) Delete(key string) {
	delete(s.raw, key)
	delete(s.seen, key)
}

// Newest is the newest observed state value of a watched key.
func (s *State) Newest(key string) (string, bool) {
	raw, ok := s.raw[key]
	if !ok {
		return "", false
	}
	parts := Decompose(raw)
	if len(parts) < 1 {
		return "", false
	}
	return parts[0], true
}

// PrintedID is the delivery id of the value the window was last SHOWN for this
// key, or "-" when it has been shown none.
func (s *State) PrintedID(key string) string {
	raw, ok := s.raw[key]
	if !ok {
		return "-"
	}
	parts := Decompose(raw)
	if len(parts) < 2 {
		return "-"
	}
	return printedIDOf(parts[1])
}

// printedLabel is the State section's form for the second half of a watched
// row: "The stored form of a watched key is `<value>|printed=<id|->`". The
// label is not decoration. A bare id in that field is indistinguishable from
// any other field, so nothing outside this package can read a state file and
// say what the window was shown -- and a guard that greps the file for
// `printed=` over rows written without it can never go red, which is exactly
// what cmd/nova-wake's failed-write guard had quietly become.
const printedLabel = "printed="

// printedField renders the printed half. The empty id and the missing one are
// both "-": the window has been shown nothing for this key.
func printedField(id string) string {
	if id == "" {
		id = "-"
	}
	return printedLabel + id
}

// printedIDOf reads the printed half back, and accepts the BARE form a state
// file written before the label carries: a mind that upgrades mid-watch keeps
// what it was already shown rather than being woken once more by every
// standing thing. The bare form is never written, only read; Save rewrites
// every row it loads in the labelled form.
func printedIDOf(field string) string {
	if field == "" {
		return "-"
	}
	if id, ok := strings.CutPrefix(field, printedLabel); ok {
		if id == "" {
			return "-"
		}
		return id
	}
	return field
}

// Observe compares a freshly polled value against the stored newest one, byte
// for byte, and reports whether this is a change.
//
// A change APPENDS a queue record and then replaces the newest value. It never
// replaces an UNPRINTED value, because the unprinted value is in the queue and
// a queue record leaves only by being printed: red then green with the red
// unprinted prints red, then green, in that order. Nothing coalesces.
func (s *State) Observe(key, value, id string) bool {
	return s.ObserveDisplay(key, value, value, id)
}

// ObserveDisplay is Observe where the line the window reads carries one field
// more than the identity does. A report file is the case: its identity is
// mtime:size and nothing else, because a poll that found the same file must be
// quiet -- and its LINE says new or modified, which is a fact about the stored
// value rather than about the file. Folding that word into the compared value
// would make every second poll of an unchanged file a change, which is the
// false wake of 2026-09-11 wearing different clothes.
func (s *State) ObserveDisplay(key, value, display, id string) bool {
	s.touch(key)
	old, had := s.Newest(key)
	if had && old == value {
		return false
	}
	n := s.nextQueueNumber()
	s.raw[queueKey(n)] = Compose(id, key, display)
	s.raw[key] = Compose(value, printedField(s.PrintedID(key)))
	return true
}

// Record writes a watched value with no queue record and no change: the
// cold-start rule's "records the world and reports nothing", and
// --final-only's suppression at observation time.
func (s *State) RecordOnly(key, value string) {
	s.touch(key)
	s.raw[key] = Compose(value, printedField(s.PrintedID(key)))
}

// Sight records an unrecognised bus line as SEEN: the watched form every other
// key is stored in, and the recency the LRU evicts by. It is the one writer of
// the sighting memory, because a second one that wrote the value without the
// recency is what made the bound a sort by key name -- and a bound by key name
// deletes the same earliest-sorting lines every poll and wakes the window with
// them again, which is the prototype's item 9 in a new costume.
func (s *State) Sight(key string) {
	s.touch(key)
	s.raw[key] = Compose("seen", printedField(s.PrintedID(key)))
}

func (s *State) touch(key string) {
	if !evictable(key) {
		return
	}
	s.tick++
	s.seen[key] = s.tick
}

// watchedRow names the five namespaces the State section calls watched -- the
// ones stored as `<value>|printed=<id|->` -- as against the plain entries
// beside them: bus:advance, fail:<source>, serve:<id>, queue:next and the
// queue records.
func watchedRow(key string) bool {
	for _, prefix := range []string{"bus:line:", "bus:note:", "entry:", "report:", "line:"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func evictable(key string) bool {
	return strings.HasPrefix(key, "bus:line:") || strings.HasPrefix(key, "bus:note:")
}

func queueKey(n int) string { return "queue:" + strconv.Itoa(n) }

func (s *State) nextQueueNumber() int {
	n := 0
	if v, ok := s.raw["queue:next"]; ok {
		n, _ = strconv.Atoi(v)
	}
	s.raw["queue:next"] = strconv.Itoa(n + 1)
	return n
}

// Queue returns the undelivered records in <n> order: oldest first, which is
// the order they are printed in and the reason a line elided by one call's cap
// is printed by the next before anything newer.
func (s *State) Queue() []Record {
	var out []Record
	for k, v := range s.raw {
		rest, ok := strings.CutPrefix(k, "queue:")
		if !ok || rest == "next" {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		parts := Decompose(v)
		if len(parts) != 3 {
			continue
		}
		out = append(out, Record{N: n, ID: parts[0], Key: parts[1], Value: parts[2]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// MarkPrinted deletes a record and writes printed=<its id> for its key. It is
// called ONLY after the record's line reached stdout (rule 11, step 3): a kill
// before this leaves the entry pending, so the duplicate is a repeated wake and
// never a lost one.
func (s *State) MarkPrinted(r Record) {
	delete(s.raw, queueKey(r.N))
	value, ok := s.Newest(r.Key)
	if !ok {
		value = r.Value
	}
	s.raw[r.Key] = Compose(value, printedField(r.ID))
}

// Pending is the length of the delivery queue: every observation this run or an
// earlier one made and has not printed.
func (s *State) Pending() int { return len(s.Queue()) }

// IsPending reports whether any queue record names this key.
func (s *State) IsPending(key string) bool {
	for _, r := range s.Queue() {
		if r.Key == key {
			return true
		}
	}
	return false
}

// Evict holds the two event-shaped parts of the state to LRUMax entries each,
// least recently SEEN first, with every key a queue record names exempt.
func (s *State) Evict() {
	pending := map[string]bool{}
	for _, r := range s.Queue() {
		pending[r.Key] = true
	}
	for _, prefix := range []string{"bus:line:", "bus:note:"} {
		var keys []string
		for k := range s.raw {
			if strings.HasPrefix(k, prefix) && !pending[k] {
				keys = append(keys, k)
			}
		}
		if len(keys) <= LRUMax {
			continue
		}
		sort.Slice(keys, func(i, j int) bool {
			if s.seen[keys[i]] != s.seen[keys[j]] {
				return s.seen[keys[i]] < s.seen[keys[j]]
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys[:len(keys)-LRUMax] {
			s.Delete(k)
		}
	}
}

// Fail records one failed poll of a source and returns the streak, the stamp
// the streak started at and the reason. The streak LIVES IN THE STATE FILE and
// spans calls: a counter that started at zero on every call would never reach
// three over a source whose error text changes, and three in a row is what ends
// a watch loudly (rule 8).
func (s *State) Fail(source, reason string, now time.Time) (n int, since, last string) {
	key := "fail:" + source
	n, since = 0, Stamp(now)
	if v, ok := s.raw[key]; ok {
		parts := Decompose(v)
		if len(parts) == 3 {
			if had, err := strconv.Atoi(parts[0]); err == nil {
				n, since = had, parts[1]
			}
		}
	}
	n++
	s.raw[key] = Compose(strconv.Itoa(n), since, reason)
	return n, since, reason
}

// Streak reads a source's standing failure streak without adding to it.
func (s *State) Streak(source string) (n int, since, last string) {
	v, ok := s.raw["fail:"+source]
	if !ok {
		return 0, "", ""
	}
	parts := Decompose(v)
	if len(parts) != 3 {
		return 0, "", ""
	}
	n, _ = strconv.Atoi(parts[0])
	return n, parts[1], parts[2]
}

// ClearFail is what a successful poll does to the streak.
func (s *State) ClearFail(source string) { delete(s.raw, "fail:"+source) }

// Stamp is the one time format this tool writes: RFC 3339 in UTC, from the
// tool's own clock, never from a time that arrived inside text (rule 9).
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Compose joins fields into one line that round-trips. Each field is escaped --
// "%" first as %25, then "|" as %7C, then the two characters that would end a
// line in the file -- and the fields are joined with "|". So a value holding a
// literal %7C comes back as those three characters and never as a pipe.
//
// The separator is deliberately not a character the reader can eat: the failure
// this codec exists to close is a tab-joined value whose trailing empty field
// vanished on reload. Compose("a", "") and Compose("a") are different strings,
// and Decompose tells them apart.
func Compose(parts ...string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = escapeField(p)
	}
	return strings.Join(out, "|")
}

// Decompose is Compose's inverse. strings.Split keeps every field including the
// empty ones, which is the half the tab-joined prototype lost.
func Decompose(s string) []string {
	parts := strings.Split(s, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = unescapeField(p)
	}
	return out
}

// The escape, in order: % first, so that unescaping in the reverse order cannot
// turn a stored %7C into a pipe. \n and \r are escaped as well because the file
// is one entry per line and a report path may hold either.
func escapeField(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "|", "%7C")
	s = strings.ReplaceAll(s, "\n", "%0A")
	s = strings.ReplaceAll(s, "\r", "%0D")
	return s
}

func unescapeField(s string) string {
	s = strings.ReplaceAll(s, "%0D", "\r")
	s = strings.ReplaceAll(s, "%0A", "\n")
	s = strings.ReplaceAll(s, "%7C", "|")
	return strings.ReplaceAll(s, "%25", "%")
}

func itoa(i int) string { return strconv.Itoa(i) }
