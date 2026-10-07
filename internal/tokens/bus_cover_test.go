package tokens

// Unit coverage for internal/tokens/bus.go's reader side: the note grammar's parser
// (readNote, parseBody), the fold (foldLane and its refusal helpers), and FoldBus and
// ReadBus, which wire the log's messages, the lanes and the fold together. Every test is
// table-driven and fixed-clock: the fold's at= stamp is a moment the test names, never
// the machine's clock, and the bus is internal/bus's Fake (bustest), so nothing here
// opens a socket, a store or a subprocess.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
)

// busCoverDay is the day every fixture note reports on, and busCoverAt is the fold's own
// at= stamp the messages' at is validated against. Both are fixed: a test pins the code
// against a moment it names, never against the clock the machine happens to run.
const busCoverDay = "2026-09-11"

var busCoverAt = time.Date(2026, 9, 11, 23, 55, 2, 0, time.UTC)

// busCoverSubject renders a note subject for the fixture day with an optional trailer
// tail such as `supersedes=<id>`, in the one order ParseSubject accepts.
func busCoverSubject(tail string) string { return busCoverSubjectDay(busCoverDay, tail) }

// busCoverSubjectDay renders a note subject for the day given.
func busCoverSubjectDay(day, tail string) string {
	s := SubjectPrefix + day + " at=2026-09-11T23:55:02Z build=b1"
	if tail != "" {
		s += " " + tail
	}
	return s
}

// busCoverMessage renders one message of the log: the sender, subject, id and instant the
// bus wrote, and the body.
func busCoverMessage(from, subject, id string, at time.Time, body string) bus.Message {
	return bus.Message{ID: id, From: from, To: []string{"rowan"}, Subject: subject, At: at, Body: body}
}

// busCoverEntry is the message as the log holds it, under the stream id entry.
func busCoverEntry(entry string, m bus.Message) bus.Entry {
	return bus.Entry{Stream: bus.LogKey, Entry: entry, Fields: m.Fields()}
}

// busCoverRules loads a one-line rules file through the package's own attribution seam.
func busCoverRules(t *testing.T) *Rules {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	return rules
}

// busCoverGoodLine is one body line the serializer wrote: the report's grammar, not a
// hand-built one, so the parser and the serializer stay one grammar in the tests too.
func busCoverGoodLine(t Type, count int64, day string) string {
	return BodyLine(day, "emma", "gemini", "schema", t, count, UTC)
}

// busCoverAll indexes notes the way ReadBus does, by the lane each was read from and its
// id, which is the index the fold resolves predecessor sets through.
func busCoverAll(ns ...*note) map[noteKey]*note {
	all := map[noteKey]*note{}
	for _, n := range ns {
		if n == nil {
			continue
		}
		all[noteKey{n.lane, n.id}] = n
	}
	return all
}

func TestBusCoverNearMissSubject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		subject string
		want    bool
	}{
		{name: "capitalised tokens with a day is a near miss", subject: "Tokens 2026-09-11 (rough)", want: true},
		{name: "the exact prefix with a broken trailer is a near miss", subject: "tokens 2026-09-11 at=x", want: true},
		{name: "a subject that never meant tokens is not a near miss", subject: "chore: bump the deps", want: false},
		{name: "a day that is not on the calendar is not a near miss", subject: "tokens 2026-13-40", want: false},
		{name: "a bare word with no day is not a near miss", subject: "tokens", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			why, near := nearMissSubject(tc.subject)
			assert.Equal(t, tc.want, near, "nearMissSubject(%q)", tc.subject)
			if tc.want {
				assert.Contains(t, why, tc.subject, "the why names the subject it refused")
			} else {
				assert.Empty(t, why, "no why for a subject that was not meant as a tokens note")
			}
		})
	}
}

func TestBusCoverNoteClean(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		n    *note
		want bool
	}{
		{name: "a note with no refusal and no unparsed line is clean", n: &note{}, want: true},
		{name: "a refused note is not clean", n: &note{dead: &Unparsed{Text: "refused whole"}}, want: false},
		{name: "a note with one unparsed line is not clean", n: &note{lineErrs: []Unparsed{{Text: "line"}}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.n.clean(), tc.name)
		})
	}
}

