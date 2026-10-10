package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends (sprint.Friends; docs/SPEC-SPRINT.md section 1, the friends
// table): two kinds of record under the deployment's prefix, outside the
// tables, the fence and the epochs, as a reader's beat and hold are. friends
// is the roster, every friend of nova-config's friend rows (friend sync) with
// the coordinator's hold of each (friend down; friend up releases it) and her
// width; friend-beat:<f> is the friend's last beat (friend beat), written by
// her daemon, shown and never evidence; friend-finish:<f> is when a card of
// hers last finished (FriendFinished). A friend's status is derived when it is
// shown, never stored, by the friends' rule (sprint.FriendStatus): held, else
// up only on her session's evidence within its window (a wake ping her session
// answered, friend health; a card of hers finished), else down. Her
// counts are her sprint cards' (the cards dealt to her fleet row friend.<name>,
// read from the fleet table by where, never stored as a record here):
// (the owner, 2026-10-02: "give friends in the friends table the same ready,
// working, width, done, ok%, status that we have for machines, but no load").

const keyFriends = "friends"

// currentBuild is the build this process runs (buildinfo.Version): the build a friend's
// daemon is compared with when she comes up (sprint.FriendSeat.Current).
var currentBuild = buildinfo.Version("")

func friendBeatKey(friend string) string { return "friend-beat:" + friend }

// friendHealthKey is the coordinator's last accepted observation of the friend
// (sprint.FriendHealth; friend health), written by the health step's commit.
func friendHealthKey(friend string) string { return "friend-health:" + friend }

// friendEntry is a friend's entry in the roster: the coordinator's hold,
// empty while released, and her width, how many jobs she works at once.
type friendEntry struct {
	Held  bool      `json:"held,omitempty"`
	At    time.Time `json:"at,omitempty"`
	By    string    `json:"by,omitempty"`
	Width int       `json:"width,omitempty"`
	// Class is her class: the tiers her nova-config row says she can do, sorted and
	// comma joined (friend level evens the friends of one class).
	Class string `json:"class,omitempty"`
	// Mode is her delivery mode, her nova-config row's (batch or one-shot),
	// which her daemon reads back from her beat; empty is batch.
	Mode string `json:"mode,omitempty"`
	// Streams and Kinds are her row's optional work restriction (nova-config's
	// streams and kinds, carried by friend sync): the comma lists the dealer
	// reads, empty for no restriction.
	Streams string `json:"streams,omitempty"`
	Kinds   string `json:"kinds,omitempty"`
	// ConfigDir is her row's config_dir, the directory a claude one-shot lane
	// runs with as CLAUDE_CONFIG_DIR, which her beat answers (row_config_dir=).
	ConfigDir string `json:"config_dir,omitempty"`
	// TokenCap is her row's per-card token cap. TokenCapSet is whether friend
	// sync wrote one: a roster from before the field is unset, and her beat
	// answers the default. An explicit 0 is no cap; omitempty would drop that
	// 0, so the bool is the record that it was set.
	TokenCap    int64 `json:"token_cap,omitempty"`
	TokenCapSet bool  `json:"token_cap_set,omitempty"`
	// Roles is her row's roles, comma joined (builder, may-hold, reader): a read card is
	// dealt only to a friend whose roles name reader (sprint read_cards.go).
	Roles string `json:"roles,omitempty"`
	// Dir is her working directory, her nova-config row's dir; empty when the
	// row declares none, and the tools join <root>/<name>-working (frienddir.go).
	Dir string `json:"dir,omitempty"`
	// Billing is her row's billing word, when it names one: a friend whose calls are
	// billed per use shows dollars on the tokens column, a subscription friend tokens.
	// The cost card adds the field; until it exists this is empty, which is a
	// subscription.
	Billing string `json:"billing,omitempty"`
	// Reason and Until are the hold's (friend down --reason --until, hold <friend>
	// --reason): why, and when the coordinator expects her back. Return is whether
	// the hold took her cards back (hold.go).
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitzero"`
	Return bool      `json:"return,omitempty"`
}

// FriendSpec is what friend sync knows of one friend: her name (a friend row
// of nova-config), her width, her class, her delivery mode and her working directory.
type FriendSpec struct {
	Name  string
	Width int
	Class string
	Mode  string // her delivery mode, config.FriendMode of her row
	// ConfigDir is her row's config_dir ("" when it names none).
	ConfigDir string
	// TokenCap is her row's per-card token cap. TokenCapSet is false for a
	// spec built before the field: her beat then answers the default, and an
	// explicit 0 (no cap) is TokenCapSet with TokenCap 0.
	TokenCap    int64
	TokenCapSet bool
	// Roles is her row's roles, comma joined: reader is the read cards'.
	Roles string
	// Dir is her working directory, config.FriendDir of her row; "" when it has none.
	Dir string
	// Billing is her row's billing word ("" when the row names none): a friend billed
	// per call shows dollars on the tokens column. The cost card adds the field.
	Billing string
	// Streams and Kinds are her row's optional work restriction, the comma
	// lists the dealer reads (sprint.Split; empty for no restriction).
	Streams string
	Kinds   string
}

// RestrictionWhy explains why the configured stream and kind do not fit this friend: ""
// when they do (sprint.FriendRestrictionWhy).
func (s FriendSpec) RestrictionWhy(stream, kind string) string {
	return sprint.FriendRestrictionWhy(sprint.Split(s.Streams), sprint.Split(s.Kinds), stream, kind)
}

// FriendRow is one row of the friends table as where draws it: the counts of
// her sprint cards (filled by where from her fleet row), her width and her
// status (filled by FriendRows from the roster and her beat).
type FriendRow struct {
	Name    string `json:"name"`
	Ready   int    `json:"ready"`
	Working int    `json:"working"`
	// DealtFleet is the fleet's cards among them: work cards whose primary carries no
	// WHO line (sprint.FriendsDealtFleet, from the tick's where record, up to a tick behind).
	DealtFleet int    `json:"dealt_fleet"`
	Width      int    `json:"width"`
	OK         int    `json:"ok"`
	Failed     int    `json:"failed"`
	Status     string `json:"status"`
	Class      string `json:"class,omitempty"`
	Mode       string `json:"mode,omitempty"`
	// Roles is her row's roles, comma joined (reader: she is dealt read cards).
	Roles string `json:"roles,omitempty"`
	// Streams and Kinds are her work restriction as the deal reads it, never a
	// column where draws: hidden from the row's JSON as the lock keeps the
	// friends table's shape.
	Streams string `json:"-"`
	Kinds   string `json:"-"`
	// Load and Report are what her last beat reported (friend beat --load, and
	// sprint.FriendReport), absent when it reported none; DaemonVersion is the build
	// stamp the daemon that beat for her runs (sprint.FriendReport.DaemonVersion,
	// friend beat --daemon-version), empty when its beat carried none.
	Load          float64              `json:"load,omitempty"`
	DaemonVersion string               `json:"daemon_version,omitempty"`
	Report        *sprint.FriendReport `json:"report,omitempty"`
	// Active is the newest write her daemon found under her working directory and
	// outbox (sprint.FriendReport.Active), zero when it reported none: the last
	// session activity column.
	Active time.Time `json:"active,omitzero"`
	// Tokens is the sum of her cards' usage records' input, output, cache read and
	// cache write, and Charged the dollars those records charged (actual where
	// reported, else predicted), both filled by FriendRows from her fleet row; a
	// subscription friend's cell shows Tokens compact, an api-billed friend's shows
	// Charged to the cent, rounded up (the friends table's tokens column).
	Tokens  int64  `json:"tokens"`
	Charged string `json:"charged,omitempty"`
	// Billing is her row's billing word, "" when it names none (a subscription).
	Billing string `json:"billing,omitempty"`
	// Beat is when her last beat came, zero when she has never beaten: how stale her
	// report is (view coordinator).
	Beat time.Time `json:"beat,omitzero"`
	// Health is the coordinator's last accepted observation of her (friend
	// health), absent until the first.
	Health *sprint.FriendHealth `json:"health,omitempty"`
	// Evidence is what her status rests on (sprint.FriendEvidence): for up, the
	// session's evidence and its age; for down, what is missing, and her beat's
	// age, which is never evidence. Finished is when a card of hers last
	// finished, zero for never.
	Evidence string    `json:"evidence,omitempty"`
	Finished time.Time `json:"finished,omitzero"`
	// Proof is her session's last proof as her beat carries it (sprint.Beat.Proof: a
	// SESSION CHECK it answered, or a bus message of its own), zero when none.
	Proof time.Time `json:"proof,omitzero"`
	// Reason and Until say why she is held or down and when she is expected
	// back, when the hold or the observation said (friend down, friend health).
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitzero"`
}

// roster is the friends record, by name; empty when there is none.
func (st *Store) roster(ctx context.Context) (map[string]friendEntry, KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil, err
	}
	out := map[string]friendEntry{}
	raw, ok, err := kv.GetKey(ctx, keyFriends)
	if err != nil || !ok {
		return out, kv, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, kv, fmt.Errorf("the friends record cannot be read (%v); run: nova-sprint friend sync", err)
	}
	return out, kv, nil
}

