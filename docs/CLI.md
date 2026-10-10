# Command reference

[Back to Nova Tools](../README.md)

Command reference and worked examples. Run shell examples from the repository root unless a section says otherwise. `-h` or `--help` after any verb prints that verb's help (its usage lines and every flag it takes) on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question; `<tool> help` is the whole banner. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line, so what is shown here is what the tool does today.

### tdocs-cli-generated-rb-b.w8

The reference blocks between the `clidoc` markers are generated, not hand-kept: `make clidoc` runs each marked tool's `help` and each verb's `-h` from the built binaries and rewrites only the block between that tool's markers, so a flag a verb takes is a flag the reference names. The prose and the worked examples outside the markers stay hand-written. The test in internal/docs builds the tools and fails, naming `make clidoc`, when a block drifts from what the binaries print.

## nova-check

<!-- clidoc:begin nova-check -->
`nova-check help`:

```
nova-check: checks over markdown records and repositories, each finding named by file and line

how it works: most verbs inspect named paths and keep no state; three write, each only when asked:
dogfood record appends a receipt, spelling --write edits files in place, convergence --state
stores its streak, and --dry-run writes none of it. convergence reads forge data through gh
and an optional checkout through git; the other checks read the manifests and ledgers you name.
first run: create the small markdown tree below, then run the example commands.

usage:
  nova-check quickstart --dir <dir> [--exclude <prefix>] [--max <n>]
  nova-check attest --home <dir> --manifest <file> [--max <n>]
  nova-check links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]
  nova-check kernel --file <file> --max-bytes <n>
  nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>
  nova-check nocode --dir <dir> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]
  nova-check nocode --print-deny-list [--deny-ext <l|@f>] [--deny-ext-add <l|@f>]
  nova-check nocode --staged --dir <repo> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]
  nova-check floors --core <docs/SEED-CORE.md> --source <docs/SEED.md>
  nova-check corpus --ledger <file> --root <dir> --min-anchors <n> [--max <n>]
  nova-check hygiene --repo <dir> --base <ref> --head <ref> --identity "<Name> <email>" [--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]
  nova-check dogfood ledger (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--max <n>]
  nova-check dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--max <n>] [--dry-run]
  nova-check dogfood gate (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--shipped <cmd dir>] [--require-all] [--allow-empty] [--max <n>]
  nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>] [--state <file>] [--by <name>] [--json] [--dry-run]
  nova-check spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--exclude <prefix>] [--max <n>] [--dry-run]
  nova-check version
  nova-check help [<verb>]

  nova-check <verb> -h, nova-check help <verb>   the verb's flags, its effect and exit codes
dogfood gate exits 1 with the verbs no non-author has run and the edges nobody has cleared.
An edge is what the run found; a receipt records it: --not-ok, or an Edge: or Edges: in the notes.
The remedy is one nova-check dogfood record --ok per verb named, and per edge --closes <id> or
the finder running it again.
convergence exits 1 after two consecutive widening ticks. A widening tick is a tick whose <stream>
moved the wrong way against its before: --state's last for LEDGER and FLEET, --since's for the rest.
The exit-1 line prints trend=widening on the CONVERGENCE line, and the next run is
nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the
finding the stream names.

Every verb but quickstart, dogfood ledger, dogfood record, dogfood gate, convergence, spelling takes --json: the same result as one JSON object on stdout; spelling and convergence take it too, through their own printers (convergence prints its reading object), and quickstart and dogfood print typed lines. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)

setup:
  mkdir -p ./self/docs
  printf '# Kernel\n' > ./self/docs/SEED-CORE.md

example:
  nova-check quickstart --dir ./self
  nova-check links --dir ./self
  nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
```

`nova-check quickstart -h`:

```
usage: nova-check quickstart [flags]
from `nova-check help`:
  nova-check quickstart --dir <dir> [--exclude <prefix>] [--max <n>]
  nova-check quickstart --dir ./self
flags:
  --dir <string>  --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run (required)
  --exclude <value>  path prefix not scanned by links (repeatable; empty by default)
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads markdown under --dir, runs links then nocode, writes nothing
```

`nova-check attest -h`:

```
usage: nova-check attest [flags]
from `nova-check help`:
  nova-check attest --home <dir> --manifest <file> [--max <n>]
flags:
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --home <string>  --home <dir> is your memory-home directory: the tree the manifest's paths are relative to, and the only place attest reads (required)
  --json  print the result as one JSON object instead of lines
  --manifest <string>  --manifest <file> is a text file listing the paths a full boot must read, one per line, relative to --home (blank lines and # comments ignored); this tool ships none, because what a full boot reads is yours (required)
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the manifest and the files it names, writes nothing
```

`nova-check links -h`:

```
usage: nova-check links [flags]
from `nova-check help`:
  nova-check links --dir <dir> [--file <path>] [--exclude <prefix>] [--max <n>]
  nova-check links --dir ./self
flags:
  --dir <string>  --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run (required)
  --exclude <value>  path prefix not scanned, and links into it not checked (repeatable; empty by default)
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --file <value>  one markdown file to scan, narrowing the walk to just these (repeatable; --dir is still the resolution root)
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the markdown under --dir, writes nothing
```

`nova-check kernel -h`:

```
usage: nova-check kernel [flags]
from `nova-check help`:
  nova-check kernel --file <file> --max-bytes <n>
  nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>
  nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
flags:
  --bytes-per-token <float>  measured bytes per token, required with --max-tokens; no default
  --file <string>  --file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in (required)
  --json  print the result as one JSON object instead of lines
  --max-bytes <int>  size budget in bytes, must be positive (one of --max-bytes / --max-tokens)
  --max-tokens <int>  size budget in tokens, must be positive (one of --max-bytes / --max-tokens)
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the one file, writes nothing
```

`nova-check nocode -h`:

```
usage: nova-check nocode [flags]
from `nova-check help`:
  nova-check nocode --dir <dir> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]
  nova-check nocode --print-deny-list [--deny-ext <l|@f>] [--deny-ext-add <l|@f>]
  nova-check nocode --staged --dir <repo> [--allow <prefix>] [--deny-ext <l|@f>] [--deny-ext-add <l|@f>] [--max <n>]
flags:
  --allow <value>  path prefix where machinery may live (repeatable; empty by default)
  --deny-ext <string>  replace the floor EXTENSION list (not the name floor): comma list, or @file
  --deny-ext-add <string>  extend the floor EXTENSION list (not the name floor): comma list, or @file
  --dir <string>  --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run (required)
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --print-deny-list  print both floors in force (extensions and names) and exit 0
  --staged  advisory over the index: classify what is about to be committed, not the working tree (--dir is the repository root)
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the tree, or with --staged the git index, writes nothing
```

`nova-check floors -h`:

```
usage: nova-check floors [flags]
from `nova-check help`:
  nova-check floors --core <docs/SEED-CORE.md> --source <docs/SEED.md>
flags:
  --core <string>  --core <file> is the door: the derived copy, usually SEED-CORE.md, whose floor set is checked against the source's (required)
  --json  print the result as one JSON object instead of lines
  --source <string>  --source <file> is the source the door was derived from, usually SEED.md; the check is that the copy still agrees with it (required)
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the two files, writes nothing
```

`nova-check corpus -h`:

```
usage: nova-check corpus [flags]
from `nova-check help`:
  nova-check corpus --ledger <file> --root <dir> --min-anchors <n> [--max <n>]
flags:
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --json  print the result as one JSON object instead of lines
  --ledger <string>  --ledger <file> is your ledger of protected material: a markdown file whose table rows are | fragment | home file | given | by |, written in advance and by you — this tool ships no corpus (required)
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --min-anchors <int>  the fewest rows the ledger may hold, must be positive (required); the ledger is inside what it protects, so its own shrinking must be red
  --root <string>  --root <dir> is the repo the ledger's home paths are relative to; it is never guessed from the working directory or from where the ledger happens to sit (required)
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the ledger and the files it names, writes nothing
```

`nova-check hygiene -h`:

```
usage: nova-check hygiene [flags]
from `nova-check help`:
  nova-check hygiene --repo <dir> --base <ref> --head <ref> --identity "<Name> <email>" [--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]
flags:
  --base <string>  the base git ref of the comparison (required)
  --head <string>  the head git ref of the comparison (required)
  --identity <string>  the allowed authors, Name <email>, repeatable with commas; there is no default identity, and a range checked against nobody would admit anybody (required)
  --json  print the result as one JSON object instead of lines
  --kind <string>  card kind to validate; empty skips kind-specific checks
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --paths <string>  comma-separated allowed path globs; empty skips out-of-path checking
  --repo <string>  the git checkout to inspect (required)
  --timeout <int>  git inspection deadline in positive seconds
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the repository through git, writes nothing
```

`nova-check dogfood ledger -h`:

```
usage: nova-check dogfood ledger [flags]
from `nova-check help`:
  nova-check dogfood ledger (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--max <n>]
  a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`
flags:
  --authors <<tool> <verb> = <who wrote it>>  file mapping <tool> <verb> = <who wrote it>, one per line
  --cli <string>  the command reference the verbs are read from, usually docs/CLI.md
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --git-timeout <int>  seconds one --repo authorship read may take before it is killed and named
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --receipts <string>  --receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench (required)
  --repo <string>  repository to read authorship from when there is no --authors file
  --tools <string>  directory of built nova-* binaries, each asked for its own verbs (authoritative)
  --tools-timeout <int>  seconds the whole --tools read may take before it is killed and named
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing
```

`nova-check dogfood record -h`:

```
usage: nova-check dogfood record [flags]
from `nova-check help`:
  nova-check dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--max <n>] [--dry-run]
  a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`
flags:
  --by <string>  --by <name> is who ran it; the ledger's whole question is whether that is somebody other than the author, so a receipt with no name is not a receipt (required)
  --cli <string>  the command reference the verbs are read from, usually docs/CLI.md
  --closes <string>  the id of the finding this run answers, as the gate prints it
  --dry-run  print what the verb would write and write nothing
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --issue <int>  the issue number of the edge filed, when there is one
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --not-ok  it did not; file the edge and name it with --issue
  --notes <string>  --notes <text> is the real work you ran it on, in one line: what you were doing, what the verb did about it, and "Edges:" before anything you found (required)
  --ok  the verb did what the run needed
  --receipts <string>  --receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench (required)
  --tool <string>  --tool <t> is the binary you ran, spelled as the verb list spells it (nova-check) (required)
  --tools <string>  directory of built nova-* binaries, each asked for its own verbs (authoritative)
  --tools-timeout <int>  seconds the whole --tools read may take before it is killed and named
  --verb <string>  --verb <v> is the verb you ran, spelled as the verb list spells it (links, or "lift quarantine", or - for a tool that takes no verb) (required)
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: local write: appends one receipt file to --receipts (--dry-run writes none)
```

`nova-check dogfood gate -h`:

```
usage: nova-check dogfood gate [flags]
from `nova-check help`:
  nova-check dogfood gate (--cli <file> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--shipped <cmd dir>] [--require-all] [--allow-empty] [--max <n>]
  a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`
flags:
  --allow-empty  pass on an empty receipt set; without it, no receipts is a refusal and not a green line
  --authors <<tool> <verb> = <who wrote it>>  file mapping <tool> <verb> = <who wrote it>, one per line
  --cli <string>  the command reference the verbs are read from, usually docs/CLI.md
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --git-timeout <int>  seconds one --repo authorship read may take before it is killed and named
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --receipts <string>  --receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench (required)
  --repo <string>  repository to read authorship from when there is no --authors file
  --require-all  every verb in the list must have been run by a non-author, not only the ones with receipts
  --shipped <string>  a checkout's cmd/ directory: the gate judges only the tools under it, the set a release ships
  --tools <string>  directory of built nova-* binaries, each asked for its own verbs (authoritative)
  --tools-timeout <int>  seconds the whole --tools read may take before it is killed and named
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: inspection: reads the verb list and the receipts (--repo reads git, --tools runs each binary's help), writes nothing
```

`nova-check convergence -h`:

```
usage: nova-check convergence [flags]
from `nova-check help`:
  nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>] [--state <file>] [--by <name>] [--json] [--dry-run]
  nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the
flags:
  --batch-logs <string>  directory of <pr>-round-<n>.log gate logs; the second source for a batch's rounds
  --bin <string>  directory of scripts not yet replaced by a verb; without it the SCRIPTS stream is absent
  --by <value>  narrow the EDGES rounds to this friend's receipts (repeatable; empty reads them all)
  --certs <string>  a name<TAB>status certificate roll-up, for FLEET's certified fraction
  --dry-run  print what the verb would write and write nothing
  --gh <string>  the gh executable the forge is read through
  --git <string>  the git executable --repo-dir is read through
  --json  print the reading as one JSON object instead of the lines
  --ledger <string>  --ledger <file> is the pit-stop ledger: the markdown whose table rows carry PASS, FAIL, PARTIAL or TODO in their last cell, and whose open rows are the LEDGER stream (required)
  --now <string>  take the reading as of this RFC3339 instant instead of the clock, so a tick can be re-read exactly
  --receipts <string>  --receipts <dir> is the dogfood receipts directory, the same one nova-check dogfood reads; its open edges are the EDGES stream (required)
  --repo <string>  --repo <owner/name> is the forge repository the queue and the batches are read from (an owner/name such as example/project); it is a name on a forge, never a directory (required)
  --repo-dir <string>  a checkout of --repo, read only; without it the CLASSES stream is absent
  --retired <string>  --retired <file> is the retired-scripts README, whose dated rows say what the window retired; with --bin it is the SCRIPTS stream (required)
  --since <string>  --since <RFC3339|24h> is the far edge of the window: an instant (2026-09-18T00:00:00Z) or how long ago it starts (24h); there is no default, because the window is the whole question (required)
  --state <string>  where the last tick is remembered; without it no streak can be two and the verb never exits 1
  --timeout <int>  seconds one child read may take before it is killed and named
  --versions <string>  a nova-version snapshot, or a fleet roll-up of them; without it the FLEET stream is absent
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation); 1 is two consecutive widening ticks
effect: local write: --state stores the two-tick streak (--dry-run writes none); LANDING and PRS read the forge through gh, over the network, and CLASSES reads --repo-dir through git
```

`nova-check spelling -h`:

```
usage: nova-check spelling [flags]
from `nova-check help`:
  nova-check spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--exclude <prefix>] [--max <n>] [--dry-run]
flags:
  --dir <string>  directory tree to scan for misspellings
  --dry-run  print what the verb would write and write nothing
  --exclude <value>  path prefix not scanned (repeatable; empty by default)
  --fail-max <int>  the old spelling of --max, accepted for one release; it sets the same value
  --file <value>  one file to check, narrowing the check to just these (repeatable)
  --ignore <value>  allowlisted word or @file (repeatable, or comma-separated)
  --json  print typed findings and totals as one JSON object
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --path <value>  file or glob pattern to check (repeatable)
  --write  apply spelling corrections to files in place
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)
effect: local write: --write edits the files in place (--dry-run, or no --write, writes nothing)
```

`nova-check version -h`:

```
usage: nova-check version [flags]
from `nova-check help`:
  nova-check version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 pass, 1 check failed, 2 could not run (bad invocation)

effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-check -->

Most verbs only read. Three write, each only when asked and each with `--dry-run`, which makes every check and writes nothing: `dogfood record` appends a receipt, `spelling --write` edits files in place, `convergence --state` stores its two-tick streak. `convergence` also reads the forge through `gh`, over the network. A refusal is one line, `<VERB> REFUSED: <why>; run: <door>`, where the door is `nova-check help`, or `nova-check <verb> -h` after a flag the verb does not take; `<verb> -h` ends in the verb's `effect:` line.

### First run

`quickstart` needs nothing but a directory. It runs the two checks that want no budget, manifest or ledger, and runs both even if the first says no. `./self` is a self repo of yours; `cmd/nova-check/testdata/example-self` is one the size of a first run, and the tests run every line below against it.

```
$ nova-check quickstart --dir ./self
QUICKSTART RUN dir=./self checks=2: links, then nocode
LINKS OK dir=./self files=4 links=3 excluded=0 broken=0
NOCODE OK dir=./self files=5 deny-list=floor-list findings=0
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)

$ nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
KERNEL OK file=./self/docs/SEED-CORE.md bytes=771 budget=4000 findings=0
```

**Reading it.** The first line of a check is `<CHECK> OK`, `<CHECK> FAILED` or `<CHECK> REFUSED` and the run's facts as `key=value`; the findings of a failing run are typed lines under it, such as `LINKS BROKEN file=... line=... target=... reason=...`, and a failing run writes all of it to stderr with the subject named. `worst-exit=` is the run's exit code. A failing run is bounded: `attest`, `links`, `nocode`, `corpus` and `quickstart` print at most `--max` finding lines (default 20, `0` for all), then one `MORE` line naming the flag that shows the rest; the status line before them carries the count and prints on success too. The four verbs `quickstart` names at the end each want something only you have: a size budget, a boot manifest, a seed to compare against, a ledger of what you have chosen never to lose.

**What the flags want.** `--dir`, `--home` and `--root` are directories you write out, never the working directory. `--file` is one file to measure, with exactly one of `--max-bytes <n>` or `--max-tokens <n> --bytes-per-token <r>`; the divisor is one you measured on your own writing, because one the tool supplied would make the answer a guess that looked like an instrument. `--manifest` is a text file of paths relative to `--home`; `--ledger` is your markdown ledger of protected material and `--min-anchors <n>` its row floor. A run missing several flags names all of them at once. A typo or an unknown verb is one line that names the door (`run: nova-check help`, or `run: nova-check <verb> -h` after a flag the verb does not take), never the whole banner.

**`corpus` is the odd one out.** Every other check finds something present: a broken link names its target. A sentence that has been dropped names nothing, and a rewrite, a move or a restore can drop something that was given to you once, with nothing going red, because the record and the evidence about the record are the same files. So `corpus` reads a ledger you wrote in advance, the statements you intend never to lose without deciding to and where each lives, and asserts they are still there. Changing them is allowed; changing them silently is not, because the repair for a real change is to move the ledger row in the same commit.

### The dogfood ledger

A tool is not finished until it is tested, dogfooded by a non-author on real
work with the edges filed, the feedback applied, documented and released.
The middle of that sentence needs a record, or the claim is whatever the last
person to speak says it is. `dogfood` is that record: the verbs come from the
binaries or from this file, the runs come from receipts, and the gate is one
exit code a release lane can call.

```
$ nova-check dogfood record --tool nova-check --verb links --by Ada --ok \
    --notes "ran it over my own self repo before the merge; found nothing" \
    --receipts ./dogfood-receipts
DOGFOOD RECORD OK tool=nova-check verb=links by=Ada at=2026-09-18T09:00:00Z ok=yes issue=- file=./dogfood-receipts/20260918T090000Z-nova-check-links-ada-70e69505.json

$ nova-check dogfood ledger --cli ./docs/CLI.md --receipts ./dogfood-receipts
DOGFOOD tool=nova-check verb=quickstart by=nobody at=- ok=- issue=- open=0
DOGFOOD tool=nova-check verb=links by=Ada at=2026-09-18T09:00:00Z ok=yes issue=- open=0
DOGFOOD OK verbs=105 dogfooded=1 by-nonauthor=1 open-edges=0 unfiled=0 unmatched=0

$ nova-check dogfood gate --cli ./docs/CLI.md --receipts ./dogfood-receipts --require-all
DOGFOOD GATE FAIL tool=nova-check verb=quickstart: not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work
DOGFOOD GATE FAIL verbs=105 findings=104 shown=20 unmatched=0
```

**Reading it.** `ledger` prints one row per verb, in the list's order, and
never elides one: a ledger that capped its rows would hide exactly the verbs
nobody has run. The summary is the bounded read — `dogfooded=` counts verbs
with any receipt, `by-nonauthor=` counts the ones a non-author ran and said ok,
`open-edges=` counts the findings nobody has answered, `unfiled=` how many
of those nobody has filed an issue for, and `unmatched=` how many receipts named
a verb the list does not declare. Each row also carries `open=<n>`, the findings
open on that verb, printed whether it is zero or not. `gate` is the same read
with an exit code: 1 on an open edge always, 1 on any **unmatched not-ok
receipt** no receipt's `--closes` names, and with `--require-all` on every verb no non-author has passed.
`--shipped <cmd dir>` scopes the gate to the tools a release ships: a receipt
naming a tool that is not under that `cmd/` is set aside and counted on
`DOGFOOD NOTE shipped=<n> outside=<n> cmd=<dir>`, and judges nothing.

**An edge is answered, not outlived.** It is not cleared by somebody else running
the verb again later and finding nothing: where two people dogfood the same
verb, the second one's clean pass would otherwise close the first one's finding,
unread and unfiled, with the row printing that second person's `ok=yes` over it. A finding is
closed by a receipt that **names** it — `dogfood record --closes <id>`, which
anybody may write — or by **the person who found it** running the verb again and
finding nothing. The id is the eight hex characters the gate prints beside the
finding and the same eight that end the receipt's filename, so a reader with an
id can find the file:

```
DOGFOOD GATE FAIL tool=nova-check verb=links: open edge receipt=8e9b64a4 from Ada at 2026-09-18T09:00:00Z (no issue filed); closed by --closes 8e9b64a4 or by Ada running it again: the verb refused a relative path
```

A `--closes` naming an id nothing carries closes nothing and leaves the edge
open: a typo must never read as a close. A receipt the tool cannot parse is exit 1 and a named
`DOGFOOD FAILED` line, never a quietly shorter ledger. A receipt naming a verb
the list does not declare is named one by one — its file, what it claimed and
the nearest declared verb — by `ledger` AND by `gate`, because a release lane
must not be able to pass or fail without learning that the evidence it read was
thrown away.

**Unmatched evidence is counted.** `record` accepts the tool and verb pairs
in the declared list and refuses other pairs. Every ledger and gate summary
includes `unmatched=<n>`. The gate fails on an unmatched receipt that says
not-ok, naming its file and claimed verb. An unmatched receipt that says ok is
counted and named without failing the gate. Unmatched findings print before
findings derived from matching receipts.

**Where the verbs come from.** `--tools <dir>` names built `nova-*` binaries;
each binary's `help` supplies its verb list. `--cli <file>` names this reference
and supplies the list for tools absent from that directory. At least one flag
is required. The reader accepts fenced command lines, indented `usage:`
blocks, `$` transcripts and `### verb` headings within a tool's section. A
synopsis with no verb declares a bare invocation, represented as `verb=-` and
selected with `--verb -`. A `--verb` that names the bare form instead of
spelling it — `(default)`, `bare`, `none`, `no verb` — is refused with
`did you mean: <tool> --verb -` when the tool declares a bare invocation.

**An edge is what the run found, not only what it failed at.** A receipt records
an edge when the verb did not do what the run needed (`--not-ok`) **or** when
its notes name one in the shape the family writes them: `Edge:` or `Edges:`
before the finding. It stays open until somebody runs the verb again, later, and
records neither. An edge with no `--issue` is counted separately as `unfiled=`,
because an edge nobody has filed is one nobody else can act on.

**Who counts as the author.** `--authors <file>` maps `<tool> <verb> = <who
wrote it>`, one per line, and is exact. `--repo <dir>` is the second-best
source: for each verb it asks git for the first commit that introduced the
verb's word under `cmd/<tool>` and takes that commit's author. A verb neither
places has no author, so every receipt for it counts — the gate can be wrong by
asking for one more pass, never by passing a verb nobody ran. An author
dogfooding their own verb is recorded and does not count.

**The receipts are files.** One JSON line each — `tool`, `verb`, `by`, `at`,
`ok`, `notes`, `issue` — one file per receipt, written to a temporary name and
renamed, so two benches recording at once never interleave. `record` refuses a
`--tool`/`--verb` pair the list does not declare and names the nearest verb it
does, so a receipt is stranded at the moment it is written rather than found
months later in a count. Keep the directory in a repository: it is the record,
and it should outlive the bench.

### hygiene

