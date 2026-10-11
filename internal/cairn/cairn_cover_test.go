// Unit coverage for the plan and refusal paths the per-function coverage
// table showed at zero: ConflictError.Error, PlanOpen and PlanAppend.
// Everything runs in-process over plain files in t.TempDir() with clock
// readings passed in: no sleeps, no real time, no network, no subprocess, no
// Redis or Postgres.
package cairn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCairnCoverConflictErrorMessage(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		err  *ConflictError
		want string
	}{
		{
			name: "the message stands alone; the remedy is never rendered",
			err: &ConflictError{
				Msg:    `entry "e-1" already holds different prose; append these words under a new --entry id, or read what it holds`,
				Remedy: "nova-cairn receipt --store st --session s --entry e-1 --text",
			},
			want: `entry "e-1" already holds different prose; append these words under a new --entry id, or read what it holds`,
		},
		{
			name: "an empty message renders empty, never the remedy",
			err:  &ConflictError{Remedy: "nova-cairn receipt --store st --session s --entry e-1 --text"},
			want: "",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.err.Error(), "ConflictError.Error must return Msg and nothing else")
		})
	}
}

func TestCairnCoverPlanOpen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

	t.Run("a fresh open plans its own record and writes nothing", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		rec, err := PlanOpen(store, "s", "bench-a/session-7", now, PublishManual)
		require.NoError(t, err, "PlanOpen")
		assert.Equal(t, OpenRecord{Source: "bench-a/session-7", Publish: PublishManual, Found: true}, rec,
			"the plan is the open's own: the source, the policy, found")
		assert.NoFileExists(t, filepath.Join(store, "sessions", "s.md"), "the plan must write no session record")
		assert.NoFileExists(t, filepath.Join(store, "log.jsonl"), "the plan must write no log line")
	})

	t.Run("a re-open naming the recorded policy changes nothing and writes nothing", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "src", now, PublishManual), "Open")
		rec, err := PlanOpen(store, "s", "src", now, PublishManual)
		require.NoError(t, err, "PlanOpen")
		assert.Equal(t, OpenRecord{Source: "src", Publish: PublishManual, Opened: now, Found: true}, rec)
		raw, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
		require.NoError(t, err, "ReadFile log.jsonl")
		assert.Equal(t, 1, len(strings.Split(strings.TrimSpace(string(raw)), "\n")), "the plan must add no log line")
	})

	t.Run("a re-open naming another policy is a conflict that still names the record standing", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "src", now, PublishManual), "Open")
		rec, err := PlanOpen(store, "s", "", now, PublishNever)
		require.Error(t, err, "PlanOpen must refuse another policy")
		var ce *ConflictError
		assert.ErrorAs(t, err, &ce, "the refusal must be the caller's conflict, not a crash")
		assert.Equal(t, OpenRecord{Source: "src", Publish: PublishManual, Opened: now, Found: true}, rec)
	})

	t.Run("a flat record plans with no open record behind it", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		testkit.WriteFile(t, filepath.Join(store, "byhand.md"), "# written by hand\n")
		rec, err := PlanOpen(store, "byhand", "", now, PublishManual)
		require.NoError(t, err, "PlanOpen")
		assert.False(t, rec.Found, "a flat record stores no open record")
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		rows := []struct {
			name, store, session, publish, want string
		}{
			{name: "no store is refused, never guessed", store: "", session: "s", publish: PublishManual, want: "no store given"},
			{name: "a session id that is not one path component is refused", store: store, session: "a/b", publish: PublishManual, want: `bad session id "a/b"`},
			{name: "a publish policy outside Policies is refused naming them", store: store, session: "s", publish: "sometimes", want: `bad publish policy "sometimes"`},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				_, err := PlanOpen(row.store, row.session, "src", now, row.publish)
				require.Error(t, err, "PlanOpen must refuse")
				assert.Contains(t, err.Error(), row.want)
			})
		}
	})
}

func TestCairnCoverPlanAppend(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 4, 9, 30, 5, 123456789, time.UTC)

	t.Run("a fresh plan carries the stamp, the inherited source and policy, and writes nothing", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "bench-a/session-7", now, PublishManual), "Open")
		res, err := PlanAppend(store, "s", "e", "the friend's words", "", now, "")
		require.NoError(t, err, "PlanAppend")
		assert.True(t, res.Stamp.Equal(now), "the plan stamps the caller's clock reading, got %v", res.Stamp)
		assert.Equal(t, PublishManual, res.Policy, "an empty publish carries the session's recorded policy")
		assert.Equal(t, "bench-a/session-7", res.Source, "an empty source carries the session's pointer")
		assert.False(t, res.Persisted, "words not yet stored are not persisted, got %+v", res)
		assert.False(t, res.Duplicate, "a fresh plan is no duplicate, got %+v", res)
		assert.NoFileExists(t, entryPath(store, "s", "e"), "the plan must write no entry file")
		log, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
		require.NoError(t, err, "ReadFile log.jsonl")
		assert.Equal(t, 1, len(strings.Split(strings.TrimSpace(string(log)), "\n")), "the plan must add no log line")
		record, err := os.ReadFile(sessionFile(store, "s"))
		require.NoError(t, err, "ReadFile session record")
		assert.NotContains(t, string(record), "ENTRY ", "the plan must add no pointer line")
	})

	t.Run("a plan of words already stored reports them stored and adds nothing", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "src", now, PublishNever), "Open")
		_, err := Append(store, "s", "e", "stored words", "", now, PublishNever)
		require.NoError(t, err, "Append")
		res, err := PlanAppend(store, "s", "e", "stored words", "", now, PublishNever)
		require.NoError(t, err, "PlanAppend")
		assert.True(t, res.Duplicate, "the words are already stored, got %+v", res)
		assert.True(t, res.Persisted, "a duplicate reports the words durable, got %+v", res)
	})

	t.Run("a plan of other words under a stored id is refused through ConflictError", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "src", now, PublishNever), "Open")
		_, err := Append(store, "s", "e", "stored words", "", now, PublishNever)
		require.NoError(t, err, "Append")
		_, err = PlanAppend(store, "s", "e", "other words", "", now, PublishNever)
		require.Error(t, err, "PlanAppend must refuse a conflicting id")
		var ce *ConflictError
		assert.ErrorAs(t, err, &ce, "the refusal must be the caller's conflict, not a crash")
		assert.Contains(t, err.Error(), `entry "e" already holds different prose`,
			"ConflictError.Error renders the message the caller sees, never the remedy")
		stored, err := EntryText(store, "s", "e")
		require.NoError(t, err, "EntryText after the refused plan")
		assert.Equal(t, "stored words", stored, "the refusal must not overwrite the stored entry")
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		require.NoError(t, Open(store, "s", "src", now, PublishManual), "Open")
		rows := []struct {
			name, store, session, id, text, want string
		}{
			{name: "no store is refused, never guessed", store: "", session: "s", id: "e", text: "words", want: "no store given"},
			{name: "a use before open is a not-found naming the remedy", store: store, session: "ghost", id: "e", text: "words", want: `no such session "ghost"`},
			{name: "empty text stores nothing", store: store, session: "s", id: "e", text: "", want: "empty note stores nothing"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				_, err := PlanAppend(row.store, row.session, row.id, row.text, "", now, PublishManual)
				require.Error(t, err, "PlanAppend must refuse")
				assert.Contains(t, err.Error(), row.want)
			})
		}
	})
}
