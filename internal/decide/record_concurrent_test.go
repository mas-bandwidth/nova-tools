package decide

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// ONE CLIENT, MANY GOROUTINES.
//
// `Client.Decide` is handed around as a function value and called
// concurrently -- internal/swarm's task decider takes `client.Decide` as its
// `decideFunc` and runs it over cards at once. Until the receipt existed,
// `record` wrote nothing on the client: it appended a row and dropped the
// error. Keeping the receipt meant keeping COUNTERS, and the first shape of
// that wrote `c.recorded++` and `c.recordErr` with no lock.
//
// It was not a theoretical race. `go test -race ./internal/swarm/` went from
// twelve seconds to a 600-second timeout, twice, at capped flags and at
// default flags alike. This is the control that keeps the bookkeeping behind
// the client's lock: it fails under -race the moment someone takes the lock
// away, and it is cheap enough to run every time.
func TestOneClientRecordsFromManyGoroutinesWithoutARace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"q":{"type":"choice","choice":"a","confidence":0.91}}}`))
	}))
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "test-key")

	client, err := New(srv.URL, "JEV_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	client.UseDecisions(&countingDriver{})
	qs := map[string]Question{"q": {Instructions: "pick one", Choice: map[string]string{"a": "the a", "b": "the b"}}}

	const callers = 8
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Every caller also names the row's source, which is the other
			// field the receipt reads back.
			client.SetRowSource(SourceProvider, true)
			if _, _, err := client.Decide(context.Background(), fmt.Sprintf("state %d", i), qs); err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := client.Recorded(); got != callers {
		t.Errorf("recorded %d rows from %d callers; the counter is not holding the lock", got, callers)
	}
	if err := client.RecordErr(); err != nil {
		t.Errorf("a row failed to append: %v", err)
	}
}

// countingDriver is a decisions table that keeps nothing: what is tested here
// is the CLIENT's bookkeeping, and the driver's own lock is its business.
type countingDriver struct {
	mu   sync.Mutex
	rows []DecisionRow
}

func (d *countingDriver) Append(row DecisionRow) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows = append(d.rows, row)
	return nil
}

func (d *countingDriver) Rows(kind string) ([]DecisionRow, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []DecisionRow{}
	for _, r := range d.rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out, nil
}

func (d *countingDriver) Close() error { return nil }