func putRoster(ctx context.Context, kv KV, r map[string]friendEntry) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyFriends, string(b))
}

// noFriend is the refusal of a name the roster lacks.
func noFriend(r map[string]friendEntry, friend string) error {
	names := slices.Sorted(maps.Keys(r))
	if len(names) == 0 {
		names = []string{"none"}
	}
	return fmt.Errorf("no friend %s on the friends table (friends: %s): its row is nova-config's friend row; run: nova-sprint friend sync", friend, strings.Join(names, ","))
}

// SyncFriends makes the roster the friends given (nova-config's friend rows,
// each with her width): a friend it lacks is added, released, at her width; a
// friend it has that the specs lack is taken off with her beat; a friend that
// stays keeps her hold, and her width is set from the spec. It writes nothing
// when there is nothing to change, and says who was added, who taken off and
// who stayed with a width, class, mode or dir that changed, each in name order.
func (st *Store) SyncFriends(ctx context.Context, specs []FriendSpec) (added, removed, updated []string, err error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	want := map[string]FriendSpec{}
	rosterChanged := false
	for _, s := range specs {
		want[s.Name] = s
		e, had := r[s.Name]
		switch {
		case !had:
			added = append(added, s.Name)
			rosterChanged = true
		case e.Width != s.Width || e.Class != s.Class || e.Mode != s.Mode || e.ConfigDir != s.ConfigDir || e.TokenCap != s.TokenCap || e.TokenCapSet != s.TokenCapSet || e.Roles != s.Roles || e.Billing != s.Billing || e.Streams != s.Streams || e.Kinds != s.Kinds || e.Dir != s.Dir:
			updated = append(updated, s.Name)
			rosterChanged = true
		}
		e.Width, e.Class, e.Mode, e.ConfigDir, e.TokenCap, e.TokenCapSet, e.Roles, e.Billing, e.Streams, e.Kinds, e.Dir = s.Width, s.Class, s.Mode, s.ConfigDir, s.TokenCap, s.TokenCapSet, s.Roles, s.Billing, s.Streams, s.Kinds, s.Dir
		r[s.Name] = e
	}
	for n := range r {
		if _, ok := want[n]; !ok {
			removed = append(removed, n)
			delete(r, n)
			rosterChanged = true
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(updated)
	if !rosterChanged {
		return nil, nil, nil, nil
	}
	if err := putRoster(ctx, kv, r); err != nil {
		return nil, nil, nil, err
	}
	if len(removed) > 0 {
		keys := make([]string, 0, len(removed))
		for _, n := range removed {
			keys = append(keys, st.Names.Key(friendBeatKey(n)), st.Names.Key(friendHealthKey(n)), st.Names.Key(friendFinishKey(n)))
		}
		if _, err := st.B.DeleteKeys(ctx, keys); err != nil {
			return added, removed, updated, err
		}
	}
	return added, removed, updated, nil
}

// FriendBeat writes one beat of the friend at the store's clock, to the
// second, naming no field of her report. A running list, a working count or a
// queue count already stored stays; a friend the roster lacks is refused and
// nothing is written.
func (st *Store) FriendBeat(ctx context.Context, friend string) (sprint.Beat, error) {
	return st.FriendBeatReport(ctx, friend, sprint.FriendReport{}, nil)
}

// FriendBeatReport is FriendBeat with what her machinery reports of her work
// (sprint.FriendReport: the cards she is running, which friend take and friend
// down keep with her, and her own counts) and her load (nil: none). A running
// list, a working count or a queue count this beat does not name stays as the
// store has it; naming the list, including an empty one, replaces it.
func (st *Store) FriendBeatReport(ctx context.Context, friend string, rep sprint.FriendReport, load *float64) (sprint.Beat, error) {
	b, _, err := st.FriendBeatProof(ctx, friend, rep, load, sprint.BeatWords{})
	return b, err
}

// friendBeatRecord is a friend's beat as kept: the beat; her session's last proof, the
// server's time of the last answer that named a check her daemon asked (sprint.ProveBeat;
// zero: none), which the friends' rule and the coordinator's pass read; the checks still
// answerable; and the last beat with no proof, and why. A reader of the beat alone reads
// the record as a sprint.Beat, its Proof the record's pong.
type friendBeatRecord struct {
	sprint.Beat
	Pong    time.Time           `json:"pong,omitzero"`
	Asked   []sprint.AskedCheck `json:"asked,omitempty"`
	NoProof string              `json:"no_proof,omitempty"`
}

// BeatProof is what one beat's proof words came to: proved (her session answered a check
// her daemon asked) or why it proved nothing, and the proof the record keeps after it.
// Set is which of running, working and queue this beat named, comma joined in that
// order, or "-" when it named none (FRIEND-BEAT OK <friend> ... set=<Set>). A field absent
// from Set was left as the store had it.
type BeatProof struct {
	Proved  bool
	NoProof string
	Proof   time.Time
	Set     string
}

// friendBeatReport lays this beat's report over the last one. A nil running list
// and a nil working or queue count are absent: they stay, and a count left off is
// never written as zero. A running list the beat names, including an empty one,
// replaces the list. The other fields are this beat's own, as before (a beat that
// does not say down withdraws the down word).
func friendBeatReport(prev *sprint.FriendReport, rep sprint.FriendReport) (sprint.FriendReport, string) {
	out := rep
	var named []string
	if rep.Running != nil {
		named = append(named, "running")
	} else if prev != nil {
		out.Running = append([]string(nil), prev.Running...)
	}
	if rep.Working != nil {
		named = append(named, "working")
	} else if prev != nil && prev.Working != nil {
		w := *prev.Working
		out.Working = &w
	}
	if rep.Queue != nil {
		named = append(named, "queue")
	} else if prev != nil && prev.Queue != nil {
		q := *prev.Queue
		out.Queue = &q
	}
	if len(named) == 0 {
		return out, "-"
	}
	return out, strings.Join(named, ",")
}

// FriendBeatProof is FriendBeatReport with the beat's proof words (sprint.BeatWords: the
// check her daemon asked, the check her session answered, the daemon's run): the record's
// checks and proof are carried from the last beat and stepped by sprint.ProveBeat at the
// store's clock, so only an answer to a check asked proves, once.
func (st *Store) FriendBeatProof(ctx context.Context, friend string, rep sprint.FriendReport, load *float64, w sprint.BeatWords) (sprint.Beat, BeatProof, error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return sprint.Beat{}, BeatProof{}, err
	}
	if _, ok := r[friend]; !ok {
		return sprint.Beat{}, BeatProof{}, noFriend(r, friend)
	}
	now := st.now().UTC().Truncate(time.Second)
	var prev friendBeatRecord
	if vals, oks, err := getKeys(ctx, kv, []string{friendBeatKey(friend)}); err != nil {
		return sprint.Beat{}, BeatProof{}, err
	} else if len(oks) == 1 && oks[0] {
		_ = json.Unmarshal([]byte(vals[0]), &prev) // ignored: an unreadable record holds no check and no proof
	}
	rep, set := friendBeatReport(prev.Beat.Friend, rep)
	b := sprint.Beat{At: now}
	if len(rep.Running) > 0 || rep.Working != nil || rep.Queue != nil || rep.Width != nil || !rep.Active.IsZero() || rep.Tests != nil {
		b.Friend = &rep // a beat that still reports nothing carries no report
	}
	if rep.Build != "" || !rep.Started.IsZero() || !rep.Present.IsZero() || rep.DaemonVersion != "" {
		b.Friend = &rep // her daemon's own facts are a report (sprint.StatusTransitions reads them)
	}
	if load != nil {
		b.Load, b.How = *load, sprint.HowGiven
	}
	rec := friendBeatRecord{Beat: b, Pong: prev.Pong, NoProof: prev.NoProof}
	asked, proved, why := sprint.ProveBeat(prev.Asked, w, now)
	rec.Asked = asked
	if proved {
		rec.Pong, rec.NoProof = now, ""
	}
	if why != "" {
		rec.NoProof = why
	}
	if legacy := w.Legacy.UTC().Truncate(time.Second); !legacy.IsZero() && !legacy.After(now) && legacy.After(rec.Pong) {
		// a daemon from before the nonces, within the server's grace (sprint.LegacyPongGrace)
		rec.Pong, rec.NoProof, proved = legacy, "", true
	}
	rec.Beat.Proof = rec.Pong
	out, err := json.Marshal(rec)
	if err != nil {
		return b, BeatProof{}, err
	}
	b.Proof = rec.Pong
	return b, BeatProof{Proved: proved, NoProof: why, Proof: rec.Pong, Set: set}, kv.SetKey(ctx, friendBeatKey(friend), string(out))
}

// SetFriendHeld holds the friend (friend down, with why and until when the
// coordinator expects her back, each "" or zero for none) or releases the hold
// (friend up), by the coordinator who, and sets her width when width is above
// zero (friend up --width; friend sync sets nova-config's again); a friend the
// roster lacks is refused.
func (st *Store) SetFriendHeld(ctx context.Context, friend string, held bool, who, reason string, until time.Time, width int) error {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return err
	}
	e, ok := r[friend]
	if !ok {
		return noFriend(r, friend)
	}
	e.Held, e.At, e.By, e.Reason, e.Until = false, time.Time{}, "", "", time.Time{}
	if held {
		e.Held, e.At, e.By, e.Reason, e.Until = true, st.now().UTC().Truncate(time.Second), who, reason, until.UTC().Truncate(time.Second)
	}
	if width > 0 {
		e.Width = width
	}
	r[friend] = e
	return putRoster(ctx, kv, r)
}

