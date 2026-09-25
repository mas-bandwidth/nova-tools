// Package presence is the friend heartbeat of nova-tools #2610: one hash per
// friend, written by that friend's window with a TTL, and read by everyone who
// is about to hand that friend work.
//
// The whole mechanism is one MULTI per beat and one pipeline per read, and no
// model anywhere. A friend's harness startup runs `nova-wake beat --as <name>`,
// which writes the hash `friend:<name>` (field `at` = <RFC3339 utc>, plus
// `width` and `window` when the caller passed them) and gives it a 90s TTL
// every 30s; a window that exits, runs out of credit or is killed simply stops
// writing, and the hash lapses. `nova-wake presence` reads the hashes of every
// member of the `friends` SET and prints one line. Nothing here spends a token,
// because the cost of presence has to be zero or the heartbeat is the first
// thing dropped under load (Glenn, 2026-09-22: "As long as this can be done
// with zero tokens, that's fine. It's a heartbeat for a timeout.").
//
// friend:<name> is one hash with more than one writer: the sprint table's
// friend-row loop writes its counts (at, up, ready, working, done, ...) into
// the same hash with no TTL. So presence is not the key's existence but its
// TTL: a friend is up only while friend:<name> carries a live expiry, which
// only a beat gives it (#2673, #3144). A row with no TTL is a table row, not a
// friend who is here, and a beat that stops lets the whole hash lapse.
//
// The second key, `friend:<name>:last`, carries no TTL and exists for one
// reason: an absent beat says a friend is down but not since when, and a
// "down" with no duration is the report that cost an hour on 2026-09-22. The
// TTL'd hash is the presence; the untimed string is the memory of it.
//
// The git message bus carries notes and never beats (#3144: 79% of bus commits
// on 2026-09-23 were `beat <friend>`). Liveness is this package's hash only.
package presence

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// FriendsSet is the registry SET of friend names nova-sprint keeps (every
// capacity write adds its friend). presence reads it with SMEMBERS; nothing
// here scans the key space, and the beat does not register a friend.
const FriendsSet = "friends"

