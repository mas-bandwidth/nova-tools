package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	code := run(args, &stdout, &stderr)
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
	code := run([]string{"plan", "check", "--file", path}, &out, &errb)
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
			code := run(args, &out, &errb)
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
	code := run([]string{"plan", "check"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--file is required") {
		t.Errorf("missing --file refusal does not name it: %s", errb.String())
	}
}
