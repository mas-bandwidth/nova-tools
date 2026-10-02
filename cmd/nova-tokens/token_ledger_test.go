package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// ledgerRedis is the Redis the ledger/report verbs are pointed at for the test: a miniredis
// on loopback, reached through the real client and the real --redis flag.
func ledgerRedis(t *testing.T) (string, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.Server().SetPreHook(ledgerFixtureACL)
	return mr.Addr(), mr
}

// Miniredis lacks acl_check_cmd. These unrestricted fixtures permit the two
// ledger write commands; denied permissions are covered by real-Redis functional
// tests in internal/record. Production never substitutes this implementation.
func ledgerFixtureACL(_ *server.Peer, cmd string, args ...string) bool {
	if cmd == "EVAL" && len(args) > 0 {
		args[0] = "redis.acl_check_cmd = function(command) return command == 'DEL' or command == 'HSET' end\n" + args[0]
	}
	return false
}

// saveDay writes the day file for day under out holding rows, as a fold would.
func saveDay(t *testing.T, out, day string, rows ...tokens.DayRow) {
	t.Helper()
	d := tokens.DayFile{Day: day, At: "2026-09-14T00:00:00Z", Build: "test", Turns: strconv.Itoa(len(rows)), Sources: []string{"openai:o"}, Rows: rows}
	require.NoError(t, d.Save(out))
}

// gpt is a day row of model gpt on repo with the counts in type order (input, output,
// cache_write, cache_read, reasoning); -1 is a dash, a type the row did not report.
func gpt(day, repo string, cells ...int64) tokens.DayRow {
	var c tokens.Counts
	for ty, n := range cells {
		if n >= 0 {
			c.Set(tokens.Type(ty), n)
		}
	}
	return tokens.DayRow{Date: day, Model: "gpt", Repo: repo, Counts: c, Basis: "utc", Sources: []string{"openai:o"}}
}

// TestTheMonthlyTokenReportFromTheRedisLedgerEqualsTheFoldedTsv is docs/SPEC-STATE.md's
// test 17 (#2201; recut of #3243 on Redis under #2623, Postgres retired): the day rows the
// fold writes are indexed into tokens:ledger:<day>, `report --redis` is a GROUP BY over the
// month's day hashes, and it equals the folded day TSVs for every one of the five types and
// every (day, model, repo) -- while the day files themselves are left byte for byte as the
// fold wrote them.
func TestTheMonthlyTokenReportFromTheRedisLedgerEqualsTheFoldedTsv(t *testing.T) {
	t.Parallel()

	dsn, mr := ledgerRedis(t)
	b := newBench(t)
	// Two days folded by the real fold from Claude transcripts: two models, two repos,
	// cache writes and reads apart.
	b.transcript("a.jsonl",
		msg("a1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 40, "cache_creation_input_tokens": 7, "cache_read_input_tokens": 900}, "/x/schema/a.go"),
		msg("a2", "2026-09-11T11:00:00Z", "opus", map[string]int{"input_tokens": 30, "output_tokens": 9, "cache_read_input_tokens": 11}, "/x/serialize/b.go"),
		msg("a3", "2026-09-12T09:00:00Z", "fable", map[string]int{"input_tokens": 5, "output_tokens": 3, "cache_creation_input_tokens": 2}, "/x/serialize/c.go"))
	for _, day := range []string{"2026-09-11", "2026-09-12"} {
		novaTokens.Do(t, "fold", "--out", b.out, "--day", day, "--repos", b.repos, "--claude", "bench="+b.tr).Exit(0)
	}
	// A third day carries two repos on one (day, model), with reasoning measured on one and
	// a dash on the other: the report sums the model's rows, and a dash is not a zero.
	saveDay(t, b.out, "2026-09-13", gpt("2026-09-13", "schema", 10, 20, -1, -1, 3), gpt("2026-09-13", "serialize", 1, 2, 4))
	snapshot := func() map[string]string {
		files := map[string]string{}
		ents, err := os.ReadDir(b.out)
		require.NoError(t, err)
		for _, e := range ents {
			files[e.Name()] = testkit.ReadFile(t, filepath.Join(b.out, e.Name()))
		}
		return files
	}
	before := snapshot()

	novaTokens.Do(t, "ledger", "--out", b.out, "--month", "2026-09", "--redis", dsn).Exit(0).Out("LEDGER OK month=2026-09 days=3")
	require.Equal(t, []string{"tokens:ledger:2026-09-11", "tokens:ledger:2026-09-12", "tokens:ledger:2026-09-13"}, mr.Keys(), "want one tokens:ledger:<day> per folded day and nothing else")

	// The folded side: every row of every day file of the month, summed per (day, model,
	// repo) with the fold's own per-type rule (a dash adds nothing, a type any row reported
	// is a number).
	paths, err := filepath.Glob(filepath.Join(b.out, "2026-09-*"+tokens.FileSuffix))
	require.NoError(t, err)
	type acc struct {
		rows int
		c    tokens.Counts
	}
	sums := map[[3]string]*acc{}
	for _, p := range paths {
		d, findings, err := tokens.ReadDayFile(p)
		require.NoError(t, err, p)
		require.Empty(t, findings, p)
		for _, r := range d.Rows {
			k := [3]string{r.Date, r.Model, r.Repo}
			if sums[k] == nil {
				sums[k] = &acc{}
			}
			sums[k].rows++
			sums[k].c.Add(r.Counts)
		}
	}
	var want []string
	for k, a := range sums {
		line := "REPORT day=" + k[0] + " model=" + k[1] + " repo=" + k[2] + " rows=" + strconv.Itoa(a.rows)
		for ty := tokens.Type(0); ty < tokens.NTypes; ty++ {
			line += " " + tokens.TypeNames[ty] + "=" + a.c.Cell(ty)
		}
		want = append(want, line)
	}
	sort.Strings(want)
	require.Len(t, want, 5, "the fixture's (day, model, repo) tuples")
	tuples := func(stdout string) []string {
		var got []string
		for _, l := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(l, "REPORT day=") {
				got = append(got, l)
			}
		}
		sort.Strings(got)
		return got
	}

	rep := novaTokens.Do(t, "report", "--redis", dsn, "--month", "2026-09", "--by", "tuple").Exit(0)
	require.Equal(t, want, tuples(rep.Stdout), "the store report is not the folded TSV to the token")
	rep.Out("REPORT day=2026-09-13 model=gpt repo=schema rows=1 input=10 output=20 cache_write=- cache_read=- reasoning=3",
		"REPORT day=2026-09-13 model=gpt repo=serialize rows=1 input=1 output=2 cache_write=4 cache_read=- reasoning=-",
		"REPORT OK month=2026-09 source=redis groups=5 rows=5 indexed=3 missing=27")

	// Re-indexing a day replaces it: the table is the day files' index, not an append log.
	novaTokens.Do(t, "ledger", "--out", b.out, "--day", "2026-09-13", "--redis", dsn).Exit(0)
	again := novaTokens.Do(t, "report", "--redis", dsn, "--month", "2026-09", "--by", "tuple")
	require.Equal(t, want, tuples(again.Stdout), "re-indexing a day changed the report")

	// The per-model group carries cache_write and reasoning too.
	novaTokens.Do(t, "report", "--redis", dsn, "--month", "2026-09").Exit(0).Out("REPORT model=gpt rows=2 input=11 output=22 cache_write=4 cache_read=- reasoning=3")
	assert.Equal(t, before, snapshot(), "ledger and report changed the fold's day files")
}