// FriendRows is the friends table at now: every friend of the roster with her
// width and her status (sprint.FriendStatus), in the fleet table's order
// (FleetOrder: up, asleep, then held, then down, each by name). The counts
// (ready, working, ok, failed) are her sprint cards', filled by where from her
// fleet row (friend.<name>); her tokens are those cards' usage records, summed
// here from the fleet table (sprint.FriendTokensFromCards). Three reads: the
// roster, the seat's generation, then every friend's beat, health and last finish
// in one exchange, and, when the roster has a friend, the fleet table's cards for
// the token sum. A store that keeps no records has no friends.
func (st *Store) FriendRows(ctx context.Context, now time.Time) ([]FriendRow, error) {
	rows, _, err := st.friendRows(ctx, now)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	fleet, err := st.friendFleet(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		row := sprint.FriendRow(rows[i].Name)
		cards := make([]*sprint.Card, 0, 4)
		for _, col := range []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed} {
			cards = append(cards, fleet.Cell(row, col)...)
		}
		rows[i].Tokens = sprint.FriendTokensFromCards(cards)
		rows[i].Charged = sprint.FriendChargedFromCards(cards)
	}
	return rows, nil
}

// friendFleet is the fleet table's cards, read once for the friends table's token
// sum: a friend's cards are on her row (sprint.FriendRow) in ready, working and
// the done cells. An empty table (a store whose fleet was never initialised) is
// no cards, not an error.
func (st *Store) friendFleet(ctx context.Context) (*sprint.Table, error) {
	loaded, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return nil, err
	}
	if loaded.Fleet == nil {
		return sprint.NewTable(sprint.Fleet), nil
	}
	return loaded.Fleet, nil
}

