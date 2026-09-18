package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/redis/go-redis/v9"
)

func writeSeed(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deps.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	return path
}

func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "work.work")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const openNeedSeed = `{"nodes":[{"id":"a","needs":["b"]},{"id":"b"},{"id":"c"}]}`

const cycleSeed = `{"nodes":[{"id":"a","needs":["b"]},{"id":"b","needs":["a"]}]}`

func invoke(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	return code, stdout.String(), stderr.String()
}

// The help text carries the dependencies and ready verb lines exactly as section 1
// prints them.
func TestHelpNamesTheDependenciesAndReadyVerbs(t *testing.T) {
	code, stdout, _ := invoke("help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, want := range []string{"nova-work dependencies", "ready --node X"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// dependencies refuses a :deps cycle before it publishes anything: exit 2, one
// remedy line naming validator rule 3.
func TestDependenciesRefusesANeedsCycle(t *testing.T) {
	seed := writeSeed(t, cycleSeed)
	code, stdout, stderr := invoke("dependencies", "--graph", seed)
	if code != 2 {
		t.Fatalf("dependencies on a cycle exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	for _, want := range []string{"rule 3", ":deps", "cycle", "nova-work help"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("cycle refusal does not name %q:\n%s", want, stderr)
		}
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refused cycle wrote to stdout: %q", stdout)
	}
}

// A seeded acyclic graph publishes once, as one line.
func TestDependenciesPublishesAnAcyclicGraph(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)
	code, stdout, stderr := invoke("dependencies", "--graph", seed)
	if code != 0 {
		t.Fatalf("dependencies exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("dependencies printed more than one line: %q", stdout)
	}
	if !strings.HasPrefix(line, "DEPENDENCIES OK") {
		t.Fatalf("dependencies line = %q, want a DEPENDENCIES OK line", line)
	}
}

// ready --node X is the ready set: the row for an open-need node names its exact
// blocker and its resolver, and a ready node's row says so.
func TestReadyPrintsEachRowsBlockerAndResolver(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)

	code, stdout, stderr := invoke("ready", "--graph", seed, "--node", "a")
	if code != 0 {
		t.Fatalf("ready --node a exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"node=a", "ready=false", "blocker=b", "state=open", `resolver="nova-merge queue"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("ready --node a line %q does not name %q", line, want)
		}
	}

	code, stdout, stderr = invoke("ready", "--graph", seed, "--node", "b")
	if code != 0 {
		t.Fatalf("ready --node b exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	if line := strings.TrimSpace(stdout); !strings.Contains(line, "node=b") || !strings.Contains(line, "ready=true") {
		t.Fatalf("ready --node b line = %q, want ready=true", line)
	}
}

// An unknown node is a refusal, never a guess.
func TestReadyRefusesAnUnknownNode(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)
	code, stdout, stderr := invoke("ready", "--graph", seed, "--node", "zzz")
	if code != 2 {
		t.Fatalf("ready on an unknown node exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "zzz") || !strings.Contains(stderr, "nova-work help") {
		t.Fatalf("unknown-node refusal = %q, want the node named and the remedy", stderr)
	}
}

func TestPlanCheckReadsAValidPlan(t *testing.T) {
	path := writePlan(t, "(:plan :version 1 (:node :id \"n1\" :kind docs :bespoke \"kept\"))\n")
	var out, errb bytes.Buffer
	code := run([]string{"plan", "check", "--file", path}, strings.NewReader(""), &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "PLAN OK file=") || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Errorf("want exactly one PLAN OK line, got %q", out.String())
	}
}

func TestPlanCheckRefusals(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
		want string
	}{
		{
			name: "dispatch macro",
			body: "(:plan :version 1 :goal #.(error \"x\"))\n",
			want: "dispatch macro",
		},
		{
			name: "unknown kind",
			body: "(:plan :version 1 (:node :id \"n1\" :kind bogus))\n",
			want: ":kind",
		},
		{
			name: "absent need",
			body: "(:plan :version 1 (:node :id \"a\" :kind go-fix :needs (\"b\")))\n",
			want: ":needs",
		},
		{
			name: "needs cycle",
			body: "(:plan :version 1 (:node :id \"a\" :kind go-fix :needs (\"b\")) (:node :id \"b\" :kind go-fix :needs (\"a\")))\n",
			want: "rule 3",
		},
		{
			name: "max-bytes",
			body: "(:plan :version 1 (:node :id \"n1\" :kind docs))\n",
			args: []string{"--max-bytes", "8"},
			want: "max-bytes",
		},
		{
			name: "max-depth",
			body: "(:plan (:a (:b (:c))))\n",
			args: []string{"--max-depth", "2"},
			want: "max-depth",
		},
		{
			name: "max-nodes",
			body: "(:plan :version 1 :clip :per-node)\n",
			args: []string{"--max-nodes", "2"},
			want: "max-nodes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writePlan(t, tc.body)
			args := append([]string{"plan", "check", "--file", path}, tc.args...)
			var out, errb bytes.Buffer
			code := run(args, strings.NewReader(""), &out, &errb, time.Now().UTC())
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("refusal does not name %q: %s", tc.want, errb.String())
			}
			if !strings.Contains(errb.String(), "run: nova-work help") {
				t.Errorf("refusal is not one remedy line: %s", errb.String())
			}
			if out.Len() != 0 {
				t.Errorf("a refusal printed on stdout: %q", out.String())
			}
		})
	}
}

