package pulse

// #1950: `harvest --bench` on a SHARED launched directory.
//
// The launched directory of a pull queue is shared -- seven manager lanes drop cards into
// one ready/ and the resident fill loops move them into one launched/ -- and on 2026-09-19
// one `nova-pulse harvest --bench vision --max 1` emptied a live 151-card queue in 733 ms:
//
//	HARVEST DRAIN card=card-pull-11.md lane=- state=failed bench=captainamerica why=job-dir-gone
//	HARVEST MORE kind=event shown=1 total=151 use --max 0 to show all
//	HARVEST BENCH OK bench=vision jobs=0 done=0 pushed=0 prs=0 no-commit=0 skipped=0 drained=151 took=733ms
//
// Every card was marked failed, every `.launched` marker was DELETED, and five of the cards
// belonged to other lanes whose jobs were alive on other benches. `jobs=0 ... drained=151`
// on one line is the whole defect.
//
// These tests drive the production path -- Harvest(HarvestInput) -- through the same two
// seams the rest of this package's bench tests use, and assert on a before/after listing of
// the shared directory with a sha256 per file: everything this harvest did not consume is
// byte-identical afterwards.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// launchedCard is one card of the shared queue, with the launch record `fill` wrote beside
// it: whose it is (session), which lane it holds, and which bench its job is on.
type launchedCard struct {
	name, lane, bench, session string
}