// The store's answers short of a full month. A report names what it wants rather than
// guessing a month, and one report has one source; `ledger` wants its store named, and a
// day with no file is a NO naming the fold. #3462: a month with no indexed calendar-day
// keys is REPORT NO exit 1, and a month with only some days indexed names indexed and
// missing on the OK line. No row writes to the store.
func TestReportAndLedgerOnAStoreShortOfTheMonth(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		seed     [][3]string // key, field, value
		args     []string    // $redis is the store, $out an empty --out
		exit     int
		out, err []string
	}{
		{"report --redis wants --month", nil, []string{"report", "--redis", "$redis"}, 2, nil, []string{"--month is required"}},
		{"report --by names its groups", nil, []string{"report", "--redis", "$redis", "--month", "2026-09", "--by", "card"}, 2, nil, []string{"--by is model, repo, day or tuple"}},
		{"ledger wants --redis", nil, []string{"ledger", "--out", "$out", "--day", "2026-09-11"}, 2, nil, []string{"--redis"}},
		{"ledger names a missing day", nil, []string{"ledger", "--out", "$out", "--day", "2026-09-11", "--redis", "$redis"}, 1,
			[]string{"LEDGER BAD day=2026-09-11 why=no day file; fold --day 2026-09-11 first", "LEDGER NO day=2026-09-11 days=0 rows=0 bad=1"}, nil},
		{"a month with no indexed day is NO", nil, []string{"report", "--redis", "$redis", "--month", "2026-08"}, 1,
			[]string{"REPORT NO month=2026-08 source=redis indexed=0"}, nil},
		{"a partial month names indexed and missing",
			[][3]string{{"tokens:ledger:2026-09-05", `["c","m","r"]`, `{"provider":"x","tokens":[10,20,null,null,3],"sources":"x:o"}`},
				{"tokens:ledger:2026-09-20", `["c2","m2","r2"]`, `{"provider":"y","tokens":[5,10,1,null,0],"sources":"y:o"}`}},
			[]string{"report", "--redis", "$redis", "--month", "2026-09", "--by", "tuple"}, 0,
			[]string{"REPORT OK month=2026-09 source=redis groups=2 rows=2 indexed=2 missing=28"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			addr, mr := ledgerRedis(t)
			for _, kv := range c.seed {
				mr.HSet(kv[0], kv[1], kv[2])
			}
			fill := strings.NewReplacer("$redis", addr, "$out", testkit.Mkdir(t, filepath.Join(t.TempDir(), "out")))
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = fill.Replace(a)
			}
			novaTokens.Do(t, args...).Exit(c.exit).Out(c.out...).Err(c.err...)
			assert.Len(t, mr.Keys(), len(c.seed), "the run wrote to the store")
		})
	}
}

