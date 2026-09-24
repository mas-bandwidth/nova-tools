package log

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// a-long-running-verb-writes-one-json-line-per-event: a run that changes state five times
// leaves five JSON objects, one per line, each carrying the run's host, the verb and the
// event label a LogQL query selects on. The test WRITES the run's log to a file and then
// PARSES it back, which is the read a person does after the fact -- no buffer shortcut, and
// the file is inside t.TempDir().
func TestLongVerbWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create run log: %v", err)
	}

	v := NewLongVerb(f, "nova-pulse", "space", "harvest", fixedClock(), fixedGUID())

	// One run of a long-running verb: the spine, an action per item, then done. The labels
	// are the spec's vocabulary plus the part's own nouns.
	run := []struct{ label, msg string }{
		{"start", "harvest: pass begins"},
		{"delete", "delete: /tmp/a by age"},
		{"keep", "keep: /tmp/b under the cap"},
		{"fetch", "fetch: origin/dev 39d1d6a"},
		{"done", "harvest: pass ends"},
	}
	for _, e := range run {
		v.Event(e.label, e.msg)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close run log: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	log := string(raw)

	// One event, one line: five objects, each on its own line, the file ending in a newline.
	if got := strings.Count(log, "\n"); got != len(run) {
		t.Fatalf("the run wrote %d lines, want one per event (%d):\n%s", got, len(run), log)
	}
	if !strings.HasSuffix(log, "\n") {
		t.Fatalf("the run's last line has no newline:\n%s", log)
	}

	lines := strings.Split(strings.TrimSuffix(log, "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			t.Fatalf("line %d is empty; every event is one nonempty JSON object", i)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i, err, line)
		}
		want := run[i]
		if h := got["bench"]; h != "space" {
			t.Errorf("line %d host/bench = %v, want space", i, h)
		}
		if gv := got["verb"]; gv != "harvest" {
			t.Errorf("line %d verb = %v, want harvest", i, gv)
		}
		if gv := got["event"]; gv != want.label {
			t.Errorf("line %d label/event = %v, want %v", i, gv, want.label)
		}
		if gv := got["msg"]; gv != oneline.Field(want.msg) {
			t.Errorf("line %d msg = %v, want the oneline.Field rendering %v", i, gv, oneline.Field(want.msg))
		}
		if gv := got["source"]; gv != "nova-pulse" {
			t.Errorf("line %d source = %v, want nova-pulse", i, gv)
		}
		if gv := got["guid"]; gv != "a1b2c3" {
			t.Errorf("line %d guid = %v, want a1b2c3", i, gv)
		}
	}

	// The run's labels never change: every line names the same host, verb and source.
	for i, line := range lines {
		if !strings.Contains(line, `"bench":"space"`) || !strings.Contains(line, `"verb":"harvest"`) {
			t.Errorf("line %d dropped the run's host or verb:\n%s", i, line)
		}
	}

	// A quiet run writes nothing: no writer, no lines, the additive promise.
	quiet := NewLongVerb(nil, "nova-pulse", "space", "harvest", fixedClock(), fixedGUID())
	quiet.Event("start", "should not be written")
}
