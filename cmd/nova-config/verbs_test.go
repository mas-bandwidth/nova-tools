package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// words splits an example line as a shell does for the lines the examples
// hold: blanks between words, and a single-quoted word kept whole.
func words(line string) []string {
	var out []string
	var cur strings.Builder
	quoted, inWord := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			quoted, inWord = !quoted, true
		case r == ' ' && !quoted:
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
			}
			inWord = false
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// Every kind's worked examples, the ones each verb's -h prints, run as
// printed and in order on one --file store, each exiting 0; the rows another
// row names are let go first, as a reader would.
func TestEveryKindsExamplesRunInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	step := func(line string) {
		t.Helper()
		args := words(line)
		require.Equal(t, "nova-config", args[0], line)
		code, out, errs := h.run(t, args[1:]...)
		assert.Equal(t, 0, code, "%s\nstdout: %s\nstderr: %s", line, out, errs)
		assert.Empty(t, errs, line)
	}
	step(toolExamples["migrate"])
	clear := []string{
		"nova-config sprint set --coordinator '' --as a1 --file try.json",
		"nova-config fleet set --coordinator '' --store '' --as a1 --file try.json",
		"nova-config tier set flash --routes '' --as a1 --file try.json",
	}
	for _, e := range kindExamples {
		if e.verb == "remove" && clear != nil {
			for _, c := range clear {
				step(c)
			}
			clear = nil
		}
		step(e.line)
	}
	step(toolExamples["status"])
	assert.Zero(t, h.redis.opens, "the examples need no Redis but apply's")
}

// Every verb's -h states its effect, and every write verb takes --dry-run:
// the walk tool-answers makes, here for every kind's verbs.
func TestEveryVerbsHelpStatesItsEffect(t *testing.T) {
	t.Parallel()
	h := newHarness()
	verbs := [][]string{{"version"}, {"kinds"}, {"migrate"}, {"status"}, {"apply"}, {"inventory"}, {"machine", "width"}, {"machine", "self"}}
	for _, k := range config.Kinds {
		for _, v := range []string{"add", "set", "remove", "list", "show", "history"} {
			if k.Singleton && (v == "add" || v == "remove" || v == "list") {
				continue
			}
			verbs = append(verbs, []string{k.Name, v})
		}
	}
	for _, v := range verbs {
		code, out, errs := h.run(t, append(v, "-h")...)
		name := strings.Join(v, " ")
		require.Equal(t, 0, code, "%s -h: %s", name, errs)
		assert.Contains(t, out, "\neffect: ", "%s -h states no effect", name)
		writes := strings.Contains(out, "\neffect: store write") || strings.Contains(out, "\neffect: external delivery")
		if writes {
			assert.Contains(t, out, "--dry-run", "%s writes and takes no --dry-run", name)
		}
		if name != "inventory" {
			assert.Contains(t, out, "--json", "%s -h has no --json", name)
		}
	}
	_, out, _ := h.run(t, "machine", "add", "-h")
	assert.Contains(t, out, "--seat <text>  required; text: ")
	assert.Contains(t, out, "--width <number>  number: ")
	assert.Contains(t, out, "required: --user --seat --slots (and --as)")
	assert.Contains(t, out, "example: nova-config machine add m1 ")
	_, out, _ = h.run(t, "machine", "set", "-h")
	assert.NotContains(t, out, "required:", "set requires no field")
}

