package bus

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemStoreContract runs the Store contract on the in-memory bus. It is the unit tier,
// with no build tag: every property passes here and on gitStore, which is the proof the
// fake is strict. The design is LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a.
func TestMemStoreContract(t *testing.T) {
	t.Parallel()
	runStoreContract(t, func(t *testing.T) Store {
		t.Helper()
		return NewMemStore()
	})
}

// TestMemStorePublishesAtM1AndReadIsAMapFS pins the position names and the Read tree.
func TestMemStorePublishesAtM1AndReadIsAMapFS(t *testing.T) {
	t.Parallel()
	st := NewMemStore()
	who := Participant{Name: "A", Lane: "from-a", GitName: "A", GitEmail: "a@example.com"}
	_, err := st.Publish(context.Background(), Change{
		Author:  who,
		Notes:   map[string][]byte{"from-a/one.md": []byte("one")},
		Message: "one",
	})
	require.NoError(t, err)
	head, err := st.Head(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Position("m1"), head)
	at, err := st.Read(context.Background(), head)
	require.NoError(t, err)
	_, ok := at.(fstest.MapFS)
	assert.True(t, ok, "Read returns an fstest.MapFS")
	_, err = st.Publish(context.Background(), Change{
		Author:  who,
		Notes:   map[string][]byte{"from-a/two.md": []byte("two")},
		Message: "two",
	})
	require.NoError(t, err)
	head, err = st.Head(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Position("m2"), head)
}

// TestMemStoreRefusesAnIndexLineWithNoNote pins the catalogue refusal: an INDEX line whose
// path is not a note in the change and not a note already on the bus is refused, and the
// head does not move.
func TestMemStoreRefusesAnIndexLineWithNoNote(t *testing.T) {
	t.Parallel()
	st := NewMemStore()
	who := Participant{Name: "A", Lane: "from-a", GitName: "A", GitEmail: "a@example.com"}
	_, err := st.Publish(context.Background(), Change{
		Author:  who,
		Index:   []IndexEntry{{ID: "a-000000000001", Path: "from-a/missing.md", Lane: "from-a"}},
		Message: "index",
	})
	require.Error(t, err)
	head, err := st.Head(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Position("m0"), head)
}

// TestMemStoreRefusesALanelessAuthor pins the sender refusal: a participant with no lane
// has nowhere to publish, and nothing is written.
func TestMemStoreRefusesALanelessAuthor(t *testing.T) {
	t.Parallel()
	st := NewMemStore()
	_, err := st.Publish(context.Background(), Change{
		Author:  Participant{Name: "A"},
		Notes:   map[string][]byte{"from-a/one.md": []byte("one")},
		Message: "note",
	})
	require.Error(t, err)
	head, err := st.Head(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Position("m0"), head)
}

// TestMemStoreFailHookLostReplyIsAlreadyOnRetry pins the lost-reply injection: Fail returns
// an error between Refresh and the record, the change is on the bus, and the retry is Already.
func TestMemStoreFailHookLostReplyIsAlreadyOnRetry(t *testing.T) {
	t.Parallel()
	st := NewMemStore()
	var calls int
	st.Fail = func(step string) error {
		calls++
		assert.Equal(t, "publish", step)
		if calls == 1 {
			return errors.New("lost reply")
		}
		return nil
	}
	who := Participant{Name: "A", Lane: "from-a", GitName: "A", GitEmail: "a@example.com"}
	c := Change{Author: who, Notes: map[string][]byte{"from-a/one.md": []byte("one")}, Message: "one"}
	_, err := st.Publish(context.Background(), c)
	require.EqualError(t, err, "lost reply")
	head, err := st.Head(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Position("m1"), head, "the lost reply still recorded the change")
	again, err := st.Publish(context.Background(), c)
	require.NoError(t, err)
	assert.True(t, again.Already)
}

// TestMemStoreFailHookSeesAnotherBenchPublish pins the other injection: between Refresh and
// the record another bench publishes, and the further cursor wins.
func TestMemStoreFailHookSeesAnotherBenchPublish(t *testing.T) {
	t.Parallel()
	a := NewMemStore()
	who := Participant{Name: "A", Lane: "from-a", GitName: "A", GitEmail: "a@example.com"}
	_, err := a.Publish(context.Background(), Change{
		Author:  who,
		Notes:   map[string][]byte{"from-a/one.md": []byte("one")},
		Message: "one",
	})
	require.NoError(t, err)
	ahead, err := a.Head(context.Background())
	require.NoError(t, err)
	b := a.Peer()
	b.Fail = func(step string) error {
		assert.Equal(t, "publish", step)
		_, err := a.Publish(context.Background(), Change{
			Author:  who,
			Cursor:  &Cursor{Commit: string(ahead)},
			Message: "ahead",
		})
		require.NoError(t, err)
		return nil
	}
	_, err = b.Publish(context.Background(), Change{
		Author:  who,
		Cursor:  &Cursor{Commit: "m0"},
		Message: "behind",
	})
	require.NoError(t, err)
	final, _, err := b.Refresh(context.Background())
	require.NoError(t, err)
	at, err := b.Read(context.Background(), final)
	require.NoError(t, err)
	raw, err := fs.ReadFile(at, CursorPath("from-a"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), string(ahead))
	assert.NotContains(t, string(raw), "m0")
}
