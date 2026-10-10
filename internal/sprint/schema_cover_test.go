package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The schema (schema.go): the four tables' definitions and the key, id and
// identity helpers. Every function here reads only its arguments, so these
// tests are pure: no store, no clock, no subprocess.

func TestSchemaCoverFriendsDef(t *testing.T) {
	t.Parallel()
	def := FriendsDef()
	assert.Equal(t, Friends, def.Name)
	assert.Equal(t, []string{DoneOK, DoneFailed, Refused, Provider}, def.Hidden, "ok, failed, refused and provider are kept but not drawn")
	var names []string
	for _, c := range def.Columns {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"ready", "working", "width", Done, OkPct, Status, Active, Tokens, DoneOK, DoneFailed, Refused, Provider}, names)
}

func TestSchemaCoverNamesTable(t *testing.T) {
	t.Parallel()
	n := Names{Prefix: "pre-"}
	assert.Equal(t, "pre-work", n.Table(Work))
	assert.Equal(t, "pre-sprint", n.View())
	assert.Equal(t, "pre-sprint:w:", n.MemberPrefix(Work))
	assert.Equal(t, "pre-sprint:r:", n.MemberPrefix(Readers))
	assert.Equal(t, "pre-sprint:m:", n.MemberPrefix(Merge))
	assert.Equal(t, "pre-sprint:f:", n.MemberPrefix(Fleet))
	assert.Equal(t, "pre-sprint:w:p.w1", n.RecordKey(Work, "p.w1"))
	assert.Equal(t, "pre-sprint:fence", n.Key("fence"))
	assert.Equal(t, "pre-sprint:epoch", n.EpochKey())
	assert.Equal(t, Work, n.Logical(n.Table(Work)))
	assert.Equal(t, "other", n.Logical("other"), "a name without the prefix is left alone")
}

func TestSchemaCoverKeyAtAndStoredID(t *testing.T) {
	t.Parallel()
	n := Names{Prefix: "pre-"}
	assert.Equal(t, "pre-sprint:inbox", n.KeyAt("inbox", 0), "epoch 0 is the key itself")
	assert.Equal(t, "pre-sprint:inbox@7", n.KeyAt("inbox", 7), "a later epoch follows the key")
	assert.Equal(t, "p.w1", StoredID("p.w1", 0), "epoch 0 is the id itself")
	assert.Equal(t, "p.w1~7", StoredID("p.w1", 7), "a later epoch follows the id")
	assert.Equal(t, "fam~4", OpFamily("fam", 4), "an operation family is a stored id")
	assert.Equal(t, StoredID("fam", 0), OpFamily("fam", 0))
}

func TestSchemaCoverIDEpoch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		id   string
		want uint64
	}{
		{"no ~ is epoch 0", "p.w1", 0},
		{"the number after the last ~", "p.w1~7", 7},
		{"a family id", "fam~4", 4},
		{"a leading ~ is not a separator", "~3", 3},
		{"nothing after ~", "x~", 0},
		{"not a number after ~", "x~abc", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, IDEpoch(c.id), "IDEpoch(%q)", c.id)
		})
	}
}

func TestSchemaCoverOtherEpoch(t *testing.T) {
	t.Parallel()
	future := OtherEpoch("j~9", 9, 4)
	assert.Contains(t, future, "judgment j~9 belongs to epoch 9")
	assert.Contains(t, future, "unknown to this sprint")
	assert.Contains(t, future, "nothing was changed")

	closed := OtherEpoch("j~2", 2, 5)
	assert.Contains(t, closed, "judgment j~2 belongs to epoch 2")
	assert.Contains(t, closed, "a clear closed it")
	assert.Contains(t, closed, "nothing was changed")
}

