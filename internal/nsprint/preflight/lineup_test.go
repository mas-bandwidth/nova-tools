package preflight

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// declared is the fleet standard of the 2026-09-23 incident: nova_build
// v0.16.0-dev.e4386caa and the Studio's secrets revision (rowan-tools #181).
var declared = Declared{Build: "e4386caa", SecretsRev: "0fa4aa554bef"}

// goodAnswers is a lined-up bench's probe answers, key for key as
// bin/bench-conform prints them.
func goodAnswers() map[string]string {
	return map[string]string{
		KeyBuild:           "e4386caa",
		KeySecretsStore:    "branch=main,upstream=yes,rev=0fa4aa554bef,clean=yes",
		KeyCloneVerb:       "verb=ok,mirrors=nova-tools:nova-work:rowan-tools:schema",
		KeyPushCredential:  "present",
		KeyMirrorAge:       "42",
		KeyResultsRoot:     "~/nova-bench/results",
		KeyFinishedJobdirs: "0",
	}
}

var probes = []string{"probe-1", "probe-2", "probe-3", "probe-4", "probe-5"}

// lineupFixture registers the benches with a live beat, publishes each
// bench's conform record through the one writer, cuts the five probes and
// lands the first `landed` of them. It returns the gathered input.
func lineupFixture(t *testing.T, benches map[string]map[string]string, landed int) LineupInput {
	t.Helper()
	mr, c := fixture(t)
	ctx := context.Background()
	for b, answers := range benches {
		c.SAdd(ctx, "benches", b)
		c.HSet(ctx, "bench:"+b+":beat", "at", t0.Unix())
		if err := PublishConform(ctx, c, Evaluate(b, answers, declared)); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, "s:s1", "status", "lining-up")
	if err := CutProbes(ctx, c, "s1", probes); err != nil {
		t.Fatal(err)
	}
	for _, p := range probes[:landed] {
		c.SAdd(ctx, "s:s1:idx:card:landed", p)
	}
	mr.FastForward(time.Minute)
	in, err := GatherLineup(ctx, c, "s1")
	if err != nil {
		t.Fatal(err)
	}
	in.GraphQL = GraphQLBudget{Known: true, Remaining: 5000, CallsPerPass: 0, Cadence: 10 * time.Second}
	in.BinaryScanned = true
	return in
}

func lineFor(t *testing.T, lines []Line, n string) Line {
	t.Helper()
	return find(t, lines, n)
}

func mustRefuse(t *testing.T, lines []Line, red ...string) {
	t.Helper()
	err := OpenGate(lines)
	if err == nil {
		t.Fatalf("sprint open did not refuse on %v", lines)
	}
	for _, n := range red {
		if !lineFor(t, lines, n).Red {
			t.Fatalf("%s is not RED: %v", n, lines)
		}
		if !strings.Contains(err.Error(), n) {
			t.Fatalf("refusal %q does not name %s", err, n)
		}
	}
}

// Positive control: a lined-up fleet with five landed probes opens.
func TestLineupGreenOpens(t *testing.T) {
	in := lineupFixture(t, map[string]map[string]string{"hulk": goodAnswers(), "vision": goodAnswers()}, 5)
	lines := LineupChecks(in)
	if err := OpenGate(lines); err != nil {
		t.Fatalf("a lined-up fleet refused: %v\n%s", err, RenderLineup(in, lines))
	}
	if out := RenderLineup(in, lines); !strings.Contains(out, "LINEUP 6/6 lined up") {
		t.Fatalf("tally missing:\n%s", out)
	}
}

// Control 49: nova.build != declared prints DRIFT, 7.18 is RED and sprint
// open refuses. The same with a finished job dir left behind (7.19) and with
// 4 of 5 probes landed (7.20).
func TestControl49(t *testing.T) {
	stale := goodAnswers()
	stale[KeyBuild] = "351bafe3"
	in := lineupFixture(t, map[string]map[string]string{"hulk": stale, "vision": goodAnswers()}, 5)
	lines := LineupChecks(in)
	mustRefuse(t, lines, "7.18")
	if out := RenderLineup(in, lines); !strings.Contains(out, "DRIFT hulk nova.build have=351bafe3 want=e4386caa") {
		t.Fatalf("lineup did not print the build drift:\n%s", out)
	}
	if lineFor(t, lines, "7.19").Red || lineFor(t, lines, "7.24").Red {
		t.Fatalf("a build drift reddened another key's check: %v", lines)
	}

	jobs := goodAnswers()
	jobs[KeyFinishedJobdirs] = "3"
	in = lineupFixture(t, map[string]map[string]string{"hulk": jobs}, 5)
	lines = LineupChecks(in)
	mustRefuse(t, lines, "7.19")
	if out := RenderLineup(in, lines); !strings.Contains(out, "DRIFT hulk code.finished-jobdirs have=3 want=0") {
		t.Fatalf("lineup did not print the job dir drift:\n%s", out)
	}

	in = lineupFixture(t, map[string]map[string]string{"hulk": goodAnswers()}, 4)
	lines = LineupChecks(in)
	mustRefuse(t, lines, "7.20")
	if got := lineFor(t, lines, "7.20").Why; !strings.Contains(got, "4 of 5 probes landed") || !strings.Contains(got, "probe-5") {
		t.Fatalf("7.20 does not name the unlanded probe: %q", got)
	}
}