func TestBusCoverReadNote(t *testing.T) {
	t.Parallel()

	const id = "01ADA000000000000000000001"
	t.Run("a clean note parses whole", func(t *testing.T) {
		t.Parallel()

		m := busCoverMessage("ada", busCoverSubject(""), id, busCoverAt, busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")
		n := readNote("ada", "1-0", m, busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		assert.True(t, n.clean(), "a good note is clean")
		assert.Equal(t, "ada", n.lane)
		assert.Equal(t, id, n.id)
		assert.Equal(t, busCoverDay, n.subject.day)
		assert.Equal(t, "2026-09-11T23:55:02Z", n.subject.at)
		assert.Equal(t, "b1", n.subject.build)
		require.Len(t, n.msgs, 1)
		assert.True(t, n.zones[UTC], "a six-field line's basis is utc")
		assert.Equal(t, 1, n.comments)
		assert.Equal(t, []string{"schema"}, n.touched)
	})

	t.Run("a body line's number is its line in the body, from 1", func(t *testing.T) {
		t.Parallel()

		m := busCoverMessage("ada", busCoverSubject(""), id, busCoverAt, busCoverGoodLine(Input, 1, busCoverDay)+"\nnot a line\n")
		n := readNote("ada", "1-0", m, busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		require.Len(t, n.lineErrs, 1)
		assert.Equal(t, 2, n.lineErrs[0].Line)
	})

	t.Run("a message with no id field is named by its stream entry", func(t *testing.T) {
		t.Parallel()

		m := busCoverMessage("ada", busCoverSubject(""), "", busCoverAt, busCoverGoodLine(Input, 1, busCoverDay)+"\n")
		n := readNote("ada", "1760000000000-0", m, busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		assert.Equal(t, "1760000000000-0", n.id)
		assert.True(t, n.clean())
	})

	for _, tc := range []struct {
		name       string
		subject    string
		at         time.Time
		want       string
		wantNil    bool
		wantRemedy bool // only the near miss carries its own remedy
	}{
		{name: "a message that is not a note at all is not a note", subject: "chore: bump the deps",
			at: busCoverAt, wantNil: true},
		{name: "a near miss is a dead note with its own remedy", subject: "Tokens 2026-09-11 (rough)",
			at: busCoverAt, want: "names tokens and a day", wantRemedy: true},
		{name: "a message with no at stamp refuses the note", subject: busCoverSubject(""),
			want: "no at stamp"},
		{name: "an at after the fold's own at= refuses the note", subject: busCoverSubject(""),
			at: busCoverAt.Add(24 * time.Hour), want: "later than this fold's own at="},
		{name: "a bad predecessor set refuses the note", subject: busCoverSubject("supersedes=not-an-id"),
			at: busCoverAt, want: "is not a bus id (a 26-character ULID)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := busCoverMessage("ada", tc.subject, id, tc.at, busCoverGoodLine(Input, 1, busCoverDay)+"\n")
			n := readNote("ada", "1-0", m, busCoverRules(t), busCoverAt)
			if tc.wantNil {
				assert.Nil(t, n, tc.name)
				return
			}
			require.NotNil(t, n)
			require.NotNil(t, n.dead, tc.name)
			assert.Contains(t, n.dead.Text, tc.want, tc.name)
			assert.Equal(t, tc.wantRemedy, n.dead.Remedy != "", "only a near miss carries its own remedy")
			assert.False(t, n.clean(), "a dead note is not clean")
		})
	}
}

func TestBusCoverParseBody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		body  []string
		check func(*testing.T, *note)
	}{
		{
			name: "a six-field utc line folds into the stream",
			body: []string{busCoverGoodLine(Input, 1234, busCoverDay)},
			check: func(t *testing.T, n *note) {
				t.Helper()
				require.Len(t, n.msgs, 1)
				v, ok := n.msgs[0].Counts.Get(Input)
				assert.True(t, ok, "the line named input, so the count is present")
				assert.EqualValues(t, 1234, v)
				assert.Equal(t, busCoverDay, n.msgs[0].Day)
				assert.Equal(t, "gemini", n.msgs[0].Model)
				assert.Equal(t, "schema", n.msgs[0].Repo)
				assert.Equal(t, UTC, n.msgs[0].Basis)
				assert.True(t, n.zones[UTC], "the line's basis is a zone of the note")
				assert.Equal(t, 0, n.rough)
				assert.Equal(t, 0, n.redated)
			},
		},
		{
			name: "a rough zoned line keeps its mark and its basis",
			body: []string{"2026-09-11\temma\tgemini\tschema\toutput\t~7\tday_basis=America/Los_Angeles"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				require.Len(t, n.msgs, 1)
				v, ok := n.msgs[0].Counts.Get(Output)
				assert.True(t, ok, "the line named output, so the count is present")
				assert.EqualValues(t, 7, v)
				assert.Equal(t, 1, n.msgs[0].Rough, "the rough mark travels with the message")
				assert.Equal(t, 1, n.rough)
				assert.False(t, n.zones[UTC], "a zoned line is not utc")
				assert.True(t, n.zones["America/Los_Angeles"])
			},
		},
		{
			name: "a line for another day is redated, not refused",
			body: []string{busCoverGoodLine(Input, 3, "2026-09-12")},
			check: func(t *testing.T, n *note) {
				t.Helper()
				require.Len(t, n.msgs, 1)
				assert.Equal(t, 1, n.redated, "the line's day is the note's business, kept as a count")
				assert.Empty(t, n.lineErrs)
			},
		},
		{
			name: "a comment folds no number but the repos comment touches",
			body: []string{"# a comment", "# repos: schema,other", ""},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Equal(t, 2, n.comments)
				assert.Equal(t, []string{"schema", "other"}, n.touched)
				assert.Empty(t, n.msgs)
				assert.True(t, n.clean(), "comments and blank lines fold nothing and refuse nothing")
			},
		},
		{
			name: "an uppercase repos comment is only a comment",
			body: []string{"# repos: SCHEMA"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Equal(t, 1, n.comments)
				assert.Empty(t, n.touched, "the one comment shape is lowercase repo slugs")
			},
		},
		{
			name: "a line with the wrong number of fields is unparsed",
			body: []string{"only three\tfields\there"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				require.Len(t, n.lineErrs, 1)
				assert.Equal(t, 3, n.lineErrs[0].Line, "line= is the line in the FILE, offset included")
				assert.Equal(t, "01ADA000000000000000000001", n.lineErrs[0].Note)
				assert.Equal(t, Label(KindBus, "ada"), n.lineErrs[0].Label)
				assert.False(t, n.clean(), "a note with an unparsed line is not clean")
			},
		},
		{
			name: "a count with no digits is unparsed",
			body: []string{"2026-09-11\temma\tgemini\tschema\tinput\tseven"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1)
			},
		},
		{
			name: "an unknown type name is unparsed",
			body: []string{"2026-09-11\temma\tgemini\tschema\ttokens\t7"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1)
			},
		},
		{
			name: "a count that overflows the int64 is unparsed",
			body: []string{"2026-09-11\temma\tgemini\tschema\tinput\t99999999999999999999"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1, "the shape admits it, the number does not fit, so the line is unparsed")
				assert.Empty(t, n.msgs)
			},
		},
		{
			name: "day_basis=utc is refused, the six-field form already says it",
			body: []string{busCoverGoodLine(Input, 1, busCoverDay) + "\tday_basis=utc"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := &note{lane: "ada", id: "01ADA000000000000000000001", subject: parsedSubject{day: busCoverDay},
				zones: map[string]bool{}}
			parseBody(n, Label(KindBus, "ada"), tc.body, 2, busCoverRules(t))
			tc.check(t, n)
		})
	}
}