// friendRows is FriendRows and, by name, why each friend is not up
// (sprint.FriendDownWhy; absent while she is up): the words a take refused for her names
// (FriendSeats).
func (st *Store) friendRows(ctx context.Context, now time.Time) ([]FriendRow, map[string]string, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil {
		return nil, nil, nil // a store that keeps no records: no friend
	}
	if err != nil || len(r) == 0 {
		return nil, nil, err
	}
	generation, err := st.seatGeneration(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, 0, 3*len(names))
	for _, n := range names {
		keys = append(keys, friendBeatKey(n), friendHealthKey(n), friendFinishKey(n))
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, nil, err
	}
	rows := map[string]FriendRow{}
	status := map[string]string{}
	whys := map[string]string{}
	for i, n := range names {
		var b sprint.Beat
		var h sprint.FriendHealth
		var fin time.Time
		if 3*i+2 < len(oks) {
			if oks[3*i] {
				// ignored: an unreadable record is no beat, which the next beat replaces
				_ = json.Unmarshal([]byte(vals[3*i]), &b)
			}
			if oks[3*i+1] {
				// ignored: an unreadable record is no observation, which the next replaces
				_ = json.Unmarshal([]byte(vals[3*i+1]), &h)
			}
			if oks[3*i+2] {
				// ignored: an unreadable record is no finish, which the next replaces
				fin, _ = time.Parse(time.RFC3339, vals[3*i+2])
			}
		}
		presence := sprint.FriendPresence{Held: r[n].Held, Beat: b, Health: h, Generation: generation, Finished: fin}
		word, evidence := sprint.FriendEvidence(presence, now)
		row := FriendRow{Name: n, Width: r[n].Width, Status: word, Evidence: evidence, Finished: fin, Class: r[n].Class, Mode: r[n].Mode, Roles: r[n].Roles, Billing: r[n].Billing, Streams: r[n].Streams, Kinds: r[n].Kinds, Load: b.Load, Report: b.Friend, Beat: b.At, Proof: b.Proof}
		if why := sprint.FriendDownWhy(presence, now); why != "" {
			whys[n] = why
		}
		if b.Friend != nil {
			row.Active = b.Friend.Active
			row.DaemonVersion = b.Friend.DaemonVersion
		}
		if h.Observed() {
			row.Health = &h
		}
		switch row.Status {
		case sprint.Held:
			row.Reason, row.Until = r[n].Reason, r[n].Until
		case sprint.Down:
			if h.Observed() && h.Generation == generation {
				row.Reason, row.Until = h.Reason, h.Until
			}
		}
		rows[n] = row
		status[n] = row.Status
	}
	out := make([]FriendRow, 0, len(names))
	for _, n := range FleetOrder(names, status) {
		out = append(out, rows[n])
	}
	return out, whys, nil
}

