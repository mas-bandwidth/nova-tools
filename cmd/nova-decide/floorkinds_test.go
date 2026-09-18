package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// The floor is PER KIND at the verb, and every line says where its floor came
// from. A reader cannot tell a measured 0.90 from the default nobody has tuned
// by looking at the number.
func TestRouteUsesTheRegistrysFloorForTheKind(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		floor string
		from  string
	}{
		"measured kind": {[]string{"--kind", "fleet-chore", "--files", "1"}, "floor=0.70", "floor_from=kind"},
		"another kind":  {[]string{"--kind", "fix-with-red-test", "--files", "3", "--packages", "1"}, "floor=0.73", "floor_from=kind"},
		"no row":        {[]string{"--kind", "cause-to-find", "--files", "3", "--packages", "1"}, "floor=0.90", "floor_from=built-in"},
		"flag wins":     {[]string{"--kind", "fleet-chore", "--files", "1", "--floor", "0.5"}, "floor=0.50", "floor_from=flag"},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"route", "--no-jev", "--unit-id", "f-1"}, tc.args...)
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("%s: exit = %d (stderr=%q)", name, code, stderr.String())
			continue
		}
		for _, want := range []string{tc.floor, tc.from} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("%s: want %s in %s", name, want, stdout.String())
			}
		}
	}
}

// A registry the caller names carries its own floors, and they are the ones
// used: the table is data, like the ladder.
func TestRouteTakesTheFloorFromTheRegistryGiven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{
	  "minds":[{"name":"tiny","lineage":"deepseek","height":0,"availability":"available","ask":"card"},
	           {"name":"guardian","lineage":"johnny","height":3,"kinds":["guard","fresh-take"],"lanes":["security"],"availability":"reserved","ask":"bus"}],
	  "floors":[{"kind":"rebase","floor":0.25,"from":"a bench of its own"}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--no-jev", "--registry", path, "--unit-id", "r-1", "--kind", "rebase", "--files", "2", "--packages", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "floor=0.25") || !strings.Contains(stdout.String(), "floor_from=kind") {
		t.Errorf("the registry's own floor answers: %s", stdout.String())
	}
}

// The floor's source travels into the log row too, so the ledger can tell a
// measured floor from an untuned one a week later.
func TestTheLogRowCarriesTheFloorSourceAndTheRead(t *testing.T) {
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--no-jev", "--unit-id", "l-1", "--kind", "fleet-chore",
		"--files", "1", "--touches", "secrets", "--log", log}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var e decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.FloorFrom != decide.FloorFromKind || e.Floor != 0.70 {
		t.Errorf("floor %.2f from %q, want 0.70 from kind", e.Floor, e.FloorFrom)
	}
	if e.Read != "johnny" {
		t.Errorf("read = %q, want johnny: the row says who reads the work", e.Read)
	}
	if e.RungTried == "johnny" {
		t.Errorf("a reserved mind takes no work: %s", raw)
	}
}