// Control 64: a bench whose secrets clone is on seal/x with no upstream
// prints DRIFT secrets.store and sprint open refuses (7.24).
func TestControl64(t *testing.T) {
	seal := goodAnswers()
	seal[KeySecretsStore] = "branch=seal/x,upstream=no,rev=0fa4aa554bef,clean=yes"
	in := lineupFixture(t, map[string]map[string]string{"hulk": goodAnswers(), "batman": seal}, 5)
	lines := LineupChecks(in)
	mustRefuse(t, lines, "7.24")
	out := RenderLineup(in, lines)
	if !strings.Contains(out, "DRIFT batman secrets.store have=branch=seal/x,upstream=no") || !strings.Contains(out, "failed=branch,upstream") {
		t.Fatalf("lineup did not print the secrets drift:\n%s", out)
	}
	if strings.Contains(lineFor(t, lines, "7.24").Why, "hulk") {
		t.Fatalf("7.24 names the conforming bench: %v", lines)
	}
	// An undeclared secrets_rev is not a pass: no evidence is not negative evidence.
	c := Evaluate("hulk", goodAnswers(), Declared{Build: "e4386caa"})
	if c.Verdict != "DRIFT" || !strings.Contains(strings.Join(c.Keys, " "), KeySecretsStore) {
		t.Fatalf("undeclared secrets_rev passed: %+v", c)
	}
}

// Control 65: a bench with no scratch-clone verb, or missing one of the four
// mirrors, is DRIFT code.clone-verb and sprint open refuses (7.25).
func TestControl65(t *testing.T) {
	noVerb := goodAnswers()
	noVerb[KeyCloneVerb] = "verb=absent,mirrors=nova-tools:nova-work:rowan-tools:schema"
	noMirror := goodAnswers()
	noMirror[KeyCloneVerb] = "verb=ok,mirrors=nova-tools:schema"
	unanswered := goodAnswers()
	delete(unanswered, KeyCloneVerb)
	in := lineupFixture(t, map[string]map[string]string{"hulk": noVerb, "vision": noMirror, "thor": unanswered}, 5)
	lines := LineupChecks(in)
	mustRefuse(t, lines, "7.25")
	out := RenderLineup(in, lines)
	for _, want := range []string{
		"DRIFT hulk code.clone-verb have=verb=absent",
		"DRIFT vision code.clone-verb have=verb=ok,mirrors=nova-tools:schema",
		"missing=nova-work,rowan-tools",
		"MISSING thor code.clone-verb",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("lineup did not print %q:\n%s", want, out)
		}
	}
}

