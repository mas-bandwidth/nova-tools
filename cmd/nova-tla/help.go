package main

import (
	"runtime"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// hostOS is the operating system the checks run on. The run verb refuses to
// start TLC anywhere but Linux; the tests set the env's own.
var hostOS = runtime.GOOS

// hostArch is the architecture the checks run on.
var hostArch = runtime.GOARCH

// Defaults a verb's flags and its help both name.
const (
	defaultChecksTimeout = 2 * time.Minute
	defaultTroughWait    = 30 * time.Minute
	defaultTroughPoll    = 15 * time.Second
)

// tlcFlags are the flags of every verb that runs TLC: the checkout, the jar,
// java and the run's own directory.
func tlcFlags(f *tool.Flags) {
	f.String("root", ".", "the checkout whose tla/ holds CASES.tsv and the models (default .)")
	f.String("jar", "", "the TLC jar, a path; without it the environment variable TLC_JAR names it")
	f.String("java", "", "the java program, a path; without it java on PATH; the path found is echoed")
}

// positive refuses a duration flag that is not above zero.
func positive(c *tool.Call, name string) {
	if c.Dur(name) <= 0 {
		c.Problem("--" + name + " must be positive; it wants a Go duration above zero, such as 2m")
	}
}

func verbs(e env) []tool.Verb {
	return []tool.Verb{
		{
			Name: "run",
			Usage: `run --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>] [--group <group>] [--shards <n> --shard <i>] [--timeout <duration>] [--workers 1|2] [--parallel <n>] [--manual]
run --dir <dir> --bench <machine>|any [--root <checkout>] [--group <group>] [--jar <path on the bench>] [--java <java on the bench>] [--timeout <duration>] [--workers 1|2] [--load-below <load>] [--trough-wait <duration>] [--trough-poll <duration>]`,
			Effect: tool.Delivery + " with --bench: it stages the tree's tla/ and a runner built for the bench on that machine over ssh, runs the cases there, removes what it staged and merges the records into the checkout's tla/RUNS.tsv; without --bench, a local write: TLC (java) runs here and the records and logs are written under --dir",
			Detail: `Run the declared cases of tla/CASES.tsv under one budget and write the run records.
Each case runs with two JVM processors and final liveness checking, in its own temporary
directory. It must reach the result CASES.tsv declares: a passing case exits 0 with TLC's
completion line; a counterexample case exits with TLC's code for its kind and names the
declared invariant, action or temporal property. A timeout, a parse failure or an unrelated
violation is a failure. A case that ends the budget early fails the run.

RUNS.tsv holds one record per case: config, module, the fingerprint of what the case reads,
the number of files under it, the jar's digest, the java version, host (the platform label
<goos>-<goarch> the tool computes, never a machine name), the logical CPU count, UTC start,
the TLC workers, states generated and distinct ("-" when unknown), seconds, exit, result,
expected, property, budget and mode. The fingerprint covers the case's configuration, its
module and the modules that one extends or instantiates, the case's own row of CASES.tsv and
the runner's result files (nova-tla inputs prints them). It is written only if every case's
fingerprint is the same when the cases are done as when they started.

The run refuses a nova-tla that is not the tree's runner (nova-tla help runner).

With --bench, the command builds the tree's runner for the bench (./cmd/nova-tla in
nova-tools; nova-tools' cmd/nova-tla at the version its go.mod names in a tree that
requires nova-tools; refused in any other tree), stages it with the top of tla/ in
~/tla-runs/nova-tla-* there (ssh <machine>; removed when the run ends, and by the next run
after a day if a run died), and runs each case of each group as its own bounded run,
niced, after the load trough: so each record is under the cap, and a group's total is not
one budget. Without --group it runs every stale group. The records and logs come back
under --dir/<group>/<i>/, and when every case is as declared they are merged into the
checkout's tla/RUNS.tsv (merge --keep); when one is not, nothing is merged. With --bench
any the machine rows come from nova-config machine list, with this environment
(NOVA_PG_DSN); a machine named is not looked up, its jar is the check.

output: HELPER OK name=jar|java|java-version ..., one CASE OK|FAIL config= result= seconds=
        exit= generated= distinct= per case, then RUN OK|FAIL cases= records=. It runs only
        on Linux, or on a record machine with --bench, which adds BENCH OK|FAIL bench= ...,
        STAGE OK bench= dir= files=, TROUGH OK bench= group= case= load= below= waited=,
        the bench's own CASE and RUN lines, and MERGE OK runs= records= out=. OK lines go to
        stdout, FAIL lines to stderr.
example (a first run):
  nova-tla run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group tablefirstcontact
  nova-tla run --root . --dir /tmp/tlc-bench --bench any`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				tlcFlags(f)
				f.Required("dir", "the working directory the run owns: one <config>.log per case, RUNS.tsv, and work/, the private copy of the models TLC runs in")
				f.String("group", "", "run one declared group, by name (nova-tla groups lists the required ones); with --bench, empty runs every stale group")
				f.Int("shards", 1, "split the cases over this many runs, a count (default 1); --shard picks one; with --group, the group's cases")
				f.Int("shard", 0, "the shard this run takes, zero-based (default 0), under --shards")
				f.Duration("timeout", tlc.BoundedCap, "the whole run's limit, a Go duration (default 110s; at most 110s, or 1h with --manual)")
				f.Int("workers", 2, "TLC workers for a case expected to pass, 1 or 2 (default 2); a counterexample case always uses one")
				f.Int("parallel", 0, "independent cases to run at once, 1 to half the logical CPUs (default 0: a quarter of them, at least 1); not taken with --bench")
				f.Bool("manual", false, "an explicit bench experiment: mode=manual in the records and a cap of 1h; refused in CI")
				f.String("bench", "", "run on a TLC record machine from here: the machine named (ssh <machine>), or any: the row with tla=true (nova-config machine list) with the lowest load per CPU")
				f.Float64("load-below", 0, "with --bench, the 1-minute load each case waits to fall under (default 0: 0.8 x the bench's logical CPUs)")
				f.Duration("trough-wait", defaultTroughWait, "with --bench, how long a case waits for the load before the run gives up, a Go duration (default 30m)")
				f.Duration("trough-poll", defaultTroughPoll, "with --bench, how often the load is read while waiting, a Go duration (default 15s)")
				f.Check(func(c *tool.Call) {
					if c.Str("bench") != "" {
						for _, n := range []string{"shards", "shard", "parallel"} {
							if c.Given(n) {
								c.Problem("--" + n + " is not taken with --bench: it runs a group's cases one at a time itself")
							}
						}
						if c.Get("load-below").(float64) < 0 || c.Dur("trough-poll") <= 0 || c.Dur("trough-wait") < 0 {
							c.Problem("--load-below, --trough-wait and --trough-poll take a load of 0 or more (0: 0.8 x the bench's CPUs), a wait of 0 or more and a poll above 0")
						}
						return
					}
					for _, n := range []string{"load-below", "trough-wait", "trough-poll"} {
						if c.Given(n) {
							c.Problem("--" + n + " is taken only with --bench")
						}
					}
				})
			},
			Run: e.on(cmdRun),
		},
		{
			Name:    "groups",
			Usage:   "groups [--root <checkout>] [--stale]",
			Example: "groups --root ./cmd/nova-tla/testdata/example\ngroups --root ./cmd/nova-tla/testdata/example --stale",
			Effect:  tool.Inspection,
			Detail: `Print the case groups as a JSON array: the sorted groups that hold a required case (the
matrix a CI run derives), or with --stale the groups that need a run: those that hold a
declared case whose record in tla/RUNS.tsv is missing, or is not the fingerprint its case has
now (nova-tla inputs prints what a case's fingerprint covers). A checkout with no
tla/RUNS.tsv has measured nothing, so every required group is stale. With --stale it
refuses a nova-tla that is not the tree's runner (nova-tla help runner).

output: exactly one line of JSON on stdout, such as ["alpha","beta"]; nothing else is printed
        there, and a refusal goes to stderr. With --json, that line is the payload of the
        result object.
example (a first run):
  nova-tla groups --root .`,
			Flags: func(f *tool.Flags) {
				f.String("root", ".", "the checkout whose tla/CASES.tsv is read (default .)")
				f.Bool("stale", false, "print the groups to run again instead: a case's record missing or not its current fingerprint")
			},
			Run: e.on(cmdGroups),
		},
		{
			Name:   "merge",
			Usage:  "merge --out <file> [--root <checkout>] [--keep <RUNS.tsv>] <RUNS.tsv>...",
			Args:   true,
			Effect: tool.LocalWrite + ": it writes --out, replaced whole and atomically",
			Detail: `Join the records of several runs into one records file. Each run of nova-tla run covers
one group; the committed tla/RUNS.tsv holds every case, so the runs are merged in the order
of the case plan. Refused: a record for a case the plan does not declare, a case recorded
twice, a declared case with no record, a record whose fingerprint is not the one this
checkout gives its case (a run on other models, another plan row or another runner than
this binary was built from), and records measured with more than one jar. A record in
--keep that is not current is not kept: the merge then names it as stale, with its group and
the nova-tla run commands that measure it again. A --keep record of a case the plan no
longer declares is dropped and named. A file in another column layout than this tool writes
is refused naming both layouts. The verb refuses a nova-tla that is not the tree's runner
(nova-tla help runner).

output: MERGE OK runs= records= out= on stdout, and DROP OK config= why=not-in-the-plan keep=
        for each kept record dropped; MERGE FAIL runs=: <why> on stderr, exit 1, with --out
        left as it was.
example (a first run):
  nova-tla merge --root . --out /tmp/RUNS.tsv /tmp/tlc-a/RUNS.tsv /tmp/tlc-b/RUNS.tsv`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.Required("out", "the records file to write, a path (replaced whole, atomically)")
				f.String("root", ".", "the checkout whose tla/CASES.tsv orders the records (default .)")
				f.String("keep", "", "a records file whose current records stay for the cases no run measured again, usually the committed tla/RUNS.tsv")
				f.Check(func(c *tool.Call) {
					if len(c.Args()) == 0 && c.Str("keep") == "" {
						c.Problem("no records files named; it wants the RUNS.tsv of each run after the flags, or --keep <RUNS.tsv>")
					}
				})
			},
			Run: e.on(func(e env, c *tool.Call) *tool.Out {
				return doMerge(e, c.Str("root"), c.Str("out"), c.Str("keep"), c.Args())
			}),
		},
		{
			Name:    "inputs",
			Usage:   "inputs --case <config> [--root <checkout>]\ninputs [--root <checkout>] <config>",
			Example: "inputs --root ./cmd/nova-tla/testdata/example --case MCDoor.cfg",
			Args:    true,
			Effect:  tool.Inspection,
			Detail: `Print what a TLC run of one case reads, with the hash of each. The inputs are the case's
configuration; the module CASES.tsv gives it and every module that one EXTENDS or INSTANCEs,
transitively (a name with no file under tla/ must be one of TLC's standard modules, which
read nothing from the tree: the ten the jar bundles, listed in standardModules in
pkg/tlc/inputs.go); the case's own row of CASES.tsv under its header; and the runner's
result files (outcome.go, plan.go, run.go and suite.go of pkg/tlc: the command line, flags,
workers and timeouts of a run, the reading of its output and of its plan row) as this binary
was built. The package's other files are bookkeeping and in no fingerprint. The fingerprint
in a record is the SHA-256 over the paths and hashes listed here, in path order, and the
record's input_files is their count. A change to none of them leaves the record current.
The jar is not an input: a record names it in its own column. The verb refuses a nova-tla
that is not the tree's runner (nova-tla help runner).

output: one INPUT OK path= sha256= line per input, sorted by path, then INPUTS OK case= files=
        fingerprint=, all on stdout; a refusal goes to stderr.
example (a first run):
  nova-tla inputs --root . --case MCFirstConn.cfg`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.String("root", ".", "the checkout whose tla/ holds CASES.tsv and the models (default .)")
				f.String("case", "", "the case, as the config column of tla/CASES.tsv names it (MCFoo.cfg; MCFoo also works); required, or given as the one argument instead")
				f.Check(func(c *tool.Call) {
					switch {
					case c.Str("case") == "" && len(c.Args()) == 0:
						c.Problem("--case is required; it wants the case's config (MCFoo.cfg), as --case or as the one argument")
					case c.Str("case") != "" && len(c.Args()) > 0, len(c.Args()) > 1:
						c.Problem("unexpected argument " + oneline.Quote(c.Args()[len(c.Args())-1]) + "; it wants the one case, as --case or as the one argument, never both")
					}
				})
			},
			Run: e.on(func(e env, c *tool.Call) *tool.Out {
				name := c.Str("case")
				if name == "" {
					name = c.Args()[0]
				}
				return cmdInputs(e, c.Str("root"), name)
			}),
		},
		{
			Name:   "table",
			Usage:  "table --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>] [--mode strict|contracts|witnesses|controls|all] [--timeout <duration>]",
			Effect: tool.LocalWrite + ": TLC (java) runs here, and one <case>.log per case and the private copy of the models are written under --dir",
			Detail: `Check the table model (MCTableMachine) under one budget. --mode strict (the default) runs
the five findings expected to PASS, the desired contract, and fails on the first finding the
table code still has; contracts: the model and its fixed point pass; witnesses: each of the
five findings violates its invariant; controls: the cross-table configuration violates
OneTablePerMember (a scope control: the model is per table); all: contracts, witnesses and
controls. The run stops at the first case that is not what its mode expects.

output: HELPER OK lines, one TABLE OK|FAIL case= verdict= seconds= generated= distinct= log= per
        case, then TABLE OK|FAIL mode= cases= seconds=. Exit 1 a case failed or timed out.
example (a first run):
  nova-tla table --root . --jar /path/to/tla2tools.jar --dir /tmp/table-out --mode witnesses`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				tlcFlags(f)
				f.Required("dir", "the working directory the run owns: one <case>.log per case and work/, the private copy of the models")
				f.String("mode", "strict", "which cases: strict (default), contracts, witnesses, controls or all")
				f.Duration("timeout", defaultChecksTimeout, "the whole run's limit, a Go duration (default 2m)")
				f.Check(func(c *tool.Call) { positive(c, "timeout") })
			},
			Run: e.on(cmdTable),
		},
		{
			Name:   "member",
			Usage:  "member --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>] [--suite all|member|epoch|small] [--workers <n>] [--timeout <duration>]",
			Effect: tool.LocalWrite + ": TLC (java) runs here, and one <case>.log per case and the private copy of the models are written under --dir",
			Detail: `Check the member and epoch protocol and its mutation controls. The positive instances must
pass; each mutation control must violate its named invariant or action property. The run
stops at the first case that is not what it expects.

output: HELPER OK lines, one MEMBER OK|FAIL case= verdict= seconds= generated= distinct= log=
        per case, then MEMBER OK|FAIL suite= cases= seconds=. Exit 1 a case failed or timed out.
example (a first run):
  nova-tla member --root . --jar /path/to/tla2tools.jar --dir /tmp/member-out --suite small`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				tlcFlags(f)
				f.Required("dir", "the working directory the run owns: one <case>.log per case and work/, the private copy of the models")
				f.String("suite", "all", "which cases: all (default), member (MemberTable), epoch (EpochMemberTable), or small (every case but the two large instances)")
				f.Int("workers", 4, "TLC workers for the positive instances, a count (default 4); each mutation control uses one")
				f.Duration("timeout", defaultChecksTimeout, "the whole run's limit, a Go duration (default 2m)")
				f.Check(func(c *tool.Call) { positive(c, "timeout") })
			},
			Run: e.on(cmdMember),
		},
		{
			Name:   "replay",
			Usage:  "replay --source <table.lua> --dir <dir> [--root <checkout>] [--models <dir>] [--jar <tla2tools.jar>] [--java <java>] [--redis-server <program>] [--timeout <duration>]",
			Effect: tool.LocalWrite + ": a disposable redis-server and TLC (java) run here, and the trace, the harness and the TLC logs are written under --dir",
			Detail: `Replay table.lua receipts and check them against EpochMemberTable. It runs 32 controlled
calls of the table.lua in a disposable redis-server (a Unix socket in a private directory,
TCP off, nothing saved) and checks each committed receipt against the store states around
it; replays the receipts into a second fresh store; checks that a receipt with a revision
gap and one with a wrong member delta are refused; and has TLC walk the captured states as a
linear harness over EpochMemberTable, which must pass, and again with one corrupted
observation, which must violate MatchesExecution. The trace, the source's and the models'
digests are kept in trace.json.

output: HELPER OK lines, REPLAY OK step= verdict= ... per step, then REPLAY OK seconds=, or
        REPLAY FAIL seconds=: <why> on stderr and exit 1 when a check failed.
example (a first run):
  nova-tla replay --root . --source pkg/nsprint/fn/lua/table.lua --jar /path/to/tla2tools.jar --dir /tmp/replay-out`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				tlcFlags(f)
				f.Required("source", "the table.lua under test, a path")
				f.Required("dir", "the working directory the run owns: trace.json, the generated harness in execution/ and mutated-observation/, and the TLC logs")
				f.String("models", "", "the directory of the TLA+ modules (default <root>/tla)")
				f.String("redis-server", "", "the redis-server program, a path; without it redis-server on PATH; the path found is echoed")
				f.Duration("timeout", defaultChecksTimeout, "the whole run's limit, a Go duration (default 2m)")
				f.Check(func(c *tool.Call) { positive(c, "timeout") })
			},
			Run: e.on(cmdReplay),
		},
		{
			Name:   "witnesses",
			Usage:  "witnesses [--redis-server <program>] [--timeout <duration>] <table.lua>",
			Args:   true,
			Effect: tool.LocalWrite + ": a disposable redis-server runs here in a private directory, removed when it ends",
			Detail: `Replay the table model's findings against a table.lua: a pinned copy of an older table.lua
reproduces the findings, a fixed one refuses them. It loads the library into a disposable
redis-server (a Unix socket in a private directory, TCP off, nothing saved) and runs each
finding as the exact calls it names, asserting the replies and the stored state. A finding
that reproduces is CONFIRMED (the defect is in this source); a control that holds is PASS. A
reply other than the one a finding names fails the run: the source no longer has that
defect, and the finding is retired or the model updated.

output: HELPER OK name=redis-server path=, one WITNESS OK result=confirmed|pass name=<finding>:
        <what> per finding, then WITNESS OK findings=, or WITNESS FAIL findings=: <why> on
        stderr and exit 1 when one differed.
example (a first run):
  git show f77458853af46fdbbafd6881a4b46006431f266f:pkg/nsprint/fn/lua/table.lua > /tmp/table-pinned.lua
  nova-tla witnesses /tmp/table-pinned.lua`,
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.String("redis-server", "", "the redis-server program, a path; without it redis-server on PATH; the path found is echoed")
				f.Duration("timeout", defaultChecksTimeout, "the whole run's limit, a Go duration (default 2m)")
				f.Check(func(c *tool.Call) {
					if len(c.Args()) != 1 {
						c.Problem("want exactly one table.lua after the flags, got " + strconv.Itoa(len(c.Args())))
					}
					positive(c, "timeout")
				})
			},
			Run: e.on(func(e env, c *tool.Call) *tool.Out {
				return cmdWitnesses(e, c, c.Args()[0])
			}),
		},
	}
}
