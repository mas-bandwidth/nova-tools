package tokens

// Unit coverage for internal/tokens/bus.go's reader side: the note grammar's parser
// (splitNote, readNote, parseBody), the fold (foldLane and its refusal helpers), and
// ReadBus, which wires the roster, the lanes and the fold together. Every test is
// table-driven and fixed-clock: the fold's at= stamp is a moment the test names, never
// the machine's clock, and nothing here opens a socket, a store or a subprocess.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// busCoverDay is the day every fixture note reports on, and busCoverAt is the fold's own
// at= stamp the notes' Date: lines are validated against. Both are fixed: a test pins the
// code against a moment it names, never against the clock the machine happens to run.
const busCoverDay = "2026-09-11"

var busCoverAt = time.Date(2026, 9, 11, 23, 55, 2, 0, time.UTC)

func busCoverStamp() string { return busCoverAt.Format(BusDateLayout) }

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

// busCoverNoteText renders one note file the way nova-bus writes it: a Subject:, a Date:,
// an Id:, and the body after one blank line.
func busCoverNoteText(subject, id, date, body string) string {
	return "Subject: " + subject + "\nDate: " + date + "\nId: " + id + "\n\n" + body
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
	return BodyLine(day, "operator", "gemini", "schema", t, count, UTC)
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

func TestBusCoverLaneNames(t *testing.T) {
	t.Parallel()

	t.Run("the roster names the lanes sorted and skips what is not one", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		roster := `{"participants":[{"name":"Ada","lane":"from-ada"},` +
			`{"name":"Zed","lane":"from-zed"},{"name":"NoLane","lane":"other-nobody"},` +
			`{"name":"Empty","lane":"from-"}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
		names, err := laneNames(os.DirFS(dir))
		require.NoError(t, err)
		assert.Equal(t, []string{"ada", "zed"}, names, "the slugs are sorted, and only from-<slug> lanes are named")
	})

	t.Run("a directory with no roster is refused by name", func(t *testing.T) {
		t.Parallel()

		_, err := laneNames(os.DirFS(t.TempDir()))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "participants.json", "the refusal names the file it wanted")
	})

	t.Run("a malformed roster is refused", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte("{oops"), 0o644))
		_, err := laneNames(os.DirFS(dir))
		require.Error(t, err)
	})

	t.Run("a roster that names no lane is refused with the shape it wants", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		roster := `{"participants":[{"name":"Ada","lane":"other-ada"}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
		_, err := laneNames(os.DirFS(dir))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "names no lane", "the refusal says what the roster wants")
	})
}

func TestBusCoverSplitNote(t *testing.T) {
	t.Parallel()

	t.Run("the header tolerates a heading, a bullet and a duplicate, and finds the body", func(t *testing.T) {
		t.Parallel()

		text := "# heading\n\n- Subject: " + busCoverSubject("") + "\nSubject: other\nDate: " +
			busCoverStamp() + "\nnot a header\n\nbody line"
		header, body, lines, bodyStart := splitNote(text)
		assert.Equal(t, busCoverSubject(""), header["Subject"], "the first Subject: wins over a duplicate")
		assert.Equal(t, busCoverStamp(), header["Date"])
		assert.Equal(t, 3, lines["Subject"], "line= is the line in the FILE, heading included")
		assert.Equal(t, 5, lines["Date"])
		assert.Equal(t, []string{"body line"}, body)
		assert.Equal(t, 7, bodyStart, "bodyStart is the 0-based index of the first body line")
		assert.NotContains(t, header, "not a header", "a header line without a colon is skipped")
	})

	t.Run("a note with no body has none", func(t *testing.T) {
		t.Parallel()

		_, body, _, bodyStart := splitNote("Subject: " + busCoverSubject("") + "\nDate: " + busCoverStamp())
		assert.Empty(t, body)
		assert.Equal(t, 3, bodyStart)
	})
}

func TestBusCoverReadNote(t *testing.T) {
	t.Parallel()

	t.Run("a clean note parses whole", func(t *testing.T) {
		t.Parallel()

		text := busCoverNoteText(busCoverSubject(""), "ada-000000000001", busCoverStamp(),
			busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")
		n := readNote("ada", filepath.Join(t.TempDir(), "ada-000000000001.md"), text, busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		assert.True(t, n.clean(), "a good note is clean")
		assert.Equal(t, "ada", n.lane)
		assert.Equal(t, "ada-000000000001", n.id)
		assert.Equal(t, busCoverDay, n.subject.day)
		assert.Equal(t, "2026-09-11T23:55:02Z", n.subject.at)
		assert.Equal(t, "b1", n.subject.build)
		assert.Equal(t, 1, n.subjectLine, "Subject: is the first line of the file")
		assert.Equal(t, 2, n.dateLine)
		require.Len(t, n.msgs, 1)
		assert.True(t, n.zones[UTC], "a six-field line's basis is utc")
		assert.Equal(t, 1, n.comments)
		assert.Equal(t, []string{"schema"}, n.touched)
	})

	t.Run("a note with no Id: header takes the file's name", func(t *testing.T) {
		t.Parallel()

		text := "Subject: " + busCoverSubject("") + "\nDate: " + busCoverStamp() + "\nId: \n\n" +
			busCoverGoodLine(Input, 1, busCoverDay) + "\n"
		n := readNote("ada", filepath.Join(t.TempDir(), "ada-000000000001.md"), text, busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		assert.Equal(t, "ada-000000000001.md", n.id, "an id-less note is named by the file it is in")
		assert.True(t, n.clean(), "a missing Id: does not refuse a note")
	})

	for _, tc := range []struct {
		name       string
		subject    string
		date       string
		noDate     bool
		want       string
		wantNil    bool
		wantRemedy bool // only the near miss carries its own remedy
	}{
		{name: "a file that is not a note at all is not a note", subject: "chore: bump the deps",
			date: busCoverStamp(), wantNil: true},
		{name: "a near miss is a dead note with its own remedy", subject: "Tokens 2026-09-11 (rough)",
			date: busCoverStamp(), want: "names tokens and a day", wantRemedy: true},
		{name: "a note with no Date: header refuses the note", subject: busCoverSubject(""), noDate: true,
			want: "no Date: header"},
		{name: "an unreadable Date refuses the note", subject: busCoverSubject(""), date: "the day after the fold",
			want: BusDateLayout},
		{name: "a Date after the fold's own at= refuses the note", subject: busCoverSubject(""),
			date: busCoverAt.Add(24 * time.Hour).Format(BusDateLayout), want: "later than this fold's own at="},
		{name: "a bad predecessor set refuses the note", subject: busCoverSubject("supersedes=not-an-id"),
			date: busCoverStamp(), want: "is not <sender>-<12 hex>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text := "Subject: " + tc.subject + "\n"
			if !tc.noDate {
				text += "Date: " + tc.date + "\n"
			}
			text += "Id: ada-000000000001\n\n" + busCoverGoodLine(Input, 1, busCoverDay) + "\n"
			n := readNote("ada", filepath.Join(t.TempDir(), "ada-000000000001.md"), text,
				busCoverRules(t), busCoverAt)
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
			body: []string{"2026-09-11\toperator\tgemini\tschema\toutput\t~7\tday_basis=America/Los_Angeles"},
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
				assert.Equal(t, "ada-000000000001", n.lineErrs[0].Note)
				assert.Equal(t, Label(KindBus, "ada"), n.lineErrs[0].Label)
				assert.False(t, n.clean(), "a note with an unparsed line is not clean")
			},
		},
		{
			name: "a count with no digits is unparsed",
			body: []string{"2026-09-11\toperator\tgemini\tschema\tinput\tseven"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1)
			},
		},
		{
			name: "an unknown type name is unparsed",
			body: []string{"2026-09-11\toperator\tgemini\tschema\ttokens\t7"},
			check: func(t *testing.T, n *note) {
				t.Helper()
				assert.Len(t, n.lineErrs, 1)
			},
		},
		{
			name: "a count that overflows the int64 is unparsed",
			body: []string{"2026-09-11\toperator\tgemini\tschema\tinput\t99999999999999999999"},
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

			n := &note{lane: "ada", id: "ada-000000000001", subject: parsedSubject{day: busCoverDay},
				zones: map[string]bool{}}
			parseBody(n, Label(KindBus, "ada"), tc.body, 2, busCoverRules(t))
			tc.check(t, n)
		})
	}
}

func TestBusCoverBadPredecessors(t *testing.T) {
	t.Parallel()

	cleanPred := func(lane, day string) *note {
		return &note{lane: lane, id: "ada-000000000001", subject: parsedSubject{day: day}}
	}
	unclean := &note{lane: "ada", id: "ada-000000000001", subject: parsedSubject{day: busCoverDay},
		lineErrs: []Unparsed{{Text: "line"}}}
	succ := func(ids ...string) *note {
		return &note{lane: "ada", id: "ada-000000000002", subject: parsedSubject{day: busCoverDay, supersedes: ids}}
	}
	for _, tc := range []struct {
		name string
		pred *note
		ids  []string
		want string
	}{
		{name: "a clean predecessor of the same lane and day is accepted", pred: cleanPred("ada", busCoverDay),
			ids: []string{"ada-000000000001"}, want: ""},
		{name: "a predecessor the bus has never seen is refused", pred: nil,
			ids: []string{"ada-000000000009"}, want: "no such note in this lane for this day: ada-000000000009"},
		{name: "a predecessor of another lane is refused", pred: cleanPred("zed", busCoverDay),
			ids: []string{"ada-000000000001"}, want: "another lane (from-zed)"},
		{name: "a predecessor for another day is refused", pred: cleanPred("ada", "2026-09-12"),
			ids: []string{"ada-000000000001"}, want: "another day (2026-09-12)"},
		{name: "a predecessor that did not parse is refused", pred: unclean,
			ids: []string{"ada-000000000001"}, want: "did not parse"},
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
		n := readNote("ada", filepath.Join(t.TempDir(), id+".md"),
			busCoverNoteText(subject, id, busCoverStamp(), body), busCoverRules(t), busCoverAt)
		require.NotNil(t, n)
		return n
	}

	t.Run("two clean notes on two days fold in day order", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject(""), "ada-000000000001",
			busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")
		b := laneNote(t, busCoverSubjectDay("2026-09-12", ""), "ada-000000000002",
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

		pred := laneNote(t, busCoverSubject(""), "ada-000000000001", busCoverGoodLine(Input, 1234, busCoverDay))
		succ := laneNote(t, busCoverSubject("supersedes=ada-000000000001"), "ada-000000000002",
			busCoverGoodLine(Output, 5, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(pred, succ)
		foldLane(s, "ada", []*note{pred, succ}, all)
		require.Len(t, s.Supersededs, 1)
		assert.Equal(t, "ada-000000000001", s.Supersededs[0].Note)
		assert.Equal(t, "ada-000000000002", s.Supersededs[0].By)
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

		orph := laneNote(t, busCoverSubject("supersedes=ada-000000000009"), "ada-000000000001",
			busCoverGoodLine(Input, 1, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		foldLane(s, "ada", []*note{orph}, busCoverAll())
		require.Len(t, s.Unparseds, 1)
		assert.Contains(t, s.Unparseds[0].Text, "no such note in this lane for this day: ada-000000000009")
		assert.Contains(t, s.Unparseds[0].Text, "; send a correction whose subject carries supersedes=<id>")
		assert.Equal(t, 1, s.Stat.Unparsed)
		assert.Empty(t, s.Stream)
	})

	t.Run("two tips on one day conflict and fold nothing", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject(""), "ada-000000000001", busCoverGoodLine(Input, 1, busCoverDay))
		b := laneNote(t, busCoverSubject(""), "ada-000000000002", busCoverGoodLine(Input, 2, busCoverDay))
		s := &Source{Label: Label(KindBus, "ada"), Kind: KindBus}
		all := busCoverAll(a, b)
		foldLane(s, "ada", []*note{a, b}, all)
		require.Len(t, s.Conflicts, 1)
		assert.Equal(t, busCoverDay, s.Conflicts[0].Day)
		assert.Equal(t, []string{"ada-000000000001", "ada-000000000002"}, s.Conflicts[0].Notes)
		assert.Empty(t, s.Stream)
		assert.Empty(t, s.Supersededs)
	})

	t.Run("a cycle refuses every note on it", func(t *testing.T) {
		t.Parallel()

		a := laneNote(t, busCoverSubject("supersedes=ada-000000000002"), "ada-000000000001",
			busCoverGoodLine(Input, 1, busCoverDay))
		b := laneNote(t, busCoverSubject("supersedes=ada-000000000001"), "ada-000000000002",
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

	dir := t.TempDir()
	roster := `{"participants":[{"name":"Alice","lane":"from-alice"},{"name":"Bob","lane":"from-bob"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
	aliceDir := filepath.Join(dir, "from-alice")
	require.NoError(t, os.MkdirAll(aliceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(aliceDir, "alice-aaaaaaaaaaaa.md"), []byte(
		busCoverNoteText(busCoverSubject(""), "alice-aaaaaaaaaaaa", busCoverStamp(),
			busCoverGoodLine(Input, 1234, busCoverDay))), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(aliceDir, "alice-bbbbbbbbbbbb.md"), []byte(
		busCoverNoteText(busCoverSubject("supersedes=alice-aaaaaaaaaaaa"), "alice-bbbbbbbbbbbb",
			busCoverStamp(), busCoverGoodLine(Input, 4321, busCoverDay))), 0o644))
	bobDir := filepath.Join(dir, "from-bob")
	require.NoError(t, os.MkdirAll(bobDir, 0o755))
	// A hand-planted file in another lane whose Id: header carries the owner's id.
	require.NoError(t, os.WriteFile(filepath.Join(bobDir, "bob-cccccccccccc.md"), []byte(
		busCoverNoteText(busCoverSubject(""), "alice-aaaaaaaaaaaa", busCoverStamp(),
			busCoverGoodLine(Input, 7, busCoverDay))), 0o644))

	got := ReadBus(dir, os.DirFS(dir), busCoverRules(t), busCoverAt)
	require.Len(t, got, 2)
	alice, bob := got[0], got[1]
	assert.Empty(t, alice.Unparseds, "another lane's planted id does not refuse the owner's correction")
	assert.Empty(t, alice.Conflicts)
	require.Len(t, alice.Stream, 1)
	v, ok := alice.Stream[0].Counts.Get(Input)
	assert.True(t, ok, "the correction folds a line")
	assert.EqualValues(t, 4321, v, "the lane-day shows the corrected counts, not the first note's")
	require.Len(t, alice.Supersededs, 1)
	assert.Equal(t, "alice-aaaaaaaaaaaa", alice.Supersededs[0].Note)
	assert.Equal(t, "alice-bbbbbbbbbbbb", alice.Supersededs[0].By)
	assert.Equal(t, busCoverDay, alice.Supersededs[0].Day)
	assert.Equal(t, 1, alice.Stat.Superseded)
	require.Len(t, bob.Stream, 1)
	bv, ok := bob.Stream[0].Counts.Get(Input)
	assert.True(t, ok, "the planted note folds in the lane that holds it")
	assert.EqualValues(t, 7, bv)
}

func TestBusCoverReadBus(t *testing.T) {
	t.Parallel()

	roster := `{"participants":[{"name":"Ada","lane":"from-ada"},` +
		`{"name":"Gone","lane":"from-gone"},{"name":"Zed","lane":"from-zed"}]}`

	t.Run("every lane the roster names folds, in roster order", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
		adaDir := filepath.Join(dir, "from-ada")
		require.NoError(t, os.MkdirAll(adaDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(adaDir, "ada-000000000001.md"), []byte(
			busCoverNoteText(busCoverSubject(""), "ada-000000000001", busCoverStamp(),
				busCoverGoodLine(Input, 1234, busCoverDay)+"\n# repos: schema\n")), 0o644))
		// from-gone has no directory: the lane is quiet, not unreadable.
		zedDir := filepath.Join(dir, "from-zed")
		require.NoError(t, os.MkdirAll(zedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(zedDir, "zed-000000000001.md"), []byte(
			"Subject: Tokens 2026-09-11 (rough)\n\n"+busCoverGoodLine(Input, 1, busCoverDay)+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(zedDir, "zed-000000000002.md"), []byte(
			"Subject: chore: bump the deps\n\nnothing here\n"), 0o644))

		got := ReadBus(dir, os.DirFS(dir), busCoverRules(t), busCoverAt)
		require.Len(t, got, 3)
		ada, gone, zed := got[0], got[1], got[2]
		assert.Equal(t, "bus:ada", ada.Label)
		assert.Equal(t, KindBus, ada.Kind)
		assert.Equal(t, adaDir, ada.Path)
		assert.Equal(t, UTC, ada.Basis)
		assert.Equal(t, []Type{Input}, ada.Reports)
		require.Len(t, ada.Stream, 1)
		v, ok := ada.Stream[0].Counts.Get(Input)
		assert.True(t, ok, "ada's note folds its input line")
		assert.EqualValues(t, 1234, v)
		assert.Equal(t, "schema", ada.Stream[0].Repo, "the source is the lane owner, whatever the who field says")
		assert.Equal(t, 1, ada.Stat.Files)
		assert.Equal(t, 1, ada.Stat.Comments)
		assert.Empty(t, ada.Unreadables)
		assert.Equal(t, "bus:gone", gone.Label)
		assert.Equal(t, 0, gone.Stat.Files, "a lane with no directory opened no files")
		assert.Empty(t, gone.Unreadables)
		assert.Empty(t, gone.Stream)
		assert.Equal(t, "bus:zed", zed.Label)
		assert.Equal(t, 2, zed.Stat.Files, "files= is what the lane OPENED, tokens note or not")
		assert.Equal(t, 1, zed.Stat.Unparsed, "the near miss is counted; the other file is only traffic")
		require.Len(t, zed.Unparseds, 1)
		assert.Equal(t, "zed-000000000001.md", zed.Unparseds[0].Note,
			"a near miss with no Id: header is named by the file it is in")
		assert.Equal(t, 1, zed.Unparseds[0].Line, "a refusal of the whole note is a refusal of the Subject: line")
		assert.NotEmpty(t, zed.Unparseds[0].Remedy)
		assert.Empty(t, zed.Stream)
	})

	t.Run("a lane that cannot be read is counted unreadable, never skipped", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		roster := `{"participants":[{"name":"Busy","lane":"from-busy"},{"name":"Link","lane":"from-link"}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
		// from-busy is a file where a lane's directory should be: ReadDir refuses it with
		// an error that is not a missing directory, so the lane is unreadable, not quiet.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "from-busy"), []byte("not a directory"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "from-link"), 0o755))
		// A note file that cannot be opened is one unreadable, never a silent skip.
		require.NoError(t, os.Symlink("nowhere", filepath.Join(dir, "from-link", "link-000000000001.md")))

		got := ReadBus(dir, os.DirFS(dir), busCoverRules(t), busCoverAt)
		require.Len(t, got, 2)
		busy := got[0]
		assert.Equal(t, 1, busy.Stat.Unreadable)
		require.Len(t, busy.Unreadables, 1)
		assert.Equal(t, filepath.Join(dir, "from-busy"), busy.Unreadables[0].Path)
		assert.Equal(t, 0, busy.Stat.Files)
		link := got[1]
		assert.Equal(t, 1, link.Stat.Unreadable)
		require.Len(t, link.Unreadables, 1)
		assert.Equal(t, filepath.Join(dir, "from-link", "link-000000000001.md"), link.Unreadables[0].Path)
		assert.Equal(t, 0, link.Stat.Files, "a file the lane could not read is not counted in files=")
		assert.Empty(t, link.Stream)
	})

	t.Run("a directory with no roster is one unreadable source", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		got := ReadBus(dir, os.DirFS(dir), busCoverRules(t), busCoverAt)
		require.Len(t, got, 1)
		s := got[0]
		assert.Equal(t, KindBus, s.Label)
		assert.Equal(t, KindBus, s.Kind)
		assert.Equal(t, dir, s.Path)
		assert.Equal(t, 1, s.Stat.Unreadable)
		assert.Equal(t, 0, s.Stat.Files)
		require.Len(t, s.Unreadables, 1)
		assert.Equal(t, filepath.Join(dir, "participants.json"), s.Unreadables[0].Path)
		assert.Empty(t, s.Stream)
	})

	t.Run("a roster that names no lane is refused with the shape it wants", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		bad := `{"participants":[{"name":"Ada","lane":"other-ada"}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(bad), 0o644))
		got := ReadBus(dir, os.DirFS(dir), busCoverRules(t), busCoverAt)
		require.Len(t, got, 1)
		require.Len(t, got[0].Unreadables, 1)
		assert.Contains(t, got[0].Unreadables[0].Why, "names no lane")
		assert.True(t, strings.Contains(got[0].Unreadables[0].Why, "participants.json"),
			"the refusal tells the reader where the roster was")
	})
}
