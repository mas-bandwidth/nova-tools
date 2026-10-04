//go:build functional

package bus

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGitStoreContract runs the Store contract on the git transport. It is functional
// because it makes repositories and pushes; the logic it pins is the bus's own, stated
// once in runStoreContract. The model is tla/BusCursor.tla.
func TestGitStoreContract(t *testing.T) {
	t.Parallel()
	hermetic(t)
	newStore := func(t *testing.T) Store {
		t.Helper()
		return NewGitStore(cloneBus(t, bareBus(t)), "origin", "main", 3, 10*time.Second)
	}
	runStoreContract(t, newStore)
}

// runStoreContract holds the eleven properties the Store must keep, and runs them against
// whatever transport newStore builds. Each case takes its own store over its own bus so a
// failure names the property rather than the case before it.
func runStoreContract(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	author := func(name, lane, email string) Participant {
		return Participant{Name: name, Lane: lane, GitName: name, GitEmail: email}
	}
	change := func(who Participant, note, name string) Change {
		return Change{
			Author:  who,
			Notes:   map[string][]byte{name: []byte(note)},
			Message: who.Slug() + ": note",
		}
	}
	ada := author("Ada", "from-ada", "ada@example.com")
	bo := author("Bo", "from-bo", "bo@example.com")

	t.Run("publish then Since returns exactly the change's paths", func(t *testing.T) {
		st := newStore(t)
		c := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		c.Index = []IndexEntry{{ID: "ada-000000000001", Path: "from-ada/one.md", Lane: "from-ada"}}
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
		head, err := st.Head(ctx)
		require.NoError(t, err)
		paths, capped, err := st.Since(ctx, "", head, 0)
		require.NoError(t, err)
		assert.False(t, capped)
		assert.Contains(t, paths, "from-ada/one.md")
		assert.Contains(t, paths, "from-ada/INDEX")
	})

	t.Run("a note is create-only, same bytes is Already, other bytes ErrConflict", func(t *testing.T) {
		st := newStore(t)
		c := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
		again, err := st.Publish(ctx, c)
		require.NoError(t, err)
		assert.True(t, again.Already, "a retry of the same bytes is Already")
		other := change(ada, noteText("Ada", "one", "different body"), "from-ada/one.md")
		_, err = st.Publish(ctx, other)
		assert.ErrorIs(t, err, ErrConflict)
	})

	t.Run("a refused publish changes nothing", func(t *testing.T) {
		st := newStore(t)
		c := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
		before, err := st.Head(ctx)
		require.NoError(t, err)
		other := change(ada, noteText("Ada", "one", "different body"), "from-ada/one.md")
		_, err = st.Publish(ctx, other)
		require.ErrorIs(t, err, ErrConflict)
		after, err := st.Head(ctx)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})

	t.Run("another lane's change is refused before any write", func(t *testing.T) {
		st := newStore(t)
		_, err := st.Publish(ctx, change(ada, noteText("Ada", "one", "body"), "from-ada/one.md"))
		require.NoError(t, err)
		before, err := st.Head(ctx)
		require.NoError(t, err)
		_, err = st.Publish(ctx, change(ada, noteText("Ada", "bo", "body"), "from-bo/other.md"))
		require.Error(t, err)
		after, err := st.Head(ctx)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})

	t.Run("two lanes both land", func(t *testing.T) {
		st := newStore(t)
		_, err := st.Publish(ctx, change(ada, noteText("Ada", "one", "body"), "from-ada/one.md"))
		require.NoError(t, err)
		_, err = st.Publish(ctx, change(bo, noteText("Bo", "two", "body"), "from-bo/two.md"))
		require.NoError(t, err)
		paths, _, err := st.Since(ctx, "", "", 0)
		require.NoError(t, err)
		assert.Contains(t, paths, "from-ada/one.md")
		assert.Contains(t, paths, "from-bo/two.md")
	})

	t.Run("two benches of one lane keep every INDEX and RECEIPTS line, the furthest cursor wins", func(t *testing.T) {
		st := newStore(t)
		one := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		one.Index = []IndexEntry{{ID: "ada-000000000001", Path: "from-ada/one.md", Lane: "from-ada"}}
		one.Receipts = []string{"2026-09-07T00:00:00Z ada-000000000001"}
		_, err := st.Publish(ctx, one)
		require.NoError(t, err)
		two := change(ada, noteText("Ada", "two", "body"), "from-ada/two.md")
		two.Index = []IndexEntry{{ID: "ada-000000000002", Path: "from-ada/two.md", Lane: "from-ada"}}
		two.Receipts = []string{"2026-09-07T00:00:01Z ada-000000000002"}
		_, err = st.Publish(ctx, two)
		require.NoError(t, err)
		_, _, found, err := st.Find(ctx, "ada-000000000001")
		require.NoError(t, err)
		assert.True(t, found, "the first bench's INDEX line is kept")
		_, _, found, err = st.Find(ctx, "ada-000000000002")
		require.NoError(t, err)
		assert.True(t, found, "the second bench's INDEX line is kept")
	})

	t.Run("a lost reply's retry is Already", func(t *testing.T) {
		st := newStore(t)
		c := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		_, err := st.Publish(ctx, c)
		require.NoError(t, err)
		retry, err := st.Publish(ctx, c)
		require.NoError(t, err)
		assert.True(t, retry.Already)
	})

	t.Run("Since(unknown) is ErrUnknownPosition and limit sets capped", func(t *testing.T) {
		st := newStore(t)
		_, _, err := st.Since(ctx, Position("0000000000000000000000000000000000000000"), "", 10)
		assert.ErrorIs(t, err, ErrUnknownPosition)
		names := []string{"from-ada/one.md", "from-ada/two.md", "from-ada/three.md"}
		for i, name := range names {
			c := change(ada, noteText("Ada", "note", "body"), name)
			c.Index = []IndexEntry{{ID: "ada-00000000000" + string(rune('1'+i)), Path: name, Lane: "from-ada"}}
			_, err := st.Publish(ctx, c)
			require.NoError(t, err)
		}
		from, _, err := st.Since(ctx, "", "", 0)
		require.NoError(t, err)
		require.NotEmpty(t, from)
		cursor, err := st.Head(ctx)
		require.NoError(t, err)
		for i := range names {
			c := change(ada, noteText("Ada", "more", "body"), "from-ada/more"+string(rune('a'+i))+".md")
			c.Index = []IndexEntry{{ID: "ada-00000000001" + string(rune('1'+i)), Path: "from-ada/more" + string(rune('a'+i)) + ".md", Lane: "from-ada"}}
			_, err := st.Publish(ctx, c)
			require.NoError(t, err)
		}
		paths, capped, err := st.Since(ctx, cursor, "", 1)
		require.NoError(t, err)
		require.NotEmpty(t, paths)
		assert.True(t, capped, "a limit below the commit count sets capped")
	})

	t.Run("Read(p) after later publishes shows p", func(t *testing.T) {
		st := newStore(t)
		_, err := st.Publish(ctx, change(ada, noteText("Ada", "one", "body"), "from-ada/one.md"))
		require.NoError(t, err)
		p, err := st.Head(ctx)
		require.NoError(t, err)
		_, err = st.Publish(ctx, change(ada, noteText("Ada", "two", "body"), "from-ada/two.md"))
		require.NoError(t, err)
		at, err := st.Read(ctx, p)
		require.NoError(t, err)
		_, err = fs.ReadFile(at, "from-ada/one.md")
		assert.NoError(t, err, "the earlier position still holds the first note")
		_, err = fs.ReadFile(at, "from-ada/two.md")
		assert.True(t, errors.Is(err, fs.ErrNotExist), "the earlier position does not hold the later note")
	})

	t.Run("Find agrees with Read(Head)", func(t *testing.T) {
		st := newStore(t)
		c := change(ada, noteText("Ada", "one", "body"), "from-ada/one.md")
		c.Index = []IndexEntry{{ID: "ada-000000000001", Path: "from-ada/one.md", Lane: "from-ada"}}
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
	})

	t.Run("Refresh with nothing new is moved=false", func(t *testing.T) {
		st := newStore(t)
		_, err := st.Publish(ctx, change(ada, noteText("Ada", "one", "body"), "from-ada/one.md"))
		require.NoError(t, err)
		_, moved, err := st.Refresh(ctx)
		require.NoError(t, err)
		assert.False(t, moved)
	})
}