// No record, a stale record or an unread registry is RED, never GREEN.
func TestLineupMissingAndStaleConform(t *testing.T) {
	mr, c := fixture(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "hulk", "vision", "parked")
	c.HSet(ctx, "bench:hulk:beat", "at", t0.Unix())
	c.HSet(ctx, "bench:vision:beat", "at", t0.Unix())
	if err := PublishConform(ctx, c, Evaluate("hulk", goodAnswers(), declared)); err != nil {
		t.Fatal(err)
	}
	// The record's own TTL is 15 min; age it past the bound without expiry by
	// rewriting at, which is what a writer that stopped publishing leaves.
	c.HSet(ctx, "bench:hulk:conform", "at", t0.Add(-16*time.Minute).Unix())
	mr.FastForward(time.Second)
	in, err := GatherLineup(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	got := CheckConform(in)
	if !got.Red || !strings.Contains(got.Why, "hulk conform 960s old") || !strings.Contains(got.Why, "vision conform MISSING") {
		t.Fatalf("7.18 %v", got)
	}
	if strings.Contains(got.Why, "parked") {
		t.Fatalf("a bench with no beat is not UP: %v", got)
	}
	if l := CheckConform(LineupInput{}); !l.Red || !strings.Contains(l.Why, "MISSING") {
		t.Fatalf("an unread registry is not RED: %v", l)
	}
	if l := CheckProbes(LineupInput{}); !l.Red {
		t.Fatalf("no sprint is not RED: %v", l)
	}
}

func TestPublishRoundTripAndDeclared(t *testing.T) {
	dir := t.TempDir()
	yml := filepath.Join(dir, "all.yml")
	body := "ansible_python_interpreter: /usr/bin/python3\nnova_build: \"v0.16.0-dev.e4386caa\"\nsecrets_rev: \"0fa4aa554bef\"   # the declared HEAD\n"
	if err := os.WriteFile(yml, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDeclared(yml)
	if err != nil || d != declared {
		t.Fatalf("declared %+v %v", d, err)
	}
	answers := ParseProbe(strings.NewReader("os=Darwin\nnova.build=351bafe3\nsecrets.store=branch=main,upstream=yes,rev=0fa4aa554bef,clean=yes\n"))
	if answers[KeySecretsStore] != "branch=main,upstream=yes,rev=0fa4aa554bef,clean=yes" {
		t.Fatalf("probe parse %v", answers)
	}
	_, c := fixture(t)
	ctx := context.Background()
	if err := PublishConform(ctx, c, Evaluate("hulk", answers, declared)); err != nil {
		t.Fatal(err)
	}
	h := c.HGetAll(ctx, "bench:hulk:conform").Val()
	if h["verdict"] != "DRIFT" || h["build_have"] != "351bafe3" || h["build_want"] != "e4386caa" || h["at"] != "1790000000" {
		t.Fatalf("record %v", h)
	}
	if ttl := c.TTL(ctx, "bench:hulk:conform").Val(); ttl != ConformTTL {
		t.Fatalf("ttl %v", ttl)
	}
	if err := CutProbes(ctx, c, "s1", probes[:4]); err == nil {
		t.Fatal("four probes were cut")
	}
	if err := CutProbes(ctx, c, "s1", probes); err != nil {
		t.Fatal(err)
	}
	if err := CutProbes(ctx, c, "s1", probes); err != nil {
		t.Fatalf("the same five again is not a change: %v", err)
	}
	other := append([]string{"probe-x"}, probes[1:]...)
	if err := CutProbes(ctx, c, "s1", other); err == nil {
		t.Fatal("a second, different probe cut overwrote the first")
	}
	var _ redis.Cmdable = c
}

func TestGraphQLBudget(t *testing.T) {
	in := LineupInput{BinaryScanned: true, GraphQL: GraphQLBudget{Known: true, Remaining: 100, CallsPerPass: 1, Cadence: 10 * time.Second}}
	if l := CheckGraphQL(in); !l.Red || !strings.Contains(l.Why, "100 < 360") {
		t.Fatalf("7.21 %v", l)
	}
	in.GraphQL.Remaining = 360
	if l := CheckGraphQL(in); l.Red {
		t.Fatalf("7.21 %v", l)
	}
	in.BinaryHits = []string{"/graph" + "ql"}
	if l := CheckGraphQL(in); !l.Red {
		t.Fatalf("a binary that calls GraphQL is not RED: %v", l)
	}
	if l := CheckGraphQL(LineupInput{}); !l.Red || !strings.Contains(l.Why, "MISSING") {
		t.Fatalf("unread budget %v", l)
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(bin, []byte("xx POST /graph"+"ql yy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hits, err := ScanBinaryForGraphQL(bin); err != nil || len(hits) != 1 {
		t.Fatalf("scan %v %v", hits, err)
	}
}

// graphQLUse is what a GraphQL call looks like in Go source: the endpoint, the
// gh subcommand as an argv literal, or the v4 client. Lowercase and exact, so
// the words in this package's own identifiers and comments do not match.
var graphQLUse = regexp.MustCompile(`/graph` + `ql\b|"graph` + `ql"|api graph` + `ql|githubv4|shurcooL`)

// TestNoGraphQLInNovaSprint is 7.21's half that holds at the binary's version:
// no production file behind nova-sprint calls GitHub GraphQL. The rowan-claude
// GraphQL budget emptied on 2026-09-22 and the lander made 149 blind passes;
// nova-sprint reads GitHub by REST only.
func TestNoGraphQLInNovaSprint(t *testing.T) {
	if !graphQLUse.MatchString(`exec.Command("gh", "api", "graph`+`ql", "-f", q)`) ||
		!graphQLUse.MatchString(`const endpoint = "/graph`+`ql"`) {
		t.Fatal("the pattern misses a known GraphQL call; the test would pass on nothing")
	}
	root := moduleRoot(t)
	n, sawSelf := 0, false
	for _, dir := range []string{"cmd/nova-sprint", "internal/nsprint"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			n++
			sawSelf = sawSelf || strings.HasSuffix(filepath.ToSlash(p), "internal/nsprint/preflight/lineup.go")
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, l := range strings.Split(string(body), "\n") {
				if graphQLUse.MatchString(l) {
					rel, _ := filepath.Rel(root, p)
					t.Errorf("%s:%d calls GitHub GraphQL; nova-sprint is REST only (7.21): %s", rel, i+1, strings.TrimSpace(l))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 5 || !sawSelf {
		t.Fatalf("read %d files under cmd/nova-sprint and internal/nsprint, lineup.go read %v; the walk is broken", n, sawSelf)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = up
	}
}
