package main

// events_log_test.go drives the events verb's structured log at the edge a bench reaches:
// run() with a real argv. The promises are the ones the fleet's Alloy depends on --
// SPEC-LOGS.md Part 2's fields, the four labels its LogQL selects on, one JSON object per
// line, and a --log file the agent already installed on every bench can tail without a
// new shipper. Nothing here opens a socket: miniredis and the fake forge.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// jsonLines reads a file of JSON objects, one per line, failing on anything else.
func jsonLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a log line is not one JSON object: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// eventsDeps is the verb's dependencies with a fake forge holding one green PR and a base.
func eventsDeps(addr string) Deps {
	return Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(_, _ string, _ time.Duration) ci.Forge {
			return &fakeForge{snap: ci.Snapshot{
				PRs:  []ci.PRState{{Number: 42, Branch: "rowan/x", Head: "a1b2c3d", Conclusion: ci.ConclusionSuccess}},
				Base: "0d739352",
			}}
		},
	}
}

// 1. --log writes the file Alloy tails, and every line carries the four labels its queries
// select on plus the spec's fixed fields.
func TestEventsLogWritesTheFileWithTheLabels(t *testing.T) {
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "nova-events.log")

	var out, errb bytes.Buffer
	code := run([]string{"events", "--redis", mr.Addr(), "--repo", "mas-bandwidth/nova-tools",
		"--bench", "hulk", "--log", path, "--once"}, &out, &errb, eventsDeps(mr.Addr()))
	if code != 0 {
		t.Fatalf("events --once exit = %d, stderr=%s", code, errb.String())
	}
	lines := jsonLines(t, path)
	if len(lines) < 3 {
		t.Fatalf("the log has %d lines, want start, the two published events and done", len(lines))
	}
	var kinds []string
	for _, m := range lines {
		for k, v := range map[string]string{"source": "nova-work", "verb": "events", "bench": "hulk"} {
			if got, _ := m[k].(string); got != v {
				t.Errorf("%s = %q, want %q; line=%v", k, got, v, m)
			}
		}
		if ev, _ := m["event"].(string); ev == "" {
			t.Errorf("a line carries no event kind: %v", m)
		} else {
			kinds = append(kinds, ev)
		}
		for _, field := range []string{"ts", "level", "guid", "msg", "dur_ms", "err", "card", "pr"} {
			if _, ok := m[field]; !ok {
				t.Errorf("the line is missing the fixed field %s: %v", field, m)
			}
		}
	}
	for _, want := range []string{"start", "pr-checks-done", "dev-moved", "done"} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s line in the log; kinds=%v", want, kinds)
		}
	}
}

// 2. The stdout event line is unchanged: the JSON line is written BESIDE it, never
// instead of it, so a reader that only knows the grammar still works.
func TestEventsLogLeavesTheStdoutLineAlone(t *testing.T) {
	mr := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "nova-events.log")
	var out, errb bytes.Buffer
	code := run([]string{"events", "--redis", mr.Addr(), "--repo", "mas-bandwidth/nova-tools",
		"--log", path, "--once"}, &out, &errb, eventsDeps(mr.Addr()))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "EVENTS OK once=true ") {
		t.Fatalf("the stdout event line changed: %q", out.String())
	}
	if strings.Contains(out.String(), "{") {
		t.Fatalf("a JSON line reached stdout: %q", out.String())
	}
}

// 3. A --log path the verb cannot open is a refusal with the path named, not a silent
// run with no log: a bench whose log never appears in Loki must say why.
func TestEventsRefusesALogPathItCannotOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "no-such-dir", "nova-events.log")
	var out, errb bytes.Buffer
	code := run([]string{"events", "--redis", mr.Addr(), "--log", path, "--once"}, &out, &errb, eventsDeps(mr.Addr()))
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--log") {
		t.Errorf("the refusal does not name --log: %s", errb.String())
	}
}

// 4. With no --log the lines go to stderr, which on a bench is the unit's journal and so
// the source Alloy already reads. The stdout line is still the grammar's.
func TestEventsWithoutALogWritesToStderr(t *testing.T) {
	mr := miniredis.RunT(t)
	var out, errb bytes.Buffer
	code := run([]string{"events", "--redis", mr.Addr(), "--repo", "mas-bandwidth/nova-tools",
		"--bench", "space", "--once"}, &out, &errb, eventsDeps(mr.Addr()))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), `"source":"nova-work"`) || !strings.Contains(errb.String(), `"bench":"space"`) {
		t.Fatalf("no structured line on stderr: %s", errb.String())
	}
	if !strings.HasPrefix(out.String(), "EVENTS OK ") {
		t.Fatalf("the stdout event line changed: %q", out.String())
	}
}

// 5. A card id off the stream shaped like an API key never reaches the log file. The
// stream is data from outside this program, so this is the leak the emitter must stop.
func TestEventsLogNeverCarriesASecret(t *testing.T) {
	const secret = "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	mr := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "nova-events.log")
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	// The group is created at the stream's end, so one pass must run before the card is
	// written or the card is history the bridge does not replay.
	var out, errb bytes.Buffer
	if code := run([]string{"events", "--redis", mr.Addr(), "--log", path, "--once"}, &out, &errb, eventsDeps(mr.Addr())); code != 0 {
		t.Fatalf("first pass exit = %d, stderr=%s", code, errb.String())
	}
	if err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: ci.StreamCardsDone,
		Values: map[string]interface{}{"card": secret, "label": "leaked"},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"events", "--redis", mr.Addr(), "--log", path, "--once"}, &out, &errb, eventsDeps(mr.Addr())); code != 0 {
		t.Fatalf("second pass exit = %d, stderr=%s", code, errb.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatalf("a secret value reached the log file: %s", raw)
	}
	if !bytes.Contains(raw, []byte("[redacted]")) {
		t.Fatalf("the card-done line carries no redaction mark: %s", raw)
	}
}