// friendNames is every friend of the roster, for teardown; none when the
// store keeps no records or the record cannot be read.
func (st *Store) friendNames(ctx context.Context) []string {
	r, _, err := st.roster(ctx)
	if err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(r))
}

// FriendSeats returns every friend of the roster as a FriendSeat (with Name, Width, Status,
// Class, Mode, and Running: the cards her last beat names running).
func (st *Store) FriendSeats(ctx context.Context, now time.Time) ([]sprint.FriendSeat, error) {
	rows, whys, err := st.friendRows(ctx, now)
	if err != nil {
		return nil, err
	}
	seats := make([]sprint.FriendSeat, len(rows))
	for i, r := range rows {
		seats[i] = sprint.FriendSeat{Name: r.Name, Width: r.Width, Status: r.Status, Class: r.Class, Mode: r.Mode, Roles: sprint.Split(r.Roles), Streams: sprint.Split(r.Streams), Kinds: sprint.Split(r.Kinds), Why: whys[r.Name], Proof: r.Proof, Finished: r.Finished}
		if r.Reason != "" && seats[i].Why != "" {
			seats[i].Why += ": " + r.Reason
		}
		if r.Report != nil {
			seats[i].Running, seats[i].Active = r.Report.Running, r.Report.Active
		}
		if r.Health != nil && r.Health.State == sprint.Up {
			seats[i].Answered = r.Health.Seen
		}
		// what the status transitions read (sprint.StatusTransitions): her beat, her evidence,
		// whether she is down on her daemon's pong alone, and the build this server runs
		seats[i].Beat = sprint.Beat{At: r.Beat, Friend: r.Report, Proof: r.Proof, Load: r.Load}
		seats[i].Evidence, seats[i].Current = r.Evidence, currentBuild
		seats[i].DaemonOnly = r.Status == sprint.Down && r.Health != nil && r.Health.State == sprint.DaemonPong
	}
	return seats, nil
}