func TestBusCoverBadPredecessors(t *testing.T) {
	t.Parallel()

	cleanPred := func(lane, day string) *note {
		return &note{lane: lane, id: "01ADA000000000000000000001", subject: parsedSubject{day: day}}
	}
	unclean := &note{lane: "ada", id: "01ADA000000000000000000001", subject: parsedSubject{day: busCoverDay},
		lineErrs: []Unparsed{{Text: "line"}}}
	succ := func(ids ...string) *note {
		return &note{lane: "ada", id: "01ADA000000000000000000002", subject: parsedSubject{day: busCoverDay, supersedes: ids}}
	}
	for _, tc := range []struct {
		name string
		pred *note
		ids  []string
		want string
	}{
		{name: "a clean predecessor of the same lane and day is accepted", pred: cleanPred("ada", busCoverDay),
			ids: []string{"01ADA000000000000000000001"}, want: ""},
		{name: "a predecessor the bus has never seen is refused", pred: nil,
			ids: []string{"01ADA000000000000000000009"}, want: "no such note in this lane for this day: 01ADA000000000000000000009"},
		{name: "a predecessor of another lane is refused", pred: cleanPred("zed", busCoverDay),
			ids: []string{"01ADA000000000000000000001"}, want: "another lane (from-zed)"},
		{name: "a predecessor for another day is refused", pred: cleanPred("ada", "2026-09-12"),
			ids: []string{"01ADA000000000000000000001"}, want: "another day (2026-09-12)"},
		{name: "a predecessor that did not parse is refused", pred: unclean,
			ids: []string{"01ADA000000000000000000001"}, want: "did not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			all := busCoverAll(tc.pred)
			got := badPredecessors(succ(tc.ids...), all)
			if tc.want == "" {
				assert.Empty(t, got, tc.name)
			} else {
				assert.Contains(t, got, tc.want, tc.name)
			}
		})
	}

	t.Run("an id another lane claims does not refuse the owner's own note", func(t *testing.T) {
		t.Parallel()

		all := busCoverAll(cleanPred("ada", busCoverDay), cleanPred("zed", busCoverDay))
		assert.Empty(t, badPredecessors(succ("ada-000000000001"), all),
			"the own lane's claim is the predecessor; the other lane's claim is not")
	})
}