func TestPlanCheckRefusesAMissingFile(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"plan", "check"}, strings.NewReader(""), &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--file is required") {
		t.Errorf("missing --file refusal does not name it: %s", errb.String())
	}
}

const expandPlan = `(:plan :version 1
 (:node :id "n1" :kind docs :repo "o/r" :base "dev"
  :inputs ((:spec "docs/a.md:1-2"))
  :output (:branch "rowan/n1-a" :green ("test:a"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:node :id "n2" :kind go-fix :repo "o/r" :base "dev" :needs ("n1")
  :output (:branch "rowan/n2-b" :green ("test:b"))
  :budget (:minutes 45 :tokens 180000 :model-floor opus)
  :affinity (:bench local :route "deepseek-flash"))
 (:clip :per-node))`

// plan expand writes one card directory per node and prints exactly one line.
func TestPlanExpandWritesACardPerNode(t *testing.T) {
	path := writePlan(t, expandPlan)
	out := t.TempDir()
	code, stdout, stderr := invoke("plan", "expand", "--file", path, "--out", out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "PLAN EXPANDED") {
		t.Fatalf("expand line = %q, want a PLAN EXPANDED line", stdout)
	}
	if strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("plan expand printed more than one line: %q", stdout)
	}
	for _, id := range []string{"n1", "n2"} {
		card, err := os.ReadFile(filepath.Join(out, id, "card"))
		if err != nil {
			t.Fatalf("read %s card: %v", id, err)
		}
		if !strings.Contains(string(card), "card "+id) {
			t.Errorf("card %s does not carry its node: %s", id, card)
		}
	}
}

// plan expand refuses a missing --out, never guessing a directory.
func TestPlanExpandRefusesAMissingOut(t *testing.T) {
	path := writePlan(t, expandPlan)
	code, _, stderr := invoke("plan", "expand", "--file", path)
	if code != 2 || !strings.Contains(stderr, "--out is required") {
		t.Fatalf("exit = %d stderr=%q, want 2 naming --out", code, stderr)
	}
}

// plan expand refuses a needs cycle before any card is written.
func TestPlanExpandRefusesANeedsCycle(t *testing.T) {
	body := `(:plan :version 1
 (:node :id "a" :kind docs :repo "o/r" :base "dev" :needs ("b")
  :output (:branch "rowan/a-a" :green ("t"))
  :budget (:minutes 1 :tokens 1 :model-floor sonnet)
  :affinity (:bench verify :route "r"))
 (:node :id "b" :kind docs :repo "o/r" :base "dev" :needs ("a")
  :output (:branch "rowan/b-b" :green ("t"))
  :budget (:minutes 1 :tokens 1 :model-floor sonnet)
  :affinity (:bench verify :route "r"))
 (:clip :per-node))`
	path := writePlan(t, body)
	out := t.TempDir()
	code, stdout, stderr := invoke("plan", "expand", "--file", path, "--out", out)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "cycle") || !strings.Contains(stderr, "run: nova-work help") {
		t.Fatalf("cycle refusal = %q, want the cycle named and the remedy", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refused cycle wrote to stdout: %q", stdout)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a refused cycle wrote %d cards", len(entries))
	}
}

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testReadyStream(t *testing.T) (*miniredis.Miniredis, redisq.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.EnsureGroup(context.Background(), redisq.ReadyStream, redisq.Group, "0"); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	return mr, c
}

