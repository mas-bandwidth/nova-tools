package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runSprint(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	code, stdout, stderr := runSprint()
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout %q; a refusal belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "run: nova-sprint help") {
		t.Fatalf("stderr %q, want the help door", stderr)
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("a bare command printed more than one line:\n%s", stderr)
	}
}

func TestTableRefusalNamesEveryMissingPiece(t *testing.T) {
	code, _, stderr := runSprint("table")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--once", "--fixture", "--refresh pending", "--out", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestEmptyFixtureRefusesToBlank(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runSprint("table", "--once", "--fixture", empty, "--out", filepath.Join(dir, "out.txt"))
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr %s", code, stderr)
	}
	if !strings.Contains(stderr, "empty") {
		t.Fatalf("stderr %q, want it to say the fixture is empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("an empty fixture published a table")
	}
}

func TestSecondStartDoesNotClearThePreviousTable(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join("testdata", "table.txt")
	want, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(want)) == 0 {
		t.Fatal("fixture is empty")
	}
	out := filepath.Join(dir, "sprint-table.txt")

	code, published, stderr := runSprint("table", "--once", "--fixture", fixture, "--out", out)
	if code != 0 {
		t.Fatalf("publish exit %d; stderr %s", code, stderr)
	}
	if !strings.Contains(published, "TABLE PUBLISHED") {
		t.Fatalf("stdout %q, want TABLE PUBLISHED", published)
	}

	code, kept, stderr := runSprint("table", "--once", "--refresh", "pending", "--out", out)
	if code != 0 {
		t.Fatalf("second start exit %d; stderr %s", code, stderr)
	}
	if !strings.Contains(kept, "TABLE KEPT") || !strings.Contains(kept, "reason=refresh-not-ready") {
		t.Fatalf("stdout %q, want TABLE KEPT reason=refresh-not-ready", kept)
	}
	if !strings.Contains(kept, "SPRINT TABLE") {
		t.Fatal("second start did not show the previous table")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("second start changed the published table (%d bytes -> %d)", len(want), len(got))
	}
	if len(got) == 0 {
		t.Fatal("published table is empty")
	}
}

func TestFixtureDryRenderIsByteIdentical(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "table.txt"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runSprint("table", "--once", "--fixture", filepath.Join("testdata", "table.txt"))
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if stdout != string(want) {
		t.Fatalf("dry render is not the fixture\n got %q\nwant %q", stdout, want)
	}
}

func TestRefreshOwnSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		code, _, stderr := runSprint("refresh", "--", "true")
		if code != 2 || !strings.Contains(stderr, "setsid") {
			t.Fatalf("windows refresh exit %d stderr %q, want a setsid refusal", code, stderr)
		}
		return
	}
	code, stdout, stderr := runSprint("refresh", "--", "/usr/bin/true")
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "REFRESH SESSION pid=") {
		t.Fatalf("stdout %q, want REFRESH SESSION pid=", stdout)
	}
}

func TestRefreshRefusesAMissingCommand(t *testing.T) {
	code, _, stderr := runSprint("refresh")
	if code != 2 || !strings.Contains(stderr, "--") {
		t.Fatalf("exit %d stderr %q, want a refusal that names --", code, stderr)
	}
}

// TestHarvestHasNoResultsRoot is the #3329 DONE-WHEN (verb half): the
// results root is not typed on argv. `card harvest --results-root` is an
// unknown flag whose refusal names the card hash field that replaced it.
func TestHarvestHasNoResultsRoot(t *testing.T) {
	for _, arg := range [][]string{{"--results-root", "/srv/results"}, {"--results-root=/srv/results"}} {
		args := append([]string{"card", "harvest", "--redis", "127.0.0.1:1", "--sprint", "s", "--bench", "b"}, arg...)
		code, stdout, stderr := runSprint(args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2 (usage)", arg, code)
		}
		if stdout != "" {
			t.Fatalf("%v: stdout %q; a refusal belongs on stderr", arg, stdout)
		}
		for _, want := range []string{"unknown flag --results-root", "s:<S>:card:<label>", "results"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v: stderr %q lacks %q", arg, stderr, want)
			}
		}
	}
}

// TestEndRefusesRelativeResults is the #3329 DONE-WHEN (writer half): the
// card hash field `results` is always absolute, so `card end --results
// rel/dir` exits 1 before it touches Redis, and ns_card_end, the field's
// single writer, refuses a relative dir itself and writes nothing.
func TestEndRefusesRelativeResults(t *testing.T) {
	const (
		sprint   = "s3329"
		label    = "rel"
		identity = sprint + "/" + label + "/0123abcd/b/1"
		token    = "tok-3329"
	)
	t.Run("card end", func(t *testing.T) {
		t.Setenv(store.UserEnv, "")
		mr := miniredis.RunT(t)
		mr.HSet("s:"+sprint+":card:"+label, "state", "running", "attempt", "1",
			"token", token, "identity", identity, "bench", "b")
		before := mr.Dump()
		code, stdout, stderr := runSprint("card", "end", "--redis", mr.Addr(), "--sprint", sprint, label,
			"--token", token, "--outcome", "DONE", "--reason", "done", "--results", identity)
		if code != 1 {
			t.Fatalf("exit %d, want 1 (USAGE); stdout %q stderr %q", code, stdout, stderr)
		}
		if !strings.Contains(stderr, "--results "+identity+" is relative") || !strings.Contains(stderr, "absolute") {
			t.Fatalf("stderr %q does not name the relative --results", stderr)
		}
		if n := mr.CommandCount(); n != 0 {
			t.Fatalf("card end sent %d Redis commands for a relative --results; want none", n)
		}
		if after := mr.Dump(); after != before {
			t.Fatalf("card end wrote to Redis:\nbefore %s\nafter %s", before, after)
		}
	})
	t.Run("ns_card_end", func(t *testing.T) {
		_, client := sprintRedis(t)
		ctx := context.Background()
		key := "s:" + sprint + ":card:" + label
		if err := client.HSet(ctx, key, "state", "running", "attempt", "1", "token", token,
			"token_sha", "sha", "identity", identity, "bench", "b", "base_sha", "0123abcd").Err(); err != nil {
			t.Fatal(err)
		}
		keys := []string{key, "s:" + sprint + ":log", "s:" + sprint + ":idem"}
		reply, err := client.FCall(ctx, "ns_card_end", keys, "token", sprint, label, token, identity,
			identity, "DONE", "done", "sha", "pushed", "0", "DONE", "done").Result()
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(reply); !strings.Contains(got, "USAGE") {
			t.Fatalf("ns_card_end with relative results = %s, want USAGE", got)
		}
		h, err := client.HGetAll(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if h["state"] != "running" || h["results"] != "" {
			t.Fatalf("ns_card_end wrote the card: %v", h)
		}
		if n, _ := client.XLen(ctx, "s:"+sprint+":log").Result(); n != 0 {
			t.Fatalf("ns_card_end wrote %d receipts", n)
		}
	})
}
