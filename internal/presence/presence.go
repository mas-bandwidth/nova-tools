// Package presence is the friend heartbeat of nova-tools #2610: one key per
// friend, written by that friend's window with a TTL and by nothing else, and
// read by everyone who is about to hand that friend work.
//
// The whole mechanism is two Redis commands and no model anywhere. A friend's
// harness startup runs `nova-wake beat --as <name>`, which writes
// `friend:<name> = <RFC3339 utc>` with a 90s TTL every 30s; a window that
// exits, runs out of credit or is killed simply stops writing, and the key
// lapses. `nova-wake presence` reads those keys and prints one line. Nothing
// here spends a token, because the cost of presence has to be zero or the
// heartbeat is the first thing dropped under load (Glenn, 2026-09-22: "As long
// as this can be done with zero tokens, that's fine. It's a heartbeat for a
// timeout.").
//
// The second key, `friend:<name>:last`, carries no TTL and exists for one
// reason: an absent key says a friend is away but not since when, and "AWAY"
// with no duration is the report that cost an hour on 2026-09-22. The TTL'd key
// is the presence; the untimed one is the memory of it.
//
// Two further keys sit beside the beat, and only when the caller passed them.
// `friend:<name>:window` is the cap's reset time, copied from the caller's
// flag: this package does not read a clock to invent one. `friend:<name>:width`
// is how many children are in use now. A flag that was not passed does not
// write its key and does not fail the beat. Both carry the beat's TTL, so a
// window that stops writing takes them with it. Zero children is a real width.
package presence

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Prefix is the one namespace these keys live under; the bench ACL user gains
// `~friend:*` and nothing wider.
const Prefix = "friend:"

// LastSuffix is the untimed companion key's suffix: `friend:<name>:last`.
const LastSuffix = ":last"

// WindowSuffix and WidthSuffix are the optional keys beside the beat:
// `friend:<name>:window` and `friend:<name>:width`.
const (
	WindowSuffix = ":window"
	WidthSuffix  = ":width"
)

// DefaultEvery and DefaultTTL are the cadence and the timeout of #2610: beat
// every 30s, lapse after 90s. The TTL is three beats rather than two so one
// missed write -- a network hiccup, a laptop asleep for a moment -- is not
// reported as a friend who left.
const (
	DefaultEvery = 30 * time.Second
	DefaultTTL   = 90 * time.Second
)

// Stamp is the layout every heartbeat value is written in: RFC3339 in UTC, to
// the second. It is parsed back to say how long ago the beat was, and a value
// that will not parse is still a beat -- the key's existence is the presence.
const Stamp = time.RFC3339

// Key is the TTL'd presence key for a friend, and LastKey its untimed
// companion. Both normalize the name, so `Emma` and `emma` are one friend.
func Key(name string) string       { return Prefix + Normalize(name) }
func LastKey(name string) string   { return Prefix + Normalize(name) + LastSuffix }
func WindowKey(name string) string { return Prefix + Normalize(name) + WindowSuffix }
func WidthKey(name string) string  { return Prefix + Normalize(name) + WidthSuffix }

// Side is the pair a caller may pass beside the beat. The zero value writes
// nothing and is not an error: a missing flag leaves its key absent.
type Side struct {
	// Window is the cap's reset time, as the caller spelled it. Empty means
	// the flag was not passed: friend:<name>:window is not written. This is
	// never computed from a clock in this package.
	Window string
	// Width is how many children are in use. Nil means the flag was not
	// passed, which is not the same as zero children.
	Width *int64
}

