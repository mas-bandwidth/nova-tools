package bus

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemStore* tests run the Store contract on the in-memory transport.

func newMemStoreForTest(t *testing.T) Store {
	t.Helper()
	config := &Config{
		Participants: []Participant{
			{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"},
			{Name: "Bo", Lane: "from-bo", GitName: "Bo", GitEmail: "bo@example.com"},
		},
	}
	if err := config.validate(); err != nil {
		t.Fatalf("config validation failed: %v", err)
	}
	return NewMemStore(config)
}

func TestMemStorePublishThenSinceReturnsExactlyTheChangesPaths(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	note := noteText("Ada", "one", "body")
	c := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(note)},
		Index:   []IndexEntry{{ID: "ada-000000000001", Path: "from-ada/one.md", Lane: "from-ada"}},
		Message: "ada: note",
	}
	_, err := st.Publish(ctx, c)
	require.NoError(t, err)
	head, err := st.Head(ctx)
	require.NoError(t, err)
	paths, capped, err := st.Since(ctx, "", head, 0)
	require.NoError(t, err)
	assert.False(t, capped)
	assert.Contains(t, paths, "from-ada/one.md")
	assert.Contains(t, paths, "from-ada/INDEX")
}

func TestMemStoreNoteIsCreateOnly(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	note := noteText("Ada", "one", "body")
	c := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(note)},
		Message: "ada: note",
	}
	_, err := st.Publish(ctx, c)
	require.NoError(t, err)

	again, err := st.Publish(ctx, c)
	require.NoError(t, err)
	assert.True(t, again.Already, "a retry of the same bytes is Already")

	other := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "different body"))},
		Message: "ada: different",
	}
	_, err = st.Publish(ctx, other)
	assert.ErrorIs(t, err, ErrConflict)
}

func TestMemStoreRefusedPublishChangesNothing(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	note := noteText("Ada", "one", "body")
	c := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(note)},
		Message: "ada: note",
	}
	_, err := st.Publish(ctx, c)
	require.NoError(t, err)
	before, err := st.Head(ctx)
	require.NoError(t, err)

	other := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "different"))},
		Message: "ada: different",
	}
	_, err = st.Publish(ctx, other)
	require.ErrorIs(t, err, ErrConflict)

	after, err := st.Head(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestMemStoreAnotherLaneRefused(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	_, err := st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Message: "ada: note",
	})
	require.NoError(t, err)
	before, err := st.Head(ctx)
	require.NoError(t, err)

	_, err = st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-bo/other.md": []byte(noteText("Ada", "bo", "body"))},
		Message: "ada: wrong lane",
	})
	require.Error(t, err)

	after, err := st.Head(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestMemStoreTwoLanesBothLand(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	bo := Participant{Name: "Bo", Lane: "from-bo", GitName: "Bo", GitEmail: "bo@example.com"}

	_, err := st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Message: "ada: note",
	})
	require.NoError(t, err)

	_, err = st.Publish(ctx, Change{
		Author:  bo,
		Notes:   map[string][]byte{"from-bo/two.md": []byte(noteText("Bo", "two", "body"))},
		Message: "bo: note",
	})
	require.NoError(t, err)

	paths, _, err := st.Since(ctx, "", "", 0)
	require.NoError(t, err)
	assert.Contains(t, paths, "from-ada/one.md")
	assert.Contains(t, paths, "from-bo/two.md")
}

func TestMemStoreReadAtEarlierPosition(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	_, err := st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Message: "ada: one",
	})
	require.NoError(t, err)
	p, err := st.Head(ctx)
	require.NoError(t, err)

	_, err = st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/two.md": []byte(noteText("Ada", "two", "body"))},
		Message: "ada: two",
	})
	require.NoError(t, err)

	at, err := st.Read(ctx, p)
	require.NoError(t, err)
	_, err = fs.ReadFile(at, "from-ada/one.md")
	assert.NoError(t, err, "the earlier position still holds the first note")
	_, err = fs.ReadFile(at, "from-ada/two.md")
	assert.True(t, errors.Is(err, fs.ErrNotExist), "the earlier position does not hold the later note")
}

func TestMemStoreFindAgreesWithRead(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	c := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Index:   []IndexEntry{{ID: "ada-000000000001", Path: "from-ada/one.md", Lane: "from-ada"}},
		Message: "ada: note",
	}
	_, err := st.Publish(ctx, c)
	require.NoError(t, err)

	note, entry, found, err := st.Find(ctx, "ada-000000000001")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "from-ada/one.md", entry.Path)

	head, err := st.Head(ctx)
	require.NoError(t, err)
	at, err := st.Read(ctx, head)
	require.NoError(t, err)
	raw, err := fs.ReadFile(at, entry.Path)
	require.NoError(t, err)
	assert.Equal(t, raw, note)
}

func TestMemStoreRefreshWithNothingNew(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	_, err := st.Publish(ctx, Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Message: "ada: note",
	})
	require.NoError(t, err)

	_, moved, err := st.Refresh(ctx)
	require.NoError(t, err)
	assert.False(t, moved)
}

func TestMemStoreLostReplyRetryIsAlready(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	c := Change{
		Author:  ada,
		Notes:   map[string][]byte{"from-ada/one.md": []byte(noteText("Ada", "one", "body"))},
		Message: "ada: note",
	}
	_, err := st.Publish(ctx, c)
	require.NoError(t, err)

	retry, err := st.Publish(ctx, c)
	require.NoError(t, err)
	assert.True(t, retry.Already)
}

func TestMemStoreSinceUnknown(t *testing.T) {
	t.Parallel()
	st := newMemStoreForTest(t)
	ctx := context.Background()

	_, _, err := st.Since(ctx, Position("0000000000000000000000000000000000000000"), "", 10)
	assert.ErrorIs(t, err, ErrUnknownPosition)

	ada := Participant{Name: "Ada", Lane: "from-ada", GitName: "Ada", GitEmail: "ada@example.com"}
	names := []string{"from-ada/one.md", "from-ada/two.md", "from-ada/three.md"}
	for i, name := range names {
		c := Change{
			Author:  ada,
			Notes:   map[string][]byte{name: []byte(noteText("Ada", "note", "body"))},
			Index:   []IndexEntry{{ID: "ada-00000000000" + string(rune('1'+i)), Path: name, Lane: "from-ada"}},
			Message: "ada: note",
		}
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
	}
	from, _, err := st.Since(ctx, "", "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, from)

	cursor, err := st.Head(ctx)
	require.NoError(t, err)
	for i := range names {
		c := Change{
			Author:  ada,
			Notes:   map[string][]byte{"from-ada/more" + string(rune('a'+i)) + ".md": []byte(noteText("Ada", "more", "body"))},
			Index:   []IndexEntry{{ID: "ada-00000000001" + string(rune('1'+i)), Path: "from-ada/more" + string(rune('a'+i)) + ".md", Lane: "from-ada"}},
			Message: "ada: more",
		}
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
	}

	paths, capped, err := st.Since(ctx, cursor, "", 1)
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	assert.True(t, capped, "a limit below the commit count sets capped")
}