func TestBusCoverLaneBasis(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		zones map[string]bool
		want  string
	}{
		{name: "no zone at all is utc", zones: nil, want: UTC},
		{name: "utc alone is utc", zones: map[string]bool{UTC: true}, want: UTC},
		{name: "the one zone a lane carried is the basis", zones: map[string]bool{"America/New_York": true},
			want: "America/New_York"},
		{name: "utc and a zone are mixed", zones: map[string]bool{UTC: true, "America/New_York": true},
			want: "mixed"},
		{name: "two non-utc zones are mixed too", zones: map[string]bool{"America/New_York": true, "Asia/Tokyo": true},
			want: "mixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, laneBasis(tc.zones), tc.name)
		})
	}
}

func TestBusCoverReportedTypes(t *testing.T) {
	t.Parallel()

	counts := func(ts ...Type) Counts {
		var c Counts
		for _, tt := range ts {
			c.Set(tt, 1)
		}
		return c
	}
	for _, tc := range []struct {
		name   string
		stream []Message
		want   []Type
	}{
		{name: "the types a stream named, in the five types' own order",
			stream: []Message{{Counts: counts(Output)}, {Counts: counts(Input)}}, want: []Type{Input, Output}},
		{name: "a type named twice is reported once",
			stream: []Message{{Counts: counts(Input)}, {Counts: counts(Input)}}, want: []Type{Input}},
		{name: "a stream that measured nothing reports no types", stream: []Message{{}}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, reportedTypes(tc.stream), tc.name)
		})
	}
}