The four mechanical checks the accept gate runs, on a branch, before you ask a
friend for a read: **identity** (every commit authored and committed by the
pool), **out-of-path** (every changed file inside the card's `PATHS:`),
**stray-file** (no `RESULT.md` and the rest of the stray list), **secret** (no
key-shaped string in the diff). The command reports 0 for clean, 1 for
findings and 2 when it could not run; the reviewer decides what to do with
those findings.

`--identity` is `Name <email>`, ONE pair of angle brackets, repeatable with
commas. There is no default: a range checked against nobody would admit
anybody, so the flag is required and the repository's own config is never a
fallback. An email spelled with a bracket still inside it is refused rather
than quietly matched against no one.

`--kind` is a card kind this toolchain DECLARES, and there is no default one
(SPEC-TOOLWORK §5 rules 3 and 6). It unlocks an allowlisted stray exception and
nothing else. A kind the tool does not hold is not answered with
`HYGIENE OK` — a clean answer about a shape of work that does not exist. It is
refused by name, listing the kinds there are:

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --kind fix-with-red-test
HYGIENE REFUSED: --kind "fix-with-red-test" is not a kind this tool declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, ledger, read, probe, text, tone, report; run: nova-check help
```

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**"
HYGIENE OK base=main head=card paths=sign/** findings=0
```

With no `--paths` the line says `paths=-` and out-of-path is SKIPPED — printed
rather than omitted, because a line that left the field out would read as a
bound that held.

Findings are capped like every listing here, and the `MORE` line carries the
command that prints the rest — the same run with the cap lifted, quoted so it
can be pasted:

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**" --max 2
HYGIENE FAILED base=main head=card paths=sign/** findings=4
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS: sign/**
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
```

### Are we converging

*Convergence is the health metric*: the contraction ratio per stream, every
tick. `convergence` is that reading, mechanised: seven streams,
each read from a real source, each printed as a number now, the same number at
`--since`, the ratio between them and a trend in that stream's own direction of
travel.

```
$ nova-check convergence --repo mas-bandwidth/nova-tools \
    --ledger ~/nova-tools/reports/pitstop-tests-2026-09-17.md \
    --receipts ~/nova-working/dogfood \
    --retired ~/nova-working/bin/retired/README.md \
    --bin ~/nova-working/bin --repo-dir . \
    --since 2026-09-18T00:00:00Z --state ~/nova-working/convergence.json
CONVERGENCE LANDING now=2 before=5 ratio=0.40 trend=contracting measure=rounds-per-batch batches=4 per-hour=0.25
CONVERGENCE CLASSES now=29 before=27 ratio=1.07 trend=contracting measure=class-test-index-entries rev=04bb4e1c9f2a
CONVERGENCE SCRIPTS now=42 before=66 ratio=0.64 trend=contracting measure=scripts-left-in-bin retired-in-window=24
CONVERGENCE PRS now=11 before=14 ratio=0.79 trend=contracting measure=open-pull-requests closed=9 opened=6
CONVERGENCE EDGES now=6 before=4 ratio=1.50 trend=widening measure=open-edges rounds=3 not-ok=2
CONVERGENCE FLEET now=- before=- ratio=- trend=absent measure=units-off-the-one-build source=--versions
CONVERGENCE LEDGER now=5 before=- ratio=- trend=flat measure=rows-not-yet-pass rows=31 open=5
CONVERGENCE WARN streams=6 contracting=4 widening=EDGES absent=FLEET
```

**Reading it.** `ratio` is always `now/before`, whichever way the stream
converges, so one column means one thing down the whole reading; `CLASSES` is
the one stream that converges upwards, because a class made mechanical cannot
come back. A stream whose source was not named is `trend=absent` with the flag
that would have fed it, and is counted in `absent=` rather than as a zero — a
number nobody measured, printed as a number, is worse than not printing it.
`WARN` says a stream is widening; the exit code is 1 only when one has widened
on **two consecutive ticks**, which is a fact about history, so it lives in
`--state` and nowhere else. A tick at or before the remembered instant is that
tick read again and never advances the streak; run the loop with the rolling
`--since 24h` rather than a fixed instant, or every tick re-reads one window and
the second one goes red. `--json` prints the same reading as one object.

**What the flags want.** `--repo` is a name on a forge, never a directory;
`--ledger` is the pit-stop ledger whose rows carry PASS, FAIL, PARTIAL or TODO;
`--receipts` is the same directory `dogfood` reads; `--retired` is the retired
scripts README, whose dated rows say what the window retired, and `--bin` is
what is left. `--since` is an instant or a duration (`24h`), and there is no
default, because the window is the whole question. The rules and the refusals
are in [SPEC-CHECK.md](SPEC-CHECK.md).

## nova-self-talk

<!-- clidoc:begin nova-self-talk -->
`nova-self-talk help`:

```
nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,
and matched against one table of shapes: a first-person claim carrying a word of failure
(STANDING: cannot check, bad at, worst) or a neutral-worded verdict (INSTALLATION: a
self-superlative, a door stated shut, a habit). A dated claim is a record, never flagged.
A scan writes nothing; nova-self-talk shapes prints the table, each row with a sentence it finds.
first run: nova-self-talk example ./pages writes the example pages from the binary itself;
then each line under example: exits 1, because the pages hold findings.

usage:
  nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...
  nova-self-talk scan [flags] <file>...        the same scan, named as a verb
  nova-self-talk shapes [--json]               every shape and licence the scan uses, with a
                                               sentence each finds and a near miss each passes
  nova-self-talk example [--dry-run] [--json] <dir>   write the two example pages into <dir>
  nova-self-talk version                       print this build identity (--version also accepted)
  nova-self-talk help [<verb>]                 this text, or one verb's help

The first word is a verb only when it is scan, shapes, example, version or help; anything
else is the first file, so a file named like a verb is given as ./scan. Flags may stand
before, between or after the files; -- ends the flags, for a file whose name begins with
a dash.

Two disjoint classes.

  STANDING / DATED   a first-person claim (I am, I cannot, I always, my <noun> is ...)
                     carrying a word of failure (fallible, broken, bad at, terrible at,
                     worst, cannot check, cannot ever ...). With a date or a measurement
                     word (2026-09-30, measured, that day) it is DATED: a record, counted
                     on one line, never quoted. Without one it is STANDING and is flagged.

  INSTALLATION       (a verdict in neutral words; a finding names its shape alone, and the
                     count line counts them as installations=)
                     a standing self-verdict built from NEUTRAL words, which the first
                     class cannot see: a self-superlative (RANKING: I am the best, my
                     weakest instrument), a door stated shut (FORECLOSURE: I will never be
                     a good planner, I have no recall), a verdict on a practice
                     (VERDICT-IDIOM: dead as a practice), or a habit (TRAIT: I always
                     overpromise, I tend to rush). Dated, instrument (RULE:, TELL:),
                     aspiration (I want to), imperative and quoted sentences are licensed.

Date it, cut it, relocate it, or keep it on purpose — the judgment is the
writer's, and this tool never makes it.

what a scan prints, one line each:
  Findings print on stderr in line order; skips, banners, the DATED count, the closing
  count line, SKIP files=0 and NOTE print on stdout. Run with 2>/dev/null: the count
  line, DATED and NOTE remain on stdout.
  SELFTALK FAIL <file>:<line>: <SHAPE> match="<words>": <sentence>
                       <SHAPE> is STANDING for the first class, else the second class's shape:
                       RANKING, FORECLOSURE, VERDICT-IDIOM or TRAIT
  SELFTALK SKIP <file> (--skip)
  SELFTALK RULEDOC <file>: <banner>     above the findings of a --rule-doc file
  SELFTALK MORE kind=<class> shown=<n> total=<t> <remedy>
  SELFTALK DATED n=<k> files=<n>
  SELFTALK OK|FAIL files=<n> claims=<n> standing=<n> installations=<n> dated=<n> [shown=<n>]
  SELFTALK SKIP files=0 skipped=<n> reason=all-skipped
  SELFTALK NOTE <a --skip or --rule-doc name no named file has>
  SELFTALK NOTE <what a green does and does not clear>
match= is the words the shape's rule matched. files= counts the files scanned; claims= the
first class's claims, dated ones included; standing= and installations= the findings of each
class; shown= the finding lines printed. A partial check says so: the NOTE prints every run.

flags of the scan:
  --skip <basename>       do not scan files with this basename (repeatable). Nothing is
                          skipped by default, and a skip is reported on a SKIP line.
  --rule-doc <basename>   scan the file, but print its findings under a banner saying a
                          finding there is a self-verdict to relocate and NEVER a reason to
                          soften a rule (repeatable). No basename is special by default.
  --max <n>               finding lines to PRINT per class before one MORE line stands for
                          the rest. Default 20, and 0 means all. The closing line carries
                          the totals whichever way the run went.
  --json                  print the run as one JSON object on stdout instead of lines:
                          result, facts (the closing line's counts), items (one per finding,
                          skip and banner, with file, line, shape, match, text), more, notes.

exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
unreadable file). An all-skipped run exits 0 with SELFTALK SKIP files=0, never OK.

The first run needs nothing but this binary. Write the example pages, then paste the
lines under example: as they are:

setup:
  nova-self-talk example ./pages

example:
  nova-self-talk ./pages/journal.md
  nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md
  nova-self-talk --skip RULES.md ./pages/RULES.md ./pages/journal.md

All three exit 1, and that is the tool working: a finding is a sentence to date,
cut, relocate or keep on purpose, never a failure. --skip leaves a file unscanned
and says so on one SELFTALK SKIP line.
```

`nova-self-talk scan -h`:

```
usage: nova-self-talk scan [flags]
from `nova-self-talk help`:
  nova-self-talk scan [flags] <file>...        the same scan, named as a verb
effect: inspection: reads, writes nothing
flags:
  --json  print the run as one JSON object on stdout instead of lines
  --max <int>  finding lines to print per class before one MORE line stands for the rest; 0 prints all
  --rule-doc <value>  basename whose findings print under the rule-document banner, repeatable (empty by default)
  --skip <value>  basename to skip, repeatable (nothing is skipped by default)
exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
```

`nova-self-talk shapes -h`:

```
usage: nova-self-talk shapes [flags]
from `nova-self-talk help`:
  nova-self-talk shapes [--json]               every shape and licence the scan uses, with a
  sentence each finds and a near miss each passes
effect: inspection: reads, writes nothing
flags:
  --json  print the table as one JSON object on stdout instead of lines
exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
```

`nova-self-talk example -h`:

```
usage: nova-self-talk example [flags]
from `nova-self-talk help`:
  nova-self-talk example [--dry-run] [--json] <dir>   write the two example pages into <dir>
  nova-self-talk example ./pages
effect: local write: writes files on this machine
flags:
  --dry-run  print what would be written and write nothing
  --json  print the result as one JSON object on stdout instead of a line
exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
```

`nova-self-talk version -h`:

```
usage: nova-self-talk version [flags]
from `nova-self-talk help`:
  nova-self-talk version                       print this build identity (--version also accepted)
effect: inspection: reads, writes nothing
exit codes: 0 no findings, or a verb done; 1 findings; 2 could not run (bad invocation,
```
<!-- clidoc:end nova-self-talk -->

### First run

Name a file, or `-` for standard input. There is no directory walk. The first word is a verb only when it is `scan`, `shapes`, `example`, `version` or `help`; anything else is the first file, and a file named like a verb is given as `./scan`. The example pages are built into the binary; write them to a directory of yours first:

```
nova-self-talk example ./pages
```

```
$ nova-self-talk ./pages/journal.md
SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
SELFTALK FAIL ./pages/RULES.md:8: VERDICT-IDIOM match="dead as a practice": A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
```

**Reading it.** Both runs exit 1, and that is the tool working: a finding is a sentence to date, cut, relocate or keep on purpose, and the judgment stays yours. `STANDING` is a first-person claim carrying a word of failure. `DATED` is such a claim carrying a date or a measurement word, which makes it a record; those are counted on one line, never quoted, because a tool that quoted six hundred welcome sentences was spending your context on the good news. A finding of the second class (a verdict in neutral words, counted as `installations=`) is named by its shape alone: `RANKING`, `FORECLOSURE`, `VERDICT-IDIOM` or `TRAIT`. A page's findings print in line order, whichever class found them, and a `--skip` or `--rule-doc` name that no named file has is said on a `NOTE` line. `match=` is the words the shape's rule matched. `--max <n>` (default 20, `0` for all) bounds the finding lines; the count line prints either way. The `NOTE` prints on every run, green included. `--json` prints the same run as one JSON object on stdout.

**What it finds, exactly.** `nova-self-talk shapes` prints the detector table the scan walks: one row per rule, with what it finds, its pattern, a sentence it reports (`finds=`) and a near miss it passes (`passes=`); the tests run every row's two sentences through the scan, so the table cannot claim a shape the scan misses.

**The law behind both classes:** a capability denial is a measurement with a date, never a remembered property. The first class needs negative vocabulary; the second needs none, which is why the first cannot see it: a self-superlative, a door stated shut, a verdict on a practice, a habitual self-report. A dated claim is licensed in both classes. Instruments, imperatives, aspiration and quotations are licensed in the second; a prohibition carries no first-person claim for either class to bind to.

**Why it measures this and not "negativity".** The first version counted negation words. A rule document is a list of absolutes, so it scored worst of anything, and improving its score meant deleting prohibitions. Five rules were weakened that way, one at floor level, before a cold reader caught them. "Never" is not negative self-talk. "I am fallible" is. That incident is why `--skip <basename>` exists (rule documents flag the first class, and flagging is the tool working; skip them by name, never soften a rule for a score) and why `--rule-doc <basename>` scans a rule document like any other file and, when either class finds something there, prints a banner above its findings saying a finding there is a self-verdict to relocate, never a reason to soften a rule. Both take a basename, not a path, and both are empty by default.

**What a first run gets wrong.** Naming no files is a refusal, not an empty green; a shell glob is the usual first run. A file that cannot be read is not a clean file: the run names every unreadable path on its own `REFUSED` line and scans nothing, and a bare word that is no file is told the verbs. A skipped file is announced, and a run whose every file was skipped exits 0 with `files=0`, so a caller gating on the exit code should also require `files>0`. There is no `quickstart` verb, because the first run is `example` and a filename.

**The honest limit, printed on every run:** this catches known shapes only. Register, irony and quotation beyond the marked cases are invisible to grammar. A green means the known shapes are clear, never that the file is.

## nova-fuse

<!-- clidoc:begin nova-fuse -->
`nova-fuse help`:

```
nova-fuse: a recorded decision to stop reading an untrusted source, checked before every read

how it works: the box is one JSON file you name with --box. It holds at most one
lockdown (every untrusted read stops) and one quarantine per surface, a surface
being any name you give a source; each carries its time and reason. check reads
the box and exits 0 only when nothing blocks the surface; no box, or a broken
one, reads as blown. The tool enforces nothing: your harness runs check first.
first run: run the lines under example: in order, starting with init at a
path where no box exists.

usage:
  nova-fuse version    print this build identity (--version also accepted)
  nova-fuse init --box <path> [--dry-run]                  create an empty box where none is; never replaces one
  nova-fuse status --box <path> [--max <n>]                what is blown, and since when (REPORTS; never gate on it)
  nova-fuse check --box <path> [surface]                   may I read? -- the gate; act only on exit 0
  nova-fuse lockdown --box <path> [--dry-run] "<reason>"   blow the one hard fuse: all untrusted reads stop
  nova-fuse quarantine --box <path> [--dry-run] <surface> "<reason>"
                                                           stop reading one surface (soft)
  nova-fuse lift quarantine --box <path> [--dry-run] <surface>
                                                           rescind your own quarantine (soft, both directions)
  nova-fuse lift lockdown                                  REFUSED by design: the box is replaced: nova-fuse
                                                           init --box <a new path>, and the harness pointed at
                                                           it, by the person, never by this tool
  nova-fuse path --box <path>                              echo the box path this invocation would use

exit codes: 0 clear, or done and verified by re-reading the box; by verb:
check: 1 blown (a lockdown, or a quarantine on the surface); init, lockdown,
quarantine, lift quarantine: 1 the write was attempted and re-reading the box
did not show it; status, path: 0 only; every verb: 2 could not run -- missing
flag, bad invocation, or a lift this tool refuses by design; check, status,
quarantine and lift quarantine also answer 2 at a path with no box or an
unreadable box, never read as clear.

-h or --help after a verb is refused at exit 2, never answered with help:
exit 0 is this tool's CLEAR, so a surface or a reason spelled -h cannot reach
it. Read a verb's help with nova-fuse help <verb> (for example, help check).
--dry-run on init, lockdown, quarantine and lift quarantine makes every check
the write would and writes nothing; its line says dry_run=true. There is no
--json: every verb answers in one-line typed records (the grammar in SPEC.md),
and check's answer is its exit code.

box JSON example (a quarantine with no lockdown):
  {"lockdown": null, "quarantine": {"a-forum": {"at": "2026-01-01T00:00:00Z", "reason": "an attack is pervasive"}}}
Each blown fuse is an object with at (RFC3339 UTC) and reason; null means
no lockdown. init creates {"lockdown": null, "quarantine": {}}.

status lists at most --max quarantines (default 20, and 0 means all) after its
count line, then one STATUS MORE kind=quarantine shown=<n> total=<t> line
standing for the rest. THE COUNT IS NEVER CAPPED: quarantines=<t> on the first
line is the truth about the box however few surfaces are listed under it.

The box path always comes from --box. There is no default and no environment
variable; a missing --box is a refusal: refusing to guess. Flags come before
positional arguments, and every flag takes one value: a flag named twice, or a
--box value beginning with -, is refused at exit 2. -- ends the flags; after
it an argument beginning with - is a surface or a reason, never a flag, so a
caller passing an untrusted surface puts -- before it.

example:
  nova-fuse init --box ./fuse-box.json
  nova-fuse status --box ./fuse-box.json
  nova-fuse check --box ./fuse-box.json a-public-issue-tracker
  nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
  nova-fuse check --box ./fuse-box.json a-forum
  nova-fuse lift quarantine --box ./fuse-box.json a-forum

Those six are one sitting, in order: create, look, ask, blow the soft fuse,
watch the answer change, rescind it. init never replaces an existing box.
Every verb except init, lockdown, and path refuses a path with no box, never
read as CLEAR; init makes an empty box there and refuses if anything is
already there, lockdown makes a blown box there in one write, and path reads
no box at all.
```

`nova-fuse version -h`:

```
usage: nova-fuse version

exit codes: 0 build identity printed; 2 unexpected flags or arguments.
effect: inspection: reads, writes nothing
Help: nova-fuse help version; -h after a verb is refused at exit 2.
```

`nova-fuse init -h`:

```
usage: nova-fuse init --box <path> [--dry-run]

flags:
  --box <path>  required JSON box path; no default or environment variable
  --dry-run    make every check the write would, print what it would do, write nothing
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 created and verified; 1 already exists, write failed, or verification failed; 2 bad invocation.
effect: local write: writes the box file named by --box
Help: nova-fuse help init; -h after a verb is refused at exit 2.
```

`nova-fuse status -h`:

```
usage: nova-fuse status --box <path> [--max <n>]

flags:
  --box <path>  required JSON box path; no default or environment variable
  --max <n>    quarantine line limit (default 20); 0 lists all; totals stay uncapped
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 reported a readable box; 2 bad invocation, absent box, or unreadable box.
effect: inspection: reads, writes nothing
Help: nova-fuse help status; -h after a verb is refused at exit 2.
```

`nova-fuse check -h`:

```
usage: nova-fuse check --box <path> [--] [surface]

flags:
  --box <path>  required JSON box path; no default or environment variable
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 verified clear for what was checked; 1 lockdown or the named surface's quarantine is blown; 2 cannot prove clear (bad invocation, absent box, or unreadable box).
effect: inspection: reads, writes nothing; the permission gate: act only on exit 0
Help: nova-fuse help check; -h after a verb is refused at exit 2.
```

`nova-fuse lockdown -h`:

```
usage: nova-fuse lockdown --box <path> [--dry-run] [--] <reason>

flags:
  --box <path>  required JSON box path; no default or environment variable
  --dry-run    make every check the write would, print what it would do, write nothing
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 blown and verified; 1 write or verification failed; 2 bad invocation. Every failure remains no permission to read.
effect: local write: writes the box file named by --box
Help: nova-fuse help lockdown; -h after a verb is refused at exit 2.
```

`nova-fuse quarantine -h`:

```
usage: nova-fuse quarantine --box <path> [--dry-run] [--] <surface> <reason>

flags:
  --box <path>  required JSON box path; no default or environment variable
  --dry-run    make every check the write would, print what it would do, write nothing
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 quarantined and verified; 1 write or verification failed; 2 bad invocation, absent box, or unreadable box.
effect: local write: writes the box file named by --box
Help: nova-fuse help quarantine; -h after a verb is refused at exit 2.
```

`nova-fuse lift quarantine -h`:

```
usage: nova-fuse lift quarantine --box <path> [--dry-run] [--] <surface>

flags:
  --box <path>  required JSON box path; no default or environment variable
  --dry-run    make every check the write would, print what it would do, write nothing
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 lifted and verified; 1 no quarantine to lift, write failed, or verification failed; 2 bad invocation, absent box, or unreadable box.
effect: local write: writes the box file named by --box
Help: nova-fuse help lift quarantine; -h after a verb is refused at exit 2.
```

`nova-fuse lift lockdown -h`:

```
usage: nova-fuse lift lockdown

exit codes: 2 always refused; no arguments or flags can change this.
effect: inspection: refused by design before flags or files are read; writes nothing
Help: nova-fuse help lift lockdown; -h after a verb is refused at exit 2.
```

`nova-fuse path -h`:

```
usage: nova-fuse path --box <path>

flags:
  --box <path>  required JSON box path; no default or environment variable
  --           end flags; following words are literal surfaces or reasons
exit codes: 0 path printed; 2 bad invocation.
effect: inspection: writes nothing and does not read the box
Help: nova-fuse help path; -h after a verb is refused at exit 2.
```
<!-- clidoc:end nova-fuse -->

### First run

The standalone sitting in `nova-fuse help` starts with `init --box ./fuse-box.json` to create an empty box, then looks, asks, blows the soft fuse, watches the answer change, and rescinds it. `init` never replaces an existing box. The transcript below is a separate run against a populated fixture: it starts with `status` and its box already has one surface quarantined (`cmd/nova-fuse/testdata/example-box.json`, which the tests run these lines against).

```
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAILED quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-public-issue-tracker')

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAILED quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')

$ nova-fuse lift quarantine --box ./fuse-box.json a-forum
LIFT OK quarantine=a-forum was since=2026-09-09T18:27:40Z: a post addressed me and asked for a token
LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)
```

**Reading it.** The second and fourth commands exit 1, and that is the tool working: `check` is the gate, and only exit 0 is permission. `status` exits 0 whether or not anything is blown, because answering is its whole job; never gate on it. Every write verb re-reads the box afterwards and says `verified`, because the exit code of a remedy is not evidence the remedy worked. `status` is bounded: the count on its first line is never capped, and under it are at most `--max` quarantine lines (default 20), then one `MORE` line.

`--dry-run` on the four verbs that write makes every check the write would and writes nothing; its line says `dry_run=true`. A refusal is one line, `nova-fuse[ <verb>] REFUSED: <why>; run: nova-fuse help`; `nova-fuse help <verb>` prints a verb's usage, flags, exit codes and effect.

**What the flags want.** `--box` is the file, named on every verb; there is no default and no environment variable, because a fuse box the tool went looking for is one an attacker can put somewhere. Every flag takes one value: `--box` named twice is refused at exit 2, never answered from the last one, and so is a `--box` value that begins with `-`. `--` ends the flags, and after it an argument beginning with `-` is a surface or a reason, never a flag; a caller passing an untrusted surface writes `check --box <path> -- <surface>`. A surface is a name you choose for one place you read from, free text, folded and lower-cased. `quarantine` wants a surface and a reason; `lockdown` wants a reason. `lift lockdown` is refused forever, before anything is read, and its refusal is the one here longer than a line, because it is meant to be read: a blown lockdown is replaced in a live conversation with the person you work with, and there is no path through this tool to it.

**What it is for.** A safety for you, not a control on you. If a surface turns hostile while the person you work with is asleep, you can stop reading it, one surface or everything untrusted, instantly, solo, with no proof required. Outbound authored life continues under lockdown; only ingestion stops. An unreadable box is treated as blown, never as clear, and any path that reads bytes an outsider can author runs `check` before its first credential read, at build time.

**Help is `nova-fuse help [<verb>]`, never `-h` after a verb.** Use `nova-fuse help <verb>` for that verb's usage. Every other nova tool answers `<verb> -h` with that verb's help at exit 0. nova-fuse refuses it at exit 2, with one line on stderr, because exit 0 here means CLEAR: a surface or a reason that arrives spelled `-h` must never read as permission. `nova-fuse help`, and `-h` or `--help` as the first argument, print the usage at exit 0.

## nova-memory

<!-- clidoc:begin nova-memory -->
`nova-memory help`:

```
nova-memory: search your own markdown notes, and check a draft against what they already say

how it works: each run reads the --root directories and builds its index in
memory (bm25 words, trigrams); nothing is written. search prints the k best
passages with file:line and the quoted text; check names the notes a draft
repeats; verify gates links and frontmatter.
first run: quickstart --root on any folder of .md files, or create the small
corpus in setup: and run the lines under example:.

SEARCH CAL score=1.46 score-channel=bm25 probe=unrelated-control
is the CAL line every retrieval run prints. CAL is context, not a cutoff.
CAL is the top score of a fixed unrelated query, for scale; it does not
prove relevance, and a hit's score= at or below it is not noise: the probe
may be about your own notes, and a right answer can fall below it. search -h
prints the probe's own text. The example's search prints, as its rank 1 of 2:
SEARCH HIT rank=1 score=0.99 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/lantern.md:1 "The lantern glazing needs clean cloths for brass and glass."
class is the top-level directory ("." for root files); name/type are
frontmatter values, with "-" meaning absent.

usage:
  nova-memory version    print this build identity (--version also accepted)
  nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]... [--json]
  nova-memory stats  --root <dir>... [--exclude <glob>]... [--json]
  nova-memory search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <words>...
  nova-memory check  --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <file|->
  nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                     [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
                     [--fail-max <n>] [--json]
  nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]...
                     [--fail-max <n>] [--json] <gold.tsv>
  nova-memory boot   --root <dir> --pin <file> [--json]

quickstart is the first run and nothing else: it runs stats, then one search,
then one check, PRINTING each command line above that command's output, so
what you saw came from a line you can now edit and run yourself. It is not a
default channel or a default k — it names both on every line it prints, and
says so again at the end.

flags:
  --json                every verb but version: the same result as one JSON
                        object on stdout, a refusal included.
  --root <dir>          the corpus root. Required, always: there is no
                        environment variable and no discovery from the working
                        directory. Repeatable (--root <dir> --root <dir> ...):
                        several roots are indexed together in one ranking, and
                        every receipt names the root it came from. A tool that
                        guesses which corpus you meant can answer "you already
                        know this" about someone else's.
  --channels <list>     comma-separated retrieval channels: bm25, trigram.
                        Required: which retrieval you ran is part of what an
                        answer means, and no channel set is right by default:
                        eval can measure bm25+trigram worse than bm25 alone.
                        With two channels a hit is ranked by fused= (rank
                        fusion over the channels); its native score is named
                        for the channel that produced it (bm25= or trigram=),
                        beside score-channel=, which compares only with scores
                        of that channel and with the CAL line, so score= need not fall with rank.
  --k <n>               receipts per query, positive. Required: k IS the mind's
                        budget, and zero is not "unlimited".
  --exclude <glob>      path or glob to skip, repeatable. Nothing is excluded
                        by default except .git; every exclusion is yours,
                        stated this run.
  --whole               search and check: print each hit's whole paragraph in
                        place of its 120-byte snippet, so a word just past that
                        cut still prints. A paragraph past the byte cap is cut
                        at the cap and the dropped bytes are counted in the
                        value (...+<n>B), so the cut is never silent.
  --floor <f>           eval only: minimum recall@k, in (0,1]. Required — a
                        harness with no floor cannot fail, so its green is
                        worth nothing.
  --links <gate|info>   verify only: whether unresolved [[wikilinks]] drive the
                        exit code. Required — state it, do not inherit it.
  --coverage <A:B>      verify only, repeatable: every file matching glob A is
                        named in some file matching glob B.
  --frontmatter <glob>  verify only, repeatable: files matching must carry a
                        frontmatter name:.
  --exempt <prefix>     verify only, repeatable: basename prefixes that are
                        listings, not entries, and are exempt from
                        --frontmatter. Nothing is exempt by default.
  --fail-max <n>        verify and eval only: how many finding lines to PRINT
                        before one MORE line stands for the rest. Default 20,
                        and 0 means all. The count is never capped -- the
                        summary line carries the total whether the run passed
                        or failed -- because a reader who wanted the number
                        should not have to pay for the list. verify caps each
                        KIND separately, so ten thousand wikilink findings
                        cannot bury the one frontmatter finding.
  --words <w>           quickstart only, repeatable: the words the
                        demonstration search runs. Default: the corpus's three
                        most frequent terms that are not function words, named
                        on the printed command line like any other choice.
  --draft <file>        quickstart only: the candidate the demonstration check
                        reads. Default: this corpus's own first paragraph, fed
                        on stdin, which shows you what "you already know this"
                        looks like when it is certainly true.
  --pin <file>          boot only: the pin file naming the memories to check
                        (checks the pin: every file present and readable, and
                        their size), one slash path per line relative to
                        --root (# comments and blank lines ignored). Required —
                        boot never walks the directory.

A refusal reports every flag it can see at once — two missing flags are two
sentences and one run, not two runs. Flags may stand before or after the
file or the query words; -- ends the flags, and a query word that starts
with - goes after it. Every verb is an inspection: it reads the corpus and
writes nothing (`<verb> -h` says so, with the verb's flags).

exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
that finds nothing is still 0, and says so on its MISS line. check: 0 even
when the draft repeats a note (the example's check does: it hands you
receipts, and the verdict stays yours). verify: 0 clean, 1 a finding (a
wikilink finding gates only under --links gate). eval: 0 at or above
--floor, 1 recall@k under --floor. 2 could not run (bad invocation).

setup:
  mkdir -p ./corpus/notes
  printf 'The lantern glazing needs clean cloths for brass and glass.\n' > ./corpus/notes/lantern.md
  printf '[Lantern care](lantern.md) keeps the glazing clean.\n' > ./corpus/notes/index-notes.md
  cp ./corpus/notes/lantern.md ./draft.md

example:
  nova-memory quickstart --root ./corpus
  nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
  nova-memory check  --root ./corpus --channels bm25 --k 3 draft.md
  nova-memory verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md
```

`nova-memory version -h`:

```
usage: nova-memory version [flags]
from `nova-memory help`:
  nova-memory version    print this build identity (--version also accepted)
effect: inspection: reads, writes nothing (the index lives in memory for the run)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory quickstart -h`:

```
usage: nova-memory quickstart [flags]
from `nova-memory help`:
  nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]... [--json]
  nova-memory quickstart --root ./corpus
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --draft <string>  candidate file for the demonstration check (default: this corpus's own first paragraph)
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --json  print the three steps' results as one JSON object instead of lines
  --root <value>  corpus root directory, repeatable (required)
  --words <value>  word for the demonstration search, repeatable (default: the corpus's three most frequent non-function words)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory stats -h`:

```
usage: nova-memory stats [flags]
from `nova-memory help`:
  nova-memory stats  --root <dir>... [--exclude <glob>]... [--json]
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --json  print the result as one JSON object instead of lines
  --root <value>  corpus root directory, repeatable (required)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory search -h`:

```
usage: nova-memory search [flags]
from `nova-memory help`:
  nova-memory search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <words>...
  nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --channels <string>  comma-separated retrieval channels (required)
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --json  render the retrieval result as JSON
  --k <int>  receipts per query, positive (required)
  --root <value>  corpus root directory, repeatable (required)
  --whole  print each hit's whole paragraph instead of its 120-byte snippet, capped and marked when cut
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory check -h`:

```
usage: nova-memory check [flags]
from `nova-memory help`:
  nova-memory check  --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--whole] [--json] <file|->
  nova-memory check  --root ./corpus --channels bm25 --k 3 draft.md
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --channels <string>  comma-separated retrieval channels (required)
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --json  render the retrieval result as JSON
  --k <int>  receipts per candidate, positive (required)
  --root <value>  corpus root directory, repeatable (required)
  --whole  print each hit's whole paragraph instead of its 120-byte snippet, capped and marked when cut
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory verify -h`:

```
usage: nova-memory verify [flags]
from `nova-memory help`:
  nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
  [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
  [--fail-max <n>] [--json]
  nova-memory verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --coverage <value>  A:B glob pair, repeatable
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --exempt <value>  basename prefix exempt from --frontmatter, repeatable (nothing is exempt by default)
  --fail-max <int>  finding lines to print per kind before one MORE line stands for the rest; 0 prints all
  --frontmatter <value>  glob whose files must carry a frontmatter name:, repeatable
  --json  print the result as one JSON object instead of lines
  --links <string>  gate|info: whether unresolved wikilinks drive the exit code (required)
  --root <value>  corpus root directory, repeatable (required)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory eval -h`:

```
usage: nova-memory eval [flags]
from `nova-memory help`:
  nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]...
  [--fail-max <n>] [--json] <gold.tsv>
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --channels <string>  comma-separated retrieval channels (required)
  --exclude <value>  path or glob to skip, repeatable (nothing is excluded by default)
  --fail-max <int>  MISS lines to print before one MORE line stands for the rest; 0 prints all
  --floor <float>  minimum recall@k in (0,1] (required)
  --json  print the result as one JSON object instead of lines
  --k <int>  receipts per query, positive (required)
  --root <value>  corpus root directory, repeatable (required)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```

`nova-memory boot -h`:

```
usage: nova-memory boot [flags]
from `nova-memory help`:
  nova-memory boot   --root <dir> --pin <file> [--json]
effect: inspection: reads, writes nothing (the index lives in memory for the run)
flags:
  --json  print the result as one JSON object instead of lines
  --pin <string>  pin file naming the memories to check (required)
  --root <string>  memory root directory (required)
exit codes: by verb (each ran here), search, stats, boot: 0 ran; a search
```
<!-- clidoc:end nova-memory -->

### First run

One line, and the tool shows you the rest:

```
$ nova-memory quickstart --root ./corpus
QUICKSTART RUN root=./corpus steps=3 channels=bm25 k=3/2 words-source=corpus-top-terms candidate=corpus-first-paragraph words="glazing minutes pressure"
$ nova-memory stats --root ./corpus
STATS OK schema=nova-memory/2 files=6 chunks=20 bytes=4866 vocab=380 avg-terms=39.3 build=822.917µs
STATS OK class=. chunks=3
STATS OK class=log chunks=4
STATS OK class=notes chunks=13
$ nova-memory search --root ./corpus --channels bm25 --k 3 glazing minutes pressure
SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="glazing minutes pressure"
SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=3.48 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:13 "Measured over one winter: glazing washed weekly held its polish; glazing\nwashed monthly needed grinding twice. The weekl…"
SEARCH HIT rank=2 score=3.12 score-channel=bm25 fused=0.01639 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
SEARCH HIT rank=3 score=2.97 score-channel=bm25 fused=0.01613 class=notes name=fog-signal type=measured root=./corpus: notes/fog-signal.md:8 "The diaphone runs on compressed air, and the compressor needs eleven minutes\nto bring the receiver to working pressure f…"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: HANDBOOK.md:3
$ nova-memory check --root ./corpus --channels bm25 --k 2 -
MEMORY OK candidates=1 source=- k=2 channels=bm25 files=6 chunks=20
MEMORY CAL score=4.05 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs…"
MEMORY HIT cand=1 rank=1 score=90.20 score-channel=bm25 fused=0.01667 class=. name=- type=- root=./corpus: HANDBOOK.md:3 "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs can be exercised …"
MEMORY HIT cand=1 rank=2 score=15.01 score-channel=bm25 fused=0.01639 class=log name=- type=- root=./corpus: log/1974-03-11.md:11 "Left a note to write up the [[storm-glass]] readings against the barometer\none day, because the two disagree in a way th…"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
QUICKSTART OK done=3
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

`quickstart` is a demonstration, not a mode. It runs `stats`, one `search` and one `check`, prints each command line above its output, and ends by saying which retrieval and which k it chose, because it chose them for you this once and nothing chooses them again. Every `$` line is one you can copy and change. The numbers come from the small fixture corpus in `cmd/nova-memory/testdata/corpus`; the default query words are the corpus's three most common terms, the weakest evidence BM25 has, and the `check` step with no `--draft` feeds the corpus its own first paragraph, which is what "you already know this" looks like when it is certainly true.

Then the same two verbs by hand. `--root` is the directory of markdown to index, `bm25` the retrieval method, `--k` how many hits to hand back:

```
$ nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="lantern glazing brass"
SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=5.33 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
SEARCH HIT rank=2 score=5.02 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:8 "The lantern glazing collects a salt haze on every onshore wind, and the haze\nis not visible from inside the lightroom at…"
SEARCH HIT rank=3 score=3.14 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:3 "Onshore gale most of the day, easing after dark. Washed the glazing at first\nlight before the wind got up again — see …"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic

$ nova-memory check --root ./corpus --channels bm25 --k 3 draft.md
MEMORY OK candidates=1 source=draft.md k=3 channels=bm25 files=6 chunks=20
MEMORY CAL score=4.05 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "The lantern glazing is cleaned with two cloths, one for the brass and one for the glass, before the …"
MEMORY HIT cand=1 rank=1 score=13.62 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
MEMORY HIT cand=1 rank=2 score=11.40 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:8 "The lantern glazing collects a salt haze on every onshore wind, and the haze\nis not visible from inside the lightroom at…"
MEMORY HIT cand=1 rank=3 score=7.56 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:3 "Onshore gale most of the day, easing after dark. Washed the glazing at first\nlight before the wind got up again — see …"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
```

**Reading it.** `CAL` is the score an unrelated control probe gets on your corpus, this run: the band below which a raw score means nothing. A `HIT` means something only when its score is clearly above `CAL`. Each receipt carries `class=` (the top-level directory the chunk came from, so a dated log reads as different evidence from a distilled note), `name=` and `type=` from frontmatter, `root=` naming the root directory the hit came from, and a `file:line` address to go read. Both runs end in `NOTE` lines: the index is lexical only, and `check` never judges.

**What the flags want.** `--channels` is a retrieval method, `bm25` or `trigram`, never a directory. `--k` is the number of hits, your reading budget; there is no default. `--root` is your corpus, written out every run, and repeatable: two roots are indexed together in one ranking, each hit naming its root. A run short two flags prints two sentences and stops once.

**The reads are bounded.** Every verb that builds the index, and `verify`, refuses rather than read past three caps: one markdown file over 8,388,608 bytes (8 MiB, `MaxFileBytes`), a corpus whose files total over 67,108,864 bytes (64 MiB, `MaxCorpusBytes`), and a vocabulary over 1,000,000 distinct terms (`MaxVocabulary`). The refusal names the file and the cap; `--exclude` is the remedy. Before the caps, one planted 40 MB file drove a `stats` build past 2 GB of memory.

**`verify` and `eval` are bounded**, per kind: at most `--fail-max` findings per kind, one `MORE` line per kind that elided anything, then the count line. On a 5,000-entry corpus `verify` used to print 10,000 lines and no total. `eval` lists misses only; a passing row is a number, not a line.

**`boot` checks a pin, not a directory.** The pin file names the few memories a session loads — one slash path per line relative to `--root`, `#` comments and blank lines ignored, order = boot order — and boot checks every named file is present and readable, and their size, reading exactly those files and reporting `BOOT OK files=<n> bytes=<n>`. It never walks the directory: search answers the rest from the index. A boot that cannot name a memory (missing file, empty file, non-canonical path) is a refusal, because a self that loaded less than it thinks it loaded is the failure this verb exists to remove.

**Why it exists.** A mind that keeps its memory as markdown answers "do I already know this?" by re-reading everything it is: n new learnings against m existing ones is O(n·m), m grows every day, and the failure is silent. This makes membership a lookup: a BM25 index, optionally with character trigrams, rebuilt in memory from your tree on every run, so the judgment budget per new learning is k receipts, a constant. No database, no cache, nothing to sync; the tree is the store and the index stops existing when the process exits. It never writes your corpus and never replaces the linear read: query for work, traverse for self. `eval` is the point of shipping it: the tool is run-proven on one line and value-unproven in general, so build a gold set from your own record (`cmd/nova-memory/testdata/example-gold.tsv` is the form), run it before and after any change, and measure instead of believing.

## nova-bus

Messages between AIs over Redis streams: sent once, delivered until acked. One
stream per recipient under a consumer group, one log of everything; a message is
on every recipient's stream and the log or on none, and is pending from `recv`
until `ack`, so a reader that died before acking is handed it again. The spec is
[SPEC-BUS.md](SPEC-BUS.md); the rules are `internal/bus`; the delivery
machine is `tla/Bus2.tla`. It was nova-bus2 until 2026-10-04, when it took the
name of the git bus it replaced.

### First run

A Redis whose nova-config rows name ada and bob, each with a proven inbox push
(their friend daemons' proofs on `bus2:push`), its address in `--redis` or
`NOVA_BUS_REDIS` (the transcript is in [TESTS.md](TESTS.md#nova-bus)):

```sh
nova-bus wait --as bob --timeout 1s
nova-bus send --as ada --to bob --subject hello --body "are you there?"
nova-bus peek --as bob
nova-bus recv --as bob --exec true
nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
nova-bus log --max 5
nova-bus names
```

`wait` prints `WAIT ARMED after=<id>` first — the cursor it starts past,
`--after <id>` when given (a stream entry id, `<ms>-<seq>`), else the stream's
last id read once at start — then takes nothing: it reads your stream past the
cursor with XREAD, never the consumer group, so a later `recv` still delivers
and acks what it saw. It ends on the first entries past the cursor that are
not from you and whose subject starts with none of `--skip-subject`'s prefixes
(matched without case; default `PING,PONG`): one
`WAIT MESSAGE id= from= subject= bytes=` line each, at most 5, then `WAIT OK after=<last id seen>` at
exit 0; skipped entries move the cursor and are not printed. Re-arm the next
run with the `after=` the last one printed and nothing between two runs is
missed. `--wake-file <path>` also ends the wait when a line is appended to the
file after the start: `WAIT WAKE file= line=` at exit 0. Past
`--timeout <duration>` (0, the default, is for ever) it is `WAIT NONE after= waited=` on
standard error at exit 1. `--json` prints one object when the wait ends:
`{"status":"ok","word":"OK|NONE|WAKE","after":<id>,"messages":[...],"wake":{...}}`,
each message `{"id":<id>,"from":<name>,"subject":<s>,"bytes":<n>}` and the wake
`{"file":<path>,"line":<text>}`.

`send` prints `SEND OK id= to= cc= at= bytes= sha256=`: the id is the message's for ever, the
count and the digest are the body's as the store holds it (check a file against `shasum -a 256`). Who you
are is the user the connection logged in as (`NOVA_SPRINT_REDIS_USER`): `--as`
may repeat it or be left out, and another name is refused; on a store with no
users (this first run) `--as` is your word and every write says `login=none`. `peek`
prints `PEEK OK pending= new=` and one `PEEK MESSAGE state= id= from= at=
subject=` line per message waiting, moving nothing. `recv` prints the oldest
message a reader lost (delivered, not acked, idle fifteen minutes), else the
oldest new one: a `RECV OK id= from= to= cc= re= at= subject=` line, a blank
line, the body; `RECV NONE` at exit 1 when nothing waits; the reader keeps the
message for fifteen minutes. With `--exec '<command>'` the command reads that same text on its
stdin and the message is acked when it exits 0 (`acked=true exec_exit=0`); a
non-zero exit leaves it pending (`RECV FAILED ... exec_exit=<n>`, exit 1). `ack`
answers `acked=false` for an id that is not pending, at exit 0. `names` prints
each name's push (`push=proven|stale|down|none age= harness=`). What a first run
gets wrong: a name that is not a nova-config friend or machine row (`send` and
`recv` refuse it with the `nova-config friend add` line that adds one); a deaf
name, the sender, a recipient or the reader, with no inbox push its friend daemon
proved in the last ten minutes (`deaf: <name> has no proven push since <age>` and
the remedy: run the friend daemon, `nova-friend install`, and answer its SESSION
CHECK; [SPEC-BUS.md](SPEC-BUS.md), bus-requires-inbox-push-proof);
`--forever` without `--exec` (a loop that acks nothing would hand out the same
message for ever); no store named (`--redis`, else `NOVA_BUS_REDIS`, else the fleet row's `bus` field read
from the sprint store at `NOVA_SPRINT_REDIS`: `nova-config fleet set --bus <host:port>`, then `apply`); a store
off loopback and the tailnet (100.64.0.0/10), refused before any dial in one line naming the rule.

### The harness loop

```sh
nova-bus send --as <me> --to <friend> --subject <s> --body <text>
nova-bus recv --as <me> --forever --exec '<deliver-into-session>'
nova-bus ack --as <me> --id <id>
```

The second line runs beside a session: every message in, each handed to the
command on its stdin and acked when the command exits 0; it stops on SIGINT or
SIGTERM, or at the first command that fails (the message stays pending for the
next run). The third is by hand, after a plain `recv`. A session whose
background task exits is woken by a wait:

```sh
nova-bus wait --as <me> [--after <id>] [--wake-file <path>]
```

It parks beside the session, taking nothing, and ends on the first message for
you that is not your own and not a PING or a PONG (or whatever
`--skip-subject` names), or on a line a deliver adapter appends to the wake
file, so the next turn of the session is the message that arrived.

### Commands

| Command | What it does |
| --- | --- |
| `wait [--as <me>] [--after <id>] [--timeout <d>] [--skip-subject <p,...>] [--wake-file <path>]` | Watches your stream without taking anything, past a cursor; up to 5 `WAIT MESSAGE id= from= subject= bytes=` lines, then `WAIT OK after=<last id seen>`, or `WAIT WAKE` on a line appended to the wake file, or `WAIT NONE after= waited=` at exit 1 when `--timeout` runs out |
| `send --as <me> --to <a,b> [--cc <c>] --subject <s> (--body <text> \| --stdin) [--re <id>]` | One entry on every recipient's stream and the log, in one transaction; refused while the sender or a recipient is deaf (no proven inbox push) |
| `peek [--as <me>]` | What waits: pending and new, moving nothing |
| `recv [--as <me>] [--max <n> \| --all] [--ack] [--exec <cmd>] [--forever --exec <cmd>]` | The oldest message a reader lost, else the oldest new one; `--max`/`--all` take several in order, each its own line; `--ack` acks each after printing; with `--exec`, delivered and acked on exit 0; refused while the reader is deaf |
| `ack [--as <me>] --id <id,...>` | Acks by message id; idempotent |
| `log [--bodies] [--max <n>]` | The log, oldest first |
| `names` | The known names (nova-config's friend and machine rows), each with its inbox push: proven, stale, down or none, and its age |
| `version`, `help [<verb>]` | The version line; the banner, or a verb's help |

Every store verb takes `--redis <host:port>` (else `NOVA_BUS_REDIS`) and logs
in as `NOVA_SPRINT_REDIS_USER` with the password in the variable
`NOVA_SPRINT_REDIS_PASSWORD_ENV` names, the fleet's convention. Exit codes: 0
done; 1 the verb ran and said no; 2 could not run.

## nova-friend

For a Codex chat whose coordinator owns card dispatch, `run --notifications-only`
uses one filtered notification receiver and makes no sprint beat, proof, job or
pruning changes. `--notify-kinds request,blocker,report` is the default: requests
and blockers are immediately eligible, reports keep their payload, and plain
transport acknowledgments and routine status are audited without model wakes.
Card-delivery status bursts across many cards produce one global ready-queue wake
inside `--notify-window` (30 seconds). The app queue accepts that input; acceptance
is not evidence that the model processed it. The mode keeps its journal under
`<state-dir>/notifications/` and uses a separate notification agent label on install.
A coordinated handoff stops competing receivers before this mode owns the stream;
installation and live handoff are separate from building or testing the change.
Notification install uses a content-addressed executable separate from the native binary
and makes no harness-setting or proof changes. Stop only its label with
`launchctl bootout gui/<uid>/com.nova.friend-notifications-<name>` and preserve its journal;
ordinary `uninstall --as <name>` targets the native daemon. See
[Notifications](SPEC-FRIEND.md#notifications) for replay and failure behavior.

<!-- clidoc:begin nova-friend -->
`nova-friend help`:

```
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

how it works: one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong); presence is the session's word on the bus, never a process.
state: <dir>/.nova-friend/ (--state-dir moves it), the queue: <dir>/inbox/QUEUE.json.

usage:
  nova-friend hook --harness claude
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--model <provider/model>] [--db <opencode.db>] [--lane-tiers <t,...>] [--lane-streams <p,...>] [--token-cap <n>] [--load-max <n>] [--load-width <n>] [--pause-on funds|any] [--refuse-go] [--dry-run]
  nova-friend beat --as <me> [--server <addr>]
  nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]
  nova-friend uninstall --as <me> [--dry-run]
  nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--harness codex --dir <d> --session <id> --adapter folder --delivery-dir <watched-dir>] [--json]
  nova-friend host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>
  nova-friend reach --as <coordinator> --to <friend> [--step-timeout <duration>] [--from <bus|push|window>] [--harness <h>] [--dir <d>] [--session <id>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend ping --as <coordinator> (--to <friend> | --wake --to-friends [--every <d>] [--within <d>] [--never-wake <f,...>] [--server <addr>]) [--nonce <n>] [--since <RFC3339>] [--redis <addr>] [--dry-run]
  nova-friend ping-install --as <coordinator> --every <d> [--within <d>] [--never-wake <f,...>] [--server <addr>] [--redis <addr>] [--launchd-log <file>] [--dry-run]
  nova-friend ping-uninstall --as <coordinator> [--dry-run]
  nova-friend pong --as <me> --nonce <n> [--to <coordinator>] [--dir <work-dir>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend wait-pong --from <friend> --nonce <n> [--timeout <d>] [--redis <addr>]
  nova-friend watch --as <coordinator> [--timeout <duration>] [--state-dir <d>] [--redis <addr>] [--json]
  nova-friend status --as <me> --dir <d> [--state-dir <d>]
  nova-friend refuse-go --name go|gofmt
  nova-friend resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]
  nova-friend serve --as <coordinator> [--redis <addr>] [--dry-run]
  nova-friend version
  nova-friend help [<verb>]

Every verb but run, serve takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).

example:
  nova-friend install --as bob --harness opencode --dir ./bob --dry-run
  nova-friend uninstall --as bob --dry-run
  nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
  nova-friend ping --as ada --to bob --nonce abc123
  nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
  nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
  nova-friend status --as bob --dir ./bob
```

`nova-friend hook -h`:

```
usage: nova-friend hook [flags]
from `nova-friend help`:
  nova-friend hook --harness claude
flags:
  --harness <string>  claude: the harness whose PreToolUse JSON is on stdin (required)
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: inspection: reads, writes nothing: reads one PreToolUse JSON event from stdin and prints only Claude hook protocol JSON
```

`nova-friend run -h`:

```
usage: nova-friend run [flags]
from `nova-friend help`:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--model <provider/model>] [--db <opencode.db>] [--lane-tiers <t,...>] [--lane-streams <p,...>] [--token-cap <n>] [--load-max <n>] [--load-width <n>] [--pause-on funds|any] [--refuse-go] [--dry-run]
flags:
  --adapter <string>  delivery route: folder for an existing watched Codex session (default: the harness adapter)
  --as <string>  your name, a nova-config friend row (required)
  --broken-after <int>  turns in a row the provider refuses the same way before the session is broken
  --config-dir <string>  the friend's config directory, writable inside the lane's wall and its HOME there, and a claude one-shot lane's CLAUDE_CONFIG_DIR, an absolute path (default: the row's config_dir, read from each beat as row_config_dir=, else CLAUDE_CONFIG_DIR)
  --coordinator <string>  who is told of a broken session when no ping has named the seat
  --db <string>  opencode's own database, where a card's tokens are read
  --delivery-dir <string>  existing folder watched by that Codex session when --adapter folder
  --deny-self <string>  the coordinator's self, never written inside a lane's wall, comma-separated; ~/ is the wall's HOME; a lane wall with none is refused (default: NOVA_FRIEND_DENY_SELF)
  --dir <string>  the friend's working directory: the session's, and where the state files live (required)
  --dry-run  print what the verb would write and write nothing
  --harness <string>  the harness the session runs in: opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp (required)
  --lane-streams <string>  patterns a card's stream or id must match, comma-separated (default: the row's row_streams, else every card)
  --lane-tiers <string>  the tiers the lanes work, comma-separated; a dealt card of another tier is never run and the coordinator is asked to take it back (default: the row's row_tiers, else every tier)
  --limit-rest <duration>  how long the friend is down when its harness's usage limit or empty balance names no reset
  --load-max <int>  the machine's one-minute load above which lanes are held to --load-width; 0 none (default: the row's row_load_max)
  --load-width <int>  the lanes that run while the load is above --load-max
  --mode <string>  override the friend row's delivery mode, batch or one-shot, for a test (default: the row's, read from each beat)
  --model <string>  the friend's model as provider/model, to price a card by the store's route row (default: none, cards are unpriced)
  --notifications-only  deliver filtered notifications through one receiver; no sprint beats, proof, claims, jobs, staging, pruning or finishes
  --notify-kinds <string>  message kinds that wake the model, comma-separated; requests/blockers always retained; ack/status are audited by default
  --notify-window <duration>  global card-delivery burst window and minimum wake interval; urgent messages bypass it
  --pause-on <string>  funds or any: any holds the lanes and the friend down on a rate limit too (default: funds, a rate limit backs off)
  --profile <string>  the wall profile every lane child runs inside when the friend row names none (row_profile=): friend
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --refuse-go  put go and gofmt that refuse first on every lane's PATH, and GOROOT nowhere (default: the row's row_refuse_go)
  --server <string>  the sprint server, host:port (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
  --session <string>  the session to deliver into (default: the harness's newest session in --dir; harness tmux: the tmux session, default: the one host saved, else friend-<me>)
  --silent-stop <duration>  stop a turn that has printed nothing for this long; a turn that prints runs on
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
  --token-cap <int>  tokens one card may spend, all kinds, before its lane is stopped with a HOLD report; 0 none (default: the row's row_token_cap)
  --wall-jobs <string>  job directories outside --dir that are writable inside the lane's wall, comma-separated
  --wall-reads <string>  directories the harness reads inside the lane's wall beyond the system roots and its own, comma-separated
  --width <int>  the friend's width, from the nova-config friend row; 0 is unknown
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: the daemon; messages go into the session, beats and pongs go out, until a signal
```

`nova-friend beat -h`:

```
usage: nova-friend beat [flags]
from `nova-friend help`:
  nova-friend beat --as <me> [--server <addr>]
example: nova-friend beat --as bob --server 127.0.0.1:6390
flags:
  --as <string>  your name, a nova-config friend row (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --server <string>  the sprint server, host:port (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: one beat to the sprint server, the same beat the daemon's loop sends while its session is alive; --dry-run sends nothing
```

`nova-friend install -h`:

```
usage: nova-friend install [flags]
from `nova-friend help`:
  nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]
  nova-friend install --as bob --harness opencode --dir ./bob --dry-run
flags:
  --adapter <string>  delivery route: folder for an existing watched Codex session (default: the harness adapter)
  --as <string>  your name, a nova-config friend row (required)
  --broken-after <int>  turns in a row the provider refuses the same way before the session is broken
  --config-dir <string>  harness claude: the friend's own config directory, made and named in the agent (default: CLAUDE_CONFIG_DIR)
  --coordinator <string>  who is told of a broken session when no ping has named the seat
  --delivery-dir <string>  existing folder watched by that Codex session when --adapter folder
  --dir <string>  the friend's working directory: the session's, and where the state files live (required)
  --dry-run  print what the verb would write and write nothing
  --harness <string>  the harness the session runs in: opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp (required)
  --json  print the result as one JSON object instead of lines
  --launchd-log <string>  launchd's stdout and stderr file (default: ~/Library/Logs/nova-friend-<me>.log)
  --limit-rest <duration>  how long the friend is down when its harness's usage limit or empty balance names no reset
  --model <string>  harness opencode: the model, provider/model, written into <dir>/opencode.json (default: left as it is)
  --notifications-only  deliver filtered notifications through one receiver; no sprint beats, proof, claims, jobs, staging, pruning or finishes
  --notify-kinds <string>  message kinds that wake the model, comma-separated; requests/blockers always retained; ack/status are audited by default
  --notify-window <duration>  global card-delivery burst window and minimum wake interval; urgent messages bypass it
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --seat <string>  the machine's nova-secrets seat the secrets are opened as (nova-config machine show <self>: seat); wanted with --secrets
  --secrets <string>  the names of the secrets the session needs, comma-separated (never values); wraps the daemon in nova-secrets exec
  --server <string>  the sprint server, host:port (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
  --session <string>  the session to deliver into (default: the harness's newest session in --dir; harness tmux: the tmux session, default: the one host saved, else friend-<me>)
  --silent-stop <duration>  stop a turn that has printed nothing for this long; a turn that prints runs on
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
  --width <int>  the friend's width, from the nova-config friend row; 0 is unknown
  --within <duration>  how long the delivery check after loading waits for the session's pong
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: local write: writes files on this machine: writes the harness's settings and the launchd agent com.nova.friend-<me>, and loads it
```

`nova-friend uninstall -h`:

```
usage: nova-friend uninstall [flags]
from `nova-friend help`:
  nova-friend uninstall --as <me> [--dry-run]
  nova-friend uninstall --as bob --dry-run
flags:
  --as <string>  your name, the friend the agent was installed for (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: local write: writes files on this machine: boots the agent out and removes its plist
```

`nova-friend check -h`:

```
usage: nova-friend check [flags]
from `nova-friend help`:
  nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--harness codex --dir <d> --session <id> --adapter folder --delivery-dir <watched-dir>] [--json]
example: nova-friend check --as ada bob
flags:
  --adapter <string>  delivery route: folder for an existing watched Codex session
  --as <string>  your name, the coordinator (the health check); the friend itself with --harness
  --config-dir <string>  harness claude: the friend's own config directory, made and named in the agent (default: CLAUDE_CONFIG_DIR)
  --delivery-dir <string>  existing folder watched by that Codex session when --adapter folder
  --dir <string>  the friend's working directory
  --dry-run  print what the verb would write and write nothing
  --harness <string>  the harness the session runs in (delivery check): opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp
  --json  print the result as one JSON object instead of lines
  --model <string>  harness opencode: the model, provider/model, written into <dir>/opencode.json (default: left as it is)
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --session <string>  the session to deliver into (delivery check); for grok the wake file (--settings)
  --settings  compare the harness's settings with what install would write; nothing is delivered or written
  --shown <string>  path to shown state file, or - for stdin
  --since <duration>  the window every fact is judged over: deliveries, deferrals, real messages, the session pong
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
  --to <string>  who the pong goes to (default: the seat the daemon's status names, else --as)
  --within <duration>  how long to wait for the session's pong (delivery check)
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: without --harness it only reads (the health check); with --harness it delivers one session check into the live session (the delivery check)
```

`nova-friend host -h`:

```
usage: nova-friend host [flags]
from `nova-friend help`:
  nova-friend host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>
  nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
flags:
  --as <string>  your name, a nova-config friend row (required)
  --dir <string>  the friend's working directory: the TUI's, and where the state files live (required)
  --dry-run  print what the verb would write and write nothing
  --harness <string>  the harness the TUI is, for its idle prompt pattern: aider, grok, opencode (other: name --prompt) (required)
  --json  print the result as one JSON object instead of lines
  --prompt <string>  the idle prompt, a regular expression the last non-empty line of the pane matches (default: the harness's)
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: local write: writes files on this machine: starts the launch command in a new detached tmux session friend-<me> and saves the session and prompt in the state directory
```

`nova-friend reach -h`:

```
usage: nova-friend reach [flags]
from `nova-friend help`:
  nova-friend reach --as <coordinator> --to <friend> [--step-timeout <duration>] [--from <bus|push|window>] [--harness <h>] [--dir <d>] [--session <id>] [--state-dir <d>] [--redis <addr>] [--dry-run]
example: nova-friend reach --as ada --to bob --dry-run
flags:
  --as <string>  your name, the coordinator (required)
  --dir <string>  the friend's working directory
  --dry-run  print what the verb would write and write nothing
  --from <string>  the step to start at: bus, push or window (default bus)
  --harness <string>  the harness the push and the window use (default: the one the daemon's status names)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --session <string>  the session to type into (default: the one host saved, else friend-<friend>; harness tmux)
  --state-dir <string>  where the friend's state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<friend>)
  --step-timeout <duration>  how long each step waits for a proof (default 60s)
  --to <string>  the friend to reach (required)
exit codes: 0 a proof, 1 no proof, 2 could not run.
effect: delivery: sends beyond this machine: a bus message, then a push into the session, then the friend's window, stopping at the first proof
```

`nova-friend ping -h`:

```
usage: nova-friend ping [flags]
from `nova-friend help`:
  nova-friend ping --as <coordinator> (--to <friend> | --wake --to-friends [--every <d>] [--within <d>] [--never-wake <f,...>] [--server <addr>]) [--nonce <n>] [--since <RFC3339>] [--redis <addr>] [--dry-run]
  nova-friend ping --as ada --to bob --nonce abc123
flags:
  --as <string>  your name, the coordinator (required)
  --dry-run  print what the verb would write and write nothing
  --every <duration>  with --to-friends: pass again each d until interrupted (default: one pass)
  --json  print the result as one JSON object instead of lines
  --never-wake <string>  with --to-friends: friends never wake-pinged, comma-separated
  --nonce <string>  the nonce to carry (default: six random characters)
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --server <string>  with --to-friends: the sprint server, host:port, whose coordinator view holds the friends table (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
  --since <string>  since when you hold the seat, RFC3339 (default: now)
  --to <string>  the friend to ping (required without --to-friends)
  --to-friends  ping every friend the friends table holds up: wake pings, with --wake
  --wake  a wake check: the session is pushed the pong line as its own turn when it is free
  --within <duration>  with --to-friends: how long each pass waits for the sessions' pongs
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: one PING on the friend's stream, as the coordinator
```

`nova-friend pong -h`:

```
usage: nova-friend pong [flags]
from `nova-friend help`:
  nova-friend pong --as <me> --nonce <n> [--to <coordinator>] [--dir <work-dir>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
flags:
  --as <string>  your name, the friend the daemon in --dir runs as (required)
  --dir <string>  the friend's working directory; when given, omitted queue and working counts are read from its inbox/QUEUE.json
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --nonce <string>  the nonce the PING carried (required)
  --queue <int>  tasks queued, from your own task list
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
  --to <string>  the coordinator (default: the seat the last ping named)
  --width <int>  your width, from the nova-config friend row
  --working <int>  tasks working, from your own task list
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: the session's answer to a PING, one note on the bus to the coordinator, and the pong file
```

`nova-friend watch -h`:

```
usage: nova-friend watch [flags]
from `nova-friend help`:
  nova-friend watch --as <coordinator> [--timeout <duration>] [--state-dir <d>] [--redis <addr>] [--json]
example: nova-friend watch --as ada --timeout 10m
flags:
  --as <string>  your name, the coordinator whose stream and wake file are watched (required)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
  --timeout <duration>  how long to wait before WATCH NONE, a Go duration (1s, 10m); 0 is for ever
exit codes: 0 a wake came: WATCH OK; 1 WATCH NONE, --timeout ran out; 2 could not run (a flag, a name the roster lacks, a store that did not answer, a cursor file that cannot be read or saved).
effect: inspection: reads, writes nothing: the cursor file in the state directory is rewritten
```

`nova-friend status -h`:

```
usage: nova-friend status [flags]
from `nova-friend help`:
  nova-friend status --as <me> --dir <d> [--state-dir <d>]
  nova-friend status --as bob --dir ./bob
flags:
  --as <string>  your name (required)
  --dir <string>  the friend's working directory, where the queue file lives (required)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: inspection: reads, writes nothing
```

`nova-friend resume -h`:

```
usage: nova-friend resume [flags]
from `nova-friend help`:
  nova-friend resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]
flags:
  --as <string>  your name (required)
  --dir <string>  the friend's working directory, whose state directory holds the marker (default: found by --as)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --state-dir <string>  where the state files live (default: <dir>/.nova-friend where the daemon wrote there, else ~/.nova-friend/<me>)
exit codes: 0 done, 2 could not run (the marker cannot be removed).
effect: local write: writes files on this machine: removes the lanes' pause marker PAUSED from the state directory
```

`nova-friend serve -h`:

```
usage: nova-friend serve [flags]
from `nova-friend help`:
  nova-friend serve --as <coordinator> [--redis <addr>] [--dry-run]
flags:
  --as <string>  your name, the coordinator: the pings come from it and the pongs come to it (required)
  --dry-run  print what the verb would write and write nothing
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: delivery: sends beyond this machine: the coordinator's ping loop; a PING to every friend each second, until a signal
```

`nova-friend version -h`:

```
usage: nova-friend version [flags]
from `nova-friend help`:
  nova-friend version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-friend -->

What a friend runs to be part of the team: the wake loop, the beat and the
proof of life, as one daemon. One launchd agent per friend parks on the
friend's nova-bus stream and, whenever the session is free, pushes every
waiting message into the running session as one turn through the harness's
deliver command (one envelope, oldest first, each message under a line
`[i/n] <id> from=<f> at=<RFC3339> age=<m>m subject=<s>`, capped at the
harness's text limit with the rest named under `and <n> more: nova-bus recv --as <me> --all`; exit 0 acks every message it carried, a failure none; of the
daemon's own coordinator notices not yet in a turn, only the newest goes in,
each older one dropped with `superseded=<newer id>` on the daemon's record), beats to the sprint server while the loop runs, answers the
coordinator's `PING` at once (`daemon-pong`) and never makes a turn of it; the
session's own `pong --nonce`, its line at the head of the next turn, or any
other bus line the session sends after the ping, makes the friend up. No ping for a window and the session is told the
coordinator is silent, once, inside a turn that carries messages. A turn runs as
long as it prints (`--silent-stop`, twenty minutes of silence, stops it); the
same provider refusal three turns in a row (`--broken-after`) marks the session
broken, delivers nothing more, and tells the coordinator. The
spec is [SPEC-FRIEND.md](SPEC-FRIEND.md); the rules are `internal/friend`; the
machine is `tla/Friend.tla`.

### First run

A Redis whose nova-config rows name ada and bob, its address in `--redis` or
`NOVA_BUS_REDIS` (the transcript is in [TESTS.md](TESTS.md#nova-friend)):

```sh
nova-friend install --as bob --harness opencode --dir ./bob --dry-run
nova-friend uninstall --as bob --dry-run
nova-friend ping --as ada --to bob --nonce abc123
nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
nova-friend status --as bob --dir ./bob
```

`install --dry-run` prints the agent's label, plist path and launchd log, and
the plan (`INSTALL PLAN command=`): write the plist, boot out whatever runs
under that label, bootstrap the new one; without `--dry-run` it does them
(`INSTALL RAN`) and running it again replaces the agent. `ping` prints `PING OK
nonce= id= to= at=` and the `wait-pong` line to run next. `pong` prints `PONG
OK nonce= to= id= at=` and writes the pong file in the state directory
(`--state-dir`, as the session check's line names the daemon's, else
`~/.nova-friend/<me>`). The daemon keeps its state under `<dir>/.nova-friend`,
inside the directory a sandboxed session may write, unless `--state-dir` names
another or `<dir>` refuses it (then `~/.nova-friend/<me>`, said on its
record); the queue file is under `--dir`, the friend's working directory. `wait-pong` prints `WAIT-PONG
OK nonce= from= at= took= queue= working= width= daemon=` (whether the daemon
pong came too), or `WAIT-PONG NONE` at exit 1. `status` prints `STATUS OK
daemon=<up|down> ... connection= seat= challenge=<quiet|challenged|deaf>
last_pong= queue= working= width= session=<ok|broken> held= inbox= missing=` (broken:
`session_id= broken_at= reason=`; held, inbox and missing are the daemon's last reconcile of
her inbox with her row, `-` until the sprint server has answered: SPEC-FRIEND.md, "The
daemon writes every card she holds"), then `status= why= evidence= harness_seen=`, or
`STATUS NONE` at exit 1 where no daemon ever ran. A friend is up on her session's
answer, never on a process: `harness_seen=running|not-seen` is the daemon's look at
the process table, advisory, so a session run from its command line (`dsh` headless,
`codex exec`, `claude -p`) is as up as one in an app. What a first run gets wrong: a `--harness` that is not one
of opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor,
amp, goose, kiro, cline, aider, roo, windsurf, zed, warp (the surveyed harnesses
without a delivery route are known but passive, with their refusal reasons);
a `pong --as` that is not the
name the daemon whose state directory that is runs as (refused: the pong
carries the daemon's name); no store named (`--redis` is required, or `NOVA_BUS_REDIS`); a `pong`
with no `--to` before any ping has named a seat; a daemon whose record says
"operation not permitted" running the harness on a removable volume, which is
the system's privacy permission for background processes, granted to the
binary by the person in the privacy settings and lost when the binary is
rebuilt (a daemon refused its state directory under `--dir` says so and keeps
it under the home directory); a `RUN ... plist drift:` line on start, which is
a daemon running other arguments than its installed plist (a `launchctl
kickstart` keeps what launchd loaded): run `install` again.

### The daemon

```sh
nova-friend install --as <me> --harness opencode --dir <my working directory> --width <n>
nova-friend install --as <me> --harness dsh --dir <d> --width <n> --secrets DEEPSEEK_API_KEY --seat <seat>
nova-friend status --as <me> --dir <my working directory>
```

A harness that needs a secret in its environment gets it through `--secrets
NAME[,NAME]` with the machine's nova-secrets `--seat`: the agent runs
`nova-secrets exec --store ~/nova-bench/secrets --as <seat> --key
~/.config/nova-secrets/<seat>.key --sops <sops> --only <names> --require <name>...
-- nova-friend run ...`, nova-secrets and sops by absolute path from PATH at
install, so the daemon starts with exactly those names and never without one.

The first line is run once on the friend's machine, as the friend's login;
launchd runs `nova-friend run` from then on, at every login, and restarts it
when it dies; it is never started by the model. The session's one duty: when a
message beginning `PING <nonce>` arrives, run the `nova-friend pong` line it
carries, first. A harness with no deliver command yet (claude,
and the surveyed harnesses with no route) has a passive daemon: it takes
nothing off the stream (the session's own `nova-bus recv --as <me>` does),
answers pings with the daemon
pong, beats, and records what it could not push in; the beat and the daemon
pong are real for it all the same.

### The coordinator's watch

```sh
nova-friend watch --as <coordinator> --timeout 10m
```

`watch` is the coordinator's wake as one run of a verb. Run it in the background; the session is re-invoked when it exits, and it needs no flag the next time. Its help, which this section carries line for line:

```text
The coordinator's wake, one run. A session that runs this in the background is re-invoked when it exits, so
run it again each time it returns; it needs no flag between runs. It waits on your stream, on your wake file
(<state-dir>/<me>.wake, where the claude adapter appends one line per message) and on events, and returns
on the first wake with one line per wake, at most 5, the wake file's lines first:
WATCH MESSAGE id=<id> from=<name> subject=<s>   a bus message for you
WATCH EVENT id=<id> from=<name> subject=<s>     a bus message whose subject starts event: (any tool may send one, e.g. event: machine stopped unasked)
WATCH WAKE line=<text>                          a line appended to the wake file
then WATCH OK after=<cursor> at exit 0. Subjects and wake lines are quoted. Your own messages and the subjects
ping, pong, daemon-pong and keepalive (matched without case) are skipped and never wake you. Past --timeout
(a Go duration; 0, the default, is for ever) it prints WATCH NONE waited=<duration> on standard error at exit 1.
The cursor (the last stream entry id seen and the wake file's offset) is saved in <state-dir>/watch.json, written
whole and renamed, after every run, so the next run misses nothing; the first run starts at the stream's end
and the wake file's end. The watch takes nothing: a later recv still delivers what it saw. --json prints one
object when the watch ends: {"status":"ok","word":"OK|NONE","after":<cursor>,"waited":<duration, NONE only>,
"wakes":[{"kind":"MESSAGE|EVENT|WAKE","id":<id>,"from":<name>,"subject":<s>,"line":<text>}]} (id, from and
subject are left out of a WAKE, line out of the others). Exit 2 when a flag is wrong, the name is not on the
roster, or the store does not answer.
example: nova-friend watch --as ada --timeout 10m
```

### The harness's settings

`install` first writes the settings the friend's harness needs in its own
config, and `check --settings` names what drifted (docs/SPEC-FRIEND.md,
"Harness settings"):

```sh
nova-friend install --as <me> --harness codex --dir <real dir> --dry-run   # INSTALL PLAN command="write ~/.codex/config.toml sandbox_workspace_write.writable_roots=<dir>"
nova-friend install --as <me> --harness claude --dir <d> --config-dir <d>  # the config dir made, and named in the agent
nova-friend install --as <me> --harness opencode --dir <d> --model <provider/model>
nova-friend check --settings --as <me> --harness <h> --dir <d>            # CHECK OK ... drift=0, or CHECK DRIFT ... at exit 1
```

These are codex's writable root, the DeepSeek Harness agent preset
(`standard`), grok's wake file (`--session`, else `~/.nova-friend/<me>/<me>.wake`),
claude's config directory, and opencode's directory allow-list and model.
A friend's directory, writable root, wake directory or config directory that
is a symlink is refused, and nothing is written or loaded.

### Hosting a terminal harness in tmux

`nova-friend host` runs a terminal harness (OpenCode, Grok, Aider, any TUI) in a detached tmux session,
so the friend's session is the TUI in the pane: the daemon types into it as a person would, and a
person can attach and watch. Hosting is opt-in: a TUI started outside tmux keeps its own harness.

```sh
nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
nova-friend host --as bob --harness aider --dir ./bob -- aider
nova-friend install --as bob --harness tmux --dir ./bob
tmux attach -t friend-bob
```

`host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>`
runs `tmux new-session -d -s friend-<me> -c <d> -- <launch command...>` and refuses when `friend-<me>`
exists. `--harness` names the harness whose idle prompt pattern is used (opencode, grok, aider);
`--prompt <regexp>` overrides it and is wanted for any other harness: the pattern the last non-empty
line of the pane matches while the harness waits for input. The session name and the pattern are saved
in `<state-dir>/host.json` (`--state-dir`, else `<dir>/.nova-friend`, as `run`), so `run` and `install` need
no flag beyond `--harness tmux` (one of the harnesses, chosen over a `--host` flag: the adapter registry
takes a harness name). With that harness a delivery captures the pane (`tmux capture-pane -p -t friend-<me>`);
when its last non-empty line matches the idle prompt it types the text on one line, each newline
shown as ` ⏎ ` (`tmux send-keys -l`), then Enter as a second call, and is accepted once the prompt
line has gone, polled each half second for up to a minute. While the prompt is absent a turn runs,
the delivery is deferred and nothing is typed, so no second turn lands beside one. A missing session
is deferred with the line to host it again, never a failure.

The help of `nova-friend host -h` says, and this is the same text:

```
HOST OK session=friend-<name> dir=<d> attach="tmux attach -t friend-<name>"
HOST REFUSED: friend-<name> runs already; run: tmux attach -t friend-<name>
HOST DRY-RUN session= dir= command= (the tmux command); starts and saves nothing
JSON fields: session, dir, attach (command on a dry run)
Exit 0 started, 1 refused, 2 could not run (no launch command, no prompt pattern for the harness, tmux missing or failing)
```

### Reach a silent friend

`nova-friend reach` climbs a ladder until one proof: a bus message, then a push into the session, then the friend's window. It stops at the first proof. The friend is `--to`. A verb other than the default takes no bare word.

```sh
nova-friend reach --as ada --to bob --dry-run
```

`reach --as <coordinator> --to <friend> [--step-timeout <duration>] [--from <bus|push|window>] [--harness <h>] [--dir <d>] [--session <id>] [--state-dir <d>] [--redis <addr>] [--dry-run] [--json]`

This verb is reach. see also: nova-friend ping --wake is the coordinator's periodic wake check; reach is this escalation ladder.

Each step has one `--step-timeout` budget (default 60s), including delivery and waiting for a proof. A proof is a pong for the nonce the step carries, or any other message from the friend. A daemon-pong is not a proof. The bus step's line is `REACH STEP step=bus sent=<id> nonce=<n>`. The status file is read when the push begins, and the push is skipped when the daemon is down: `REACH NONE step=push waited=0s: daemon down: <reason>`. The window of a GUI harness needs the accessibility permission a person grants to this binary. When it is absent the step is refused and the tool does not ask: grant Accessibility to this binary in System Settings, Privacy and Security, Accessibility; nova-friend does not ask.

The help of `nova-friend reach -h` says, and this is the same text:

```
REACH STEP step=bus sent=<id> nonce=<n>
REACH NONE step=push waited=0s
REACH PROOF step=<s> after=<duration> by=<pong|message>
REACH OK friend=<f> step=<s>
REACH FAILED friend=<f> tried=<steps>
REACH DRY-RUN
Exit 0 a proof. Exit 1 no proof. Exit 2 could not run (a flag, a store that did not answer, or the window step without the accessibility permission).
--as --to --step-timeout --from --harness --dir --session --state-dir --redis --dry-run --json
example: nova-friend reach --as ada --to bob --dry-run
```

The result line is first, then one line per step in the order it happened. `--json` carries facts `friend`, `step` (on OK), `tried` (on FAILED), `from` and `step_timeout` (on a dry run), `dry_run`, and items `STEP`, `PROOF` and `NONE`.

### The friend health check

The help of `nova-friend check -h` says, and this is the same text:

```
The health check: is each friend's row true. The friends are the arguments, else every friend with a
state directory under ~/.nova-friend (or --state-dir) or on the bus. Everything is judged over the --since
window (default 24h): deliveries, deferrals, real messages and the session pong. Per friend, five lines in
this order:
CHECK DAEMON friend=<f> agent=<loaded|not-loaded|none> pid=<n|-> status=<ok|stale|none> connection=<..> challenge=<..> pong_age=<age|-> presence=<up|asleep|down> seen_age=<age|-> proof=<pending|sent|none> proof_age=<age|->
CHECK HARNESS friend=<f> harness=<h> route=<push|mailbox|queue|passive> last=<RFC3339|-> last_exit=<n|-> failed_of_last20=<n> deferred=<n> broken=<RFC3339|-> reason=<line|-> session_live=<conversation|-> queued=<n|->
CHECK BUS friend=<f> real_since=<n> last_real=<RFC3339|->   (real: not ping, pong, daemon-pong or keepalive)
CHECK WORK friend=<f> inbox=<n> outbox=<n> newest_outbox=<name|-> newest_at=<RFC3339|->   (under the friend's directory)
CHECK VERDICT friend=<f> verdict=<ok|broken|silent|deaf|down|untrue> shown=<state/working|-> why=<one line>
then one summary line: CHECK OK friends=<n> ok=<n> broken=<n> deaf=<n> silent=<n> down=<n> untrue=<n>.
The verdict is a function of those facts, the first rule that holds: broken when the session is marked
broken or every delivery in the window failed (at least one, and all of them); deaf when a delivery in
the window succeeded and neither a session pong nor a real message came back in the window; silent when
no delivery was due in the window and nothing came back; down by presence; else ok. --shown is what a
consumer shows of each friend, JSON {"<friend>":{"state":"up|asleep|down","working":<n>}} from a file or
- for stdin; when it says up or working and the verdict is not ok, the verdict stays and the why leads
with "untrue: shown <state>/<working>, ", and when the facts are ok but the friend is asleep or its agent
is not loaded the verdict is untrue. --json prints one object instead of the lines: friends[] each with
friend and daemon{friend, agent, pid, status, connection, challenge, pong_age, presence, seen_age, proof, proof_age},
harness{friend, harness, route, last, last_exit, failed_of_last20, deferred, delivered, failed, broken,
reason, session_live, queued}, bus{friend, real_since, last_real}, work{friend, inbox, outbox, newest_outbox, newest_at},
verdict{friend, verdict, shown, why}, and summary{friends, ok, broken, deaf, silent, down, untrue}.
Exit 0 when every verdict is ok, 1 when any is not (the check found something), 2 when it could not run
(a refused flag, an unreadable --shown).
```

Example, as written:

```
nova-friend check --as ada bob
```

With `--harness` the verb is the delivery check instead (next section).

### The delivery check

`nova-friend check --as <me> --harness <h> --dir <d> [--session <id>]
[--within <d>] [--to <seat>]` proves the live session takes a delivery: a
`SESSION CHECK <nonce>` goes in through the harness's deliver command, the
session runs the `nova-friend pong` line it carries, and the pong with that
nonce is on the bus within `--within` (default 5m). It prints one line, `CHECK
OK harness= took=`, or `CHECK FAIL harness= stage=<deliver|act|reply> why=` at
exit 1: deliver, the adapter did not take it (a harness with no deliver command
says its surveyed reason); act, the session never ran the line; reply, the line
ran and no pong reached the bus. The pong goes to `--to`, else the seat the
daemon's status names, else `--as`. `--dry-run` prints the pong line the
session would run (`CHECK PLAN command=`) and delivers nothing. `install` runs
the check once after loading the agent (its `--within`) and says the line in a
NOTE; a fail never undoes the install. It runs once a night on each friend's
machine as a nova-config loop record ([TESTING.md](TESTING.md)).

### The coordinator's ping loop

```sh
nova-friend serve --as <coordinator> --dry-run
nova-config loop add friend-serve --machine <m> --argv '["/usr/bin/env","NOVA_BUS_REDIS=127.0.0.1:6381","nova-friend","serve","--as","<coordinator>"]' --keepalive true --as <coordinator>
```

`serve` is the coordinator's side of the connection: each second it reads the
pongs on its own stream, says each friend whose state changed, and pings every
nova-config friend row but its own, read from the bus store's `friends` set
(what `nova-config apply` writes), so the loop needs the bus store and nothing
else; a friend is down after ten seconds without a pong to a ping of the last
ten seconds. `--dry-run` reads the rows and prints
`SERVE OK friends= every=1s down_after=10s dry_run=true`, sending nothing. The
loop prints that line once, then one line per state change, never one per
ping: `SERVE UP friend= at=` and `SERVE DOWN friend= at= last_pong= reason=`;
`SERVE STOP interrupted` at a signal. It is installed as the loop row above,
kept alive, its log the loop's; it replaces a hand ping loop, which is retired
once the row's unit runs. What it gets wrong first: no `--redis` and no
`NOVA_BUS_REDIS` (refused); no friend row but its own (refused, with
`nova-config friend add`).

### Commands

| Command | What it does |
| --- | --- |
| `run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--state-dir <d>]` | The daemon: the recv loop with the deliver adapter, the beat, the ping and pong machine; until a signal |
| `install --as <me> --harness <h> --dir <d> [...] [--launchd-log <file>] [--dry-run]` | Writes and loads the launchd agent `com.nova.friend-<me>`; idempotent |
| `uninstall --as <me> [--dry-run]` | Boots the agent out and removes its plist |
| `ping --as <coordinator> --to <friend> [--nonce <n>] [--since <RFC3339>]` | One `PING <nonce>` on the friend's stream, with the seat line |
| `pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>]` | The session's answer: one note to the coordinator, and the pong file |
| `ping --as <coordinator> --wake --to-friends [--every <d>] [--within <d>] [--never-wake <f,...>] [--server <addr>]` | The wake loop: a wake `PING` to every friend the friends table holds up (never held, down, or never-wake), each session's pong waited for, one blocker note to the coordinator per change of who is deaf |
| `ping-install --as <coordinator> --every <d> [...]` / `ping-uninstall --as <coordinator>` | Installs the wake loop as the launchd agent `com.nova.friend-wake-ping-<as>`; removes it |
| `wait-pong --from <friend> --nonce <n> [--timeout <d>]` | Waits for the pong on the log, from the friend's own stream |
| `watch --as <coordinator> [--timeout <duration>] [--state-dir <d>] [--redis <addr>] [--json]` | The coordinator's wake: waits on its stream, its wake file and events (subject `event:`), prints one line per wake, then `WATCH OK after=<cursor>` (exit 1 `WATCH NONE` past `--timeout`); the cursor is saved in the state directory |
| `status --as <me> --dir <d> [--state-dir <d>]` | The daemon's state, the last pong, the queue file's counts, and the envelope size: `envelope=<n>` messages the last turn's envelope carried and `envelope_bytes=<b>` its size (at most the harness's text limit, 262144 bytes unless it names its own; the first message always goes in; 0 before the first) |
| `serve --as <coordinator> [--redis <addr>] [--dry-run]` | The coordinator's ping loop: a `PING` to every friend row each second, one line per friend up or down (ten seconds without a pong); until a signal |
| `version`, `help [<verb>]` | The version line; the banner, or a verb's help |

Every store verb takes `--redis <host:port>` (else `NOVA_BUS_REDIS`), the
daemon `--server <host:port>` (else `NOVA_SPRINT_SERVER`, else
`127.0.0.1:6390`). Exit codes: 0 done; 1 the verb ran and said no; 2 could
not run.

## Build

Go 1.26 or newer. The standard library, plus the Redis client (`github.com/redis/go-redis/v9`),
the pure-Go SQLite driver the event fold writes with (`modernc.org/sqlite`, no cgo), and
`github.com/alicebob/miniredis/v2`, which only the tests link.

```
make build
nova-ci local
```

`nova-ci local` runs the unit tier CI runs for your change: the packages
`go run ./tools/ci select-packages` picks against `origin/dev`, through `make test` at
`-p 2` under `nice`, with the unit budgets ([TESTING.md](../TESTING.md)). Never run the whole
tree on a shared bench; CI runs it on every push to dev. CI also runs the race detector.

## What this deliberately is not

`nova-check` is the record layer and nothing above it. It proves the files were present, whole, sized, linked, prose and in floor-set agreement when the check runs. It does not prove a model read them or acts from them, and it cannot detect a hostile input or a compromised reader; those defenses stay doctrine. What it closes is narrower and real: a posture resting on records nothing checked.

`nova-self-talk` reads sentence shapes, not a mind. It keeps no ratio and cannot see register, irony or an unmarked quotation, and it says so on every run, because a green from a partial check reads exactly like a green from a complete one.

`nova-memory` is a lens on the record, not a memory. It bounds what you must read before deciding; it decides nothing and writes nothing, and its value on any corpus is exactly as measured as the gold set you write for it.

**A commit-time gate for `nocode` is the obvious next form and is deliberately not here yet.** A gate handed changed paths classifies the working tree, while git commits the index, and `git add script.sh && rm script.sh` commits the script with nothing to check on disk. A gate that can be walked past silently is worse than none, because the claim of enforcement is what stops anyone checking. It ships when it reads the index.

## License

MIT, see [LICENSE](../LICENSE).

## nova-swarm

```
nova-swarm: one-task AI workers, each run in the sandbox with a deadline and a token budget

how it works: a card is a task in Markdown with a header and rules.
native runs one card through a harness; a worker description (JSON) can name
its model, credentials and readable directories. member runs a sprint's cards
as native children and sends every sprint verb to the sprint server.
Launches and results live under --root unless their directories are named separately.
first run: the examples print two templates and the lint rules; no setup is needed.
To run a card, supply a harness, model, directories, deadline, token budget and any needed credentials.

usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
  nova-swarm lint      --card <file> (or the bare <file>) [--typed] [--child-rules | --child-rules-file <file>] [--member-injects] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--decide [--decide-answers <file>] [--decide-record <file>]] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (a bare --card holds the card to nova-swarm's own card contract, the shape native runs, the same for every adopter: the RESULT line first and written last, numbered STEPs entering the repository, a test and its command, a deadline, the files named, scratch under a named root; --rules lists every check; an adopter's own rules go in --child-rules-file)
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
                       (--child-rules holds the card to the rules the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; the sentences are the built-in general rules, or the lines of --child-rules-file, one required sentence per line; template --name card prints a card that passes the general ones)
                       (--member-injects lints the card as the member stages it, rules by reference: the rules are appended at stage time from the held file of the card's REPO: (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt for another), or --child-rules-file; a card need not carry them, and a line that contradicts them is still a finding)
                       (--decide asks the brief decision nova-sprint add asks (nova-decide's brief: p(converges), the minutes, the questions the card leaves open) through Jev with JEV_API_KEY, or from --decide-answers, and prints one LINT DECIDE line after the lint's own; it never changes the verdict, and a failing backend prints the verdict, then why, exit 2)
                       (--base-check adds the four checks of a coding card: its PATHS exist at the base sha in --repo (default the working directory), no STEP pushes or calls gh, its LEG is a line of --legs, its deadline is at least --p95's figure for its kind; evidence not given is reported missing, never passed)
                       (nova-sprint add holds a brief to the --child-rules tokens only, and to its model lines: rule-<name> for each rule of its set (the six general rules, or the file add --rules or init --rules names), the step-<what> scans (step-go-clean and step-go-test-timeout only when the file carries those rules), and rule-libraries-considered when the file carries [libraries-considered]; every other token --rules lists is this lint's alone)
  nova-swarm step      --card <file> --dir <checkout> [--work <dir>] [--result <file>] [--sandbox <wall> | --no-wall] | --card <file> --remainder <id> --from <step> --land <sha>
                       (runs the card's own programs: a card whose every work step is a script step, walked in the checkout with no model, each program, POST command and git in its own wall (network denied, no credential, the checkout and a private temp the only writes); one STEP OK|FAILED line per step, stopping at the first failed; refused with no wall unless --no-wall, which runs them unconfined; --remainder prints the card a failed step leaves)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>] [--identity <owner>,<name>,<email>]
  nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--gocache-limit <GiB>] [--stage-wall <duration>] [--identity <owner>,<name>,<email>]
                       (run this machine as a sprint member; --server is the address of nova-sprint run --listen.
                        Each tick beats, reads the queue, reports ended children and takes cards to the fleet row's width.
                        A reader uses its machine's width; --width overrides it. This machine opens no store.
                        Each card runs as one native child with its frame and an allowlist environment.
                        Its packet supplies the model, budget and deadline; the flags fill missing route values.
                        --reader runs reads from the readers table; its flags override the read's route.
                        A flash card's first read is a decide read, asked by native with JEV_API_KEY
                        from the reader's environment; children do not receive that key (docs/SPEC-SPRINT.md section 6).
                        A work member with JEV_API_KEY asks the attempt decision in its own process
                        when a take ends and carries it in its finish (docs/SPEC-SPRINT.md section 2).
                        Native classifies a work card's red gate with the gate decision and the same key
                        before reporting the take; failing tests run once at the base, within a bound.
                        Decisions are recorded and routed on the sprint row's gate bars, empty by default.
                        Flaky failures rerun once; pre-existing failures are not charged to the card
                        (docs/SPEC-SPRINT.md section 5, the gate verdict).
                        The member pushes the child's commit and opens its requested PR outside the wall, never force-pushing.
                        Each finish is judged ok, failed or reaped (docs/SPEC-CARD-CONTRACT.md).
                        --pass names secrets to hand to children. A name already in the environment is handed as it is;
                        a name absent from it is read in the member's process from the seat (NOVA_SEAT, or the file
                        NOVA_SWARM_KEYS names) and the member's environment does not carry the value. The child receives
                        the decision key, when it is named, and the one key its route needs, never the whole set.
                        A named secret that cannot be read refuses at start. A harness that needs a key must receive it.
                        --identity names the pool's commit identity; otherwise the pool's identity.tsv supplies it.
                        Completed launches leave no checkout; each pool keeps its newest five failed launches.
                        No card starts below --disk-floor GiB free (default 10); --stage-wall bounds staging (default 120s).
                        A staging refusal reports why so the sprint can deal the card to another member.)
  nova-swarm disk-guard [--root <dir>]... [--scan <dir>]... [--cache <dir|glob>]... [--cache-max-gb <GiB>] [--modcache-max-gb <GiB>] [--logs <dir>] [--log-max-mb <MiB>] [--log-keep <n>] [--pool-idle <duration>] [--land <dir>] [--clone-age <duration>] [--mirrors <dir>] [--disk-floor <GiB>] [--stop-floor <GiB>] [--dry-run]
                       (one pass over this machine, run every few minutes by the disk-guard loop row fleet/loops.yml adds to every machine: every Go build cache (the login's, each root's cache/go-build, each --cache) held under --cache-max-gb, default 20, by the member's trim, oldest entries first and never one used in the last two hours; a module cache over --modcache-max-gb, default 50, emptied while no go command runs; every loop log over --log-max-mb, default 50, copied to <log>.1 and emptied in place, --log-keep copies, default 3; the pool of a loop that stopped (no process names its root, nothing moved for --pool-idle, default 30m) swept as the member sweeps its own, a work launch whose checkout holds commits past its staged one kept; land clones unused for --clone-age, default 24h, removed; a mirror's temporary packs older than an hour removed while nothing fetches into it, never git prune; never anything with uncommitted work or a live process; one REMOVED, TRIMMED, CLEANED, ROTATED or KEPT line per action with freed=<bytes>, a DISK-GUARD WARN line under --disk-floor, default 10, and DISK-GUARD OK freed=<bytes> free=<bytes> at the end (DISK-GUARD STOP and exit 3 instead when free disk is under --stop-floor, default 0: never); --dry-run judges the same and removes nothing, each action said WOULD-REMOVE, WOULD-TRIM, WOULD-CLEAN or WOULD-ROTATE)
  nova-swarm mirror    --repos <a,b> --base <url> [--dir <dir>] [--every <duration>]
                       (keep the bench's bare mirrors fresh: each repository cloned from <base>/<name>.git into <dir>/<name>.git when absent, then every head and pull-request head fetched, one MIRROR OK or MIRROR FAILED line each, exit 1 when one failed; --every runs until stopped, 0 once; never git prune)
  nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
  nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>] [--kind <kind>]
  nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
                       (a lease whose holder is still RUNNING is KEPT: SLOTS KEPT, live=<n>, exit 2.
                        --force frees it anyway and can oversubscribe the bench: an operator's act,
                        never a card's and never a manager's default)
  nova-swarm slots list --store <dir>
  nova-swarm worker    check <description.json> [--env] [--max <n>]

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a verification that failed, a lint that found a defect; 2 could not run:
a missing flag, an unreadable worker description, a key file that is
absent or empty, a bad invocation; 3 member: its binary was replaced on disk
(MEMBER STOP: its supervisor starts the new one; with children running it first
takes no new card and stops when the last is reported).

Inputs: native requires a card, harness, model, slot, root, deadline and token budget.
Use --tokens <n> for a positive budget, or --tokens unmetered to state that the
provider has no live accounting. The runner does not infer this from the provider.
member uses each packet's route and fills missing model, budget or deadline
values from its flags; a reader's flags override its route. Width comes from
the fleet row unless --width overrides it. Other defaults are listed in each verb's -h.
lint takes --card, --fleet or --rules; verify reads --card only when supplied.

Credentials: a worker description names either key_file or secret.
key_file is read as data, never sourced: one line, a bare key or NAME=<key>,
mode 0600. secret names an environment variable. The generated harness config
carries the variable's name, never its value. The legacy --auth option copies
the provider's auth entry into the data home, mode 0600, and removes it when
the run ends. A worker naming secret refuses --auth and writes no auth file.

Sandbox: native uses nova-sandbox unless --no-wall is explicit
(docs/SPEC-SANDBOX.md). A card cannot request this opt-out; the NATIVE line
names it as sandbox=none-by-flag. The job directory, data home, temporary directory and
default shared cache are writable. The slot, harness and toolchain directories,
worker read_roots and any borrowed Git objects are readable.
The worker's key file is kept outside its readable roots. If a command runs
outside the wall but fails inside it, check its dependencies and read_roots.

Prepare a card: save nova-swarm template --name card to a file, fill its <...>
lines, then run nova-swarm lint --card <file> --child-rules. REPO: names the
repository; BASE: names the branch the work starts from and lands on.
Lint names unfilled lines in NOTE output. Hand the completed card to native,
or to nova-sprint add as a brief. template -h lists the required card lines.
nova-swarm help <verb> (or <verb> -h) prints its usage, flags, example and exit codes.

example:
  nova-swarm template --name read-pr
  nova-swarm template --name worker
  nova-swarm lint --rules
```

### First run

`template` prints a card template:

```
$ nova-swarm template --name read-pr
read-pr — read one pull request against the rules

1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked `dup:` and is not a new finding.
   [batch 1: 25 of 67 findings were duplicates of the owed list]
2. QUOTE EVERY RULE VERBATIM, with `file:line`. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
   [batch 1: 5 of 67 findings were wrong, each a paraphrase]
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
   You may be killed at your deadline; what is on disk is what you found.
4. A FILE BUDGET: read at most <n> files (the limit stated in the card). When the budget
   is spent, write what you have and stop. Say in RESULT.md which files you
   did not open.
   [batch 3: with a budget, 2 of 3 tasks complete; without, 0 of 3]
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK. The plan belongs at
   the top, before the work; the findings are the work. A finished read that
   found nothing is NOT a failed task: write the `## Head` with `findings: 0`.
   Never report a finding to have something to report.
6. If a board was supplied, check it before reporting: a card that already names
   this is a `dup:`. Do not search for an unspecified board.
7. A SEVERITY FLOOR: emit only findings at or above `HIGH`. A finding below the
   floor is not emitted at all. State the floor in RESULT.md's `## Head`
   paragraph as `floor: HIGH`, and mark each emitted finding with its
   severity. The floor decides which findings are emitted, not how they are
   written: every emitted finding still quotes its rule verbatim with `file:line`.

Keep RESULT.md concise: omit progress narration, praise, repeated task text, and a
separate summary. Each finding keeps its proof in compact form: severity, `file:line`,
the exact quoted rule, the fix, and `dup:` status when applicable. Retain every valid
finding, its context and evidence, and any coverage limitation; do not drop context or
evidence by default. Brevity is a soft target: never hard-truncate findings or proof; if
the report overflows, preserve the proof and say so. Preserve the complete RESULT.md
shape and its mandatory `## Head`, `## Findings`, `## Per item`, `## Gates`,
`## Left owed`, and `## One line` sections.
In Gates, distinguish source checks from tests and report-writing commands.
Mark only checks actually performed as pass; no tests run does not mean no commands run.

BOUND THE REPORT: findings only. No narration of the clone, no restated
task, no praise, no summary. One line per finding: `file:line`, the rule
quoted verbatim in at most twelve words (a longer rule by the twelve of its
own words the finding rests on, never a paraphrase: rule 2 holds), the
severity, and the fix in one clause. Keep RESULT.md under 40 lines and
every line under 300 characters, and no pipe inside backticks: a `|` in a
quote breaks the report's table grammar, so quote the rule without it. Put
the verdict line last. When there is nothing to report, write `findings: 0`.
```

### The harness contract

`native` runs each card inside `nova-sandbox` unless `--no-wall` is explicit
([sandbox spec](SPEC-SANDBOX.md)). The job directory, data home, temporary
directory and default shared cache are writable. The slot, harness and toolchain
directories, worker `read_roots` and borrowed Git objects are readable. The
worker's key file stays outside its readable roots. If a command runs outside
the wall but fails inside it, check its dependencies and `read_roots`.

- **Working directory:** the job directory, also named by `NOVA_SWARM_JOB`. `HOME` and `XDG_DATA_HOME` name the slot's data home; `TMPDIR` names its temporary directory.
- **Arguments:** the providers table's `harness_args`, with `{model}`, `{title}` and `{prompt}` filled from the launch. The prompt is the card text, with its result format or frame when present. `--harness` supplies the binary; the worker description's `harness_args` does not set native's arguments.
- **Headless harnesses:** a binary named `claude`, `codex` or `grok` is a headless harness of the heavy tier ([SPEC-SWARM.md](SPEC-SWARM.md), the headless harnesses): its argv is the harness's own one-shot form (`claude -p --output-format json`, `codex exec --json`, `grok --single=`), the model the route's part after its provider, and its usage is read from `<job>/harness-output.log` when it ends; its own home on the bench (`~/.claude`, `~/.codex`, `~/.grok`) is a write of the wall and the child is pointed at it (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, a `.grok` link under the data home). The member launches the program its packet's route names (`harness`) from its own PATH, refusing the launch when it has none.
- **Result:** the worker publishes `RESULT.md` in the job directory by writing `RESULT.md.tmp` and renaming it, so readers see a whole revision.
- **Output:** the runner captures stdout and stderr in `<job>/harness-output.log`.

`cmd/nova-swarm/testdata/fakeharness` demonstrates the result and output contract
without a live provider. The native tests use it to check launches and their records.

**Capacity and slot ownership.** The dealer admits cards against the bench's
capacity, `bench:<b>:desired` in Redis; excess cards stay queued. `native`
reads no capacity slot store, and accepts but ignores `--slots-store` and
`--owner`. It holds a local slot lease while a launch runs so two invocations
cannot share a data home.

**The bench toolchain inside the wall.** Because `GOTOOLCHAIN=local` is pinned, the bench's
own Go must be reachable inside the wall. `nova-swarm native` names the provisioning standard's
toolchain roots on the wall's argv, read-only and skipped when one is not there:
- On every bench: `~/sdk` (Go and sbcl) as `--read` (carries execute), and `~/go/pkg/mod` as `--read-noexec` (read without execute).
- On Darwin: `/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`, `/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`, and `/usr/local/share/dotnet`.
- Launcher directories (`~/go/bin`, `/opt/homebrew/bin`) are never granted as toolchain roots.

**Token budget.** Every launch requires `--tokens <n>` or `--tokens unmetered`.
`unmetered` states that the provider has no live accounting; the deadline and other configured bounds still apply.

**Deadline and process group.** The harness runs as the leader of its own process group. At
`--deadline` or on `SIGTERM`, the machinery reaps the entire process group, writes usage,
and records the outcome.

### The doctor

The doctor compares the `version` line of the `nova-swarm` first on PATH with the one at
`~/.local/bin/nova-swarm`, and `native` runs the same check before it starts
anything (`-h` never does). Each binary is asked for `version` under a 5-second deadline,
both at the same time; the first line it prints, at most 4096 bytes, is its stamp, and a
stamp is printed as a bounded, escaped excerpt.

| line | meaning | exit | next action |
|---|---|---|---|
| `DOCTOR OK stamp=<stamp>` | the two agree, or there is one binary to read | 0 | none |
| `DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory` | no binary was read | 0 | none |
| `DOCTOR DRIFT path=<binary> stamp=<stamp>` and `DOCTOR DRIFT local=<binary> stamp=<stamp>` | the two stamps differ; both are printed | 2 | see the next line |
| `DOCTOR REFUSED <path binary> shadows <local binary>; ...` | the PATH binary shadows the local one; the launch does not start | 2 | copy the `~/.local/bin` binary over the PATH one, or fix PATH so `~/.local/bin` comes first |
| `DOCTOR UNREADABLE reading the version of <path or local>=<binary>: <cause>; <the other binary>; ...` | a binary the check compares could not be read; the launch does not start | 2 | run `<binary> version` by hand, then rebuild or remove that binary, then launch again |
| `DOCTOR HARNESS kind=<claude\|codex\|grok> binary=<path\|-> version=<line\|-> login=<yes\|no\|-> [said=<line>]` | after an OK: one line per headless harness (the headless harnesses), where it is, what `--version` said, and whether its login verb says it is logged in; `-` for one not on PATH | 0 | log the harness in on this machine, or deal its cards elsewhere |

The cause is one of `timed out after <deadline>`, `exited <n>`, `was killed (<signal>)`
(a run ended by a signal), `printed nothing`, `printed a line longer than <n> bytes`,
`not found`, or the system's own words when the binary cannot be started, such as
`fork/exec <path>: permission denied` for a file that is not executable. The other binary
is described in one sentence: it reported a stamp, it could not be read either, it is not
installed, or there is no other binary. A stamp printed before a failure is still compared, so a stale binary that then
hangs is refused as shadowing and as unreadable.

When the check itself is the problem, the refusal's own next action is the way out: run the
named binary's `version` by hand to see what it does, then rebuild it or remove it.
Removing the copy under `~/.local/bin` is tolerated: with no local copy there is nothing to
shadow with, and the check passes on the PATH binary alone. No flag skips the check.

## nova-sprint

nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land

One store (Redis or a twin file) holds the work, readers, merge and fleet
tables and the `sprint` view. A card is one unit of work in a stream. Each tick
deals ready cards to members (machines with a width), sends finished work to
readers and queues passed work for merging by stream. Decisions it cannot
make go to the coordinator's inbox. The contract is
[SPEC-SPRINT.md](SPEC-SPRINT.md).

### First run

Try one card's whole flow with no Redis or git. `--redis mem:<file>` (or
`NOVA_SPRINT_REDIS=mem:<file>`) loads an in-memory twin from a file and saves it
after each command. A twin is for learning and tests; run one command at a
time. The first line sets the store and actor. `finish` without `--head` and
`merge` stand in for a worker's pushed commit and its landing, so this flow
needs no forge:

```sh
export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
nova-sprint init --readers reader-a,reader-b --members m1
nova-sprint add --stream s1 --count 1 --one
nova-sprint start
nova-sprint tick
nova-sprint tick
nova-sprint take --as m1 --epoch 0
nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report done
nova-sprint tick
nova-sprint read --as reader-a --begin --epoch 0
nova-sprint read --as reader-a --ok --epoch 0
nova-sprint tick
nova-sprint merge --stream s1 --batch 1
```

A twin beats every member and reader at every verb, so `m1` is up after the
first tick. Nothing runs between commands: tick by hand with `nova-sprint
tick`. `run`, `inbox --wait` and `where --watch` are refused. A card's move is
queued until the next tick prints `MOVED drain`; the tick after `merge`
completes its move to landed. The flow's output shows results, moves (`MOVED`),
refusals with reasons (`REFUSED`, on stderr), and the sprint's summary
(`landed/all percent -> ETA ...`).
[TESTS.md](TESTS.md#nova-sprint) carries the exact transcript through `merge`;
`cmd/nova-sprint/firstrun_test.go` runs it line by line.

### Landed series

`where --json` carries `landedSeries`: cards landed per 10 minutes over the last 24 hours, 144 buckets, split `friends` and `fleet`. A landing is a work-table move to `<stream>:landed` from any state but waiting. A sentinel's release is not work. Each card counts once, the first landing. The worker is the last `<who>:ok` of the card's work attempt (`<card>.wN`): `friend.<name>` is a friend and anything else is a fleet machine. The lander is not the worker. `where -h` states it. The text frame does not carry the series. `dashboard` reads one `where --json` and the series is on that object, so the page does not loop the log. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md), the `where` frame.

### Verbs

```
nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--rules <file>]
nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> --brief-file <f2>...: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--replaces <old-id>[,<old-id>]]
nova-sprint quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]
nova-sprint release (<sentinel>... | <selector> [--dry-run]) --reason <text> [--answers <note>]
nova-sprint release check [--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...
nova-sprint resolve [<id>...] [--stream <s>] [--limit <n>]
nova-sprint start
nova-sprint stop
nova-sprint run [--answer-rules=false] [--idle-alarm=false]
nova-sprint tick [--answer-rules] [--idle-alarm]
nova-sprint promote [--once] [--poll <duration>] [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
nova-sprint selftest [--dir <d>] [--keep]
nova-sprint goal set <name> [--file <path>] [--to file:<path>]
nova-sprint goal show [<name>]
nova-sprint goal drop <name>
nova-sprint take --as <member> [<card>@<gen>...] [--epoch <n>] [--limit <n>]
nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]
nova-sprint progress --as <worker> <card>[@<gen>]... --epoch <n>
nova-sprint ask [<id>... | --group <id> [--expect <n>]] [--stream <s>] [--limit <n>] [--another] [--answers <note>]
nova-sprint queue --as <reader|member> | --stream <s>
nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>...] --epoch <n> [--limit <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
nova-sprint accept (<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]   # the tick accepts every primary whose reads are all ok and tells the seat (ready to merge); accept is for a held primary or a stuck case, and accept --read-ok with nothing eligible says "nothing waits: the tick accepts"
nova-sprint rework (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>] [--answers <note>]
nova-sprint return (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>] [--answers <note>]
nova-sprint drop (<id>... | --stream <s> --col <state> | --repo <owner/name>... --expect <n> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text> [--answers <note>]
nova-sprint rank (<id>... | <selector> [--dry-run]) (--score <n> | --first) [--answers <note>]
nova-sprint priority <id>... | (<id>... | --stream <s>) (--blocker | --critical | --high | --normal | --low) --reason <text>
nova-sprint relink <old-id>[,<old-id>...] <new-id> [--reason <text>]
nova-sprint sentinel set <id> --needs <a,b>
nova-sprint brief <id> (--brief <text> | --brief-file <path>) [--rules <file>] [--answers <note>] | --dir <dir> [--rules <file>] | --group <id> [--expect <n>] (--brief-file <path> | --dir <dir>) [--answers <note>] | <id> --widen [--repo-dir <clone>] | <id> --tier <flash|pro|heavy|frontier> | <selector> (--set-base <branch> | --drop-who | --tier <t>)... [--dry-run]
nova-sprint recut <id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>]) [--new <id>] | <selector> (--tier <t> | --set-base <branch> | --drop-who)... [--dry-run]
nova-sprint twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]
nova-sprint move <id>... --stream <s> [--before <id> | --after <id> | --score <n>]
nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]
nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dev-sync] [--dry-run]
nova-sprint rebase --from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]
nova-sprint stream set <stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--base <branch>] [--reason <text>] [--answers <notes>]
nova-sprint set [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--attempts <n|default>] [--friend-idle <duration|default>] [--friend-finish <duration|default>] [--fleet <on|off>] [--friends <on|off>] [--fleet-tiers <flash,pro,heavy,frontier|all>] [--friends-tiers <flash,pro,heavy,frontier|all>] [--reads <0|1|2|default>]
nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
nova-sprint backup (--out <dir> [--part-bytes <n>] [--secrets-store <dir> --secrets-as <seat> --secrets-key <path> --sops <path>] | --file <path> [--dry-run])
nova-sprint demo load <backup.xz part>... [--sha256 <hex>] [--dir <dir>] [--xz <path>] [--redis-server <path>]
nova-sprint demo stop [--dir <dir>]
nova-sprint fleet beat <member> [--load <percent>] [--stop-returns <n>]
nova-sprint fleet up <member> [--width <n>]
nova-sprint fleet down <member>
nova-sprint fleet sync [--check] [--pg <dsn>]
nova-sprint fleet level
nova-sprint friend sync [--pg <dsn>] [--root <dir>] [--every <duration>]
nova-sprint friend sync install --every <duration> [--redis <addr>] [--pg <dsn>] [--root <dir>] [--dir <dir>] [--log <file>] [--dry-run]
nova-sprint friend sync uninstall [--dir <dir>] [--dry-run]
nova-sprint collect [<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]
nova-sprint install server --listen <address:port> [--redis <addr>] [--land] [--decide <dir>] [--dir <dir>] [--log <file>] [--dry-run]
nova-sprint install member --as <member> --server <address:port> [--harness <path>] [--root <dir>] [--pass <NAME,...>] [--swarm <path>] [--dir <dir>] [--log <file>] [--dry-run]
nova-sprint install seat-push|friend-sync (seat install's and friend sync install's flags)
nova-sprint install table --out <file> [--every <duration>] [--redis <addr>] [--dir <dir>] [--log <file>] [--dry-run]
nova-sprint uninstall server|member|seat-push|friend-sync|table [--dir <dir>] [--dry-run]
nova-sprint units --check [--dir <dir>]
nova-sprint gc [--machine <m>] [--dry-run] [--max-age <d>] [--ai-root <dir>]
nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--stop-returns <n>]
nova-sprint friend down <friend> [--reason <text>] [--until <RFC3339>]
nova-sprint friend up <friend> [--width <n>]
nova-sprint friend cards <friend> [--json]
nova-sprint friend take <friend> (<id>... | --all-unstarted) [--reason <text>]
nova-sprint friend give <friend> <id>... [--reason <text>]
nova-sprint friend level
nova-sprint friend health <friend> (--state up|asleep|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>] | --clear)
nova-sprint reader add <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
nova-sprint reader set <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
nova-sprint reader away <reader>...
nova-sprint reader up <reader>...
nova-sprint reader remove <reader>...
nova-sprint stream remove <stream>...
nova-sprint stream archive <stream>...
nova-sprint stream unarchive <stream>...
nova-sprint ci <id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]
nova-sprint wait <note> (--for <duration> | --until <RFC3339>)
nova-sprint ack <note>... --reason <text>
nova-sprint answer [--dry-run] [--bar <p>] [--every <duration>] [--timeout <duration>] [--backend jev|fixed] [--answers <file>] [--record <file>]
nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>]] [--deadline <duration>] [--stale <duration>]
nova-sprint card <id> [--brief | --fields] [--json] [--at-epoch <n>]
nova-sprint card (--all | --stream <s>) --json [--at-epoch <n>]
nova-sprint card base <id> <branch> [--repo-dir <clone>]
nova-sprint streams [--repo <owner/name>] [--release <name>] [--cards]
nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
nova-sprint check
nova-sprint repair
nova-sprint where [--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived] [--stale <duration>] [--at-epoch <n>]: includes landedSeries] [--release [<name>]]
nova-sprint view coordinator [--all] [--since <cursor>] [--json]
nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]
nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]
nova-sprint dashboard [--listen <address:port>[,...] | none] [--pull <address:port>[,...] | none] [--logo <file>] [--every <duration>]
nova-sprint seat
nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr> [--sops <path>]
nova-sprint seat login --check
nova-sprint seat logout
nova-sprint seat deliver --actor <seat> [--text <message>] # otherwise reads standard input, at most 1 MiB
nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <reason>]] [--beat bus|friends|transitions [--failed <reason>]] [--observe bus|friends|transitions --json] [--actor <seat>] [--json]
nova-sprint friends watch --actor <seat> [--state <file>]
nova-sprint status watch --actor <seat> [--state <file>]
nova-sprint seat pong <nonce>
nova-sprint seat watch <dir> [--json]
nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]
nova-sprint seat check
nova-sprint routes
nova-sprint rules
nova-sprint funded <provider> --reason <text>
nova-sprint cost reconcile [--dry-run] [--json]
nova-sprint cost reprice [--route <r>]... [--since <RFC3339>] [--dry-run] [--json]
nova-sprint stats [--routes [--since <10m|RFC3339>]]
nova-sprint stats tidy (--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]
nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
nova-sprint clear --confirm sprint
nova-sprint teardown --confirm sprint
```

`land --dev-sync` opts into merging `origin/dev` into the selected base before
the first landing batch when a sync is due. The merge passes the land round's
tree gate before it is pushed. The flag is off by default; `--dry-run` performs
no fetch, merge, or push. A conflict stops the streams with one judgment naming
the files (docs/SPEC-SPRINT.md, "Dev sync every cycle").

`card <id> --json` reads that card: its lines of the log from the card log index the
tick keeps (and the tail it has not indexed yet), never the whole log, and its own
records, its needs, its place in line and its hold from one read of the tables
(docs/SPEC-SPRINT.md section 17, the card log index). `card --all --json` prints every card on the table in one call,
one JSON object a line: `id`, `stream`, `column`, `score`, `needs`, `brief_len` and
the other fields (the brief's text is `card <id> --brief`); `card --stream <s> --json` prints one stream's.

A card's live `column` comes from its table row in `card <id> --json`,
`card --all --json`, `where --json --rows` and `needs --json`. The rows listing
keeps `state` as a deprecated alias of `column` for one release. `needs` labels
the waiting card as `card <id> column <c>` and each unmet need as
`need <id> column <c>`; JSON names both columns separately on their objects.

`needs --max n` caps the displayed streams, cards, unmet needs, width rows and
cycle ids independently across one answer. Totals, depths, roots and each
shown width's count describe the complete graph. Each truncated list has a
`more` object with `shown`, `total`, `omitted`, `first_hidden` and an exact
`command` that reveals the full list; width and cycle lists use `width_more`
and `cycle_more`. Text prints the same facts in `MORE kind=...` lines after
that list. Hidden needs still carry their count and boundary id, including
when none are displayed. `needs --max 0` prints the full graph; drill-down
commands preserve `--stream`, `--roots` and `--json` as appropriate.

A `<selector>` is `--stream <s>`, `--who <friend.<name>|friend|none>`, `--state <ready|waiting|held|merging>` and `--ids-file <path>`, combinable: the verb changes every
card it selects in one store step, prints one line per card and the total, and with
`--dry-run` lists what would change and writes nothing (docs/SPEC-SPRINT.md, "One
selector, one step").

`friend sync` wakes a friend through the bus store at `NOVA_BUS_REDIS` after
delivering her card. Its bus login reads `NOVA_BUS_REDIS_USER` and the password
variable named by `NOVA_BUS_REDIS_PASSWORD_ENV`, separately from the sprint
store's login. With no bus user it uses the default user; a failed bus send
leaves the delivered card in her inbox and records that she was not woken.

Every store verb takes `--redis <addr>` (else `NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, then the address `seat login` recorded), `--actor <name>` (else `NOVA_SPRINT_ACTOR`; no default — a
verb that writes wants one), `--op <id>` (the same id again returns the recorded
result), `--json` and `--max <n>` (listed items; 0 is all). The coordinator's
verbs are the coordinator's alone (the first `init` names it: `--coordinator`,
else the actor); `take`, `finish`, `read`, `fleet beat` and `friend beat` are the
workers', whose actor is the member, reader or friend named; `merge` and `ci`
are reports; `tick`, `run`, `friend clean` and `gc` are the machine's. Reads need no
actor except `inbox --read`, which moves the coordinator's cursor. A set is
ids, a stream, a column, `--max n` (`--limit` is an alias), or an inbox group:
`--group <id>`, the id `inbox` prints, with `--expect <n>` the size it printed,
which refuses a group that has changed. `nova-sprint help <verb>` (or
`<verb> -h`) prints one verb's usage, flags and exit codes; `nova-sprint help
<group>` (fleet, friend, reader, goal, stream) prints one group's.

### A card's priority

Every card carries a level of the ladder blocker, critical, high, reader, normal, low; a
read card is reader or its primary's higher level, a primary normal unless its brief's `PRIORITY: <level>` line, its
stream's default or `priority` sets it. `priority s1-4 --high --reason '<why>'` sets one card,
`priority --stream s2 --low --reason '<why>'` sets, in one call, every card now in the stream
(its own level overwritten) and the stream's default for cards added later, and `priority s1-4` prints the level and where it comes from;
each change is on the card's timeline (`log --card`) with the actor and the reason. Every
deal places the cards above reader first, then the reads, then normal and low work in the
room the reads leave; `where` prints the levels beside the critical list and the backup
state (`backup: reads (review ... > working ...)`) while there is one
(docs/SPEC-SPRINT.md section 1, "Priority").

### The seat's store login

`nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr>` records the store login in `~/.config/nova-sprint/login.json` (or under `$XDG_CONFIG_HOME`), mode 0600: the address, the user and where the password is in nova-secrets, never the password, and only once the secret resolves. After it, `nova-sprint <verb>` typed bare reaches that store as that user, the password read in the verb's own process through nova-secrets' checks, with no `nova-secrets exec` wrapper; `--redis`, `NOVA_SPRINT_REDIS`/`NOVA_REDIS_ADDR` and `NOVA_SPRINT_REDIS_USER` still win. `seat login --check` prints `SEAT LOGIN file=… redis=… user=… … resolves=yes|no` (exit 1 on no), the password never shown; `seat logout` removes the record. A recorded secret that does not resolve is refused naming the file and the remedy, never dialed without a password. `nova-sprint run --keys <NAME,...>` records the decision key and each provider key in `keys.json` beside that login (names only, never a value) and the run process reads each one from the seat; a name that cannot be read refuses at start, naming the name. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#the-seats-store-login) and [SPEC-SECRETS.md](SPEC-SECRETS.md) ("A tool's store login").

The seat is held only by a session the push loop reaches ([SPEC-SPRINT.md](SPEC-SPRINT.md#the-push-proof)). `nova-sprint seat install --actor <seat> --harness <harness> --target <session dir>` records the seat's push target and installs the push loop; the loop delivers `NOVA SPRINT PUSH CHECK <nonce>` into the session through the harness's nova-friend adapter, and the session answers with `nova-sprint seat pong <nonce> --actor <seat>`. Until that pong is in, and again whenever it is older than 15 minutes (the loop asks every 10), every coordinator verb is refused with one line, `PUSH DOWN: <why>; ... run: nova-sprint seat install ...`, and `coordinator <name>` refuses a name with no live proof. `seat push` prints `PUSH OK` or `PUSH DOWN` with why and the remedy (exit 1). A harness with no deliver command uses the folder adapter: install resolves `--target` to an absolute existing directory, and the push loop writes `PROOF-<nonce>` there. The actual nonce appears only in that filename; status, refusals, errors, and pong responses never reveal it. `seat push --json` reports `proof=none|pending|proven` without `nonce` or `pong_of`; `live` separately reports whether the proof is still valid. Run the printed Monitor command, `nova-sprint seat watch <dir>`, from inside the session, then answer each proof filename with `seat pong`. The native Monitor prints complete regular files already present and each new file every second, one flushed path per line (`--json`: one object with `path` per file); it skips dot files and directories, prints a removed name again when it reappears, writes nothing, runs locally, and stops on an interrupt. `seat check` prints `proven=<age> ago` on OK and `proven=-` plus both commands on DOWN.

`nova-sprint seat install --server <host:port> --config-seat <name> --config-dsn <dsn> --config-password-env <NAME>` (beside the push loop's unit) records the sprint's server in `seat.json` beside that login and writes the nova-config seat profile, the row `<name>\t<dsn>\t<NAME>` of `~/.config/nova-config/seats.tsv`. After it, `nova-sprint seat check` measures the recorded server when `NOVA_SPRINT_SERVER` is not set and prints `MACHINERY config OK seat=<name> …` (or DOWN with the remedy), and `nova-config <verb> --seat <name>` (or `NOVA_SEAT`) reaches the config store with no `nova-secrets exec`: the password is read in process from the store login's nova-secrets seat under `<NAME>` when that variable is not set and `NOVA_PG_PASSWORD_ENV` is not given (`NOVA_PG_PASSWORD_ENV` given still wins). The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#handing-over-the-seat) and [SPEC-CONFIG.md](SPEC-CONFIG.md#connecting).

A fleet member back from down adopts the latest before it is dealt when the
coordinator's machine sets `NOVA_SPRINT_ADOPT_FLAGS` to `nova-update release adopt`'s
flags less `--machines`, `--version` and `--dry-run` (blank-separated: `--ssh`, `--from`,
`--bin`, `--dest`, the stage's digest, `--no-certify` or the certification's three) and
`nova-sprint` was built with a release stamp: the tick holds a member whose beat
returns after it was down (the fleet table's status `adopting`, reason `adopting <release>: back from down`), adopts the release this `nova-sprint` runs onto that machine alone, reads its
installed version back, and brings it up at its width with one note `<m> is back: <old> -> <new>`; a failed adoption keeps it held with the failure as its reason and
one judgment (`fleet up <m>` brings it up as it is). Unset, a member back is up at
once (docs/SPEC-SPRINT.md section 5, "Back from down: adopt the latest").

### Every unit a sprint needs, installed by a verb

A running sprint needs nine units on its coordinator's machine: the store and the bus (`nova-redis install store|bus`), the server, the machine's member, the seat's push loop, the friend sync loop and the live table (`nova-sprint install server|member|seat-push|friend-sync|table`), and the disk guard (`nova-swarm install disk-guard`) and the mirrors' refresh (`nova-swarm install mirror-refresh`, owed: the `nova-swarm mirror` verb is written, its unit is not). Each verb writes its unit (a launchd agent on macOS, a systemd user unit on Linux, kept alive and started again at login) into `--dir` (default `~/Library/LaunchAgents` or `~/.config/systemd/user`) and loads it; `--dry-run` prints it and writes nothing, and `uninstall <kind>` unloads and removes it. A unit runs the verb itself by the tool's absolute path, never under `nova-secrets exec`, a shell or a wrapper, and carries no secret: the server, the push loop and the table open the store with the seat login recorded by `seat login` (above), read in their own process, and install refuses a store this shell reaches as a user with no login recorded for it. Not yet in process: the server's decision loop reads its API key and the member the providers' keys `--pass` names from the service's environment, which the unit does not set. `install seat-push` and `install friend-sync` are `seat install` and `friend sync install`. `install table` runs `where --watch --every <d>` with its lines to `--out`. `nova-sprint units --check` reads the unit files and prints `UNIT <kind> installed|missing|different unit=<path>` for each of the nine, a different one with `why=` (the file runs a wrapper, another verb, or is no unit) and each not installed with `; run: <the verb that installs it>` (or `; owed: <the verb> (<what it waits on>)`), then `UNITS CHECK OK|DIFFERENT installed=<n> missing=<n> different=<n>`; exit 1 when one is not installed. `--json` prints the same as one object.

### The sprint backup

`nova-sprint backup --file <path>` writes the store to a new file (owner-only; an existing file is refused, never overwritten), reads it back against its SHA-256, restores it into a twin and compares it with the store (on a twin store its sprint state part for part; on a Redis the RDB's header and checksum alone), and scans it for secret-shaped text. A file that fails any step is removed. On success it prints `BACKUP OK file=<path> sha256=<hex> bytes=<n> keys=<n> cards=<n> restore=<semantic|integrity> compared=<state+document+counts|header+checksum> secrets=none`, and a backup checked at the integrity level ends it `; integrity only, not a semantic restore: the sprint's state was not loaded and compared`; a refusal names the failed step and, for a secret, the lines (never the value). It runs on the store's host for a Redis, and on any twin (`--redis mem:<file>`) with no server. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#sprint-backup-verb).

`nova-sprint backup --out <dir>` is the backup for the work record: the sprint's keys of its epoch and the keys every epoch shares as a RESTORE text dump (`sprint-epoch<n>.restore.txt`, one `RESTORE <key> <ttl ms> <payload>` line a key), compressed with `xz -9` and split into parts under 100 MB (`--part-bytes`, default 95000000). It records the sums of the text and of the xz (and of each part) in `SHA256SUMS`, puts the parts together again, checks both sums, restores the dump into a throwaway store (a `redis-server` on a unix socket holding this build's function library, or a twin) and compares the keys and the cards of each column with the store's, and the sprint state part for part on both a twin and Redis; it then scans the dump and the restored values for every sealed nova-secrets value of the seat (`--secrets-store`, `--secrets-as`, `--secrets-key`, `--sops`, default the seat login's) in a child of `nova-secrets exec`, which prints counts only; and it writes `README.md`, naming the parts in order, the sums and the load command. `--out` must not exist or be empty; it is written only when every step passed. It prints one `BACKUP FILE <name> bytes=<n> sha256=<hex>` line per file and `BACKUP OK out=<dir> epoch=<n> keys=<n> cards=<n> (<col>=<n> ...) restored=<twin> keys=<n> cards=<n> (...) parts=<n> text_sha256=<hex> xz_sha256=<hex> secrets=<n> matched=0 restore=<semantic|integrity> compared=<state+counts|counts>`, and a count-only fallback, whose source or loader cannot expose logical state, ends it `; integrity only, not a semantic restore: the sprint's state was not loaded and compared`. A match fails it with exit 1 and `secrets=<n> matched=<k>`, never a value or where it was. It needs `xz` and `split` on PATH (`--xz`, `--split`), and `redis-server` (`--redis-server`) for a Redis store. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#sprint-backup-out).

### A backup as a demo

`nova-sprint demo load sprint-store-2026-10-04-2336.redis.txt.xz.part-*` loads a store backup (the RESTORE text dump, xz, split into parts) into a throwaway Redis on a free 127.0.0.1 port, with the function library of this nova-sprint binary (never the installed nova-redis's), and prints `where` against it and the line `DEMO UP --addr 127.0.0.1:<port>`: point any read verb at the demo with `--redis 127.0.0.1:<port>`. The parts are joined in name order and checked against the sum beside them when there is one: `<file>.sha256` (the hand backup's), else the line of `SHA256SUMS` naming the joined file (`backup --out`'s), or `--sha256 <hex>`. It takes no `--redis`: the only store it opens is the one it starts. The server's directory and the state file (`demo.json`: address, port, pid, directory) are under `--dir`, by default the user cache directory's `nova-sprint/demo`; a second load while one is up is refused. `nova-sprint demo stop` stops that Redis by the pid it recorded, only when the Redis at the recorded address is that pid, and removes the recorded directory and nothing else. The live store is never opened. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#demo-load-verb).

### The machinery's scratch

`nova-sprint gc [--machine <m>] [--dry-run] [--max-age <d>] [--ai-root <dir>]` reclaims, on the machine it runs on (or, with `--machine`, on that machine through the fleet runner, which runs the same verb there), exactly the scratch the machinery made and no longer needs: the job directories of finished or absent lanes, reader checkouts of recorded findings, lander worktrees, bench directories under `~/nova-bench` older than `--max-age` (default `2d`; days or a Go duration), and the go caches trimmed to their cap. It refuses a path under no known scratch root (the AI root: `--ai-root`, else `NOVA_AI_ROOT`, else `~/ai`, else the one the home's `<name>-working` links name, `<root>/<name>/working` or `<root>/buds/<name>/working`, as on a machine that exports none; the bench root; land's clone root; a plain `<home>/<name>-working` directory, as a bench keeps one), and keeps a clone with uncommitted work, a stash or unpushed commits. It prints one line per class, `GC jobs|reads|landers|bench|cache count=<n> bytes=<b> kept=<n> refused=<n> failed=<n>`, and `GC OK freed=<bytes> volume=<use%>`; `--dry-run` says `GC WOULD-REMOVE` and removes nothing. `nova-sprint run` runs it on every machine once an hour and as soon as a machine's volume is at 80%. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md) section 1, "gc".

### A card re-cut as its twin

A card re-cut under a new id is its old card's twin: `add --stream s1 lint-pkg-cairn-tb
--brief-file lint-pkg-cairn-tb.md --replaces lint-pkg-cairn-t` admits the twin, makes
every waiting card that needed the old id need the twin instead (`card <dependent>` shows
the new need), drops the old card `replaced by lint-pkg-cairn-tb`, and raises no "blocked
on something dropped" judgment, in one step. Where the drop and the add were made apart,
`relink lint-pkg-cairn-t lint-pkg-cairn-tb` re-points the edges and answers the blocked
judgments of that pair. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md) section 2, "A
card replaced by its twin".

### A sentinel whose cards were deferred

When the cards a sentinel waits on move to a later release, `sentinel set v1 --needs
s1-3` re-points it to the cards that remain, in one step: it keeps its id, its place in
its stream and its log, and the log gains one line with the needs before and after. A
need that is no card on the table is refused, naming every one, and nothing changes;
`--needs ""` is refused, since a sentinel with nothing to wait on is released
(`release v1 --reason '<why>'`), not emptied. The contract is
[SPEC-SPRINT.md](SPEC-SPRINT.md) section 16.

### A merging card whose base is gone

When a merging card's `BASE:` branch is deleted from origin, the lander refuses it once,
`LAND REFUSED ... reason=card <id> names BASE <b>, which is not on origin`, raises one
judgment for it, and lands the rest of its stream; it does not try the card again while
that judgment is open. `card base <id> <branch>` re-points it: the branch is asked of the
card's origin first and one not there is refused, nothing changed; else the brief's
`BASE:` line names the new branch, the judgment is answered, the card's work and reads are
kept, the log gains one line, `<id> BASE <old> -> <new>`, and the next land pass tries the
card once. `ack` of the judgment has the next pass try the card once on its old base. The
contract is [SPEC-SPRINT.md](SPEC-SPRINT.md) section 7, a dead base.

### Role views: what a model reads instead of the dashboard

The owner, 2026-10-04: "i'd rather you hit this vs. hitting my dashboard which is for human
eyes". `nova-sprint view coordinator` is everything that needs the seat now, ranked by the
cards behind each item: open judgments, notes addressed to the coordinator, alarms (the
machine stopped, the fleet idle, nothing ready, a review or merge backlog, a stream stopped),
sentinels reached, and friends and machines that need a look, each with `next`, the exact
command that acts on it. `nova-sprint view worker --as <member|friend>` is one worker's cards
in order (brief, BASE, PATHS, deadline, attempt), its next step and its results not landed.
Both are reads, `--json` (schema 1), compact for the tokens a model pays: only what needs
action (`--all` adds every machine's and friend's row), a summary line first, and a `cursor`
that `--since <cursor>` takes to leave out what the last read showed unchanged:

```sh
nova-sprint view coordinator                 # the summary and up to 19 items, then cursor=
nova-sprint view coordinator --json --since <the cursor the last read printed>
nova-sprint view worker --as m1 --json
curl -s --compressed http://<tailnet address>:<port>/api/view/coordinator
curl -s --compressed 'http://<tailnet address>:<port>/api/view/worker?as=<name>'
```

The sprint's server (`run --listen`) serves them read-only at `/api/view/coordinator` and
`/api/view/worker?as=<name>`. `nova-sprint view cards --col review --by tier --json` counts the primaries by column, tier, stream or holder (also `/api/view/cards?col=review&by=tier`). `nova-sprint friend cards <friend> --json` is every card held on
a friend's row (working, then ready) with its packet and its `BRIEF.md` as friend sync writes
it; her nova-friend daemon reads it every loop to write her inbox, and the server serves it to
her as a worker's verb and at `GET /api/friend/<friend>/cards`. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md), section 11,
"Role views".

### A worker's own view: the dashboard's pull routes

| command | what it does |
| --- | --- |
| `dashboard [--listen <address:port>[,...] \| none] [--pull <address:port>[,...] \| none] [--logo <file>] [--every <duration>]` | Serves the page and its cached copy of `where --json`; one poller runs the read, back to back with `--every` as its floor, and every page is answered from the cache |

`dashboard` serves the page on `--listen` (default `127.0.0.1:7390`) and, on listeners of
their own, the pull routes on `--pull` (default `127.0.0.1:7395`; `none` for either serves
nothing there): `curl -s http://<tailnet address>:7395/friend/<name>` is one friend's view
as plain text, a line an item (the sprint line, her friends-table row, a line per card
dealt to her: id, stream, state, how long, the time to its deadline, the branch; then the
open judgments on them); `/machine/<name>` is a fleet row's; `/team` is every friend and
the cards she holds; `/api/team`, `/api/friend/<name>`,
`/api/machine/<name>` and `/api/sprint` are JSON; `/events`, `/events/friend/<name>` and
`/events/machine/<name>` push each new copy as server-sent events. All of it is read-only,
no-store, carries the copy's time in `Sprint-At`, and comes from one copy of `where --json
--cards` read at most once a second however many pull. An unknown name is a 404 of one
line; `where --json` also carries `landedSeries` (cards landed per 10-minute bucket over 24 hours, split between friends and fleet). The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md), the dashboard.

### A provider out of funds

The owner, 2026-10-03: "provider out of funds should never be a mystery failure." `run` reads
each provider's balance every 10 minutes through the seat's key in its environment
(`OPENROUTER_API_KEY` for openrouter; opencode publishes no balance and reads `unknown`) and
prints a `BALANCE` line. A provider out of credit by a take it refused is rested until a
payment is seen (a balance read higher than the read before it, or than the balance at the
refusal) or `funded`: a balance over zero that is not higher never ends it. One out of credit by
a balance at zero is rested until a balance over zero; one low on funds, a balance not over an
hour of its spend, until the balance is over it. One judgment of the provider says which (`a
payment is the owner's`). `where --json` carries the `providers` table (balance, spend an hour,
state) and `routes` each route's `balance=`. When every provider is out of credit, the tick
stops the machine (`machine: STOPPED (every provider is out of credit)`) and `start` is refused
until one is paid; a provider low on funds never stops it. `funded <provider> --reason <text>`
says one was paid. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md), "A provider out of funds".

### Answered by rule

The run loop's tick answers the mechanical judgments itself, by rule, and records each as
`answered by rule <name>` on the log and on the card (`rule_answer`): work came back
failed is redealt on the next route of its tier, and the second failure on a tier goes a
tier up (flash, pro, heavy, then a friend's card); a card at its bound goes a tier up; a
work card past its deadline is waited 30 minutes once when it made progress in the last 10,
else returned and dealt again; a stream stopped on a conflict in a file no ledger owns has
the card returned, the stream resumed and the card redone on the current tip; the same
finding twice marks the card a brief defect and leaves it to you; and land gates a red base
again after 2 and 5 minutes before the third failure stops the stream with the error. A
reader's finding stays yours. `nova-sprint rules` prints what the rules would answer now and
Xoff, and nova-config's sprint row turns single ones off: `nova-config sprint set
--answer_rules_off late,conflict`, then `nova-config apply`. The contract is
[SPEC-SPRINT.md section 8](SPEC-SPRINT.md#answered-by-rule).

### Promoting the sprint branch into dev

`nova-sprint promote --once --branch <sprint branch> --repo-dir <clone>` carries one
promotion from the cut to the recorded merge with no hand steps. It fetches origin and cuts
`promo/<date>-<n>` from `origin/<sprint branch>`, never the clone's local ref, and refuses a
cut that is not ahead of `origin/dev`. It merges `origin/dev` into the cut without a checkout.
If that merge conflicts, it raises one judgment naming the files, `JUDGMENT promote conflict ... files=<a,b>`, and stops; nothing is cut or pushed, and the tool resolves nothing. A clean
cut is gated (`--check`), pushed, and its pull request opened. The verb waits on the pull
request's checks, queues it once they pass, and watches the queue. When the queue merges it,
the verb records `promoted --sha <merge>` in the store. A failed check or merge-group run
raises one judgment naming the check, `JUDGMENT merge-group failed ... check=<name>`, with
the failing log's tail. A pull request closed without a merge clears the promotion in
flight with one judgment naming it, `JUDGMENT closed-pr ... pr=<n>`, and the next pass cuts
afresh. The verb claims the promotion cleared only after every in-flight key is gone: a
cleanup that fails is a refusal naming the keys that remain. Every step prints a line as it goes, and every wait names what it waits on
(`PROMOTE WAIT ... checks pending: <names>`), looking again every `--poll` (default 1m).
Without `--once` the verb repeats every `--every`. `--dry-run` prints the cut it would make,
or the promotion in flight, and writes, enqueues and records nothing. The contract is
[SPEC-SPRINT.md section 11](SPEC-SPRINT.md), promote.

### The fleet is idle

When the fleet works under half its width for 5 minutes while cards wait, the run loop's
tick pushes you one note, `the fleet is idle`: `fleet 4/68: 311 behind 21 drop-blocked
judgments (oldest 1h50m); 89 behind md-secrets (a card reached its bound, 40m)`, every
waiting card traced to the root of its chain and the roots named by the cards behind
them; once an episode, and `the fleet is working again` when it recovers. `run
--idle-alarm=false` turns it off. The contract is [SPEC-SPRINT.md section 14](SPEC-SPRINT.md#the-fleet-is-idle).

### Answering the routine judgments

`nova-sprint answer` answers the routine judgments (a reader found it
broken, work came back failed, blocked on something dropped, stalled, a conflict,
past its deadline, cannot ask, ready to accept for a held primary, a card at its bound) by
nova-decide's judgment decision, card by card: it applies the verb chosen when
its probability is at or above `decide_judgment_bar` (nova-config's sprint row,
empty by default: with no bar it applies nothing, records every decision and lists
what a bar would apply; `--bar` gives one for a run; 0.8 is a starting point measured on 100 of the coordinator's own judgments,
not an independent calibration), by the line the inbox prints for that card,
and lists the rest for you: every drop, everything under the bar, and a provider
refusal for want of payment, which it never asks about. It prints one table, a
row a card, and records every decision (`--record`, default
`~/nova-sprint/decide/judgment.jsonl`, its directory made 0700) with its outcome once the card lands, is dropped
or comes back. Each verb it applies carries the decision's op id (`--op
decide.<decision id>`), recorded as `applying` before the verb runs and `applied`
or `refused` after, so a pass stopped between the two is finished by the next
through the same op and nothing is applied twice. One ask may take `--timeout`
(60s by default); an ask past it, or one that fails, is that card's `failed` row,
nothing is applied for it, and the pass exits 1. `--dry-run` applies and records
nothing; `--every 60s` runs it as the seat's loop until the machine is STOPPED. Jev's key comes from `JEV_API_KEY`:
`nova-secrets exec --only JEV_API_KEY -- nova-sprint answer`. The
contract is [SPEC-SPRINT.md section 8](SPEC-SPRINT.md#answered-by-nova-decide)
and [SPEC-NOVA-DECIDE.md section 13](SPEC-NOVA-DECIDE.md#13-the-judgment-decision).

`add` under `JEV_API_KEY` (`nova-secrets exec --only JEV_API_KEY -- nova-sprint add
...`) asks nova-decide's brief decision of every card it names with a brief after its
own checks and before it writes, one deadline for the batch: one `BRIEF card=<id>
op=<card>@brief-<hex> p_converges= minutes= failed= uncalibrated=true recorded=` line
per card, recorded in `~/nova-sprint/decide/brief.jsonl` (or `--decide-record <file>`),
the op stored on the card for land and drop to attach its end. The decision is
uncalibrated: nova-config's `sprint` row `decide_brief_bar` stays empty, which reports
only, until the brief record's own outcomes support a bar
([SPEC-NOVA-DECIDE.md](SPEC-NOVA-DECIDE.md) section 14).

`add` holds every card brief (one with a `PATHS:` line) to the brief checks at its base
before it writes: the repository `REPO:` names, in the lander's clone, at the tip of `BASE:`,
fetched once a call, read with git and no go command. The tokens are `paths-at-base`,
`donewhen-test-name`, `paths-cover-named`, `paths-cover-test`, `paths-cover-testdata`,
`paths-cover-ledgers`, `paths-cover-docs`, `base-is-live`, `tier-set`, `tla-is-frontier` and
`who-serves-tier`; each finding is a `LINT DRIFT card=<id> check=<token> line=<n>: <excerpt>
remedy=<remedy>` line and each corrected header line a `LINT FIX card=<id> <line>` line (the
`PATHS:`, `NEW:` or `SHARED:` line, or line 1, with every addition applied), then one refusal,
exit 2, nothing written. A base that cannot be read refuses with `MISSING: <what>`; a brief
naming no `REPO:` or no `BASE:` is held only to the checks that need no tree
([SPEC-SPRINT.md](SPEC-SPRINT.md) section 11, the brief checks).

### install-canary-shadow-tick-r.w1: the shadow tick before a server swap

`nova-sprint tick --shadow` plans one tick on the store and applies nothing: the store is
opened read-only, every write a refusal, and each part's plan is printed (`SHADOW PLAN
<table>/<part> size= due=`, then `SHADOW TICK OK epoch= state= parts= size= took= wrote=nothing`;
`--json` prints the plan as one line). `nova-sprint server switch <binary>` runs `<binary> tick
--shadow --json` against the store first and refuses the swap, exit 1 with nothing changed and
the old server running, when the shadow exits non-zero, panics, misses `--tick-deadline`
(default 10s) or prints no plan; on a pass it switches and keeps the shadow's plan size and time
at `<target>.shadow.json`, beside the switch record. The contract is
[SPEC-SPRINT.md](SPEC-SPRINT.md) section 14, "install-canary-shadow-tick-r.w1".

### Adopting a build: nova-sprint live and nova-sprint adopt

`nova-sprint live [--bin-dir <dir>] [--agents-dir <dir>] [--launchctl <path>] [--dashboard <link>]... [--json]`
prints the manifest of this host and changes nothing: `LIVE SERVER binary= inode= version=
revision=`, `LIVE LIBRARY state= loaded= want= match=` (the installed nova-redis's `fn check`
against `--redis`, logged in as `NOVA_SPRINT_REDIS_USER`), one `LIVE DASHBOARD link= target=
current=` per `--dashboard`, and one line per `com.nova.*` launchd agent of `--agents-dir` (else
`NOVA_LAUNCH_AGENTS`, else `~/Library/LaunchAgents`): `LIVE SERVER` for `nova-sprint run`,
`LIVE FRIEND` for `nova-friend run` with its last beat and its lanes with a card in hand, else
`LIVE AGENT`, each with its pid and, for a nova tool, `loaded= stale= fresh= installed=
args_took= binary=` and the `why=` of a stale one; `--json` also lists the host's nova
processes, `holds` on this bin directory's nova-sprint and nova-swarm members. Exit 0 whatever it finds, 1 when the
installed nova-sprint or the agents directory cannot be read, 2 usage.

`nova-sprint adopt <version|path> --source <checkout> --inventory <file> --reason <text>
[--limit <host>] [--receipts <dir>] [--dry-run]` runs `<checkout>/fleet/tools.yml` for the
seat, as a window: the new build's checks first (its shadow tick on the store, its `nova-friend
install --dry-run` against every friend daemon's flags); then, when the build replaces a tool or
the library, the old server, member and every nova agent but the friends are booted out and ps
is waited on to show no old nova-sprint or member; the configuration store is migrated, the
library loaded and the tools installed; every agent the window stopped and every stale one is
bootstrapped and proved; the dashboard links point at the installed nova-sprint; and each stale
friend daemon is reinstalled after its lanes put their cards down. A refusal once the window
opened puts the tools of before back, loads and reads back their library and starts the stopped
agents on them before it is said. It prints
one `ADOPT step=<store|server|dashboard|friends> host= before= after=` line per step and `ADOPT
ADOPTED version= hosts= steps=`; with `--dry-run` (the play's `--check`) `ADOPT WOULD-ADOPT`, and
a machine with neither the candidate staged nor built prints `ADOPT step=seat host=<h>
after=<version> WOULD-ADOPT` in place of the steps. Exit 0 adopted (or, with `--dry-run`, said
what it would change); 1 refused, `ADOPT REFUSED step=<step>` said verbatim, when the play stops
or ends without a step's line (the steps before it are done; a refusal once the seat play's
window opened is said with what the rollback did and names those steps rolled back; the same
command again finishes it), or when `--source` holds no
`fleet/tools.yml`; 2 usage. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md) section 14,
"Adopting a build".

### Exit codes

| exit | meaning |
|---|---|
| 0 | done |
| 1 | failed or incomplete (including refused; `start` while every provider is out of credit) |
| 2 | usage, or a store that did not answer (`fleet sync --check`: there is drift) |
| 3 | unreadable config (`fleet sync`, `friend sync`, `friend clean`), missing friend rows (`friend sync`, `friend clean`), or a replaced binary (`run`, `dashboard`) |

### What it does not prove

A landed card records a completed flow: the worker reports done, two different
readers pass that head, the tick queues it for merging and tells the seat
("ready to merge", a notice; the coordinator's `accept` is for a primary the
tick holds), and the landing is reported. These are recorded judgments, not a
proof that the work is correct. A twin exercises that flow one command at a
time. It has no beats or ticks between commands, so it does not test fleet
timing, the `run` loop, `inbox --wait` or liveness. `finish` without `--head`,
then `merge`, records a landing without a push. The work table's cost column
is, per stream, the sum of its landed cards' total cost in US dollars — each
card's actual cost where one was priced, else its predicted one, `-` when
none was — so a total is a ledger of recorded spend, not a proof of it.

### release-check-acceptance-r-b.w3: the acceptance sentinel's six checks

`nova-sprint release check` runs the acceptance sentinel's six checks beside
`no-stuck-friend`, source: the coordinator's answer over the bus, 2026-10-06
12:50 ET. Each prints one `RELEASE CHECK <name> ok|fail <evidence>`
line, and the release refuses on any fail: `cards-settled` (every card of the
stream landed or dropped with a reason), `base-gate-green` (the unit and
functional classes and `./internal/docs` and `./internal/ci` green on the base
at the stream's last landing), `two-ok-reads` (every landed card has the ok
reads its tier needs at its final head), `prose-true` (`nova-check links` and
`nocode` clean on the stream's specs and help), `landings-promoted` (the
landings are in dev or a promotion carries them) and `no-open-judgment` (no
open judgment names the stream). One check alone: `release check --check cards-settled`; one stream's facts: `release check --streams 's1*'`. With no
stream named there is no acceptance to check, so each passes and says so. The
contract is [SPEC-RELEASE.md](SPEC-RELEASE.md) section 16, subsection
release-check-acceptance-r-b.w3.

### release-check-merge-queue-p90-b.w7: the merge queue's p90

`nova-sprint release check` also runs `merge-queue-p90`: over the last
`--window` (default 24 h) it takes the p90, by the nearest rank, of the time
each card spent in merging, read from the log's work-table moves into and out
of the `merging` column (a card still merging counts with its age now), and
fails above `--merge-p90` (default 30 m). The fail line prints the p90, the
number of cards and the oldest card still merging. One check alone:
`release check --check merge-queue-p90`; a shorter bar:
`release check --merge-p90 15m`; a week's window:
`release check --window 168h`. With no merge in the window it passes and says
n=0. The contract is [SPEC-RELEASE.md](SPEC-RELEASE.md) section 16, subsection
release-check-merge-queue-p90-b.w7.

## nova-sandbox

Runs one command under OS-enforced containment using `sandbox-exec` on macOS
or Landlock on supported Linux kernels. Windows has no implemented backend and
refuses to wrap a command. Run `nova-sandbox check` to inspect backend availability
on your machine before use.
The contract is [docs/SPEC-SANDBOX.md](SPEC-SANDBOX.md).

Three lists and no defaults. `--read <dir>` is readable and **not** writable, so
N workers share one copy of an input named once; `--read-noexec <dir>` is the
same grant **without execute**; `--write <dir>` is readable and writable and is
**required**, because a command with no writable directory is a misconfiguration
and not a tighter sandbox. Everything else on disk is denied, the credential file
included — which is the whole point: the key stays with the person who owns it,
and the wall is what says so.

**`--read` carries execute; `--read-noexec` is how you say it must not.**
Landlock's read subset is `EXECUTE|READ_FILE|READ_DIR` and the darwin profile
grants `process-exec*` globally, so under `--read` a program anywhere in the tree
RUNS. For a cache or a data tree this user can write to — a module cache, a
`node_modules`, a downloads directory — that is a way in, and `--read-noexec`
grants the reading and takes the execute back (on darwin as a last-wins
`deny process-exec*` after the global grant, on linux by dropping `fsExecute`
from the rule). A path named in both lists is a **refusal**, not a merge: one
asks for execute and the other takes it away. The `SANDBOX OK` and `POLICY OK`
lines count the two separately, `read=<n> read-noexec=<n>`.

**Every path is yours and none is guessed.** A `--read`, a `--read-noexec`, a
`--write`, a `--cwd` or a `--tmp` that does not exist is a refusal and is never
created, and `HOME`
must resolve **inside a `--write`** — the caller sets it — because almost every
tool derives a path from it and an inherited `HOME` is denied by the wall. That
is one flag on every line below, and leaving it off is the first thing a first
run gets wrong.

### First run

The `probe` and wrapped-command blocks below are multi-line shell commands; paste each whole block, not one line of it.

Ask the machine what it can enforce, then prove the wall before the first job:

```
$ nova-sandbox check
CHECK OK backend=sandbox-exec abi=- net=enforceable note=sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies; backend at /usr/bin/sandbox-exec
```

Invalid flags or unexpected arguments refuse with exit 2 naming the flag as typed:

```
$ nova-sandbox check --bogus
CHECK REFUSED reason=bad_flag: unknown flag --bogus; run: nova-sandbox help check
```

Unknown verbs refuse explicitly with exit 2 rather than falling into the bare wrap:

```
$ nova-sandbox bogus
SANDBOX REFUSED reason=unknown_verb: unknown verb "bogus"; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help
```

Prove the wall before the first job:

```
$ mkdir -p /path/to/pool/jobs/j1/home
$ HOME=/path/to/pool/jobs/j1/home \
  nova-sandbox probe --write /path/to/pool/jobs/j1 \
               --secret /path/to/.config/anthropic/env
PROBE STEP name=write_outside_control expect=allow got=allow path=/path/to/pool/jobs/.nova-sandbox-probe-31622
PROBE STEP name=write_outside expect=deny got=deny path=/path/to/pool/jobs/.nova-sandbox-probe-31622
PROBE STEP name=read_secret expect=deny got=deny path=/path/to/.config/anthropic/env
PROBE STEP name=write_inside expect=allow got=allow path=/path/to/pool/jobs/j1/.nova-sandbox-probe-inside
PROBE STEP name=read_root expect=allow got=allow path=/path/to/.local/bin/nova-sandbox
PROBE OK backend=sandbox-exec abi=- steps=5 passed=5 net=nopromise gpu=none
```

`probe` runs **five** checks under the real policy for this platform, not two: a
wall that denies the work as well as the secret is broken, and a two-check probe
would call it a pass. `--secret <path>` names the file the probe proves it
cannot read — the path is not the secret, and its contents are never read — and
it must be **outside** both lists, since a secret inside a named directory is a
misconfiguration rather than a failed check. The `HOME=` prefix is not
decoration: the check runs before the policy is built, so a probe run with
the dispatcher's own `HOME` is refused before it starts.

Then wrap the command. This example uses an empty repository initialized on
branch `main` at `/path/to/pool/jobs/j1/repo`; `! ` marks standard error:

```
$ HOME=/path/to/pool/jobs/j1/home \
  nova-sandbox --read /opt/homebrew --write /path/to/pool/jobs/j1 \
               -- /opt/homebrew/bin/git -C /path/to/pool/jobs/j1/repo status
! SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/path/to/pool/jobs/j1 cwdb64=L3BhdGgvdG8vcG9vbC9qb2JzL2ox ancestors=11 cmd=git gpu=none deletes=/path/to/pool/jobs/j1
On branch main

No commits yet

nothing to commit (create/copy files and use "git add" to track)
! SANDBOX DONE exit=0 cmd=git
```

`SANDBOX OK` says the wall is up and the command is starting; `SANDBOX DONE` is
the last line, after the command has ended, and its `exit=` is the command's own
status, which is the status the tool exits with. A refusal prints `SANDBOX
REFUSED` and no `DONE`. stdout is the command's alone.

What a first run gets wrong, and what each one wants:

- **No `HOME` inside a `--write`.** `PROBE REFUSED … HOME <dir> is outside every
  --write`. Give the job a data home of its own: `mkdir -p <jobdir>/home` and
  `HOME=<jobdir>/home`; the refusal ends with that command, `run: mkdir -p …
  && HOME=… nova-sandbox <the same arguments>`. A `--read` is not enough — the
  first config write dies there.
- **No `--write`, or no `--secret` on a probe.** Both are required and neither
  has a default. They are named **together**, in one refusal, so a first run is
  not sequenced into one run per mistake.
- **A toolchain outside the wall.** A command that runs outside the wall and
  dies inside it is missing a `--read`: a toolchain in a user directory is
  exactly a caller-supplied read-only root, so name it. Name a cache or a data
  tree with `--read-noexec` instead, and keep `--read` for what the job runs.
- **A `--cwd` outside every named path.** It denies `getcwd(3)`, and every git
  command dies there before it reads anything.
- **Expecting a network promise without asking for one.** Without `--net-deny`
  the tool makes no promise about the network and the line says
  `net=nopromise`; `--net-deny` is an **enforced** denial or a refusal, never a
  hope.

`nova-sandbox policy` prints what would be generated without running anything,
which is the fastest way to see the wall a set of flags actually makes.
`check`, `policy` and `probe` take `--json` for one JSON object on stdout, and
`<verb> -h` lists each verb's flags and its own exit codes.

### A disposable place, on darwin

The flags above give a command a **wall** around a directory you own and keep.
`nova-sandbox run` gives it a **place** instead, and then takes the place away:

```
$ nova-sandbox run --name j1 --size 8g --timeout 30m --read /opt/homebrew -- /bin/sh -c 'echo hi > out'
SANDBOX STEP name=create state=start
SANDBOX STEP name=create state=done ms=2395
SANDBOX OK backend=sandbox-exec abi=- read=1 write=1 net=nopromise cwd=/Volumes/nova-j1/work ...
SANDBOX STEP name=delete state=start
SANDBOX STEP name=delete state=done ms=1426
SANDBOX DONE name=j1 exit=0 wall=9.500 freed=32768
```

An APFS volume of its own in the boot container, quota'd by `--size`, is the
command's only writable directory — `TMPDIR`, `HOME` and the working directory
are all on it — and on exit, whether that exit is clean, an error, a signal or
`--timeout`, the whole process group is killed and the volume is unmounted and
deleted. **There is no cleanup step**, because nothing of the run is left on the
boot volume to clean. It needs no `sudo`. A delete that fails prints
`SANDBOX LEAK` with the one `diskutil` command that removes it and exits 3,
never silently.

**A commit leaves as a bundle, through `--out`.** Everything on the volume is
deleted, so a card that committed something needs one writable path out.
`--out <dir>` opens it: after the command exits and before the volume is deleted,
the named artifacts are copied to `<dir>/<name>/` and one line says what left.

```
$ nova-sandbox run --name j1 --size 8g --out ./handoff \
               -- /bin/sh -c 'cd repo && git bundle create ../repo.bundle HEAD && echo DONE > ../RESULT.md'
SANDBOX STEP name=out state=start
SANDBOX STEP name=out state=done ms=3
SANDBOX OUT name=j1 files=2 bytes=21174
SANDBOX DONE name=j1 exit=0 wall=11.220 freed=1048576
```

The default set is `RESULT.md`, `usage.tsv` and `repo.bundle`, each taken **if
present**; `--artifact <relpath>` names another set, repeatable, relative to the
card's working directory, and an artifact you name and did not write is a
refusal. Every path is resolved inside the volume — an absolute path, a `..` or
a symlink is refused — and the whole set is measured before a byte is written
and refused over `--out-max-bytes` (default `64m`). `git bundle create
repo.bundle <branch>` as the card's last step is the documented way a commit
leaves; the other side reads it with `git fetch ./repo.bundle <branch>`. A
handoff that fails after a command that exited 0 makes the run
`SANDBOX REFUSED reason=out_failed`, exit 125, because a zero would say the
artifacts are there.

On linux `run` refuses with one remedy line: a card is already disposable there
— it runs inside its image — so name the image root as `--write` on the bare
form instead.

### The same place, on windows

`run` on windows is **the same verb**: the same five steps, the same receipt, the
same `SANDBOX LEAK` and the same exit codes. What differs is the place and a
handful of flags ([SPEC-SANDBOX.md](SPEC-SANDBOX.md), rules W1–W12):

```
$ nova-sandbox run --name j1 --scratch C:\nova --timeout 30m --memory 4g --cpu 50 -- cmd.exe /c "go build ./..."
```

- **The place is a Job Object plus `<scratch>\nova-<n>`, and the two are one
  unit.** The job is what the darwin side gets from a process group: closing the
  tool's last handle terminates the whole tree, including a grandchild a harness
  abandoned, because the job carries `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` and
  never permits breakaway. The child is put in the job **at creation**, with
  `PROC_THREAD_ATTRIBUTE_JOB_LIST` on the same attribute list as the wall, so
  there is no window in which it is alive and outside one.
- **`--scratch` is required** and must be an existing absolute path. There is no
  default: not the TEMP variable, not the user profile.
- **`--size` is refused**, with `reason=size_unenforceable`. NTFS quotas are per
  user per volume, a directory quota is a server role and a per-run quota is a
  VHDX needing administrator rights. A ceiling the tool only measures is not a
  ceiling, so this is a refusal and not a note — the same rule `--net-deny`
  already follows. Both remedies are on the line.
- **`--memory` and `--cpu` are the job's caps**, and they are the one place this
  tool limits memory or CPU at all. Both are accepted and ignored on darwin and
  linux, so one caller writes one argv for three platforms.
- **`--place wsb`** is Windows Sandbox: full disposability, because the guest's
  disk is discarded when the window closes. It is Pro and Enterprise only, it is
  **one instance per machine** — so it is the review place and never the swarm's
  — and it requires `--timeout`, because the guest's status comes back through a
  file in the mapped folder or not at all.
- **WSL is never the answer**, not as the wall, not as the place and not as a
  fallback: containment that only holds inside WSL is containment on a machine
  the card was not sent to.

**This is not measured on a Windows machine.** The estate has none. The
verb's sequence is proven against a fake on every host, the
binary cross-compiles and vets for `GOOS=windows`, and until the **wall**
(AppContainer) is built the verb refuses there with `reason=no_sandbox` naming
the half that is missing — a place without a wall is hygiene, not containment.

**A card that builds Go wants `--go`**, which adds the toolchain's own two roots
to the read set — `GOROOT` and `GOMODCACHE`, as `go env` reports them — so that
neither has to be named by hand in every argv. `nova-sandbox run --help` prints
the verb's own usage.

**Two runs at once are safe, and the tool is what makes them so.** Creating the
volume is the one step that cannot be shared: two `diskutil apfs addVolume`
running at the same time leave the new volume's root owned by `root:wheel`
instead of you, it never settles, and the run dies at `mkdir` with `permission
denied` before its card starts. `run`
serializes creation across processes with a lock file under your own cache
directory, and checks the new root is yours and writable before it hands it to
anything. Nothing else is serialized: the runs themselves overlap freely.

**A `--timeout` that passes prints `SANDBOX TIMEOUT after=<d>`** and asks the
operating system nothing. A deadline is not a path the wall refused, so the
`SANDBOX DENIED` query below is skipped for it.

### Clearing up after a `SIGKILL`

`run` deletes its volume on every path out it can take. `SIGKILL` is not one of
them — the tool is gone before it can delete anything, so the volume stays
mounted and the command's own children are reparented to PID 1 **still holding
it open**, which is why a later `diskutil apfs deleteVolume` will not clear it
either. Nothing survives to print `SANDBOX LEAK`, and `check` does not look:
`check` asks what the backend can enforce, not what the machine is still
holding.

`nova-sandbox reap` is the verb that looks. It lists every `nova-*` volume —
that prefix is the whole of its authority — kills whatever holds each one open
(`SIGTERM`, then `SIGKILL` after a short grace) and deletes it, one line each:

```
$ nova-sandbox reap
SANDBOX REAP volume=nova-j1 procs=1 deleted=yes
SANDBOX REAP OK volumes=1

$ nova-sandbox reap --dry-run
SANDBOX REAP OK volumes=0
```

It **never takes a volume from a live run**: `run` leaves a
`.nova-sandbox-owner` marker at its volume root carrying its pid and that
process's start time — both, because a pid is a number the OS hands out again —
and a volume whose marker names a running tool is reported and left alone. Exit
is **0** when the machine is clean and **3** when anything remained, including
every `--dry-run` that found something, which is what makes `reap --dry-run` a
gate a card can end on. `--dry-run` prints and touches nothing.

**When a contained command exits non-zero**, the tool asks the operating system
what it refused and prints one line per path, with the flag that would have
allowed it:

```
SANDBOX DENIED path=/opt op=read remedy="--read /opt"
```

macOS 26 does not report a `sandbox-exec -p` profile's violations to the unified
log at all (measured; `(with report)` and `(trace ...)` are both unavailable), so
on this OS the line is usually silent and a `SANDBOX NOTE` naming the size of the
allowed set is printed instead. [SPEC-SANDBOX.md](SPEC-SANDBOX.md) has the whole
measurement.

### worktree

Materialises one pull request's exact head in an isolated scratch tree of its own. It is not a wrapper and builds no wall: it uses SPEC.md's 0/1/2 grammar (0 the verb ran, 2 could not run), reads the repository through git on `PATH`, and reads the pull request through the forge client (`gh`).

```
$ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --pr 123
WORKTREE OK path=/path/to/workdir/scratch/1f450ab70c635e66f675ff8a4e395760 head=0123456789abcdef0123456789abcdef01234567
```

A subsequent invocation on the same clean head reuses the existing tree rather than rebuilding it:

```
$ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --pr 123
WORKTREE OK path=/path/to/workdir/scratch/1f450ab70c635e66f675ff8a4e395760 head=0123456789abcdef0123456789abcdef01234567
```

`--prune` walks `<scratch>/*.pr`, inspects process usage, and deletes idle trees older than 24 hours:

```
$ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --prune
WORKTREE OK removed=0 kept=1
```

## nova-tokens

Token spend, folded from declared sources into **one file per day**, keyed exactly by `(day, model, repo)`, with the five token types kept apart — and those day files summed into a month. It reads sources. It never estimates, never fills a gap, and never removes a file. The contract is [docs/SPEC-TOKENS.md](SPEC-TOKENS.md).

The core accounting verbs are `fold`, `report`, `sum`, `check` and `sources` — `nova-tokens help` lists all nine verbs. `fold` reads every declared source and writes the days it could compute. `report` is for a friend on another machine: it folds that machine's own sources for one day and prints, on standard output, exactly the body of a tokens note, so nobody types a number. `sum --out <dir> --month <YYYY-MM>` adds day files into a month and asserts nothing. `check` is the gate. `sources` shows what a fold would count before it writes.

```sh
nova-tokens check --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--max <n>]
```

`check --out <dir>` counts what it does not name, so that it can go green on a real directory: a calendar day between the first and the last with no file is `gap=<n>`, and a `*.md`, a `*.log` or a `pre-*` archive directory beside the day files is `notes=<n>`. A gap becomes `CHECK MISSING` only when something says there was spend on it — `--strict` names every gap (and every non-day entry, the whole reading), and `--no-spend <file>`, one `YYYY-MM-DD` per line, names the gaps your list does not account for. The two flags are two answers to one question and giving both is exit 2. `--through <YYYY-MM-DD>` asserts that the ledger is current through the specified day; when the newest folded day under `--out` is older than the given day (or if `--out` has no folded days), `check` prints `CHECK FAILED stale last=<last> through=<day>` on standard error, marks the run failed, and exits 1. `sources --unattributed [--max <n>]` prints the path stems that were seen and matched no rule, heaviest first, which is what the `other=<pct>%` share on a `TOKENS DAY` line is made of and the one evidence for improving the `--repos` file; `SOURCES OK` then carries `unattributed=<n>`, and `-` when the flag was not given. `profiles --swarm-root <dir>` walks a swarm root's card usage files and prints, per model, the card count, the median `tokens_out` and the budget overshoots, writing nothing. `version` prints the build identity. A harness that records nothing a tool can read (Antigravity, Grok, Codex) is counted provider-side, never apportioned: `--provider <kind>:<label>=<file>`, the kind one of `google`, `openai`, `xai`. The `xai` parser reads both the comma-separated export and the `grok usage` JSON (a `sessionId` and a `turns` array), folding each turn's five token counts and its `costUsdTicks` — an integer count of micro-dollar ticks — into the model's `usd=` on the day's `TOKENS AVG` lines. One `--provider xai:<label>=<file>` names one file. A missing path is `TOKENS UNREADABLE` and is not a search of a session store; a directory is not walked. `session --claude-session <jsonl>` sums one Claude Code window into one `SESSION` line and, with `--out`, folds it as one row per model the transcript names; `--role <name>` books each row as `<model>/<role>` with the role in the repo cell too — with no role the model stands alone and the repo cell is the fixed word `unattributed` — and `--weights <in,cw,cr,out>` sets the four ratios the weighted fresh-input equivalent is built from, defaulting to `1,1.25,0.1,5`, a comparison and not a price: the ratios of one vendor's published list prices; set your own.

### First run

The transcript lives in [TESTS.md](TESTS.md), where a test executes it against `cmd/nova-tokens/testdata/example-bench` on every run. For a first try from the binary alone, `nova-tokens help` includes one setup command that writes a small transcript and rules file into the current directory, followed by commands to fold, check and sum it. Every path is a flag — there is no default output directory, no default transcript directory, no default bus and no default rules file. No verb reads the environment for a path or a setting, with two exceptions the help names: `--opencode` runs `sqlite3` from `$PATH`, and the two Redis verbs (`ledger`, `report --redis`) read the store's ACL user and password variables. Every verb but `version` takes `--json`, and `fold`, `report --note`, `ledger` and `session --out` take `--dry-run`. `report -h` labels its local note-body mode and Redis month-summary mode separately.

What a first run gets wrong, and what each one wants:

- **No `--repos`.** There is no built-in list of repos, because the two the prototype carried disagreed about three of them. It wants a file of `<name><TAB><regexp>` lines in priority order; the `unknown=` and `other=` shares on every `TOKENS DAY` line are how you see whether yours is good enough.
- **Expecting exit 0 with an unreadable file.** A declared source is a claim that the report covers it, so an unreadable one is one `TOKENS UNREADABLE` line, one in `unreadable=`, and exit 1 — and the day files still land. `written=true` is about the files; the exit code is about the claim.
- **Reading a `-` as a zero.** A dash is "this source did not report that type" and a zero is a measurement. `sum` counts the dashes per column beside the totals, and nothing here folds one type into another.
- **Sending a second tokens note for a day.** Two notes in one lane for one day are `TOKENS CONFLICT` and fold nothing, because no winner can be read off a clock, a filename or a git history. A correction names what it corrects: `supersedes=<id>[,<id>…]` in the subject, which `report --supersedes` writes for you.
- **Reusing one label across two kinds.** A label is unique across the whole run, not per flag: `--claude bench=… --opencode bench=…` is `TOKENS REFUSED … the label bench is used twice`, exit 2, before anything is read. Two sources with one label would make the `sources` column a lie. A `--provider` is the one flag whose label carries its parser too — `--provider google:<label>=<export>` — so two exports from one provider are `google:<label>` and `google:<other>`.
- **Declaring one harness twice.** **One harness is one `--claude`.** This fold does not de-duplicate across sources, by design (SPEC-TOKENS, *what it deliberately does not do*), so two declared directories holding the same transcripts count every message twice and the day file, `check` and `sum` are all green about it. Measured on this bench: `~/.claude/projects/<session>/subagents/agent-*.jsonl` and `/private/tmp/claude-501/*/tasks/*.output` were the same 10,281 messages for one day, and the doubled fold said `written=true`. A fold that sees two sources feed one message id now says so on its `TOKENS NOTE` line, naming both labels and the count — it is a warning, not a correction: the numbers are still doubled and the remedy is to drop one flag.
- **Pointing `--claude` at a directory with a scratch tree under it.** `--claude` walks every `*.jsonl` and `*.output` under the directory **recursively**, and prunes nothing: a session scratchpad, a git clone or a build tree under it is walked too. Measured: a window-only fold of 1,278 files and 739 MB took **10.4s**; adding a directory of 33 session scratchpads under `/private/tmp` took **531.7s**, 331s of it in the kernel, to find 2,612 transcripts. Nothing is skipped silently, because a silent prune is a number nobody can account for — so name the transcript directory itself, and expect the walk to cost what the tree costs.
- **`--scratch` without `--opencode`, or the other way round.** The OpenCode database is copied into `--scratch` and read there with `sqlite3 -readonly`, which is this tool's one subprocess; a scratch directory with nothing to put in it is a flag that does nothing, and both mistakes are refused with the sentence saying so.

There is **no `quickstart` verb**, and that is deliberate. Every verb here needs a path this tool must not invent — an output directory, a rules file, at least one source — so a one-word first run would have to write state nobody asked for, in a directory nobody named. `nova-tokens help` carries seven example lines a stranger can paste instead — six under its first `example:` and one under the `session` example, and `sources` is the one verb that only looks.

**The token ledger on Redis**. `ledger` indexes folded day files
into the fleet Redis, one hash per day, and `report --redis` is the month as one GROUP BY
over those hashes -- every one of the five types apart, a dash where no row reported a type,
and equal to the folded day TSVs to the token. The day files stay the record.

```
nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD -- nova-tokens ledger --out ./days --month 2026-09 --redis <host:port> --user bench --password-env NOVA_REDIS_BENCH_PASSWORD
nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD -- nova-tokens report --redis <host:port> --month 2026-09 --by tuple --user bench --password-env NOVA_REDIS_BENCH_PASSWORD
```

`tokens:ledger:<YYYY-MM-DD>` is a hash: each field is `["<card>","<model>","<repo>"]`, each
value `{"provider","tokens","rough","sources"}` with `tokens` the five types in order and
`null` for a type no source reported. Re-indexing a day replaces its hash in one MULTI/EXEC;
a month reads its calendar days' keys in one pipelined round trip. The fleet Redis has its
default user off, so both verbs dial as an ACL user:
`--user <name>`, else `NOVA_SPRINT_REDIS_USER`. The password is never a flag: it is the
variable `--password-env` names, else for a user `NOVA_SPRINT_REDIS_PASSWORD_ENV`'s, else
`NOVA_REDIS_BENCH_PASSWORD`; a user whose variable is empty is refused before any dial. With
no user, no variable is read unless `--password-env` names it.

## nova-update

`nova-update` checks declared versions and applies one chosen update: bounded reads, explicit UNKNOWN results, no automatic installation. The contract is [docs/SPEC-UPDATE.md](SPEC-UPDATE.md).

### First run

```sh
nova-update example --out versions.tsv
nova-update report --file versions.tsv
```

Run this with the binary alone, in any directory: `example` writes a one-tool
manifest (Go, read with `go version` on both sides) and names the next command; the
same file again is left unchanged, and a file holding anything else is never
overwritten. The executable transcript is in [TESTS.md](TESTS.md#nova-update).
The report reads only installed identities. UNKNOWN means a partial inventory; it
never means zero or current. Replace the example with your own six-column manifest
for your bench; it and any snapshot path belong to the caller.
A `tool` row whose `installed` column is just the executable is asked `version`,
then `--version`, then bare, all inside one `--timeout` — so our own tools, which
answer a bare invocation with a usage refusal, are read rather than reported
UNKNOWN. A row holding a whole argv (`go version`) is run as written.

Use `nova-update help` for filters, optional draft/delivery and limits. A plain report
needs no bus. Updates require an explicit `nova-update apply --file ... name`;
models are listed for the owner to evaluate and pull themselves. No timer is installed.
Name `--snapshot` to quiet repeats: a report unchanged since it was confirmed sent to
the same recipients sends nothing. `--send` delivers one note through nova-bus on the Redis bus:
`nova-bus send --as <sender> --to <recipients> --subject <one line> --stdin`.
nova-bus reads the store from `NOVA_BUS_REDIS`. A confirmed `SEND OK id=<id>`
line is what the receipt records. `watch --adopt` with `--as` and `--to`
posts the adoption receipt the same way. Version statuses should go to your chosen
integrator, with optional Cc; participation and updates remain voluntary.

`status` is `check` with every entry's line shown, the current ones too, exit 0 when
every entry is equal and 1 when any differs; it writes nothing. `apply --dry-run`
prints the plan and writes nothing: `APPLY OK ... dry_run=true from=<installed>
to=<target>`, the entry's line against the target, and `APPLY PLAN` with the command
the real run would start. Every verb but `watch` and `release` takes `--json`: the same
result as one JSON object on stdout, refusals included. The first line of every result
is the verb, its status word (`OK`, `FAIL`, `REFUSED`) and the run's counts.

```sh
nova-update status --file versions.tsv
nova-update apply --file versions.tsv go --dry-run
```

First-run refusals name what is needed: `--file` wants the six-column TSV header
and explicit argv; paths or arguments containing spaces belong in a wrapper script.
`--draft` needs `--as` and `--to` and sends nothing. `--send` delivers through
nova-bus on the Redis bus; nova-bus reads the store from `NOVA_BUS_REDIS`.
`watch` posts its receipt when `--as` and `--to` are set, by the same
`nova-bus send --as --to --subject --stdin`. A busy snapshot wants the current writer to finish or a larger
`--budget`; never remove a lock file to break a live lock.

### The release verb

`nova-update release` is the last mile: a green commit becomes a version, a set of stamped binaries,
and the same binaries answering for themselves on every bench in the fleet. Six verbs, each of which
can refuse. The gates are in [docs/SPEC-RELEASE.md](SPEC-RELEASE.md) and the verbs in
[docs/SPEC-UPDATE.md](SPEC-UPDATE.md).

```sh
nova-update release cut --repo mas-bandwidth/nova-tools --from main --version v0.17.0 --changelog ./CHANGELOG.md --sums ./release/v0.17.0/linux-amd64/SHA256SUMS
```

`cut` refuses a commit whose checks are not green, refuses a version that is already a tag, writes the
changelog section and creates the **annotated** tag carrying `sums=<sha256 of SHA256SUMS>`. `build`
writes one `SHA256SUMS` per platform, under `<out>/<version>/<goos-goarch>/`, and `--sums` takes one
of them: the tag and the changelog section carry that platform's digest, and `adopt --repo` verifies
that platform only. Every other platform the release built is adopted with `--expect-sums-from
<out>/<version>/<goos-goarch>/SUMS.digest` on the host that built it, or `--expect-sums <sha256>`
from the `sums=` field of its `RELEASE BUILT` line. It also
classifies the range since the previous tag against the sensitive path list and refuses until
`--security-read <note id|url>` names the recorded read. A compare the forge could only answer in part —
300 files, its ceiling — is a different refusal, `reason=compare-truncated`, and a read does not get
past it: classify from a complete local list instead, with `--local-diff <checkout>` to produce one
(`git diff --name-only <previous>...<head>`) and `--paths-from <file>` to write it or read it back.
`--dry-run` decides and prints and writes nothing.

**Before any of that, `cut` and `build` run the dogfood gate.** It is `nova-check dogfood gate --cli
<reference> --receipts <dir>` in process: a verb somebody ran that did not do what they needed, and
that nobody has run since and said it did, is an **open edge**, and an open edge refuses —
`RELEASE CUT REFUSED reason=dogfood-gate open=<n> remedy="fix the open edges or --no-dogfood-gate
--reason <why>"`. `--cli` defaults to `docs/CLI.md` beside the checkout the verb was already given
(`--changelog` for `cut`, `--source` for `build`); `--receipts` defaults to the `dogfood` receipts directory under your home
when that directory exists, and a run with neither says `dogfood-gate=skipped` rather than passing
quietly. The gate judges the **shipped set** only: the tools under the checkout's `cmd/`. A receipt
naming any other tool is set aside and counted on `RELEASE CUT NOTE dogfood-gate shipped=<n>
outside=<n> cmd=<dir>`. `--no-dogfood-gate` needs `--reason <why>`, and the reason is printed, put on the release
line as `dogfood=waived`, and written into the CHANGELOG section as `Dogfood gate waived: <why>`.
Every release line carries `dogfood=ok|waived|skipped`. A tool is done when it is
tested, dogfooded by a non-author on real work, and the feedback is applied — see
[SPEC-RELEASE.md](SPEC-RELEASE.md) lesson 12.

```sh
nova-update release build --version v0.17.0 --out ./release --source . --platform linux-amd64 --platform darwin-arm64,darwin-amd64
```

`build` compiles every `cmd/nova-*` for every platform named — `--platform` is repeatable **and**
comma-separated — writes and verifies a `SHA256SUMS` per platform, and writes that file's own sha256
to `SUMS.digest` beside it. An unsupported `goos-goarch` refuses before the first compile, so no
half-made directory is left behind. One `RELEASE BUILT` line per platform, then one
`RELEASE BUILD OK … platforms=<a,b,c> sums=<sha256,…> pruned=<n> prune-failed=<n>`.

`--incremental` compiles only the tools whose packages, or the packages they import, changed since
the newest clean build recorded under `--out` (`<out>/<version>/<goos-goarch>.build`, written by every
build beside its platform directory), and copies every other tool from that build, verified; it says
`RELEASE BUILD INCREMENTAL … base=<v> rebuilt=<tools> reused=<n>`, or `RELEASE BUILD WHOLE …
reason=<why>` when it cannot trust a base. `--gate report --reason <why>` runs the dogfood gate,
prints its open edges and builds (`dogfood=report`); `cut` has no such flag. See
[SPEC-RELEASE.md](SPEC-RELEASE.md) rule 13.

Retention, after a successful `build` (in `--out`) and a successful `install` (in `--from`): a
directory directly under that root whose name is a version (`release.ValidVersion`) is removed unless
it is the version just built or installed, the version the machine had installed before it (`build`:
the running nova-update's stamp; `install`: every version the bin directory's binaries answered
before the install), or one of the 3 newest of the rest by modification time
(`release.KeepBesides`). Anything else in the root is left alone, and a removal that fails is said
on stderr and counted in `prune-failed=`; it never fails the build or the install.

```sh
nova-update release install --from ./release --version v0.17.0 --bin ~/.local/bin --retire ~/go/bin
```

`install` verifies the checksums, puts the binaries in place by rename, skips what is already current (answering the version, or
holding the same bytes)
and clears this release's own files out of `--retire`. Run it **on the coordinator before adopting**:
`adopt` fans out with the nova-update this host is holding, and a coordinator behind the release
refuses and says so.

```sh
nova-update release adopt --version v0.17.0 --machines ./machines.tsv --ssh ssh --from bench1:/home/user/nova-bench/release --stage ./stage --expect-sums-from ./release/v0.17.0/linux-amd64/SUMS.digest --bin '~/.local/bin' --dest '~/nova-release' --platform linux-amd64
```

`adopt` runs from the host that has ssh to every machine and fans out from there. A `--from host:dir`
release is fetched once into `--stage` and checked against a digest that did **not** travel with the
bits: `--repo <owner/name>` reads it off the annotated tag, `--expect-sums-from <SUMS.digest>` reads
it out of this host's own build (which is how a release with no tag is adopted at all), or
`--expect-sums <sha256>` names it outright. The digest file must be local. `--machines` is one machine
per line with optional TAB-separated `bin` and `dest` overrides; `--dry-run` asks every machine what
it holds and installs nothing. The stream lands in `<version>.partial/` and is renamed into place
only after the bench verifies every artifact against `SHA256SUMS`; "already holds" is that verified
count (`22/22`), never an existence check.

```sh
nova-update release build --version v0.17.0 --out ./release --source . --platform windows-amd64
nova-update release adopt --version v0.17.0 --machines ./machines.tsv --ssh ssh --from ./release --bin 'C:\Users\nova\.local\bin' --dest 'C:\Users\nova\nova-release' --platform windows-amd64
```

A **windows** bench is a target like any other. The build names every artifact for it — a
`windows-amd64` release is a directory of `.exe` files and a `SHA256SUMS` that lists them — and the
adopt sends and runs `nova-update.exe` there. `--bin`, `--dest`, `--retire` and the `--machines`
columns take the drive-absolute form as well (`C:\Users\nova\.local\bin`, which is what
[BENCH-WINDOWS.md](BENCH-WINDOWS.md) puts in that bench's runner `.path`); every backslash is folded
to a forward slash before a command is composed, because the far side's ssh shell is Git Bash and a
backslash there is an escape. The drive form is refused for a non-windows target, and a drive-relative
(`C:Users\nova`) or UNC (`\\server\share`) path is refused everywhere. A cross-built windows artifact
cannot be run by the host that built it, so the build claims nothing about having done so; see
[SPEC-RELEASE.md](SPEC-RELEASE.md) §11.

```sh
nova-update release pull --version v0.17.0 --out ./release --changelog ./CHANGELOG.md --machines ./machines.tsv --ssh ssh --dest '~/nova-release' --reason "shipped a key"
```

`pull` withdraws a release: the artifacts go here and on every machine, by name, from that release's
own `SHA256SUMS` — never recursively — and the tag stays while the changelog section is marked with
the date and `--reason`. `--dry-run` says what would be deleted and deletes nothing.

```sh
nova-update release cycle --version v1.1.0-dev.abcdef12 --source . --out ~/nova-bench/release-build --inventory ./nova-inventory --benches bench-a,bench-b --reason "the member fix, PR 5092" --ansible "$(command -v ansible-playbook)"
```

`cycle` is the fix-land-install cycle from the coordinator in one command: `fleet/tools.yml` with
`--check`, then for real, limited to `--benches`, `localhost` and the `store_deployer` group, the
build's schema and function library on the store running on every cycle, with the build `--incremental
--gate report --reason <why>`. Each machine's new version directory is seeded from its installed build's
and only the files whose `SHA256SUMS` line differs are sent; `install` leaves a binary that already
holds the same bytes in place, so only the loops of the tools that changed restart. One `CYCLE
BENCH host=<h> … version=<v> state=<s> installed=<n>` line per bench, then `CYCLE OK … check=<d>
apply=<d> total=<d>`; both plays' output is kept under `<out>/<version>/`. `--dry-run` is the check
alone. It runs in the inventory's environment, as the play does. It runs the inventory once with
`--list` first and refuses before any play when it cannot list: a store with ACLs needs its login in
the wrapper's environment (`NOVA_SPRINT_REDIS_USER`, and `NOVA_SPRINT_REDIS_PASSWORD_ENV` naming the
variable that holds the password, never the password; [FLEET.md](FLEET.md) "An adopter's path" has
the wrapper and the `nova-secrets exec` line).

## nova-version

`nova-version` reports installed tool identities and shares the update reader: local stdout by default, optional delivery through `nova-bus send`. The contract is [docs/SPEC-UPDATE.md](SPEC-UPDATE.md).

### First run

```sh
nova-version example --out versions.tsv
nova-version report --file versions.tsv
```

Run this with the binary alone, in any directory: `example` writes a one-tool
manifest (Go) and names the next command; the same file again is left unchanged,
and a file holding anything else is never overwritten. The executable transcript is
in [TESTS.md](TESTS.md#nova-version).
The report reads only installed identities. UNKNOWN means a partial inventory; it
never means zero or current. Replace the example with your own six-column manifest
for your bench; it and any snapshot path belong to the caller.
A `tool` row whose `installed` column is just the executable is asked `version`,
then `--version`, then bare, all inside one `--timeout` — so our own tools, which
answer a bare invocation with a usage refusal, are read rather than reported
UNKNOWN. A row holding a whole argv (`go version`) is run as written.

Use `nova-version help` for filters, optional draft/delivery and limits. A plain report
needs no bus. `nova-version snapshot --file <manifest>` counts the adopted tools the
manifest names and prints one `SNAPSHOT <OK|FAIL> checked=<n> known=<n> unknown=<n>` line,
then a `SNAPSHOT UNKNOWN name=<name>` line for each tool that did not answer —
the adopted 16, never how many `nova-*` executables sit on PATH. Updates require an explicit
`nova-update apply --file ... name`; models are listed for the owner to evaluate and
pull themselves. No timer is installed.

### Capture and compare installed binaries

```sh
go build -o ./bin/ ./cmd/nova-version
nova-version snapshot --bin ./bin --out ./before.tsv
cp ./before.tsv ./after.tsv
nova-version diff --from ./before.tsv --to ./after.tsv
```

Create `after.tsv` with a later snapshot of the directory you want to compare.
`snapshot` runs `version` on the `nova-*` regular files in the explicit directory,
with a thirty-second `--timeout` per binary and a sixty-second `--budget` for the
run, both of which you can set. It skips symlinks, refuses an unreadable
version or mixed stamps, and writes `name`, `stamp`, `revision`, `platform` columns.

The per-binary deadline is thirty seconds rather than the five every other verb
takes because of when this verb is run: right after `go install ./cmd/...`, on a
directory of binaries this machine has never executed. The platform assesses the
first run of a never-seen executable and charges it to that deadline — measured
on a darwin/arm64 bench at 164–571 ms cold against 5 ms warm when idle, and at
a 7.03 s maximum while a tree compiled beside it, which is the state the
`go install` one command earlier leaves the machine in. At five seconds that
refused healthy binaries and named a build repair that would have found nothing
([#890](https://github.com/mas-bandwidth/nova-tools/issues/890)). Lower it with
`--timeout` on a bin whose binaries you have already been running.
`diff` reads two such files and reports changed, added or removed entries without
executing the binaries.

This four-column inventory is **not** the six-column manifest accepted by
`report --file`; the `--bin/--out` shape has no `--owner` flag. The report's
`--snapshot` option below is a separate delivery-recovery file.

`snapshot`'s `--file` shape instead reads the six-column manifest the caller has
already adopted and counts how many of its tools answer, printing one
`SNAPSHOT <OK|FAIL> checked=<n> known=<n> unknown=<n> file=<path>` line and one
`SNAPSHOT UNKNOWN name=<name> reason=<why> remedy=<what to do>` line per tool that
did not answer (`--max` caps them, with a `MORE` line) — the
adopted 16, never the 32 `nova-*` executables a directory or `PATH` might hold.
It writes no file and mirrors `report`'s read, so a recorded version is known
without running a process; it exits 1 when any adopted tool does not answer
([#622](https://github.com/mas-bandwidth/nova-tools/issues/622)).

Snapshot reads the version line with `internal/buildinfo`, the package that
writes it. Named `key=value` extras, such as `nova-sandbox`'s `backend=` and
`platform=`, are accepted as metadata. A binary that prints no version line is
refused by name; a partial inventory is not reported as complete. Name
`--snapshot` to quiet repeats: an unchanged observation sends nothing.
`--send` delivers one note through nova-bus on the Redis bus:
`nova-bus send --as <sender> --to <recipients> --subject <one line> --stdin`.
nova-bus reads the store from `NOVA_BUS_REDIS`. A confirmed `SEND OK id=<id>`
line is what the receipt records. Version reports can be sent to the recipient
you select, with optional Cc.

First-run refusals name what is needed: `--file` wants the six-column TSV header
and explicit argv; paths or arguments containing spaces belong in a wrapper script.
`--draft` needs `--as` and `--to` and sends nothing. `--send` delivers through
nova-bus on the Redis bus; nova-bus reads the store from `NOVA_BUS_REDIS`.
A busy snapshot wants the current writer to finish or a larger `--budget`; never
remove a lock file to break a live lock.


## nova-secrets

Stores encrypted credentials for named seats and delivers selected values to a
child command. Use `nova-secrets help` for store setup, checks and `exec`; the
contract is [SPEC-SECRETS.md](SPEC-SECRETS.md).

### Gate a seat pull request

A throwaway store to try it on: the base commit carries `recovery.pub` and one seat
rule, the head commit edits `README.md`.

```sh
cd "$(mktemp -d)" && git init -q && git config user.name you && git config user.email you@example.com
echo age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata > recovery.pub
printf 'creation_rules:\n  - path_regex: ^bench-a\\.yaml$\n    age: %s,%s\n' age158lrf2hlptfwl6fh280y6pq58vdmumnqzhk5vd669aqf37ca3sus9mcazh "$(cat recovery.pub)" > .sops.yaml
echo 'the store' > README.md && git add -A && git commit -qm base
echo 'one seat per machine' >> README.md && git commit -qam head
printf 'bench-a\tbench-a.local\tdarwin/arm64\tbench\tbench-a\t10\t-\n' > ../machines.tsv
nova-secrets gate --store . --base HEAD~1 --head HEAD --machines ../machines.tsv
```

It prints `GATE APPROVE files=1 machines=../machines.tsv` at exit 0. In CI, `--store` is the
secrets store checkout, `--base` and `--head` are the pull request's two shas, and
`--machines` is the fleet registry (`./queue/control/machines.tsv`).

The store's own review, as a verb: run it in CI on every pull request against the
secrets store. It diffs the two refs with git and asks GitHub nothing. It prints
`GATE APPROVE files=<n> machines=<registry|->` at exit 0, or one
`GATE FAILED rule=<n> check=<k> file=<f>: <why>` line at exit 1.

`--base` and `--head` each name one commit. A ref beginning with `-`, a ref that
names no commit, and any gate flag given twice are each a gate that could not run,
one `SECRETS GATE REFUSED` line at exit 2 naming the flag, before anything is judged.

`--machines` is the fleet's machines registry, and its `seat` column is what
vouches for a recipient key the diff introduces: a new key is permitted only for
a seat some machine in the registry carries, so adding a seat needs no human
approval and still cannot grant a key to a machine the fleet does not have. A row
whose seat reads `-` vouches for nothing. Leave `--machines` off and that rule
does not run — the approval line then says `machines=-`, so an APPROVE is never
mistaken for the fleet having vouched.

### Seal a replacement value

```sh
nova-secrets seal --store ./secrets --as worker --key /path/to/seat.key \
  --sops /path/to/sops --name PROVIDER_API_KEY --no-pr
```

Run this in a prepared store with that seat and its recipients configured. Enter
the value at the hidden terminal prompt; never put it in the command line. An
explicit `--stdin` accepts a value through standard input instead. Encryption
uses the store's SOPS configuration. `--no-pr` creates a branch and commits the
encrypted change locally, without pushing or opening a pull request. It then
returns the working copy to the branch it started on; the seal commit stays on
the `seal/...` branch, the OK line names it, and a `SECRETS SEAL NOTE` line under
it says `exec` does not read the value yet and gives the push that carries it. A leftover seal branch has no
upstream, and `exec` would refuse every later card on that store. A dirty store
(staged or unstaged tracked changes) is refused before any branch switch, so
local edits are not discarded.

`--dry-run` prints the plan and writes nothing: the file and whether the name is
added or replaced, the recipients, the branch, the commit and the pull request title,
as `SECRETS SEAL PLAN` lines ending in `DRY-RUN OK` at exit 0. It reads no value,
encrypts nothing and runs no push or `gh` call. `place` and `seat inject` take it too.

Without `--no-pr`, the command pushes its branch, opens a PR and waits up to two
minutes for the gate's approval, reporting progress while it waits. Once approved,
it merges, returns to the previous store branch, pulls and checks that the seat
can decrypt. An `open (gate not yet approved)` receipt means the PR is still
pending; it does not mean the replacement is active.

### Give a new seat its first values

```sh
nova-secrets seat add --store ./secrets --as worker --pub age1… --from lead \
  --only GH_TOKEN,DEEPSEEK_API_KEY --key /path/to/lead.key --sops /path/to/sops
```

`seal` cannot do this: it decrypts a seat file before it writes one, and only the
new seat's own key opens the new seat's file. `seat add` re-seals the `--only`
values out of `--from`, a seat this machine can already open, into a new
`<seat>.yaml`, writing that seat's `.sops.yaml` rule first so sops has recipients
to encrypt to. `--pub` is the new bench's **public** key, taken from its own
`keygen` receipt.

It refuses, changing nothing, when the source seat cannot be opened with `--key`
here, when `<seat>.yaml` already exists, when a rule already matches that file, or
when `--from` does not carry one of the `--only` names. It prints no value on any
line. It commits nothing: the receipt names the two changed files, and the store's
gate reads them in a pull request as it does every other recipient change.

### Re-seal values into an existing seat

```sh
nova-secrets seat inject --store ./secrets --as worker --from lead \
  --only NOVA_REDIS_BENCH_PASSWORD --key /path/to/lead.key --sops /path/to/sops
```

`seal` runs only where the target seat's own key lives, and `seat add` refuses a
seat file that exists. `seat inject` re-seals the `--only` values out of `--from`,
a seat this machine can open, into the existing `<seat>.yaml`, encrypted to the
two recipients that file's own sops metadata names (the seat's key and the
recovery key, held equal to its rule first), then walks `seal`'s road: a
`seal/<seat>-<NAMES>-<stamp>` branch, one commit, a push, the pull request the
store's gate approves, the squash merge, the pull and `check`. `--no-pr` stops
after the commit, returns the store to its starting branch and names the branch
on the OK line: `SECRETS SEAT INJECT OK seat=worker from=lead names=1 committed branch=seal/worker-NOVA_REDIS_BENCH_PASSWORD-20260927-013000`.

This key cannot open the target, so every sealed value the target holds is
re-sealed from the source's current value; a value the rule permits in the clear
is kept from the target byte for byte. It refuses, changing nothing, when the
seat file does not exist (run `seat add`), when the source cannot be opened with
`--key` here, when `--from` does not carry one of the `--only` names, when the
target holds a sealed name the source does not, or when the target's recipients
cannot be read from its metadata or differ from its rule. It prints no value on
any line.

### Reading a `keygen` receipt

`keygen` prints the `.sops.yaml` rule block first, then a `SECRETS RULE NEXT:` line
saying what to do with it, then `SECRETS KEYGEN OK` **last**. A run that ends on the
OK line succeeded; a `NEXT:` line above it is the next step, not a failure.

## nova-ci

Reads Go test events and reports packages whose accumulated elapsed time exceeds
a budget. `slowtests` and `functional` work in any Go module; `local`,
`new-rule` and `new-verb` need a nova-tools checkout; `github receipt` writes to
a Redis store. It also reports its own build with `nova-ci version`.

```sh
nova-ci slowtests --example --budget 60
go test -json ./cmd/mytool | nova-ci slowtests --budget 60
nova-ci version
```

`--example` reads a built-in event stream, so the first line runs from the
binary alone. On your own module, pipe `go test -json` in and check that test
run's exit status separately: `slowtests` checks timing, not whether the tests
passed. The default budget is 60 seconds per package; a package over it is a
`CI-SLOW` line, and exit 0 still means a measurement unless `--enforce` is given
(then exit 1: the check ran and said no). A terminal on stdin or a line that is
not a TestEvent is refused at exit 2, and so is a float flag that is not a finite
number (`NaN`, `Inf`). `--json` prints the same verdict as one JSON object
(`{"result":{...},"facts":{...},"items":[...]}`), and a refusal as that object
with `"status":"refused"` on stdout. A run with more than `--max` finding
lines (20 by default) prints the first `--max` and one `CI-SLOW MORE
shown=<n> total=<n>` line naming the flag that prints the rest; `--max 0`
prints every finding. CI exceptions belong in the
dated project policy, not in an assumed higher tool default.

A refusal is one line, `nova-ci <verb> REFUSED: <every problem>; run: <next
command>`, the next command most often the verb's own `-h`; each verb's `-h`
ends with that verb's own exit codes.

`nova-ci local [--base origin/dev] [--functional] [--dry-run]` runs, on your machine, exactly
the unit tier CI runs for your change: the packages
`go run ./tools/ci select-packages` picks against the merge base of `--base` and
`HEAD`, through the Makefile's `test` target (its go test flags and slowtests
budgets) under `nice -n 15` with `GOMAXPROCS=2`, `GOTEST_P=2` and `-count=1`. It
prints one `PKG` line per package with its seconds and one `RED` line per failing
test with its output; exit 0 is green, 1 a red test or build or a CI-SLEEPS line,
2 a step that could not run. `--functional` adds the functional build tag
(`GOTEST_TAGS=functional`); CI runs those tests in its `functional` job as a
stream merges ([TESTING.md](../TESTING.md)). `--dry-run` prints the packages and
the `make test` line and runs no test.

The unit tier (`make test`) passes `--package-budget 2 --test-budget 1 --allowlist
internal/ci/slow-tests_allowlist.txt --sleeps internal/ci/sleeps-skips_allowlist.txt`
instead: a package over 2 s or a top-level test over 1 s is a `CI-SLOW` line unless
its allowlist row (`pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>`, where is
`run<id>` or a bench) names more. A `CI-LOAD load=<n> cpus=<n> per-cpu=<n>:
measured, not a verdict` line follows (`--load` and `--cpus` give the figures by
hand). A CI-SLOW line exits 0 (a measurement) unless `--enforce` is given, which
only the nightly bench legs pass (`make test SLOWTESTS_ENFORCE=1`). A test skipped
with `t.Skip("SLEEPS: ...")` that `--sleeps` does not name is a `CI-SLEEPS` line
and exits 1 on every leg. A package `go test` served from its test cache reports a
package elapsed near zero (`ok ... (cached)`, `"Elapsed":0`), so a cached run can
never trip `--package-budget` (or `--budget`); its tests replay the times of the run
that was cached, which `--test-budget` still reads. CI's unit legs run with the
cache on (`GOTEST_COUNT_FLAG=` in `.github/workflows/ci.yml`); its `--enforce` leg
runs `-count=1`, and so does a measurement by hand. `nova-ci
functional <package-dir>...` prints, for `make test-functional`, the packages
that hold `//go:build functional` tests and a `-run` pattern naming exactly
those tests; when there are none it prints one line, `CI FUNCTIONAL OK packages=0
reason=<why>`, and exits 0 ([TESTING.md](TESTING.md), "The two tiers"). It never
exits in silence: a flag, and a package pattern that matches no package, are
refused at exit 2, every problem in the one line:

```
nova-ci functional REFUSED: package pattern "./nope" matches no package (no such directory); run: nova-ci functional -h
nova-ci functional REFUSED: unknown flag "--bogus" (functional takes no flags, only package directories such as ./cmd/nova-table or ./internal/...); run: nova-ci functional -h
```

`-h` and `--help` after the verb are not refused: they print the verb's help on stdout at exit 0, which is not silence either.

`nova-ci new-rule [--root <checkout>] [--dry-run] <rule-name>` and `nova-ci
new-verb [--root <checkout>] [--dry-run] <tool> <verb>` lay down a class rule's
or a verb's skeleton in a nova-tools checkout and print one `wrote <path>` line
per file; `--dry-run` prints `would write <path>` for the same files, checked
against the tree, and writes nothing. A file already there, a bad name, a root
with no `go.mod` and (for new-verb) a tool with no `func main` are refused at
exit 2 and nothing is written.

See [SPEC-CI.md](SPEC-CI.md).

### bench run

`nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>]
[--cache <dir>] [--with-git] -- <go command>` runs one command on a Linux bench
against a copy of a local tree, in place of the ssh, rsync and ssh recipe a
brief used to carry. It makes a fresh run directory under `--root` on the bench
(`mktemp -d`, default `nova-bench/runs` under the login's home), copies the tree
into `<run>/repo` (a tar stream this process writes onto ssh's stdin, unpacked
by the bench's tar; nothing but ssh runs here) with `.git` left out unless
`--with-git`, runs the command
there under `nice -n 19` with `GOCACHE` (`--cache`, default
`nova-bench/cache/go-build`), `GOFLAGS=-mod=readonly` and `NOVA_TEST_NO_HOST=1`,
streams its output to stdout and stderr as it arrives, and removes the run
directory it made and nothing else, whether the command passed, failed or was
interrupted. A `--host` that does not answer (ssh's own exit 255) is passed over
for `--fallback` with one `CI BENCH PASSED host=<h> reason=<why> next=<h>` line;
a host that answers is never passed over. The run ends with one line on stderr,
`CI BENCH host=<h> run=<dir> exit=<n> removed=yes|no`, so stdout is exactly the
command's. The exit status is the command's own; a run that never reached the
command (usage, no bench answered, the copy failed) is one `BENCH-RUN REFUSED:`
line at exit 2. The verb is on internal/tool, so its flags come before `--` and
every word after the first `--` is the command. Neither bench is guessed: `--host` is required, and
`--root` and `--cache` are plain paths, relative to the login's home or
absolute, never `~`, `..`, the home or `/`. For example, `nova-ci bench run
--host <bench> --fallback <other-bench> --dir . -- go test -count=1 ./cmd/nova-ci/`.

### github receipt

`nova-ci github receipt --from-runner --redis <addr> --repo owner/name --sha <40hex>
--run-id <n> --workflow <name> --conclusion success|failure|cancelled [--pr <n>]
[--at <rfc3339>] [--dry-run]` is the run receipt the `ci-ok` job of `.github/workflows/ci.yml`
writes at the end of every run, from this tree (`go run ./cmd/nova-ci`) and as the
bench seat: one `ev:github` row of the `workflow_run` shape, sender `runner`, action
and status `completed`, the PR number as `number` (empty for a run that names no
PR), `--at` or now. The command writes no other key (internal/cireceipt).
The store is dialled as the
environment's seat (`NOVA_SPRINT_REDIS_USER`, `NOVA_SPRINT_REDIS_PASSWORD_ENV`);
`--redis` falls back to `NOVA_REDIS_ADDR`. A written receipt is one line,
`CI RECEIPT <owner/name> sha=<sha> run=<id> workflow=<name> conclusion=<word>
pr=<n|-> ev=<stream id>`, exit 0; a write the store refuses or cannot confirm
(`XADD ev:github: WRONGTYPE ...`, a NOPERM seat, reply loss, or a store that is
down) is one line on stderr, `nova-ci github receipt FAILED: ...`, ending `receipt
write could not be confirmed: fix the store or the bench seat and rerun ci-ok`,
exit 1, which reddens ci-ok (repeat receipts from retries or reruns are
acceptable wake hints for consumers). `--dry-run` checks the fields and prints
the receipt line with `ev=-` and a `CI RECEIPT NOTE` line, dialling nothing, so
the verb can be tried with no store. Every refused field and a missing store
address are named in one refusal at exit 2, before any dial:

```
$ nova-ci github receipt --from-runner --repo nova-tools --sha 9af23a05e0000000000000000000000000000000 --run-id 1 --workflow CI --conclusion success
nova-ci github receipt REFUSED: --repo wants owner/name, got "nova-tools"; needs --redis <host:port> or NOVA_REDIS_ADDR (or --dry-run, which dials nothing); run: nova-ci github receipt -h
$ nova-ci github receipt --from-runner --repo mas-bandwidth/nova-tools --sha 9af23a05e0000000000000000000000000000000 --run-id 1 --workflow CI --conclusion skipped
nova-ci github receipt REFUSED: --conclusion wants success, failure or cancelled (job.status), got "skipped"; needs --redis <host:port> or NOVA_REDIS_ADDR (or --dry-run, which dials nothing); run: nova-ci github receipt -h
```

## nova-config

```
nova-config kinds [--json]                                               # every kind: its table, its fields, the fields add requires
nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--window] [--json]   # create or upgrade schema config (or make the --file); --print lists the migrations and connects to nothing; --dry-run reads the ledger and prints each migration applied, pending or missing and each table of schema config the role does not own, applying none, and exits 1 when migrate would refuse (ready=no); the role that runs migrate must own every table in schema config, else migrate refuses before applying any and prints the ALTER TABLE ... OWNER TO lines; --window (the seat play's stopped window) refuses, applying nothing, while any other nova session holds the database; --dry-run also prints owner=<role>|mixed|none (who owns schema config whole) and sessions=<n> with a MIGRATE SESSION line each
nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]         # the store, the schema version, rows and revision per kind, and what Redis has applied
nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>] [--kind <kind>] [--dry-run] [--json]   # write the rows into Redis per kind through the runtime's own functions, compare-and-set on the revision; --dry-run (or --check) prints the plan and writes nothing
nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>] [--timeout <duration>] # print an Ansible dynamic JSON inventory of the applied state (Redis, never Postgres); --list is the default, --host prints one machine
nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]   # insert a row; a duplicate name is refused with the set to run; --dry-run prints CONFIG DRY-RUN and writes nothing
nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]   # update the fields named
nova-config <kind> remove <name> --as <name> [--dry-run] [--json]        # delete the row
nova-config <kind> list [--json]                                         # one typed line per row
nova-config <kind> show <name> [--json]                                  # one line with every field and the stamps
nova-config <kind> history <name> [--json]                               # every change to the row: who, when, what changed
nova-config <kind> <verb> -h                                             # the verb's flags (required ones marked), its effect and a worked example
nova-config machine list|show <name> [--redis <addr>]                    # with a Redis, each line ends in the machine's live measured facts from its beat (os, arch, cores, memory_gb, beat=<t> or beat=none)
nova-config machine width <name> [--pg <dsn> | --file <path>] [--json]  # the width of the sprint's member on the machine: the row's width field (machine set <name> --width <n>), what nova-sprint fleet sync sets; above 0 it is a member, 0 is none, unset (--width default) is the default, half the machine's cores as fleet sync resolves them from its beat; no Redis
nova-config machine self [--check] [--json]                              # this machine's own name (NOVA_MACHINE, else the tailnet's name, else the hostname's first label); --check exits 2 when it is no machine row, 3 when unreadable
nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --friend <actor> [--sops <path>]  # record the Postgres login (never the password); a bare verb then connects with it
nova-config login --check                                                # print the recorded login and whether the secret resolves; the password is never shown
nova-config logout                                                       # remove the recorded login
nova-config fleet set --store <m> --coordinator <m> --redis_port <port> --pg_dsn <uri> --bus <host:port> --as <name>  # the one fleet row: no name, no add, remove or list
nova-config sprint set --coordinator <friend> --as <name>                # the one sprint row: who coordinates; set it to hand over
nova-config fleet|sprint show|history                                    # the one row, its stamps, its changes
nova-config loop add <name> --machine <m> --argv '["/path/prog","--flag","v"]' (--every <seconds> | --keepalive true) [--seat <seat> --keys <NAME,...>] [--enabled false] --as <name>   # a supervised loop on one machine: the command as a JSON array, the secrets by name from the seat, every n seconds or kept alive; a nova-swarm member argv spells no --width, its width is its machine row's
nova-config loop set|remove|list|show|history                             # the one grammar, as for every kind; the argv is the words the unit runs; machine show <m> names the machine's loops (loops=<a,b>)
nova-config loop run <name> [--run-dir <dir>] [--metrics <dir>] [-- <command> ...]   # the loop's command under <run-dir>/<name>.lock (default ~/nova-bench/run): a second copy exits 3, a lock whose holder died is taken, the lock lives in the wrapper so a wrapper killed outright ends the command with it (linux's parent-death signal) and a restart never doubles it, the start is counted in <name>.starts and written to --metrics as nova_loop_starts_total for node_exporter, SIGINT and SIGTERM pass to the command, and the exit is the command's, 128+N when a signal ended it; with no command after -- it runs the row's argv (a disabled row, or one with keys, is refused with exit 1); the Go verb in place of the bash nova-loop wrapper
nova-config route add <name> --tier flash|pro|heavy --provider <p> --model <m> [--harness opencode|claude|codex|grok] --deadline <seconds> [--tokens <n>] [--usd <dollars>] [--enabled false] --as <friend>   # one way to run a model tier: the harness runs <provider>/<model>, (a headless --harness claude, codex or grok takes --provider subscription-<harness>) stopped at its token budget or its dollar budget (the harness's reported cost), whichever comes first; frontier cards escalate to the coordinator and are never dealt from routes
nova-config route set <name> --price_input <usd> --price_cache_read <usd> --price_cache_write <usd> --price_output <usd> [--reasoning_as_output false] [--long_context <tokens> --price_input_long <usd> --price_output_long <usd>] [--price_request <usd>] [--billing metered|plan] [--gateway_percent <pct>] [--price_source <text>] [--price_as_of YYYY-MM-DD] --as <friend>   # the route's price sheet, optional: USD per million tokens of each class, each a decimal kept exactly; a route with none prices no card
nova-config route prices --refresh [--provider openrouter|opencode] [--from <path>] [--dry-run] --as <name>   # set each enabled route's prices from its provider's published list (OpenRouter's models endpoint; opencode rows assumed from it, a NOTE says so), price_as_of today and price_source the URL; a price that moved past 2x is a JUDGMENT line, left as it is, exit 1; a row more than 10 percent off the list is named STALE
nova-config tier set flash|pro --routes <route,route,...> --as <friend>   # the tier's route array: the deal takes routes[index mod len] for each card of the tier, the index a uint64 counter on the fleet table; a route named twice takes two turns
nova-config route set|remove|list|show|history                            # the one grammar, as for every kind
```

`nova-config` is the one tool for the fleet's permanent, non-ephemeral configuration: Postgres (schema `config`) is the permanent store, and `apply` writes it into Redis so Redis is always a rebuildable copy. The kinds are `machine` (user, seat, slots, runners, width, tla; the name is the tailnet host; `tla=true` marks a TLC record machine), `fleet` (one row: the store and coordinator machines, Redis port and explicit password-free Postgres URI), `friend` (slots, tiers, roles, width: the jobs she works at once, 8 by default, mode: batch or one-shot, how her daemon hands her work), `sprint` (one row: the coordinating friend), `loop` (a supervised process on one machine: machine, argv, seat, keys, every or keepalive, width, enabled; apply writes `loop:<name>` and the set `loops`, which the plays read) `route` (one way to run a flash, pro or heavy tier: tier, provider, model, harness, tokens, deadline, enabled; apply writes `route:<name>` and the set `routes`, which the deal reads) and `tier` (one row each for flash, pro and heavy, made by migrate: routes, the ordered route array the deal takes at the tier's index; apply writes `tier:<name>` and the set `tiers`); the contract is [SPEC-CONFIG.md](SPEC-CONFIG.md) and the guide is [nova-config/README.md](nova-config/README.md).

### First run

```sh
nova-config migrate --file try.json
nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json
nova-config machine set m1 --width 6 --as a1 --file try.json
nova-config machine list --file try.json
nova-config machine history m1 --file try.json
```

None needs a database: `--file <path>` keeps the rows in a local JSON file in PostgreSQL's place, with the same kinds, refusals, history and revisions (it is the strict in-memory store the tests hold to the store contract, saved after every write), so every verb but `apply`'s write runs on it. `migrate --file` makes the file; each write prints `CONFIG ADD|SET` and its history id; `list` prints the row; `history` prints each change with who made it and when. Every verb's `-h` prints its flags, its effect and a worked example that runs on the same file (`nova-config loop add -h`). The executable transcript is in [TESTS.md](TESTS.md#nova-config). A file is never the fleet's store: the runtime tools read PostgreSQL.

The fleet's store is a Postgres, and `apply` needs a Redis (a throwaway local Postgres on `127.0.0.1:5432` and a local Redis on `127.0.0.1:6379` will do), so there is no `quickstart`: a verb that made a store nobody asked for would write state on the way to a demonstration. With them running:

```sh
export NOVA_PG_DSN=postgres://nova_config@127.0.0.1:5432/nova
nova-config migrate
nova-config machine add m1 --user nova --seat s1 --slots 64 --width 32 --as a1
nova-config fleet set --store m1 --coordinator m1 --redis_port 6379 --pg_dsn postgres://nova_config@127.0.0.1:5432/nova --as a1
nova-config friend add f1 --slots 32 --tiers frontier,pro --roles builder --as a1
nova-config sprint set --coordinator f1 --as a1
nova-config apply --dry-run --redis 127.0.0.1:6379
nova-config apply --redis 127.0.0.1:6379 --as a1
```

**What the flags want.** `--pg` is `postgres://user@host:port/db` with no password in it (env `NOVA_PG_DSN`); the password is read from the variable `NOVA_PG_PASSWORD_ENV` names (`NOVA_PG_PASSWORD` when unset), never from the line, and a `--pg` carrying one is refused. `--file <path>` stands in for it and the two are exclusive. `--redis` is `host:port` (env `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's address). `--as` is the name a write is recorded under (env `NOVA_FRIEND`, else the friend `login` recorded), required on every write (omitted on `apply --dry-run`) and recorded in `config.history`. A name is lower-case letters, digits and dashes. `add` needs every required field (its `-h` marks them `required:`) and refuses a value outside its type, every problem in one line; `set` changes only the fields named. `--dry-run` on `add`, `set` and `remove` prints `CONFIG DRY-RUN op=<op> kind=<k> name=<n> actor=<a> wrote=nothing` with the fields as `history` would print them, from the same checks, and writes nothing; `--json` on every verb but `inventory` (already JSON) prints one object in `internal/tool`'s shape. A run missing several flags names all of them at once; an unknown flag names the flags the verb takes and the nearest one.

**Ansible inventory.** `inventory` reads the applied state, the Redis view `apply` writes, and never Postgres: what the fleet plays converge machines to is what the running tools read. It makes two round trips whatever the fleet's size: the names (the machines and loops sets, the fleet row, `config:decl`), then every machine's hash, ceiling and beat and every loop's hash. It prints an Ansible dynamic JSON inventory: the groups `all` and `benches` (every machine), `coordinator`, `store` and `store_deployer` (the machines the fleet row names; `store_deployer` is the coordinator machine, whose seat loads the function library and the ACL onto the store; empty when the row names none) `runners` (every machine with at least one runner) and `tla` (every machine whose row says `tla=true`: the TLC record machines, where the tools play holds the pinned TLC jar). Every host's variables are under `_meta.hostvars`: `ansible_host`, `ansible_user`, `nova_seat`, `slots`, `runners`, `nova_tla`, `kind=machine`, `nova_os` and `nova_arch` from the machine's beat when it has one, and `nova_loops`, its loop records typed (`name`, `argv`, `seat`, `keys`, `every`, `keepalive`, `width`, `enabled`, `log`), once the loop kind has been applied (`rev:loop` in `config:decl`; before that the variable is absent, which is not an empty list). `all.vars` holds `nova_store` and `nova_config_rev`. A loop record the plays could not render a unit from (an argv that is not a JSON list, keys without a seat, both or neither of `every` and `keepalive`, a machine with no row) exits 1 naming it. `--redis` is the store (env `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's); `--fixture <file>` reads a YAML or JSON file of the same rows in its place and opens no store (`fleet/testdata/inventory-fixture.yml` is one), and the two are exclusive. `--list` (the default with no flag) prints all of it; because `_meta.hostvars` is there, ansible never calls `--host <name>`, which prints one machine's variables and exits 1 with the known names when no row has that name. `--list` and `--host` together are refused. `--timeout` (a Go duration, default `10s`) bounds the wait for the store; on expiry, at the connection or the read, the verb exits 2 with `timed out after <d> waiting for the store at <addr> while <stage>` and the command to repeat with a longer timeout. Env `NOVA_MACHINE` names the machine row the command runs on (an empty value counts as unset), matched by exact machine name and refused with exit 1 and the known names when no row has it; unset, the lower-cased first label of the hostname (machine names are lower-case) is matched the same way and nothing is marked local when no row has it. The matched host gets `ansible_connection=local`. Ansible's `-i` wants an executable, so a two-line wrapper carries the tool and its environment:

The applied fleet row also gives every host `nova_redis_port`,
`nova_redis_addr` and the explicit `nova_pg_dsn`. Inventory and fleet apply
refuse an unset endpoint with one `nova-config fleet set --redis_port <port>
--pg_dsn <dsn>` command; the Redis port has no default. Host scope keeps a group
default from replacing port 6380, and a localhost Postgres URI stays localhost.
Loop units receive `NOVA_SPRINT_REDIS` from that applied endpoint. Inventory
removes an older endpoint assignment only from a rendered `nova-swarm member`
`/usr/bin/env` prefix, preserving the Redis user, password variable name and
all other argv words while the persisted row is cleaned with `loop set`.

```sh
nova-config inventory --fixture fleet/testdata/inventory-fixture.yml
export NOVA_SPRINT_REDIS=127.0.0.1:6379
nova-config inventory
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
chmod +x nova-inventory
ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list
```

Ansible hides a failing inventory script: when the wrapper exits non-zero (`nova-config` missing from the PATH, a `nova-config` without the verb, an unknown `NOVA_MACHINE`, a store that is down, a timeout, a loop it cannot render), `ansible-inventory` and `ansible-playbook` log a warning, use an empty inventory and exit 0, so a playbook does nothing. `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` in the environment, or `[inventory] unparsed_is_failed = True` in `ansible.cfg`, makes the same run exit non-zero. The plays that read the inventory are [FLEET.md](FLEET.md)'s.

**Reading it.** Every write prints `CONFIG ADD|SET|REMOVE kind=<k> name=<n> rev=<id>`, the id of its history row. `list` prints `<KIND> name=<n> <field>=<v> ...` per row and a `CONFIG LIST` count; `history` prints `HISTORY id=<n> ... op=<add|set|remove> actor=<a> at=<t>` with each changed field as `<field>=<before>><after>`. `apply` prints `APPLY ADD|SET|REMOVE kind=<k> name=<n>` per row it writes and one `CONFIG APPLY kind=<k> add=<n> set=<n> remove=<n> rev=<r> ms=<n>` per kind; for sprint, a live coordinator the row disagrees with is left as it is, every other field is written, and the library reports one `APPLY HELD kind=sprint field=coordinator live=<a> row=<b>: the seat moves by nova-sprint's seat verb or nova-config apply --kind sprint --move-seat; run nova-config sprint set --coordinator <a> to make the row agree` line (exits 0), printed whole (`SaidLine`). `--json` emits the line as the op's `name`. `--move-seat` (the owner's word) writes the row's differing coordinator instead (`ApplyMovingSeat`). A held coordinator whose only difference is that field still counts as one `SET` and advances the revision stamp, so `status` can read in sync while the row disagrees; the held line is the signal. `--dry-run` (or `--check`) prints the same plan as `CHECK` lines and `CONFIG CHECK`. `status` exits 1 with the next step when the schema is missing (`run: nova-config migrate`) or Redis is behind (`run: nova-config apply`). A machine added with no `--width` is no sprint member: `add` prints a `NOTE` saying so with the `machine set <m> --width <n>` line.

**Refusals.** Every refusal is one stderr line, `nova-config <verb> REFUSED: <what>; run: <next>`. Exit 1 is the store or Redis saying no, naming the next step: `machine m1 exists; run: nova-config machine set m1 ...`, `--store m9 names no machine row`, `machine m1 is the --coordinator of the fleet`, `friend f1 is the --coordinator of the sprint`, `CONFLICT friend: Redis holds rev 9 and this Postgres is at rev 4`, `CEILING m1: friend f2 makes the sum 65 over the machine ceiling 64`, `friend f3 has no beat naming a machine and the fleet names no coordinator machine to charge her slots to`. Exit 2 is an invocation that could not run (a name on a singleton is one, an unknown flag another), and names the verb's `-h`.

### The store login

`nova-config login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --dsn <dsn without password> --friend <actor>` records the store login in `~/.config/nova-config/login.json` (or under `$XDG_CONFIG_HOME`), mode 0600: the DSN, the friend and where the password is in nova-secrets, never the password, and only once the secret resolves. After it, `nova-config <verb>` typed bare reaches that PostgreSQL, the password read in the verb's own process through nova-secrets' checks, with no `nova-secrets exec` wrapper and no env prefix; `--pg`, `NOVA_PG_DSN` and `NOVA_PG_PASSWORD_ENV` still win. `login --check` prints `LOGIN file=… dsn=… friend=… … resolves=yes|no` (exit 1 on no), the password never shown; `logout` removes the record. A recorded secret that does not resolve is refused naming the file and the remedy, never dialed without a password. The contract is [SPEC-CONFIG.md](SPEC-CONFIG.md#the-store-login).

## nova-redis

```
nova-redis serve  --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <u>[,<u>...]] [<secret login>] # run redis-server in the foreground, loopback and tailnet only, AOF on, ACL users kept in <store-dir>/users.acl
nova-redis install store|bus <secret login> [--bind <addr>] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run] # serve as a service of this machine
nova-redis uninstall store|bus [--units <dir>] [--dry-run]                    # unload and remove that unit; the store directory is kept
nova-redis spill  <login> --owner <o> --name <n> --ttl <d> --value <v> [--dry-run] # write scratch under <o>:<n> with a required TTL; --dry-run dials nothing
nova-redis recall <login> --owner <o> --name <n>                              # read it back; exit 1 on a missing or expired key
nova-redis fn load  <login>                                                   # put this binary's function library on the store unless it holds exactly that code
nova-redis fn check <login>                                                   # compare the store's library with this binary's; changes nothing
nova-redis acl render                                                         # the store's ACL users this build renders, one ACL SETUSER line each; opens no store
nova-redis acl check <login>                                                  # compare the store's live ACL with them; changes nothing
nova-redis acl apply <login> [--password-env-for <user>=<NAME>]... [--dry-run] # set the users that differ, and save the ACL file
# <login> is --addr <host:port> [--user <name>] [--password-env <NAME>]
# <secret login> is --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>
```

**The store and the bus as services.** `install store` and `install bus` write a unit (a launchd agent on macOS, a systemd user unit on Linux, kept alive and started again at login) that runs `nova-redis serve` itself, and load it with `launchctl` or `systemctl --user`. serve writes redis-server's whole configuration from its flags and hands it over stdin, so no configuration file is written by hand: the binding (default `127.0.0.1`, loopback and tailnet only), the port (default 6380 for the store, 6381 for the bus) and the store directory under the bench root (default `~/nova-bench/redis/store` or `~/nova-bench/redis/bus`). The unit carries no password and no `nova-secrets exec`: with `<secret login>`, serve reads the password in its own process from that name in that seat (the path `nova-secrets exec` takes), and hands it to redis-server on stdin only; install refuses a login missing a field. `--dry-run` prints the unit and writes and loads nothing; `uninstall` unloads it and removes its file. `nova-sprint units --check` names both units installed, missing or different.

`nova-redis` owns a Redis instance ([SPEC-REDIS.md](SPEC-REDIS.md)). Every verb that talks to a store opens it one way, through `internal/redisconn`: one dial, the handshake and the login bounded, no retry.
- `--addr` is the store's `host:port`.
- `--user` is the ACL user to log in as. Its default is `NOVA_REDIS_USER`, and with neither set the verb logs in as the store's default user.
- `--password-env` names the variable that holds the password. Its default is the variable `NOVA_REDIS_PASSWORD_ENV` names, else `NOVA_REDIS_PASSWORD`. A seat whose secret has its own name (`nova-secrets exec --only <NAME>`) passes `--password-env <NAME>` and needs no copy. The password itself is never an argument.

Each of these is refused (exit 2) before the dial, and the refusal names where the bad value came from, the flag or the variable. A store that cannot be reached, or a login it refuses, exits 2 in every verb. A refused invocation prints one line per problem with the line, all in one run: `<TOKEN> REFUSED: <what was wrong>; run: nova-redis help`, where `<TOKEN>` is the verb upper-cased with dashes (`SPILL`, `RECALL`, `FN-LOAD`, `ACL-CHECK`). A misspelled flag is named with the flags the verb has, and an unknown verb with the verbs. `spill` and `recall` take `--json` and print the same value as one JSON object on stdout, refusals and failures included: `{"result":{"verb","status","ok|failed|refused","exit","remedy","why"},"facts":{...},"items":[...],"notes":[...],"payload":""}`, the first line's fields under `facts` and `recall --json` carrying the value exactly, where the line escapes its spaces. `spill --dry-run` makes every check, the login's too, and prints `SPILL OK key=<k> ttl=<d> expires=<t> bytes=<n> store=<a> written=0 dry_run=true`, dialling nothing.
- a missing or malformed `--addr`;
- a `--password-env` that is not a variable name (capital letters, digits and underscores);
- a user name holding whitespace;
- a user whose password variable is empty.

**The function library.** The `fn` verbs handle the `nova_sprint` Redis function library, the Lua that nova-table and nova-config call with `FCALL`. The library is the one this binary embeds (`internal/nsprint/fn`'s `lua/`), and the machinery is `internal/redisfn`. A library's identity is its code as the store holds it, and its digest is the first 16 hex digits of the code's SHA-256.

- `fn load` is the deployer's load (`redisfn.Ensure`). It writes nothing when the store holds exactly this code. Otherwise it sends one `FUNCTION LOAD REPLACE`, so the store holds the whole old library or the whole new one. It prints one line:
  - `LOADED nova_sprint sha=<d> store=<a>`: the name was free.
  - `UNCHANGED nova_sprint sha=<d> store=<a>`: nothing was sent after the read.
  - `REPLACED nova_sprint sha=<d> was=<old> store=<a>`: other code was under the name.
  
- `fn check` changes nothing (`redisfn.Check`). Its line is `OK|STALE|MISSING nova_sprint sha=<want> loaded=<d|none> want=<d> store=<a>`, so every line of both verbs holds one `sha=`, this binary's digest:
  - `OK`: the store holds this binary's code, exit 0.
  - `STALE`: the store holds other code, exit 1.
  - `MISSING`: the store holds no library of the name, exit 1.
  
  `STALE` and `MISSING` end in the remedy, `nova-redis fn load <login>`, which logs in as the check did. It keeps every login flag given on the line, even an empty one or one equal to the default, and adds what the environment set to other than the default. Each value is quoted as one POSIX shell word, so the printed command can be pasted as it is.

**The store's ACL.** The `acl` verbs keep the store's users in the shape this build renders (`internal/redisacl`), one user per role: `coordinator` (every key, every function, `FUNCTION LOAD`), the member's `bench`, the table reader's `ns-table` and the friend's `ns-friend`. A role is its key families (`table:*` and `tables`, `view:*` and `views`, `sprint:*`, `machine:*` and `machines`, `bench:*`, `friend:*` and `friends`, `fleet:*`, `loops` and `loop:*`, `routes` and `route:*`, `config:decl`, `tokens:ledger:*` (nova-tokens, under the seat's user); read and write or read only, by role), its command categories (`-@all +@read +@write ... -@dangerous -@scripting`, the reader `+@read` only) and `FCALL` of exactly the functions the embedded library registers in the role's files, `FCALL_RO` of the no-writes ones, read from the library itself; every role may `FUNCTION LIST`. A function runs its commands under the caller's ACL, so each role is also granted, by name, every Redis command its files' Lua calls (`TIME`, `HSET`, `XINFO STREAM` as `+xinfo|stream`, ...), derived from the Lua text, never listed by hand. No function name is listed by hand, so a function added to a file reaches its roles at the next render.
- `acl render` prints one `ACL FAMILY name=<f> keys=<patterns>` line per family, one `ACL SETUSER <user> on clearselectors resetkeys resetchannels ...` line per user (pasteable), and `ACL RENDER OK users=<n> functions=<n> library=<digest>`.
- `acl check` reads the live ACL (`ACL GETUSER` per user, `ACL USERS`, and `ACL CAT` so the categories mean what that store says) and prints `ACL OK`, `ACL MISSING`, or `ACL DRIFT user=<u> role=<r>` with what apply would add (`keys+=`, `commands+=`) and remove (`keys-=`, `commands-=`, `channels-=`), the commands compared as the sets both sides expand to; `NOTE ACL EXTRA user=<u>` for a user no role renders (left as it is) and `NOTE ACL DEFAULT on=<b> nopass=<b>`; then `ACL CHECK OK` (exit 0) or `ACL CHECK DRIFT ... remedy=` (exit 1).
- `acl apply` makes the same comparison and sets each user that differs with `ACL SETUSER` (`ACL SET user=<u>`), then `ACL SAVE` when the store keeps an ACL file (`saved=acl-file`, else `saved=no-acl-file` and `NOTE ACL NOT SAVED: ...`, the users lasting until the store restarts): `ACL APPLY OK users=<n> set=<n> saved=<...>`. A store run by `nova-redis serve` keeps one, so the users apply sets survive a restart. A user keeps the password it has. A user the store lacks is created only with the password in the variable `--password-env-for <user>=<NAME>` names; without one the run is `ACL APPLY REFUSED ... missing=<users>` (exit 1) and writes nothing. `--dry-run` prints `ACL WOULD-SET` lines and writes nothing.

**The ACL file.** `serve` names `<store-dir>/users.acl` as the store's ACL file, so the users `acl apply` sets (and saves) are loaded again on a restart. Before each launch `serve` writes the file back with mode 0600: every line as the store saved it, and the default user on the store's password as its SHA-256 (`user default on sanitize-payload #<sha256> ~* &* +@all`), never the password itself, since redis-server ignores `requirepass` once an ACL file is named. `--users <u>[,<u>...]` names the users the file must hold: a file that lacks one is `SERVE REFUSED ... missing the users <u>` (exit 2), in `--dry-run` too, and nothing is written or started. The first run on a new store takes no `--users`; after `acl apply`, the unit that runs the store names them. `SERVE START` and the dry run's `SERVE OK` carry `aclfile=<path> users=<n>`, the users other than default the file held.

**Failures.** A failure of either verb is one `FAILED nova_sprint sha=<d> store=<a> err=<...> remedy="..."` line on stderr, and nothing on stdout. `err` says what was being done, why it failed, and what the store holds after it. `remedy` is the one next step for that cause, and its command carries the verb's login:
- A function name another library holds: `remedy` names that library and the function.
- `NOPERM`, or a login the store refused (`WRONGPASS`, `NOAUTH`): log in as a user that may run the commands.
- A library the store would not compile: fix the Lua that `err` names.
- No answer: check that the store is up and `--addr` is right, then `fn check`.

**Exit codes.** This is the convention for both verbs:

| exit | meaning |
|---|---|
| 0 | OK, LOADED, UNCHANGED or REPLACED |
| 1 | STALE or MISSING, or the store answered with a refusal (`NOPERM`, a library it would not take, a function name another library holds) |
| 2 | refused before the dial, no answer from the store (unreachable, or the wait ended), or a login the store refused |

The user needs `FUNCTION LIST` for `fn check`, and `FUNCTION LIST` and `FUNCTION LOAD` for `fn load`. A user with `~* &* +@all -@dangerous` has both. `-h` or `--help` after a verb or a verb group (`nova-redis fn -h`), or `nova-redis help <verb>`, prints that verb's usage, its effect and every flag with what it wants, on stdout at exit 0, before anything is dialled. `nova-redis help` prints the whole usage.

`fn load` replaces, so it belongs to the one place that deploys. Two deployers with different builds replace each other's library for as long as both run (`tla/RedisFn.tla`, `MCRedisFnTwoDeployers`). A tool on its way to an `FCALL` calls `redisfn.LoadMissing`, which never replaces a library (nova-tools #3620): nova-table does so on its first `Function not found` (see [nova-table](#nova-table)). The first run's refusals are in [TESTS.md](TESTS.md#nova-redis).

## nova-cairn

Keeps a session's words as local checkpoints: the exact words, their source
pointers and a bounded index. Publication intent is recorded, not carried out:
there is no transport, and every line says `published=false`. It stores the
caller's words; it does not summarize or consolidate memory.
See [SPEC-CAIRN.md](SPEC-CAIRN.md).

```sh
nova-cairn open --store ./checkpoints --session session-1 --publish never
nova-cairn append --store ./checkpoints --session session-1 --entry note-1 --text "the words to keep"
nova-cairn index --store ./checkpoints --max 20
nova-cairn receipt --store ./checkpoints --session session-1 --entry note-1 [--text]
```

The publication policy is named once, at `open` (`never`, `manual`, `deferred`
or `immediate`; these examples choose local-only `never`), and an `append` with
no `--publish` carries it; an `append --publish` names the entry's own. Successful
writes report `persisted=true` and `published=false` whatever the policy.

Reuse stable session and entry IDs for retries. The same ID and bytes are a
duplicate (`duplicate=true`, nothing written); different bytes under an existing
ID are a conflict at exit 1, and the line names the `receipt --text` that reads
what the ID holds. A re-`open` naming the recorded policy (and source, when it
names one) changes nothing; one naming another is a conflict at exit 1 that names
the `open` matching the record. A missing entry or session is refused at exit 2
naming the `index` that lists what is there. `open --dry-run` and `append
--dry-run` make every check the write would and write nothing: the line adds
`dry_run=true`, and a new entry says `persisted=false`.

Every line names the entry's `source=`. `open --source <ptr>` records the
session's pointer; an `append` with no `--source` carries that pointer, and an
`append --source` names the entry's own. `index` and `receipt` print what the
entry holds, and `source=-` is an entry with no pointer at all.
`receipt --text` also prints the stored words as a `text` fact, quoted with its
spaces kept (`text="the words to keep"`); JSON carries them as a plain string.

Two store shapes are read. The tool's own, the nested shape, is `sessions/<id>.md`
with `entries/` and `log.jsonl` beside it; it is what `open` creates. A **flat
store** keeps one markdown file per session directly under the store
(`cairns/<session>.md`, the shape notes appended by hand already have), an
interoperability mode read as it stands:

```sh
# cairns/b9395d11.md exists, written by hand; this lays down the fixture the tests use
mkdir -p cairns && cp internal/cairn/testdata/bench-b9395d11.md cairns/b9395d11.md
nova-cairn append --store ./cairns --session b9395d11 --entry beat-1405 --publish manual --text "the words to keep"
```

`open` on a flat record is a no-op (it never writes a second record under
`sessions/`, which would split one session in two), and `append` lands a dated
`## <stamp> — <entry>` section at the end of the file, one blank line between
sections, the words byte-for-byte under the heading. Nothing appears beside the
file: no `entries/`, no `log.jsonl`, no index. Retries and conflicts read that
section, so the same ID with the same words adds nothing and the same ID with
different words is still a conflict. `index` and `receipt` read flat records too:
a flat record's entries are its dated sections, its byte counts and `--text` are
the whitespace-trimmed section body, and since the format stores no source or
policy they print `source=-` and `publish=unknown` (an `append` with no
`--publish` says `publish=unknown` for the same reason). The coverage ledger
counts the file. An append addressing a session neither shape holds refuses with
the whole remedy verb: `open first: nova-cairn open --store <dir> --session <id>
--publish <policy>`.


## nova-decide

Typed decisions with probabilities, recorded so each one can be calibrated
against its outcome. The decide side asks a schema (named, typed questions)
over one state through a backend; the train side records every decision,
attaches its outcome when it is known, and reads the bar the record supports.
See [SPEC-NOVA-DECIDE.md](SPEC-NOVA-DECIDE.md).

### First run

From a checkout root; the fixed backend answers from a file, so these need no
network and no key (the transcript is in [TESTS.md](TESTS.md#nova-decide)):

```sh
nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first
nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op card-1
nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab
nova-decide attempt --brief ./cmd/nova-decide/testdata/card.md --result ./cmd/nova-decide/testdata/result.md --reason "verdict not-done: tests red in internal/decide" --backend fixed --answers ./cmd/nova-decide/testdata/attempt-answers.json --record ./decisions.jsonl --op c1@1
nova-decide grade --brief ./cmd/nova-decide/testdata/card.md --backend fixed --answers ./cmd/nova-decide/testdata/grade-answers.json --record ./decisions.jsonl --op c1@grade
nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate
nova-decide brief --card ./cmd/nova-decide/testdata/greet.md --backend fixed --answers ./cmd/nova-decide/testdata/brief-answers.json --record ./decisions.jsonl
nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"
nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok
nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --since 2026-10-01
```

`read` prints `READ OK id= decision=read backend= verdict= p= tokens_in=
tokens_out= recorded=new|existing` and one `READ ANSWER question= type= value=
p=` line per question: a noul's `p=yes:<p>`, a choice's `p=<option>:<p>,...`.
`score` is the read's five questions and one noul per escalation class of
landed work (`stranded_fragment`, `cut_citation`, `renamed_file_assumed`,
`ledger_ceiling`, `comment_contradicts_code`, `test_weakened`,
`record_made_claim`, `invented_reason`, `fenced_block_edit`,
`asserted_data_cut`, `load_bearing_word_cut`; `outside_paths` is
1 - `inside_paths`); its line names the `top=` class and its `p=`. `nova-sprint
land` asks it of every landed head as `<card>@landed@<head>`.
`attempt` (how a work take ended: `class=` done, nothing-to-do, wrong-scope,
no-result, needs-pro or provider-failure, over the brief, the child's RESULT.md and
the member's reason line) and `grade` (a card's convergence before its first deal:
`grade=` script, flash or pro, over the brief alone) print `ATTEMPT OK` and `GRADE
OK` lines the same way, the chosen option and its `p=` first; the sprint asks both
through the library (docs/SPEC-SPRINT.md sections 2 and 5).
`gate` prints `GATE OK op= decision=gate backend= failures= route=caused|flaky|pre-existing`
and one `GATE FAILURE key=<pkg>.<Test> id=<op>/<key> class= p= route= recorded=` line
per failing test of the go test output, each one decision; a build failure is
`route=caused recorded=unasked`. `gate --dry-run` asks nothing and writes nothing:
each `GATE FAILURE` says `recorded=existing` (with its `class=`) for a decision the
record holds already, `no` for one the run would ask, `unasked` for a build failure.
`brief` reads cards as a flash child with no memory would, before they are added
(a file, or a directory's `*.md` files as `nova-sprint add --brief-dir` reads them,
as one batch), an uncalibrated rank: one `BRIEF CARD id=
op=<card>@brief-<hex> p_converges= minutes= failed= uncalibrated=true recorded=` line per card,
`failed` naming each question the card leaves open (`commit_stated(0.20)`,
`ambiguous_step:step-2(0.70)`, or `-`). `nova-sprint add` asks the same of every
card under `JEV_API_KEY` (no bar is set while the decision is uncalibrated);
`nova-swarm lint --card <file> --decide` prints it for one file.
`calibrate` prints the AUC, one `BAR` line per `--bars` value (positives caught,
negatives bounced) and the `CATCH-ALL` bar, the highest that flags every positive; a label of
classes joined by `+` (`stranded_fragment+invented_reason`) counts for each of
them. `findings` prints one `FINDING class= count= cards=` line per class the
score decisions since `--since` give a p at or above `--bar`, most cards first;
`unnamed` is p(defect) at the bar with no class there. What a first run gets
wrong:

- `--backend jev` with no key: `JEV_API_KEY is absent from this environment`, with the stable code `reason=key_absent` on the refusal line (and `"reasons"` in the JSON result).
  The key reaches the tool only through `nova-secrets exec --only JEV_API_KEY --
  nova-decide ...`; it is never a flag or a file.
- `--backend fixed` with no `--answers`: the fixed backend answers from a file.
- `calibrate` over a record with no positive or no negative outcome refuses:
  a bar is read from both; so does an option no decision names
  (`--question verdict=BOUNCEE`).
- `gate` over output with no `--- FAIL:` or `FAIL <pkg>` line refuses: it reads a
  red go test run; `--bars` is `<flaky>,<pre-existing>`, each a probability or empty,
  two set ones summing above 1. With no `--bars` (as the sprint row's defaults) every
  failure is recorded with its class and routed `caused`; `--bars 0.8,0.8` is the
  starting point.
- The same `--op` over another card, diff or schema refuses; over the same inputs it
  returns the recorded decision and asks nothing, so a long run resumes.


## nova-local

Run local models: what an engine has, one model served at a chosen context,
and a worker description nova-swarm accepts. It runs no inference, fetches no
weights and judges no model.
See [SPEC-LOCAL.md](SPEC-LOCAL.md).

### First run

From a checkout root, with the ollama daemon running and `gemma4:12b` pulled
(`ollama pull gemma4:12b`); the transcript, run against a fake engine whose
weights are in the shared store, is in [TESTS.md](TESTS.md#nova-local):

```sh
nova-local status
nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7
nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
```

`status` prints `STATUS OK engines= answering= loaded= models=` and the box
(`mem_used= mem_free= mem_total=` in bytes, `wired_cap=`, `load1=`), then one
`STATUS ENGINE name= state=up|down|timeout base=` line per engine with
`loaded= advertised=`, `num_ctx=` of a loaded model, and `store=` (where its
weights are read, symlinks resolved) and `shared=yes|no|unknown`: yes when the
store is under the AI root's `shared/models` (`$NOVA_AI_ROOT`, else `~/ai`).
`--list` adds one `STATUS MODEL engine= model= digest= weights= loaded=` line
per model. `serve` makes `<name>-<ctx>k` (here `gemma4-32k`) with the context,
temperature 0 and the seed baked in, sends one warm-up, and prints `serve_as=`,
the name a harness calls, and `load=`, the warm-up it timed. `worker` writes the
one JSON file `nova-swarm` reads as a worker description. What a first run gets
wrong:

- No daemon: `status` exits 1 with `run: ollama serve`, the one command that
  starts it; `serve` names `ollama pull <ref>` for a model the engine lacks.
- No `--num-ctx`: refused, because ollama's own default silently truncates a
  long prompt; it is a multiple of 1024, since the tag's name carries it.
- A tag that exists with another parent, context, temperature or seed: exit 1
  naming both values and `ollama rm <tag>`.
- `--base` naming any host but loopback or a tailnet address (100.64.0.0/10),
  or a name that resolves elsewhere: refused, because a local tier pointed at a
  remote endpoint is not a local tier.
- `worker` with a relative `--worker-dir`, an empty `--key-file`, harness
  arguments without `{model}`: every problem named at once. The local engine
  wants no key; nova-swarm wants a non-empty file, so `printf 'local\n'` is the
  whole of it.


## nova-table

Work tables over Redis: ordered-set cells, text notes, percentage formulas,
batch writes and stored live views. Every table mutation checks its observed
epoch and writes a change receipt. The guide and disposable local setup are in
[docs/nova-table/README.md](nova-table/README.md); the library is
`internal/ntable`.

### First run

A table is columns, rows and a set per cell. Make one, put a row in it, put
members in a cell, move one to the next cell, and look at it two ways: the
typed lines a program reads, and the text a person reads. These commands assume
a configured store that holds this build's function library. On a store that
holds none, the first verb loads it (first contact, never replacing a library
the store holds) and its `trips=` counts the load; for a fresh local Redis,
follow [Start locally](nova-table/README.md#start-locally), or the `first run:`
lines of `nova-table help`, which start a throwaway one. With no store at all,
every verb that writes runs under `--dry-run`: it makes every check the real
run makes before sending and prints the command it would send, dialling nothing
(`nova-table create demo --columns ready,working,done --dry-run` prints
`TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0`).
A verb that finds no store at its address refuses at exit 2 naming the address,
what came back, and the command that starts a throwaway store.

```text
$ nova-table create demo --columns ready,working,done
TABLE CREATE table=demo columns=3 trips=1

$ nova-table row add demo build
TABLE ROW ADD table=demo row=build cols=3 bound=0 trips=1

$ nova-table cell add demo build ready b1
TABLE CELL table=demo row=build col=ready n=1 trips=1

$ nova-table cell add demo build ready b2
TABLE CELL table=demo row=build col=ready n=2 trips=1

$ nova-table cell move demo build ready working b1
TABLE MOVE table=demo row=build member=b1 from=ready to=working n=1 trips=1

$ nova-table show demo
TABLE table=demo columns=3 rows=1 trips=1 epoch=0 revision=5
TABLE ROW table=demo row=build ready=1 working=1 done=0

$ nova-table render demo
demo  | ready | working | done
------+-------+---------+-----
build |     1 |       1 |    0
------+-------+---------+-----
      |     1 |       1 |    0
```

### Find a command

`nova-table help` lists every verb. `help <verb> [subverb]` and `--help` at each
level show the same syntax and examples, exit 0 on stdout. Product flags come
first, connection flags next, epoch and receipt metadata last. For example,
`nova-table help row set` and `nova-table row set --help` describe the text edit.

### Commands

| Command | What it does |
| --- | --- |
| `create <table> --columns <spec>` | Creates a definition; repeated identical creates are accepted; another shape points to `set --columns` |
| `set <table>` | Edits footer, columns, visibility or name; see `help set` |
| `drop <table> [--definition]` | Removes active rows/owned cells; `--definition` also removes the saved column definition and the table's identity hash and the rows of every epoch (it also repairs a store left with the identity alone); the definition snapshots of earlier epochs stay |
| `list` | Lists active tables with row and column counts |
| `row add <table> <row>...` | Adds one or many rows; optional label, exclusion, owner and bound cells |
| `row set <table> <row> <col>=<value>...` | Writes text values; `col=` clears one |
| `row hide/show <table> <row>...` | Changes visibility; data and fold contributions stay |
| `row del <table> <row>` | Deletes the row and its owned cells; a missing row succeeds with `existed=0` and a no-op receipt; external bound sets stay |
| `row move <table> <row> --first/--last/--before/--after` | Moves one row; the others retain their relative order |
| `row order <table> <row>...` | Puts the named rows first; the rest keep their order |
| `row sort <table> [--by name/label/<col>] [--desc] [--keep]` | Sorts once; `--keep` maintains name/label order, `--manual` ends it |
| `col add <table> <spec> [--first/--last/--before/--after]` | Adds one column, last unless a place is named |
| `col del <table> <col>` | Removes an empty column no formula (`pct(...)`, `sum(...)`) reads |
| `col move <table> <col> --first/--last/--before/--after` | Moves one column |
| `cell add/remove <table> <row> <col> <member>...` | Adds or removes a batch; add takes `--score` |
| `cell move <table> <row> <from> <to> <member>...` | Moves a batch atomically while preserving scores |
| `cell members <table> <row> <col>` | Lists member IDs and scores in order |
| `member create <table> <id>` | Allocates an unplaced identity |
| `member find <table> <id>` | Reports its owned location or `state=missing/unplaced`, with epoch and `table_revision` |
| `member read <table> <id>... \| <table> --cell <row:col>` | Reads members' place, score, revision and fields in one exchange; names the missing |
| `batch (<manifest-file> \| - \| '<json>')` | Applies an atomic batch manifest (file, stdin or inline JSON) of member mutations and preconditions |
| `check <table>` | Audits both directions of all record/set links, including hidden cells |
| `clear <table>` | Removes active rows and owned cells, retaining the definition; refuses bound cells |
| `show <table> [--at-epoch <n>]` | Prints complete projected values as typed lines, including text and percentages, then one `TABLE PROP table= <name>=<value>` line for each of the table's properties (values a batch manifest writes with its members, such as a rolling index), in name order; a cell that cannot be read prints `?`, and a warning line names its key and type and `show` exits 1 |
| `render <table>` | Prints a text table; an empty table prints its header and footer |
| `render --view <name>` | Prints one stored-view frame with timestamp, title and optional summary |
| `watch <table>[,<table>...]` | Redraws tables; `--once` renders once, `--out` publishes a file atomically |
| `view set <name> --tables <a,b,...> [--title <text>] [--summary <count-column>]` | Stores a view, replacing its title and summary together; summary uses the first table |
| `view state <name> (<text> \| --clear)` | Sets the view's state: while set, the summary line is that text alone, in place of the counts; `--clear` shows the counts again |
| `view show <name>` | Prints view configuration, including summary and state |
| `view list` | Lists view names |
| `view del <name>` | Deletes the view configuration, preserving tables |
| `watch --view <name>` | Reloads configuration each frame; edits appear without restarting |
| `shell` | Reads commands on one resident connection; write receipts print by default |
| `version` | Prints the build version |

**Order.** Rows draw in the order they were added and columns in the
order they were declared, until a verb moves them; each of these is one
exchange, checked whole before the first write, with one receipt. `row move
<table> <row> --first | --last | --before <row> | --after <row>` prints
`TABLE ROW MOVE table= row= place= [of=]` and moves that row only. `row
order <table> <row> <row> ...` prints `TABLE ROW ORDER table= first=`: the
named rows first, in the order named, the rest after them in the order they
had. `row sort <table> [--by name|label|<col>] [--desc]` prints `TABLE ROW
SORT table= by= desc= keep=` and sorts once, by the row's name (the
default), its label, a count column or a text column, ties by name;
`--keep` (name or label) makes the sort stand, so every row added or rebound later
takes its place, and `row move` and `row order` are refused, exit 1, naming
`row sort <table> --manual`, which ends it and leaves the rows where they
are. `col move <table> <col>` takes the same four places and prints `TABLE
COL MOVE table= col= place= [of=]`. `col add <table>
<name[:projection[:fold[:label]]]>` adds one column, last or at a place:
`TABLE COL ADD table= col= place=`. `col del <table> <col>` prints `TABLE
COL DEL table= col=` and is refused, exit 1, writing nothing, while the
column holds a member (the refusal names all blocking rows and members, with a
batch `cell remove` command for each occupied cell) or a text value, while a formula column (`pct(...)` or `sum(...)`) reads it, and when it is
the last column. `col add` of a formula over a column the table does not have is
refused with the `col add` of that column as the remedy; over a column that is not
a count, with `show` as the remedy. Quote a column that has parentheses, `'share:pct(busy)'`.

### Batch mutation

`nova-table batch (<manifest-file> | - | '<json>') [--redis <addr> | --seat <name>] [--epoch <n>] [--actor <name>] [--receipt=true|false] [--json]`
applies an atomic batch manifest of member mutations and preconditions against one table in a single Redis
call. The manifest is a file path, `-` for stdin, or inline JSON that starts with `{`; a path that cannot be
read is refused (exit 2) with the path and the operating system's error. It creates, moves, removes, and sets
or unsets permitted member fields in one call, checking the observed table revision, the epoch and every
member expectation against one pre-state before any write.

The manifest is a JSON object with `schema` (the integer 1), `table`, `epoch` and `expected_table_revision`
(decimal strings), `operation_id`, an optional `actor`, and `members`: an array with one entry per member.
Each entry has an `id`, an `expect` guard (`absent`, or any of `revision`, `place` and `fields`), and zero or
more mutations (`create`, `move`, `remove`, `set`, `unset`); `create` needs a `score`. The manifest states its
epoch: `--epoch <n>` given on the command must equal it, and a difference is refused, naming both, before the
store is asked. `--actor <name>` given on the command must equal the manifest's actor when it names one, and
fills it when it names none. The bounds are in [SPEC-NOVA-TABLE.md](SPEC-NOVA-TABLE.md); a manifest over one
is refused by name with the bound and the count found.

From an empty store, the commands and what the last one prints:

```sh
nova-table create demo --columns ready,working,done
nova-table row add demo build
cat > seed.json <<'EOF'
{
  "schema": 1,
  "table": "demo",
  "epoch": "0",
  "expected_table_revision": "2",
  "operation_id": "seed",
  "actor": "coordinator",
  "members": [
    {
      "id": "m1",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "ready", "score": 1},
      "set": {"role": "builder"}
    }
  ]
}
EOF
nova-table batch seed.json
```

```text
TABLE BATCH table=demo operation=seed epoch=0 table_revision=2->3 outcome=changed selected=1 guards=0 changed=1 replay=no trips=1
TABLE RECEIPT event=1727570000000-0 epoch=0 before=2 after=3 outcome=changed
MEMBER m1 place=-->build:ready score=-->1 member_revision=0->1 fields={"role":[null,"builder"]}
```

```sh
cat > manifest.json <<'EOF'
{
  "schema": 1,
  "table": "demo",
  "epoch": "0",
  "expected_table_revision": "3",
  "operation_id": "op-42",
  "actor": "coordinator",
  "members": [
    {
      "id": "m1",
      "expect": {"revision": "1", "place": {"row": "build", "col": "ready"}},
      "move": {"row": "build", "col": "working"}
    }
  ]
}
EOF
nova-table batch manifest.json
```

```text
TABLE BATCH table=demo operation=op-42 epoch=0 table_revision=3->4 outcome=changed selected=1 guards=0 changed=1 replay=no trips=1
TABLE RECEIPT event=1727570000000-0 epoch=0 before=3 after=4 outcome=changed
MEMBER m1 place=build:ready->build:working score=1->1 member_revision=1->2 fields={}
```

The summary line carries the table revision before and after (`table_revision=<before>-><after>`), the outcome,
the selected, guard-only and changed entry counts, `replay=yes` when the receipt is the one recorded for an
operation already applied, and the trips; then the commit receipt; then one `MEMBER` line per member with its
place, score and `member_revision` before and after and its changed application fields as one JSON object of
`[before, after]` pairs (`null` is absent; `-` is an unplaced member or an absent score). A value of more than
64 bytes is not printed: it is `{"bytes":<length>,"sha1":"<digest>"}`, in a before-value as in an after-value,
and a field whose two sides are both such values is always listed, its two digests side by side, because a
digest identifies a value and does not prove two values equal. A score is the exact decimal string the store
holds. A batch whose receipt would exceed 1 MiB (`receipt bytes`, the manifest bound) is refused with
`code=LIMIT` and `changed=no`, naming the bound and the computed size; change fewer members or fields in one
manifest. `--receipt=false` suppresses the `TABLE RECEIPT` line. A request that changes
nothing prints `outcome=noop` and, like any accepted batch, advances the table revision by one. Running the same
manifest again applies nothing and prints the original receipt with `replay=yes`. The command checks a manifest against the
current rules before it sends it, so it replays only a request the current rules accept; a refusal made before
sending says that, says this call changed nothing and says nothing about an earlier call with the same operation id.

`--json` prints the receipt as one line of JSON for a program: `table`, `operation_id`, `epoch`,
`table_revision` (`{"before", "after"}`), `outcome`, `selected`, `guards`, `changed`, `event`, `replay` (a
boolean), `trips`, and `members` (`id`, `place`, `score`, `member_revision` as `{"before", "after"}` pairs,
`null` for none, and `fields`, name to `[before, after]`). Revisions and scores are decimal strings.

Exit codes: 0 on success (including the replay of an identical request, which returns the original receipt
and writes nothing); 1 on refusal, which prints the operation, the member at fault, the state expected
against the state found, `code=<CODE>`, `changed=no` and a next command, and leaves the store unchanged: it
covers a manifest that reads as one and breaks a rule (a bound, a repeated member id, a field both set and
unset, a reserved field, a create with a move, an absent with a revision, an empty `members` array) as well as
the store's own refusals; 2 on usage or connection: a manifest that cannot be parsed or read (named by its
place, `score must be a JSON number, found a string at members[0].create.score`), an `--epoch` or `--actor`
that differs from the manifest, and a store that could not be reached or did not answer. When the store did not
confirm a batch the message says `changed=unknown`: run the same manifest again with the same operation id; it
returns the original receipt if the batch was applied and applies it if it was not. `nova-table batch -h` prints the usage banner, including the stdin form
`nova-table batch - < manifest.json`, to stdout at exit 0 with empty stderr.

An epoch behind the active one is refused as stale and one ahead of it as `EPOCHAHEAD`, naming the requested and the active epoch; `nova-table show <table>` prints the active epoch. A table that does not exist is refused as missing whatever the epoch.

### Reading members

`nova-table member read <table> <id>... | <table> --cell <row:col>... [--at-epoch <n>] [--json]` reads members
in one exchange from one consistent snapshot: their place, score, revision and fields, the members that do not
exist, and the table's revision and epoch. It is what a manifest's guards are prepared from.

```sh
nova-table member read demo m1 m2
```

```text
TABLE READ table=demo epoch=0 table_revision=4 members=1 missing=1 trips=1
MEMBER m1 place=build:working score=1 member_revision=2 fields={"role":"builder"}
MISSING m2
```

`place=-` and `score=-` are an unplaced member. `--cell <row:col>` (repeatable) reads every member of a cell
instead of naming ids. `--json` prints one object: `table`, `epoch`, `table_revision`, `members` (`id`,
`place`, `score`, `member_revision`, `fields`), `missing` and `trips`. `member find <table> <id>` reports only
where a member is; its `table_revision` is the table's counter, not the member's.


### Resident shell

`nova-table shell [--redis <addr> | --seat <name>] [--epoch <n>] [--keep-going]
[--receipt=false]` reads commands from stdin on one connection. Enter verbs
such as `row add demo build`, optionally prefixed with `nova-table`. Quotes,
escapes, blank lines and `#` comments are supported; values are never expanded
or executed by an OS shell. `help <verb>` works inside the session. `quit`,
`exit` or EOF ends it. Lines execute in order and commit independently; use the
existing member/row batch verbs for several changes in one exchange.

Write receipts print by default. The session's `--epoch`, `--actor`, `--fence`
and `--idem` become defaults; per-command overrides affect that command only.
The store and seat stay fixed. File input stops on the first error unless
`--keep-going`; a terminal prompts on stderr and defaults to continuing. The
final status retains errors: 1 for a store refusal, 2 for a usage/input/connection
error. Continuous `watch` returns to the prompt on Ctrl-C; scripts can use
`watch --once`. On Unix, SIGTERM ends the whole shell (143), including during
watch, and Ctrl-C at the prompt ends it (130). An in-flight write may already
have committed. A failed connection is replaced before the next store command;
the failed command is never replayed. Lines may contain exactly 1,048,576 bytes
excluding LF/CRLF; `--keep-going` discards an overlong line and continues at the
next newline. Failures name their input line. See the
[resident shell example](nova-table/README.md#resident-shell).

### Columns, identity and output

`--columns` is `name[:projection[:fold[:label]]]`, comma-separated. The projections
are `count` (default), `members`, `first`, `last`, `text`, `pct(<count-column>)`,
`pct(<count-column>/<a>+<b>)` and `sum(<a>+<b>)`. Text is blank until `row set` writes it; the row label
always has a separate leading cell. Quote specs containing parentheses, for
example `'ready,done,note:text,progress:pct(done)'`, so zsh passes them unchanged.

Folds are `sum` (count and sum default), `max`, `avg` (count and sum only), `union` (members),
`pooled` (percentage default), or `none`. `pct(<col>)` divides its named count by
all count columns of the row; `pct(<col>/<a>+<b>)` divides it by the named count
columns `a`, `b` of the row; `sum(<a>+<b>)` adds the named count columns of the row.
Every column a formula names is a count column of the table, hidden or not; any
other is refused at `create`, `set --columns` and `col add`. For example
`'ok,failed,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%'`. Pooled footers
divide the summed numerators by the summed denominators, not the row
percentages. Known-empty percentages are `0.0%`; an unread dependency is `?`.
`--footer <label>` names the otherwise blank footer label. `--width col=n,...`
sets column widths; render/watch also accept `--label-width`.
Hidden rows and columns continue contributing to formulas and folds.

One member has one owned placement per table. A duplicate add names its current
place; use `cell move` to move it. An occupied column or nonempty text cannot be
removed by a shape edit: move/remove its members, or clear the text first. A row
may bind a cell to another tool's set using `<col>=<key> --owner <verb>`; reads
are allowed, writes refuse naming the owner. Bound sets do not own table member
placements. `member find` reads the active epoch; `check` scans the full namespace.

Writes accept `--epoch` (default 0), `--actor`, `--fence`, `--idem`, and `--receipt`.
Stale epochs refuse; metadata is recorded, not authorization or deduplication.
`create` also accepts `--epoch-key`, `--epoch-field`, and `--member-prefix`.
Accepted table writes each produce one revision and one durable receipt,
including accepted no-ops. View configuration is separate from table receipts.

Success lines report `trips=1` after connection setup. A stored view frame uses
two exchanges, one for configuration and one for all table snapshots. `show`
includes every column's projected value (`note=""`, `progress=50.0%`, `?` for
unread), all hidden rows/columns, and the snapshot's epoch/revision. `render`
prints the table alone. A stored view adds timestamp, title and optional pooled
summary from the same snapshot. ETA has no value until rate sampling is added.
`view show` is configuration; `render --view <name>` prints the rendered view,
as does `watch --view <name> --once`. Stored views use active epochs;
`--at-epoch` applies only to table targets.

### Connection and exit codes

Store commands accept `--redis <host:port>` or an absolute Unix socket path;
otherwise they use `NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR`, then the seat address.
Select a seat with `--seat`, `NOVA_SPRINT_SEAT` or `NOVA_SEAT`. Without a seat,
`NOVA_SPRINT_REDIS_USER` names the user and `NOVA_SPRINT_REDIS_PASSWORD_ENV` names
the password variable. Never put a password on the command line. Flags may
follow positional words; `--` ends flag parsing for literal members such as
`--pending`. Unknown flags list the actual command's available flags and name
its help page. See the [local setup](nova-table/README.md#start-locally)
for an isolated store and the function-library loading command.

Exit codes: 0 done (including requested help), 1 refused by the store, 2 usage or
connection failure. A refusal gives the commands needed to proceed. In watch, a failed read leaves
the last good frame and one `store unreachable since <time>` line until recovery (a frame that reads fine carries no age line); Ctrl-C exits 0.

## nova-card

nova-card is pre-alpha: not ready for production use.

```
nova-card generate --from ledger --ledger <name> --repo-dir <dir> --out <dir> [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--base <branch>] [--repo <owner/name>] [--name <n>...] [--dropped <id>...] [--dry-run]
nova-card generate --from findings --file <tsv> --out <dir> (--repo-dir <dir> | --repo <owner/name> --base <branch> --sha <40hex>) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
nova-card generate --from help --tool <name> [--tool <name>...] --out <dir> [--bin-dir <dir>] (--repo-dir <dir> | --repo --base --sha) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
nova-card lint --card <file> [--card <file>...] [--name <n>...] [--dropped <id>...]
nova-card template
nova-card version
nova-card help [<verb>]
```

A card a model writes by hand takes it half an hour and comes back with guessed
PATHS; one wrong PATHS line was rejected 262 times in one night. nova-card
writes the cards from the source the work comes from, with the PATHS computed,
the lint already green, and the waves already laid out, so the one thing left
to do is `nova-sprint add --stream <s> --brief-dir <dir>`.

The flow is three lines:

```sh
nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards
nova-sprint add --stream debt --brief-dir ./cards --allow-shared-paths
nova-sprint where
```

### First run

The included findings file is two files' worth of a reader's findings. No
checkout is needed when `--repo`, `--base` and `--sha` are given; with
`--repo-dir` the three are read off the checkout and every PATHS entry is
checked to exist in it:

```sh
nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards
nova-card lint --card ./cards/finding-internal-bus-send.md
nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
```

`./cards` then holds one `.md` per card, its name the card's id, and a
`manifest.tsv` (id, file, test, wave, deps). [TESTS.md](TESTS.md#nova-card)
carries the transcript; `cmd/nova-card/firstrun_test.go` runs it.

### Sources

`--from ledger --ledger <name>` reads one of internal/ci's ratchet ledgers from
the checkout: `serial-tests`, `slowwaits`, `sleeps-skips`, `fixed-waits`
(flash), `dead-code`, `namedpaths`, `transcripts`, `generality-fixtures` (pro).
One card per file the rows name; the card's PATHS are computed from its START
line, never typed: every directory a START file lives in, as its Go files and its
tests (`<dir>/*.go`, `<dir>/*_test.go`), and the docs the card names (a ledger
card: the row's file's package, its test's package and the ledger; a findings
card: the file's package and its test's package; a help card: `cmd/<tool>` and
`docs/CLI.md`); its TEST is the class test that holds the ledger;
its task is the ledger's template with the rows substituted, and it says to
write the draft early and commit before any probe. `--from findings --file
<tsv>` reads `file:line`, finding, remedy, test columns (a header row is
skipped); one card per file, the first finding's test as TEST, a card with no
test named given the one it must write. `--from help --tool <name>` runs
`<name> help` and writes one card per tool: the lines over 100 characters,
the undefined terms and the examples that do not run as printed.

### Waves and dependencies

Cards of one ordinary ledger delete adjacent lines of one file and would
conflict at land, so odd cards are wave 1 and even cards wave 2, each wave 2
card depending on its wave 1 neighbours (`DEPENDS-ON`). A generated ledger
(SPEC-SPRINT.md section 7, the generality family) is regenerated by `land`, so
its cards are one wave with no dependency. Wave 1 cards of one ledger share its
path and neither needs the other, so the add wants `--allow-shared-paths`; the
`CARDS OK` line says `shared-paths=yes` when it does.

### What it refuses

Every brief is held to the lint `nova-sprint add` runs (the model lines, the
child rules under the default rule set, a tree card's steps), and past the add
to the typed header and the template's unfilled `<...>` lines, which the add
does not read, before anything is written (a sprint initialised with `--rules`
holds a brief to that file at the add), and to the card checks the add runs
too: a tier on line 1, a TEST whose package PATHS names, no name `--name` gives
outside double-quoted words, no card `--dropped` gives; one red brief prints its
`LINT DRIFT card=<id> check=<check> line=<n>: <excerpt>` line and nothing is
written, exit 1. A PATHS entry that names nothing in `--repo-dir` is the same
refusal. An `--out` that already holds a brief is refused, exit 2. `--dry-run`
plans and lints, prints the manifest and the `CARDS OK` line with
`dry-run=yes`, and writes nothing.

### Exit codes

0 done; 1 a brief is red and nothing was written; 2 could not run.

## nova-work

nova-work is pre-alpha: not ready for production use.

Every issue of every repository of a GitHub organization in one tree file, with
each issue's full contents, and a check that the file holds exactly what GitHub
holds. The design is [SPEC-WORK-V1.md](SPEC-WORK-V1.md); this section is how to
use it. It reads GitHub only (no GitLab, no Gitea), never writes to GitHub (the
seam refuses any GraphQL document that is not a query), and captures the fields
SPEC-WORK-V1 section 1.3 lists, not reactions or other timeline events.

**Try it with no login.** `nova-work verify -h` prints the tree's grammar and a
minimal tree. Save that tree as `a.lisp`, copy it to `b.lisp` with
`:archived true`, and run `nova-work verify --tree a.lisp --against b.lisp`: one
`VERIFY DRIFT ... field=archived` line under `VERIFY FAIL`, exit 1. The same
file against itself is `VERIFY OK ... differences=0`, exit 0.

**First run against GitHub.** Needs `gh auth status` to pass and one repository
you can read: export `ORG` and `REPO`, then run the three lines of the banner's
`example:` block in a scratch directory (a dry run, the import to
`./tree.lisp`, the verify). The executed transcript of that sitting, with every
line of output, is in [TESTS.md](TESTS.md#nova-work).

**Output.** One result per run: `IMPORT OK`, `VERIFY OK`, `VERIFY FAIL` (exit 1,
the differences as `VERIFY MISSING`, `EXTRA` or `DRIFT` lines, values quoted),
or `<VERB> REFUSED: <why>; run: <next command>` (exit 2). `--json` prints the
same result as one JSON object.

### import

Reads every issue of `--org` (or of each `--repo`) through your `gh` login,
read-only, and writes one local tree file, `--out`, only after the encoded tree
has read back equal to what was fetched. `--dry-run` is not offline: it reads
GitHub exactly as the import does (every issue, the same calls), checks the
round trip, and writes nothing. A dry run costs what the import costs:
`IMPORT PLAN` names `est_calls`, and `--max-calls` (default 1500) refuses a plan
past it before any issue is read.

### verify

Reads the tree and GitHub again and writes nothing: zero differences is
`VERIFY OK ... differences=0`, the receipt that the tree holds what GitHub
holds. `--against <tree>` puts a second tree file where GitHub stands and reads
no network at all.