// writeSharedQueue lays down a shared launched directory: each card and its `.launched`
// marker, in the exact shape writeLaunchedMarker writes.
func writeSharedQueue(t *testing.T, launched string, cards []launchedCard) {
	t.Helper()
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if err := os.WriteFile(filepath.Join(launched, c.name), []byte("RESULT "+c.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("lane=%s\nbench=%s\nlabel=%s\nsession=%s\ncard=%s\nat=2026-09-19T22:40:00Z\n",
			c.lane, c.bench, strings.TrimSuffix(c.name, ".md"), c.session, c.name)
		if err := os.WriteFile(filepath.Join(launched, c.name+".launched"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// listing is every file in a directory with the sha256 of its contents: the before/after
// evidence that a file nobody consumed is byte-identical, name and content both.
func listing(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		sum := sha256.Sum256(raw)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

// diffListing reports every file that appeared, vanished or changed between two listings.
func diffListing(before, after map[string]string) []string {
	var out []string
	for name, sum := range before {
		switch got, ok := after[name]; {
		case !ok:
			out = append(out, "gone: "+name)
		case got != sum:
			out = append(out, "changed: "+name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			out = append(out, "new: "+name)
		}
	}
	sort.Strings(out)
	return out
}

// TestHarvestConsumesOnlyItsOwnFinishedCardOnASharedQueue is #1950's rule in one run: three
// owners' records in one launched directory, two of the caller's own jobs finished, one of
// its own still running, two of them on other benches and other sessions. `--bench vision
// --session fill-loop-0919b --max 1` must consume exactly ONE of its own finished cards and
// leave every other file in the directory byte-identical, marker included.
func TestHarvestConsumesOnlyItsOwnFinishedCardOnASharedQueue(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		// The caller's own, both finished on vision: --max 1 takes the first.
		{"card-9601.md", "schema", "vision", "fill-loop-0919b"},
		{"card-9602.md", "pulse", "vision", "fill-loop-0919b"},
		// Another lane's, alive on another bench. The drain printed
		// `bench=captainamerica` under `--bench vision` and moved it anyway.
		{"card-pull-11.md", "pull", "captainamerica", "tools18-0919b"},
		// Another lane's again, on a bench this harvest never looked at.
		{"card-schema-419.md", "schema-419", "hulk", "schema12-0919b"},
		// The caller's own, still running on vision.
		{"card-9603.md", "bus", "vision", "fill-loop-0919b"},
	})
	before := listing(t, launched)

	visionRoot := "/home/gaffer/rowan-swarm-root"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		if bench != "vision" {
			t.Errorf("the harvest opened a shell to %q; only --bench vision was named", bench)
		}
		return benchJobListing(visionRoot+"/0/jobs/card-9601", []string{
			"RESULT card-9601 sha=abc", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
			"SESSION fill-loop-0919b",
		}) + benchJobListing(visionRoot+"/1/jobs/card-9602", []string{
			"RESULT card-9602 sha=def", "BRANCH rowan/card-9602", "REPO mas-bandwidth/nova-tools",
			"SESSION fill-loop-0919b",
		}) + benchJobListing(visionRoot+"/2/jobs/card-9603", nil), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Root = visionRoot
	in.Session = "fill-loop-0919b"
	in.Launched = launched
	in.Max = 1
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}

	// Exactly one card was consumed, and it is the caller's own finished one.
	if !strings.Contains(out, "drained=1 left=4") {
		t.Errorf("the summary does not say one drained and four left alone:\n%s", out)
	}
	moved := filepath.Join(root, "queue", "done", "card-9601.md")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the consumed card did not reach the done directory: %v", err)
	}
	// Its receipt names the lane that was released and why. (The `HARVEST DRAIN` line
	// itself is elided here: --max 1 is one EVENT line, and the HARVEST JOB of the fold
	// spent it. The bounded-output rule and the consumption bound are one flag.)
	notes, _ := filepath.Glob(filepath.Join(root, "queue", "done", "card-9601.md.done-*"))
	if len(notes) != 1 {
		t.Fatalf("the consumed card carries %d done markers, want 1", len(notes))
	}
	if note, _ := os.ReadFile(notes[0]); !strings.Contains(string(note), "lane=schema") ||
		!strings.Contains(string(note), "why=result") {
		t.Errorf("the done marker does not name the lane and the reason: %q", note)
	}
	// The launch record MOVED with its card. It is the only record of which bench the
	// job is on; 149 of them had to be rebuilt by hand after this deleted them.
	if _, err := os.Stat(filepath.Join(root, "queue", "done", "card-9601.md.launched")); err != nil {
		t.Errorf("the launched marker was not moved with its card: %v", err)
	}

	// Everything else in the shared directory is exactly as it was found.
	after := listing(t, launched)
	for _, name := range []string{"card-9601.md", "card-9601.md.launched"} {
		delete(before, name)
	}
	if diff := diffListing(before, after); len(diff) > 0 {
		t.Errorf("the harvest touched files that were not its own:\n%s\n%s", strings.Join(diff, "\n"), out)
	}
	for _, name := range []string{"card-9602.md", "card-pull-11.md", "card-schema-419.md", "card-9603.md"} {
		if _, ok := after[name+".launched"]; !ok {
			t.Errorf("%s lost its launched marker: the card can no longer be harvested by anyone", name)
		}
	}
	if n := len(after); n != 8 {
		t.Errorf("the shared directory holds %d files, want 8 (four cards and four markers)", n)
	}
}

// TestHarvestDrainsNothingWhenTheBenchListedNoJobs is the 733 ms receipt itself: a listing
// that found nothing on ONE bench proves nothing about a directory holding every bench's
// cards. `jobs=0` and `drained=151` on one line was the defect; `jobs=0` must drain nothing.
func TestHarvestDrainsNothingWhenTheBenchListedNoJobs(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-pull-11.md", "pull", "captainamerica", "tools18-0919b"},
		{"card-pull-12.md", "pull", "hulk", "tools18-0919b"},
		{"card-9601.md", "schema", "vision", "fill-loop-0919b"},
	})
	before := listing(t, launched)

	shell := &fakeShell{answer: func(bench, script string) (string, error) { return "", nil }}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "fill-loop-0919b"
	in.Launched = launched
	in.Max = 1
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "jobs=0") || !strings.Contains(out, "drained=0 left=3") {
		t.Errorf("a harvest that found no jobs drained something:\n%s", out)
	}
	if strings.Contains(out, "HARVEST DRAIN ") {
		t.Errorf("a harvest that found no jobs printed a drain line:\n%s", out)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("a harvest that found no jobs changed the shared queue:\n%s", strings.Join(diff, "\n"))
	}
}