func TestBusCoverFoldLane(t *testing.T) {
	t.Parallel()

	laneNote := func(t *testing.T, subject, id, body string) *note {
		t.Helper()
		n := readNote("ada", "1-0", busCoverMessage("ada", subject, id, busCoverAt, body), busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		return n
	}

	t.Run("two clean notes on two days fold in day order", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject(""), "01ADA000000000000000000001",
			busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")
		b := laneNote(t, busCoverSubjectDay("2026-09-12", ""), "01ADA000000000000000000002",
			busCoverGoodLine(Input, 5, "2026-09-12"))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(a, b)
		foldLane(s, "ada", []*note{b, a}, all)
		require.Len(t, s.Stream, 2)
		assert.Equal(t, busCoverDay, s.Stream[0].Day, "the days fold in order, whatever order the notes arrived in")
		assert.Equal(t, "2026-09-12", s.Stream[1].Day)
		assert.Equal(t, []Type{Input}, s.Reports)
		assert.Equal(t, UTC, s.Basis)
		assert.Equal(t, 1, s.Stat.Comments)
		require.Len(t, s.Toucheds, 1)
		assert.Equal(t, []string{"schema"}, s.Toucheds[0].Repos)
		assert.Empty(t, s.Unparseds)
	})

	t.Run("a correction supersedes its predecessor and folds alone", func(t *testing.T) {
		t.Parallel()

		pred := laneNote(t, busCoverSubject(""), "01ADA000000000000000000001", busCoverGoodLine(Input, 1234, busCoverDay))
		succ := laneNote(t, busCoverSubject("supersedes=01ADA000000000000000000001"), "01ADA000000000000000000002",
			busCoverGoodLine(Output, 5, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(pred, succ)
		foldLane(s, "ada", []*note{pred, succ}, all)
		require.Len(t, s.Supersededs, 1)
		assert.Equal(t, "01ADA000000000000000000001", s.Supersededs[0].Note)
		assert.Equal(t, "01ADA000000000000000000002", s.Supersededs[0].By)
		assert.Equal(t, busCoverDay, s.Supersededs[0].Day)
		assert.Equal(t, 1, s.Stat.Superseded)
		require.Len(t, s.Stream, 1)
		v, ok := s.Stream[0].Counts.Get(Output)
		assert.True(t, ok, "only the successor's line folds")
		assert.EqualValues(t, 5, v)
		assert.Empty(t, s.Conflicts)
	})

	t.Run("a successor naming an absent predecessor is refused whole", func(t *testing.T) {
		t.Parallel()

		orph := laneNote(t, busCoverSubject("supersedes=01ADA000000000000000000009"), "01ADA000000000000000000001",
			busCoverGoodLine(Input, 1, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		foldLane(s, "ada", []*note{orph}, busCoverAll())
		require.Len(t, s.Unparseds, 1)
		assert.Contains(t, s.Unparseds[0].Text, "no such note in this lane for this day: 01ADA000000000000000000009")
		assert.Contains(t, s.Unparseds[0].Text, "; send a correction whose subject carries supersedes=<id>")
		assert.Equal(t, 1, s.Stat.Unparsed)
		assert.Empty(t, s.Stream)
	})

	t.Run("two tips on one day conflict and fold nothing", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject(""), "01ADA000000000000000000001", busCoverGoodLine(Input, 1, busCoverDay))
		b := laneNote(t, busCoverSubject(""), "01ADA000000000000000000002", busCoverGoodLine(Input, 2, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(a, b)
		foldLane(s, "ada", []*note{a, b}, all)
		require.Len(t, s.Conflicts, 1)
		assert.Equal(t, busCoverDay, s.Conflicts[0].Day)
		assert.Equal(t, []string{"01ADA000000000000000000001", "01ADA000000000000000000002"}, s.Conflicts[0].Notes)
		assert.Empty(t, s.Stream)
		assert.Empty(t, s.Supersededs)
	})

	t.Run("a cycle refuses every note on it", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject("supersedes=01ADA000000000000000000002"), "01ADA000000000000000000001",
			busCoverGoodLine(Input, 1, busCoverDay))
		b := laneNote(t, busCoverSubject("supersedes=01ADA000000000000000000001"), "01ADA000000000000000000002",
			busCoverGoodLine(Input, 2, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(a, b)
		foldLane(s, "ada", []*note{a, b}, all)
		require.Len(t, s.Unparseds, 2)
		assert.Contains(t, s.Unparseds[0].Text, "a cycle")
		assert.Contains(t, s.Unparseds[1].Text, "a cycle")
		assert.Empty(t, s.Stream)
	})
}

// A note another lane holds under an id the lane owner also chose must not refuse the
// owner's own correction: the predecessor a supersedes= names is the successor's lane's
// business, resolved there first (security#75 finding 3).
func TestBusAPlantedNoteIdInAnotherLaneDoesNotRefuseTheOwnersCorrection(t *testing.T) {
	t.Parallel()

	// Alice's note and her correction of it; a hand-planted message of bob's whose id
	// field carries alice's id.
	first := "01AAAAAAAAAAAAAAAAAAAAAAAA01"
	second := "01AAAAAAAAAAAAAAAAAAAAAAAA02"
	entries := []bus.Entry{
		busCoverEntry("1-0", busCoverMessage("alice", busCoverSubject(""), first, busCoverAt, busCoverGoodLine(Input, 1234, busCoverDay)+"\n")),
		busCoverEntry("2-0", busCoverMessage("alice", busCoverSubject("supersedes="+first), second, busCoverAt, busCoverGoodLine(Input, 4321, busCoverDay)+"\n")),
		busCoverEntry("3-0", busCoverMessage("bob", busCoverSubject(""), first, busCoverAt, busCoverGoodLine(Input, 7, busCoverDay)+"\n")),
	}

	got := FoldBus("x", []string{"alice", "bob"}, entries, busCoverRules(t), busCoverAt)
	require.Len(t, got, 2)
	alice, bob := got[0], got[1]
	assert.Empty(t, alice.Unparseds, "another lane's planted id does not refuse the owner's correction")
	assert.Empty(t, alice.Conflicts)
	require.Len(t, alice.Stream, 1)
	v, ok := alice.Stream[0].Counts.Get(Input)
	assert.True(t, ok, "the correction folds a line")
	assert.EqualValues(t, 4321, v, "the lane-day shows the corrected counts, not the first note's")
	require.Len(t, alice.Supersededs, 1)
	assert.Equal(t, first, alice.Supersededs[0].Note)
	assert.Equal(t, second, alice.Supersededs[0].By)
	assert.Equal(t, busCoverDay, alice.Supersededs[0].Day)
	assert.Equal(t, 1, alice.Stat.Superseded)
	require.Len(t, bob.Stream, 1)
	bv, ok := bob.Stream[0].Counts.Get(Input)
	assert.True(t, ok, "the planted note folds in the lane that holds it")
	assert.EqualValues(t, 7, bv)
}

func TestBusCoverFoldBus(t *testing.T) {
	t.Parallel()

	const path = "127.0.0.1:6381"
	t.Run("every lane the roster names and every sender folds, sorted", func(t *testing.T) {
		t.Parallel()

		ada := busCoverMessage("ada", busCoverSubject(""), "01ADA000000000000000000001", busCoverAt,
			busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")
		zedNear := busCoverMessage("zed", "Tokens 2026-09-11 (rough)", "01ZED000000000000000000001", busCoverAt, busCoverGoodLine(Input, 1, busCoverDay)+"\n")
		zedChat := busCoverMessage("zed", "chore: bump the deps", "01ZED000000000000000000002", busCoverAt, "nothing here\n")
		// A sender the roster no longer holds is still a lane: its messages are in the log.
		old := busCoverMessage("old", busCoverSubject(""), "01OLD000000000000000000001", busCoverAt, busCoverGoodLine(Input, 2, busCoverDay)+"\n")
		entries := []bus.Entry{busCoverEntry("1-0", ada), busCoverEntry("2-0", zedNear), busCoverEntry("3-0", zedChat), busCoverEntry("4-0", old)}

		got := FoldBus(path, []string{"zed", "gone", "ada"}, entries, busCoverRules(t), busCoverAt)
		require.Len(t, got, 4)
		adaS, gone, oldS, zed := got[0], got[1], got[2], got[3]
		assert.Equal(t, "bus:ada", adaS.Label)
		assert.Equal(t, KindBus, adaS.Kind)
		assert.Equal(t, "127.0.0.1:6381/from-ada", adaS.Path)
		assert.Equal(t, UTC, adaS.Basis)
		assert.Equal(t, []Type{Input}, adaS.Reports)
		require.Len(t, adaS.Stream, 1)
		v, ok := adaS.Stream[0].Counts.Get(Input)
		assert.True(t, ok, "ada's note folds its input line")
		assert.EqualValues(t, 1234, v)
		assert.Equal(t, "schema", adaS.Stream[0].Repo, "the source is the sender, whatever the who field says")
		assert.Equal(t, 1, adaS.Stat.Files)
		assert.Equal(t, 1, adaS.Stat.Comments)
		assert.Empty(t, adaS.Unreadables)
		assert.Equal(t, "bus:gone", gone.Label)
		assert.Equal(t, 0, gone.Stat.Files, "a lane that sent nothing opened no messages")
		assert.Empty(t, gone.Unreadables)
		assert.Empty(t, gone.Stream)
		assert.Equal(t, "bus:old", oldS.Label)
		require.Len(t, oldS.Stream, 1)
		assert.Equal(t, "bus:zed", zed.Label)
		assert.Equal(t, 2, zed.Stat.Files, "files= is what the lane OPENED, tokens note or not")
		assert.Equal(t, 1, zed.Stat.Unparsed, "the near miss is counted; the other message is only traffic")
		require.Len(t, zed.Unparseds, 1)
		assert.Equal(t, "01ZED000000000000000000001", zed.Unparseds[0].Note)
		assert.Equal(t, 0, zed.Unparseds[0].Line, "a refusal of the whole note is line 0")
		assert.NotEmpty(t, zed.Unparseds[0].Remedy)
		assert.Empty(t, zed.Stream)
	})

	t.Run("the order of the log does not change the fold", func(t *testing.T) {
		t.Parallel()

		a := busCoverEntry("1-0", busCoverMessage("ada", busCoverSubject(""), "01ADA000000000000000000001", busCoverAt, busCoverGoodLine(Input, 1, busCoverDay)+"\n"))
		b := busCoverEntry("2-0", busCoverMessage("ada", busCoverSubject("supersedes=01ADA000000000000000000001"), "01ADA000000000000000000002", busCoverAt, busCoverGoodLine(Input, 7, busCoverDay)+"\n"))
		fwd := FoldBus("x", nil, []bus.Entry{a, b}, busCoverRules(t), busCoverAt)
		rev := FoldBus("x", nil, []bus.Entry{b, a}, busCoverRules(t), busCoverAt)
		require.Len(t, fwd, 1)
		require.Len(t, rev, 1)
		assert.Equal(t, fwd[0].Stream, rev[0].Stream)
		assert.Equal(t, fwd[0].Supersededs, rev[0].Supersededs)
	})
}

// pagedStore answers a Range with at most page entries, as a long log does.
type pagedStore struct {
	*bustest.Fake
	page int
}

func (p *pagedStore) Range(ctx context.Context, stream, from, to string, count int) ([]bus.Entry, error) {
	return p.Fake.Range(ctx, stream, from, to, min(count, p.page))
}

func TestBusCoverReadBus(t *testing.T) {
	t.Parallel()

	send := func(t *testing.T, b *bus.Bus, from, subject, body string) {
		t.Helper()
		_, err := b.Send(context.Background(), bus.Message{From: from, To: []string{"rowan"}, Subject: subject, Body: body})
		require.NoError(t, err)
	}

	t.Run("the whole log is read, page after page, however long it is", func(t *testing.T) {
		t.Parallel()

		// The fake's clock is the fold's: the store takes each message a second after the last.
		fb := bustest.NewFake(busCoverAt.Add(-time.Hour), "ada", "rowan")
		b := &bus.Bus{Store: &pagedStore{Fake: fb, page: 2}}
		for i := 0; i < 5; i++ {
			send(t, b, "ada", "chat", "x")
		}
		got := ReadBus(context.Background(), b, "x", busCoverRules(t), busCoverAt)
		var ada *Source
		for _, s := range got {
			if s.Label == "bus:ada" {
				ada = s
			}
		}
		require.NotNil(t, ada)
		assert.Equal(t, 5, ada.Stat.Files, "no message is dropped for the log being longer than a page")
	})

	t.Run("a note on the log folds through ReadBus", func(t *testing.T) {
		t.Parallel()

		fb := bustest.NewFake(busCoverAt.Add(-time.Hour), "ada", "rowan")
		b := &bus.Bus{Store: fb}
		send(t, b, "ada", busCoverSubject(""), busCoverGoodLine(Input, 1234, busCoverDay)+"\n")
		got := ReadBus(context.Background(), b, "x", busCoverRules(t), busCoverAt)
		require.Len(t, got, 2)
		assert.Equal(t, "bus:ada", got[0].Label)
		require.Len(t, got[0].Stream, 1)
		assert.Empty(t, got[0].Unreadables)
	})

	t.Run("a store that does not answer is one unreadable source, never a short log", func(t *testing.T) {
		t.Parallel()

		fb := bustest.NewFake(busCoverAt, "ada")
		fb.Fail = errors.New("connection refused")
		got := ReadBus(context.Background(), &bus.Bus{Store: fb}, "127.0.0.1:6381", busCoverRules(t), busCoverAt)
		require.Len(t, got, 1)
		s := got[0]
		assert.Equal(t, KindBus, s.Label)
		assert.Equal(t, 1, s.Stat.Unreadable)
		assert.Equal(t, 0, s.Stat.Files)
		require.Len(t, s.Unreadables, 1)
		assert.Equal(t, "127.0.0.1:6381/"+bus.LogKey, s.Unreadables[0].Path)
		assert.Contains(t, s.Unreadables[0].Why, "connection refused")
		assert.Empty(t, s.Stream)
	})
}