// friendSeats is every friend of the roster as the tick's deal and level see her
// (sprint.TickDeal, sprint.FriendLevel, and the attempt cap's deal, sprint.AttemptCapDeal): her name, width and status at now and what her
// beat names running, read every tick while the roster has a friend, whether or not a
// card is ready: the deal offers every ready card to the friends first, a queued change or
// a dependency resolution can make work ready later in the same tick, and a friend coming
// up is levelled on the same tick (docs/SPEC-SPRINT.md, WHO preference, and section 1,
// friend-deal-idle-lanes-first.w1); nil when it has none.
// The snapshot no longer gates the read; it stays in the signature for its callers.
func (st *Store) friendSeats(ctx context.Context, _ *sprint.Snapshot, now time.Time) ([]sprint.FriendSeat, error) {
	return st.FriendSeats(ctx, now)
}

// FriendNames is every friend of the roster in name order (the friends table's rows), for
// add's hold of a WHO line's name; none when the store keeps no records.
func (st *Store) FriendNames(ctx context.Context) ([]string, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil || err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(r)), nil
}

// FriendSpecs reads the synced roster once for admission checks: every friend with her
// width, class, mode and work restriction, for add's and brief's hold of a named friend's
// streams and kinds (FriendSpec.RestrictionWhy); none when the store keeps no records.
func (st *Store) FriendSpecs(ctx context.Context) ([]FriendSpec, error) {
	r, _, err := st.roster(ctx)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	out := make([]FriendSpec, 0, len(names))
	for _, name := range names {
		e := r[name]
		out = append(out, FriendSpec{Name: name, Width: e.Width, Class: e.Class, Mode: e.Mode, ConfigDir: e.ConfigDir,
			TokenCap: e.TokenCap, TokenCapSet: e.TokenCapSet, Streams: e.Streams, Kinds: e.Kinds})
	}
	return out, nil
}