// TestPushThenPull puts a valid card and reads it back as the bench's group
// would: the entry carries the id, label, body, priority, needs and pushed-at
// the spec names.
func TestPushThenPull(t *testing.T) {
	mr, c := testReadyStream(t)
	dir := t.TempDir()
	path := writeCard(t, dir, "card-one.md", "RESULT: CARD-1 green\n\nrun the thing\n")

	var out, errb bytes.Buffer
	code := run([]string{"push", "--redis", mr.Addr(), "--card", path, "--priority", "7", "--needs", "a,b"},
		strings.NewReader(""), &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("push exit %d: %s", code, errb.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "PUSH OK id=") || !strings.Contains(line, "label=card-one") {
		t.Fatalf("push line = %q, want PUSH OK id=<stream id> label=card-one", line)
	}

	entry, err := c.ReadGroup(context.Background(), redisq.ReadyStream, redisq.Group, "bench-a", 0)
	if err != nil || entry == nil {
		t.Fatalf("read ready: entry=%v err=%v", entry, err)
	}
	if got := entry.Field("label"); got != "card-one" {
		t.Errorf("label = %q, want card-one", got)
	}
	if got := entry.Field("body"); !strings.Contains(got, "run the thing") {
		t.Errorf("body = %q", got)
	}
	if got := entry.Field("priority"); got != "7" {
		t.Errorf("priority = %q, want 7", got)
	}
	if got := entry.Field("needs"); got != "a,b" {
		t.Errorf("needs = %q, want a,b", got)
	}
	if entry.Field("id") == "" || entry.Field("pushed-at") == "" {
		t.Errorf("id and pushed-at must be set: %+v", entry.Fields)
	}
}

// TestPushRefusesACardWhoseFirstLineIsNotResult is the card-content refusal:
// a body whose first line is not a RESULT line never reaches the ready set.
func TestPushRefusesACardWhoseFirstLineIsNotResult(t *testing.T) {
	mr, _ := testReadyStream(t)
	dir := t.TempDir()
	path := writeCard(t, dir, "not-a-card.md", "just some prose\n")

	var out, errb bytes.Buffer
	code := run([]string{"push", "--redis", mr.Addr(), "--card", path},
		strings.NewReader(""), &out, &errb, time.Now().UTC())
	if code == 0 {
		t.Fatalf("push accepted a card without a RESULT first line: %s", out.String())
	}
	if !strings.Contains(errb.String(), "RESULT:") {
		t.Errorf("refusal does not name the RESULT line: %s", errb.String())
	}
	if n := streamLen(t, mr.Addr(), redisq.ReadyStream); n != 0 {
		t.Errorf("a refused card reached cards:ready: len=%d", n)
	}
}

// TestPushRefusesABadLabel refuses a card whose label carries a character the
// label grammar does not allow.
func TestPushRefusesABadLabel(t *testing.T) {
	mr, _ := testReadyStream(t)
	dir := t.TempDir()
	path := writeCard(t, dir, "bad label.md", "RESULT: CARD-2 green\n")

	var errb bytes.Buffer
	code := run([]string{"push", "--redis", mr.Addr(), "--card", path},
		strings.NewReader(""), &bytes.Buffer{}, &errb, time.Now().UTC())
	if code == 0 {
		t.Fatalf("push accepted a label outside [A-Za-z0-9._-]+")
	}
	if !strings.Contains(errb.String(), "label") {
		t.Errorf("refusal does not name the label: %s", errb.String())
	}
	if n := streamLen(t, mr.Addr(), redisq.ReadyStream); n != 0 {
		t.Errorf("a refused card reached cards:ready: len=%d", n)
	}
}

func streamLen(t *testing.T, addr, stream string) int64 {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	n, err := rdb.XLen(context.Background(), stream).Result()
	if err != nil {
		t.Fatalf("xlen %s: %v", stream, err)
	}
	return n
}
