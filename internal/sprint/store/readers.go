package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A reader's presence (sprint/readers.go; docs/SPEC-SPRINT.md section 6): two
// records per reader under the deployment's prefix, outside the tables and the
// fence as a member's beat is: reader-beat:<reader>, written by the reader's
// own queue, and reader-away:<reader>, the coordinator's hold, written by
// reader away and emptied by reader up.

func readerBeatKey(reader string) string { return "reader-beat:" + reader }
func readerAwayKey(reader string) string { return "reader-away:" + reader }

// readerHold is the coordinator's hold of a reader away: empty while released.
type readerHold struct {
	Away bool      `json:"away,omitempty"`
	At   time.Time `json:"at,omitempty"`
	By   string    `json:"by,omitempty"`
}

// ReaderRows is the readers table's rows, in table order.
func (st *Store) ReaderRows(ctx context.Context) ([]string, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	shapes, err := pinned.B.Shapes(ctx, []string{pinned.Names.Table(sprint.Readers)})
	if refusalCode(err) == "NOTABLE" {
		return nil, nil // no sprint yet: no reader
	}
	if err != nil || len(shapes) == 0 {
		return nil, err
	}
	var rows []string
	for _, r := range shapes[0].Rows {
		rows = append(rows, r.Key)
	}
	return rows, nil
}

// ReaderBeat writes one beat of reader at the store's clock, to the second,
// when the readers table has its row; it says whether it wrote. It touches no
// table: the reader's own queue is its beat.
func (st *Store) ReaderBeat(ctx context.Context, reader string) (bool, error) {
	rows, err := st.ReaderRows(ctx)
	if err != nil || !contains(rows, reader) {
		return false, err
	}
	return true, st.beatReaders(ctx, reader)
}

// BeatReaders is a beat of every reader of the readers table: the readers of
// a twin are this one process's.
func (st *Store) BeatReaders(ctx context.Context) error {
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return err
	}
	return st.beatReaders(ctx, rows...)
}

func (st *Store) beatReaders(ctx context.Context, readers ...string) error {
	kv, err := st.rootKV()
	if err != nil {
		return nil // a store that keeps no beats holds every reader up
	}
	out, err := json.Marshal(sprint.Beat{At: st.now().UTC().Truncate(time.Second)})
	if err != nil {
		return err
	}
	for _, r := range readers {
		if err := kv.SetKey(ctx, readerBeatKey(r), string(out)); err != nil {
			return err
		}
	}
	return nil
}

// SetReaderAway holds reader away (reader away) or releases the hold (reader
// up), by the coordinator who. The reader is a row of the readers table.
func (st *Store) SetReaderAway(ctx context.Context, reader string, away bool, who string) error {
	kv, err := st.rootKV()
	if err != nil {
		return err
	}
	rows, err := st.ReaderRows(ctx)
	if err != nil {
		return err
	}
	if !contains(rows, reader) {
		return fmt.Errorf("no reader %s on the readers table; run: nova-sprint reader add %s", reader, reader)
	}
	hold := readerHold{}
	if away {
		hold = readerHold{Away: true, At: st.now().UTC().Truncate(time.Second), By: who}
	}
	out, err := json.Marshal(hold)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, readerAwayKey(reader), string(out))
}

// ForgetReaders deletes the beat and the hold of readers taken off the readers
// table (reader remove): a reader added again under the name starts clean, and
// teardown has no key of a reader it can no longer find.
func (st *Store) ForgetReaders(ctx context.Context, readers []string) error {
	var keys []string
	for _, r := range readers {
		keys = append(keys, st.Names.Key(readerBeatKey(r)), st.Names.Key(readerAwayKey(r)))
	}
	_, err := st.B.DeleteKeys(ctx, keys)
	return err
}

// ReaderStates is each of readers' state at now: held away, up, away or down
// (sprint.ReaderState). A store that keeps no records says none (nil), and
// every reader is held up.
func (st *Store) ReaderStates(ctx context.Context, readers []string, now time.Time) (map[string]string, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil
	}
	out := map[string]string{}
	if len(readers) == 0 {
		return out, nil
	}
	names := make([]string, 0, 2*len(readers))
	for _, r := range readers {
		names = append(names, readerBeatKey(r), readerAwayKey(r))
	}
	vals, oks, err := getKeys(ctx, kv, names)
	if err != nil {
		return nil, err
	}
	for i, r := range readers {
		var b sprint.Beat
		var hold readerHold
		if oks[2*i] {
			// ignored: an unreadable record is no beat, which the next beat replaces
			_ = json.Unmarshal([]byte(vals[2*i]), &b)
		}
		if oks[2*i+1] {
			// ignored: an unreadable record is no hold, which the next reader away or reader up replaces
			_ = json.Unmarshal([]byte(vals[2*i+1]), &hold)
		}
		out[r] = sprint.ReaderState(hold.Away, b, now)
	}
	return out, nil
}

// readerStatesInto gives a snapshot its readers' states: the readers table's
// rows as the snapshot holds them.
func (st *Store) readerStatesInto(ctx context.Context, s *sprint.Snapshot) error {
	if s.Readers == nil {
		return nil
	}
	m, err := st.ReaderStates(ctx, s.Readers.Rows(), s.Now)
	if err != nil {
		return err
	}
	s.ReaderStates = m
	return nil
}