// FriendBeatOf is the friend's last beat; the zero beat when she has never beaten.
func (st *Store) FriendBeatOf(ctx context.Context, friend string) (sprint.Beat, error) {
	var b sprint.Beat
	kv, err := st.rootKV()
	if err != nil || kv == nil {
		return b, err
	}
	raw, ok, err := kv.GetKey(ctx, friendBeatKey(friend))
	if err != nil || !ok {
		return b, err
	}
	// ignored: an unreadable record is no beat, which the next beat replaces (FriendRows)
	_ = json.Unmarshal([]byte(raw), &b)
	return b, nil
}

// FriendStartStep is friend sync's start receipt for one friend's cards (sprint.FriendStart;
// docs/SPEC-SPRINT.md section 1, a friend's card is working once she starts it): each card
// she has begun moves to working on her row, its deadline from now, or is stamped started
// where it is working. It runs as friend sync, the coordinator's verb.
func FriendStartStep(r sprint.FriendStartReq) Step {
	return Step{Named: len(r.IDs) > 0, Args: ArgsOf(r), Verb: "friend sync", Load: tables(sprint.Fleet), Extras: sprint.NamedExtras(sprint.Fleet, r.IDs), StartsWork: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendStart(s, r) }}
}

// HealthStep is the coordinator's observation of a friend written
// (sprint.ObserveFriend): the step reads the seat in its own snapshot, so an
// observation under a seat that moved is refused, and its commit writes her
// record.
func HealthStep(r sprint.HealthReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "friend health",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.ObserveFriend(s, r) }}
}

// FriendHealth is one observation of a friend by who (the seat's holder):
// her roster entry and her record are read, a replay of the record is
// answered as recorded with nothing written (sprint.HealthReq.Replays), and
// any other observation is the health step, refused under a seat that is
// not the one it names, or with a proof not newer than the row's. The result
// is the record as it stands after, her status by the friends' rule at the
// store's clock, and whether the observation was the row's already.
func (st *Store) FriendHealth(ctx context.Context, friend, who string, obs sprint.FriendHealth, callerOp string) (sprint.FriendHealth, string, bool, error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return sprint.FriendHealth{}, "", false, err
	}
	req := sprint.HealthReq{Friend: friend, Who: who, Obs: obs}
	e, known := r[friend]
	req.Known = known
	if known {
		raw, ok, err := kv.GetKey(ctx, friendHealthKey(friend))
		if err != nil {
			return sprint.FriendHealth{}, "", false, err
		}
		if ok {
			// ignored: an unreadable record is no observation, which this one replaces
			_ = json.Unmarshal([]byte(raw), &req.Prev)
		}
	}
	generation, err := st.seatGeneration(ctx)
	if err != nil {
		return sprint.FriendHealth{}, "", false, err
	}
	if req.Replays() && req.Obs.Generation == generation {
		status, err := st.friendStatusAfter(ctx, friend, sprint.FriendPresence{Held: e.Held, Health: req.Prev, Generation: generation})
		return req.Prev, status, true, err
	}
	step := HealthStep(req)
	step.CallerOp = callerOp
	res, err := st.Run(ctx, step)
	if err != nil {
		return sprint.FriendHealth{}, "", false, err
	}
	if len(res.Refused) > 0 {
		return sprint.FriendHealth{}, "", false, errors.New(res.Refused[0].Why)
	}
	status, err := st.friendStatusAfter(ctx, friend, sprint.FriendPresence{Held: e.Held, Health: obs, Generation: obs.Generation})
	return obs, status, false, err
}

// friendStatusAfter is a friend's status by the friends' rule at the store's clock,
// the presence p given (her hold and the observation a reply stands on) with the
// rest of her evidence read as the friends table reads it: her last beat (a beat
// that says down) and when a card of hers last finished (FriendFinishedAt).
func (st *Store) friendStatusAfter(ctx context.Context, friend string, p sprint.FriendPresence) (string, error) {
	b, err := st.FriendBeatOf(ctx, friend)
	if err != nil {
		return "", err
	}
	fin, err := st.FriendFinishedAt(ctx, friend)
	if err != nil {
		return "", err
	}
	p.Beat, p.Finished = b, fin
	return sprint.FriendStatus(p, st.now()), nil
}

// HealthClearStep is the coordinator's removal of a friend's observation
// (sprint.ClearFriendHealth): the step reads the seat in its own snapshot, and its
// commit removes her friend-health record.
func HealthClearStep(r sprint.HealthClearReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "friend health",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.ClearFriendHealth(s, r) }}
}

