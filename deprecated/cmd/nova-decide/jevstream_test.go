package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// fakeJevStream installs an in-memory cards:done in place of the fleet Redis for
// one test and returns it with the Dial the verb asked for.
func fakeJevStream(t *testing.T) (*events.FakeStream, *events.Dial) {
	t.Helper()
	stream := events.NewFakeStream()
	var asked events.Dial
	prev := dialJevStream
	dialJevStream = func(ctx context.Context, d events.Dial) (events.Emitter, func() error, error) {
		asked = d
		return stream, stream.Close, nil
	}
	t.Cleanup(func() { dialJevStream = prev })
	return stream, &asked
}

// TestReviewWritesEveryJevLineToTheStream is the DONE-WHEN of recut 2788
// (supersedes PR #2788): every JEV line the review verb prints is written to
// the ledger stream by production code -- one kind=jev entry per line, with the
// pull request, the exact head and the score the line carries.
func TestReviewWritesEveryJevLineToTheStream(t *testing.T) {
	gh, _ := fakeGH(t)
	stream, asked := fakeJevStream(t)
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "not-printed")
	batch := filepath.Join(t.TempDir(), "batch.txt")
	if err := os.WriteFile(batch, []byte("1488\n1556\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "l.jsonl")
	var out, errb bytes.Buffer
	run([]string{"review", "--repo", "mas-bandwidth/schema", "--batch", batch,
		"--gh", gh, "--replay", cellData(t, "jev-2026-09-22"),
		"--ledger", "file,redis", "--store", "fleet-redis.invalid:6379",
		"--ledger-path", ledger}, &out, &errb)

	if asked.Addr != "fleet-redis.invalid:6379" || asked.Password != "not-printed" || asked.Stream != events.Stream {
		t.Fatalf("dialled %+v, want the --store address, the password from the environment and %s", *asked, events.Stream)
	}
	if strings.Contains(out.String()+errb.String(), "not-printed") {
		t.Fatal("the store password was printed")
	}
	type jevLine struct{ head, score string }
	var lines []jevLine
	for _, l := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(l, "JEV head=") {
			continue
		}
		var jl jevLine
		for _, f := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(f, "head="); ok {
				jl.head = v
			}
			if v, ok := strings.CutPrefix(f, "score="); ok {
				jl.score = v
			}
		}
		lines = append(lines, jl)
	}
	if len(lines) != 2 {
		t.Fatalf("%d JEV lines, want 2: stdout=%s stderr=%s", len(lines), out.String(), errb.String())
	}
	got, err := stream.Range(context.Background(), "-", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(lines) {
		t.Fatalf("%d stream entries for %d JEV lines; every line goes to the stream once: stderr=%s", len(got), len(lines), errb.String())
	}
	for i, pr := range []int{1488, 1556} {
		f := got[i].Fields
		want := map[string]string{
			"event": "jev",
			"label": "mas-bandwidth/schema#" + strconv.Itoa(pr),
			"pr":    strconv.Itoa(pr),
			"head":  lines[i].head,
			"route": "score/" + lines[i].score,
			"model": "jev-latest",
		}
		for k, v := range want {
			if f[k] != v {
				t.Errorf("entry %d field %s = %q, want %q (fields %v)", i, k, f[k], v, f)
			}
		}
	}
	if raw, err := os.ReadFile(ledger); err != nil || strings.Count(string(raw), "\n") != 2 {
		t.Fatalf("file,redis must still append the JSONL: %v %q", err, raw)
	}
}

// TestReviewRedisLedgerWantsAStore. --ledger redis with no address is a refusal
// at the door, before any pull request is fetched.
func TestReviewRedisLedgerWantsAStore(t *testing.T) {
	_, _ = fakeJevStream(t)
	t.Setenv("NOVA_REDIS_ADDR", "")
	var out, errb bytes.Buffer
	if code := run([]string{"review", "--repo", "a/b", "--pr", "1", "--ledger", "redis"}, &out, &errb); code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--store") {
		t.Fatalf("the refusal does not name --store: %s", errb.String())
	}
}