// TestHarvestRefusesARootThatDoesNotResolveOnTheBench is #1950's first defect: `--root
// '~/rowan-working/tmp'` reached the verb with the tilde unexpanded and nothing refused it,
// so the listing was empty and the drain read that emptiness as "every job is gone". A root
// that does not resolve ON THE BENCH is a refusal before any state changes.
func TestHarvestRefusesARootThatDoesNotResolveOnTheBench(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "fill-loop-0919b"},
		{"card-pull-11.md", "pull", "captainamerica", "tools18-0919b"},
	})
	before := listing(t, launched)

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		return "ROOT\t~/rowan-working/tmp\tmissing\n", nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Root = "~/rowan-working/tmp"
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (a refusal that never started)\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(errb, "HARVEST REFUSED") || !strings.Contains(errb, "does not exist on") {
		t.Errorf("the refusal does not name the root and the bench:\n%s", errb)
	}
	if strings.Contains(out, "HARVEST BENCH") {
		t.Errorf("a refused harvest printed its summary:\n%s", out)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("a refused harvest changed the shared queue:\n%s", strings.Join(diff, "\n"))
	}
}

// TestBenchListScriptAsksWhetherEachRootIsThere holds the one line of shell the refusal
// above reads: the listing answers ROOT <path> ok|missing for every root it was given.
func TestBenchListScriptAsksWhetherEachRootIsThere(t *testing.T) {
	script := benchListScript([]string{"~/rowan-working/tmp", "/home/gaffer/rowan-swarm-root"})
	for _, want := range []string{
		"if [ -d '~/rowan-working/tmp' ]",
		"if [ -d '/home/gaffer/rowan-swarm-root' ]",
		"ROOT\\t%s\\tmissing",
		"ROOT\\t%s\\tok",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the listing script does not hold %q:\n%s", want, script)
		}
	}
	jobs, missing, incomplete := parseBenchJobs("ROOT\t/a\tok\nROOT\t/b\tmissing\nROOT\t/c\tincomplete\n" +
		benchJobListing("/a/0/jobs/card-1", nil))
	if len(jobs) != 1 {
		t.Errorf("the parser read %d jobs past the ROOT lines, want 1", len(jobs))
	}
	if len(missing) != 1 || missing[0] != "/b" {
		t.Errorf("the parser reported missing=%v, want [/b]", missing)
	}
	if len(incomplete) != 1 || incomplete[0] != "/c" {
		t.Errorf("the parser reported incomplete=%v, want [/c]", incomplete)
	}
}

// TestHarvestLeavesACardWhoseJobFailedToFetch is the durability half of the rule: a marker
// is removed only after its result has been HARVESTED. A job whose fetch failed was never
// folded, so its card keeps its lane and stays where the fill loop put it.
func TestHarvestLeavesACardWhoseJobFailedToFetch(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "fetch", Exit: 1, Stdout: "fatal: could not read from remote"},
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
	}})
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{{"card-9601.md", "schema", "vision", "s-42"}})
	before := listing(t, launched)

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/card-9601", []string{
			"RESULT card-9601 sha=abc", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
			"SESSION s-42",
		}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "s-42"
	in.Launched = launched
	code, out, _ := runBenchHarvest(t, in)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (a fetch failed)\n%s", code, out)
	}
	if !strings.Contains(out, "HARVEST FETCH-FAIL") {
		t.Fatalf("the job was never fetched, so this proves nothing:\n%s", out)
	}
	if !strings.Contains(out, "drained=0 left=1") {
		t.Errorf("a card whose job was never folded was drained:\n%s", out)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("the card of an unharvested job moved:\n%s", strings.Join(diff, "\n"))
	}
}
