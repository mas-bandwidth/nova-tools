package main

import "runtime"

// hostOS is the operating system the checks run on. The run verb refuses to
// start TLC anywhere but Linux; the tests set the env's own.
var hostOS = runtime.GOOS

const helpRun = `tlacheck run: run the declared cases of tla/CASES.tsv under one budget and write the run records.

usage: tlacheck run --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>]
                    [--group <group> | --shards <n> --shard <i>] [--timeout <duration>]
                    [--workers 1|2] [--manual]

  --dir      the working directory the run owns: one <config>.log per case, RUNS.tsv,
             and work/, the private copy of the models TLC runs in (required)
  --root     the checkout whose tla/ holds CASES.tsv and the models (default .)
  --jar      the TLC jar; without it the environment variable TLC_JAR names it
  --java     the java program; without it java on PATH; the path found is echoed
  --group    run one declared group (tlacheck groups lists the required ones)
  --shards   split the cases over this many runs, and --shard picks one (zero-based);
             not with --group
  --timeout  the whole run's limit, a Go duration (default 110s; at most 110s, or 1h with --manual)
  --workers  TLC workers for a case expected to pass, 1 or 2 (default 2); a counterexample
             case always uses one
  --manual   an explicit bench experiment: mode=manual in the records; refused in CI

Each case runs with two JVM processors and final liveness checking, in its own temporary
directory. It must reach the result CASES.tsv declares: a passing case exits 0 with TLC's
completion line; a counterexample case exits with TLC's code for its kind and names the
declared invariant, action or temporal property. A timeout, a parse failure or an unrelated
violation is a failure. A case that ends the budget early fails the run.

RUNS.tsv holds one record per case: config, module, the fingerprint of what the case reads,
the number of files under it, the jar's digest, the java version, host, UTC start, the TLC workers, states generated and distinct
("-" when unknown), seconds, exit, result, expected, property, budget and mode. The
fingerprint covers the case's configuration, its module and the modules that one extends or
instantiates, the case's own row of CASES.tsv and the runner's result files (tlacheck inputs prints
them). It is written only if every case's fingerprint is the same when the cases are done as
when they started.

output: CASE OK|FAIL config= result= seconds= exit= generated= distinct=, then RUN OK|FAIL
        cases= records=. It runs only on Linux. Exit 0 all cases as declared, 1 a case or the
        budget failed, 2 could not run.
first run: tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group tablefirstcontact
`

const helpGroups = `tlacheck groups: print the case groups as a JSON array.

usage: tlacheck groups [--root <checkout>] [--stale]

  --root   the checkout whose tla/CASES.tsv is read (default .)
  --stale  the groups to run again instead: those that hold a declared case whose record in
           tla/RUNS.tsv is missing, or is not the fingerprint its case has now (tlacheck
           inputs prints what a case's fingerprint covers)

Payload: exactly one line of JSON on stdout, the sorted groups that hold a required case (the
matrix a CI run derives), or with --stale the groups that need a run. Nothing else is printed
there; refusals go to stderr. Exit 0, or 2 when the case plan or the records cannot be read or
are refused.
first run: tlacheck groups --root .
`

const helpMerge = `tlacheck merge: join the records of several runs into one records file.

usage: tlacheck merge --out <file> [--root <checkout>] [--keep <RUNS.tsv>] <RUNS.tsv>...

  --out   the records file to write (replaced whole, atomically) (required)
  --root  the checkout whose tla/CASES.tsv orders the records (default .)
  --keep  a records file whose current records stay: those of cases no run measured again
          (usually the committed tla/RUNS.tsv, so only the groups an edit staled run again)

Each run of tlacheck run covers one group. The committed tla/RUNS.tsv holds every case, so
the runs are merged in the order of the case plan. Refused: a record for a case the plan does
not declare, a case recorded twice, a declared case with no record, and a record whose
fingerprint is not the one this checkout gives its case (a run on other models, another plan
row or another runner than this binary was built from), and records measured with more than
one jar. A record in --keep that is not current is not kept: the merge then names it as stale,
with its group and the tlacheck run commands that measure it again. A --keep record of a case
the plan no longer declares is dropped and named (DROP OK config= why=not-in-the-plan). A file in
another column layout than this tool writes is refused naming both layouts.

output: MERGE OK runs= records= out=, and DROP OK config= why= for each kept record dropped.
first run: tlacheck merge --root . --out /tmp/RUNS.tsv /tmp/tlc-a/RUNS.tsv /tmp/tlc-b/RUNS.tsv
`

const helpTable = `tlacheck table: check the table model (MCTableMachine) under one budget.

usage: tlacheck table --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>]
                      [--mode strict|contracts|witnesses|controls|all] [--timeout <duration>]

  --dir      the working directory the run owns: one <case>.log per case and work/, the
             private copy of the models (required)
  --root     the checkout whose tla/ holds the models (default .)
  --jar      the TLC jar; without it the environment variable TLC_JAR names it
  --java     the java program; without it java on PATH; the path found is echoed
  --mode     strict (default): the five findings expected to PASS, the desired contract; it
             fails on the first finding the table code still has.
             contracts: the model and its fixed point pass.
             witnesses: each of the five findings violates its invariant.
             controls: the cross-table configuration violates OneTablePerMember (a scope
             control: the model is per table).
             all: contracts, witnesses and controls.
  --timeout  the whole run's limit, a Go duration (default 2m)

The run stops at the first case that is not what its mode expects.

output: TABLE OK|FAIL case= verdict= seconds= generated= distinct= log=, then TABLE OK|FAIL
        cases= mode=. Exit 0 every case as expected, 1 a case failed or timed out, 2 could
        not run.
first run: tlacheck table --root . --jar /path/to/tla2tools.jar --dir /tmp/table-out --mode witnesses
`