func TestSchemaCoverNoJudgment(t *testing.T) {
	t.Parallel()
	t.Run("another epoch names the epoch", func(t *testing.T) {
		t.Parallel()
		got := noJudgment(&Snapshot{Epoch: 5}, "j~3")
		assert.Contains(t, got, "a clear closed it")
	})
	t.Run("a stale stream names its own remedy", func(t *testing.T) {
		t.Parallel()
		got := noJudgment(&Snapshot{Epoch: 5}, "stale:s1~5")
		assert.Contains(t, got, "stale:s1~5 is a stream that has not moved")
		assert.Contains(t, got, "nova-sprint wait stale:s1~5 --for 30m")
	})
	t.Run("no such judgment", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "no open judgment j1; run: nova-sprint inbox", noJudgment(&Snapshot{Epoch: 0}, "j1"))
	})
}

func TestSchemaCoverCardID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "p.w1", CardID("p.w1~7"), "the epoch after the last ~ is dropped")
	assert.Equal(t, "p.w1", CardID("p.w1"), "no ~ is the id itself")
	assert.Equal(t, "~7", CardID("~7"), "a leading ~ is not a separator")
}

func TestSchemaCoverDefinitions(t *testing.T) {
	t.Parallel()
	defs := Names{Prefix: "pre-"}.Definitions()
	require.Len(t, defs, 4, "the four tables, in the stored view's order")
	want := []struct {
		name   string
		prefix string
		hidden []string
	}{
		{"pre-work", "pre-sprint:w:", nil},
		{"pre-readers", "pre-sprint:r:", nil},
		{"pre-merge", "pre-sprint:m:", []string{Since, Returned, Ctl}},
		{"pre-fleet", "pre-sprint:f:", []string{Withdrawn, Refused, Provider, DoneOK, DoneFailed, DoneDefect, Ctl}},
	}
	for i, w := range want {
		t.Run(w.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, w.name, defs[i].Name)
			assert.Equal(t, w.prefix, defs[i].MemberPrefix)
			assert.Equal(t, w.hidden, defs[i].Hidden)
			assert.Equal(t, "pre-sprint:epoch", defs[i].EpochKey)
			assert.Equal(t, "n", defs[i].EpochField)
			assert.NotEmpty(t, defs[i].Columns)
		})
	}
}

func TestSchemaCoverViewDef(t *testing.T) {
	t.Parallel()
	v := Names{Prefix: "pre-"}.ViewDef()
	assert.Equal(t, "pre-sprint", v.Name)
	assert.Equal(t, "SPRINT TABLE", v.Title)
	assert.Equal(t, Landed, v.Summary)
	assert.Equal(t, []string{"pre-work", "pre-readers", "pre-merge", "pre-fleet"}, v.Tables)
}

func TestSchemaCoverValidCardID(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"a primary", "p", true},
		{"a work card", "p.w1", true},
		{"a read card", "p.r1.reader-a", true},
		{"more than three parts", "a.b.c.d", false},
		{"an empty part", "p..w1", false},
		{"a part is not an id word", "p.w 1", false},
		{"nothing", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, ValidCardID(c.id), "ValidCardID(%q)", c.id)
		})
	}
}

func TestSchemaCoverParseCards(t *testing.T) {
	t.Parallel()
	t.Run("a work card's refusals", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{"p", "p.w", "p.w0", "p.wx", ".w1"} {
			_, _, ok := ParseWorkCard(id)
			assert.False(t, ok, "ParseWorkCard(%q)", id)
		}
		p, n, ok := ParseWorkCard("p.w2")
		require.True(t, ok)
		assert.Equal(t, "p", p)
		assert.Equal(t, 2, n)
	})
	t.Run("a read card's refusals", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{"p", "p.r1", "p.x1.reader", "p.r0.reader", "p.r1.reader.extra", "p.rx.reader"} {
			_, _, _, ok := ParseReadCard(id)
			assert.False(t, ok, "ParseReadCard(%q)", id)
		}
		p, n, r, ok := ParseReadCard("p.r3.reader-a")
		require.True(t, ok)
		assert.Equal(t, "p", p)
		assert.Equal(t, 3, n)
		assert.Equal(t, "reader-a", r)
	})
}