// The fields of the friend:<name> hash this package writes and reads. at is
// the beat's stamp; width (children in use now) and window (the cap's reset
// time) are written only when the caller passed them.
const (
	FieldAt     = "at"
	FieldWidth  = "width"
	FieldWindow = "window"
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

// Key is the TTL'd presence hash for a friend, and LastKey its untimed
// companion. Both normalize the name, so `Emma` and `emma` are one friend.
func Key(name string) string     { return Prefix + Normalize(name) }
func LastKey(name string) string { return Prefix + Normalize(name) + LastSuffix }

// Side is the pair a caller may pass beside the beat. The zero value writes
// nothing and is not an error: a missing flag leaves its field unwritten.
type Side struct {
	// Window is the cap's reset time, as the caller spelled it. Empty means
	// the flag was not passed: the window field is not written. This is
	// never computed from a clock in this package.
	Window string
	// Width is how many children are in use. Nil means the flag was not
	// passed, which is not the same as zero children.
	Width *int64
}

// Normalize is the one spelling of a friend's name in a key: lower case,
// trimmed. The bus roster spells names with a capital and the keys do not.
func Normalize(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Store is the small half of Redis this package uses, one round trip per
// method. It is an interface so the tests run against a fake with a clock they
// control, and so nothing here can reach for a command the bench ACL does not
// grant.
type Store interface {
	// WriteBeat is one heartbeat in one MULTI: HSET key fields... (field,
	// value pairs), PEXPIRE key ttl, SET lastKey stamp with no expiry.
	WriteBeat(ctx context.Context, key string, fields []string, ttl time.Duration, lastKey, stamp string) error
	// ReadBeats is one pipeline over every key: HMGET key at width window,
	// PTTL key, GET key+":last". An absent key or field answers "".
	ReadBeats(ctx context.Context, keys []string) ([]Reading, error)
	// Members is SMEMBERS of one set: the friends registry.
	Members(ctx context.Context, set string) ([]string, error)
}

// Reading is what the store holds for one friend: the three fields of the
// hash, whether the hash carries a live TTL, and the untimed memory.
type Reading struct {
	At, Width, Window string
	// Live is PTTL > 0: the hash exists and a beat's expiry is running on
	// it. A hash with no TTL (a table row) or no hash at all is not live.
	Live bool
	Last string
}

// Beat writes one heartbeat: the TTL'd hash that IS the presence and the
// untimed key that remembers it, in one MULTI. It passes no Side, so it writes
// neither width nor window.
func Beat(ctx context.Context, st Store, name string, now time.Time, ttl time.Duration) error {
	return BeatSide(ctx, st, name, now, ttl, Side{})
}

// BeatSide is Beat plus the optional fields. A zero Side field is not a value:
// that field is not written, and the beat still succeeds. Window is stored as
// given. Width is stored as a decimal. The whole hash carries the beat's TTL,
// so a window that stops writing takes them all with it.
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
	fields := []string{FieldAt, stamp}
	if side.Width != nil {
		fields = append(fields, FieldWidth, strconv.FormatInt(*side.Width, 10))
	}
	if side.Window != "" {
		fields = append(fields, FieldWindow, side.Window)
	}
	return st.WriteBeat(ctx, Key(name), fields, ttl, LastKey(name), stamp)
}

// State is what one friend's two keys say.
type State int

const (
	// Away: the presence hash has lapsed (or carries no TTL). Last says
	// when the last beat was, when the untimed key survives to say it.
	// The line prints it as down.
	Away State = iota
	// Up: the presence hash carries a live TTL, so a beat landed inside it.
	Up
	// Never: no beat and no memory of one. Freddy, all of 2026-09-22. The
	// line prints it as down.
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
	// Window and Width are the optional fields, read only while the friend
	// is up. Empty means that field is absent. Width "0" is a friend who has
	// no children in use, not a missing flag.
	Window string
	Width  string
}

// Present reports whether this friend is here. It is true only while the
// hash friend:<name> carried a live beat TTL at the read. A missing hash is
// absent, so is one that has lapsed, and so is a table row with no TTL. The
// untimed friend:<name>:last key dates a down; it is not presence. Nothing in
// this package reads a hand-written override in place of the beat. The TTL is
// the only evidence (#2675).
func (s Status) Present() bool { return s.State == Up }

// Read returns one Status per name, in the order given, from one pipeline
// over the presence hash, its TTL and its untimed memory for every friend. One
// round trip, whatever the roster's length: the line is refreshed every second
// on a table and must cost the store nothing.
func Read(ctx context.Context, st Store, names []string, now time.Time) ([]Status, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("presence needs at least one friend name")
	}
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, Key(n))
	}
	rs, err := st.ReadBeats(ctx, keys)
	if err != nil {
		return nil, err
	}
	if len(rs) != len(keys) {
		return nil, fmt.Errorf("store answered %d readings for %d friends", len(rs), len(keys))
	}
	out := make([]Status, 0, len(names))
	for i, n := range names {
		r := rs[i]
		s := Status{Name: Normalize(n)}
		switch {
		case r.Live:
			s.State = Up
			s.Window, s.Width = r.Window, r.Width
			if t, err := time.Parse(Stamp, r.At); err == nil {
				s.Last, s.Dated = t, true
				s.Age = now.Sub(t)
			}
		case r.Last != "":
			s.State = Away
			if t, err := time.Parse(Stamp, r.Last); err == nil {
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

// Friends is the roster from the registry: SMEMBERS friends, normalized,
// sorted, minus Glenn and Rowan. One round trip and no scan of friend:*.
func Friends(ctx context.Context, st Store) ([]string, error) {
	members, err := st.Members(ctx, FriendsSet)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range members {
		n := Normalize(m)
		if n == "" || notFriends[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the %s set names no friends besides Glenn and Rowan", FriendsSet)
	}
	sort.Strings(out)
	return out, nil
}

// Line is the one line #2610 asks for, in #2673's two words:
//
//	friends: johnny up 12s width=8 · stella up 4s · emma down 1h12m (last 09:41Z) · freddy down
//
// A friend is up or down and nothing else (Glenn 2026-09-22: "friends only
// up|down"). A down friend whose last beat is remembered carries its age and
// clock time; one who never beat carries nothing.
func Line(sts []Status, now time.Time) string {
	parts := make([]string, 0, len(sts))
	for _, s := range sts {
		parts = append(parts, s.Phrase(now))
	}
	return "friends: " + strings.Join(parts, " · ")
}

// Phrase is one friend's word on the line. window= and width= are appended
// only when those fields are present on a live beat. Their values are
// whatever the store holds, so they go through oneline.Field: a stored string
// is not a stamp this package formatted.
func (s Status) Phrase(now time.Time) string {
	var base string
	switch s.State {
	case Up:
		if !s.Dated {
			// The beat is live, so the friend is here; the at field was
			// not a stamp this build knows how to read.
			base = s.Name + " up"
		} else {
			base = s.Name + " up " + Short(s.Age)
		}
	case Away:
		if !s.Dated {
			base = s.Name + " down"
		} else {
			base = fmt.Sprintf("%s down %s (last %s)", s.Name, Short(s.Age), Clock(s.Last, now))
		}
	default:
		base = s.Name + " down"
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
