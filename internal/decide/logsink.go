// The decision log has two sinks. The JSONL file at the path --log names is the
// log as it has always been, on every bench, the whole row with its evidence;
// routelog.go writes it and the summary reads it. The fleet's record of the
// same decision is one `decide` event on the cards:done stream (nova-tools
// #2623): written by internal/events, the writer every card transition goes
// through, and folded into the `decisions` table of the SQLite fold, where the
// calibration set is answered across benches.
//
// There was a third sink, the decide_log table. It is retired (#2623): the
// stream carries the row, the fold is the table, and one writer means one
// stream (Rowan + Johnny, 2026-09-22 17:16Z).
package decide

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// LogSink is where a decision row lands. Append is the one writer; Entries is
// the read the summary is a projection of; Close releases whatever the sink
// holds. A file sink holds nothing and an event sink holds a connection.
type LogSink interface {
	Append(e Entry) error
	Entries() ([]Entry, error)
	Close() error
}

// OpenLogSink opens the JSON lines log at a path. An empty path is a refusal,
// never a guess at one -- the same refusal AppendEntry has always made.
func OpenLogSink(path string) (LogSink, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("decide: no log; refusing to guess one. Pass the path of the JSON lines log")
	}
	return &FileSink{Path: path}, nil
}

// FileSink is the log as it has always been: append-only JSON lines at a path
// the caller names, written by AppendEntry and read by ReadEntries.
type FileSink struct{ Path string }

// Append writes one row.
func (f *FileSink) Append(e Entry) error { return AppendEntry(f.Path, e) }

// Entries reads the rows back, in the order they were written.
func (f *FileSink) Entries() ([]Entry, error) { return ReadEntries(f.Path) }

// Close is a no-op: the file sink holds nothing open between rows.
func (f *FileSink) Close() error { return nil }

// FakeLogSink is the in-memory sink the unit tests run on: append, read back
// in order, and a seam for a sink that fails.
type FakeLogSink struct {
	mu   sync.Mutex
	rows []Entry

	// AppendErr, when set, is what Append returns: the seam for a test that
	// wants to see a sink fail.
	AppendErr error
}

// NewFakeLogSink returns an empty sink.
func NewFakeLogSink() *FakeLogSink { return &FakeLogSink{} }

// Append stores the row.
func (f *FakeLogSink) Append(e Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.AppendErr != nil {
		return f.AppendErr
	}
	f.rows = append(f.rows, e)
	return nil
}

// Entries returns the rows in the order they were appended.
func (f *FakeLogSink) Entries() ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// Close is a no-op; the fake holds nothing.
func (f *FakeLogSink) Close() error { return nil }

// EventSink writes each decision as one `decide` event through an Emitter:
// the fleet store's RedisStore in the tool, the in-memory FakeStream in a test.
// It is write-only. The decisions on the stream are read by the fold
// (`nova-pulse fold`), never back through this sink.
type EventSink struct {
	Emitter events.Emitter
	// Bench is the bench the decision was made on, when the caller knows it.
	Bench string
	// Timeout bounds one write; zero is ten seconds.
	Timeout time.Duration
	closer  io.Closer
}