// FriendHealthClear removes the coordinator's observation of a friend (friend health
// --clear), so her status is her session's evidence alone (sprint.FriendStatus): her roster
// entry and her record are read, and with dry the removal is checked against the roster
// and the seat and nothing is written. The result is the record that stood (had says
// there was one) and her status by the friends' rule at the store's clock after it.
func (st *Store) FriendHealthClear(ctx context.Context, friend, who string, dry bool, callerOp string) (prev sprint.FriendHealth, had bool, status string, err error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return prev, false, "", err
	}
	e, known := r[friend]
	if known {
		raw, ok, err := kv.GetKey(ctx, friendHealthKey(friend))
		if err != nil {
			return prev, false, "", err
		}
		if ok {
			// ignored: an unreadable record is no observation, and is removed all the same
			_ = json.Unmarshal([]byte(raw), &prev)
			had = true
		}
	}
	req := sprint.HealthClearReq{Friend: friend, Who: who, Known: known}
	if dry {
		holder, err := st.B.Coordinator(ctx)
		if err != nil {
			return prev, had, "", err
		}
		if p := sprint.ClearFriendHealth(&sprint.Snapshot{Coordinator: holder}, req); len(p.Refused) > 0 {
			return prev, had, "", errors.New(p.Refused[0].Why)
		}
	} else {
		step := HealthClearStep(req)
		step.CallerOp = callerOp
		res, err := st.Run(ctx, step)
		if err != nil {
			return prev, had, "", err
		}
		if len(res.Refused) > 0 {
			return prev, had, "", errors.New(res.Refused[0].Why)
		}
	}
	status, err = st.friendStatusAfter(ctx, friend, sprint.FriendPresence{Held: e.Held})
	return prev, had, status, err
}

// FriendSpecOf is what friend sync last wrote of the friend: her width and
// her delivery mode as her nova-config row has them, which her beat answers
// so her daemon reads her row without reaching the config store; a friend the
// roster lacks is refused.
func (st *Store) FriendSpecOf(ctx context.Context, friend string) (FriendSpec, error) {
	r, _, err := st.roster(ctx)
	if err != nil {
		return FriendSpec{}, err
	}
	e, ok := r[friend]
	if !ok {
		return FriendSpec{}, noFriend(r, friend)
	}
	return FriendSpec{Name: friend, Width: e.Width, Class: e.Class, Mode: e.Mode, ConfigDir: e.ConfigDir, TokenCap: e.TokenCap, TokenCapSet: e.TokenCapSet, Roles: e.Roles, Billing: e.Billing, Streams: e.Streams, Kinds: e.Kinds, Dir: e.Dir}, nil
}

// FriendDirs is each friend of the roster's working directory as friend sync last
// wrote it from her nova-config row, by name; a friend whose row has no dir is
// absent. The run loop's reconcile reads it once a pass (frienddir.go).
func (st *Store) FriendDirs(ctx context.Context) (map[string]string, error) {
	r, _, err := st.roster(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for n, e := range r {
		if e.Dir != "" {
			out[n] = e.Dir
		}
	}
	return out, nil
}

// FriendSessions is every friend of the roster with her session's last pong as her last
// beat carries it (zero: none) and whether the coordinator holds her, for the coordinator's
// pass (sprint.TickReq.Sessions); none when the store keeps no records.
func (st *Store) FriendSessions(ctx context.Context) (map[string]sprint.FriendSession, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil || err != nil || len(r) == 0 {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, friendBeatKey(n))
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sprint.FriendSession, len(names))
	for i, n := range names {
		var rec friendBeatRecord
		if i < len(oks) && oks[i] {
			_ = json.Unmarshal([]byte(vals[i]), &rec) // ignored: an unreadable record is no beat
		}
		out[n] = sprint.FriendSession{Pong: rec.Pong, Held: r[n].Held}
	}
	return out, nil
}

// FriendBeats is every friend of the roster with her last beat (a zero beat
// for one who never beat), for the seat check (nova-sprint seat check): the
// ages the friends table derives its status from, read as beats.
func (st *Store) FriendBeats(ctx context.Context) (map[string]sprint.Beat, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil || err != nil || len(r) == 0 {
		return map[string]sprint.Beat{}, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, friendBeatKey(n))
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sprint.Beat, len(names))
	for i, n := range names {
		var b sprint.Beat
		if i < len(oks) && oks[i] {
			_ = json.Unmarshal([]byte(vals[i]), &b) // ignored: an unreadable record is no beat
		}
		out[n] = b
	}
	return out, nil
}