// Normalize is the one spelling of a friend's name in a key: lower case,
// trimmed. The bus roster spells names with a capital and the keys do not.
func Normalize(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Store is the small half of Redis this package uses: SET with an optional TTL
// and a multi-key GET. It is an interface so the tests run against a fake with
// a clock they control, and so nothing here can reach for a command the bench
// ACL does not grant.
type Store interface {
	// Set writes value at key. A ttl of 0 means no expiry.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// MGet returns one string per key, in order, with "" for a key that is
	// absent or expired. A heartbeat value is never empty, so "" is absence.
	MGet(ctx context.Context, keys ...string) ([]string, error)
}

// Beat writes one heartbeat: the TTL'd key that IS the presence, then the
// untimed key that remembers it. The TTL'd write goes first so a store that
// fails between the two leaves a stale `:last` rather than a presence nobody
// can date. It passes no Side, so it writes neither :window nor :width.
func Beat(ctx context.Context, st Store, name string, now time.Time, ttl time.Duration) error {
	return BeatSide(ctx, st, name, now, ttl, Side{})
}

// BeatSide is Beat plus the optional keys. A zero Side field is not a value:
// that key is not written, and the beat still succeeds. Window is stored as
// given. Width is stored as a decimal. Both use the beat's TTL.
func BeatSide(ctx context.Context, st Store, name string, now time.Time, ttl time.Duration, side Side) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("beat needs a name")
	}
	if ttl <= 0 {
		return fmt.Errorf("beat needs a positive ttl")
	}
	if side.Width != nil && *side.Width < 0 {
		return fmt.Errorf("width cannot be negative")
	}
	stamp := now.UTC().Format(Stamp)
	if err := st.Set(ctx, Key(name), stamp, ttl); err != nil {
		return err
	}
	if err := st.Set(ctx, LastKey(name), stamp, 0); err != nil {
		return err
	}
	if side.Window != "" {
		if err := st.Set(ctx, WindowKey(name), side.Window, ttl); err != nil {
			return err
		}
	}
	if side.Width != nil {
		if err := st.Set(ctx, WidthKey(name), strconv.FormatInt(*side.Width, 10), ttl); err != nil {
			return err
		}
	}
	return nil
}

// State is what one friend's two keys say.
type State int

const (
	// Away: the presence key has lapsed. Last says when the last beat was,
	// when the untimed key survives to say it.
	Away State = iota
	// Up: the presence key is there, so a beat landed inside the TTL.
	Up
	// Never: neither key exists. Freddy, all of 2026-09-22.
	Never
)

// Status is one friend's reading: the state, the age of the beat it was read
// from, and the moment of the last beat when one is known.
type Status struct {
	Name  string
	State State
	// Age is how long ago the beat behind this reading was written: the
	// presence beat when Up, the last beat when Away. Valid only when
	// Dated is true.
	Age   time.Duration
	Last  time.Time
	Dated bool
	// Window and Width are the optional keys. Empty means that key is absent.
	// Width "0" is a friend who had no children in use, not a missing flag.
	Window string
	Width  string
}

// Present reports whether this friend is here. It is true only while the
// TTL'd beat key, friend:<name>, was alive at the read. A missing key is
// absent, and so is a key that has lapsed. The untimed friend:<name>:last
// key dates an AWAY; it is not presence. none is neither key. Nothing in
// this package reads a hand-written override in place of the beat key.
// The key is the only evidence (#2675).
func (s Status) Present() bool { return s.State == Up }

// Read returns one Status per name, in the order given, from one MGet over
// the presence key, its untimed memory, and the two optional keys of every
// friend. One round trip, whatever the roster's length: the line is refreshed
// every 30 seconds on a table and must cost the store nothing.
func Read(ctx context.Context, st Store, names []string, now time.Time) ([]Status, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("presence needs at least one friend name")
	}
	keys := make([]string, 0, 4*len(names))
	for _, n := range names {
		keys = append(keys, Key(n), LastKey(n), WindowKey(n), WidthKey(n))
	}
	vals, err := st.MGet(ctx, keys...)
	if err != nil {
		return nil, err
	}
	if len(vals) != len(keys) {
		return nil, fmt.Errorf("store answered %d values for %d keys", len(vals), len(keys))
	}
	out := make([]Status, 0, len(names))
	for i, n := range names {
		beat, last := vals[4*i], vals[4*i+1]
		s := Status{Name: Normalize(n), Window: vals[4*i+2], Width: vals[4*i+3]}
		switch {
		case beat != "":
			s.State = Up
			if t, err := time.Parse(Stamp, beat); err == nil {
				s.Last, s.Dated = t, true
				s.Age = now.Sub(t)
			}
		case last != "":
			s.State = Away
			if t, err := time.Parse(Stamp, last); err == nil {
				s.Last, s.Dated = t, true
				s.Age = now.Sub(t)
			}
		default:
			s.State = Never
		}
		out = append(out, s)
	}
	return out, nil
}