// OpenEventSink dials the fleet store at addr and returns the sink that writes
// decisions to cards:done. The password comes from the caller's environment,
// never from argv.
func OpenEventSink(addr, user, password, bench string) (*EventSink, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("decide: no store; the decision event wants the fleet Redis as host:port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := events.Open(ctx, events.Dial{Addr: addr, Username: user, Password: password})
	if err != nil {
		return nil, fmt.Errorf("decide: the decision stream: %w", err)
	}
	return &EventSink{Emitter: store, Bench: bench, closer: store}, nil
}

// Append writes the decision as one event on the stream.
func (s *EventSink) Append(e Entry) error {
	ev, err := DecisionEvent(e)
	if err != nil {
		return err
	}
	ev.Bench = s.Bench
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := s.Emitter.Emit(ctx, ev); err != nil {
		return fmt.Errorf("decide: write the decision to %s: %w", events.Stream, err)
	}
	return nil
}

// Entries refuses: the stream is read by the fold, not by the writer.
func (s *EventSink) Entries() ([]Entry, error) {
	return nil, fmt.Errorf("decide: the decisions on %s are read by the fold (nova-pulse fold --report), not through the writer; pass --log <path> to read the JSON lines log", events.Stream)
}

// Close releases the store connection, if this sink dialed one.
func (s *EventSink) Close() error {
	if s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

// Tee is one sink over several: Append writes every one of them and reports
// every one that failed, Entries reads the first, Close closes them all. A nil
// sink is skipped, so a caller passes what it opened and nothing else.
func Tee(sinks ...LogSink) LogSink {
	var live []LogSink
	for _, s := range sinks {
		if s != nil {
			live = append(live, s)
		}
	}
	return tee(live)
}

type tee []LogSink

func (t tee) Append(e Entry) error {
	var errs []error
	for _, s := range t {
		if err := s.Append(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t tee) Entries() ([]Entry, error) {
	if len(t) == 0 {
		return nil, fmt.Errorf("decide: no log to read")
	}
	return t[0].Entries()
}

func (t tee) Close() error {
	var errs []error
	for _, s := range t {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DecisionEvent is one log row as a `decide` event: decide_log's fields under
// decide_log's names (internal/events/decide.go). The stamp is the entry's at;
// the label is the unit, the id the whole stream joins on. A counter the
// provider did not report stays absent, never zero, and so does a confidence or
// a floor the row did not carry. Free text (the reason, the
// refusal) is capped at the stream's field ceiling with a mark that says so;
// the uncut text is in the JSON lines log. The evidence document does not
// travel: its measured columns do.
//
// A stamp that is not a time is a refusal naming it: the record never takes a
// zero stamp for a timestamp nobody could read.
func DecisionEvent(e Entry) (events.Event, error) {
	var at time.Time
	if stamp := strings.TrimSpace(e.Time); stamp != "" {
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return events.Event{}, fmt.Errorf("decide: the decision for unit %s carries %q, which is not an RFC3339 time: %w", e.Unit, e.Time, err)
		}
		at = parsed.UTC()
	}
	kind := strings.TrimSpace(e.Kind)
	if kind == "" {
		kind = strings.TrimSpace(e.Evidence.Kind)
	}
	return events.Event{
		Label:     e.Unit,
		Kind:      events.Decide,
		TokensIn:  counter(e.TokensIn),
		TokensOut: counter(e.TokensOut),
		At:        at,
		Decision: &events.Decision{
			UnitID:              e.Unit,
			Kind:                events.Text(kind),
			Files:               e.Evidence.Files,
			Packages:            e.Evidence.Packages,
			Lanes:               e.Evidence.Lanes,
			Lane:                events.Text(e.Evidence.LaneOwner),
			RungTried:           events.Text(e.RungTried),
			Height:              e.Height,
			Confidence:          measuredCopy(e.Confidence),
			Floor:               measuredCopy(e.Floor),
			SteppedUp:           e.SteppedUp,
			Escalated:           e.Escalated,
			Designated:          e.Designated,
			Source:              events.Text(e.Source),
			RowanPick:           events.Text(e.RowanPick),
			Reason:              events.Text(e.Reason),
			Wait:                events.Text(e.Wait),
			AwaitingTermination: e.AwaitingTermination,
			Refusal:             events.Text(e.Refusal),
			Outcome:             events.Text(e.Outcome),
			RungSucceeded:       events.Text(e.RungSucceeded),
			Calls:               e.Calls,
			UsageFailed:         e.UsageFailed,
		},
	}, nil
}

// measuredCopy is a confidence or floor the row carried, copied so the event
// never aliases the entry, and one it did not carry as the absence it is.
func measuredCopy(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// counter is a reported count as the stream's counter, and an unreported one
// as the absence it is.
func counter(n *int) *int64 {
	if n == nil {
		return nil
	}
	v := int64(*n)
	return &v
}