// --dry-run on add, set and remove prints the change the write would record
// and writes nothing; a refusal is the write's own.
func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	run := func(args ...string) (int, string, string) {
		t.Helper()
		return h.run(t, append(args, "--as", "a1", "--file", "try.json")...)
	}
	code, _, errs := h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	code, out, errs := run("machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG DRY-RUN op=add kind=machine name=m1 actor=a1 wrote=nothing note=- runners=0 seat=s slots=8 tla=false user=u width=-\nNOTE machine=m1 width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set m1 --width <n> (0: no member) --as a1 --file try.json\n", out)
	code, out, _ = h.run(t, "machine", "list", "--file", "try.json")
	require.Equal(t, 0, code)
	assert.Equal(t, "CONFIG LIST kind=machine rows=0\n", out, "the dry run added nothing")
	code, _, errs = run("machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4")
	require.Equal(t, 0, code, errs)
	code, out, errs = run("machine", "set", "m1", "--width", "6", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=machine name=m1 actor=a1 wrote=nothing width=4>6\n", out)
	code, out, errs = run("machine", "remove", "m1", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.True(t, strings.HasPrefix(out, "CONFIG DRY-RUN op=remove kind=machine name=m1 actor=a1 wrote=nothing "), out)
	code, _, errs = run("machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--dry-run")
	assert.Equal(t, 1, code, "a dry run is refused as the write would be")
	assert.Contains(t, errs, "machine add REFUSED: machine m1 exists")
	code, out, _ = h.run(t, "machine", "history", "m1", "--file", "try.json")
	require.Equal(t, 0, code)
	assert.Equal(t, 1, strings.Count(out, "HISTORY id="), "one history row, the real add:\n%s", out)
	code, out, errs = h.run(t, "migrate", "--dry-run", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.True(t, strings.HasSuffix(out, "applied=0 dry_run=true pending=0 missing=0 role=- ready=yes\n"), out)
}

// migrate --dry-run prints the ledger, not the greatest version alone: each
// migration applied, pending (migrate applies it) or missing (below the
// greatest recorded, so migrate will not), and a NOTE for a missing one.
func TestMigrateDryRunPrintsTheLedger(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	n := currentSchema()
	h.store.version = n - 1
	h.store.ledger = nil
	for v := 1; v < n; v++ {
		if v != n-2 {
			h.store.ledger = append(h.store.ledger, v)
		}
	}
	code, out, errs := h.run(t, "migrate", "--dry-run")
	require.Equal(t, 0, code, errs)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Len(t, lines, n+2, out)
	assert.True(t, strings.HasSuffix(lines[n-3], " state=missing"), lines[n-3])
	assert.True(t, strings.HasSuffix(lines[n-2], " state=applied"), lines[n-2])
	assert.True(t, strings.HasSuffix(lines[n-1], " state=pending"), lines[n-1])
	assert.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=0 dry_run=true pending=1 missing=1 role=nova_config ready=yes", n-1, n), lines[n])
	assert.Equal(t, fmt.Sprintf("NOTE version(s) %d are not in the ledger and are below %d, the greatest recorded: migrate applies only versions above it, so it will not apply them", n-2, n-1), lines[n+1])
	assert.Equal(t, n-1, h.store.version, "a dry run applies nothing")
}

// --json is one object in internal/tool's shape, the same facts as the
// lines.
func TestJSONIsOneObjectOfTheSameResult(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	code, _, errs := h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	type result struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]any `json:"facts"`
		Items []struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
		Notes []string `json:"notes"`
	}
	decode := func(args ...string) result {
		t.Helper()
		code, out, errs := h.run(t, append(args, "--json")...)
		require.Equal(t, 0, code, "%v: %s", args, errs)
		require.Equal(t, 1, strings.Count(out, "\n"), "%v: one line: %s", args, out)
		var r result
		require.NoError(t, json.Unmarshal([]byte(out), &r), out)
		assert.Equal(t, "ok", r.Result.Status)
		return r
	}
	r := decode("kinds")
	assert.Equal(t, "kinds", r.Result.Verb)
	assert.EqualValues(t, len(config.Kinds), r.Facts["count"])
	assert.Equal(t, "machine", r.Items[0].Fields["name"])
	r = decode("machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--as", "a1", "--file", "try.json")
	assert.Equal(t, "machine add", r.Result.Verb)
	assert.EqualValues(t, 1, r.Facts["rev"])
	require.Len(t, r.Notes, 1, "the width NOTE is a note of the result")
	r = decode("machine", "list", "--file", "try.json")
	assert.EqualValues(t, 1, r.Facts["rows"])
	assert.Equal(t, "8", r.Items[0].Fields["slots"])
	r = decode("machine", "show", "m1", "--file", "try.json")
	assert.Equal(t, "m1", r.Items[0].Fields["name"])
	assert.NotEmpty(t, r.Items[0].Fields["created"])
	r = decode("machine", "history", "m1", "--file", "try.json")
	assert.Equal(t, "add", r.Items[0].Fields["op"])
	r = decode("migrate", "--print")
	assert.Len(t, r.Items, currentSchema())
	r = decode("status", "--file", "try.json")
	assert.EqualValues(t, currentSchema(), r.Facts["schema"])
	r = decode("machine", "self")
	assert.Equal(t, "elsewhere", r.Facts["machine"])
}

// An unknown flag names the verb's flags and the nearest, never the flag
// package's stock line; a flag with no value says it wants one.
func TestAMistypedFlagIsAnsweredWithTheFlagsThereAre(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, out, errs := h.run(t, "machine", "list", "--jsno")
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-config machine list REFUSED: unknown flag --jsno (nearest: --json); this verb takes --file, --json, --pg, --redis; run: nova-config machine list -h\n", errs)
	code, _, errs = h.run(t, "machine", "list", "--pg")
	assert.Equal(t, 2, code)
	assert.Equal(t, "nova-config machine list REFUSED: --pg wants a value; run: nova-config machine list -h\n", errs)
}