// TestLedgerAndReportDialAsTheAclUser (#3461): the fleet Redis has its default user off and
// the bench password is the ACL user bench's, so a seat password without its user is
// WRONGPASS. `--user bench --password-env X` connects as bench for both verbs; the same seat
// comes from NOVA_SPRINT_REDIS_USER when no --user is given (the one config nova-sprint
// uses); a user whose password variable is empty is refused before any dial. And first: the
// password is never a flag, and no variable is consulted unless --password-env names it.
func TestLedgerAndReportDialAsTheAclUser(t *testing.T) {
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "")

	addr, mr := ledgerRedis(t)
	mr.RequireAuth("sesame")
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "sesame")
	novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09").Exit(1).Err("REPORT FAILED store=redis")
	t.Setenv("LEDGER_TEST_PW", "sesame")
	novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09", "--password-env", "LEDGER_TEST_PW").Exit(1).Out("REPORT NO month=2026-09 source=redis indexed=0")

	addr, mr = ledgerRedis(t)
	mr.RequireUserAuth("bench", "sesame")
	out := t.TempDir()
	saveDay(t, out, "2026-09-11", gpt("2026-09-11", "schema", 10, 20))
	novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09", "--password-env", "LEDGER_TEST_PW").Exit(1).Err("REPORT FAILED store=redis")
	novaTokens.Do(t, "ledger", "--out", out, "--day", "2026-09-11", "--redis", addr, "--user", "bench", "--password-env", "LEDGER_TEST_PW").Exit(0).Out("LEDGER OK day=2026-09-11 days=1")
	novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09", "--user", "bench", "--password-env", "LEDGER_TEST_PW").Exit(0).Out("REPORT OK month=2026-09 source=redis groups=1")

	t.Setenv("NOVA_SPRINT_REDIS_USER", "bench")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "LEDGER_TEST_PW")
	novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09").Exit(0).Out("REPORT OK month=2026-09 source=redis groups=1")

	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	t.Setenv("LEDGER_EMPTY_PW", "")
	r := novaTokens.Do(t, "report", "--redis", addr, "--month", "2026-09", "--user", "bench", "--password-env", "LEDGER_EMPTY_PW").
		Exit(1).Err("--user bench but LEDGER_EMPTY_PW is empty; run under nova-secrets exec --only LEDGER_EMPTY_PW")
	require.NotContains(t, r.Stderr+r.Stdout, "sesame", "a refusal printed the password")
}

// TestLedgerMonthPipelinesWritesAndDropsSuperfluousPing pins rowan-7fbdefecf56e:
// `ledger --month` pipelines day hash writes in one round trip and openLedger drops
// the superfluous PING that report --redis and ledger --day previously paid (before it,
// a PING and a transaction per day in a loop: 24 trips for 23 days).
func TestLedgerMonthPipelinesWritesAndDropsSuperfluousPing(t *testing.T) {
	t.Parallel()

	addr, mr := ledgerRedis(t)
	var mu sync.Mutex
	var seen []string
	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		mu.Lock()
		seen = append(seen, strings.ToUpper(cmd))
		mu.Unlock()
		return ledgerFixtureACL(peer, cmd, args...)
	})
	out := t.TempDir()
	for i, day := range []string{"2026-09-01", "2026-09-02", "2026-09-03"} {
		saveDay(t, out, day, gpt(day, "schema", int64(10*(i+1)), int64(20*(i+1))))
	}

	for _, s := range []struct {
		args []string
		ok   string
		eval bool
	}{
		{[]string{"ledger", "--out", out, "--month", "2026-09", "--redis", addr}, "LEDGER OK month=2026-09 days=3 rows=3 bad=0", true},
		{[]string{"report", "--redis", addr, "--month", "2026-09", "--by", "day"}, "REPORT OK month=2026-09 source=redis groups=3 rows=3 indexed=3 missing=27", false},
		{[]string{"ledger", "--out", out, "--day", "2026-09-01", "--redis", addr}, "LEDGER OK day=2026-09-01 days=1 rows=1 bad=0", true},
	} {
		novaTokens.Do(t, s.args...).Exit(0).Out(s.ok)
		require.Len(t, mr.Keys(), 3, "one key per day in the ledger")
		mu.Lock()
		cmds := seen
		seen = nil
		mu.Unlock()
		assert.NotContains(t, cmds, "PING", "%q sent the superfluous PING", s.args)
		if s.eval {
			assert.Contains(t, cmds, "EVAL", "%q did not write through EVAL", s.args)
		}
	}
}