// usd on a usage row. Token spend reporting is an obligation, and the column
// was a dash on every rc=0 row: the ledger counted tokens and never a cent.
// With a rate in the registry it is priced; with none it stays a dash and a
// NOTE names the model and the row to add.
func TestUsageRowIsPricedFromTheRateTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, []byte(`{
	  "minds":[{"name":"opus","lineage":"rowan","height":2,"availability":"available","ask":"child"},
	           {"name":"sol","lineage":"stella","height":2,"availability":"available","ask":"child"},
	           {"name":"emma","lineage":"emma","height":3,"availability":"available","ask":"bus"},
	           {"name":"guardian","lineage":"johnny","height":3,"kinds":["guard","fresh-take"],"availability":"reserved","ask":"bus"}],
	  "rates":[{"model":"jev-latest","provider":"typesafe","input_usd_per_mtok":3,"output_usd_per_mtok":15}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	useFake(t, &fake{conf: 0.99, usage: decide.Usage{InputTokens: 511, HasInput: true, OutputTokens: 61, HasOutput: true}})
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--registry", path, "--unit-id", "p-1", "--kind", "new-verb",
		"--files", "3", "--packages", "1", "--usage", usage, "--log", log}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	row := lastUsageRow(t, usage)
	// 511 in at $3/Mtok plus 61 out at $15/Mtok.
	if row["usd"] != "0.002448" {
		t.Errorf("usd = %q, want 0.002448 priced from the rate table", row["usd"])
	}
	if strings.Contains(stderr.String(), "ROUTE NOTE") {
		t.Errorf("a priced row needs no note: %q", stderr.String())
	}

	// No rate for the model: a dash, and a NOTE naming it and the remedy.
	stdout.Reset()
	stderr.Reset()
	usage2 := filepath.Join(dir, "usage2.tsv")
	code = run([]string{"route", "--unit-id", "p-2", "--kind", "new-verb",
		"--files", "3", "--packages", "1", "--usage", usage2, "--log", log}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	row = lastUsageRow(t, usage2)
	if row["usd"] != "-" {
		t.Errorf("usd = %q, want the dash: a price nobody published is not invented", row["usd"])
	}
	if row["tokens_in"] != "511" {
		t.Errorf("the tokens are still counted: %q", row["tokens_in"])
	}
	if !strings.Contains(stderr.String(), "ROUTE NOTE") || !strings.Contains(stderr.String(), decide.DefaultModel) {
		t.Errorf("the note must name the model with no rate: %q", stderr.String())
	}
}

// lastUsageRow reads the final row of a usage TSV as a map of column to value.
func lastUsageRow(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("%s holds no row: %q", path, raw)
	}
	head := strings.Split(lines[0], "\t")
	last := strings.Split(lines[len(lines)-1], "\t")
	out := map[string]string{}
	for i, name := range head {
		if i < len(last) {
			out[name] = last[i]
		}
	}
	return out
}

// tune --propose-floors reads an escalation log and proposes a floor per kind,
// and --write puts them in a registry the next route will use.
func TestTuneProposesAndWritesTheFloors(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "decide.jsonl")
	if err := os.WriteFile(log, []byte(strings.Join([]string{
		`{"unit":"a","kind":"new-verb","source":"jev","confidence":0.71,"rung_tried":"emma"}`,
		`{"unit":"b","kind":"new-verb","source":"jev","confidence":0.75,"rung_tried":"emma"}`,
		`{"unit":"c","kind":"new-verb","source":"jev","confidence":0.80,"rung_tried":"emma"}`,
		`{"unit":"d","kind":"new-verb","source":"jev","confidence":0.83,"rung_tried":"emma"}`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "registry.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"tune", "--propose-floors", "--log", log, "--write", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	// p25 of 0.71 0.75 0.80 0.83 by nearest rank is the first of four.
	if !strings.Contains(stdout.String(), "kind=new-verb") || !strings.Contains(stdout.String(), "floor=0.71") {
		t.Errorf("the proposal:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUNE FLOORS WRITTEN floors=1") {
		t.Errorf("the finish names what was written:\n%s", stdout.String())
	}
	reg, err := decide.LoadRegistry(out)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := reg.FloorFor(decide.KindNewVerb)
	if !ok || f.Floor != 0.71 {
		t.Fatalf("the written floor is %+v, %v", f, ok)
	}
	if !strings.Contains(f.From, "decide.jsonl") {
		t.Errorf("a floor says which log it was read off: %q", f.From)
	}
	// And the next route is gated on it.
	stdout.Reset()
	if code := run([]string{"route", "--no-jev", "--registry", out, "--unit-id", "n-1",
		"--kind", "new-verb", "--files", "3", "--packages", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "floor=0.71") {
		t.Errorf("the written floor is the one the next decision is gated on: %s", stdout.String())
	}
}

// A hand-named floor above what the provider has ever answered for that kind is
// refused with the remedy, and nothing is written.
func TestTuneRefusesAFloorAboveTheObservedMax(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "decide.jsonl")
	if err := os.WriteFile(log, []byte(strings.Join([]string{
		`{"unit":"a","kind":"new-verb","source":"jev","confidence":0.71,"rung_tried":"emma"}`,
		`{"unit":"b","kind":"new-verb","source":"jev","confidence":0.78,"rung_tried":"emma"}`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "registry.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"tune", "--propose-floors", "--log", log, "--write", out, "--floor-for", "new-verb=0.95"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "floor-above-observed") || !strings.Contains(stderr.String(), "0.78") {
		t.Errorf("the refusal names the observed max and the remedy: %q", stderr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused floor is not written")
	}
}

// --propose-floors wants a log: a floor with no rows behind it is untuned.
func TestProposeFloorsWantsALog(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"tune", "--propose-floors"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--log") {
		t.Errorf("the refusal names the flag: %q", stderr.String())
	}
}

// Every sub-verb answers --help on stdout at exit 0, with its own line and no
// other verb's (the #1358 class). `flag: help requested` at exit 2 is package
// flag's sentinel handed to somebody who asked a reasonable question.
func TestEverySubVerbAnswersHelp(t *testing.T) {
	for _, verb := range []string{"route", "help", "log", "tune"} {
		for _, flag := range []string{"--help", "-h"} {
			var stdout, stderr bytes.Buffer
			code := run([]string{verb, flag}, &stdout, &stderr)
			if code != 0 {
				t.Errorf("%s %s: exit = %d (stderr=%q)", verb, flag, code, stderr.String())
				continue
			}
			if stderr.Len() != 0 {
				t.Errorf("%s %s: an answer belongs on stdout: %q", verb, flag, stderr.String())
			}
			if !strings.Contains(stdout.String(), "nova-decide "+verb) {
				t.Errorf("%s %s: the answer must carry that verb's own line: %q", verb, flag, stdout.String())
			}
			if strings.Contains(stdout.String(), "flag: help requested") {
				t.Errorf("%s %s: package flag's sentinel reached a person: %q", verb, flag, stdout.String())
			}
			for _, other := range []string{"route", "help", "log", "tune"} {
				if other == verb {
					continue
				}
				if strings.Contains(stdout.String(), "  nova-decide "+other+" ") {
					t.Errorf("%s %s: answered with %s's line too: %q", verb, flag, other, stdout.String())
				}
			}
		}
	}
	// A flag that is genuinely wrong is still a refusal, at exit 2.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--nope"}, &stdout, &stderr); code != 2 {
		t.Errorf("a bad flag is a refusal: exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "REFUSED reason=bad-flags") {
		t.Errorf("want the refusal line: %q", stderr.String())
	}
}