// Line is the one line #2610 asks for:
//
//	friends: johnny up 12s · stella up 4s · emma AWAY 1h12m (last 09:41Z) · freddy none
//
// AWAY is the only word in capitals because it is the only one that changes
// what the reader does next.
func Line(sts []Status, now time.Time) string {
	parts := make([]string, 0, len(sts))
	for _, s := range sts {
		parts = append(parts, s.Phrase(now))
	}
	return "friends: " + strings.Join(parts, " · ")
}

// Phrase is one friend's word on the line. window= and width= are appended
// only when those keys are present. Their values are whatever the store
// holds, so they go through oneline.Field: a stored string is not a stamp
// this package formatted.
func (s Status) Phrase(now time.Time) string {
	var base string
	switch s.State {
	case Up:
		if !s.Dated {
			// The key is there, so the friend is here; the value was
			// not a stamp this build knows how to read.
			base = s.Name + " up"
		} else {
			base = s.Name + " up " + Short(s.Age)
		}
	case Away:
		if !s.Dated {
			base = s.Name + " AWAY"
		} else {
			base = fmt.Sprintf("%s AWAY %s (last %s)", s.Name, Short(s.Age), Clock(s.Last, now))
		}
	default:
		base = s.Name + " none"
	}
	if s.Window != "" {
		base += " window=" + oneline.Field(s.Window)
	}
	if s.Width != "" {
		base += " width=" + oneline.Field(s.Width)
	}
	return base
}

// Short is a duration in the fewest characters that still say it: seconds
// under a minute, minutes under an hour, then h+m, then d+h. A table line is
// read at a glance or it is not read.
func Short(d time.Duration) string {
	if d < 0 {
		// A friend's clock is ahead of the reader's. Zero, not a
		// negative age: the reading is "just now" either way.
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		h := d / time.Hour
		return fmt.Sprintf("%dh%02dm", h, (d-h*time.Hour)/time.Minute)
	default:
		days := d / (24 * time.Hour)
		return fmt.Sprintf("%dd%02dh", days, (d-days*24*time.Hour)/time.Hour)
	}
}

// Clock is the wall time of a beat, in UTC because every other time on the
// table is: `09:41Z` inside the day, the date as well beyond it, so "last
// 09:41Z" can never mean yesterday without saying so.
func Clock(t, now time.Time) string {
	t = t.UTC()
	if now.Sub(t) < 24*time.Hour {
		return t.Format("15:04") + "Z"
	}
	return t.Format("2006-01-02T15:04") + "Z"
}

// notFriends are the two names on the bus roster that never beat: Glenn is a
// person and Rowan is the coordinator doing the reading. A line that reported
// on them would be asking whether the reader is here.
var notFriends = map[string]bool{"glenn": true, "rowan": true}

// ParticipantNames reads the bus participants file and returns the friends in
// its order, minus Glenn and Rowan. The roster is the bus's, so a friend who
// joins is on the line the moment the bus knows them and no list in this repo
// has to be edited to match.
func ParticipantNames(data []byte) ([]string, error) {
	var doc struct {
		Participants []struct {
			Name string `json:"name"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("participants file: %w", err)
	}
	var out []string
	for _, p := range doc.Participants {
		n := Normalize(p.Name)
		if n == "" || notFriends[n] {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("participants file names no friends besides Glenn and Rowan")
	}
	return out, nil
}
