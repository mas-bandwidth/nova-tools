package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settingsSnapshot is the observed state Set reads: a sprint whose coordinator
// is coord, the work table's properties set as given, and each named stream's
// control card placed on the merge table with a read tier of its own to take off.
func settingsSnapshot(props map[string]string, streams ...string) *Snapshot {
	s := &Snapshot{Coordinator: "coord", Work: NewTable(Work), Merge: NewTable(Merge)}
	s.Work.SetProps(props)
	for _, st := range streams {
		s.Merge.Put(&Card{ID: CtlID(st), Row: st, Col: Ctl, Rev: 2, Fields: map[string]string{FieldReadTier: "pro"}})
	}
	return s
}

func TestSettingsCoverStrongerHoldsTheStrongerTier(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ a, b, want string }{
		{"flash", "pro", "pro"}, // the stronger named second
		{"pro", "flash", "pro"}, // the stronger named first, kept
		{"pro", "pro", "pro"},   // equal: the first
		{"flash", "flash", "flash"},
	} {
		t.Run(c.a+" then "+c.b, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, stronger(c.a, c.b), "stronger(%q, %q)", c.a, c.b)
		})
	}
}

func TestSettingsCoverOrDefaultSaysWhatDefaultIs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		v, own string // the setting's value, and the property named
		want   string
	}{
		{"a value is said as it is: the dealt bound", "6h", PropDealtMax, "6h"},
		{"a value is said as it is: the read tier", "pro", PropReadTier, "pro"},
		{"default on the dealt bound: the bound default", ReadTierDefault, PropDealtMax,
			"default (" + DealtMaxDefault.String() + ", 3 times the take deadline)"},
		{"default on the read tier: each card's own tier", ReadTierDefault, PropReadTier, "default (each card's own tier)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, orDefault(c.v, c.own))
		})
	}
}

func TestSettingsCoverSetWritesAndRefusesWhole(t *testing.T) {
	t.Parallel()
	t.Run("the sprint's read tier and dealt bound, each with the value it was read at", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(map[string]string{PropReadTier: "flash"}, "s1"),
			SetReq{ReadTier: "pro", DealtMax: "90m", Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Props, 2, "the two settings, guarded on what was read")
		assert.Equal(t, PropWrite{Table: Work, Name: PropReadTier, Value: "pro", Was: "flash"}, p.Props[0])
		assert.Equal(t, PropWrite{Table: Work, Name: PropDealtMax, Value: "90m", WasAbsent: true}, p.Props[1])
		require.Len(t, p.Units, 1)
		assert.Equal(t, "set", p.Units[0].Key)
		assert.Equal(t, "sprint read-tier pro, dealt-max 90m", p.Units[0].Moved)
		assert.Empty(t, p.Units[0].Changes, "the sprint's settings are properties, not a card's fields")
	})
	t.Run("one setting only: the empty one is skipped, left as it is", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(nil, "s1"), SetReq{DealtMax: "2h", Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Props, 1, "the read tier named none, so none is written")
		assert.Equal(t, PropWrite{Table: Work, Name: PropDealtMax, Value: "2h", WasAbsent: true}, p.Props[0])
		assert.Equal(t, "sprint dealt-max 2h", p.Units[0].Moved)
	})
	t.Run("a stream's read tier is its control card's field", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(nil, "s1"), SetReq{Streams: []string{"s1"}, ReadTier: "flash", Who: "coord"})
		require.Empty(t, p.Refused)
		assert.Empty(t, p.Props, "a stream's setting is no property of the sprint's")
		require.Len(t, p.Units, 1)
		u := p.Units[0]
		assert.Equal(t, CtlID("s1"), u.Key)
		assert.Equal(t, "s1", u.Stream)
		assert.Equal(t, "stream s1 read-tier flash", u.Moved)
		require.Len(t, u.Changes, 1)
		assert.Equal(t, Merge, u.Changes[0].Table)
		assert.Equal(t, CtlID("s1"), u.Changes[0].Entry.ID)
		assert.Equal(t, map[string]string{FieldReadTier: "flash"}, u.Changes[0].Entry.Set)
	})
	t.Run("a stream's default takes its read tier off", func(t *testing.T) {
		t.Parallel()
		p := Set(settingsSnapshot(nil, "s1"), SetReq{Streams: []string{"s1"}, ReadTier: ReadTierDefault, Who: "coord"})
		require.Empty(t, p.Refused)
		require.Len(t, p.Units, 1)
		assert.Equal(t, "stream s1 read-tier the sprint's", p.Units[0].Moved)
		require.Len(t, p.Units[0].Changes, 1)
		assert.Nil(t, p.Units[0].Changes[0].Entry.Set)
		assert.Equal(t, []string{FieldReadTier}, p.Units[0].Changes[0].Entry.Unset, "the field is unset, not written over")
	})
	t.Run("a refusal names every problem and writes nothing", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name string
			req  SetReq
			why  []string
		}{
			{"not the coordinator", SetReq{ReadTier: "pro", Who: "someone"}, []string{"is the coordinator's alone: coord"}},
			{"a read tier that is not a tier, and a dealt bound that is not a duration, named together",
				SetReq{ReadTier: "ultra", DealtMax: "bogus", Who: "coord"}, []string{"wants flash or pro or heavy, or default", "wants a duration above zero"}},
			{"nothing to set", SetReq{Who: "coord"}, []string{"nothing to set"}},
			{"a dealt bound with streams named", SetReq{Streams: []string{"s1"}, DealtMax: "6h", Who: "coord"},
				[]string{"--dealt-max is the sprint's, not a stream's"}},
			{"a stream that is not a stream", SetReq{Streams: []string{"nope"}, ReadTier: "pro", Who: "coord"}, []string{"no stream nope"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				p := Set(settingsSnapshot(nil, "s1"), c.req)
				require.Len(t, p.Refused, 1, "refused whole: %+v", p)
				assert.Equal(t, "set", p.Refused[0].Key)
				for _, w := range c.why {
					assert.Contains(t, p.Refused[0].Why, w)
				}
				assert.Empty(t, p.Units, "a refusal writes nothing")
				assert.Empty(t, p.Props, "a refusal writes nothing")
			})
		}
	})
}