const helpMember = `tlacheck member: check the member and epoch protocol and its mutation controls.

usage: tlacheck member --dir <dir> [--root <checkout>] [--jar <tla2tools.jar>] [--java <java>]
                       [--suite all|member|epoch|small] [--workers <n>] [--timeout <duration>]

  --dir      the working directory the run owns: one <case>.log per case and work/, the
             private copy of the models (required)
  --root     the checkout whose tla/ holds the models (default .)
  --jar      the TLC jar; without it the environment variable TLC_JAR names it
  --java     the java program; without it java on PATH; the path found is echoed
  --suite    all (default), member (MemberTable), epoch (EpochMemberTable), or small (every
             case but the two large instances)
  --workers  TLC workers for the positive instances (default 4; each mutation control uses one)
  --timeout  the whole run's limit, a Go duration (default 2m)

The positive instances must pass; each mutation control must violate its named invariant or
action property. The run stops at the first case that is not what it expects.

output: MEMBER OK|FAIL case= verdict= seconds= generated= distinct= log=, then MEMBER OK|FAIL
        suite= cases= seconds=. Exit 0 every case as expected, 1 a case failed or timed out,
        2 could not run.
first run: tlacheck member --root . --jar /path/to/tla2tools.jar --dir /tmp/member-out --suite small
`

const helpReplay = `tlacheck replay: replay table.lua receipts and check them against EpochMemberTable.

usage: tlacheck replay --source <table.lua> --dir <dir> [--root <checkout>] [--models <dir>]
                       [--jar <tla2tools.jar>] [--java <java>] [--redis-server <program>]
                       [--timeout <duration>]

  --source        the table.lua under test (required)
  --dir           the working directory the run owns: trace.json, the generated harness in
                  execution/ and mutated-observation/, and the TLC logs (required)
  --root          the checkout whose tla/ holds the models (default .)
  --models        the directory of the TLA+ modules (default <root>/tla)
  --jar           the TLC jar; without it the environment variable TLC_JAR names it
  --java          the java program; without it java on PATH; the path found is echoed
  --redis-server  the redis-server program; without it redis-server on PATH; echoed
  --timeout       the whole run's limit, a Go duration (default 2m)

Runs 32 controlled calls of the table.lua in a disposable redis-server (a Unix socket in a
private directory, TCP off, nothing saved) and checks each committed receipt against the
store states around it; replays the receipts into a second fresh store; checks that a
receipt with a revision gap and one with a wrong member delta are refused; and has TLC walk
the captured states as a linear harness over EpochMemberTable, which must pass, and again
with one corrupted observation, which must violate MatchesExecution. The trace, the source's
and the models' digests are kept in trace.json.

output: REPLAY OK step= verdict= ..., then REPLAY OK|FAIL seconds=. Exit 0 all steps, 1 a check
        failed, 2 could not run.
first run: tlacheck replay --root . --source internal/nsprint/fn/lua/table.lua --jar /path/to/tla2tools.jar --dir /tmp/replay-out
`

const helpWitnesses = `tlacheck witnesses: replay the table model's findings against a table.lua.

usage: tlacheck witnesses [--redis-server <program>] [--timeout <duration>] <table.lua>

  --redis-server  the redis-server program; without it redis-server on PATH; the path found
                  is echoed
  --timeout       the whole run's limit, a Go duration (default 2m)
  <table.lua>     the table library to replay against; a pinned copy of an older table.lua
                  reproduces the findings, a fixed one refuses them

Loads the library into a disposable redis-server (a Unix socket in a private directory,
TCP off, nothing saved) and runs each finding as the exact calls it names, asserting the
replies and the stored state. A finding that reproduces is CONFIRMED (the defect is in this
source); a control that holds is PASS. A reply other than the one a finding names fails the
run: the source no longer has that defect, and the finding is retired or the model updated.

output: WITNESS OK result=confirmed|pass name=<finding>: <what>, then WITNESS OK|FAIL
        findings=. Exit 0 every finding as named, 1 one differed, 2 could not run.
first run: git show f77458853af46fdbbafd6881a4b46006431f266f:internal/nsprint/fn/lua/table.lua > /tmp/table-pinned.lua
           tlacheck witnesses /tmp/table-pinned.lua
`

const helpInputs = `tlacheck inputs: print what a TLC run of one case reads, with the hash of each.

usage: tlacheck inputs --case <config> [--root <checkout>]

  --case  the case, as its config column of tla/CASES.tsv names it (MCFoo.cfg; MCFoo also
          works) (required)
  --root  the checkout whose tla/ holds CASES.tsv and the models (default .)

The inputs are the case's configuration; the module CASES.tsv gives it and every module that
one EXTENDS or INSTANCEs, transitively (a name with no file under tla/ must be one of TLC's
standard modules, which read nothing from the tree: the ten the jar bundles, listed in
standardModules in internal/tlc/inputs.go); the case's own row of CASES.tsv under
its header; and the runner's result files (outcome.go, run.go and suite.go of internal/tlc: the command
line, flags, workers and timeouts of a run and the reading of its output) as this binary was
built. The package's other files are bookkeeping and in no fingerprint.
The fingerprint in a record is the SHA-256 over the paths and hashes listed here, in path
order, and the record's input_files is their count. A change to none of them leaves the
record current. The jar is not an input: a record names it in its own column.

Payload: one INPUT OK path= sha256= line per input, sorted by path, then INPUTS OK case=
files= fingerprint=, all on stdout. Refusals go to stderr. Exit 0, or 2 when the case is not
declared, the plan is refused, or a configuration or module cannot be read or names a module
that is neither a file nor a standard one.
first run: tlacheck inputs --root . --case MCFileLock.cfg
`
