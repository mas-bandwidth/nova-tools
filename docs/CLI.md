# Command reference

[Back to Nova Tools](../README.md)

Command reference and worked examples. Run shell examples from the repository root unless a section says otherwise. `-h` or `--help` after any verb prints that verb's help (its usage lines and every flag it takes) on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question; `<tool> help` is the whole banner. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line, so what is shown here is what the tool does today.

### tdocs-cli-generated-rb-b.w8

The reference blocks between the `clidoc` markers are generated, not hand-kept: `make clidoc` runs each marked tool's `help` and each verb's `-h` from the built binaries and rewrites only the block between that tool's markers, so a flag a verb takes is a flag the reference names. The prose and the worked examples outside the markers stay hand-written. The test in internal/docs builds the tools and fails, naming `make clidoc`, when a block drifts from what the binaries print.

### tdocs-cli-generated-rb-r3c

Every `## nova-*` section now has a generated reference block, including nova-doctor and nova-up. The four per-verb flag checks remain alongside the generated check.

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
  --allow-empty  answer OK when the read finds no file, instead of FAILED over nothing
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
  --allow-empty  answer OK when the read finds no file, instead of FAILED over nothing
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

<!-- clidoc:begin nova-bus -->
`nova-bus help`:

```
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks; an unproven push is a NOTE, not a refusal; --require-push refuses.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis at --redis (else NOVA_BUS_REDIS, else fleet:bus); loopback/tailnet only.

usage:
  nova-bus wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--wake-after <cursor>] [--redis <addr>]
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--timeout <duration>] [--redis <addr>]
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus ack [--as <me>] --id <id,...> [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus receipts [--as <me>] [--id <id,...>] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus log [--bodies] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus names [--timeout <duration>] [--redis <addr>]
  nova-bus version
  nova-bus help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
```

`nova-bus wait -h`:

```
usage: nova-bus wait [flags]
from `nova-bus help`:
  nova-bus wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--wake-after <cursor>] [--redis <addr>]
  nova-bus wait --as bob --timeout 1s
flags:
  --after <string>  the stream entry id <ms>-<seq> to wait past; default: the stream's last id read once at start, as WAIT ARMED prints it
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --skip-subject <string>  subjects starting with one of these prefixes, comma-separated, are skipped; matched without case
  --timeout <duration>  how long to wait before WAIT NONE, a Go duration (1s, 2m); 0 is for ever
  --wake-after <string>  0 replays the file from byte zero; otherwise the complete wake-after cursor returned by wait; requires --wake-file; preserves unread bytes across rearm and restart
  --wake-file <string>  a file whose lines, appended after the start, also end the wait (one line per message)
exit codes: 0 the wait ended: WAIT OK, entries that counted, or WAIT WAKE, a line on the wake file; 1 WAIT NONE, the timeout ran out; 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus send -h`:

```
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
flags:
  --as <string>  your name, the sender: the login user when there is one (then it may be left out)
  --body <string>  the message's text (or --stdin; at most 1 MiB)
  --cc <string>  more recipients, comma-separated names; each gets the message as well
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --kind <string>  the kind of message, one of report, ack, status, request, blocker: what a reader filters on
  --re <string>  the id of the message this one answers
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --require-push  refuse a name with no proven push (deaf: ...) instead of noting it; also NOVA_BUS_REQUIRE_PUSH=1
  --stdin  read the message's text from stdin
  --subject <string>  one line saying what the message is (required)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
  --to <string>  the recipients, comma-separated names (required)
  --token <string>  your word for this one send, the same on every retry of it (letters, digits, . _ : -; at most 128 bytes)
  --token-cleanup <duration>  when the store drops the token (never before its life ends)
  --token-life <duration>  how long a retry under --token answers the first send
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: one entry on every recipient's stream and the log, in one transaction
```

`nova-bus peek -h`:

```
usage: nova-bus peek [flags]
from `nova-bus help`:
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--timeout <duration>] [--redis <addr>]
  nova-bus peek --as bob
flags:
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --json  print the result as one JSON object instead of lines
  --kind <string>  only these kinds, comma-separated, of report, ack, status, request, blocker (default: every kind)
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus recv -h`:

```
usage: nova-bus recv [flags]
from `nova-bus help`:
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus recv --as bob --exec true
flags:
  --ack  ack each message after printing it (a plain recv leaves it pending)
  --all  take every message waiting, in order, each its own result
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --dry-run  print what the verb would write and write nothing
  --exec <string>  a shell command run with each message on its stdin; exit 0 acks the message
  --forever  loop over every message, delivering each with --exec, until a signal
  --json  print the result as one JSON object instead of lines
  --kind <string>  only these kinds, comma-separated, of report, ack, status, request, blocker (default: every kind); others are left for the next reader
  --max <int>  how many messages to take, in order, each its own result; 1 is one message
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --require-push  refuse a name with no proven push (deaf: ...) instead of noting it; also NOVA_BUS_REQUIRE_PUSH=1
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: moves one message to pending; with --exec it runs the command and acks on exit 0
```

`nova-bus ack -h`:

```
usage: nova-bus ack [flags]
from `nova-bus help`:
  nova-bus ack [--as <me>] --id <id,...> [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
flags:
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --dry-run  print what the verb would write and write nothing
  --id <string>  the message ids, comma-separated, as recv printed them (required)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: acks the messages on your stream
```

`nova-bus receipts -h`:

```
usage: nova-bus receipts [flags]
from `nova-bus help`:
  nova-bus receipts [--as <me>] [--id <id,...>] [--max <n>] [--timeout <duration>] [--redis <addr>]
flags:
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --id <string>  the message ids, comma-separated, as recv printed them (default: every receipt you hold)
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus overdue -h`:

```
usage: nova-bus overdue [flags]
from `nova-bus help`:
  nova-bus overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]
flags:
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --older <duration>  how long a message may wait short of delivered before it is overdue, a Go duration (10m, 1h)
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 OVERDUE OK, nothing short of delivered past --older; 1 BUS OVERDUE, one or more; 2 could not run (a flag, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus log -h`:

```
usage: nova-bus log [flags]
from `nova-bus help`:
  nova-bus log [--bodies] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus log --max 5
flags:
  --bodies  print each message's body as well
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus names -h`:

```
usage: nova-bus names [flags]
from `nova-bus help`:
  nova-bus names [--timeout <duration>] [--redis <addr>]
  nova-bus names
flags:
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else NOVA_SPRINT_REDIS, else fleet:bus)
  --timeout <duration>  how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and 10s more
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```

`nova-bus version -h`:

```
usage: nova-bus version [flags]
from `nova-bus help`:
  nova-bus version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-bus -->

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
  nova-friend status --as <me> --dir <d> [--state-dir <d>] | status --all
  nova-friend refuse-go --name go|gofmt
  nova-friend resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]
  nova-friend serve --as <coordinator> [--redis <addr>] [--dry-run]
  nova-friend version
  nova-friend help [<verb>]

Every verb but run, serve takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
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
  nova-friend status --as <me> --dir <d> [--state-dir <d>] | status --all
  nova-friend status --as bob --dir ./bob
flags:
  --all  one line per friend daemon agent installed for this login (com.nova.friend-*): name, daemon up or down, daemon_version, last_beat_age
  --as <string>  your name (required without --all)
  --dir <string>  the friend's working directory, where the queue file lives (required without --all)
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

<!-- clidoc:begin nova-swarm -->
`nova-swarm help`:

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
                       (a bare --card holds the card to nova-swarm's own card contract, the shape native
                        runs; --rules lists every check; --fleet lints a launcher script against the
                        coordinator's /bin/bash 3.2. --base-check adds the four checks of a coding card;
                        --decide asks the brief decision nova-sprint add asks; an adopter's own rules go
                        in --child-rules-file. --member-injects lints the card as the member stages it.
                        nova-swarm lint -h has the rest.)
  nova-swarm step      --card <file> --dir <checkout> [--work <dir>] [--result <file>] [--sandbox <binary> | --no-wall] | --card <file> --remainder <id> --from <step> --land <sha>
                       (runs the card's own programs: a card whose every work step is a script step,
                        walked in the checkout with no model, each program, POST command and git in its
                        own wall (network denied, no credential, the checkout and a private temp the only
                        writes). --sandbox <binary> names the wall binary each program, POST command and
                        git runs in, default nova-sandbox on PATH; --no-wall runs them unconfined. One STEP
                        OK|FAILED line per step; --remainder prints the card a failed step leaves.)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>] [--identity <owner>,<name>,<email>]
  nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--max-load <load>] [--warn-load <load>] [--gocache-limit <GiB>] [--stage-wall <duration>] [--identity <owner>,<name>,<email>]
                        (run this machine as a sprint member; --server is the address of nova-sprint run --listen.
                         Each tick beats, reads the queue, reports ended children and takes cards to the
                         fleet row's width; --width overrides it, and --pass names environment secrets to
                         hand to children. A reader runs the reads of the readers table; this machine opens
                         no store. --no-wall runs each child with no wall. nova-swarm member -h has the rest.)
  nova-swarm disk-guard [--root <dir>]... [--scan <dir>]... [--cache <dir|glob>]... [--cache-max-gb <GiB>] [--modcache-max-gb <GiB>] [--logs <dir>] [--log-max-mb <MiB>] [--log-keep <n>] [--pool-idle <duration>] [--land <dir>] [--clone-age <duration>] [--mirrors <dir>] [--disk-floor <GiB>] [--stop-floor <GiB>] [--dry-run]
                       (one pass over this machine, run every few minutes by the disk-guard loop row
                        fleet/loops.yml adds to every machine: it trims every Go build cache over
                        --cache-max-gb, empties a module cache over --modcache-max-gb, rotates loop logs
                        over --log-max-mb and sweeps a stopped loop's pool and old land clones. It never
                        removes anything with uncommitted work or a live process, and prints one REMOVED,
                        TRIMMED, CLEANED, ROTATED or KEPT line per action. disk-guard -h has the rest.)
  nova-swarm mirror    --repos <a,b> --base <url> [--dir <dir>] [--every <duration>]
                       (keep the bench's bare mirrors fresh: each repository cloned into <dir>/<name>.git when absent, then every head and pull-request head fetched, one MIRROR OK or MIRROR FAILED line each; --every runs until stopped. mirror -h has the rest.)
  nova-swarm install   <disk-guard|mirror-refresh> [--dir <dir>] [--log <file>] [--every <duration>] [--dry-run]
                       (writes the unit that runs this binary, kept alive, and loads it. disk-guard's own
                        flags ride on the unit; mirror-refresh is refused here, and its loop runs
                        nova-swarm mirror as a nova-config loop row.)
  nova-swarm uninstall <disk-guard|mirror-refresh> [--dir <dir>] [--dry-run]
                       (unloads that unit and removes its file; a dry run removes nothing.)
  nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
  nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>] [--kind <kind>]
  nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
                       (a lease whose holder is still RUNNING is KEPT: SLOTS KEPT, live=<n>, exit 2.
                        --force frees it anyway and can oversubscribe the bench: an operator's act,
                        never a card's and never a manager's default)
  nova-swarm slots list --store <dir>
  nova-swarm slots run --store <dir> --owner <o> [--n <k>] [--for <duration>] [--kind <kind>] [--label <text>] [--wait <duration>] -- <command> [args...]
  nova-swarm worker    check <description.json> [--env] [--max <n>]

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a verification that
failed, a lint that found a defect; 2 could not run: a missing flag, an unreadable worker
description, a key file that is absent or empty, a bad invocation; by verb:
  member: 3 its binary was replaced on disk (MEMBER STOP: its supervisor starts the new
    one; with children running it first takes no new card and stops when the last is
    reported)

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

Sandbox: native and member use nova-sandbox unless --no-wall is explicit
(docs/SPEC-SANDBOX.md); no card can opt out. The NATIVE line reports the opt-out
as sandbox=none-by-flag. The job directory, data home, temporary directory and
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
```

`nova-swarm version -h`:

```
usage: nova-swarm version [flags]
from `nova-swarm help`:
  nova-swarm version    print this build identity (--version also accepted)
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
effect: inspection: reads, writes nothing
```

`nova-swarm doctor -h`:

```
usage: nova-swarm doctor [flags]
from `nova-swarm help`:
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
flags:
  --local <file>  the nova-swarm binary file to read as the local build (default: ~/.local/bin/nova-swarm)
  --path <file>  the nova-swarm binary file to read as the one first on PATH (default: PATH's)
exit codes: 0 the binaries agree, or there is one to read; 2 they drift, one shadows
  the other, or one cannot be read (the DOCTOR line says which)
effect: inspection: reads, writes nothing
```

`nova-swarm verify -h`:

```
usage: nova-swarm verify [flags]
from `nova-swarm help`:
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
effect: inspection: reads the job's RESULT.md and checks its line 1, writes nothing
flags:
  --card <file>  the card file, read only when given, for the checks that need the card
  --contract <line>  required: the card's contract line, which line 1 of RESULT.md must equal exactly
  --label <label>  required: the job's label, carried on the result line and in the receipt
  --max <int>  the most evidence lines past the disposition, at least 1
  --result <file>  required: the job's RESULT.md file, whose line 1 is checked
  --run-record <file>  the job's exit.json file: the harness's exit code joins the verdict
  --usage <file>  the job's usage.tsv file: tokens, dollars and wall time join the receipt
exit codes: 0 the result holds its contract; 1 it does not (the line says why); 2 could
  not run: a missing flag, a file that cannot be read, a receipt that cannot be written
```

`nova-swarm lint -h`:

```
usage: nova-swarm lint [flags]
from `nova-swarm help`:
  nova-swarm lint      --card <file> (or the bare <file>) [--typed] [--child-rules | --child-rules-file <file>] [--member-injects] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--decide [--decide-answers <file>] [--decide-record <file>]] [--max <n>] | --fleet <file> [--max <n>] | --rules
  (a bare --card holds the card to nova-swarm's own card contract, the shape native
  runs; --rules lists every check; --fleet lints a launcher script against the
  coordinator's /bin/bash 3.2. --base-check adds the four checks of a coding card;
  --decide asks the brief decision nova-sprint add asks; an adopter's own rules go
  in --child-rules-file. --member-injects lints the card as the member stages it.
  nova-swarm lint -h has the rest.)
  nova-swarm lint --rules
flags:
  --base-check  also run the four base checks of a coding card: PATHS exist at the base sha in --repo, no STEP pushes or calls gh, the LEG is in --legs, the deadline meets --p95
  --card <lint <file>>  the card file to lint before any spend (the bare lint <file> is the same)
  --child-rules  also hold the card to the child rules: the built-in general rules, or the sentences of --child-rules-file
  --child-rules-file <file>  the rules file to hold the card to instead of the built-in general rules (it implies --child-rules): one required sentence per line, [name] sentence to name the token
  --decide  also ask the brief decision nova-sprint add asks (p(converges), the minutes, the questions the card leaves open) and print it on one LINT DECIDE line; Jev with JEV_API_KEY from the environment, or --decide-answers; the lint's verdict is unchanged, and a failing backend prints it, then why, exit 2
  --decide-answers <file>  with --decide: the fixed backend's answers file (nova-decide's shape), in place of Jev: no network, no key
  --decide-record <file>  with --decide: the record file the decision is appended to (nova-decide's; created if absent); none records nothing
  --fleet <string>  a launcher script to lint instead of a card
  --legs <string>  with --base-check: the fleet leg table, one leg per line or a TSV whose first column is the leg
  --lineup <string>  with --typed: the sprint lineup, one card id per line or a TSV with an id column, so an unknown depends-on id is named
  --max <int>  at most this many item lines, 0 for all
  --member-injects  the member injects the child rules at stage time (rules by reference): the card need not carry them, and a line that contradicts them is still a finding; the rules are --child-rules-file's, else the held file of the card's repository (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt for another); it implies --child-rules
  --p95 <file>  with --base-check: a file of <kind> <seconds> rows, the p95 wall of each kind's finished cards (* answers for any kind)
  --repo <string>  with --base-check: the git checkout the card's PATHS are resolved in at the base sha (default the working directory)
  --rules  print every rule token with what it wants, and lint nothing; nova-sprint add holds a brief to the rule-<name> and step-<what> tokens of its rule set (and rule-libraries-considered when the set carries it) and to no other
  --trust <string>  a file of TRUST kind=<kind> state=<trial|trusted|paused> lines, for the paused check
  --typed  require the typed header (KIND, PATHS, TEST, PAUSED and DEPENDS-ON lines) even on a card that has none
exit codes: 0 the card is clean (a NOTE line is advice and changes nothing); 1 a drift,
  each on its LINT DRIFT line; 2 could not run: a missing flag, a file that cannot be read, a
  bad invocation
effect: inspection: reads, writes nothing
```

`nova-swarm step -h`:

```
usage: nova-swarm step [flags]
from `nova-swarm help`:
  nova-swarm step      --card <file> --dir <checkout> [--work <dir>] [--result <file>] [--sandbox <binary> | --no-wall] | --card <file> --remainder <id> --from <step> --land <sha>
  (runs the card's own programs: a card whose every work step is a script step,
  walked in the checkout with no model, each program, POST command and git in its
  own wall (network denied, no credential, the checkout and a private temp the only
  writes). --sandbox <binary> names the wall binary each program, POST command and
  git runs in, default nova-sandbox on PATH; --no-wall runs them unconfined. One STEP
  OK|FAILED line per step; --remainder prints the card a failed step leaves.)
effect: local write: commits in the checkout --dir names, and runs the card's programs in their own wall; --dry-run writes nothing
flags:
  --card <file>  required: the tree card's file
  --dir <checkout>  the checkout the steps run in (cwd of every program and POST command); required unless --remainder
  --dry-run  print each step the walk would run (its language, paths and POST lines) and the wall its commands would run in, and run and write nothing
  --from <step>  with --remainder: the failed step the remainder starts at
  --land <sha>  with --remainder: the full sha steps 1..n-1 landed at (the finish's pushed=)
  --no-wall  run the card's programs with no wall, with this process's own powers: network, environment and every file it can write
  --remainder <id>  print the remainder card of this card id from --from, staged at --land, and run nothing
  --result <file>  the RESULT.md file to write in the result shape, the step lines in its body
  --sandbox <binary>  the wall binary each program, POST command and git runs in (default: nova-sandbox on PATH)
  --work <directory>  the directory the programs are built in and the steps' private temp made under (default: a new one under TMPDIR)
exit codes: 0 every step run is ok, each on its STEP OK line (--remainder: the card printed);
  1 a step failed, on its STEP FAILED line, and the steps after it were not run; 2 could not
  run: a missing flag, a card that cannot be read, whose tree has a finding or that is not
  script steps only, no wall and no --no-wall
```

`nova-swarm template -h`:

```
usage: nova-swarm template [flags]
from `nova-swarm help`:
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm template --name read-pr
  nova-swarm template --name worker
flags:
  --name <name>  required: the template's name: capacity, card, drift, fix, fix-card, models.tsv, probe-row, read, read-pr, replay, result, setup, text, tone, worker (card is a whole card that passes lint --child-rules)
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
effect: inspection: prints a template, writes nothing
```

`nova-swarm profile -h`:

```
usage: nova-swarm profile [flags]
from `nova-swarm help`:
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
flags:
  --jobs <glob>  required: a glob of job directories (or timeline.tsv files), each holding a native run's per-turn timeline
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
effect: inspection: reads the timeline.tsv of each job the glob names, writes nothing
```

`nova-swarm native -h`:

```
usage: nova-swarm native [flags]
from `nova-swarm help`:
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>] [--identity <owner>,<name>,<email>]
flags:
  --auth <file>  the harness's auth file, one entry of it copied into the child's data home (not with a --worker naming a secret)
  --bench <name>  this bench's name, in a staging timeout's report (default: this machine's host name up to its first dot)
  --card <file>  required: the card file, handed to the child byte for byte as its task
  --config <file>  the harness's provider config file (opencode.json), copied beside the auth (not with a --worker naming a secret)
  --deadline <duration>  required: the wall-clock bound that ends the child, a duration such as 30m
  --frame <file>  the frame file a member wrote: the repository, commit and branch to stage, from which JOB.md and the shims are written
  --harness <path>  required: the harness binary path the child runs under, checked for existence and execution
  --identity <owner,name,email>  the pool identity the child commits under, owner,name,email (default: <root>/identity.tsv)
  --idle <duration>  end the card when neither its output nor its process tree has moved for this duration; 0 turns the watch off (default 5m)
  --label <label>  the run's label, on its NATIVE line and its result (default: the card file's name without its extension)
  --model <provider/model>  required without --worker: the provider/model to run, one slash, both sides nonempty
  --no-shared-caches  keep the Go caches under the child's HOME instead of the bench's shared <root>/cache
  --no-wall  run the child with no nova-sandbox wall: the caller owns every read and write it makes
  --owner <string>  accepted and read by nothing, with --slots-store
  --recipient <value>  a bus lane the card may address (again for more); a bus send is denied inside the wall whatever is named
  --repo <owner/name>  a repository the card may clone, owner/name (again for more): the wall opens the network to it alone
  --results-root <dir>  the dir RESULT.md, usage.tsv and the report are published under (default <root>/results)
  --root <dir>  required: the configured root dir the slot sits under; results go under <root>/results
  --sandbox <path>  the nova-sandbox binary path that builds the wall (default: nova-sandbox on PATH); not with --no-wall
  --slot <dir>  required: the slot dir this run executes in, under --root; HOME is a data directory beneath it
  --slots-store <string>  accepted and read by nothing: native takes no slot lease (the dealer holds a bench's capacity)
  --stage-timeout <duration>  the bound on staging the card's checkout from the bench mirror, a duration (default 120s)
  --sweep-now  delete the job directory once its results are published (never before)
  --tokens <n|unmetered>  required: the token budget, a number of tokens, or the word unmetered when the provider has no live accounting and the deadline is the only stop (n|unmetered)
  --usage-interval <duration>  how often the token budget's source is read, a duration or whole seconds, at least 1s and under --deadline (default 5s)
  --usd <string>  the dollar budget per card, a decimal such as 0.50: the harness's reported cost at which the card is stopped (stopped=usd), beside --tokens; empty for none
  --worker <file>  the worker description file (JSON) that names the model, the key and the read roots; nova-swarm worker check checks it
exit codes: 0 the child exited 0 (the NATIVE line's OK, or INCOMPLETE and its why=, is
  the verdict); 1 the child was killed (its deadline, a TERM) or exited 255; any other code is
  the child's own; 2 could not run: a missing flag, a wall, a card or a worker description that
  is not there
effect: delivery: runs the card's harness, which calls the model's provider, and writes the job directory under --root
```

`nova-swarm member -h`:

```
usage: nova-swarm member [flags]
from `nova-swarm help`:
  nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--max-load <load>] [--warn-load <load>] [--gocache-limit <GiB>] [--stage-wall <duration>] [--identity <owner>,<name>,<email>]
  (run this machine as a sprint member; --server is the address of nova-sprint run --listen.
  Each tick beats, reads the queue, reports ended children and takes cards to the
  fleet row's width; --width overrides it, and --pass names environment secrets to
  hand to children. A reader runs the reads of the readers table; this machine opens
  no store. --no-wall runs each child with no wall. nova-swarm member -h has the rest.)
flags:
  --as <name>  required: this machine's name, its row in the fleet table (with --reader, its row in the readers table, reader-<machine>)
  --auth <file>  the harness's auth file, handed to each child's native --auth
  --config <file>  the harness's provider config file, handed to each child's native --config
  --deadline <duration>  the wall-clock bound a card with no route runs to, a duration or whole seconds; a reader given it runs every read to it
  --disk-floor <GiB>  the free GiB the slots' volume keeps: below it no card starts (default 10; 0 checks nothing)
  --every <duration>  the time between passes and beats, a duration or whole seconds, above 0 and at most 5s (default 3s)
  --gh <path>  the gh path the member opens a work card's pull request with, outside the wall (default gh)
  --gocache-limit <GiB>  the GiB the shared Go build cache is held under by the cleaner, oldest unused entries removed down to 80% of it, never one used in the last two hours (default 20; a busy machine holds its working set with more)
  --harness <path>  required: the harness binary path each card's child runs under (native --harness)
  --identity <owner,name,email>  the pool identity every child commits under, owner,name,email (default: the pool's identity.tsv)
  --max-load <load>  the maximum one-minute host load at which a local child starts (default 0: no load gate)
  --model <provider/model>  the provider/model a card with no route runs on; a reader given it runs every read on it
  --no-wall  run each child with no nova-sandbox wall (native --no-wall): the caller owns every read and write it makes
  --once  run one pass, wait for its starts and pushes, and stop
  --pass <NAME,...>  the NAME,... of secrets in this environment a child is handed (the loop record's nova-secrets keys); a harness that reads its provider key from the environment needs it
  --reader  run as a reader: take and run reads of finished work instead of work cards, at its machine's width; a flash card's first read is a decide read, asked with JEV_API_KEY from this environment (docs/SPEC-SPRINT.md section 6)
  --results-root <dir>  the dir each launch's results are written under (default <root>/results)
  --root <dir>  required: the dir the launches and results sit under
  --server <address:port>  required: the sprint server's address:port, which nova-sprint run --listen started on the coordinator's machine; every sprint verb goes there and this machine opens no store
  --slots <dir>  the dir of the launch directories, one per card launch (default <root>/slots)
  --stage-wall <duration>  the bound on staging each card's checkout, a duration or whole seconds, handed to native as --stage-timeout: a slow machine under load names a longer one in its loop row's argv (default 120s)
  --ticks <int>  run this many passes and stop (not with --once; default: run until stopped)
  --tokens <n|unmetered>  the token budget a card with no route runs on, a number of tokens or the word unmetered (n|unmetered); a reader given it runs every read on it
  --warn-load <load>  the one-minute host load at which a local child start warns, at or below --max-load (default 0: no warning)
  --width <int>  an override of the most cards it runs at once, a twin's; a worker runs its fleet row's width, read every tick: a member its own row's, a reader its machine's (reader-<m> runs at m's width)
  --worker <file>  the worker description file (JSON), handed to each child's native --worker; the secret it names is handed through too
exit codes: 0 it stopped as asked (--once, --ticks); 2 could not run: a missing flag, a
  directory that cannot be made; 3 its binary was replaced on disk (MEMBER STOP: its supervisor
  starts the new one; with children running it first takes no new card and stops when the last
  is reported)
effect: delivery: joins a sprint's fleet through --server, runs its cards as native children, pushes their commits and opens their pull requests
```

`nova-swarm mirror -h`:

```
usage: nova-swarm mirror [flags]
from `nova-swarm help`:
  nova-swarm mirror    --repos <a,b> --base <url> [--dir <dir>] [--every <duration>]
  (keep the bench's bare mirrors fresh: each repository cloned into <dir>/<name>.git when absent, then every head and pull-request head fetched, one MIRROR OK or MIRROR FAILED line each; --every runs until stopped. mirror -h has the rest.)
  nova-swarm mirror as a nova-config loop row.)
effect: local write: creates and fetches into bare repositories under --dir; reads each repository over the network
flags:
  --base <url>  the url each repository is fetched from, <url>/<name>.git, such as https://github.com/<org> or git@<alias>:<org>
  --dir <dir>  the dir the bare mirrors <repo>.git live in (default ~/nova-bench/mirror)
  --every <duration>  refresh again after this duration, until stopped; 0 refreshes once (default 0)
  --repos <list>  the repository names to mirror, a comma-separated list
exit codes: 0 every repository refreshed (MIRROR OK each); 1 a repository failed (MIRROR FAILED
  names it and the cause; the others are still refreshed); 2 could not run: a missing flag or a bad name
```

`nova-swarm install -h`:

```
usage: nova-swarm install [flags]
from `nova-swarm help`:
  nova-swarm install   <disk-guard|mirror-refresh> [--dir <dir>] [--log <file>] [--every <duration>] [--dry-run]
  (writes the unit that runs this binary, kept alive, and loads it. disk-guard's own
  flags ride on the unit; mirror-refresh is refused here, and its loop runs
  nova-swarm mirror as a nova-config loop row.)
flags:
  --dir <string>  the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --dry-run  print the unit and write and load nothing
  --every <duration>  the least time between two starts
  --log <string>  the file the unit's lines go to on macOS; on Linux they are in the journal
exit codes: 0 done (the unit written or kept, and loaded); 1 the unit did not write or load; 2 usage, or mirror-refresh (no mirror verb yet)
effect: local write: writes the kind's unit (the verb itself, never a wrapper) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run writes nothing
```

`nova-swarm uninstall -h`:

```
usage: nova-swarm uninstall [flags]
from `nova-swarm help`:
  nova-swarm uninstall <disk-guard|mirror-refresh> [--dir <dir>] [--dry-run]
  (unloads that unit and removes its file; a dry run removes nothing.)
flags:
  --dir <string>  the directory the unit was written into (default: as install's)
  --dry-run  say which unit would be unloaded and removed, and unload and remove nothing
exit codes: 0 done (removed, or no unit there); 1 the unit did not unload or remove; 2 usage
effect: local write: unloads the kind's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing
```

`nova-swarm slots init -h`:

```
usage: nova-swarm slots init [flags]
from `nova-swarm help`:
  nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
flags:
  --capacity <int>  required: how many leases the bench grants at once, at least 1
  --owner <owner>  required: the one owner the store starts with
  --share <int>  required: how many of those the owner may hold at once, at least 1 and at most --capacity
  --store <dir>  required: the store dir to create, to hold shares.tsv and slots/
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
```

`nova-swarm slots take -h`:

```
usage: nova-swarm slots take [flags]
from `nova-swarm help`:
  nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>] [--kind <kind>]
flags:
  --for <duration>  required: how long the leases last, a positive duration such as 30m
  --kind <kind>  the card's kind, charged at its admission weight
  --label <label>  a label the leases carry, which slots release --label frees
  --n <int>  required: how many leases to grant, at least 1
  --owner <string>  required: whose share the leases count against
  --store <dir>  required: the store dir holding shares.tsv and slots/
exit codes: 0 the leases are granted; 2 refused: the owner's share or the bench is
  full (SLOTS REFUSED names the holders), a missing flag, or a store that cannot be read
```

`nova-swarm slots release -h`:

```
usage: nova-swarm slots release [flags]
from `nova-swarm help`:
  nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
  (a lease whose holder is still RUNNING is KEPT: SLOTS KEPT, live=<n>, exit 2.
  --force frees it anyway and can oversubscribe the bench: an operator's act,
  never a card's and never a manager's default)
flags:
  --all  free every lease of the owner (or --label)
  --force  free a lease whose holder is still running too: an operator's act, which can oversubscribe the bench
  --label <label>  free the owner's leases carrying this label (or --all)
  --owner <string>  required: whose leases are freed
  --store <dir>  required: the store dir holding shares.tsv and slots/
exit codes: 0 the leases named are freed; 2 a lease's holder still runs (SLOTS
  KEPT; --force frees it), a missing flag or a store that cannot be read
```

`nova-swarm slots list -h`:

```
usage: nova-swarm slots list [flags]
from `nova-swarm help`:
  nova-swarm slots list --store <dir>
flags:
  --store <dir>  required: the store dir holding shares.tsv and slots/
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
effect: inspection: reads, writes nothing
```

`nova-swarm slots run -h`:

```
usage: nova-swarm slots run [flags]
from `nova-swarm help`:
  nova-swarm slots run --store <dir> --owner <o> [--n <k>] [--for <duration>] [--kind <kind>] [--label <text>] [--wait <duration>] -- <command> [args...]
flags:
  --for <duration>  how long the leases last, a positive duration such as 30m (default 1h)
  --kind <kind>  the card's kind, charged at its admission weight
  --label <label>  a label the leases carry (default: command name)
  --n <int>  how many leases to grant, at least 1 (default 1)
  --owner <string>  required: whose share the leases count against
  --store <dir>  required: the store dir holding shares.tsv and slots/
  --wait <duration>  how long to wait when capacity is occupied, a bounded duration such as 30s
exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation
```

`nova-swarm worker -h`:

```
usage: nova-swarm worker [flags]
from `nova-swarm help`:
  nova-swarm worker    check <description.json> [--env] [--max <n>]
exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER
  DRIFT line; 2 it cannot be read, or a bad invocation
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-swarm -->

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

<!-- clidoc:begin nova-sprint -->
`nova-sprint help`:

```
nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land

how it works: one store (Redis or a twin file) holds the work, readers, merge
and fleet tables and the sprint view. A card is one unit of work in a stream.
Each tick deals ready cards to members (machines with a width), sends finished
work to readers and queues passed work for merging by stream. Decisions it
cannot make go to the coordinator's inbox.
first run: no Redis needed; the store is the file sprint.twin:
  export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
Follow the card flow under "trying it without a Redis", ticking by hand.
For a real fleet, "A real fleet" explains the server and clients; the example:
block shows the coordinator's day on that store.
For one verb's usage, examples, flags and exit codes:
  nova-sprint help <verb> (or <verb> -h)
For one group's help: nova-sprint help <group> (fleet, friend, reader, goal,
stream, lane, merge-window).

usage:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin] [--allow-personal-base]
  nova-sprint quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]
  nova-sprint preflight --brief-dir <dir> [--repo-dir <dir>]
  nova-sprint release check [--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...
  nova-sprint release (<sentinel or held card>... | <selector> [--dry-run]) --reason <text> [--answers <note>]
  nova-sprint resolve [<id>...] [--stream <s>] [--max <n>]
  nova-sprint friends watch [--actor <seat>] [--state <file>]
  nova-sprint status watch [--actor <seat>] [--state <file>]
  nova-sprint start
  nova-sprint stop --reason <text> --until <time or duration>
  nova-sprint stop-return --as <owner-row> <card>@<gen>... --epoch <n> --reason <cancel acknowledgement> [--dry-run]
  nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
  nova-sprint tick [--answer-rules] [--idle-alarm] [--shadow]
  nova-sprint selftest land [--binary <path>] [--scratch-dir <dir>]
  nova-sprint selftest [--dir <d>] [--keep]
  nova-sprint goal set <name> [--file <path>] [--to file:<path>]
  nova-sprint goal show [<name>]
  nova-sprint goal drop <name>
  nova-sprint take --as <member> [<card>@<gen>...] [--epoch <n>] [--max <n>]
  nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]
  nova-sprint progress --as <worker> <card>[@<gen>]... --epoch <n>
  nova-sprint ask [<id>... | --group <id> [--expect <n>]] [--stream <s>] [--max <n>] [--another] [--answers <note>]
  nova-sprint queue --as <reader|member> | --stream <s>
  nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>[@<gen>]...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
  nova-sprint accept (<id>... [--heavy --evidence <path> --reason <text>] | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]
  nova-sprint rework (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>] [--tier <tier>] [--answers <note>] [--one]
  nova-sprint return (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>] [--answers <note>]
  nova-sprint redo <card>... [--stream <s>] [--answers <note>]
  nova-sprint drop (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text> [--answers <note>] [--one]
  nova-sprint priority <id>... | (<id>... | --stream <s>) (--blocker | --critical | --fix | --high | --normal | --low) --reason <text>
  nova-sprint unpin (<id>... | --stream <s>) --reason <text> [--dry-run]
  nova-sprint rebase --from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]
  nova-sprint rank (<id>... | <selector> [--dry-run]) (--score <n> | --first | --before <id>) [--answers <note>]
  nova-sprint relink <old-id>[,<old-id>...] <new-id> [--reason <text>]
  nova-sprint recut <id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>]) [--new <id>] | <selector> (--tier <t> | --set-base <branch> | --drop-who)... [--dry-run]
  nova-sprint twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]
  nova-sprint brief <id> (--brief <text> | --brief-file <path>) [--rules <file>] [--answers <note>] | --dir <dir> [--rules <file>] | --group <id> [--expect <n>] (--brief-file <path> | --dir <dir>) [--answers <note>] | <id> --widen [--repo-dir <clone>] | <id> --tier <flash|pro|heavy|frontier> | <selector> (--set-base <branch> | --drop-who | --tier <t>)... [--dry-run]
  nova-sprint move <id>... --stream <s> [--before <id> | --after <id> | --score <n>]
  nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]
  nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
  nova-sprint verify-landed [--stream <s>...] [--repo-dir <clone>] [--base <branch>]
  nova-sprint landed <id>... --sha <commit> --reason <text> [--repo-dir <clone>] [--base <branch>]
  nova-sprint snapshot (--dir <dir> [--keep <n>] [--every <duration>] | --restore-drill <file>)
  nova-sprint backup (--out <dir> [--part-bytes <n>] [--secrets-store <dir> --secrets-as <seat> --secrets-key <path> --sops <path>] | --file <path> [--dry-run])
  nova-sprint demo load <backup.xz part>... [--sha256 <hex>] [--dir <dir>] [--xz <path>] [--redis-server <path>]
  nova-sprint demo stop [--dir <dir>]
  nova-sprint promote [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
  nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
  nova-sprint hold <member|reader|friend|stream>... --reason <text> [--return] [--dry-run]
  nova-sprint unhold <member|reader|friend|stream>... [--reason <text>] [--dry-run]
  nova-sprint fleet beat <member> [--load <percent>]
  nova-sprint fleet up <member> [--width <n> | --width 0]
  nova-sprint fleet down <member>
  nova-sprint fleet sync [--check] [--pg <dsn>]
  nova-sprint fleet level
  nova-sprint fleet quiet <member> (--for <duration> | --until <RFC3339>) --reason <text> | <member> --end [--dry-run]
  nova-sprint friend sync [--pg <dsn>] [--root <dir>]
  nova-sprint collect [<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]
  nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>] [--check <nonce>] [--pong <nonce>] [--run <id>]
  nova-sprint friend down <friend> [--reason <text>] [--until <RFC3339>]
  nova-sprint friend up <friend> [--width <n>]
  nova-sprint friend cards <friend> [--json]
  nova-sprint friend take <friend> (<id>... | --all-unstarted) [--reason <text>]
  nova-sprint friend give <friend> <id>... [--reason <text>]
  nova-sprint friend level
  nova-sprint friend health <friend> (--state up|asleep|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>] | --clear)
  nova-sprint friend clean [--pg <dsn> | --file <path>] [--root <dir>] [--days <n>] [--dry-run]
  nova-sprint gc [--machine <m>] [--dry-run] [--max-age <d>]
  nova-sprint friend reconcile <friend> [--root <dir>] [--dry-run]
  nova-sprint lane take <kind> --machine <m> --as <worker> [--wait <duration>] [--dry-run]
  nova-sprint lane give <kind> --machine <m> --as <worker> [--dry-run]
  nova-sprint lane list
  nova-sprint reader add <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
  nova-sprint reader set <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
  nova-sprint reader away <reader>...
  nova-sprint reader up <reader>...
  nova-sprint reader remove <reader>...
  nova-sprint reader retire <reader>...
  nova-sprint stream remove <stream>...
  nova-sprint stream archive <stream>...
  nova-sprint stream unarchive <stream>...
  nova-sprint stream set <stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--promotion[=false]] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--base <branch>] [--reason <text>] [--answers <notes>]
  nova-sprint set [--rework-priority <fix|high|keep>] [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--attempts <n|default>] [--friend-idle <duration|default>] [--friend-finish <duration|default>] [--fleet-tiers <tiers|all>] [--friends-tiers <tiers|all>] [--reads <0|1|2|default>]
  nova-sprint promoted --sha <merge sha> [--answers <note>]
  nova-sprint merge-window open --for <duration> --reason <text>
  nova-sprint funded <provider> --reason <text>
  nova-sprint cost reconcile [--dry-run] [--json]
  nova-sprint cost reprice [--route <r>]... [--since <RFC3339>] [--dry-run] [--json]
  nova-sprint ci <id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]
  nova-sprint wait (<note>[,<note>]... | --group <id> [--expect <n>]) (--for <duration> | --until <RFC3339>)
  nova-sprint remind (--in <duration> | --at <time>) --note <text> [--for <actor>] | --list | --cancel <id>
  nova-sprint ack <note>[,<note>]... --reason <text>
  nova-sprint answer [--dry-run] [--bar <p>] [--every <duration>] [--timeout <duration>] [--backend jev|fixed] [--answers <file>] [--record <file>]
  nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>] [--push <dir> | --push seat]] [--deadline <duration>] [--stale <duration>]
  nova-sprint card base <id> <branch> [--repo-dir <clone>]
  nova-sprint card <id> [--brief | --fields] [--at-epoch <n>] | (--all | --stream <s>) --json: every card, one JSON object a line
  nova-sprint needs [--stream <s>] [--roots]
  nova-sprint streams [--repo <owner/name>] [--release <name>] [--cards]
  nova-sprint held [--stream <s>]
  nova-sprint sentinels [--stream <s>]
  nova-sprint sentinel set <id> --needs <a,b>
  nova-sprint bases
  nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
  nova-sprint check
  nova-sprint repair
  nova-sprint watch --wake [--every <duration>] [--state <file>] [--check <duration>] [--judgment-every <duration>] [--merge-every <duration>] [--backlog-every <duration>] [--land-after <duration>] [--merge-over <n>] [--merging-over <n>] [--review-over <n>]
  nova-sprint seat check
  nova-sprint machinery
  nova-sprint where [--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived] [--stale <duration>] [--at-epoch <n>]: includes landedSeries] [--release [<name>]]
  nova-sprint dashboard [--listen <address:port>[,<address:port>...] | none] [--pull <address:port>[,<address:port>...] | none] [--logo <file>] [--every <duration>]
  nova-sprint handover
  nova-sprint view coordinator [--all] [--since <cursor>] [--json]
  nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]
  nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]
  nova-sprint seat watch <dir> [--json]
  nova-sprint seat uninstall [--dir <dir>]
  nova-sprint seat deliver [--text <message>] --actor <seat>
  nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <why>]] [--beat bus|friends|transitions [--failed <why>]] [--observe friends|transitions --json] [--dry-run]
  nova-sprint seat pong <nonce> [--dry-run]
  nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
  nova-sprint fsck seat [--pg <host:port or postgres:// URI>]
  nova-sprint routes
  nova-sprint rules
  nova-sprint stats tidy (--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]
  nova-sprint stats [--routes [--since <10m|RFC3339>]]
  nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
  nova-sprint clear --confirm sprint
  nova-sprint teardown --confirm sprint
  nova-sprint live [--bin-dir <dir>] [--dashboard <link>]... [--json]
  nova-sprint adopt <version|path> --source <checkout> --inventory <file> --reason <text> [--limit <host>] [--receipts <dir>] [--dry-run]
  nova-sprint server switch [<binary>] [--rollback] [--window <duration>] [--target <path>] [--tick-deadline <duration>]
  nova-sprint install <server|member|seat-push|friend-sync|table> [--dir <dir>] [--log <file>] [--dry-run] (each kind's own flags are listed by install <kind> -h)
  nova-sprint uninstall <server|member|seat-push|friend-sync|table> [--dir <dir>] [--dry-run]
  nova-sprint units --check [--dir <dir>]
  nova-sprint cost attach <card>.<attempt> --model <provider/model> --input <n> --cache-read <n> --cache-write <n> --output <n> [--reasoning <n>] [--usd <x>] [--source <text>] [--replace] [--dry-run] | --file <tsv> [--replace] [--dry-run]
  nova-sprint coordinator <name> --reason <text> | <name> --take --approved-by <owner> --reason <text>

Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --actor <name> (else NOVA_SPRINT_ACTOR; no
default: a verb that writes wants one), --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items, and the count of a
set when the verb takes one; 0 is all listed; --limit is --max for one
release). The
coordinator's verbs are the coordinator's alone (the first init names it:
--coordinator, else the actor); take, finish, read, fleet beat, friend
beat, lane take and lane give are the workers', whose actor is the member, reader or friend named; merge and ci are
reports; tick, run, friend clean and promote are the machine's; the reads need no actor (inbox
--read, which moves the coordinator's cursor, is the coordinator's). The seat
moves by coordinator <name> --reason <text>: given by its holder or the owner
(init --owner), or taken by <name> itself with --take --approved-by <owner>,
each in the log; handover prints what the next seat needs. A set is
ids, a stream, a column, --max n, or an inbox group: --group <id>, the id
inbox prints, which does not move, with --expect <n>, the size it printed,
which refuses a group that has changed. Each verb prints what moved (MOVED),
what did not and why (REFUSED, on stderr), its summary line, and the sprint's
line: landed/all percent -> ETA <estimate> (the streams on the table: an
archived stream's cards leave it; every card left, held ones too, at
the cards landed an hour: where's over the last hour of running time, the
whole sprint's average with fewer than five there and on this line; in minutes
rounded up, days and hours from a day; where shows the largest
of the last 10 s, and held=N, the cards behind a sentinel not released or
admitted held; the word alone until one has landed; a stopped
machine has no ETA: STOPPED, then
landed/all and the percent when there are cards; every card landed, no ETA:
done in <time from the first start> while it runs, and STOPPED ... done once
the machine has stopped itself).

The tables are work, merge, readers and fleet, and the view is sprint; a store
holds one sprint (a second sprint is a second store). The work table's cost
column is, per stream, the sum of its landed cards' total cost in US dollars
(each consumer's actual cost, else its predicted one; - when none was priced),
with the sum over the streams at the bottom; card <id> shows the detail. clear and teardown want
--confirm sprint, the name of the view, and refuse anything else.

A work card is named with its generation, <card>@<gen>: the generation the
worker holds, from queue --as <member> (--json: "gen"). take by id and finish
name it for every card; a card named without one is refused, naming the live
generation, and a generation that is not the live one is refused as stale.
take with no card takes the member's oldest ready cards (--max n, default 1)
and prints each one's generation.
```

`nova-sprint init -h`:

```
usage: nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
from `nova-sprint help`:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
  nova-sprint init --readers reader-a,reader-b --members m1
  nova-sprint init --readers reader-a,reader-b --members m1:8
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --attempts <string>  the sprint's attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect; 1 to 100 (default 4; later: nova-sprint set --attempts <n>)
  --coordinator <string>  the sprint's coordinator, the one actor who releases sentinels (default: the actor); the seat then moves by coordinator <name>
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --members <string>  fleet members to bring up, comma separated, each <name> or <name>:<width>, its width the most work cards it runs at once; it holds 2 times that, ready and working (default 64)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --owner <string>  the sprint's owner, who may give the seat and whose name a take of it carries (coordinator --take --approved-by); set once, never changed (else NOVA_SPRINT_OWNER)
  --readers <string>  the readers' rows, comma separated
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --rules <string>  the child rules file every brief is held to: one required sentence per line, its path recorded for the sprint (default: the built-in general rules; add --rules <file> overrides it for one add)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint add -h`:

```
usage: nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin] [--allow-personal-base]
from `nova-sprint help`:
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin] [--allow-personal-base]
  nova-sprint add --stream s1 --count 1 --one
  nova-sprint add --stream s1 --count 3 --brief-file brief.txt
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --after <string>  place the cards in line after this primary of the stream
  --allow-personal-base  admit cards whose brief's BASE: is a personal branch (<name>/* for the sprint's coordinator, its owner or a friends table row), by default refused naming the base and this flag: no sprint watches a personal branch's gate (docs/SPEC-SPRINT.md section 11, bases-view-r.w2)
  --allow-shared-paths  with a card per brief file (--brief-dir, or --brief-file with no ids): admit cards that name one file in their PATHS: lines though neither needs the other and neither brief declares it on a SHARED: line (by default refused, naming the file and the cards)
  --before <string>  place the cards in line in front of this primary of the stream
  --brief <string>  the brief: a child's whole brief, at most 16 KiB (the card lint advises 12000 bytes), held to the card lint (the sentences of the rules file: --rules, else the one init --rules recorded, else the built-in general rules; nova-swarm template --name card prints a card that passes the general ones, nova-swarm lint --rules lists them) and refused, exit 2, nothing written, when it fails; a card with no brief is not linted; a brief that names PATHS, REPO and BASE is also held at the BASE tip (a literal path must exist, a glob must match a file, and every func, type or verb STOP or START names with a file, and a TEST name the tree already holds, must be inside a PATHS file; one line per miss names the nearest file; a new _test file or a NEW: line may be absent); under JEV_API_KEY each card's brief is then asked nova-decide's brief decision (one BRIEF line per card, an uncalibrated rank) and refused under the sprint row's decide_brief_bar, empty by default
  --brief-dir <string>  one card per *.md file in this directory, in byte order of file name, each card's id its file's name without .md (a1.md is a1); not with --brief-file
  --brief-file <value>  the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; given once with ids, --count or --sentinel, the brief of the cards they name; given alone or again, one card per file in the order given, each card's id its file's name without .md (a1.md is a1); not with --brief or --brief-dir
  --brief-op <id=op>  id=op: a card's brief decision op id (<id>@brief-<hex>), which add sends its server itself when it asked the decision where it was typed; refused when typed on an add no server runs; repeated, one per card
  --count <int>  admit n primaries with generated ids <stream>-<n>
  --decide-record <file>  the record file of the cards' brief decisions under JEV_API_KEY (default ~/nova-sprint/decide/brief.jsonl, the coordinator's root); each card stores it and its op, and land and drop attach the card's end there
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --held  admit the cards held: waiting, a sentinel never reached and no card dealt, nothing raised, until nova-sprint release <id> --reason <text>; a wave loads behind a held sentinel with nothing before it
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --needs <string>  primaries that must land first, comma separated; each is a primary on the table or of this add (default: the brief's Needs: or DEPENDS-ON: line; with a brief per card, added to each card's own)
  --one  admit a single card (one positional id, --count 1 on one stream, or one --brief-file alone): refused without it, since cards are admitted in waves (--brief-dir, --count 2 or more, several --brief-file)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --replaces <ids>  the card this add admits is the twin of these ids, comma separated: it takes over every edge where a waiting card needs one of them (that card needs the twin instead, in the same place), each still on the table is dropped with the reason "replaced by <the new id>", and no blocked judgment is raised for it, in one step; a card dropped before is replaced too, and its blocked judgments are answered; one card only (it means --one), never a sentinel
  --rules <file>  the child rules file, read at add time: one required sentence per line, [name] sentence names its token (default: the file init --rules recorded, else the built-in general rules); e.g. --rules rules/card.txt. A file the members hold (fleet/child-rules*.txt of this build) is by reference: a card on a repository with a held file (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt) need not carry it, the card names the file, and the member injects it at stage time
  --score <string>  the first primary's score; the rest follow it (default: after every primary)
  --sentinel <string>  admit a sentinel with this id: a stop the coordinator releases; what sorts after it waits for it
  --sentinel-every <int>  with --count: a sentinel <stream>-gate-<n> after every k cards (a stop by its place in line)
  --sentinel-last  with --sentinel-every: a sentinel after the last card too
  --stream <string>  the stream the primaries belong to, for life; with --count, several streams comma separated, one step
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint quack -h`:

```
usage: nova-sprint quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]
from `nova-sprint help`:
  nova-sprint quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --base <string>  the branch of the test repository the cards start from
  --count <int>  quack cards per stream, at least 1
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo <string>  the clone URL of the test repository the quack cards commit to
  --streams <string>  the streams to cut quack cards into, comma separated (a stream new to the sprint is made)
  --tiers <string>  the model tiers each stream's cards take in turn, comma separated: frontier, heavy, pro or flash
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint preflight -h`:

```
usage: nova-sprint preflight --brief-dir <dir> [--repo-dir <dir>]
from `nova-sprint help`:
  nova-sprint preflight --brief-dir <dir> [--repo-dir <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --brief-dir <string>  one brief per *.md file in this directory, in byte order of file name: preflight checks each one
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  a repository clone: TEST names a test git grep finds at BASE, and BASE is checked there; without it both are unchecked
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the briefs, the table and the repository, writes nothing
```

`nova-sprint release check -h`:

```
usage: nova-sprint release check [--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...
from `nova-sprint help`:
  nova-sprint release check [--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --check <value>  run only this check, by name (repeat for more; default every check): no-stuck-friend, cards-settled, base-gate-green, two-ok-reads, prose-true, landings-promoted, no-open-judgment, merge-queue-p90
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --merge-p90 <duration>  the bar on merge-queue-p90: the p90 of the time cards spent merging (default 30m)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --streams <string>  only the log of the streams this glob names (path.Match over the stream's name; default every stream)
  --window <duration>  how far back a card's merging counts, for merge-queue-p90 (default 24h)
exit codes: 0 every check passed (RELEASE OK), 1 a check failed (RELEASE NOT READY; each RELEASE CHECK line names what to look at), 2 usage or a store that did not answer
effect: inspection: reads the store's log and the sprint's settings, writes nothing
```

`nova-sprint release -h`:

```
usage: nova-sprint release (<sentinel or held card>... | <selector> [--dry-run]) --reason <text> [--answers <note>]
from `nova-sprint help`:
  nova-sprint release check [--json] [--streams <glob>] [--window <duration>] [--merge-p90 <duration>] [--check <name>]...
  nova-sprint release (<sentinel or held card>... | <selector> [--dry-run]) --reason <text> [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  what you looked at and found: recorded on the sentinel or held card, and in a sentinel's notification; a sentinel not yet reached is released when each card it waits for has landed, was dropped, or is in flight (taken, in review or merging), and refused naming the first that has not started
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint resolve -h`:

```
usage: nova-sprint resolve [<id>...] [--stream <s>] [--max <n>]
from `nova-sprint help`:
  nova-sprint resolve [<id>...] [--stream <s>] [--max <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: moves the waiting primaries whose needs landed in the sprint's store; --dry-run writes nothing
```

`nova-sprint friends watch -h`:

```
usage: nova-sprint friends watch [--actor <seat>] [--state <file>]
from `nova-sprint help`:
  nova-sprint friends watch [--actor <seat>] [--state <file>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --state <string>  optional file holding the last successfully delivered snapshot; each actor and push has its own file
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint status watch -h`:

```
usage: nova-sprint status watch [--actor <seat>] [--state <file>]
from `nova-sprint help`:
  nova-sprint status watch [--actor <seat>] [--state <file>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --state <string>  optional file holding the last successfully delivered snapshot; each actor and push has its own file
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint start -h`:

```
usage: nova-sprint start [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint start
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint stop -h`:

```
usage: nova-sprint stop --reason <text> --until <time or duration>
from `nova-sprint help`:
  nova-sprint stop --reason <text> --until <time or duration>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why the machine stops, shown with it: the machine line of where, inbox and the dashboard says "STOPPED by <actor>: <reason>, back by <time>" (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --until <time or duration>  when the machine starts itself again: a time or duration, a duration from now (90m), a clock time (2:04 PM or 14:04, today's or tomorrow's) or an RFC 3339 time; the tick starts it then unless it is stopped again (required)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint run -h`:

```
usage: nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
from `nova-sprint help`:
  nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
  nova-sprint run --listen <address>:<port> --land
  ticks; serves the workers' verbs (take, finish, read, queue, fleet beat) on
  <address>:<port>, this machine's address on the fleet's private network (it
  checks no credential, so a name, a public address, a link-local address
  and an every-network address such as 0.0.0.0 are refused); serves the
  coordinator's verbs on 127.0.0.1:<port>; with --land
  lands what the readers passed, so land is not run by hand beside it.
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answer-rules  answer the mechanical judgments by rule, recorded "answered by rule <name>" (work came back failed: a harness fault or a HOLD with findings reworked on its tier with the failure as its fix, any other failure redealt, then a tier up; a card at its bound: a tier up, heavy to a friend; a late card: a wait once with progress, else returned and redealt; a conflict in a file no ledger owns: returned, redone on the tip, resumed; the same finding twice: marked a brief defect); nova-config's sprint row answer_rules_off turns single rules off; --answer-rules=false leaves every judgment to the coordinator (run answers by default, a tick by hand only with --answer-rules); nova-sprint rules prints what they would answer now
  --cpuprofile <string>  write a CPU profile of the loop's first ticks to this file (see --profile-ticks)
  --decide <dir>  also keep the record of the sprint's attempt and grade decisions in this dir (nova-decide's layer 2: attempt.jsonl, grade.jsonl): the finishes' attempt decisions recorded, every card graded before its first deal with JEV_API_KEY from this environment or read in this process when --keys or keys.json names it, and each decision's outcome attached when its card lands or is dropped, every 5s
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --idle-alarm  when the fleet works under half its width for 5m0s while cards wait, push the coordinator one note (the inbox, and inbox --push) naming the roots the waiting cards are behind, the most cards first, once an episode, and one more when it recovers (run: on by default; a tick by hand only with --idle-alarm)
  --json  print one JSON object for a program instead of the lines
  --keys <NAME,...>  the NAME,... of secrets this process reads from the seat login's nova-secrets seat (also recorded as keys.json beside the login): the decision key and each provider key. A name that cannot be read refuses at start. The unit's environment carries no key value
  --land  also land what the readers passed, every 2s, one landing at a time, as the coordinator (land's defaults: each card's REPO: and BASE: lines); every cycle prints one line, and a landing still running after 10m0s raises one judgment naming the stage; land is then not run by hand
  --land-parallel <int>  with --land, how many streams each landing merges at once before it lands them one at a time (land --land-parallel)
  --listen <address:port>  also be the sprint's server: the workers' verbs on this address:port (this machine's address on the fleet's private network; a name, a public address, a link-local address, and an every-network address are refused), where nova-swarm member --server <address>:<port> sends them, and the coordinator's verbs on 127.0.0.1 at the same port, where NOVA_SPRINT_SERVER=127.0.0.1:<port> sends them
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --profile-ticks <int>  the ticks --cpuprofile covers; the profile is written after the last of them
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --tick-deadline <duration>  the least time a tick may take before it is given up (stretched to 3 x the median wall of the last 20 ticks, at most 1m0s): past it the stacks are printed, the tick's plan is given up and the loop goes on; three wedged ticks in a row (given up and not stopped within a further deadline) exit 4 so the supervisor starts the loop again (0: wait for ever)
exit codes: 0 stopped (an interrupt), 2 usage or a store that did not answer, 3 its binary was replaced on disk (its supervisor starts the new one)
```

`nova-sprint tick -h`:

```
usage: nova-sprint tick [--answer-rules] [--idle-alarm] [--shadow]
from `nova-sprint help`:
  nova-sprint tick [--answer-rules] [--idle-alarm] [--shadow]
  nova-sprint tick
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answer-rules  answer the mechanical judgments by rule, recorded "answered by rule <name>" (work came back failed: a harness fault or a HOLD with findings reworked on its tier with the failure as its fix, any other failure redealt, then a tier up; a card at its bound: a tier up, heavy to a friend; a late card: a wait once with progress, else returned and redealt; a conflict in a file no ledger owns: returned, redone on the tip, resumed; the same finding twice: marked a brief defect); nova-config's sprint row answer_rules_off turns single rules off; --answer-rules=false leaves every judgment to the coordinator (run answers by default, a tick by hand only with --answer-rules); nova-sprint rules prints what they would answer now
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --idle-alarm  when the fleet works under half its width for 5m0s while cards wait, push the coordinator one note (the inbox, and inbox --push) naming the roots the waiting cards are behind, the most cards first, once an episode, and one more when it recovers (run: on by default; a tick by hand only with --idle-alarm)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --shadow  plan the tick on the store and apply nothing: the store is opened read-only (every write a refusal), no beat, heartbeat, repair or restore is written, and the plan is printed, part by part, with its size and time; server switch runs it as the canary of a new binary
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint selftest land -h`:

```
usage: nova-sprint selftest land [--binary <path>] [--scratch-dir <dir>]
from `nova-sprint help`:
  nova-sprint selftest land [--binary <path>] [--scratch-dir <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --binary <string>  the binary to test (default: this binary)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --scratch-dir <string>  scratch directory for the clone (default: temporary directory)
exit codes: 0 done, 1 failed (lander broken or card did not land), 2 usage
effect: inspection: lands a canned card on a scratch clone with this binary, writes nothing to the sprint
```

`nova-sprint selftest -h`:

```
usage: nova-sprint selftest [--dir <d>] [--keep]
from `nova-sprint help`:
  nova-sprint selftest land [--binary <path>] [--scratch-dir <dir>]
  nova-sprint selftest [--dir <d>] [--keep]
flags:
  --dir <string>  the directory to make the selftest's fresh directory in (default: the system's temporary directory)
  --keep  keep the fresh directory after the selftest (it is removed on success unless this is given; a failure always keeps it, named in the line)
exit codes: 0 the selftest landed its card through the tree gate (SELFTEST OK), 1 it did not (SELFTEST FAILED names the step, the why and the kept directory), 2 usage
effect: local write: makes a fresh directory, a bare origin and a clone whose base holds a go module, runs the card's flow of the walkthrough on a twin file in it and lands one card through the tree gate; writes only in that directory, opens no store of the caller's and no network, and removes it unless --keep
```

`nova-sprint goal set -h`:

```
usage: nova-sprint goal set <name> [--file <path>] [--to file:<path>]
from `nova-sprint help`:
  nova-sprint goal set <name> [--file <path>] [--to file:<path>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --file <string>  the file that holds the goal text: what this person is to keep doing
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --to <string>  how to reach the person: file:<absolute path> (default: a file this verb prints); bus is not built
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint goal show -h`:

```
usage: nova-sprint goal show [<name>]
from `nova-sprint help`:
  nova-sprint goal show [<name>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the goals in the sprint's store, writes nothing
```

`nova-sprint goal drop -h`:

```
usage: nova-sprint goal drop <name>
from `nova-sprint help`:
  nova-sprint goal drop <name>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint take -h`:

```
usage: nova-sprint take --as <member> [<card>@<gen>...] [--epoch <n>] [--max <n>]
from `nova-sprint help`:
  nova-sprint take --as <member> [<card>@<gen>...] [--epoch <n>] [--max <n>]
  nova-sprint take --as m1 --epoch 0
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the fleet member taking its cards; several, comma separated, each take from their own ready queue in one step
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, the first n of its ready queue (omitted, 1); with several members, n of each
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint finish -h`:

```
usage: nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]
from `nova-sprint help`:
  nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]
  nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --head "$(git -C work rev-parse HEAD)" --report done
  nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report done
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the fleet member finishing its cards; several, comma separated, each finishing its own named cards in one step
  --base <string>  the branch the work started from
  --branch <string>  the branch the work is on (its packet names the one to use)
  --decision <string>  the take's attempt decision, one JSON record line as nova-decide makes it (a work member with JEV_API_KEY asks it for every take): its op naming this take's card and attempt, else the finish is refused; kept on the card, recorded by the server's decide lane, and a failed finish whose class is no-result or nothing-to-do at or above that class's bar on the card is routed by it (docs/SPEC-SPRINT.md section 2)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --failed  the work failed (default: ok); a card named by id whose attempt a deadline failed already, with no later attempt started, is finished by this report, LAND or HOLD, rather than refused
  --head <string>  the commit the work finished at, the head land merges (default: the card's id, for a run with no git: land refuses a head that is not a commit id)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --report <string>  the worker's report
  --usage <string>  what the run spent, one line (the member passes its child's budget, wall, tokens by class and cost): kept on the attempt's record, timed and priced
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint progress -h`:

```
usage: nova-sprint progress --as <worker> <card>[@<gen>]... --epoch <n>
from `nova-sprint help`:
  nova-sprint progress --as <worker> <card>[@<gen>]... --epoch <n>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the fleet member or friend that holds the cards: only the holder stamps a card's progress
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint ask -h`:

```
usage: nova-sprint ask [<id>... | --group <id> [--expect <n>]] [--stream <s>] [--max <n>] [--another] [--answers <note>]
from `nova-sprint help`:
  nova-sprint ask [<id>... | --group <id> [--expect <n>]] [--stream <s>] [--max <n>] [--another] [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --another  one more reader for a primary already asked
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --instead <string>  take back this reader's read (asked or reading) of the one primary named and ask one other reader, as --another chooses
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: asks a reader for each named primary (one more with --another, another in place with --instead) in the sprint's store; --dry-run writes nothing
```

`nova-sprint queue -h`:

```
usage: nova-sprint queue --as <reader|member> | --stream <s>
from `nova-sprint help`:
  nova-sprint queue --as <reader|member> | --stream <s>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  a reader (its read cards, asked then reading) or a fleet member (its work cards, ready then working)
  --col <string>  with --stream: waiting lists the stream's waiting primaries, each with what it still waits for
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --have <string>  with --packets: the cards, comma separated, the worker wants no packet for (it runs them, or will not start them yet)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --packets <string>  with --as: the packets the worker wants, so the answer carries only those (every other card is listed with its id, column, attempt and gen, and the answer's epoch, with no packet): the first n cards it may start (asked, ready) and every in-flight card (reading, working), each not named by --have; without it every card carries its packet
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  a stream: its merge queue, then its stuck cards
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads a worker's or a stream's cards, writes nothing
```

`nova-sprint read -h`:

```
usage: nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>[@<gen>]...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
from `nova-sprint help`:
  nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>[@<gen>]...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
  nova-sprint read --as reader-a --begin --epoch 0
  nova-sprint read --as reader-a --ok --epoch 0
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the reader; use read-card IDs from queue --as <reader>; several readers, comma separated, each reporting its own named read cards in one step
  --begin  asked -> reading; a named queued packet uses <read-card>@<gen>, while --max selects the live queue
  --broken  the read found it broken
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --finding <string>  what the read found; with --broken it names the file (file:line), the line, or the card's STEP or RULE the work breaks, and what to change, or the read is refused
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, the first n of the reader's queue (omitted, 1)
  --ok  the read found it good
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  with --return: why the read has no verdict (it reaches the inbox)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --return <string>  hand back a read the reader holds and has no verdict on: not a read; the next tick asks it of another reader free at the attempt, or of this reader again; no finding against the work
  --usage <string>  with --ok, --broken or --return: what the read spent, one line (the reader passes its child's tokens, wall and cost): kept on the read card, timed and priced
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint accept -h`:

```
usage: nova-sprint accept (<id>... [--heavy --evidence <path> --reason <text>] | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]
from `nova-sprint help`:
  nova-sprint accept (<id>... [--heavy --evidence <path> --reason <text>] | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --evidence <string>  with --heavy: the path of a readable file the coordinator's heavy read rests on; its sha256 is recorded beside it
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --heavy  the coordinator's own heavy read of each named primary in review, at its attempt and head: one ok read toward its read rule, recorded on the primary under coordinator:<actor> with --evidence and its sha256, never as a reader's read; a reader's broken read at the attempt stays, marked overruled; wants ids, --evidence and --reason
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --read-ok  every primary in review with the ok reads it needs (one reader's for a flash card, two different readers' for a pro card); moves eligible primaries into the merge queue. The tick does this itself every tick and tells the seat (ready to merge); the verb is for a stuck case, and says "nothing waits: the tick accepts" when there is nothing
  --reason <string>  with --heavy: why the coordinator's read stands, kept on the primary
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: moves the eligible primaries in review into the merge queue, or records the coordinator's heavy read on each named primary, in the sprint's store; --dry-run writes nothing
```

`nova-sprint rework -h`:

```
usage: nova-sprint rework (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>] [--tier <tier>] [--answers <note>] [--one]
from `nova-sprint help`:
  nova-sprint rework (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--fix <text>] [--tier <tier>] [--answers <note>] [--one]
  nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1
  nova-sprint rework s2-4 --fix '<fix>'
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --fix <string>  the fix for every primary; without it each takes its own: the finding of its broken read, or the report of its failed work; the next attempt is staged at the tip of the card's base branch, the last pushed attempt's work carried on top where it applies cleanly, and where it does not the child is told that work must be redone
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
  --tier <string>  the tier (frontier, heavy, pro or flash) this attempt and every later deal of the card draws its route from, over its brief's line 1, kept on the card: it pins the card, never escalated past it (flash first); a card whose brief pins a model is refused; at a redeal bound it never names a lower tier, and when the attempt before also ended at its bound on the card's tier the rework is refused unless it names a tier above (flash, pro, heavy, frontier) or the provider its takes failed on is back, which lifts it once per tier per card
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint return -h`:

```
usage: nova-sprint return (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>] [--answers <note>]
from `nova-sprint help`:
  nova-sprint return (<id>... | --group <id> [--expect <n>] | <selector> [--dry-run]) [--reason <text>] [--answers <note>]
  nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why it goes back to review
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint redo -h`:

```
usage: nova-sprint redo <card>... [--stream <s>] [--answers <note>]
from `nova-sprint help`:
  nova-sprint redo <card>... [--stream <s>] [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: returns, reworks and resumes the named conflicted cards in one step in the sprint's store; --dry-run writes nothing
```

`nova-sprint drop -h`:

```
usage: nova-sprint drop (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text> [--answers <note>] [--one]
from `nova-sprint help`:
  nova-sprint drop (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>] | <selector> [--dry-run]) --reason <text> [--answers <note>] [--one]
  nova-sprint drop --group finish-0314a1b2-1.1 --expect 2 --reason '<why>' --answers finish-0314a1b2-1.1
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --cascade  drop too every waiting primary that needs a card named, and their dependants, with the same reason; without it a card another waiting primary still needs is refused, naming the dependants
  --col <string>  the cards in one column (a state)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why it leaves the table; kept with its record
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo <value>  only the cards of the streams recording this repository (owner/name), comma separated or repeated; needs --expect <n>, the number of streams it selects
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint priority -h`:

```
usage: nova-sprint priority <id>... | (<id>... | --stream <s>) (--blocker | --critical | --fix | --high | --normal | --low) --reason <text>
from `nova-sprint help`:
  nova-sprint priority <id>... | (<id>... | --stream <s>) (--blocker | --critical | --fix | --high | --normal | --low) --reason <text>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --blocker  set the level blocker
  --critical  set the level critical
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --fix  set the level fix
  --high  set the level high
  --json  print one JSON object for a program instead of the lines
  --low  set the level low
  --max <int>  listed items of each kind; 0 is all
  --normal  set the level normal
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why (required to set), recorded on each card's timeline, and the stream's, with the actor
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  one call sets every card now in the stream, whatever its column and whatever level it had (its own included), and the stream's default for cards added later (a later card's own PRIORITY line wins over the default); a held stream is set and the output says it is held
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: with a level, sets it on the named cards or the stream in the sprint's store; with none, prints the levels and writes nothing; --dry-run writes nothing
```

`nova-sprint unpin -h`:

```
usage: nova-sprint unpin (<id>... | --stream <s>) --reason <text> [--dry-run]
from `nova-sprint help`:
  nova-sprint unpin (<id>... | --stream <s>) --reason <text> [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  show the planned unpins and refusals without writing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  required reason recorded with the dropped WHO line
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  unpin unstarted cards in this stream; report each refusal
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint rebase -h`:

```
usage: nova-sprint rebase --from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]
from `nova-sprint help`:
  nova-sprint rebase --from <branch> --to <branch> [--repo-dir <clone>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  show the planned rebases and refusals without writing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --from <string>  the base branch the cards name now, on their BASE: line
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  a clone of the cards' repository, its branches as fetched, for the one git merge-base --is-ancestor --from --to that a dealt card needs; without it, a dealt card's head is not checked
  --to <string>  the branch that replaces it: it must contain --from (--repo-dir checks it, git merge-base --is-ancestor)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint rank -h`:

```
usage: nova-sprint rank (<id>... | <selector> [--dry-run]) (--score <n> | --first | --before <id>) [--answers <note>]
from `nova-sprint help`:
  nova-sprint rank (<id>... | <selector> [--dry-run]) (--score <n> | --first | --before <id>) [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --before <string>  in line in front of this primary of the cards' own stream, in the order named, placed as add --before places cards (the line is never renumbered)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --first  ahead of every primary
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --score <string>  the new score of the first id; the rest follow it
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint relink -h`:

```
usage: nova-sprint relink <old-id>[,<old-id>...] <new-id> [--reason <text>]
from `nova-sprint help`:
  nova-sprint relink <old-id>[,<old-id>...] <new-id> [--reason <text>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  say what would be relinked and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why the twin replaces the old card, recorded with the answer of each blocked judgment it closes
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: re-points what waited on the old cards to their twin in the sprint's store and answers their blocked judgments; --dry-run writes nothing
```

`nova-sprint recut -h`:

```
usage: nova-sprint recut <id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>]) [--new <id>] | <selector> (--tier <t> | --set-base <branch> | --drop-who)... [--dry-run]
from `nova-sprint help`:
  nova-sprint recut <id> (--tier <flash|pro|heavy|frontier> | --brief-file <path> [--rules <file>]) [--new <id>] | <selector> (--tier <t> | --set-base <branch> | --drop-who)... [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --brief-file <string>  the twin's brief, read from this file and held to the card lint as brief holds one; its DEPENDS-ON: line's needs are taken with the old card's (default: the old card's brief)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --new <id>  the twin's id (default: the old id with the next letter, b for a card never re-cut, c for its twin re-cut, and so on)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  retired with --widen: give it to brief <id> --widen
  --rules <string>  with --brief-file: the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)
  --tier <string>  the tier the twin is pinned to (frontier, heavy, pro or flash): every deal and read of it draws its route from it (default: the old card's pin)
  --widen  retired: a PATHS widening edits the card in place, the same id; run: nova-sprint brief <id> --widen
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint twin -h`:

```
usage: nova-sprint twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]
from `nova-sprint help`:
  nova-sprint twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --before <string>  the card of the stream the twin stands in front of (default: the card twinned, its place)
  --carry  the twin starts from the pushed head of the card's latest attempt: THE TASK names that branch and head, and the brief's CARRY: line holds it; refused, exit 1, when no attempt pushed a full head
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --instruction <string>  the correction, written verbatim at the head of the brief's THE TASK
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --needs <string>  cards the twin needs besides the card's own needs, comma separated
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --paths <string>  globs joined to the brief's PATHS: line, comma separated, each once (a glob that climbs out with .. is refused)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --tier <string>  the tier the twin is pinned to (frontier, heavy, pro or flash; default: the card's pin)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: replaces the card by its twin in the sprint's store (a merging card is returned first, then twinned; its dry run plans the return alone); --dry-run writes nothing
```

`nova-sprint brief -h`:

```
usage: nova-sprint brief <id> (--brief <text> | --brief-file <path>) [--rules <file>] [--answers <note>] | --dir <dir> [--rules <file>] | --group <id> [--expect <n>] (--brief-file <path> | --dir <dir>) [--answers <note>] | <id> --widen [--repo-dir <clone>] | <id> --tier <flash|pro|heavy|frontier> | <selector> (--set-base <branch> | --drop-who | --tier <t>)... [--dry-run]
from `nova-sprint help`:
  nova-sprint brief <id> (--brief <text> | --brief-file <path>) [--rules <file>] [--answers <note>] | --dir <dir> [--rules <file>] | --group <id> [--expect <n>] (--brief-file <path> | --dir <dir>) [--answers <note>] | <id> --widen [--repo-dir <clone>] | <id> --tier <flash|pro|heavy|frontier> | <selector> (--set-base <branch> | --drop-who | --tier <t>)... [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --brief <string>  the new brief: a child's whole brief, at most 16 KiB, held to the card lint as add holds one (--rules, else the file init --rules recorded, else the built-in general rules) and to the same PATHS check at the BASE tip (a literal path must exist, a glob must match a file, and every func, type or verb STOP or START names with a file must be inside a PATHS file; a CARRY: head= brief, such as --widen writes, skips the existence check) and refused, exit 2, nothing written, when it fails; a primary waiting, ready or in review takes one in place, on a STOPPED machine or a RUNNING one (there applied at its next tick), keeping its id: one an attempt was dealt for opens its next attempt, staged from its last pushed head, its bound reset; a card working, merging or landed keeps its brief; one that differs in its DEPENDS-ON: line alone is taken in any state, the machine running or the card dealt, and re-points the card's needs
  --brief-file <string>  the new brief, read from this file: its bytes as they are, its one trailing newline cut; not with --brief
  --dir <string>  a directory of new briefs: one per *.md file, the card its base name without .md, each read and held as --brief-file's; one bad file refuses the whole call, nothing written; not with an id, --brief or --brief-file; with --group, one file for each member of the group and no other
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --group <string>  the members of the inbox group of this id (the id or alias inbox prints): a group of one takes --brief or --brief-file, a group of several --dir
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  with --widen: the clone the base's and the head's files are read in (default: land's clone of the card's REPO:)
  --rules <string>  the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)
  --tier <string>  re-tier the card instead of replacing its brief: the tier (frontier, heavy, pro or flash) every later deal and read of the card draws its route from, kept on the card as rework --tier keeps it (it pins the card, never escalated past it); taken on a RUNNING machine and for a card dealt, where it applies to the next attempt; not with --brief or --brief-file
  --widen  the card's brief edited in place with its PATHS widened by the PATHS-PROPOSED line of its latest attempt's report (the paths before any prose on that line) and a CARRY: line naming that attempt's pushed head, its next attempt starting from it; the same id, no twin; refused, exit 1, for no line, a glob that climbs out with .. or names no file at the base or the head
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint move -h`:

```
usage: nova-sprint move <id>... --stream <s> [--before <id> | --after <id> | --score <n>]
from `nova-sprint help`:
  nova-sprint move <id>... --stream <s> [--before <id> | --after <id> | --score <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --after <string>  place the cards in line after this primary of the stream
  --before <string>  place the cards in line in front of this primary of the stream
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --score <string>  the first card's score in its new line; the rest follow it (default: after every primary)
  --stream <string>  the stream the cards move to: one of the sprint's, or a new one, made as add makes it
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: moves the named cards to the stream, in line where --before, --after or --score says, in the sprint's store; --dry-run writes nothing
```

`nova-sprint merge -h`:

```
usage: nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]
from `nova-sprint help`:
  nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]
  nova-sprint merge --stream s1 --batch 1
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --base-red <string>  fact: the base fails its tree gate, this the error (land's base-gate rule, after its third failure): the stream stops, no card moves
  --base-ref <string>  with --landed: the fetched tip of the base branch in --repo (origin/<base>)
  --batch <int>  the batch: the head n of the stream's queue (the lander's selection; to record a landing name the cards with --landed)
  --conflict <string>  fact: this card of the batch did not merge; on its own head (a file conflict, the lander's checks, the tree gate its base passes, as --note and --conflict-kind say) it is reworked at the base's tip, or returned for the widen rule, and the stream goes on; otherwise the stream stops
  --conflict-kind <string>  with --conflict: file (a path no generated ledger owns did not merge: the card's own, reworked at the tip) or ledger (a generated ledger the lander could not resolve: the stream stops)
  --conflict-path <value>  with --conflict: a path that did not merge; again, or comma separated, for more
  --cross <string>  fact: <card>=<other>: the card needs <other> first; <other> is on the table, in another stream, not landed
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --landed <value>  the record by name: <id>@<head> of each card pushed, again or comma separated; each must be merging in --stream at that head and the head an ancestor of --base-ref in --repo, or all are refused and nothing is written
  --max <int>  listed items of each kind; 0 is all
  --note <string>  what the facts' source said
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --red  fact: the stream branch went red on the batch
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --rejected  fact: the merge queue rejected the batch
  --repo <string>  with --landed: a clone whose --base-ref is fetched; git merge-base --is-ancestor runs there, once per card
  --stream <string>  the stream whose queued batches are selected to merge and land
  --suspect <value>  with --red: a card of the batch suspected of turning it red; again, comma separated, or ids after it for more
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint land -h`:

```
usage: nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
from `nova-sprint help`:
  nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
  nova-sprint land --stream s1 --repo-dir work --base sprint/s1
  nova-sprint land --stream s1 --check 'make test'
  merges each queued card's head (--no-ff) in queue order onto a branch cut
  from origin's base, one batch per run of cards naming one REPO: and BASE:
  (--base for a card naming none); runs --check once per batch; pushes, never
  forced, rebuilding once on a moved base; then reports the batch as merge
  --stream s1 --batch <n> does. A head missing or in conflict ends the batch
  before it and is reported as merge --conflict, a red check as --red, a
  second rejected push as --rejected. Each head merged is checked first, by
  script and no model: a head whose diff changes a file outside its brief's
  PATHS, or leaves a stranded sentence fragment or an unmatched backquote in
  prose, ends the batch as a head in conflict does. Tests, testdata, tla/RUNS.tsv and
  tla/CASES.tsv, the docs catalog and AGENTS.md maps are inside every PATHS. A card that adds a directory
  owns its catalog row and the AGENTS.md maps; a conflict only in those maps, or
  those maps and added catalog rows, resolves as the ledgers do. A conflict only
  in the generated ledgers lands: the tip's side, then their tests' update run
  (NOVA_CI_UPDATE=1) to a fixed point, one commit. A refusal of the card's
  own head (a file conflict, the lander's checks, a merged tree failing the
  tree gate its base passes) stops nothing: the card is reworked at the
  base's tip (the refusal its fix, the seat told once), or for files outside
  its PATHS returned for the widen rule, and the stream's other cards land in
  the same pass; a card at its brief's bound goes back to review instead. A
  ledger the lander could not resolve, or a head origin does not hold, stops
  the stream, and after resume land merges the head again. The clone is --repo-dir,
  else the dir= each line names; git uses the caller's environment. A kept
  clone a pass cut short left not clean is restored to the fetched base before
  the batch (LAND CLEANED names the files); a --repo-dir one is refused. After
  the whole pass each landed merge diff is scored (nova-decide's score
  decision, with the key JEV_API_KEY holds, a minute for the pass; recorded in
  decide/score.jsonl under the land root): a batch whose cards' top class meets
  the sprint row's decide_score_bar (empty: none) raises one judgment, landed
  work scored low, listing them (scored=, judged=).
  nova-sprint land --land-parallel 4
  merges the streams' batches beside each other, up to that many at once, each in
  its own worktree of the clone (<clone>@<stream> under the land root), each batch's
  tree gated once as a whole (a red gate then gates its heads one by one, to blame
  the head); then lands the green batches one at a time in priority order: a batch
  cut from the tip the base still has is pushed with no new gate; one whose base moved
  (a batch landed before it) is merged again onto the new tip and pushed with no new
  gate when it changes no file the landings since touched, else gated once combined,
  and a red combined gate refuses it for this pass, naming what it collided with, and
  stops no stream. 1 merges the streams one after another.
  nova-sprint land --stream s1 --dry-run
  reads the store only: no git, no push, no report. The window: land pins
  each card's head and attempt as it reads them; a caller's --epoch is held
  before any git, the queue, the heads and the epoch again just before the
  push, and the report lands the batch, its cards by name, only while the queue
  holds each of them at that head at that epoch (one store step); a card
  accepted or ranked ahead since changes nothing. A clear, a return, a
  rework or a crash after the check leaves the push unreported (LAND
  FAILED, exit 2; run land again, never a bare merge, and its own checks
  decide: an unchanged card is recorded with no new push, a reworked one
  merged at its new head or met in conflict); a clear there pushes for an epoch
  just left: nothing is recorded for it, and the push is not undone.
  nova-sprint land --stream s1
  run again, it recovers once the outside is quiet (tla/Land.tla, Recovers),
  not otherwise: a run cut short between the push and the report on every
  try never reports; and a base that moves twice between the read and the
  push gives up (one rebuild, then the rejected fact, the stream stopped):
  nothing is pushed or lost and the cards stay queued; resume the stream
  (nova-sprint resume --stream s1 --did 'the base moved') and run land again.
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --base <string>  the base branch of a card whose brief names no BASE: line
  --check <string>  a command run once per batch, by sh -c in the clone on the batch branch, before the push (bounded to 30m); non-zero reports the batch red and pushes nothing
  --dry-run  print the batches it would land and change nothing: reads the store only (no git, no push, no report)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --land-parallel <int>  how many streams merge at once, each in its own worktree of the clone, before the landings go one at a time (landpass.go); 1 merges the streams one after another
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  the clone to land from, its origin the remote pushed to; each stream's batch is built in a worktree of it under the land root (default: a clone per repository under the directory each line names)
  --stream <value>  a stream to land (again, or comma separated, for more; default: every stream with cards queued to merge and not stopped)
exit codes: 0 every batch landed (--dry-run: would land), 1 a batch was refused (its line names the next step), 2 usage, a store that did not answer, or a push that landed and was not reported (run land again)
```

`nova-sprint landed -h`:

```
usage: nova-sprint landed <id>... --sha <commit> --reason <text> [--repo-dir <clone>] [--base <branch>]
from `nova-sprint help`:
  nova-sprint landed <id>... --sha <commit> --reason <text> [--repo-dir <clone>] [--base <branch>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --base <string>  the base branch of a card whose brief names no BASE: line
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  how the work got there, in a few words (required): a pass cut short after its push, a pull request
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  the clone to check in, its origin the card's repository (default: land's clone per repository)
  --sha <string>  the commit on origin/<base> the cards' work is in, 7 to 40 hex digits (required)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint snapshot -h`:

```
usage: nova-sprint snapshot (--dir <dir> [--keep <n>] [--every <duration>] | --restore-drill <file>)
from `nova-sprint help`:
  nova-sprint snapshot (--dir <dir> [--keep <n>] [--every <duration>] | --restore-drill <file>)
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the snapshots are written to (required unless --restore-drill)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  take a snapshot now and again each time this passes, until interrupted (default: once)
  --json  print one JSON object for a program instead of the lines
  --keep <int>  how many verified snapshots stay; older ones are pruned after a newer one verifies
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --restore-drill <string>  check this snapshot file's integrity (its checksum, the RDB's header, version and CRC-64) and print its counts; not a semantic restore; the live store is never opened
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint backup -h`:

```
usage: nova-sprint backup (--out <dir> [--part-bytes <n>] [--secrets-store <dir> --secrets-as <seat> --secrets-key <path> --sops <path>] | --file <path> [--dry-run])
from `nova-sprint help`:
  nova-sprint backup (--out <dir> [--part-bytes <n>] [--secrets-store <dir> --secrets-as <seat> --secrets-key <path> --sops <path>] | --file <path> [--dry-run])
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  verify the backup in memory without writing --file; writes nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --file <string>  the file the backup is written to (it must not exist); or --out
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --nova-secrets <string>  the nova-secrets program the --out scan runs under
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --out <string>  the directory the RESTORE dump's parts, SHA256SUMS and README.md are written to (it must not exist or be empty); or --file
  --part-bytes <int>  the largest part of the xz with --out, in bytes (under 100 MB)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --redis-server <string>  the redis-server --out restores a Redis store's dump into, a throwaway on a unix socket
  --scan <string>  the child nova-secrets exec runs: count the values of these variables found on stdin (backup --out runs it)
  --secrets-as <string>  the nova-secrets seat the --out scan reads (default: the seat login's)
  --secrets-key <string>  the seat's age key file (default: the seat login's)
  --secrets-store <string>  the nova-secrets store the --out scan reads (default: the seat login's)
  --sops <string>  the sops program nova-secrets exec runs (default: the seat login's)
  --split <string>  the split program --out splits with
  --xz <string>  the xz program --out compresses with
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: with --out, writes the epoch's keys and the shared keys as a RESTORE text dump, xz -9, split into parts under 100 MB, with SHA256SUMS and a README section, after restoring the parts into a throwaway store under this build's function library, comparing the counts, and scanning for every nova-secrets value under nova-secrets exec (counts only); --out is written only when every step passed. With --file, writes the store to a new owner-only file, restores it into a twin, compares and scans it for secrets, and removes the file when any step fails; --dry-run writes nothing; the store is only read
```

`nova-sprint demo load -h`:

```
usage: nova-sprint demo load <backup.xz part>... [--sha256 <hex>] [--dir <dir>] [--xz <path>] [--redis-server <path>]
from `nova-sprint help`:
  nova-sprint demo load <backup.xz part>... [--sha256 <hex>] [--dir <dir>] [--xz <path>] [--redis-server <path>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the demo's Redis directory and state file are kept under (default: the user cache directory's nova-sprint/demo)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --redis-server <string>  the redis-server the demo runs, on a free 127.0.0.1 port
  --sha256 <string>  the sha256 of the joined .xz, when no <joined>.sha256 or SHA256SUMS sits beside the parts
  --xz <string>  the xz program the backup is decompressed with
exit codes: 0 the demo is up and where read it, 1 failed (a damaged backup, a demo already up, a Redis that did not start or a line not replayed: what this load started is stopped and removed), 2 usage
effect: local write: starts a throwaway Redis on a free 127.0.0.1 port in a directory of its own under --dir, loads this build's function library and the backup into it, and records its port, pid and directory in the state file there; the live store is never opened
```

`nova-sprint demo stop -h`:

```
usage: nova-sprint demo stop [--dir <dir>]
from `nova-sprint help`:
  nova-sprint demo stop [--dir <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory demo load kept its state under (default: the user cache directory's nova-sprint/demo)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 stopped and removed, 1 failed (no demo up, or a recorded pid or directory that is not the demo's: nothing stopped or removed), 2 usage
effect: local write: stops the Redis demo load started (the pid in its state file, only when the Redis at the recorded address is that pid) and removes the recorded directory and the state file; nothing else
```

`nova-sprint promote -h`:

```
usage: nova-sprint promote [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
from `nova-sprint help`:
  nova-sprint promote [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --base <string>  the branch the pull request targets, fetched and merged into the cut first (default dev)
  --branch <string>  the live sprint branch; the cut is taken from origin/<branch> after a fetch, never a local ref (default: the checkout's current branch)
  --check <string>  the tree gate, a command run in a private checkout of the merged cut before the pull request (default: none)
  --dry-run  print the branch and the landed cards, or the promotion in flight, and write, enqueue and record nothing: no ref, no config, no push, no pull request, no store write (it reads origin's tips with ls-remote and fetches their objects)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  how often to look, a duration (default 1h); each pass promotes when that long has passed or --landings cards have landed
  --json  print one JSON object for a program instead of the lines
  --landings <int>  also promote once this many cards have landed since the last promotion (0: the clock only)
  --max <int>  listed items of each kind; 0 is all
  --once  carry one promotion from the cut to the recorded merge (or to the judgment or refusal that stops it) and exit
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --poll <duration>  how often to look while a promotion is in flight, its checks or its merge queue (default 1m)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  the clone the branch is cut in (default: the current directory)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: delivery: promotes the landed cards toward the development branch and records the promotion in the sprint's store; --dry-run prints the branch and the landed cards and changes nothing
```

`nova-sprint resume -h`:

```
usage: nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
from `nova-sprint help`:
  nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
  nova-sprint resume --stream s2 --did 'returned s2-4' --answers merge-0315c3d4-1.1
  nova-sprint resume --stream s2 --did 'returned s2-4 for rework' --answers merge-0315c3d4-1.1
  nova-sprint resume --stream s2 --did '<what you did>' --answers merge-0315c3d4-1.1
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing, except an id the machine answered already since the inbox was read, which is a NOTE
  --did <string>  what the coordinator did about the cause; required after a red branch
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <value>  a stopped stream; again, or comma separated, for more (each is resumed or refused on its own line, and the exit is 1 when any is refused)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: resumes each named stopped stream in the sprint's store, with what was done; --dry-run writes nothing
```

`nova-sprint hold -h`:

```
usage: nova-sprint hold <member|reader|friend|stream>... --reason <text> [--return] [--dry-run]
from `nova-sprint help`:
  nova-sprint hold <member|reader|friend|stream>... --reason <text> [--return] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  check the names and the reason, print what would be held or released, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --repo: the number of streams it selects, as nova-sprint streams --repo <owner/name> printed it
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why, in words: shown beside the held status and kept in the log; a hold wants one
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo <value>  also hold or release the streams recording this repository (owner/name), comma separated or repeated; needs --expect <n>, the number of streams it selects
  --return  hand back the work begun now too: a member's working cards dealt round the fleet, a reader's reads begun asked of another, a stream's working cards withdrawn to ready (default: what is begun finishes, but a held friend keeps no begun card either way)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: holds the named members, readers, friends or streams in the sprint's store (--return also hands back their begun work); --dry-run writes nothing
```

`nova-sprint unhold -h`:

```
usage: nova-sprint unhold <member|reader|friend|stream>... [--reason <text>] [--dry-run]
from `nova-sprint help`:
  nova-sprint unhold <member|reader|friend|stream>... [--reason <text>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  check the names and the reason, print what would be held or released, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --repo: the number of streams it selects, as nova-sprint streams --repo <owner/name> printed it
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why, in words: shown beside the held status and kept in the log; a hold wants one
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo <value>  also hold or release the streams recording this repository (owner/name), comma separated or repeated; needs --expect <n>, the number of streams it selects
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: releases the named holds in the sprint's store; --dry-run writes nothing
```

`nova-sprint fleet beat -h`:

```
usage: nova-sprint fleet beat <member> [--load <percent>]
from `nova-sprint help`:
  nova-sprint fleet beat <member> [--load <percent>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --cores <int>  the machine's logical cores the beat reports, instead of this machine's own (a test's, or another meter's); a member with the default width takes half
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --fd-alarm <int>  the machine's open file descriptors above which the beat says alarm and the tick writes one judgment of the member (else NOVA_FD_ALARM, else 150000)
  --fd-warn <int>  the machine's open file descriptors above which the beat says warn and lists the top holders (else NOVA_FD_WARN, else 50000)
  --json  print one JSON object for a program instead of the lines
  --load <string>  the load as a percent of all the machine's cores, instead of measuring it (a test's, or another meter's)
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stop-returns <int>  how many stop-returns the member's lanes still owe after the machine's stop (section 14): start waits for zero
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint fleet up -h`:

```
usage: nova-sprint fleet up <member> [--width <n> | --width 0]
from `nova-sprint help`:
  nova-sprint fleet up <member> [--width <n> | --width 0]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --deadline <string>  pin the deadline every card dealt to the member gets, a duration (45m, 2700s); default takes the pin off: each card's own deadline, or 3 times the member's median run wall over its last 50 ok attempts, whichever is larger
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --width <string>  the member's width: the most work cards it runs at once; the deal holds it at 2 times that, ready and working; 1 to 1024 (default: as it is, 64 for a new member); 0 drains the member: no new deals, its untaken ready cards are levelled away, its working cards finish (fleet down deals them again elsewhere)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint fleet down -h`:

```
usage: nova-sprint fleet down <member>
from `nova-sprint help`:
  nova-sprint fleet down <member>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint fleet sync -h`:

```
usage: nova-sprint fleet sync [--check] [--pg <dsn>]
from `nova-sprint help`:
  nova-sprint fleet sync [--check] [--pg <dsn>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --check  print the drift between the fleet table and the inventory and write nothing: exit 0 when there is none, 2 when there is
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pg <string>  the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done (--check: no drift), 1 refused, 2 usage, a store that did not answer, or (--check) there is drift, 3 the config could not be read
```

`nova-sprint fleet level -h`:

```
usage: nova-sprint fleet level [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint fleet level
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint fleet quiet -h`:

```
usage: nova-sprint fleet quiet <member> (--for <duration> | --until <RFC3339>) --reason <text> | <member> --end [--dry-run]
from `nova-sprint help`:
  nova-sprint fleet quiet <member> (--for <duration> | --until <RFC3339>) --reason <text> | <member> --end [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  check the member, the time and the reason, print the quiet it would set or end, and write nothing
  --end  end the member's quiet now, before its time
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --for <string>  how long the member stays quiet, a duration from now (11m, 1h30m)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why, in words: every worker's QUIET line and the log carry it; a quiet wants one
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --until <string>  when the member's quiet ends, an RFC3339 time (2026-10-04T15:50:00-07:00), instead of --for
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: sets or ends the member's quiet in the sprint's store (the deal gives it nothing until then, every worker's view carries a QUIET line); --dry-run writes nothing
```

`nova-sprint friend sync -h`:

```
usage: nova-sprint friend sync [--pg <dsn>] [--root <dir>]
from `nova-sprint help`:
  nova-sprint friend sync [--pg <dsn>] [--root <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  sync now and again each time this passes, until interrupted, as the sprint's coordinator seat when no --actor is given, read again each pass (default: once); friend sync install --every <d> runs it as this machine's service and friend sync uninstall removes it (-h of each), or the friend sync loop row runs it (docs/FRIENDS.md)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pg <string>  the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --root <string>  the directory holding <root>/<friend>-working for a friend whose nova-config row has no dir (else HOME); the sync delivers and collects each friend's sprint cards in her row's dir, else there, and never writes elsewhere
exit codes: 0 done, 1 refused (a friend row's name, or a working directory that cannot be read), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row; with --every: 0 interrupted (a failing pass is said and the loop goes on), 2 usage, 3 its binary was replaced (its supervisor starts the new one)
```

`nova-sprint collect -h`:

```
usage: nova-sprint collect [<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]
from `nova-sprint help`:
  nova-sprint collect [<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dead-lanes  also finish failed each working card with no report whose friend's runner ENDed its job with report=no (runner.log in her working directory or beside it), so the card is dealt again
  --dry-run  print what would be finished; finish nothing and read no tip
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pg <string>  the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN), as friend sync reads the roster from it
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --root <string>  the directory holding <root>/<friend>-working (else HOME), a friend's working directory when her nova-config row has no dir (nova-config friend set <friend> --dir); collect reads every friend's outbox in her working directory and writes nothing in it
exit codes: 0 done (each card on its line, a refused or left one among them, read again by the next collect), 1 refused (a friend not on the roster), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row
effect: store write: finishes each working card of the friends' rows that a report in any friend's outbox (or, with --dead-lanes, her runner's END with no report) finishes, as her row; reads the friends' working directories and writes nothing there, and reads origin's tip (one git ls-remote) for each LAND; --dry-run writes nothing and reads no tip
```

`nova-sprint friend beat -h`:

```
usage: nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>] [--check <nonce>] [--pong <nonce>] [--run <id>]
from `nova-sprint help`:
  nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>] [--check <nonce>] [--pong <nonce>] [--run <id>]
  nova-sprint friend beat <friend>, which her nova-friend daemon runs every 1s
flags:
  --active <string>  the newest write under her working directory and outbox, as her daemon found it, RFC3339: her session's last activity
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --build <string>  the build her daemon runs (its version line's build): a friend come up is told to update when it is not the server's
  --check <string>  the nonce of the SESSION CHECK her daemon just put into her session: the server keeps it, so an answer naming it within 15m0s proves her session
  --daemon-version <string>  this daemon's build stamp, kept on the beat record as daemon_version and read by nova-sprint seat
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --load <string>  her load as a percent, as fleet beat --load gives a machine's
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pong <string>  the nonce of a check her session answered: her session's evidence for 15m0s while her beat is fresh, only when this daemon's run asked it (--check) within 15m0s, and once; anything else, a time included, is a beat with no proof
  --present <string>  when her daemon sent her the present on its start, RFC3339: the snap-to-present step of a friend come up
  --queue <string>  how many jobs she holds queued, as her daemon counts them
  --reason <string>  why she is down until --until, as her daemon read it
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --run <string>  her daemon's run, its generation: a check proves only when its answer names the run that asked it
  --running <string>  the cards she is running now, comma separated (work card ids, her job names or primaries): friend take and friend down leave them with her
  --started <string>  when her daemon started, RFC3339: its generation; a start not seen before raises a status judgment
  --stop-returns <string>  how many stop-returns her lanes still owe after the machine's stop (section 14): start waits for zero
  --until <string>  her daemon's word that she is down until then, RFC3339: her harness at its usage limit or out of credits
  --width <string>  her width as her daemon has it (the deal's is the roster's: friend up --width)
  --working <string>  how many jobs she is working now, as her daemon counts them
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint friend down -h`:

```
usage: nova-sprint friend down <friend> [--reason <text>] [--until <RFC3339>]
from `nova-sprint help`:
  nova-sprint friend down <friend> [--reason <text>] [--until <RFC3339>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why she is held, shown on her row (her model allowance ran out)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --until <string>  when you expect her back, RFC3339, shown on her row
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint friend up -h`:

```
usage: nova-sprint friend up <friend> [--width <n>]
from `nova-sprint help`:
  nova-sprint friend up <friend> [--width <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --width <string>  her width: the jobs she works at once; the deal holds her at 2 times that, ready and working; 1 to 1024 (default: as it is; friend sync sets it to her nova-config row's again)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint friend cards -h`:

```
usage: nova-sprint friend cards <friend> [--json]
from `nova-sprint help`:
  nova-sprint friend cards <friend> [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the cards held on the friend's row, their packets and briefs, writes nothing
```

`nova-sprint friend take -h`:

```
usage: nova-sprint friend take <friend> (<id>... | --all-unstarted) [--reason <text>]
from `nova-sprint help`:
  nova-sprint friend take <friend> (<id>... | --all-unstarted) [--reason <text>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --all-or-nothing  take none of the cards named when any one is refused
  --all-unstarted  take every card of hers she has not started, naming no card
  --dry-run  say which cards would be taken back and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why the cards are taken back, kept on each card ("taken back by the coordinator: <reason>")
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: takes the named cards back from the friend in the sprint's store; --dry-run writes nothing
```

`nova-sprint friend give -h`:

```
usage: nova-sprint friend give <friend> <id>... [--reason <text>]
from `nova-sprint help`:
  nova-sprint friend give <friend> <id>... [--reason <text>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why the cards are given back, kept on the note (default: given back by the coordinator)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: clears the friend's take-back mark on the named cards in the sprint's store; --dry-run writes nothing
```

`nova-sprint friend level -h`:

```
usage: nova-sprint friend level [--actor <string>] [--dry-run] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint friend level
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  say which friends are up to be levelled and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: moves queued cards between the friends' rows in the sprint's store; --dry-run writes nothing
```

`nova-sprint friend health -h`:

```
usage: nova-sprint friend health <friend> (--state up|asleep|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>] | --clear)
from `nova-sprint help`:
  nova-sprint friend health <friend> (--state up|asleep|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>] | --clear)
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --clear  remove her observation instead of recording one, so her status is her session's evidence alone (a card of hers finished, never her beat); takes no other flag but --dry-run
  --dry-run  check the observation and say what would be recorded; record nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --generation <uint>  the seat's generation the daemon read (nova-sprint seat); any other than the seat's now is refused
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --queue <int>  what her pong said she has queued
  --reason <string>  why she is not up, shown on her row while the observation stands (her model allowance ran out)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --seen <string>  when the proof this rests on was seen, RFC3339 (a session pong for up, a daemon pong for asleep, the judgment for down); a proof not newer than the row's, or dated after the server's clock, is refused
  --state <string>  what the keepalive saw: up (her session answered), asleep (her daemon answered, her session did not) or down
  --until <string>  when the daemon expects her back, RFC3339, shown on her row
  --width <int>  what her pong said her width is
  --working <int>  what her pong said she is working
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: records the coordinator's observation of the friend in the sprint's store, or removes it with --clear; --dry-run writes nothing
```

`nova-sprint friend clean -h`:

```
usage: nova-sprint friend clean [--pg <dsn> | --file <path>] [--root <dir>] [--days <n>] [--dry-run]
from `nova-sprint help`:
  nova-sprint friend clean [--pg <dsn> | --file <path>] [--root <dir>] [--days <n>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --days <int>  a done job's clean clones and build output are removed once its REPORT.md is this many days old
  --dry-run  print every removal and listing with the bytes it would free, and remove nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --file <string>  a nova-config store file in PostgreSQL's place (nova-config --file), for trying it with no database
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pg <string>  the config store whose friend rows are the roster, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --root <string>  the directory holding <root>/<name>-working for a friend whose nova-config row has no dir (else HOME, else the user's home); a row's dir is cleaned where it says
exit codes: 0 FRIENDS-CLEAN OK, 1 a removal or a read failed (FRIENDS-CLEAN FAILED names each; the summary is FRIENDS-CLEAN INCOMPLETE) or a friend row's name refused, 2 usage, 3 the config could not be read or holds no friend row
effect: local write: removes the friends' finished job directories and listings past --days under --root; --dry-run prints every removal with the bytes it would free and removes nothing
```

`nova-sprint gc -h`:

```
usage: nova-sprint gc [--machine <m>] [--dry-run] [--max-age <d>]
from `nova-sprint help`:
  nova-sprint gc [--machine <m>] [--dry-run] [--max-age <d>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --ai-root <string>  the AI root the working directories are under (else NOVA_AI_ROOT, else ~/ai, else the one the home's <name>-working links name); an absolute path
  --dry-run  print every removal with the bytes it would free, and remove nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --machine <string>  run gc on this machine (a host name ssh reaches) through the fleet runner, rather than on this one
  --max <int>  listed items of each kind; 0 is all
  --max-age <string>  how old a bench directory, a lander worktree or a job no runner names is before it goes: days (2d) or a Go duration (36h)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 GC OK, 1 a removal or a read failed (GC FAILED names each; the summary is GC INCOMPLETE) or --machine did not answer, 2 usage
effect: local write: removes, on this machine (or --machine's, through the fleet runner), the job directories of finished or absent lanes, reader checkouts of recorded findings, lander worktrees and bench directories past --max-age, and trims the go caches to their cap; never a path under no known scratch root, never a clone with work that is nowhere else; --dry-run removes nothing
```

`nova-sprint friend reconcile -h`:

```
usage: nova-sprint friend reconcile <friend> [--root <dir>] [--dry-run]
from `nova-sprint help`:
  nova-sprint friend reconcile <friend> [--root <dir>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  say what each card working on her row would get (collect, keep, return) and write nothing; the reads of the store and her directory are made
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --root <string>  the directory holding <root>/<friend>-working when her nova-config row has no dir (else HOME); her inbox/QUEUE.json and outbox are read in her row's dir, else there, and nothing is written there
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: finishes each card she reported on and returns each she abandoned, in the sprint's store; reads her inbox/QUEUE.json and outbox, writes nothing in her directory, and reads origin's tip (one git ls-remote) for each LAND it collects; --dry-run writes nothing and reads no tip
```

`nova-sprint lane take -h`:

```
usage: nova-sprint lane take <kind> --machine <m> --as <worker> [--wait <duration>] [--dry-run]
from `nova-sprint help`:
  nova-sprint lane take <kind> --machine <m> --as <worker> [--wait <duration>] [--dry-run]
  nova-sprint lane take go --machine <m> --as <worker> --wait 30m
  a store write: granted (exit 0) while the machine's holders are under its
  width (set --go-lanes, 1 by default) and nobody waits ahead; otherwise
  queued behind the waiters in the order they asked (exit 1, the place in
  the queue). --wait asks again every 5s until granted or the wait is over.
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the worker asking, the holder or waiter the lane records
  --dry-run  check the machine's lanes and say whether the take would be granted or queued; write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --machine <string>  the machine the run is on: its lanes are its own
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --wait <duration>  how long to wait for the grant, asking again every 5s (30m); 0, the default, asks once
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: takes one lane on the machine for the worker, or joins the queue; --dry-run checks availability and writes nothing
```

`nova-sprint lane give -h`:

```
usage: nova-sprint lane give <kind> --machine <m> --as <worker> [--dry-run]
from `nova-sprint help`:
  nova-sprint lane give <kind> --machine <m> --as <worker> [--dry-run]
  nova-sprint lane give go --machine <m> --as <worker>
  gives the lane (or the place in the queue) back when the run exits; the
  head of the queue is granted. A holder that does not take again within 20m0s
  is released, and a grant or a place not claimed within 1m0s.
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the worker giving its lane, or its place in the queue, back
  --dry-run  check whether the worker holds a lane or waits in the queue, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --machine <string>  the machine the run was on
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: gives the worker's lane or queue position back; --dry-run checks whether a lane is held and writes nothing
```

`nova-sprint lane list -h`:

```
usage: nova-sprint lane list [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint lane list
  every machine's holders and queue; where --json --cards carries them as lanes.
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: lists every machine's lanes and holders, writes nothing
```

`nova-sprint reader add -h`:

```
usage: nova-sprint reader add <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
from `nova-sprint help`:
  nova-sprint reader add <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --tiers <string>  the tiers this reader reads, comma separated (frontier, heavy, pro or flash); all names every tier; default, or omitted, is flash on a fleet reader while the store holds routes and every tier on a friend's reader
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint reader set -h`:

```
usage: nova-sprint reader set <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
from `nova-sprint help`:
  nova-sprint reader set <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --tiers <string>  the tiers these readers read, comma separated (frontier, heavy, pro or flash); all names every tier; default is flash on a fleet reader while the store holds routes and every tier on a friend's reader
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint reader away -h`:

```
usage: nova-sprint reader away <reader>...
from `nova-sprint help`:
  nova-sprint reader away <reader>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint reader up -h`:

```
usage: nova-sprint reader up <reader>...
from `nova-sprint help`:
  nova-sprint reader up <reader>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint reader remove -h`:

```
usage: nova-sprint reader remove <reader>...
from `nova-sprint help`:
  nova-sprint reader remove <reader>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint reader retire -h`:

```
usage: nova-sprint reader retire <reader>...
from `nova-sprint help`:
  nova-sprint reader retire <reader>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  say which readers would be retired and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: retires the named readers in the sprint's store; a read it is reading is taken back at the next tick and asked of a reader up with no card at that attempt, and it stays when none can take it; --dry-run writes nothing
```

`nova-sprint stream remove -h`:

```
usage: nova-sprint stream remove <stream>...
from `nova-sprint help`:
  nova-sprint stream remove <stream>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint stream archive -h`:

```
usage: nova-sprint stream archive <stream>...
from `nova-sprint help`:
  nova-sprint stream archive <stream>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint stream unarchive -h`:

```
usage: nova-sprint stream unarchive <stream>...
from `nova-sprint help`:
  nova-sprint stream unarchive <stream>...
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint stream set -h`:

```
usage: nova-sprint stream set <stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--promotion[=false]] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--base <branch>] [--reason <text>] [--answers <notes>]
from `nova-sprint help`:
  nova-sprint stream set <stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--promotion[=false]] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--base <branch>] [--reason <text>] [--answers <notes>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated
  --attempts <string>  the stream's attempt cap, over the sprint's: how many attempts one brief may run before the card is the coordinator's as a brief defect; 1 to 100, or default (the sprint's)
  --base <string>  the base branch to re-point the stream's cards to: every card not yet dealt and every card queued to merge has its BASE line rewritten; refused when origin holds no such branch or a card's PATHS are absent at its tip; dealt and working cards keep their base
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --land-protected <string>  the repositories (owner/name, comma separated; any for every one) on whose protected branches, dev and main, the lander lands the stream's cards; default takes the mark off, and a card based on a protected branch is then refused at land
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --promotion  mark the streams the promotion stream: they alone take cards cut on dev or main, and land them there (--land-protected any); --promotion=false takes the mark off (--land-protected default)
  --prose <string>  the globs (PATHS globs, comma separated: security/**,ratings/**) of the files whose backquotes are their own, which the lander does not read for a code span; default takes them off
  --read-tier <string>  the tier the stream's reads draw their route from when it is stronger than the card's own (flash, pro or heavy; default takes it off: the sprint's)
  --reason <string>  why the read tier is set, recorded on the stream row (the judgment 'raise the read tier of the stream?' names it)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --release <string>  the release this stream belongs to (default or none clears it)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint set -h`:

```
usage: nova-sprint set [--rework-priority <fix|high|keep>] [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--attempts <n|default>] [--friend-idle <duration|default>] [--friend-finish <duration|default>] [--fleet-tiers <tiers|all>] [--friends-tiers <tiers|all>] [--reads <0|1|2|default>]
from `nova-sprint help`:
  nova-sprint set [--rework-priority <fix|high|keep>] [--read-tier <flash|pro|default>] [--read-cards <on|off|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--attempts <n|default>] [--friend-idle <duration|default>] [--friend-finish <duration|default>] [--fleet-tiers <tiers|all>] [--friends-tiers <tiers|all>] [--reads <0|1|2|default>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --alarm-fleet <string>  the backlog alarm on the fleet: a judgment, once an episode, while the members up work fewer cards than this percent (1 to 100) of their width with a primary ready or waiting; off takes it off (the default)
  --alarm-merging <string>  the backlog alarm on merging: a judgment, once an episode, while more primaries than this whole number are merging; off takes it off (the default)
  --alarm-ready <string>  the backlog alarm on the feed: on raises a judgment, once an episode, while no primary is ready and one waits; off takes it off (the default)
  --alarm-review <string>  the backlog alarm on review: a judgment, once an episode, while more primaries than this whole number are in review; off takes it off (the default)
  --attempts <string>  the attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect (brief, drop; never dealt again); 1 to 100, or default (4); a stream's own: nova-sprint stream set <s> --attempts <n>
  --dealt-max <string>  how long a work card may wait dealt and never taken (in its member's ready queue, or withdrawn) before it is a judgment: a duration, or default (6h0m0s, 3 times the take deadline); a taken card's own deadline starts at its take
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --fleet <string>  the fleet's work: off and the deal hands no work card to a machine (its readers still read; no fleet idle alarm), on deals again (the default)
  --fleet-tiers <string>  the tiers the fleet may take: flash, pro, heavy, frontier, comma separated, or all (the default); the deal hands a machine only a work card, and a member's reader only a read card, whose tier is one of them, on top of each row's own tiers
  --friend-finish <string>  how long a friend holding working cards may finish none (working to done) before the coordinator's pass judges her idle: a duration, or default (30m0s)
  --friend-idle <string>  how long a friend holding cards may show no file write under her working directory and outbox before it is an alarm: a duration, or default (20m0s)
  --friends <string>  the friends' work: off and the deal hands no work card to a friend (her reads still flow; no empty-row alarm), on deals again (the default)
  --friends-tiers <string>  the tiers the friends may take, as --fleet-tiers says the fleet's: a friend is dealt a work or read card only of one of them, on top of her row's own tiers
  --go-lanes <string>  the Go lanes of every machine, the Go build and test runs one machine grants at once (nova-sprint lane take go): a whole number from 1, or default (1, one test stream per machine)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --read-cards <string>  on: the tick asks every read a card in review needs at once, as read cards on the fleet table dealt to friends and to members with a reader row, half a slot each; off or default: the readers table asks, one read at a time
  --read-tier <string>  the tier every card's reads draw their route from when it is stronger than the card's own (flash, pro or heavy; default takes it off: each card's own tier)
  --reads <string>  the ok reads at its head every card in review needs, whatever its tier: 0 (no read: a primary whose work finished LAND is accepted on it), 1 or 2; default: one for a flash card, two above
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --rework-priority <string>  the priority a normal or low card gets when its next attempt opens: fix (the default), high, or keep to retain its level
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: sets the sprint's settings named in the sprint's store; --dry-run writes nothing
```

`nova-sprint promoted -h`:

```
usage: nova-sprint promoted --sha <merge sha> [--answers <note>]
from `nova-sprint help`:
  nova-sprint promoted --sha <merge sha> [--answers <note>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing
  --dry-run  check the sha and say what would be recorded; record nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --returned <string>  landed cards dev or an audit returned with this promotion, comma separated: each is marked on the card and the tick asks to raise its stream's read tier
  --sha <string>  the merge commit's sha on dev, 7 to 40 hex digits (required)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: records the promotion in the sprint's store; --dry-run writes nothing
```

`nova-sprint funded -h`:

```
usage: nova-sprint funded <provider> --reason <text>
from `nova-sprint help`:
  nova-sprint funded <provider> --reason <text>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  the payment made, in a few words (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: ends the provider's rest of its funds in the sprint's store, with the reason; --dry-run writes nothing
```

`nova-sprint cost reconcile -h`:

```
usage: nova-sprint cost reconcile [--dry-run] [--json]
from `nova-sprint help`:
  nova-sprint cost reconcile [--dry-run] [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  read each provider's usage and print each gap as the step would record it, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: reads each provider's usage of today through the seat's key and writes the reconciliation and its gap judgment to the sprint's store; --dry-run writes nothing
```

`nova-sprint cost reprice -h`:

```
usage: nova-sprint cost reprice [--route <r>]... [--since <RFC3339>] [--dry-run] [--json]
from `nova-sprint help`:
  nova-sprint cost reprice [--route <r>]... [--since <RFC3339>] [--dry-run] [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  print what the reprice would do, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --route <value>  reprice only the records this route priced (again, or comma separated, for more; default: every route)
  --since <string>  reprice only the records that ended at or after this time (RFC3339)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: rewrites every priced consumer record's cost, each card's totals and each stream's landed sum from the records' tokens at the routes' current prices in the sprint's store; --dry-run writes nothing
```

`nova-sprint ci -h`:

```
usage: nova-sprint ci <id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]
from `nova-sprint help`:
  nova-sprint ci <id>... (--red | --green) --epoch <n> [--head <h>] [--run <id>] [--source <s>] [--note <text>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --green  the run passed
  --group <string>  the members of the inbox group of this id (the id inbox prints; a group number is refused)
  --head <string>  the head the run tested (default: the primary's)
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, at most n cards, in work order; accept and ask take them in stream turns from the work table's stream index
  --note <string>  what the run said
  --one  rework and drop: act on the one card named though the inbox holds a judgment group of several naming it (refused without it: the group is answered whole)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --red  the run failed
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --run <string>  the run's id: a retried report of it is recorded once
  --source <string>  where the result comes from
  --stream <string>  the cards of one stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: records the CI run's result, red or green, on each named primary in the sprint's store; --dry-run writes nothing
```

`nova-sprint wait -h`:

```
usage: nova-sprint wait (<note>[,<note>]... | --group <id> [--expect <n>]) (--for <duration> | --until <RFC3339>)
from `nova-sprint help`:
  nova-sprint wait (<note>[,<note>]... | --group <id> [--expect <n>]) (--for <duration> | --until <RFC3339>)
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --expect <int>  with --group: the group's size as inbox printed it; a group of another size now is refused and nothing changes
  --for <duration>  review it again after this long
  --group <string>  the notes of the inbox group of this id (the id inbox prints; a group number is refused)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --until <string>  review it again at this time (RFC3339)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint remind -h`:

```
usage: nova-sprint remind (--in <duration> | --at <time>) --note <text> [--for <actor>] | --list | --cancel <id>
from `nova-sprint help`:
  nova-sprint remind (--in <duration> | --at <time>) --note <text> [--for <actor>] | --list | --cancel <id>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --at <string>  wake the actor at this time: RFC3339, or a local date and time, date or time of day
  --cancel <string>  take this timer off the record: its id, from remind --list
  --dry-run  say what would be written and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --for <string>  the actor woken (default: the caller, --actor or NOVA_SPRINT_ACTOR)
  --in <duration>  wake the actor this long from now (a duration: 90s, 30m, 4h)
  --json  print one JSON object for a program instead of the lines
  --list  print the open timers (id, for, due, note)
  --max <int>  listed items of each kind; 0 is all
  --note <string>  the text the judgment carries: what the actor is woken to
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: writes one timer to the sprint's timer record, which the tick of a RUNNING machine raises as one judgment of kind "timer" addressed to its actor at its due time, once (--list reads the open timers, --cancel takes one off); --dry-run writes nothing
```

`nova-sprint ack -h`:

```
usage: nova-sprint ack <note>[,<note>]... --reason <text>
from `nova-sprint help`:
  nova-sprint ack <note>[,<note>]... --reason <text>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why nothing is to be done
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: closes the named notifications, nothing to be done, with the reason, in the sprint's store; --dry-run writes nothing
```

`nova-sprint answer -h`:

```
usage: nova-sprint answer [--dry-run] [--bar <p>] [--every <duration>] [--timeout <duration>] [--backend jev|fixed] [--answers <file>] [--record <file>]
from `nova-sprint help`:
  nova-sprint answer [--dry-run] [--bar <p>] [--every <duration>] [--timeout <duration>] [--backend jev|fixed] [--answers <file>] [--record <file>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed)
  --backend <string>  the decision's backend: jev (its key from JEV_API_KEY, which nova-secrets exec sets) or fixed (--answers)
  --bar <string>  apply a verb whose probability is at or above this bar (else the sprint row's decide_judgment_bar; with neither, nothing is applied: every decision is recorded and what a bar would apply is listed)
  --dry-run  ask the decision and print what would be applied; apply nothing and write no record
  --every <duration>  run a pass every duration until the machine is STOPPED (or DONE): the coordinator seat's loop; 0 is one pass
  --json  print one JSON object for a program instead of the lines
  --record <string>  the judgment decisions' record, JSON lines (default ~/nova-sprint/decide/judgment.jsonl, its directory made 0700)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --timeout <duration>  how long one ask of the backend may take; an ask past it is that card's failed row, and nothing is applied for it
exit codes: 0 done (each routine judgment's card applied or listed; --every: the machine is STOPPED), 1 a line applied was refused or a decision's backend failed, 2 usage, an actor not the coordinator, or a sprint that did not answer
effect: delivery: sends the routine judgments' state to the decision's backend (Jev), applies the verbs chosen through the sprint's verbs, and appends to --record; --dry-run asks and writes nothing
```

`nova-sprint inbox -h`:

```
usage: nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>] [--push <dir> | --push seat]] [--deadline <duration>] [--stale <duration>]
from `nova-sprint help`:
  nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>] [--push <dir> | --push seat]] [--deadline <duration>] [--stale <duration>]
  nova-sprint inbox --wait
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --at-epoch <int>  the inbox as it was at an earlier epoch (before a clear)
  --deadline <duration>  a judgment open longer is overdue
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --open <string>  list every member, need and notification of the group of this id
  --push <string>  with --wait, keep running (until interrupted): each new judgment and note to the coordinator is written once as <dir>/<note id>.md, the group as inbox --open prints it and the clock; the files there are the cursor, so a restart pushes nothing twice; a local write; seat is the holder's inbox, ~/<holder>-working/inbox/sprint-judgments, followed through a seat change (a directory named seat is ./seat)
  --read  move the cursor past what is shown: happened notifications before it are not shown again (open judgments always are)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stale <duration>  a stream with no progress for longer is shown stalled
  --timeout <duration>  with --wait, the longest wait; the inbox is shown when it passes (with --push, how often the loop looks at the machine)
  --wait  block until a judgment, or a note to the coordinator, that was not in the inbox when the wait began (a held judgment never wakes it), or the machine stops; then show the inbox, saying what is new
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint card base -h`:

```
usage: nova-sprint card base <id> <branch> [--repo-dir <clone>]
from `nova-sprint help`:
  nova-sprint card base <id> <branch> [--repo-dir <clone>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repo-dir <string>  a clone whose origin is asked for the branch, for a card whose brief names no REPO: line
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: re-points the merging card's BASE to the branch in the sprint's store, after asking origin (a read) whether it holds that branch; --dry-run asks origin the same and plans the step, and --dry-run writes nothing
```

`nova-sprint card -h`:

```
usage: nova-sprint card <id> [--brief | --fields] [--at-epoch <n>] | (--all | --stream <s>) --json: every card, one JSON object a line
from `nova-sprint help`:
  nova-sprint card base <id> <branch> [--repo-dir <clone>]
  nova-sprint card <id> [--brief | --fields] [--at-epoch <n>] | (--all | --stream <s>) --json: every card, one JSON object a line
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --all  every card on the table, one JSON object a line (its fields, column, needs and brief length), in one read; with --json, and no id
  --at-epoch <int>  the primary as it was at an earlier epoch (before a clear)
  --brief  the brief alone, as the card holds it, and nothing else (a card with no brief is refused, exit 1); not with --fields
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --fields  every field of the primary and its cards, one record a line, instead of its story
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  --all of one stream's cards
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads one card, its brief and its attempts, writes nothing
```

`nova-sprint needs -h`:

```
usage: nova-sprint needs [--stream <s>] [--roots]
from `nova-sprint help`:
  nova-sprint needs [--stream <s>] [--roots]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --roots  print only the roots and the width lines
  --stream <string>  the waiting cards of one stream (default: every stream)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the waiting cards, writes nothing
```

`nova-sprint streams -h`:

```
usage: nova-sprint streams [--repo <owner/name>] [--release <name>] [--cards]
from `nova-sprint help`:
  nova-sprint streams [--repo <owner/name>] [--release <name>] [--cards]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --cards  every card of each stream: its id, state, tier, the first sentence of its THE TASK and its needs
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --release <string>  only the streams of this release (stream set <s> --release <name>)
  --repo <value>  only the streams recording this repository (owner/name), comma separated or repeated; a stream whose cards name more than one repository is listed by each
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the work and merge tables once and prints each stream with the repositories and bases its cards record, its release, its open and landed counts, and with --cards every card's id, state, tier, title and needs; writes nothing
```

`nova-sprint held -h`:

```
usage: nova-sprint held [--stream <s>]
from `nova-sprint help`:
  nova-sprint held [--stream <s>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the held cards of one stream (default: every stream)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the held cards of the table, writes nothing
```

`nova-sprint sentinels -h`:

```
usage: nova-sprint sentinels [--stream <s>]
from `nova-sprint help`:
  nova-sprint sentinels [--stream <s>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  the sentinels of one stream (default: every stream)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the sentinels and what each waits on, writes nothing
```

`nova-sprint sentinel set -h`:

```
usage: nova-sprint sentinel set <id> --needs <a,b>
from `nova-sprint help`:
  nova-sprint sentinel set <id> --needs <a,b>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --needs <string>  the sentinel's needs, comma separated, in place of the ones it has: each a card on the table; a sentinel with nothing to wait on is released, not emptied
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: replaces the sentinel's needs in the sprint's store, keeping its id, stream, score and log; --dry-run writes nothing
```

`nova-sprint bases -h`:

```
usage: nova-sprint bases [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint bases
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the work and merge tables and writes nothing to the store; fetches origin's dev and each base into land's kept clone of each repository, once a call, and clones nothing
```

`nova-sprint log -h`:

```
usage: nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
from `nova-sprint help`:
  nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --at-epoch <int>  the log of an earlier epoch (before a clear), as it was
  --card <string>  the lines about this card (a primary: its work, read and merge cards too)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --member <string>  the lines of this fleet member or reader: what was dealt to, taken from or read by it
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --since <string>  the lines at or after this time: a duration back from now (10m) or a time (RFC 3339)
  --stream <string>  the lines of this stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the sprint's change log, writes nothing
```

`nova-sprint check -h`:

```
usage: nova-sprint check [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint check
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 no violation, 1 a violation (each on its line), 2 usage or a store that did not answer
effect: inspection: reads the sprint's tables and prints each violation, writes nothing
```

`nova-sprint repair -h`:

```
usage: nova-sprint repair [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint repair
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint watch -h`:

```
usage: nova-sprint watch --wake [--every <duration>] [--state <file>] [--check <duration>] [--judgment-every <duration>] [--merge-every <duration>] [--backlog-every <duration>] [--land-after <duration>] [--merge-over <n>] [--merging-over <n>] [--review-over <n>]
from `nova-sprint help`:
  nova-sprint watch --wake [--every <duration>] [--state <file>] [--check <duration>] [--judgment-every <duration>] [--merge-every <duration>] [--backlog-every <duration>] [--land-after <duration>] [--merge-over <n>] [--merging-over <n>] [--review-over <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --backlog-every <duration>  at most one backlog wake in this long
  --check <duration>  wake with a check this long after the last wake
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  how often the sprint and the bus are looked at, above 0
  --json  print one JSON object for a program instead of the lines
  --judgment-every <duration>  at most one judgment wake in this long
  --land-after <duration>  merging with no land pass for this long is a merge wake
  --max <int>  listed items of each kind; 0 is all
  --merge-every <duration>  at most one merge wake in this long
  --merge-over <int>  merging over this many is a merge wake
  --merging-over <int>  merging over this many is a backlog wake
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --review-over <int>  review over this many is a backlog wake
  --state <string>  the file that keeps the cursors between runs, so no event is missed or woken twice (default: one a store under the user cache directory); the first run starts from now
  --wake  block until the first thing that wakes the coordinator, print WAKE <kind> <time> <evidence> and exit 0 (kinds: bus, judgment, stop, friend, merge, backlog, check)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint seat check -h`:

```
usage: nova-sprint seat check [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint seat check
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done (every check OK), 1 a check is DOWN, 2 usage or a store that did not answer
effect: inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing
```

`nova-sprint machinery -h`:

```
usage: nova-sprint machinery [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint machinery
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done (every check OK), 1 a check is DOWN, 2 usage or a store that did not answer
effect: inspection: checks server, store, loop, beats, readers, dashboard, installed versions, merge queue, writes nothing
```

`nova-sprint where -h`:

```
usage: nova-sprint where [--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived] [--stale <duration>] [--at-epoch <n>]: includes landedSeries] [--release [<name>]]
from `nova-sprint help`:
  nova-sprint where [--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived] [--stale <duration>] [--at-epoch <n>]: includes landedSeries] [--release [<name>]]
  nova-sprint where
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --all  draw the readers and merge tables too
  --archived  with --json: the archived streams' rows of the work and merge tables in tables, and their primaries in --rows, beside the live ones (stream archive); their counts are in the footers and the summary either way
  --at-epoch <int>  the sprint as it was at an earlier epoch (before a clear); the series is that epoch's
  --cards  with --json: also every dealt work card, judgment and lane
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  the redraw interval with --watch, above 0
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --release  show cards left per release, or for the named release
  --rows  with --json: also every primary's row of the work table
  --stale <duration>  a stream with no progress for longer is shown stalled (--json)
  --watch  redraw in place every --every until interrupted
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the sprint table, its rows and the epoch log, writes nothing; --json carries landedSeries, cards landed per 10 minutes over the last 24 hours (144 buckets), friends and fleet by the worker of the landed attempt (the last <who>:ok of the card's .wN, never the lander; a sentinel's release is not work)
```

`nova-sprint dashboard -h`:

```
usage: nova-sprint dashboard [--listen <address:port>[,<address:port>...] | none] [--pull <address:port>[,<address:port>...] | none] [--logo <file>] [--every <duration>]
from `nova-sprint help`:
  nova-sprint dashboard [--listen <address:port>[,<address:port>...] | none] [--pull <address:port>[,<address:port>...] | none] [--logo <file>] [--every <duration>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  read the sprint at most once per this duration, above 0
  --json  print one JSON object for a program instead of the lines
  --listen <address:port>  serve the page on each address:port of a comma-separated list, one listener each and one cached copy of the sprint: loopback, or this machine's address on the fleet's private network (the tailnet); 0.0.0.0, :: and public addresses are refused; default 127.0.0.1:7390; none: no page
  --logo <file>  an image file served as the page's logo and favicon; none: the logo slot renders nothing
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pull <address:port>  serve the pull routes (/friend/<name>, /machine/<name>, their /api/ JSON and /events/ stream forms, /team and /api/team, /api/sprint, /events) on each address:port of a comma-separated list, the same addresses --listen takes, from the same cached copy, read-only; default 127.0.0.1:7395; none: no pull routes; an http:// or https:// URL of another dashboard: be its puller, reading its /api/sprint once per --every in place of the sprint and serving the page alone (the public copy)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 stopped (an interrupt), 2 usage or an address it cannot listen on, 3 its binary was replaced on disk (its supervisor starts the new one)
effect: inspection: serves the page and the pull routes, reads the sprint as where --json --cards does, writes nothing
```

`nova-sprint handover -h`:

```
usage: nova-sprint handover [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint handover
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the store, writes nothing
```

`nova-sprint view coordinator -h`:

```
usage: nova-sprint view coordinator [--all] [--since <cursor>] [--json]
from `nova-sprint help`:
  nova-sprint view coordinator [--all] [--since <cursor>] [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --all  every friend's and machine's row too, as rows (by default only the rows that need a look, as items)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --needs  instead, list every decision waiting on the coordinator (open judgments, held sentinels, stopped streams, held cards), ranked by the cards blocked behind each, ties by age, each with its evidence; takes no --all or --since
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --since <string>  the cursor an earlier view printed: leave out every item it showed that has not changed, and count them (same) and the ones that stand no more (gone)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads what needs the seat (the tables, the inbox, the friends and the machines), writes nothing
```

`nova-sprint view cards -h`:

```
usage: nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]
from `nova-sprint help`:
  nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --by <string>  count the cards by tier, stream, col or holder instead of listing them
  --col <string>  only the cards in this column (waiting, ready, working, review, merging, landed)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --holder <string>  only the cards this member or friend is working
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --stream <string>  only the cards of this stream
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: lists or counts (--by tier|stream|col|holder) the work table's primaries, filtered by --col, --stream, --holder; writes nothing
```

`nova-sprint view worker -h`:

```
usage: nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]
from `nova-sprint help`:
  nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the fleet member or the friend whose view it is
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --since <string>  the cursor an earlier view printed: leave out every card it showed that has not changed
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the worker's cards, their packets and its results not landed, writes nothing
```

`nova-sprint seat install -h`:

```
usage: nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]
from `nova-sprint help`:
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --config-dsn <dsn>  the config store's PostgreSQL dsn, postgres://user@host:port/db with no password
  --config-password-env <NAME>  the NAME of the config store's password: the variable nova-config reads it from, else the key of the store login's nova-secrets seat (nova-sprint seat login) it is read from in process
  --config-seat <name>  the name of the nova-config seat profile written into nova-config's seats.tsv (nova-config --seat <name> reads it); wants --config-dsn and --config-password-env
  --dir <string>  the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --dry-run  print the unit and where it would go, and write and load nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --harness <string>  the harness the seat's AI runs in (required): the push loop delivers each judgment, and the push proof, into the session through its adapter; a harness with no deliver command (claude) gets the folder adapter, each one a file written into --target
  --json  print one JSON object for a program instead of the lines
  --log <string>  the file the loop's lines go to, macOS (default: ~/Library/Logs/nova-sprint-seat-push.log); on Linux they are in the journal
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --server <host:port>  the sprint's server, host:port (default NOVA_SPRINT_SERVER): the unit's, and recorded in the seat beside the store login, where seat check reads it when NOVA_SPRINT_SERVER is not set
  --session <string>  the session's id, for a harness that names one (default: the adapter's newest in --target)
  --target <string>  the session's directory, where the harness's adapter delivers (required); for the folder adapter, the directory the session watches with a Monitor, which must be there
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: writes the push loop's unit (inbox --wait --push seat) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing
```

`nova-sprint seat watch -h`:

```
usage: nova-sprint seat watch <dir> [--json]
from `nova-sprint help`:
  nova-sprint seat watch <dir> [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: watches this machine's directory, prints each complete file path once as it appears, writes nothing; an interrupt stops it
```

`nova-sprint seat uninstall -h`:

```
usage: nova-sprint seat uninstall [--dir <dir>]
from `nova-sprint help`:
  nova-sprint seat uninstall [--dir <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the unit was written into (default: as seat install's)
  --dry-run  say which unit would be unloaded and removed, and unload and remove nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: unloads the push loop's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing
```

`nova-sprint seat deliver -h`:

```
usage: nova-sprint seat deliver [--text <message>] --actor <seat>
from `nova-sprint help`:
  nova-sprint seat deliver [--text <message>] --actor <seat>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --text <string>  message to deliver; without this flag read the bus receiver's standard input (at most 1 MiB)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint seat push -h`:

```
usage: nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <why>]] [--beat bus|friends|transitions [--failed <why>]] [--observe friends|transitions --json] [--dry-run]
from `nova-sprint help`:
  nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <why>]] [--beat bus|friends|transitions [--failed <why>]] [--observe friends|transitions --json] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --beat <string>  native observer receipt: bus, friends or transitions; records a completed pass, never a session pong
  --dry-run  check the flags and the record and print what would be recorded, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --failed <string>  with --sent or --beat, why the completed push pass failed
  --harness <string>  the harness the AI holding the seat runs in: its adapter delivers each push into the session (a harness with no deliver command, claude, gets the folder adapter: each push a file in --target)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --observe <string>  native observer read: friends or transitions, with --json
  --observed-session <string>  with --beat and --seat-generation, the observation's session id
  --observed-target <string>  with --beat and --seat-generation, the observation's delivery target
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --seat-generation <uint>  with --beat, the seat generation the completed observation read; with --epoch and --observed-target
  --sent <string>  the push loop's report: the nonce of the check it delivered
  --session <string>  with --harness, the session's id, for a harness that names one (default: the adapter's newest in --target)
  --target <string>  with --harness, the session's directory, where the adapter delivers (for the folder adapter, the directory the session watches, which must be there)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: with --harness and --target, records the seat's push target (harness, target, session, adapter) in the sprint's store; with --sent, records the push loop's delivery of a check or its failure; with neither, prints the record and whether the seat is live and writes nothing; --dry-run checks the same and writes nothing
```

`nova-sprint seat pong -h`:

```
usage: nova-sprint seat pong <nonce> [--dry-run]
from `nova-sprint help`:
  nova-sprint seat pong <nonce> [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  check the nonce against the seat's push record and say whether it would prove the seat, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: proves the seat live by the nonce of the last check delivered, recording the proof in the sprint's store; --dry-run checks the nonce against the record and writes nothing
```

`nova-sprint seat -h`:

```
usage: nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
from `nova-sprint help`:
  nova-sprint seat check
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--server <host:port>] [--config-seat <name> --config-dsn <dsn> --config-password-env <NAME>] [--dry-run]
  nova-sprint seat watch <dir> [--json]
  nova-sprint seat uninstall [--dir <dir>]
  nova-sprint seat deliver [--text <message>] --actor <seat>
  nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--sent <nonce> [--failed <why>]] [--beat bus|friends|transitions [--failed <why>]] [--observe friends|transitions --json] [--dry-run]
  nova-sprint seat pong <nonce> [--dry-run]
  nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  with --repair, why the key is repaired, recorded in the log (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repair  write the coordinator key from the seat's record when they differ: the record's holder or the owner, with --reason; logged with who and why
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the seat (holder, epoch, generation), writes nothing
```

`nova-sprint fsck seat -h`:

```
usage: nova-sprint fsck seat [--pg <host:port or postgres:// URI>]
from `nova-sprint help`:
  nova-sprint fsck seat [--pg <host:port or postgres:// URI>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --pg <string>  the address of nova-config's store, whose sprint row's coordinator is checked: host:port, or a postgres:// URI; else NOVA_PG_DSN
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the coordinator key, the seat record and the server's actor from the sprint's store and the sprint row's coordinator from nova-config's store, writes nothing
```

`nova-sprint routes -h`:

```
usage: nova-sprint routes [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint routes
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the route table and prints each route, writes nothing
```

`nova-sprint rules -h`:

```
usage: nova-sprint rules [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
from `nova-sprint help`:
  nova-sprint rules
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the rules the tick answers by and why the fleet is idle, writes nothing
```

`nova-sprint stats tidy -h`:

```
usage: nova-sprint stats tidy (--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]
from `nova-sprint help`:
  nova-sprint stats tidy (--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --all  all four: --friends --fleet --routes --streams
  --dry-run  print what would move; write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --fleet  the machines' done cells, as --friends does the friends'
  --friends  the friends' done cells: their history-only finished cards leave them (done and ok% count from now)
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why, kept in the archive and the stats record (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --routes  the route counters: archived, and stats --routes counts from now
  --streams  the streams' landed costs: the work table's cost and per landed count from now
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: takes the history off the done cells named and writes the tidy's archive and stats record to the sprint's store; --dry-run writes nothing
```

`nova-sprint stats -h`:

```
usage: nova-sprint stats [--routes [--since <10m|RFC3339>]]
from `nova-sprint help`:
  nova-sprint stats tidy (--friends | --fleet | --routes | --streams | --all)... --reason <text> [--dry-run]
  nova-sprint stats [--routes [--since <10m|RFC3339>]]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --routes  the route table from the log over --since, instead of the live tables
  --since <string>  with --routes, the window start: a duration back from now (10m) or an RFC 3339 time; the last stats tidy of the routes when not given
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the sprint's counts and rates, writes nothing
```

`nova-sprint play -h`:

```
usage: nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
from `nova-sprint help`:
  nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --batch <int>  a merge step's batch
  --broken <float>  the chance a read finds the work broken
  --cross <float>  the chance a batch has a card that needs a card of another stream first
  --down <float>  the chance, per member and second, that a member's machine that is up goes down (stops beating)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --every <duration>  between ticks
  --fail <float>  the chance a work card comes back failed
  --flap <float>  the chance, per member and second, that a member's machine falls silent, and the same chance that a silent one beats again: --down and --up with the one chance
  --hold  play the downs as the coordinator's hold (fleet down, fleet up) instead of a machine falling silent; holds it took are released before it stops
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reads <int>  read cards a reader begins, and reports, in its one call of each a tick; 0 is its whole queue
  --red <float>  the chance a batch turns the stream branch red
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --seed <uint>  the seed: the same seed plays the same run
  --silent <value>  <member>@<from>+<for>: the member's machine stops beating <from> after play starts, for <for> (e.g. m3@30s+20s); repeatable
  --simulation  play the locked simulation: broken=0.1 fail=0.1 stuck=0.1 cross=0.01 down=0.01 up=0.1; a chance flag beside it sets that one chance
  --stuck <float>  the chance a batch has a card that does not merge
  --take <int>  work cards a member takes in its one take a tick; 0 is the member's width, else 64
  --ticks <int>  stop after n ticks; 0 is until every stream lands
  --up <float>  the chance, per member and second, that a member's machine that is down comes back (beats again)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint clear -h`:

```
usage: nova-sprint clear --confirm sprint
from `nova-sprint help`:
  nova-sprint clear --confirm sprint
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --confirm <string>  the sprint's name, to confirm: the name of its view, sprint
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint teardown -h`:

```
usage: nova-sprint teardown --confirm sprint
from `nova-sprint help`:
  nova-sprint teardown --confirm sprint
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --confirm <string>  the sprint's name, to confirm: the name of its view, sprint
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
```

`nova-sprint live -h`:

```
usage: nova-sprint live [--bin-dir <dir>] [--dashboard <link>]... [--json]
from `nova-sprint help`:
  nova-sprint live [--bin-dir <dir>] [--dashboard <link>]... [--json]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --agents-dir <string>  the launchd agents directory read (else NOVA_LAUNCH_AGENTS, else ~/Library/LaunchAgents)
  --bin-dir <string>  the bin directory the build is installed in
  --dashboard <value>  a dashboard binary link that should name the installed nova-sprint (repeatable)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --launchctl <string>  the launchctl the agents are read with
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 the manifest was read (whatever it says: a stale process or a library mismatch is a line, not a failure), 1 the installed nova-sprint or the agents directory could not be read, 2 usage
effect: inspection: reads only, writes nothing: the installed nova-sprint's version and inode, the store's function library through nova-redis fn check, the dashboard links, and every com.nova.* launchd agent of this login (its plist, its pid from launchctl print, its running arguments from ps, its executable's inode from lsof); a friend daemon's last beat from nova-friend status
```

`nova-sprint adopt -h`:

```
usage: nova-sprint adopt <version|path> --source <checkout> --inventory <file> --reason <text> [--limit <host>] [--receipts <dir>] [--dry-run]
from `nova-sprint help`:
  nova-sprint adopt <version|path> --source <checkout> --inventory <file> --reason <text> [--limit <host>] [--receipts <dir>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --ansible <string>  the ansible-playbook binary
  --dry-run  run the play with --check: each step says WOULD and nothing is written
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --inventory <string>  the inventory the play reads, the nova-inventory script (else NOVA_INVENTORY)
  --json  print one JSON object for a program instead of the lines
  --limit <string>  the one machine to adopt on, as the inventory names it (default: the coordinator group, the seat)
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why this build is adopted now: the build's dogfood gate reports it and does not refuse
  --receipts <string>  the dogfood receipts directory (default ~/nova-working/dogfood)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --source <string>  the nova-tools checkout the build is made from; its fleet/tools.yml is the play
exit codes: 0 every step of the seat adopted the build (or, with --dry-run, said what it would change), 1 the play stopped or left a step without its line (ADOPT REFUSED step=<step>, with what the rollback did when the window had opened: the steps before it are done, or rolled back, and the play runs again to finish), 2 usage
effect: local and remote writes through ansible-playbook: the tools play builds the version if missing and runs the new build's checks on the seat (shadow tick, nova-friend install --dry-run) before anything changes; then, in a window, it stops the seat's old server and member (bootout, seen gone in ps), migrates the configuration store as its owning role (nova-config migrate --window, refusing while any other nova session holds the database), loads the function library, installs the tools and bootstraps every stopped or stale nova launchd agent, points the dashboard links at the installed nova-sprint and reinstalls each stale friend daemon with nova-friend install; a refusal in the window puts the tools and library of before back and restarts the old agents (the migration is never undone); --dry-run runs the play with --check and writes nothing
```

`nova-sprint server switch -h`:

```
usage: nova-sprint server switch [<binary>] [--rollback] [--window <duration>] [--target <path>] [--tick-deadline <duration>]
from `nova-sprint help`:
  nova-sprint server switch [<binary>] [--rollback] [--window <duration>] [--target <path>] [--tick-deadline <duration>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dry-run  run the candidate's shadow tick (read-only) and say what would be switched; switch, roll back and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --rollback  roll back to previous binary, or enable automatic rollback on failed land in window
  --target <string>  target binary to replace (default: this binary or NOVA_SPRINT_SERVER_BIN)
  --tick-deadline <duration>  the candidate's shadow tick (<binary> tick --shadow, read-only, against the store --redis names) must end in this long, or the switch is refused
  --window <string>  rollback window duration: if a land fails within this window, roll back
exit codes: 0 done, 1 failed or refused (the candidate's shadow tick failed: nothing changed), 2 usage
effect: local write: runs <binary> tick --shadow against the store first (read-only, under --tick-deadline) and refuses the swap, nothing changed, when it exits non-zero, panics or misses the deadline; then switches the server binary on disk, keeping the previous binary, the shadow's plan size and time at <target>.shadow.json, and rolling back on land failure in the window; --dry-run runs the shadow tick only and switches and writes nothing
```

`nova-sprint install -h`:

```
usage: nova-sprint install <server|member|seat-push|friend-sync|table> [--dir <dir>] [--log <file>] [--dry-run] (each kind's own flags are listed by install <kind> -h)
from `nova-sprint help`:
  nova-sprint install <server|member|seat-push|friend-sync|table> [--dir <dir>] [--log <file>] [--dry-run] (each kind's own flags are listed by install <kind> -h)
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the unit is in (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --dry-run  print what would be done, and write, load, unload and remove nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --log <string>  the file the unit's lines go to, macOS (default: ~/Library/Logs/nova-sprint-<kind>.log); on Linux they are in the journal
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done (the unit written or kept, and loaded), 1 the unit did not write or load (FAILED names it), 2 usage or a unit it refuses (a kind it does not install, the twin, a store login the unit could not use, a flag missing)
effect: local write: writes the kind's unit (the verb itself, never a wrapper) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing
```

`nova-sprint uninstall -h`:

```
usage: nova-sprint uninstall <server|member|seat-push|friend-sync|table> [--dir <dir>] [--dry-run]
from `nova-sprint help`:
  nova-sprint uninstall <server|member|seat-push|friend-sync|table> [--dir <dir>] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the unit is in (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --dry-run  print what would be done, and write, load, unload and remove nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done (removed, or no unit there), 1 the unit did not unload or remove (FAILED names it), 2 usage
effect: local write: unloads the kind's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing
```

`nova-sprint units -h`:

```
usage: nova-sprint units --check [--dir <dir>]
from `nova-sprint help`:
  nova-sprint units --check [--dir <dir>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --check  name each unit a running sprint needs installed, missing or different (required)
  --dir <string>  the directory the units are in (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 every unit a sprint needs is installed, 1 one or more is missing or different (each named with the verb that installs it), 2 usage
effect: inspection: reads the unit files in --dir and names each unit a sprint needs installed, missing or different; loads and changes nothing
```

`nova-sprint cost attach -h`:

```
usage: nova-sprint cost attach <card>.<attempt> --model <provider/model> --input <n> --cache-read <n> --cache-write <n> --output <n> [--reasoning <n>] [--usd <x>] [--source <text>] [--replace] [--dry-run] | --file <tsv> [--replace] [--dry-run]
from `nova-sprint help`:
  nova-sprint cost attach <card>.<attempt> --model <provider/model> --input <n> --cache-read <n> --cache-write <n> --output <n> [--reasoning <n>] [--usd <x>] [--source <text>] [--replace] [--dry-run] | --file <tsv> [--replace] [--dry-run]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --cache-read <int>  cache read tokens
  --cache-write <int>  cache write tokens
  --dry-run  print what attach would do, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --file <string>  a TSV of card, attempt, model, input, cache_read, cache_write, output, reasoning, usd, source; one bad row writes nothing
  --input <int>  uncached input tokens
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --model <string>  the provider/model the run used
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --output <int>  output tokens
  --reasoning <int>  reasoning tokens (default: not reported)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --replace  price a record that already holds tokens, and keep the old figures on the card's story
  --source <string>  where the figures came from, kept on the story when --replace
  --usd <string>  the harness's own cost, a non-negative decimal
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: store write: prices an existing cost record from the counts given, or from each row of --file, and moves the card's total, a landed card's cost, its stream's sum and the work card's usage; a record that already holds tokens is refused unless --replace, and the old figures go on the card's story; --file is all or none; --dry-run writes nothing
```

`nova-sprint coordinator -h`:

```
usage: nova-sprint coordinator <name> --reason <text> | <name> --take --approved-by <owner> --reason <text>
from `nova-sprint help`:
  nova-sprint coordinator <name> --reason <text> | <name> --take --approved-by <owner> --reason <text>
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --approved-by <string>  with --take, the sprint's owner who approved it (init --owner, else NOVA_SPRINT_OWNER): the take is refused without the owner's name
  --dry-run  say whether the seat would move, and how, and write nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  why the seat moves, recorded in the log with who moved it (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --take  take the seat as <name>, who runs this, when the holder is away (asleep, out of credits): wants --approved-by
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: delivery: moves the seat in the sprint's store, a note to the old holder on a take; --dry-run writes nothing
```
<!-- clidoc:end nova-sprint -->

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
nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
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

<!-- clidoc:begin nova-sandbox -->
`nova-sandbox help`:

```
nova-sandbox: run one command inside an OS-enforced wall around the directories you name

how it works: the wall is built for one run from your flags and kept nowhere:
--read directories are readable, --write directories writable, and the kernel
denies the rest (sandbox-exec on macOS, Landlock on Linux; check says which).
Paths must exist and be absolute, and HOME must sit inside a --write. The
command's own exit status comes back; 125 means the wall refused to start it.
first run: the lines under example:, in order: check the backend, make a scratch
directory, prove the wall with probe, then run a command that writes inside it.

usage:
  nova-sandbox --read <dir>... [--read-noexec <dir>...] --write <dir>... [--net-deny]
               [--net-listen] [--net-allow <host:port>] [--cwd <dir>]
               [--tmp <dir>] [--name <container>] [--acl tool|caller] -- <command> <args...>
  nova-sandbox probe --write <dir>... [--read <dir>...] [--secret <path>] [--net-deny]
  nova-sandbox policy --read <dir>... --write <dir>... [--net-deny] [--net-listen]
               [-- <command> <args...>]
  nova-sandbox check
  nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]...
               [--container <disk>] -- <command> <args...>          (darwin)
  nova-sandbox run --help
  nova-sandbox reap [--dry-run]                                   (darwin)
  nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id> [--base <branch>]
  nova-sandbox worktree --repo <dir> --scratch <dir> --prune
  nova-sandbox egress plan  --run <id> --policy <file> --model-host <host>
               --resolver <ip> [--bench-cidr <cidr>]... [--uid <n>] [--veth <if>]
               --out <file>
  nova-sandbox egress apply --plan <file> --run <id>                    (linux)
  nova-sandbox egress check --plan <file>
  nova-sandbox egress drop  --run <id>                                  (linux)
  nova-sandbox version
  nova-sandbox help

  --read <dir>    readable, recursively, and NOT writable. Repeatable, no default.
                  Shared inputs go here, named once, so N workers read one copy.
                  It CARRIES EXECUTE: a program under a --read runs.
  --read-noexec <dir>
                  readable, recursively, and NOT EXECUTABLE and not writable.
                  Repeatable, no default. This is the flag for a cache or a data
                  tree -- a module cache, a node_modules, a downloads directory --
                  that this user can write to: under --read the job could RUN
                  whatever lands there, and under this flag it can only read it.
                  A path in both lists is a refusal, not a merge.
  --write <dir>   readable AND writable, recursively. Repeatable, no default, and
                  REQUIRED: a command with no writable directory is a
                  misconfiguration, not a tighter sandbox. The FIRST --write is
                  where the working directory and the temp directory default to.
  --cwd <dir>     the command's working directory; must be inside a --write.
                  Default: the first --write. A cwd outside the wall denies
                  getcwd(3) and every git command dies before it reads anything.
  --tmp <dir>     TMPDIR/TMP/TEMP and zsh TMPPREFIX for the child; must be inside a --write.
                  Default: <first --write>/.nova-sandbox-tmp, the one directory
                  this tool creates.
  --net-deny      an ENFORCED network denial, or a refusal. Without it the tool
                  makes no promise about the network and the line says
                  net=nopromise.
  --net-listen    grant INBOUND ip as well; without it a job that does not
                  listen cannot be listened to. Never with --net-deny.
  --net-allow <host:port>  open the loopback host:port named, back up, by name;
                  the keyless local provider (ollama) that (remote ip) does not
                  reach. Repeatable.
  --gpu <n|m>     the explicit local GPU capability: none (default) or metal.
                  Opt-in only; metal records intent and never widens
                  mach-lookup or grants blanket device access (#230).
  --name <c>      the windows container name. Accepted and ignored on darwin, so
                  one caller builds one argv for three platforms.
  --acl <t|c>     who adds the windows ACEs. Accepted and ignored on darwin,
                  with one NOTE line, for the same reason as --name.
  --secret <path> probe only: the file a probe proves it cannot read. A path is
                  not a secret; the file's contents are never read. A probe may run
                  WITHOUT one -- a caller whose key is delivered by nova-secrets
                  exec into the environment has no key file, and the probe then
                  proves the wall's other checks (issue #881).

run gives one command a DISPOSABLE place to work and then takes it away: on darwin
an APFS volume of its own in the boot container, quota'd by --size and mounted at
/Volumes/nova-<n>. That volume is the only --write, the command runs in a process
group of its own, and on exit -- normal, error, signal or --timeout -- the group is
killed and the volume is unmounted and DELETED. Nothing of the run survives on the
boot volume, so there is no cleanup step. A delete that fails prints SANDBOX LEAK
with the one command that removes it and exits 3.

  --name <n>      run only: the volume is nova-<n>. Letters, digits, - _ and .
  --size <s>      run only: the volume's quota, e.g. 8g or 64m. REQUIRED: a
                  disposable place with no ceiling can fill the boot disk.
  --timeout <d>   run only: a Go duration after which the whole process group is
                  killed and the volume deleted anyway. Exit 124.
  --go            run only: add the Go toolchain's own roots as --read, GOROOT
                  and GOMODCACHE as go env reports them. A card that builds Go
                  wants this; nova-sandbox run --help says why.
  --container <d> run only: the APFS container to make the volume in. Default:
                  the container the boot volume is in.
  --dry-run       reap only: print what a reap would take and touch NOTHING.

reap clears what a SIGKILL left: a run killed outright has no path out to delete
its volume on, so the volume stays mounted and the command's own children are
reparented to PID 1 still holding it open. reap lists every nova-* volume, kills
what holds each one (SIGTERM, then SIGKILL) and deletes it -- except a volume a
LIVE run owns, which it reports and leaves alone. Exit 0 clean, 3 when anything
remained, so nova-sandbox reap --dry-run is a gate a card can end on.

egress is the card's OUTBOUND wall, and it lives on the BENCH rather than in the
card, because the worker is the adversary: plan resolves the names in the reviewed
allowlist (infra/image/egress.txt) ONCE, pins the addresses and writes an nftables
ruleset that denies everything the card did not name — TCP 443 to the pinned
addresses, UDP 53 to the resolver, and the metadata address, loopback and the other
benches denied outright. apply hands that ruleset to nft, check reads one back and
asserts its invariants, and drop takes the run's table away.

  --run <id>      egress: the run this wall belongs to; the table is nova_egress_<id>
  --policy <f>    egress plan: the allowlist in git. A name reaches a card only by a
                  PR to that file, never by a flag on one run.
  --model-host <h> egress plan: the ONE model host of this run, and it must already
                  be a line in the policy file.
  --resolver <ip> egress plan: the only destination UDP 53 is allowed to.
  --bench-cidr <c> egress plan: another bench, denied. Repeatable.
  --uid <n>       egress plan: the container's uid on the host (meta skuid).
  --veth <if>     egress plan: the container's interface (iifname). A plan needs
                  --uid or --veth: every rule is scoped to the card's own traffic.
  --out <file>    egress plan: where the ruleset is written.
  --plan <file>   egress apply and check: the ruleset to apply or to read back.

Every path is yours and none is guessed: a --read, a --read-noexec, a --write, a
--cwd or a --tmp that does not exist is a refusal and is NOT created. HOME must
resolve inside a --write (the caller sets it), because almost every tool derives
a path from it and an inherited HOME is denied by the wall.

A command that runs OUTSIDE the wall and dies inside it is missing a --read:
a toolchain in a user directory is exactly a caller-supplied read-only root.

exit codes: each verb's own, by verb:
  the bare wrap and run: the command's own status, 0-124, passed through, and
    a SANDBOX DONE ... exit=<n> line on stderr after it ends says the command
    returned it (SANDBOX OK, before it starts, says only that the wall is up);
    125 nova-sandbox refused before the command ran, a usage error included
    (a SANDBOX REFUSED line on stderr says why, and no SANDBOX DONE follows);
    126 the command could not be executed; 127 it is on no PATH entry; 128+N
    it was killed by signal N; run only: 3 a volume was left (SANDBOX LEAK),
    124 its --timeout ended it. A command that itself exits 125-127 (or 71,
    sandbox-exec's own exec failure) is told from the tool by that line.
  probe, policy, check, version, worktree, egress: 0 done, 1 the verb ran and
    said NO (probe, egress), 2 could not run (a usage error). reap: 0 clean, 3
    something remained. <verb> -h gives one verb's codes and flags.
```

`nova-sandbox probe -h`:

```
usage: nova-sandbox probe [flags]
from `nova-sandbox help`:
  nova-sandbox probe --write <dir>... [--read <dir>...] [--secret <path>] [--net-deny]
  nova-sandbox probe --write /path/to/pool/jobs/j1 \
  --secret /path/to/.config/anthropic/env
flags:
  --gpu <mode>  the local GPU capability mode: none (the default) or metal
  --json  print the result as one JSON object on stdout, a refusal included
  --net-deny  an ENFORCED network denial, or a refusal; without it the line says net=nopromise
  --net-listen  grant inbound ip as well; never with --net-deny
  --read <dir>  a dir readable and NOT writable, recursively; it carries execute. Repeatable
  --read-noexec <dir>  a dir readable, NOT writable and NOT executable: a cache or a data tree. Repeatable
  --secret <path>  a file path the probe proves it cannot read, outside every --read and --write; its contents are never read
  --write <dir>  a dir readable and writable, recursively. Repeatable and REQUIRED; HOME must sit inside one
exit codes: each verb's own, by verb:
```

`nova-sandbox policy -h`:

```
usage: nova-sandbox policy [flags]
from `nova-sandbox help`:
  nova-sandbox policy --read <dir>... --write <dir>... [--net-deny] [--net-listen]
  [-- <command> <args...>]
flags:
  --cwd <dir>  the command's working dir, inside a --write (default: the first --write)
  --gpu <mode>  the local GPU capability mode: none (the default) or metal
  --json  print the result as one JSON object on stdout, a refusal included
  --name <container>  the windows container name; accepted and ignored on darwin and linux
  --net-allow <host:port>  open the loopback host:port named. Repeatable
  --net-deny  an ENFORCED network denial, or a refusal; without it the line says net=nopromise
  --net-listen  grant inbound ip as well; never with --net-deny
  --read <dir>  a dir readable and NOT writable, recursively; it carries execute. Repeatable
  --read-noexec <dir>  a dir readable, NOT writable and NOT executable: a cache or a data tree. Repeatable
  --tmp <dir>  the child's TMPDIR, a dir inside a --write (default: <first --write>/.nova-sandbox-tmp)
  --write <dir>  a dir readable and writable, recursively. Repeatable and REQUIRED; HOME must sit inside one
exit codes: each verb's own, by verb:
```

`nova-sandbox check -h`:

```
usage: nova-sandbox check [flags]
from `nova-sandbox help`:
  nova-sandbox check
flags:
  --json  print the result as one JSON object on stdout, a refusal included
exit codes: each verb's own, by verb:
```

`nova-sandbox run -h`:

```
nova-sandbox run: one command, in a DISPOSABLE place that is deleted on exit (darwin)

usage:
  nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]...
                   [--container <disk>] -- <command> <args...>

  --name <n>      the volume is nova-<n>, mounted at /Volumes/nova-<n>. Letters,
                  digits, - _ and . REQUIRED.
  --size <s>      the volume's quota, e.g. 8g or 64m. REQUIRED: a disposable place
                  with no ceiling can fill the boot disk.
  --timeout <d>   a Go duration after which the whole process group is killed and
                  the volume deleted anyway. Exit 124.
  --go            add the Go toolchain's own roots as --read: GOROOT and GOMODCACHE,
                  as `go env` reports them. A toolchain outside the roots the
                  profile already grants is unreadable inside the wall, and a module
                  cache lives under the caller's home, which the wall denies -- so a
                  card that builds Go wants this flag, and the alternative is naming
                  both by hand in every argv.
  --max-procs <n> the most processes the command's tree may hold, default 256.
                  Counted every second; past it the whole process group is killed
                  and the run ends "runaway: <n> processes" with exit 137.
  --max-mem <s>   the most resident memory the tree may hold, default 8g. Past it
                  the group is killed and the run ends "runaway: <n> bytes of memory".
  --read <dir>    readable, recursively, and NOT writable. Repeatable.
  --out <dir>     the ONE writable path off the volume. After the command exits
                  and BEFORE the volume is deleted, the named artifacts are
                  copied to <dir>/<name>/. Without it nothing survives the run,
                  which is the whole point of the verb and the wrong answer for
                  a card that made a commit.
  --artifact <p>  what leaves, relative to the card's working directory.
                  Repeatable. Default: RESULT.md, usage.tsv and repo.bundle,
                  each taken IF PRESENT. An artifact you NAME and did not write
                  is a refusal. A directory is taken whole.
  --out-max-bytes <n>
                  the ceiling on the whole set, default 64m. Measured before a
                  byte is written; over it is a refusal, not a truncation.
  --container <d> the APFS container to make the volume in. Default: the container
                  the boot volume is in.
```

`nova-sandbox reap -h`:

```
nova-sandbox reap: clear the disposable volumes a killed run left behind (darwin)

usage:
  nova-sandbox reap [--dry-run]

  --dry-run   print what a reap would take and touch NOTHING: no signal is sent,
              no volume is deleted.
```

`nova-sandbox worktree -h`:

```
usage: nova-sandbox worktree [flags]
from `nova-sandbox help`:
  nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id> [--base <branch>]
  nova-sandbox worktree --repo <dir> --scratch <dir> --prune
flags:
  --base <branch>  the branch it is compared against (default: the pull request's base)
  --pr <id>  the pull request id whose head the worktree is placed at
  --prune  remove the worktrees under --scratch whose pull request merged or closed, or that sat unused and stale; never with --pr
  --repo <dir>  the repository dir the worktree is made from, an absolute path
  --scratch <dir>  an existing dir the worktrees and their records live in; never created
exit codes: each verb's own, by verb:
```

`nova-sandbox egress plan -h`:

```
usage: nova-sandbox egress plan [flags]
from `nova-sandbox help`:
  nova-sandbox egress plan  --run <id> --policy <file> --model-host <host>
  --resolver <ip> [--bench-cidr <cidr>]... [--uid <n>] [--veth <if>]
  --out <file>
flags:
  --bench-cidr <cidr>  another bench's cidr, denied. Repeatable
  --model-host <host>  the ONE model host of this run, already a line in the policy
  --out <file>  the file the ruleset is written to
  --policy <file>  the reviewed allowlist file in git
  --resolver <ip>  the only ip UDP 53 is allowed to
  --run <id>  the run id this wall belongs to; the table is nova_egress_<id>
  --uid <uid>  the container's uid on the host; a plan needs --uid or --veth
  --veth <interface>  the container's interface; a plan needs --uid or --veth
exit codes: each verb's own, by verb:
```

`nova-sandbox egress apply -h`:

```
usage: nova-sandbox egress apply [flags]
from `nova-sandbox help`:
  nova-sandbox egress apply --plan <file> --run <id>                    (linux)
flags:
  --plan <file>  the ruleset file egress plan wrote
  --run <id>  the run id the plan belongs to
exit codes: each verb's own, by verb:
```

`nova-sandbox egress check -h`:

```
usage: nova-sandbox egress check [flags]
from `nova-sandbox help`:
  nova-sandbox egress check --plan <file>
flags:
  --plan <file>  the ruleset file to read back and check
exit codes: each verb's own, by verb:
```

`nova-sandbox egress drop -h`:

```
usage: nova-sandbox egress drop [flags]
from `nova-sandbox help`:
  nova-sandbox egress drop  --run <id>                                  (linux)
flags:
  --run <id>  the run id whose table is taken away
exit codes: each verb's own, by verb:
```

`nova-sandbox version -h`:

```
usage: nova-sandbox version [flags]
from `nova-sandbox help`:
  nova-sandbox version
exit codes: each verb's own, by verb:
```
<!-- clidoc:end nova-sandbox -->

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

<!-- clidoc:begin nova-tokens -->
`nova-tokens help`:

```
nova-tokens: token spend per day, model and repository, read from AI session logs

how it works: fold reads the logs you name (Claude Code transcripts, OpenCode
databases, swarm pools, bus notes) and writes one day file per day into --out,
one row per (day, model, repo). The repo comes from the --repos file: lines of
<name><TAB><regexp>, and the first match on a session's path wins. check, sum
and report read the day files back; a count a source never gave prints as -.
first run: create a tiny transcript and rules file with the lines under setup:
above example:, then run the lines under example: in order.

usage:
  nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
                      [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>] [--dry-run]
  nova-tokens report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>
                      mode: local note body, printed as the tokens note artifact
                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
                      [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>] [--dry-run]
  nova-tokens report (store mode) --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
                      mode: Redis month summary
                      [--user <name>] [--password-env <NAME>]
  the local mode is selected by --who and --day; the store mode by --redis and --month; giving both --who and --redis selects the store mode (--redis wins); a mix of --who and --redis prints the store summary
  nova-tokens ledger  --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
                      [--user <name>] [--password-env <NAME>] [--dry-run]
  nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
  nova-tokens check   --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--allow-empty] [--max <n>]
  nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--unattributed] [--max <n>]
  nova-tokens profiles --swarm-root <dir>
                      one PROFILES MODEL line per model (cards, median output, overshoot), then a PROFILES OK line with totals
  nova-tokens session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]
                      [--role <name>] [--weights <in,cw,cr,out>]
  nova-tokens version

Every verb but version takes --json: the same result as one JSON object on stdout, a
refusal included. A verb that writes takes --dry-run: it is the real run's own plan --
it reads what the real run reads and refuses what the real run refuses -- prints what it
would write with dry_run=true on its last line, and writes nothing (ledger --dry-run dials
no store). The one difference: a dry fold or session takes no fold.lock, so it neither waits for nor
refuses on a fold holding one. --opencode under --dry-run (and under sources) still reads a
copy of the database, made in a new directory of the run's own under --scratch
(.nova-tokens-dry-run-*) and removed before it exits: --scratch is left as it was. `<verb> -h` lists a verb's flags and states its effect.

exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
source, an unparsed bus line or note, a row of two day bases, a lane-day with competing
reports, a day that would shrink, a fold whose every message had no id and so folded nothing,
a check finding (an --out holding no day file is one), a report with nothing to show; 2 could
not run: a missing flag, a bad flag value, a duplicate label, two sources of one
provider sharing message ids, sqlite3 absent when
--opencode is given, a second fold holding the lock.

EXIT 1 STILL WRITES. A fold with one unreadable file writes every day it could compute
and exits 1: the exit code is about the claim -- a declared source is a claim that the
report covers it -- and written=true on the TOKENS DAY line is about the files.

Every path is a flag. There is no default output directory, no default transcript
directory, no default database, no default bus and no default rules file. fold, report
(its local mode), sum, check, sources, profiles and session read no environment variable
for a path or a setting: ,  and  are ignored, and a test sets
them and proves it. Three things do read the environment: --opencode runs sqlite3 found
on ; and the two Redis verbs, ledger and report --redis, take the store's ACL user
from --user, else NOVA_SPRINT_REDIS_USER, and its password from the variable
--password-env names, else (with a user) the one NOVA_SPRINT_REDIS_PASSWORD_ENV names,
else NOVA_REDIS_BENCH_PASSWORD. The password is never a flag. --timeout is the one flag
with a default, 120 seconds, because it is how long this tool waits before saying so
rather than a fact about your data.

A source is declared by flag and every row names its sources, so every number in a day
file is traceable to the flags of the run that wrote it. A label is [a-z0-9-]+, at most
32 characters, and unique across the run. --scratch is required with --opencode and
refused without it, because a scratch directory with nothing to put in it is a flag that
does nothing. A fold or a report copies the database, with its -wal and -shm, into
--scratch/opencode-<label>/, replacing the copy there, and leaves it; the live file is
never opened, because sqlite3 keeps a WAL index beside the file it reads.

The five types -- input, output, cache_write, cache_read, reasoning -- are kept apart, and
a type the source did not report is written a dash, NEVER 0. A provider that does not expose
reasoning is not evidence that none occurred, and a zero meaning "not measured" would sum
into a month claiming to be complete. sum counts the dashes beside the totals. The same
holds for cost: usd= on a TOKENS AVG line is - when no source reported a cost for it.

A day that would go backwards is refused: TOKENS SHRANK names the type, what the file
said and what the sources say now, the file is left as it was, and --allow-shrink is the
person's act. A source that became unreadable must never quietly lower a day's spend.

Two declared sources of one provider that feed the same message ids are refused
before any day file is written. The refusal names both labels and the duplicate
count, and the remedy is to drop one of the two flags. An id is comparable only
within one provider, and a shared id is not dropped from the other source, so
check and sum never see a doubled day.

A fold merges into the day file by SOURCE: it recomputes the rows its own declared sources
wrote and keeps every other row exactly as it is, so a run that declares one source does
not erase what the others reported. A row it can neither keep nor recompute -- one already
summed over a declared and an undeclared source -- is TOKENS PARTIAL, nothing of that day
is written, and --allow-shrink does not write it either.

Two notes for one day in one lane are one report only when the later names the earlier in
its subject: supersedes=<id>[,<id>...], sorted, no duplicates. Nothing else orders them --
not the Date, not the filename, not the directory listing, not the git history. Two tips
are TOKENS CONFLICT, nothing folds for that lane-day, and the remedy names every tip; one
note whose predecessor set names them all clears it.

--note <path> is written whole through atomicfile: the file and its directory must not be
symlinks.

fold and session hold --out/fold.lock while they write, so two folds of one --out never
write the same day at once; the second waits, then refuses naming the holder. The lock
file holds the folding process's id and stays in --out between runs (it is never data);
check counts it as neither a day file nor a stray.

This tool removes nothing it was given. There is no month file, sum writes nothing, check
names a stray and leaves it, and no verb deletes, truncates or trims a file it did not make:
the one removal is the private database copy a dry run or sources made under --scratch.

check counts what it does not name. A calendar day between the first and the last with no
file is gap=<n>, and it is MISSING only when something says there was spend on it:
--strict names every gap, --no-spend <file> (one YYYY-MM-DD per line, the days that had
none) names the gaps your list does not account for. A *.md, a *.log or a pre-* archive
directory beside the day files is notes=<n> rather than a stray; --strict names those too.
A gate that cannot go green is a gate people stop reading, and both counts stay on the
CHECK line, so nothing was hidden to make it green. A gate that cannot go red is no gate
either: an --out with no day file in it is CHECK FAILED, never a green over nothing.

sources --unattributed prints the path stems that were SEEN and matched no rule, heaviest
first, capped by --max, with the mentions each stem got (one per message that touched a path
in it). That listing is what other=<pct>% on a day line is made of, and it is the evidence
for improving the --repos file.

setup:
  mkdir -p ./transcripts ./out
  printf '%s' '{"type":"assistant","timestamp":"2026-09-11T09:12:' > ./transcripts/window.jsonl
  printf '%s' '00Z","message":{"id":"example-1","model":"claude-' >> ./transcripts/window.jsonl
  printf '%s' 'fable-5-1","usage":{"input_tokens":812,' >> ./transcripts/window.jsonl
  printf '%s' '"output_tokens":40,"cache_creation_input_tokens":' >> ./transcripts/window.jsonl
  printf '%s' '1200,"cache_read_input_tokens":90000},' >> ./transcripts/window.jsonl
  printf '%s' '"content":[{"type":"tool_use","input":{' >> ./transcripts/window.jsonl
  printf '%s\n' '"file_path":"/work/schema/wire.md"}}]}}' >> ./transcripts/window.jsonl
  cp ./transcripts/window.jsonl ./session.jsonl
  printf 'schema\t(^|/)schema($|/)\n' > ./repos.tsv
```

`nova-tokens fold -h`:

```
usage: nova-tokens fold [flags]
from `nova-tokens help`:
  nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
  [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
  [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>] [--dry-run]
  nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
effect: local write: writes the day files in --out, holding --out/fold.lock while it writes, and with --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run reads the same sources, refuses what the real run refuses, and writes nothing (its database copy is made in a new directory under --scratch and removed before it exits)
flags:
  --all  fold every day named by the sources
  --allow-shrink  write a day even when its totals shrink
  --bus <string>  nova-bus directory with token notes
  --claude <value>  labeled Claude Code transcript directory; repeatable
  --day <string>  one UTC day to fold as YYYY-MM-DD
  --dry-run  read the sources and print what would be written, and write nothing (no day file, no lock)
  --exclude <value>  path or glob kept out of a recursive source tree (--claude), repeatable (nothing is excluded by default)
  --json  print the result as one JSON object on stdout instead of lines
  --max <int>  maximum findings or rows to print; 0 prints all
  --max-files <int>  ceiling on the transcript files one --claude tree holds (default 20000); the whole tree is walked and counted before any file is opened, and a tree over the ceiling is refused naming the files and bytes it found; 0 is no ceiling
  --opencode <value>  labeled OpenCode database file; repeatable
  --out <string>  directory for daily token files
  --provider <value>  kind:labeled provider export file; repeatable
  --repos <string>  tab-separated repo names and path regular expressions
  --scratch <string>  directory the OpenCode database is copied into: opencode-<label>/ in it, replaced and left by a run that writes; a new directory removed before exit by a dry run or sources
  --swarm <value>  labeled swarm pool directory; repeatable
  --timeout <int>  seconds to wait for the OpenCode sqlite3 reader
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens report -h`:

```
usage: nova-tokens report [flags]
from `nova-tokens help`:
  nova-tokens report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>
  mode: local note body, printed as the tokens note artifact
  [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
  [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>] [--dry-run]
  nova-tokens report (store mode) --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
  mode: Redis month summary
  [--user <name>] [--password-env <NAME>]
  nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts
effect: local write: --note writes the note body to that file, and --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run names the note, copies the database only into a new directory under --scratch removed before it exits, and writes nothing; --redis reads the ledger store over the network, with or without --dry-run
flags:
  --bus <string>  nova-bus directory with token notes
  --by <string>  Redis summary grouping: model, repo, day or tuple
  --claude <value>  labeled Claude Code transcript directory; repeatable
  --day <string>  one UTC day to report as YYYY-MM-DD
  --dry-run  print the body and name the --note file, and write no file
  --exclude <value>  path or glob kept out of a recursive source tree (--claude), repeatable (nothing is excluded by default)
  --json  print the result as one JSON object on stdout instead of lines
  --max <int>  maximum summary rows to print; 0 prints all
  --max-files <int>  ceiling on the transcript files one --claude tree holds (default 20000); the whole tree is walked and counted before any file is opened, and a tree over the ceiling is refused naming the files and bytes it found; 0 is no ceiling
  --month <string>  month to summarize as YYYY-MM
  --note <string>  atomically write the note body to this path
  --opencode <value>  labeled OpenCode database file; repeatable
  --password-env <string>  environment variable holding the Redis password
  --provider <value>  kind:labeled provider export file; repeatable
  --redis <string>  Redis address for the store summary mode
  --repos <string>  tab-separated repo names and path regular expressions
  --scratch <string>  directory the OpenCode database is copied into: opencode-<label>/ in it, replaced and left by a run that writes; a new directory removed before exit by a dry run or sources
  --supersedes <value>  note id this report replaces; repeatable
  --swarm <value>  labeled swarm pool directory; repeatable
  --timeout <int>  seconds to wait for the OpenCode sqlite3 reader
  --user <string>  Redis username for the store summary mode
  --who <string>  name to write in each note body row
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens ledger -h`:

```
usage: nova-tokens ledger [flags]
from `nova-tokens help`:
  nova-tokens ledger  --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
  [--user <name>] [--password-env <NAME>] [--dry-run]
effect: delivery: writes each day file's rows to the Redis store at --redis (tokens:ledger:<day>); --dry-run reads the day files, prints what it would write, and dials no store
flags:
  --day <string>  one UTC day to index as YYYY-MM-DD
  --dry-run  read the day files and print the rows that would be written, and dial no store
  --json  print the result as one JSON object on stdout instead of lines
  --month <string>  month of day files to index as YYYY-MM
  --out <string>  directory containing daily token files
  --password-env <string>  environment variable holding the Redis password
  --redis <string>  Redis address for the ledger store
  --user <string>  Redis username for the ledger store
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens sum -h`:

```
usage: nova-tokens sum [flags]
from `nova-tokens help`:
  nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
  nova-tokens sum --out ./out --month 2026-09
effect: inspection: reads, writes nothing
flags:
  --json  print the result as one JSON object on stdout instead of lines
  --max <int>  maximum rows to print; 0 prints all
  --month <string>  month to sum as YYYY-MM
  --out <string>  directory holding daily token files
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens check -h`:

```
usage: nova-tokens check [flags]
from `nova-tokens help`:
  nova-tokens check   --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--allow-empty] [--max <n>]
  nova-tokens check --out ./out
effect: inspection: reads, writes nothing
flags:
  --allow-empty  answer OK on an --out holding no day file; without it files=0 is FAILED, never a green over nothing
  --json  print the result as one JSON object on stdout instead of lines
  --max <int>  maximum findings to print; 0 prints all
  --no-spend <string>  file listing UTC dates with no spend, one per line
  --out <string>  directory containing daily token files
  --strict  treat every gap and note as a finding
  --through <string>  require coverage through this UTC day, YYYY-MM-DD
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens sources -h`:

```
usage: nova-tokens sources [flags]
from `nova-tokens help`:
  nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all) [<source flags>] [--unattributed] [--max <n>]
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts
  nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20
effect: inspection: reads, writes nothing (--opencode reads a copy made in a new directory under --scratch and removed before it exits)
flags:
  --all  inspect every day named by the sources
  --bus <string>  nova-bus directory with token notes
  --claude <value>  labeled Claude Code transcript directory; repeatable
  --day <string>  one UTC day to inspect as YYYY-MM-DD
  --exclude <value>  path or glob kept out of a recursive source tree (--claude), repeatable (nothing is excluded by default)
  --json  print the result as one JSON object on stdout instead of lines
  --max <int>  maximum rows to print; 0 prints all
  --max-files <int>  ceiling on the transcript files one --claude tree holds (default 20000); the whole tree is walked and counted before any file is opened, and a tree over the ceiling is refused naming the files and bytes it found; 0 is no ceiling
  --opencode <value>  labeled OpenCode database file; repeatable
  --provider <value>  kind:labeled provider export file; repeatable
  --repos <string>  tab-separated repo names and path regular expressions
  --scratch <string>  directory the OpenCode database is copied into: opencode-<label>/ in it, replaced and left by a run that writes; a new directory removed before exit by a dry run or sources
  --swarm <value>  labeled swarm pool directory; repeatable
  --timeout <int>  seconds to wait for the OpenCode sqlite3 reader
  --unattributed  list seen paths that matched no repo rule, with the mentions each got (one per message that touched it)
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens profiles -h`:

```
usage: nova-tokens profiles [flags]
from `nova-tokens help`:
  nova-tokens profiles --swarm-root <dir>
  one PROFILES MODEL line per model (cards, median output, overshoot), then a PROFILES OK line with totals
effect: inspection: reads, writes nothing
flags:
  --json  print the result as one JSON object on stdout instead of lines
  --swarm-root <string>  root containing the swarm pool profiles
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens session -h`:

```
usage: nova-tokens session [flags]
from `nova-tokens help`:
  nova-tokens session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]
  [--role <name>] [--weights <in,cw,cr,out>]
  nova-tokens session --claude-session ./session.jsonl --out ./out
effect: local write: with --out it writes the session's days into the day files there, holding --out/fold.lock; without --out, or with --dry-run, it writes nothing
flags:
  --claude-session <string>  one Claude Code session transcript jsonl
  --day <string>  one UTC day to write as YYYY-MM-DD; defaults to every stamped day
  --dry-run  with --out, print the days that would be written, and write nothing (no directory, no day file, no lock)
  --json  print the result as one JSON object on stdout instead of lines
  --out <string>  directory for the resulting daily token file
  --role <string>  the role the rows are booked under: given, the row is <model>/<role>; the default books the bare model
  --weights <string>  the WEIGHTED ratios as in,cw,cr,out -- a comparison, not a price: the defaults are the ratios of one vendor's published list prices; set your own
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```

`nova-tokens version -h`:

```
usage: nova-tokens version [flags]
from `nova-tokens help`:
  nova-tokens version
effect: inspection: reads, writes nothing
exit codes: 0 the verb ran and passed; 1 the verb ran and said FAILED -- an unreadable
```
<!-- clidoc:end nova-tokens -->

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

<!-- clidoc:begin nova-update -->
`nova-update help`:

```
nova-update: compare installed tools with their latest releases, and update one when asked

how it works: the manifest is a tab-separated file you write, one tool per line:
how to read its installed version, where its latest release is published, and
the command that installs it. check and report compare the two; apply runs one
named entry's command and reads the version again, nothing else. The release
verbs build, publish and install nova-tools' own releases.
first run: the binary alone; the lines under example: write a one-tool manifest
(Go) to ./versions.tsv and read it; they install nothing.

usage:
  nova-update example [--out <path>]
  nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update status --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update apply --file <path> <name> [--version <v>] [--dry-run] [--timeout <d>]
  nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to
    <who,who> | --send --as <friend> --to <who,who>]
    [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update report --store <host:port> [--timeout <d>]
  nova-update watch --adopt <checks.tsv> [--as <friend> --to <who,who>] [--host <label>]
    [--timeout <d>] [--budget <d>]
  nova-update adoption --file <path> [--as <friend>] [--max <n>]
  nova-update release <cut|build|install|adopt|pull> ...
    nova-tools' own release pipeline: nova-update help release prints its usage lines
  nova-update help
  nova-update version (or --version)

Defaults: --max 20 (0 = all), --timeout 5s, --budget 60s. Repeat --kind to select kinds.

Every verb but watch and release takes --json: the same result as one JSON object on stdout. A result's first line is the verb, OK, FAIL or REFUSED, and the run's counts; `<verb> -h` lists a verb's flags and effect.

Report needs no bus or network. Updates require an explicit apply name. status is check with every entry shown, current ones too. apply --dry-run prints the plan and writes nothing.

A delivery is one nova-bus send on the Redis bus (nova-bus reads its store from NOVA_BUS_REDIS); with --snapshot, a report unchanged since it was confirmed sent to the same recipients is not sent again.

A snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running.

Locals: latest=local:<path> runs that binary (or argv) on this host to read the version; e.g., local:/usr/local/bin/nova-update or local:go version. The installed column can be a version string (v1.2.3), a single command name found on PATH, or a full argv.

Both nova-update and nova-version read this manifest: they are two binaries that share the manifest reader and report (report prints the same lines under either). Use nova-update to ASK whether what you depend on is current and to CHANGE it: check and status (installed against latest, one line per finding; status shows the current ones too), apply (install the one entry you name, or print the plan with --dry-run), watch (run a file of adoption checks and post the receipt), adoption (list who adopted which tool) and release (cut, build, install, adopt and pull a nova-tools release). Use nova-version to RECORD what is installed: snapshot, diff, moved and send are nova-version's.

THE MANIFEST is the file --file names, written by hand, the same for both tools:
  nova-update report --file versions.tsv     the six lines that say what versions.tsv holds:
      1. line 1 is the header, byte for byte: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner; every other line is six fields, one tab between, none empty; a line starting # is a comment
      2. kind is harness, engine, model, tool or pin; name is unique in the file; owner is who answers for it
      3. installed is a version (v1.2.3), a command name on PATH, or an argv whose first line of output carries the version (single spaces, no quotes)
      4. latest is github:<owner>/<repo>, npm:<package>, brew:<formula>, ollama:<model>:<tag> (kind model), local:<argv> (a pin takes this only), or - for not known yet
      5. apply is the argv that updates it, or none, split on single spaces, no quotes, no shell: a pipe, a glob or a  is a literal argument; a run prints EVERY problem of the file at once, each with its line, never the first alone
      6. example: go<TAB>tool<TAB>go version<TAB>local:go version<TAB>none<TAB>me

exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update example -h`:

```
usage: nova-update example [flags]
from `nova-update help`:
  nova-update example [--out <path>]
  nova-update example --out versions.tsv
effect: inspection: prints the example manifest; with --out, local write: writes it, never over another file
flags:
  --json  print the result as one JSON object instead of lines
  --out <string>  write the example manifest to this path (an existing file is never overwritten); without it, print the manifest
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update check -h`:

```
usage: nova-update check [flags]
from `nova-update help`:
  nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
effect: inspection: reads each tool's installed version and asks its latest source (github:, npm:, brew: and ollama: are network reads); writes nothing
flags:
  --budget <duration>  the whole run's deadline, such as 60s
  --file <string>  the manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --json  print the result as one JSON object instead of lines
  --kind <value>  read only entries of this kind (harness, engine, model, tool or pin); repeat for several
  --max <int>  lines listed per kind before one MORE line stands for the rest; 0 lists all
  --timeout <duration>  one read's deadline, such as 5s
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update status -h`:

```
usage: nova-update status [flags]
from `nova-update help`:
  nova-update status --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update status --file versions.tsv
effect: inspection: the reads of check; writes nothing
flags:
  --budget <duration>  the whole run's deadline, such as 60s
  --file <string>  the manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --json  print the result as one JSON object instead of lines
  --kind <value>  read only entries of this kind (harness, engine, model, tool or pin); repeat for several
  --max <int>  lines listed per kind before one MORE line stands for the rest; 0 lists all
  --timeout <duration>  one read's deadline, such as 5s
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update apply -h`:

```
usage: nova-update apply [flags]
from `nova-update help`:
  nova-update apply --file <path> <name> [--version <v>] [--dry-run] [--timeout <d>]
  nova-update apply --file versions.tsv go --dry-run
effect: local write: runs the named entry's apply command, which installs; --dry-run starts no process and writes nothing
flags:
  --dry-run  print the plan and install nothing: no process starts
  --file <string>  the manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --json  print the result as one JSON object instead of lines
  --timeout <duration>  the deadline of each version read and of the install command itself, such as 5m for a slow installer
  --version <string>  the version to install, when the entry's apply argv holds {version}; default: the latest its source reports
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update report -h`:

```
usage: nova-update report [flags]
from `nova-update help`:
  nova-update report --file <path> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to
  <who,who> | --send --as <friend> --to <who,who>]
  [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-update report --store <host:port> [--timeout <d>]
  nova-update report --file versions.tsv     the six lines that say what versions.tsv holds:
  1. line 1 is the header, byte for byte: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner; every other line is six fields, one tab between, none empty; a line starting # is a comment
  2. kind is harness, engine, model, tool or pin; name is unique in the file; owner is who answers for it
  3. installed is a version (v1.2.3), a command name on PATH, or an argv whose first line of output carries the version (single spaces, no quotes)
  4. latest is github:<owner>/<repo>, npm:<package>, brew:<formula>, ollama:<model>:<tag> (kind model), local:<argv> (a pin takes this only), or - for not known yet
  5. apply is the argv that updates it, or none, split on single spaces, no quotes, no shell: a pipe, a glob or a  is a literal argument; a run prints EVERY problem of the file at once, each with its line, never the first alone
  6. example: go<TAB>tool<TAB>go version<TAB>local:go version<TAB>none<TAB>me
  nova-update report --file versions.tsv
effect: inspection: reads each installed version, no latest, no network; --snapshot writes its state file (local write); --send delivers the note through nova-bus (delivery); --store reads the fleet's Redis
flags:
  --as <string>  the sender the note is from
  --budget <duration>  the whole run's deadline, such as 60s
  --draft  print the note that --send would deliver, and deliver nothing (needs --as, --to)
  --file <string>  the manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --host <string>  a label for the machine the report ran on, carried in the note's subject
  --json  print the result as one JSON object instead of lines
  --kind <value>  read only entries of this kind (harness, engine, model, tool or pin); repeat for several
  --max <int>  lines listed per kind before one MORE line stands for the rest; 0 lists all
  --send  deliver the note through nova-bus (needs --as, --to)
  --snapshot <string>  a state file that records what was observed and what each recipient was confirmed sent: an unchanged report is not sent again
  --store <string>  a fleet Redis host:port: report every bench's nova-sprint build from its beat, instead of --file
  --timeout <duration>  one read's deadline, such as 5s
  --to <string>  the recipients, comma-separated
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update watch -h`:

```
usage: nova-update watch [flags]
from `nova-update help`:
  nova-update watch --adopt <checks.tsv> [--as <friend> --to <who,who>] [--host <label>]
  [--timeout <d>] [--budget <d>]
effect: inspection: runs each check's command; with --as and --to, delivery: the receipt goes out through nova-bus send
flags:
  --adopt <string>  the checks file (required): a header line check<TAB>command<TAB>owner, then one check per line, its command run as written
  --as <string>  the sender the receipt is from; with --to, the pass posts the receipt through nova-bus send (nova-bus reads its store from NOVA_BUS_REDIS)
  --budget <duration>  the whole pass's deadline, such as 60s
  --host <string>  a label for the machine the pass ran on, carried in the receipt's subject
  --timeout <duration>  one check's deadline, such as 5s
  --to <string>  the receipt's recipients, comma-separated (those who answer a refused check)
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update adoption -h`:

```
usage: nova-update adoption [flags]
from `nova-update help`:
  nova-update adoption --file <path> [--as <friend>] [--max <n>]
effect: inspection: reads the ledger, writes nothing
flags:
  --as <string>  list only this friend's choices
  --file <string>  the adoption ledger (required): one line per tool choice, five tab-separated fields tool friend state version detail, written by hand
  --json  print the result as one JSON object instead of lines
  --max <int>  choices listed before one MORE line stands for the rest; 0 lists all
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```

`nova-update release -h`:

```
nova-update release cut --repo <owner/name> --from <branch> --version <v> --changelog <path> [--sums <file>] [--security-read <id|url>] [--local-diff <checkout> [--paths-from <file>] | --paths-from <file>] [--cli <file>] [--receipts <dir>] [--no-dogfood-gate --reason <why>] [--journeys <file> | --no-journey-gate --reason <why>] [--spend-store <addr>] [--spend-since <RFC3339>] [--spend-receipts <file>] [--no-spend-gate --reason <why>] [--dry-run] [--timeout <d>]
exit codes: 0 the verb did what its line says (a --dry-run printed its plan and changed nothing); 1 it ran and a step failed partway, the FAILED or REFUSED line naming what was done and what to do next; 2 it refused before acting, naming the command to run. `nova-update release <verb> -h` lists a verb's flags.
```

`nova-update version -h`:

```
usage: nova-update version [flags]
from `nova-update help`:
  nova-update version (or --version)
  nova-update version
effect: inspection: prints this binary's version line
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 every entry current, an apply that left the box on the target (or an apply --dry-run that printed its plan), a report whose every entry answered; 1 the tool said NO (anything STALE, NEWER, DIFFERENT or UNKNOWN, an apply whose after is not the target, a report with an UNKNOWN or a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).
```
<!-- clidoc:end nova-update -->

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

<!-- clidoc:begin nova-version -->
`nova-version help`:

```
nova-version: which version of each tool is installed, recorded and compared

how it works: report reads each tool's installed version; snapshot records a directory's binaries;
diff compares two snapshots; moved writes the note of what two commits' binaries changed.
It is one of two binaries sharing the manifest and report; latest and installing are nova-update's.
THE MANIFEST is the file --file names, written by hand; report -h states its six rules.
first run: the binary alone; the example lines write a one-tool manifest and read it.

usage:
  nova-version example [--out <path>]
  nova-version moved --from <sha> --to <sha> --repo <dir> --out <path> [--timeout <d>] [--budget <d>] [--dry-run]
  nova-version snapshot --file <manifest>
  nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>] [--max <n>] [--dry-run]
  nova-version diff --from <a.tsv> --to <b.tsv>
  nova-version report --file <manifest> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-version send --file <manifest> --as <friend> --to <who,who> [--snapshot <path>] [--host <label>]
  nova-version version
  nova-version help [<verb>]

THE MANIFEST is the file --file names: one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand; report -h states its six rules.

Every verb but report, send takes --json: report and send write the note body the bus carries, so they take no --json; report's first line is `REPORT OK checked=1 known=1 unknown=0 changed=- sent=-`. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
```

`nova-version example -h`:

```
usage: nova-version example [flags]
from `nova-version help`:
  nova-version example [--out <path>]
  nova-version example --out versions.tsv
flags:
  --json  print the result as one JSON object instead of lines
  --out <string>  write the example manifest to this path (an existing file is never overwritten); without it, print the manifest
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: local write: writes files on this machine with --out, never over another file; without it, inspection: prints the example manifest
```

`nova-version moved -h`:

```
usage: nova-version moved [flags]
from `nova-version help`:
  nova-version moved --from <sha> --to <sha> --repo <dir> --out <path> [--timeout <d>] [--budget <d>] [--dry-run]
flags:
  --budget <duration>  whole run deadline
  --dry-run  build and read both revisions and print the note; write no --out
  --from <string>  the revision to compare from (required)
  --json  print the result as one JSON object instead of lines
  --out <string>  the path of the note to write (required)
  --repo <string>  the checkout holding both revisions (required)
  --timeout <duration>  one child's deadline
  --to <string>  the revision to compare to (required)
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: local write: writes files on this machine; with --dry-run, the note is printed and nothing is written but the builds' scratch
```

`nova-version snapshot -h`:

```
usage: nova-version snapshot [flags]
from `nova-version help`:
  nova-version snapshot --file <manifest>
  nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>] [--max <n>] [--dry-run]
  nova-version snapshot --file versions.tsv
flags:
  --bin <string>  directory holding the binaries
  --budget <duration>  whole run deadline
  --dry-run  read every binary and list the rows; write no --out
  --file <string>  manifest of adopted tools: count how many answer
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --out <string>  TSV snapshot to write
  --timeout <duration>  one binary's read deadline: 30s with --bin, 5s with --file unless --timeout is given
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: local write: writes files on this machine; with --file or --dry-run, inspection: writes nothing
```

`nova-version diff -h`:

```
usage: nova-version diff [flags]
from `nova-version help`:
  nova-version diff --from <a.tsv> --to <b.tsv>
flags:
  --from <string>  the snapshot to compare from (required)
  --json  print the result as one JSON object instead of lines
  --to <string>  the snapshot to compare to (required)
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: inspection: reads, writes nothing
```

`nova-version report -h`:

```
usage: nova-version report [flags]
from `nova-version help`:
  nova-version report --file <manifest> [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
  nova-version report --file versions.tsv
flags:
  --as <string>  the sender the note is from
  --budget <duration>  whole run deadline
  --draft  print the note only
  --file <string>  manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --host <string>  execution bench label
  --kind <value>  kind filter; repeat to select kinds
  --max <int>  per-kind output cap; 0 is all
  --send  explicit delivery
  --snapshot <string>  state file of what was observed and confirmed sent: an unchanged report is not sent again
  --timeout <duration>  one read deadline
  --to <string>  the recipients, comma-separated
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: delivery: sends beyond this machine; only with --send, which also writes the --snapshot state file; without --send, report reads and writes nothing (--draft prints the note)
```

`nova-version send -h`:

```
usage: nova-version send [flags]
from `nova-version help`:
  nova-version send --file <manifest> --as <friend> --to <who,who> [--snapshot <path>] [--host <label>]
flags:
  --as <string>  the sender the note is from
  --budget <duration>  whole run deadline
  --draft  print the note only
  --file <string>  manifest (required): one line per tool, six tab-separated fields name kind installed latest apply owner, written by hand
  --host <string>  execution bench label
  --kind <value>  kind filter; repeat to select kinds
  --max <int>  per-kind output cap; 0 is all
  --snapshot <string>  state file of what was observed and confirmed sent: an unchanged report is not sent again
  --timeout <duration>  one read deadline
  --to <string>  the recipients, comma-separated
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: delivery: sends beyond this machine; --snapshot writes its state file
```

`nova-version version -h`:

```
usage: nova-version version [flags]
from `nova-version help`:
  nova-version version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a
    manifest that did not read
  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was
    refused or unconfirmed; 2 usage, or a manifest that did not read
  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a
    manifest or directory that did not read
  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read
  moved: 0 the note written; 2 usage, or a revision or build that did not run
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-version -->

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

<!-- clidoc:begin nova-secrets -->
`nova-secrets help`:

```
nova-secrets: encrypted secrets in a git repository, handed to one command at a time

how it works: the store is a git working copy holding a .sops.yaml (one rule per
seat naming its recipients), a recovery.pub (the recovery key every file is also
sealed to) and one sops-encrypted <seat>.yaml per seat; a seat is a named identity
whose age key file (mode 0600) opens its file. exec decrypts only the --only names
into one command's environment; names reads names without decrypting; no value is printed.
first run: setup: makes a store from an empty directory (it needs age-keygen
and sops on PATH); first value: seals one with --stdin, the path a harness takes.

usage:
  nova-secrets version  print this build identity (--version also accepted)
  nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path>
                      --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
  nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path>
                      --machine <name> --secret <name> [--path <remote path>]
                      [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run]
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path>
                      --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…>
                      --from <source seat> --only <NAME,...> --key <path> --sops <path>
  nova-secrets seat inject --store <dir> --as <seat> --from <source seat>
                      --only <NAME,...> --key <path> --sops <path> [--no-pr]
                      [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets help

flags:
  --store <dir>        git working copy of the secrets store; check and exec also need it on a
                       named branch with an upstream tracking ref (see: nova-secrets check --help)
  --as <name>          seat name selecting <store>/<name>.yaml
  --key <path>         path to age private key identity file (mode 0600)
  --sops <path>        path to sops executable
  --age-keygen <path>  path to age-keygen executable
  --only <names|all>   comma-separated list of keys to deliver, or 'all' (exec only)
  --require <name>     assert key must be present in the file (repeatable)
  --max <n>            maximum items shown before MORE line (default 20, 0=unlimited)
  --json               names only: the result as one JSON object on stdout, a refusal included
  --machine <name>     fleet machine to place a secret on (its target comes from --machines)
  --secret <name>      the key in <store>/<as>.yaml to copy to the machine
  --path <remote path> remote path to write; default <home>/.config/nova-secrets/<secret>.env
  --machines <file>    place: fleet registry file: name, ssh target, home, tab separated
                       gate: the fleet machines registry whose seat column vouches for a
                       new recipient; without it that rule does not run and the APPROVE
                       line says machines=-
  --receipts <dir>     where placed receipts live; default ~/.config/nova-secrets/placed
  --ssh <path>         ssh executable to use (default ssh)
  --name NAME          key to seal (seal only)
  --pub <age1…>        the new seat's age public key, from its own keygen receipt (seat add only)
  --from <seat>        a seat this machine can open, whose values are re-sealed (seat add,
                       seat inject)
  --stdin              read the value from stdin instead of the terminal, the path a harness
                       takes (seal only)
  --no-pr              stop after the commit; make no gh call; return the store to its
                       starting branch (seal, seat inject)
  --dry-run            prints the plan and writes nothing (place, seal, seat inject): the file, the
                       recipients, the machine and remote path, the branch and the pull request the
                       real run would take, as PLAN lines ending in DRY-RUN OK, exit 0; no ssh, no
                       git write or push, no gh call, no sops encrypt, no value shown. It does
                       decrypt the seat file the real run reads first (place: --as's, to find
                       --secret; seal: the existing <as>.yaml, to say add or replace; seat inject:
                       --from's, to find the names), so --key must open it; seal reads no new value.
                       seal and seat inject read the store at HEAD, so their store must be
                       committed; a store with no commit yet is refused with the commit
                       that starts it
  --gh <path>          path to the gh executable (seal, seat inject; default: gh)
  --git <path>         path to the git executable (seal, seat inject; default: git)

exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
gate verdict, one line per failure), 2 could not run or refused (one line naming
the remedy); exec ends with the command's own status, and 125 when exec itself
refused and the command never ran.

setup: needs age-keygen and sops on PATH; run these from an empty directory first
  mkdir -m 700 -p ~/.config/nova-secrets
  nova-secrets keygen --as recovery --key ~/.config/nova-secrets/recovery.key \
    --age-keygen "$(command -v age-keygen)"
  mkdir -p ./secrets && git -C ./secrets init -q -b main
  sed -n 's/^# public key: //p' ~/.config/nova-secrets/recovery.key > ./secrets/recovery.pub
  nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key \
    --age-keygen "$(command -v age-keygen)" \
    --store ./secrets | sed -n 's/^SECRETS RULE   //p' > ./secrets/.sops.yaml
  git -C ./secrets add -A && git -C ./secrets commit -qm 'a new store'
  git init -q --bare ./secrets.git
  git -C ./secrets remote add origin ../secrets.git
  git -C ./secrets push -qu origin main

first value:
  printf '%s' 'a-token-value' | nova-secrets seal --store ./secrets --as ada \
    --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops \
    --name GH_TOKEN --stdin --no-pr
```

`nova-secrets version -h`:

```
usage: nova-secrets version [flags]
from `nova-secrets help`:
  nova-secrets version  print this build identity (--version also accepted)
effect: inspection. Prints this build's identity; opens no store, key or program.
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets exec -h`:

```
usage: nova-secrets exec [flags]
from `nova-secrets help`:
  nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path>
  --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
  nova-secrets exec   --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user
effect: delivery. Decrypts <store>/<as>.yaml with --key and becomes <cmd>, with only the --only values in its environment; writes nothing, prints no value.
flags:
  --as <name>  the seat's name, selecting <store>/<name>.yaml: letters, digits, - and _ (required)
  --key <path>  path of this seat's age private key file, mode 0600, as keygen made it (required)
  --only <names>  the key names to put in the command's environment, comma separated (GH_TOKEN,API_KEY), or all (required)
  --require <NAME>  a key NAME that must be in the seat's file and in --only, or exec refuses; repeatable
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets names -h`:

```
usage: nova-secrets names [flags]
from `nova-secrets help`:
  nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]
  nova-secrets names  --store ./secrets --as ada
effect: inspection. Reads the key names in <store>/<as>.yaml without decrypting it; writes nothing.
flags:
  --as <name>  the seat's name, selecting <store>/<name>.yaml: letters, digits, - and _ (required)
  --json  print the result as one JSON object on stdout, a refusal included: result, facts, items, more
  --max <n>  n names to list before a MORE line; 0 lists all (default 20)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets check -h`:

```
usage: nova-secrets check [flags]
from `nova-secrets help`:
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
  nova-secrets check  --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops
effect: inspection. Reads the store's files and git state and decrypts this seat's file with --key to prove it opens; writes nothing, prints no value.
flags:
  --as <name>  the seat's name, selecting <store>/<name>.yaml: letters, digits, - and _ (required)
  --key <path>  path of this seat's age private key file, mode 0600, as keygen made it (required)
  --max <n>  n failures of each kind to list before a MORE line; 0 lists all (default 20)
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets gate -h`:

```
usage: nova-secrets gate [flags]
from `nova-secrets help`:
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
effect: inspection. Diffs --base..--head in the store with git and judges every seat rule change; writes nothing, calls no network.
flags:
  --base <ref>  the pull request's base commit, a git ref (required)
  --head <ref>  the pull request's head commit, a git ref (required)
  --machines <file>  the fleet machines registry file, whose seat column vouches for a new recipient; without it that rule does not run and APPROVE says machines=-
  --store <dir>  dir of the store checkout the pull request is against (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets keygen -h`:

```
usage: nova-secrets keygen [flags]
from `nova-secrets help`:
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets keygen --as recovery --key ~/.config/nova-secrets/recovery.key \
  --age-keygen "$(command -v age-keygen)"
  nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key \
  --age-keygen "$(command -v age-keygen)" \
  --store ./secrets | sed -n 's/^SECRETS RULE   //p' > ./secrets/.sops.yaml
  nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen
effect: local write. Makes one age key file at --key (mode 0600) with --age-keygen, never over an existing file, and prints the .sops.yaml rule block for it.
flags:
  --age-keygen <path>  path of the age-keygen program, as printed by: command -v age-keygen (required)
  --as <name>  the seat name the key is for: letters, digits, - and _ (required)
  --key <path>  path to write the new private key to; its directory exists with mode 0700 and the file does not (required)
  --store <dir>  dir of the store, whose recovery.pub fills the rule's recovery key; without it the rule carries <recovery key>
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets place -h`:

```
usage: nova-secrets place [flags]
from `nova-secrets help`:
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path>
  --machine <name> --secret <name> [--path <remote path>]
  [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run]
  nova-secrets place  --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret DEEPSEEK_API_KEY --machines ./fleet.tsv
  nova-secrets place  --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --machine bench --secret API_KEY --machines ./fleet.tsv --dry-run
effect: delivery. Copies one value over ssh to --machine (on ssh's stdin, never in an argument) and writes a receipt under --receipts naming the sealed bytes it decrypted (file, blob) and the store's HEAD (head), never a hash of the value; it reads the seat file once and decrypts a private copy; --dry-run still decrypts that file to find --secret, prints no value, writes nothing and runs no ssh.
flags:
  --as <name>  the seat's name, selecting <store>/<name>.yaml: letters, digits, - and _ (required)
  --dry-run  prints the plan and writes nothing; seal and seat inject read the store at HEAD, so their store must be committed
  --key <path>  path of this seat's age private key file, mode 0600, as keygen made it (required)
  --machine <name>  the fleet machine's name, a row of --machines (required)
  --machines <file>  the fleet registry file: one machine per line, name, ssh target, home, tab separated (default ~/.config/nova-tools/fleet.tsv)
  --path <path>  the remote path written (default <home>/.config/nova-secrets/<NAME>.env, home from the machine's row)
  --receipts <dir>  dir the placed receipts live in (default ~/.config/nova-secrets/placed)
  --secret <NAME>  the key NAME in <store>/<as>.yaml whose value is copied (required)
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --ssh <path>  path of the ssh program (default ssh)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets placed -h`:

```
usage: nova-secrets placed [flags]
from `nova-secrets help`:
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets placed --machine bench-a
effect: inspection. Lists the receipts under --receipts for --machine, never a hash of a value: per secret its remote path, file (the seat file it was sealed in), blob (the git blob id of the sealed bytes place decrypted), head (the store's HEAD commit when place started, which need not hold that blob) and stamp; writes nothing.
flags:
  --machine <name>  the fleet machine's name whose receipts are listed (required)
  --receipts <dir>  dir the placed receipts live in (default ~/.config/nova-secrets/placed)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets seal -h`:

```
usage: nova-secrets seal [flags]
from `nova-secrets help`:
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path>
  --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets seal   --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --name API_KEY --dry-run
effect: store write. Reads one value at a hidden prompt (or --stdin), seals it into <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run reads no new value, decrypts the existing <as>.yaml (when there is one) to say whether NAME is added or replaced, prints no value and writes nothing.
flags:
  --as <name>  the seat's name, selecting <store>/<name>.yaml: letters, digits, - and _ (required)
  --dry-run  prints the plan and writes nothing; seal and seat inject read the store at HEAD, so their store must be committed
  --gh <path>  path of the gh program (default gh)
  --git <path>  path of the git program (default git)
  --key <path>  path of this seat's age private key file, mode 0600, as keygen made it (required)
  --name <NAME>  the key NAME to seal: capitals, digits and _, as GH_TOKEN (required)
  --no-pr  commit on a seal/ branch and stop: no push, no gh call; the store returns to its starting branch
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --stdin  read the value from standard input instead of a hidden terminal prompt; never put it in an argument
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets seat add -h`:

```
usage: nova-secrets seat add [flags]
from `nova-secrets help`:
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…>
  --from <source seat> --only <NAME,...> --key <path> --sops <path>
  nova-secrets seat add --store ./secrets --as bo --pub  --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops
effect: local write. Writes the new seat's rule into .sops.yaml and its <as>.yaml, re-sealed from --from; commits nothing.
flags:
  --as <name>  the new seat's name; the store holds no <name>.yaml yet (required)
  --from <seat>  the seat whose values are re-sealed: one this machine's --key opens (required)
  --key <path>  path of this machine's age private key, the one that opens --from (required)
  --only <NAMES>  the key NAMES to carry, comma separated: GH_TOKEN,API_KEY (required)
  --pub <key>  the new seat's age public key (age1…), from its own keygen receipt (required)
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```

`nova-secrets seat inject -h`:

```
usage: nova-secrets seat inject [flags]
from `nova-secrets help`:
  nova-secrets seat inject --store <dir> --as <seat> --from <source seat>
  --only <NAME,...> --key <path> --sops <path> [--no-pr]
  [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets seat inject --store ./secrets --as bo --from ada --only NOVA_REDIS_BENCH_PASSWORD --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --no-pr
  nova-secrets seat inject --store ./secrets --as worker --from lead --only API_KEY --key ~/.config/nova-secrets/lead.key --sops /opt/homebrew/bin/sops --dry-run
effect: store write. Re-seals the --only values from --from into the existing <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run decrypts the --from seat's file to find the names, prints no value and writes nothing.
flags:
  --as <seat>  the existing seat receiving the values; its <seat>.yaml is in the store (required)
  --dry-run  prints the plan and writes nothing; seal and seat inject read the store at HEAD, so their store must be committed
  --from <seat>  the seat whose values are re-sealed: one this machine's --key opens (required)
  --gh <path>  path of the gh program (default gh)
  --git <path>  path of the git program (default git)
  --key <path>  path of this machine's age private key, the one that opens --from (required)
  --no-pr  commit on a seal/ branch and stop: no push, no gh call; the store returns to its starting branch
  --only <NAMES>  the key NAMES to carry, comma separated: GH_TOKEN,API_KEY (required)
  --sops <path>  path of the sops program, as printed by: command -v sops (required)
  --store <dir>  the store's dir: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)
exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
```
<!-- clidoc:end nova-secrets -->

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

<!-- clidoc:begin nova-ci -->
`nova-ci help`:

```
nova-ci: test-time budgets over go test -json output, and this repository's own CI steps

how it works: slowtests and functional work in any Go module and keep no state:
slowtests reads go test -json events on stdin and prints a CI-SLOW line for each
package or test over its budget and one CI-LOAD line; functional names the
packages holding functional-tagged tests. local, new-rule and new-verb need a
nova-tools checkout; github receipt writes one row of a CI run to a Redis store.
first run: nothing to set up: slowtests --example reads a built-in event stream.
In your own module, slowtests reads the events of the packages you name, at a
60-second budget; the commands under example: are what runs.

usage, in any Go module (no state, no store):
  nova-ci help        print this banner and the verbs below (inspection)
  nova-ci version     which build this is: <version> <goos>/<goarch> <go version>
  nova-ci slowtests [--budget <seconds> | --package-budget <s>] [--test-budget <s>]
                    [--allowlist <file>] [--sleeps <file>] [--enforce]
                    [--load <n> --cpus <n>] [--example] [--allow-empty] [--json] [--max <n>]
                      (inspection) read newline-delimited `go test -json`
                      TestEvents on stdin (or the built-in example stream with
                      --example) and print one CI-SLOW line per package whose
                      total elapsed time is over its budget (--budget, whole
                      seconds, default 60; --package-budget replaces it) and
                      per top-level test over --test-budget, then one CI-LOAD
                      line. The allowlist (row shape:
                      internal/pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>,
                      where is run<id> or a bench, - in the test column for a
                      package's own row; bound: budget sits between its
                      measurement and three times it, the 3x headroom ceiling)
                      raises one package's or test's budget. One row,
                      tab-separated:
                      internal/ci/slowtests	TestA	4.5	3s@run1
                      The host's load average (the
                      larger of its 1- and 5-minute figures, over its CPUs;
                      --load and --cpus give them by hand) is printed and never
                      read by the verdict. The times are a measurement: a
                      CI-SLOW line fails the run only with --enforce (the
                      nightly reference leg). A test skipped with the marker "SLEEPS:"
                      and not on --sleeps (internal/pkg<TAB>test<TAB>where) is a
                      CI-SLEEPS line and fails the run on every leg. A package
                      go test served from its test cache reports a package
                      elapsed near zero, so a cached run never trips a package
                      budget; its tests replay the cached times, which
                      --test-budget still reads (measure with -count=1). A run
                      with more finding lines than --max prints the first --max
                      and one CI-SLOW MORE shown=<n> total=<n> line naming the
                      flag that prints the rest; --max 0 prints every finding.
                      --json prints the same verdict as one JSON object.
  nova-ci functional <package-dir>...
                      (inspection) print the packages among these that hold
                      functional tests (a _test.go built only under the
                      functional build tag) on one line and a go test -run
                      pattern naming exactly those tests on the next; when
                      there are none, one line
                      CI FUNCTIONAL OK packages=0 reason=<why>. A flag, and a
                      pattern matching no package, are refused.

usage, in a nova-tools checkout (this repository's own CI steps):
  nova-ci local [--base origin/dev] [--functional] [--dry-run]
                      (runs tests, writes only a temp dir; needs a nova-tools
                      checkout) the unit tier CI runs for this diff, on this
                      machine: the packages CI's selection picks against the
                      merge base of --base and HEAD, run through the Makefile's
                      test target (its go test flags and its slowtests budgets)
                      under nice -n 15 at -p 2, GOMAXPROCS=2 and -count=1; one
                      PKG line per package with its seconds, one RED line per
                      failing test with its output. --functional adds the
                      functional build tag (GOTEST_TAGS=functional); CI runs
                      those tests in its functional job as a stream merges.
                      --dry-run prints the packages and the make line, and
                      runs nothing.
  nova-ci new-rule [--root <checkout>] [--dry-run] <rule-name>
                      (local write; needs a nova-tools checkout) scaffold a new
                      class rule: class test, fixture and make target;
                      --dry-run lists the files and writes nothing
  nova-ci new-verb [--root <checkout>] [--dry-run] <tool> <verb>
                      (local write; needs a nova-tools checkout) scaffold a new
                      verb of an existing tool: command, test, fixture and make
                      target; --dry-run lists the files and writes nothing
  nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>
                      (delivery: writes only its own run directory on the bench)
                      copy the tree, .git left out unless --with-git, to a fresh
                      run directory on the Linux bench --host (--fallback when it
                      does not answer), run the command in it under nice -n 19
                      with GOCACHE, GOFLAGS=-mod=readonly and NOVA_TEST_NO_HOST=1,
                      stream its output, then remove that directory and nothing
                      else. One CI BENCH line on stderr ends the run.
                      example: nova-ci bench run --host <bench> --dir . -- go vet ./cmd/nova-ci/
  nova-ci github receipt --from-runner --redis <addr> --repo owner/name
                    --sha <40hex> --run-id <n> --workflow <name>
                    --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]
                    [--dry-run]
                      (store write) the ci-ok job's run receipt: one ev:github
                      row of the workflow_run shape, sender runner; dialled as
                      the environment's seat (NOVA_SPRINT_REDIS_USER),
                      with the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names, never on the line.
                      A refused write is tried once, not retried. One CI
                      RECEIPT line. --dry-run checks the fields and prints the
                      line with ev=-, dialling nothing.

exit codes: 0 done, 1 the verb said no (slowtests, local, github receipt), 2 usage or could not run; by verb:
  slowtests: 0 inside budget, or CI-SLOW lines without --enforce (a
    measurement), or an empty stream with --allow-empty; 1 a CI-SLEEPS
    line, a truncated package (started and never ended), a CI-SLOW line
    under --enforce, or an empty stream without --allow-empty (the check
    said no); 2 the invocation could not run (bad flag, unreadable stdin)
  local: 0 green; 1 a red test, a package that did not build, or a
    CI-SLEEPS line; 2 a step that could not run, or usage
  functional: 0 the selection printed (packages=0 included); 2 a flag, or
    a pattern that matches no package
  new-rule: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, or a file already there
  new-verb: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, a tool with no func main, or a file already there
  bench run: the command's own exit status; 2 also usage, or a run
    that never reached the command (no bench answered, the copy failed),
    told apart by its REFUSED line and the missing CI BENCH exit=<n>
  github receipt: 0 written (or checked, with --dry-run); 1 the store
    refused the write or could not confirm it; 2 usage or a refused field
  version: 0 printed; 2 an argument given
```
<!-- clidoc:end nova-ci -->

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

<!-- clidoc:begin nova-config -->
`nova-config help`:

```
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
first run: the example: lines need no database and write only ./try.json; the fleet's store is
  export NOVA_PG_DSN=postgres://user@host:5432/db
then migrate.

usage:
  nova-config help [<verb>]
  nova-config version
  nova-config kinds [--json]
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--actor <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>] [--example]
  nova-config backup --dir <dir> [--pg <dsn>] [--keep <n>] [--every <duration>] [--json]
  nova-config <kind> add <name> --<field> <value> ... --actor <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --actor <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --actor <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config loop run <name> [--run-dir <dir>] [--metrics <dir>] [-- <command> ...]
                    the loop's command under its one lock: a second copy exits 3
  nova-config login --store <dir> --as <seat> --key <file> --secret <NAME>
                    --dsn <dsn> --actor <name> [--sops <path>]
                    records the DSN and where the password is; never the password
  nova-config login --check
                    prints that login and whether the secret resolves
  nova-config logout
                    removes the recorded login
  nova-config fleet set|show|history        one row each, no name:
                                            fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked),
                                            its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; the password is never on the line:
NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password,
NOVA_PG_PASSWORD when it is unset, and never the password itself), or --file
<path>, or --seat <name> (or NOVA_SEAT) which supplies the DSN and password
variable name from the seat profile (nova-sprint seat install writes it; the
password, when its variable is unset, is read in this process from the
nova-secrets seat nova-sprint seat login names), or the login nova-config login records
(the DSN and friend; the password is read in this process from nova-secrets,
never recorded and never put in an environment). --pg, NOVA_PG_DSN and
NOVA_PG_PASSWORD_ENV still win when given. --redis is host:port
(NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address). --actor is
the name a write is recorded under (NOVA_FRIEND, the recorded friend, or the
seat name); its old spelling --as works for one release.
Lose Redis: run nova-config apply.

Fleet apply and inventory require explicit redis_port and pg_dsn; set both
with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --actor <name>.

exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer);
machine self: 2 not a row, 3 unreadable

kinds (nova-config <kind> add -h describes each field):
  machine  a machine of the fleet, named by its tailnet host: the login, the seat, its ceiling, its
           runners, the sprint member's width on it, and whether it is a TLC record machine
           add needs --user --seat --slots; also --runners --width --tla --note
  fleet    the one row of fleet-wide facts: the store and coordinator machines, Redis port, explicit
           password-free Postgres URI, the bus store's address and the loops log directory
           set takes --store --coordinator --redis_port --pg_dsn --bus --loops_dir
  friend   an AI friend: her slots, which tiers she can do, her roles, and her width, the jobs she
           works at once, her delivery mode, the config directory her claude lanes run with, the
           per-card token cap her one-shot lanes hold a card at, her working directory, and the
           optional streams and kinds restrictions on the work she may be dealt
           add needs --slots --tiers; also --roles --width --mode --config_dir --token_cap --streams
           --kinds --dir
  sprint   the one row of sprint-global facts: which friend coordinates and the decide_* bars, each
           a probability in [0,1]; nova-config sprint set -h says what each bar decides
           set takes --coordinator --decide_bounce --decide_review --decide_score_bar
           --decide_attempt_no_result --decide_attempt_nothing_to_do --decide_grade
           --decide_gate_flaky --decide_gate_preexisting --decide_judgment_bar --decide_brief_bar
           --answer_rules_off
  loop     a supervised loop on one machine: its command, the seat and secret names it opens, and
           how it runs (every n seconds or kept alive); a nova-swarm member's width, a reader's too,
           is its machine row's, never the argv's
           add needs --machine --argv; also --seat --keys --every --keepalive --enabled
  route    a route of a model tier: the provider and model a card of that tier runs on, its token
           budget and deadline; the tier's array orders its routes; frontier cards are never dealt
           from routes, they escalate to the coordinator
           add needs --tier --provider --model --deadline; also --harness --tokens --usd --enabled
           --first --price_input --price_cache_read --price_cache_write --price_output
           --reasoning_as_output --long_context --price_input_long --price_output_long
           --price_request --billing --gateway_percent --price_source --price_as_of --note
  tier     a model tier's route array: the deal takes routes[index mod len] for each card of the
           tier, a route named twice taking two turns; one row each for flash and pro and heavy,
           created by migrate
           set takes --routes
```

`nova-config version -h`:

```
usage: nova-config version [flags]
from `nova-config help`:
  nova-config version
effect: inspection: reads nothing but this binary
flags:
  --json  print one JSON object (internal/tool's result shape) instead of the lines
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config kinds -h`:

```
usage: nova-config kinds [flags]
from `nova-config help`:
  nova-config kinds [--json]
effect: inspection: reads nothing but this binary
flags:
  --json  print one JSON object (internal/tool's result shape) instead of the lines
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config migrate -h`:

```
usage: nova-config migrate [flags]
from `nova-config help`:
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config migrate --file try.json
effect: store write: makes or upgrades schema config (or makes the --file), each migration above the greatest recorded once; --print and --dry-run write nothing (--dry-run reads the ledger)
flags:
  --dry-run  read the ledger (config.schema_migrations) and print every migration applied, pending (migrate applies it) or missing (below the greatest recorded, which migrate will not apply), and every table of schema config the role does not own, applying none; exit 0 when migrate would apply (ready=yes), 1 when it would refuse (ready=no)
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --max <int>  SQL lines to print under each MIGRATION line before one MORE line stands for the rest; 0 prints each migration whole
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --print  list the migrations this binary carries and connect to nothing
  --window  the stopped window of the seat play: refuse, applying nothing, while any other nova role's session holds the database (the old server or member still runs); --dry-run reports the sessions and refuses nothing
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config status -h`:

```
usage: nova-config status [flags]
from `nova-config help`:
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
effect: inspection: reads the store and Redis, writes nothing
flags:
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --redis <host:port>  the Redis host:port apply writes (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); without one, status reads the store alone
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config apply -h`:

```
usage: nova-config apply [flags]
from `nova-config help`:
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--actor <name>]
  [--kind <kind>] [--dry-run] [--json]
effect: external delivery: writes Redis, the copy of the rows the fleet reads, through its own Redis Functions; --dry-run prints the lines and writes nothing
flags:
  --actor <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --as <name>  the old spelling of --actor, kept for one release; it sets the same name
  --check  the same as --dry-run
  --dry-run  print the ADD, SET and REMOVE lines (CHECK ...) and write nothing; it still reads the store and Redis
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --kind <kind>  one kind to apply (machine, fleet, friend, sprint, loop, route, tier); every kind, in order, when unset
  --move-seat  write the sprint row's coordinator over a live seat that differs (the owner's word); without it apply holds the live seat, writes every other field and prints one APPLY HELD line
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --redis <host:port>  the Redis host:port to write (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address)
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config inventory -h`:

```
usage: nova-config inventory [flags]
from `nova-config help`:
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
  [--timeout <duration>] [--example]
effect: inspection: reads Redis (the state apply wrote) or the --fixture file, never PostgreSQL, and writes nothing
flags:
  --example  print a minimal fixture to stdout and open no store; save it and read it back with --fixture
  --fixture <file>  a YAML or JSON file of machines, the fleet row, loops and each machine's os and arch, read in place of the store; print a minimal one with --example; opens no store
  --host <name>  print the variables of one machine, by name, as a JSON object; exits 1 when no machine row has that name
  --list  print the whole inventory (hosts, groups and every host's variables under _meta.hostvars, so ansible never calls --host); the default when neither --list nor --host is given; exclusive with --host
  --redis <host:port>  the Redis host:port of the applied state (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address); exclusive with --fixture
  --timeout <duration>  a Go duration, above 0: how long to wait for the store before refusing; ansible runs the verb unattended, so it never waits forever
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config backup -h`:

```
usage: nova-config backup [flags]
from `nova-config help`:
  nova-config backup --dir <dir> [--pg <dsn>] [--keep <n>] [--every <duration>] [--json]
flags:
  --dir <directory>  the directory the dumps are written to, on a volume that outlives the database's host (required)
  --every <duration>  take a dump now and again each time this passes, until interrupted (default: once)
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --keep <int>  how many verified dumps stay; older ones are pruned after a newer one verifies
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
exit codes: 0 done (with --every: interrupted), 1 refused (the dump failed or did not verify), 2 could not run (usage, or no store)
effect: local write: writes files on this machine; reads PostgreSQL through pg_dump and writes nothing to it
```

`nova-config machine width -h`:

```
usage: nova-config machine width [flags]
from `nova-config help`:
  nova-config machine width <name> [--json]
effect: inspection: reads the store (PostgreSQL, or the --file) and writes nothing
flags:
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config machine self -h`:

```
usage: nova-config machine self [flags]
from `nova-config help`:
  nova-config machine self [--check] [--json]
effect: inspection: prints this machine's name and opens no store; --check reads the machine rows
flags:
  --check  read the machine rows and exit 2 when this machine's name is none of them (exit 3 when the rows cannot be read); without it no store is opened
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config loop run -h`:

```
usage: nova-config loop run [flags]
from `nova-config help`:
  nova-config loop run <name> [--run-dir <dir>] [--metrics <dir>] [-- <command> ...]
exit codes: the command's own, 128+N when a signal ended it; 1 the row is not runnable (missing, disabled, with secrets); 2 usage, or the command did not start; 3 another copy holds the lock
flags:
  --dry-run  print what the verb would write and write nothing
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --metrics <dir>  node_exporter's textfile dir: nova_loop_<name>.prom is written there at each start; empty writes none
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --run-dir <dir>  the dir of the loop's lock (<name>.lock) and start count (<name>.starts)
exit codes: the command's own, 128+N when a signal ended it; 1 the row is not runnable (missing, disabled, with secrets); 2 usage, or the command did not start; 3 another copy holds the lock
effect: local write: takes <run-dir>/<name>.lock (a second copy exits 3), counts the start in <run-dir>/<name>.starts, writes the restart metrics to --metrics when given, then runs the loop's command (its row's argv, or the command after --) and exits with the command's exit code, 128+N when a signal ended it; it opens the store only when no command follows --; --dry-run reads the row and the start count, prints the LOOP RUN line it would print, and takes no lock, writes nothing and runs nothing
```

`nova-config login -h`:

```
usage: nova-config login [flags]
from `nova-config help`:
  nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --actor <name> [--sops <path>]
  nova-config login --check
flags:
  --actor <name>  the name a bare write is recorded under when --as and NOVA_FRIEND are unset
  --as <string>  the seat of that store whose file holds the password
  --check  record nothing: print the recorded login and whether its secret resolves (exit 1 when it does not); the password is never shown
  --dry-run  print what the verb would write and write nothing
  --dsn <dsn>  the PostgreSQL dsn with no password, postgres://user@host:port/db
  --friend <name>  the old spelling of --actor, kept for one release; it sets the same name
  --key <string>  the seat's age key file
  --secret <string>  the NAME of the password in the seat's file; never the password
  --sops <string>  the sops binary; empty is the sops on PATH, recorded as its path
  --store <string>  the nova-secrets store's working copy the password is read from
exit codes: 0 done, 1 refused (the verb ran and the secret did not resolve), 2 could not run (usage, or a store that did not answer)
effect: local write: writes files on this machine: the login file (0600) under the per-user config directory; --check and --dry-run write nothing
```

`nova-config logout -h`:

```
usage: nova-config logout [flags]
from `nova-config help`:
  nova-config logout
flags:
  --dry-run  print what the verb would write and write nothing
exit codes: 0 done, 2 could not run
effect: local write: writes files on this machine: removes the login file; --dry-run writes nothing
```

`nova-config fleet -h`:

```
usage: nova-config fleet [flags]
from `nova-config help`:
  nova-config fleet set|show|history        one row each, no name:
  fleet and sprint have no add, remove or list
effect: inspection: this help of the fleet verbs; each verb's own -h has its flags and an example
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```

`nova-config sprint -h`:

```
usage: nova-config sprint [flags]
from `nova-config help`:
  nova-config sprint set|show|history
effect: inspection: this help of the sprint verbs; each verb's own -h has its flags and an example
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
```
<!-- clidoc:end nova-config -->

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
nova-config friend set <name> --dir <absolute path> --as <name>          # her working directory, where nova-sprint delivers her cards and reads her outbox; refused when it is not an existing directory or is a symlink; --dir '' unsets it (then <root>/<name>-working)
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

<!-- clidoc:begin nova-redis -->
`nova-redis help`:

```
nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
The password is read from the variable NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD.
first run: --dry-run needs no store; the throwaway recipe below starts a store on a socket.

usage:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>] [--dry-run]
  nova-redis spill --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis recall --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis fn load --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis fn check --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl render
  nova-redis acl check --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --redis <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
  nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
  nova-redis install bus --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
  nova-redis uninstall store [--units <dir>] [--dry-run]
  nova-redis uninstall bus [--units <dir>] [--dry-run]
  nova-redis version
  nova-redis help [<verb>]

a throwaway store, by hand: (stop it: redis-cli -s "/redis.sock" shutdown nosave)
  d=$(mktemp -d)
  redis-server --port 0 --unixsocket "/redis.sock" --save '' --appendonly no --daemonize yes
  for _ in $(seq 50); do redis-cli -s "/redis.sock" ping >/dev/null 2>&1 && break; sleep 0.1; done
then run spill or recall with --redis "/redis.sock" (or a store you may write to).

Every verb but serve, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
```

`nova-redis serve -h`:

```
usage: nova-redis serve [flags]
from `nova-redis help`:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>] [--dry-run]
flags:
  --as <string>  the seat of the secrets store the password is sealed for (nova-secrets --as)
  --bind <string>  comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only
  --dir <string>  the absolute path of the store directory (AOF, RDB and ACL files), created 0700 when missing
  --dry-run  print what the verb would write and write nothing
  --key <string>  the seat's age key file (nova-secrets --key)
  --port <string>  the TCP port to listen on, 1 to 65535 (6379 is Redis's own)
  --secret <NAME>  the NAME of the password in the seat; with it, serve reads the password in its own process rather than from NOVA_REDIS_PASSWORD
  --secrets <string>  the secrets store's working copy the password is in (nova-secrets --store)
  --sops <string>  the sops binary the seat's file is decrypted with (nova-secrets --sops)
  --users <string>  comma-separated ACL users the store's ACL file must hold (those nova-redis acl apply set); a store missing one is refused
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis spill -h`:

```
usage: nova-redis spill [flags]
from `nova-redis help`:
  nova-redis spill --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --name <string>  the key's name, the part after <owner>: (no whitespace)
  --owner <string>  the key's owner prefix, the part before the colon: no ':' or whitespace (a tool's or a worker's name)
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --ttl <string>  how long the value lives, a Go duration above zero (10m, 1h30m); a key never lives forever
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
  --value <string>  the text stored under <owner>:<name>; may be empty but must be given
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis recall -h`:

```
usage: nova-redis recall [flags]
from `nova-redis help`:
  nova-redis recall --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --json  print the result as one JSON object instead of lines
  --name <string>  the key's name, as spill was given it: the part after <owner>:
  --owner <string>  the key's owner prefix, as spill was given it: the part before the colon
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: inspection: reads, writes nothing
```

`nova-redis fn load -h`:

```
usage: nova-redis fn load [flags]
from `nova-redis help`:
  nova-redis fn load --redis <host:port> [--user <name>] [--password-env <NAME>]
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis fn check -h`:

```
usage: nova-redis fn check [flags]
from `nova-redis help`:
  nova-redis fn check --redis <host:port> [--user <name>] [--password-env <NAME>]
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: inspection: reads, writes nothing
```

`nova-redis acl render -h`:

```
usage: nova-redis acl render [flags]
from `nova-redis help`:
  nova-redis acl render
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: inspection: reads, writes nothing
```

`nova-redis acl check -h`:

```
usage: nova-redis acl check [flags]
from `nova-redis help`:
  nova-redis acl check --redis <host:port> [--user <name>] [--password-env <NAME>]
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: inspection: reads, writes nothing
```

`nova-redis acl apply -h`:

```
usage: nova-redis acl apply [flags]
from `nova-redis help`:
  nova-redis acl apply --redis <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
flags:
  --addr <string>  the old spelling of --redis, kept for one release; it sets the same address and prints a NOTE
  --drop-old  with --rotate, remove the password the variable names instead of adding it: the second half of a rotation
  --dry-run  print what the verb would write and write nothing
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable  names, else NOVA_REDIS_PASSWORD)
  --password-env-for <value>  <user>=<VARIABLE>, repeatable: the variable holding the password a user apply creates gets; a user the store lacks is created only with one
  --redis <string>  the store's address: <host:port>, such as 127.0.0.1:6379, or the absolute path of a Unix socket (no default)
  --rotate <string>  comma-separated ACL users whose password this run rotates: the variable --password-env-for names for the user holds the password to add beside the old one, or with --drop-old the old one to remove (neither value is printed)
  --user <string>  the ACL user to log in as (default ; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis install store -h`:

```
usage: nova-redis install store [flags]
from `nova-redis help`:
  nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
flags:
  --as <string>  the seat of the secrets store the password is sealed for (nova-secrets --as)
  --bind <string>  comma-separated IP addresses the server listens on, loopback or tailnet only (serve --bind)
  --dir <string>  the absolute path of the store directory (serve --dir; default: ~/nova-bench/redis/store)
  --dry-run  print what the verb would write and write nothing
  --key <string>  the seat's age key file (nova-secrets --key)
  --log <string>  the file the server's lines go to, macOS (default: ~/Library/Logs/nova-redis-store.log); on Linux they are in the journal
  --port <string>  the TCP port the server listens on (serve --port)
  --secret <NAME>  the NAME of the password in the seat; with it, serve reads the password in its own process rather than from NOVA_REDIS_PASSWORD
  --secrets <string>  the secrets store's working copy the password is in (nova-secrets --store)
  --sops <string>  the sops binary the seat's file is decrypted with (nova-secrets --sops)
  --units <string>  the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis install bus -h`:

```
usage: nova-redis install bus [flags]
from `nova-redis help`:
  nova-redis install bus --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
flags:
  --as <string>  the seat of the secrets store the password is sealed for (nova-secrets --as)
  --bind <string>  comma-separated IP addresses the server listens on, loopback or tailnet only (serve --bind)
  --dir <string>  the absolute path of the store directory (serve --dir; default: ~/nova-bench/redis/bus)
  --dry-run  print what the verb would write and write nothing
  --key <string>  the seat's age key file (nova-secrets --key)
  --log <string>  the file the server's lines go to, macOS (default: ~/Library/Logs/nova-redis-bus.log); on Linux they are in the journal
  --port <string>  the TCP port the server listens on (serve --port)
  --secret <NAME>  the NAME of the password in the seat; with it, serve reads the password in its own process rather than from NOVA_REDIS_PASSWORD
  --secrets <string>  the secrets store's working copy the password is in (nova-secrets --store)
  --sops <string>  the sops binary the seat's file is decrypted with (nova-secrets --sops)
  --units <string>  the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis uninstall store -h`:

```
usage: nova-redis uninstall store [flags]
from `nova-redis help`:
  nova-redis uninstall store [--units <dir>] [--dry-run]
flags:
  --dry-run  print what the verb would write and write nothing
  --units <string>  the directory the unit was written into (default: as install's)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis uninstall bus -h`:

```
usage: nova-redis uninstall bus [flags]
from `nova-redis help`:
  nova-redis uninstall bus [--units <dir>] [--dry-run]
flags:
  --dry-run  print what the verb would write and write nothing
  --units <string>  the directory the unit was written into (default: as install's)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
```

`nova-redis version -h`:

```
usage: nova-redis version [flags]
from `nova-redis help`:
  nova-redis version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-redis -->

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

<!-- clidoc:begin nova-cairn -->
`nova-cairn help`:

```
nova-cairn: a session's words, kept durably as plain files you can come back to

how it works: a store is a directory you name (--store), plain files only, synced to disk before OK.
open starts a session and records its --publish policy; append keeps an entry's exact words.
Same id, same words: duplicate (duplicate=true, exit 0); same id, other words: conflict (exit 1).
--publish records your policy only: nothing is sent, and every line says published=false.
first run: the four examples are one sitting: the open makes ./cairns, the rest read it back.

usage:
  nova-cairn open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]
  nova-cairn NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append with no --publish carries the session's, and one that differs is a conflict naming both (exit 1).
  nova-cairn append --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>] [--now <rfc3339-utc>] [--dry-run]
  nova-cairn index --store <dir> [--session <id>] [--max <n>]
  nova-cairn receipt --store <dir> --session <id> --entry <id> [--text]
  nova-cairn version
  nova-cairn help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
```

`nova-cairn open -h`:

```
usage: nova-cairn open [flags]
from `nova-cairn help`:
  nova-cairn open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]
  nova-cairn open --store ./cairns --session s1 --publish manual
flags:
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --now <string>  RFC 3339 UTC stamp for tests and replays; default the real clock
  --publish <string>  the publication policy: never, manual, deferred, immediate (required)
  --session <string>  the stable session identifier (required)
  --source <string>  where the record points back to; appends with no --source carry it
  --store <string>  the checkpoint store directory (required)
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
effect: local write: writes files on this machine
```

`nova-cairn append -h`:

```
usage: nova-cairn append [flags]
from `nova-cairn help`:
  nova-cairn append --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>] [--now <rfc3339-utc>] [--dry-run]
  nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"
flags:
  --dry-run  print what the verb would write and write nothing
  --entry <string>  the stable entry identifier (required)
  --file <string>  file holding the exact words; - reads stdin
  --json  print the result as one JSON object instead of lines
  --now <string>  RFC 3339 UTC stamp for tests and replays; default the real clock
  --publish <string>  the publication policy: never, manual, deferred, immediate (default: the one the session was opened with)
  --session <string>  the stable session identifier (required)
  --source <string>  where the words came from; recorded, never opened (default: the session's)
  --store <string>  the checkpoint store directory (required)
  --text <string>  the exact words, stored byte for byte; exactly one of --text or --file
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
effect: local write: writes files on this machine
```

`nova-cairn index -h`:

```
usage: nova-cairn index [flags]
from `nova-cairn help`:
  nova-cairn index --store <dir> [--session <id>] [--max <n>]
  nova-cairn index --store ./cairns
flags:
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --session <string>  one session to index; default every record
  --store <string>  the checkpoint store directory (required)
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
effect: inspection: reads, writes nothing
```

`nova-cairn receipt -h`:

```
usage: nova-cairn receipt [flags]
from `nova-cairn help`:
  nova-cairn receipt --store <dir> --session <id> --entry <id> [--text]
  nova-cairn receipt --store ./cairns --session s1 --entry e1 --text
flags:
  --entry <string>  the stable entry identifier (required)
  --json  print the result as one JSON object instead of lines
  --session <string>  the stable session identifier (required)
  --store <string>  the checkpoint store directory (required)
  --text  include the entry's stored words as a quoted text fact
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
effect: inspection: reads, writes nothing
```

`nova-cairn version -h`:

```
usage: nova-cairn version [flags]
from `nova-cairn help`:
  nova-cairn version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 2 usage or could not run, for every verb; by verb:
  open: 0 the record stands (opened, or already matching); 1 a re-open naming
    another policy or source; 2 usage, or a store that did not answer
  append: 0 the words are written, or the entry already holds them
    (duplicate=true); 1 the entry id holds other words, or --publish names
    another policy than the session holds; 2 usage, or a store that did not answer
  index: 0 listed; 2 usage, or a store that did not answer
  receipt: 0 read; 2 usage, or no such session or entry
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-cairn -->

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

<!-- clidoc:begin nova-decide -->
`nova-decide help`:

```
nova-decide: typed decisions with probabilities, recorded so each one can be calibrated against its outcome

how it works: noul: a yes-or-no question answered with a probability of yes; choice: one option.
a decision is a named schema of choice or noul questions over a state; jev or fixed answers:
schema {"name":"q","questions":{"ok":{"type":"noul","instructions":"It asks."}}}
state R? fixed answers {"ok":{"noul":0.9}} print ASK OK id=f decision=q backend=fixed recorded=new
ASK ANSWER question=ok type=noul value=yes p=yes:0.9; exit 0 means recorded, never approved.

usage:
  nova-decide ask --schema <file> --state <file|-> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide read --card <file> --diff <file> [--rule <file>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide score --card <file> --diff <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide attempt --brief <file> [--result <file>] --reason <line> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide grade --brief <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide gate --output <file> --card <file> [--diff <file>] [--base-red <test,...>] [--bars <flaky,pre-existing>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide brief --card <file|dir> --backend <jev|fixed> [--answers <file>] --record <file> [--width <n>] [--timeout <d>] [--max <n>] [--dry-run]
  nova-decide outcome --record <file> --id <decision-id> --label <word> [--note <text>] [--dry-run]
  nova-decide calibrate --record <file> --decision <name> --question <name[=option]> --positive <label,...> --negative <label,...> [--bars <p,...>]
  nova-decide import --record <file> [--verdicts <glob>] [--judgments <dir> --log <file>] [--reports <glob>] [--dry-run]
  nova-decide score-grades --record <file> --log <file> [--day <date>]
  nova-decide findings --record <file> [--since <time>] [--bar <p>] [--shadow <file> --real <file>] [--read-shadow <file> [--heavy <file>]]
  nova-decide version
  nova-decide help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
```

`nova-decide ask -h`:

```
usage: nova-decide ask [flags]
from `nova-decide help`:
  nova-decide ask --schema <file> --state <file|-> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --schema <string>  the decision's schema, a JSON file (required)
  --state <string>  the text the decision is made over, a file, or - for stdin (required)
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends the state to the backend, and it appends to --record
```

`nova-decide read -h`:

```
usage: nova-decide read [flags]
from `nova-decide help`:
  nova-decide read --card <file> --diff <file> [--rule <file>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op card-1
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --card <string>  the card the worker was given, a file (required)
  --diff <string>  the worker's unified diff, a file (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --rule <string>  a rule text the read holds the diff to as well, a file
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends the card and diff to the backend, and it appends to --record
```

`nova-decide score -h`:

```
usage: nova-decide score [flags]
from `nova-decide help`:
  nova-decide score --card <file> --diff <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --card <string>  the card the worker was given, a file (required)
  --diff <string>  the landed unified diff, a file (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends the card and diff to the backend, and it appends to --record
```

`nova-decide attempt -h`:

```
usage: nova-decide attempt [flags]
from `nova-decide help`:
  nova-decide attempt --brief <file> [--result <file>] --reason <line> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide attempt --brief ./cmd/nova-decide/testdata/card.md --result ./cmd/nova-decide/testdata/result.md --reason "verdict not-done: tests red in internal/decide" --backend fixed --answers ./cmd/nova-decide/testdata/attempt-answers.json --record ./decisions.jsonl --op c1@1
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --brief <string>  the card's brief the worker was given, a file (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  the member's reason line for the take's end, as text (required)
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --result <string>  the child's RESULT.md, a file; absent when the child wrote none
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends the brief, result and reason to the backend, and it appends to --record
```

`nova-decide grade -h`:

```
usage: nova-decide grade [flags]
from `nova-decide help`:
  nova-decide grade --brief <file> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide grade --brief ./cmd/nova-decide/testdata/card.md --backend fixed --answers ./cmd/nova-decide/testdata/grade-answers.json --record ./decisions.jsonl --op c1@grade
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --brief <string>  the card's brief, a file (required)
  --dry-run  print what the verb would write and write nothing
  --examples <string>  few-shot examples from the sprint record, a JSON-lines file of {card, heading, paths, kind, label} (SPEC-NOVA-DECIDE section 11)
  --held-out <string>  cards left out of the example pool, a file of card ids one per line (with --examples)
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --seed <string>  the seed that picks the examples, so a run reproduces (with --examples)
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends the brief to the backend, and it appends to --record
```

`nova-decide gate -h`:

```
usage: nova-decide gate [flags]
from `nova-decide help`:
  nova-decide gate --output <file> --card <file> [--diff <file>] [--base-red <test,...>] [--bars <flaky,pre-existing>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]
  nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --bars <string>  the flaky and the pre-existing bars, <flaky>,<pre-existing>: each a probability or empty (that route taken by no failure), two set ones summing above 1; empty (the default) routes none, as the sprint row's defaults do; 0.8,0.8 is the starting point
  --base-red <string>  the failing tests red at the card's base, comma-separated (<Test> or <pkg>.<Test>); given empty, none is
  --card <string>  the card the worker was given, a file: its PATHS line is read (required)
  --diff <string>  the card's unified diff, a file: its files and line counts are summarised
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --output <string>  the gate's output, a file of go test's output (plain or -v) (required)
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --timeout <duration>  how long the backend may take to answer
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends each failure, the card's PATHS and the diff's summary to the backend, and it appends to --record
```

`nova-decide brief -h`:

```
usage: nova-decide brief [flags]
from `nova-decide help`:
  nova-decide brief --card <file|dir> --backend <jev|fixed> [--answers <file>] --record <file> [--width <n>] [--timeout <d>] [--max <n>] [--dry-run]
  nova-decide brief --card ./cmd/nova-decide/testdata/greet.md --backend fixed --answers ./cmd/nova-decide/testdata/brief-answers.json --record ./decisions.jsonl
flags:
  --answers <string>  the fixed backend's answers, a JSON file (--backend fixed only)
  --backend <string>  the backend that answers: jev or fixed (required)
  --card <string>  a card file, or a directory of *.md card files (as add --brief-dir reads it) (required)
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --record <string>  the record file every decision is appended to (JSON lines; created if absent) (required)
  --timeout <duration>  how long the backend may take to answer
  --width <int>  how many cards are asked at once
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: delivery: sends beyond this machine; with --backend jev it sends each card to the backend, and it appends to --record
```

`nova-decide outcome -h`:

```
usage: nova-decide outcome [flags]
from `nova-decide help`:
  nova-decide outcome --record <file> --id <decision-id> --label <word> [--note <text>] [--dry-run]
  nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"
flags:
  --dry-run  print what the verb would write and write nothing
  --id <string>  the decision's id, as ask or read printed it (required)
  --json  print the result as one JSON object instead of lines
  --label <string>  what turned out true, one word (ok, wrong, pass, fail) (required)
  --note <string>  why, in a sentence: the finding behind the label
  --record <string>  the record file the decision is in (required)
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: local write: writes files on this machine: appends one outcome line to --record
```

`nova-decide calibrate -h`:

```
usage: nova-decide calibrate [flags]
from `nova-decide help`:
  nova-decide calibrate --record <file> --decision <name> --question <name[=option]> --positive <label,...> --negative <label,...> [--bars <p,...>]
  nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok
flags:
  --bars <string>  the thresholds to report, comma-separated probabilities
  --decision <string>  the decision's name, as its schema names it (read) (required)
  --json  print the result as one JSON object instead of lines
  --negative <string>  the outcome labels it should pass, comma-separated (required)
  --positive <string>  the outcome labels the answer should flag, comma-separated (required)
  --question <string>  the answer scored: a noul's name, or <choice>=<option> (required)
  --record <string>  the record file (required)
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: inspection: reads, writes nothing
```

`nova-decide import -h`:

```
usage: nova-decide import [flags]
from `nova-decide help`:
  nova-decide import --record <file> [--verdicts <glob>] [--judgments <dir> --log <file>] [--reports <glob>] [--dry-run]
flags:
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --judgments <string>  a directory of judgment files, <judgment id>.md; needs --log
  --log <string>  a nova-sprint log --json export holding the answers to the judgments
  --record <string>  the record file the labelled decisions are appended to (required)
  --reports <string>  a glob of REPORT.md files; those beginning Verdict: HOLD are imported
  --verdicts <string>  a glob of heavy-read VERDICT.md files
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: local write: writes files on this machine; it appends to --record one labelled decision per item read, and leaves an item recorded before; --dry-run writes nothing
```

`nova-decide findings -h`:

```
usage: nova-decide findings [flags]
from `nova-decide help`:
  nova-decide findings --record <file> [--since <time>] [--bar <p>] [--shadow <file> --real <file>] [--read-shadow <file> [--heavy <file>]]
  nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --since 2026-10-01
flags:
  --bar <string>  the p at or above which a class counts, a probability
  --heavy <string>  a record imported from heavy-read verdicts (import --verdicts), to score the shadow reads against them too
  --json  print the result as one JSON object instead of lines
  --read-shadow <string>  the read-shadow record, to score Jev's shadow reads against the readers' outcome
  --real <string>  the judgment-answer record the shadow answers are joined to
  --record <string>  the record file (required)
  --shadow <string>  the judgment-shadow record, to score Jev's shadow answers per judgment kind
  --since <string>  the window's start, RFC 3339 or a date (2006-01-02, UTC); default: seven days before now
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: inspection: reads, writes nothing
```

`nova-decide version -h`:

```
usage: nova-decide version [flags]
from `nova-decide help`:
  nova-decide version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 an outcome conflicts with the one recorded, 2 could not run (a flag, an input, the backend, the record).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-decide -->

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

<!-- clidoc:begin nova-local -->
`nova-local help`:

```
nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts

how it works: an engine (ollama) serves models from the shared store, <ai-root>/shared/models.
status reads each engine and the box; serve makes <name>-<ctx>k, the context baked in, and loads it;
worker writes the one JSON file nova-swarm reads. --base is loopback or a tailnet address only.
It never fetches weights, never runs a prompt and never judges a model.

usage:
  nova-local status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <d>]
  nova-local serve --engine <name> --model <ref> --num-ctx <n> [--base <url>] [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:...>] [--max-load <f>] [--min-free <size>] [--require-shared-store] [--dry-run]
  nova-local serve --stop --engine <name> --model <tag> [--base <url>]
  nova-local worker --engine <name> --model <tag> --out <file> --name <text> --harness <cmd> --harness-args <a,b,{model},...> --worker-dir <abs dir> --key-file <file> --env-var <NAME> --usage <opencode|none> --deadline <d> [--base <url>] [--board <owner/repo#n>] [--dry-run]
  nova-local version
  nova-local help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).
```

`nova-local status -h`:

```
usage: nova-local status [flags]
from `nova-local help`:
  nova-local status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <d>]
  nova-local status
flags:
  --base <string>  the engine's /v1 URL, loopback or a tailnet address; empty is the engine's default
  --engine <string>  only this engine: one of ollama; empty reports every one
  --json  print the result as one JSON object instead of lines
  --list  list every model as one MODEL line, not only the counts
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --timeout <duration>  how long each engine has to answer
exit codes: 0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).
effect: inspection: reads, writes nothing; it asks each engine over HTTP and reads the box
```

`nova-local serve -h`:

```
usage: nova-local serve [flags]
from `nova-local help`:
  nova-local serve --engine <name> --model <ref> --num-ctx <n> [--base <url>] [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:...>] [--max-load <f>] [--min-free <size>] [--require-shared-store] [--dry-run]
  nova-local serve --stop --engine <name> --model <tag> [--base <url>]
  nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7
flags:
  --base <string>  the engine's /v1 URL, loopback or a tailnet address; empty is the engine's default
  --dry-run  print what the verb would write and write nothing
  --engine <string>  the engine: one of ollama (required)
  --expect-digest <string>  the digest the model must have (sha256:...); differing is exit 1
  --json  print the result as one JSON object instead of lines
  --keep-alive <string>  how long the engine keeps the model loaded after a request
  --max-load <string>  refuse when the one-minute load average is above this number
  --min-free <string>  refuse when free memory is below this size (16G, 512M)
  --model <string>  the model: the reference to serve (gemma4:12b), or with --stop the served tag (required)
  --num-ctx <int>  the context in tokens, a multiple of 1024 (required to serve; ollama's own default silently truncates)
  --require-shared-store  refuse when the engine's store is not under the AI root's shared/models
  --seed <string>  the seed baked into the tag, a whole number; empty bakes none
  --stop  unload the served tag --model names instead of serving
exit codes: 0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).
effect: delivery: sends beyond this machine; it asks the engine to create the derived tag and load it (--stop unloads it); nothing is written on this machine
```

`nova-local worker -h`:

```
usage: nova-local worker [flags]
from `nova-local help`:
  nova-local worker --engine <name> --model <tag> --out <file> --name <text> --harness <cmd> --harness-args <a,b,{model},...> --worker-dir <abs dir> --key-file <file> --env-var <NAME> --usage <opencode|none> --deadline <d> [--base <url>] [--board <owner/repo#n>] [--dry-run]
  nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /cmd/nova-local/testdata/home --key-file /cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
flags:
  --base <string>  the engine's /v1 URL, loopback or a tailnet address; empty is the engine's default
  --board <string>  the board issue the worker reports to, owner/repo#n; empty for none
  --deadline <string>  the default deadline per task, a Go duration (20m) (required)
  --dry-run  print what the verb would write and write nothing
  --engine <string>  the engine: one of ollama (required)
  --env-var <string>  the NAME of the variable the provider reads (OLLAMA_API_KEY), never a value (required)
  --harness <string>  the harness command, found on PATH (opencode) (required)
  --harness-args <string>  the harness's arguments, comma-separated, {model} and {prompt} placed (required)
  --json  print the result as one JSON object instead of lines
  --key-file <string>  a non-empty file nova-swarm reads as the key; stat'ed, never opened (required)
  --model <string>  the served tag a harness calls (serve's serve_as=) (required)
  --name <string>  the name nova-swarm calls this worker by (required)
  --out <string>  the file the description is written to; its directory must exist (required)
  --usage <string>  opencode (OpenCode's own accounting) or none (required)
  --worker-dir <string>  the absolute home directory copied into each slot; it must exist (required)
exit codes: 0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).
effect: local write: writes files on this machine; it writes the --out file and nothing else, after asking the engine that it serves --model
```

`nova-local version -h`:

```
usage: nova-local version [flags]
from `nova-local help`:
  nova-local version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, 1 it ran and said no (no engine answers, a model not pulled, a tag that differs, a threshold the caller gave), 2 could not run (a flag or an input).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-local -->

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

<!-- clidoc:begin nova-table -->
`nova-table help`:

```
nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text

how it works: a table is rows and columns in one Redis store; each cell is an
ordered set of members (a card, a job, any id). A column's projection prints
the set's count, its members, a text or a percentage, and the footer folds each
column. Every write names the epoch it read and prints a receipt; a view stacks
tables into one frame that watch redraws in place.
first run: needs a Redis 7 or later you may write to; an empty one is enough (the first verb loads
the functions nova-table calls). With no store at all, help and -h answer, and every verb that writes
runs under --dry-run: it makes every check the real run makes before sending, then prints what it would send:
example: (the lines need the store the first run describes; this one runs with none)
  nova-table create demo --columns ready,working,done --dry-run
a throwaway store, by hand: (stop it: redis-cli -s "/redis.sock" shutdown nosave)
  d=$(mktemp -d)
  redis-server --port 0 --unixsocket "/redis.sock" --save '' --appendonly no --daemonize yes
  for _ in $(seq 50); do redis-cli -s "/redis.sock" ping >/dev/null 2>&1 && break; sleep 0.1; done
  unset NOVA_SEAT NOVA_SPRINT_SEAT NOVA_SPRINT_REDIS_USER; export NOVA_SPRINT_REDIS="/redis.sock"
then run the lines under example: in order (or give each verb --redis <host:port or socket path>).
A verb that finds no store refuses at exit 2 naming the address it tried, what came back, and
this throwaway command (LIST REFUSED: redis at <addr> ...: unreachable: ...; run: d=$(mktemp -d) ...).

usage:
  nova-table help [<verb> [<subverb>]]
  nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>]
      [--width <col=n,...>]
  nova-table set <table> [--footer <label>] [--rename <name>] [--columns <spec>]
      [--hide <cols>] [--show <cols>] [--hidden | --visible]
  nova-table drop <table> [--definition]
  nova-table list
  nova-table row add <table> <row>... [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]
  nova-table row set <table> <row> <col>=<value>...
  nova-table row hide <table> <row>...
  nova-table row show <table> <row>...
  nova-table row del <table> <row>
  nova-table row move <table> <row> --first | --last | --before <row> | --after <row>
  nova-table row order <table> <row>...
  nova-table row sort <table> [--by name|label|<col>] [--desc] [--keep] | --manual
  nova-table col add <table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]
  nova-table col del <table> <col>
  nova-table col move <table> <col> --first | --last | --before <col> | --after <col>
  nova-table cell add <table> <row> <col> <member>... [--score <n>]
  nova-table cell remove <table> <row> <col> <member>...
  nova-table cell move <table> <row> <from-col> <to-col> <member>...
  nova-table cell members <table> <row> <col>
  nova-table member create <table> <id>
  nova-table member find <table> <id>
  nova-table member read <table> <id>... | <table> --cell <row:col>
  nova-table batch (<manifest-file> | - | '<json>')
  nova-table check <table>
  nova-table clear <table>
  nova-table show <table> [--at-epoch <n>]
  nova-table render <table> | --view <name> [--at-epoch <n>]
      [--width <col=n,...>] [--label-width <n>]
  nova-table watch <table>[,<table>...] | --view <name> [--every <duration>] [--out <file>]
      [--title <text>] [--width <col=n,...>] [--label-width <n>] [--check] [--once]
  nova-table view set <name> --tables <a,b,...> [--title <text>] [--summary <count-column>]
  nova-table view state <name> (<text> | --clear)
  nova-table view show <name>
  nova-table view list
  nova-table view del <name>
  nova-table shell [--redis <addr> | --seat <name>] [--keep-going] [--epoch <n>] [--receipt=false]
  nova-table version

Table write verbs take --epoch <observed epoch> (default 0), --actor, --fence,
--idem (receipt metadata only; does not deduplicate retries: a second cell add of the same member
is refused, naming the place it already sits, and a second row add rewrites the row and keeps its
place) and --receipt. create also takes --epoch-key, --epoch-field (default n), and --member-prefix
(default table::member:).
A stale epoch is refused, naming the live epoch:
  CELL-ADD REFUSED: table "stale-help" row "build" column "ready" member "b1": requested epoch is stale, not the active epoch: requested 0, active 1; run: nova-table show 'stale-help'
read the epoch off show <table> (it prints epoch=<n>) or off the receipt of every write (it prints
the new one, epoch=<n>). drop keeps the saved column definition and the
table's identity unless --definition is given, which removes both and the rows
of every epoch (and repairs a store left with the identity alone); the
definition snapshots of earlier epochs remain available.
View configuration has no table epoch or receipt.
Quote column specs containing parentheses, for example 'done,pct:pct(done)'.

Store verbs take --redis <addr> (host:port or an absolute Unix socket path)
(else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the seat's address) and
dial as the seat --seat <name> or NOVA_SEAT names, else as
NOVA_SPRINT_REDIS_USER with the password in the variable
NOVA_SPRINT_REDIS_PASSWORD_ENV names. Flags may follow the words.

A column is name[:projection[:fold[:label]]]: the projection is what a body
cell prints, count (the set's size, the default), members (the members in
score order), first, last, text (the value written by row set, no set),
pct(<count-column>) (the share of all count columns in the row),
pct(<count-column>/<a>+<b>) (the share of the named count columns a, b of the
row), or sum(<a>+<b>) (the named count columns of the row added). A formula
names count columns of the same table, hidden or not. The row label is a
separate cell. The fold is what the footer prints over the column, sum (the
default for count and sum), max, avg (of count cells), union (of members),
pooled (the default for pct: the numerators summed over the denominators
summed), or none. Known-empty percentages print 0.0%; unread inputs print ?.
Example: 'ok,failed,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%'.
set --hide/--show hides or shows columns without removing their data. A row's cells are owned by the table unless
row add binds a column to a set another tool owns (<col>=<key>): a bound
cell is a view, read freely, and cell add, cell remove, cell move and clear
refuse it, naming the --owner verb. --exclude names one member the row's
counts and members leave out. render <table> prints the table and nothing else,
nothing at all when it is empty. render --view <name> prints one frame with
the view's timestamp, title and summary line; view state sets a text the
summary line shows alone, in place of the counts, until --clear. watch redraws it in place every --every
(1s) with no shell loop, or publishes it to --out by atomic rename.

Order is kept by the table: rows draw in the order they were added and
columns in the order they were declared, until a verb moves them. row sort
orders the rows once; with --keep (by name or label) the sort stands, every
row added later takes its place, and row move and row order are refused
until row sort --manual. row del of a missing row succeeds with existed=0.
col del refuses a column that holds members or text, naming all blocking
members and batch removal commands, or the text to clear first.

shell reads one command per line on a shared connection. It prints write
receipts by default; --receipt=false disables them. Enter help, quit or exit.

exit codes: 0 done, 1 refused, 2 usage
```

`nova-table create -h`:

```
usage: nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>]
      [--width <col=n,...>]

flags:
  --columns <string>  the columns, name[:projection[:fold[:label]]] each, comma-separated; pct defaults to the pooled fold, sum to the sum fold
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --epoch-field <string>  field in the epoch hash
  --epoch-key <string>  hash key naming the epoch domain (empty means epoch 0)
  --footer <string>  the footer row's label (none by default)
  --member-prefix <string>  member record prefix (default table::member:)
  --width <string>  fixed column widths, col=n,...

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table set -h`:

```
usage: nova-table set <table> [--footer <label>] [--rename <name>] [--columns <spec>]
      [--hide <cols>] [--show <cols>] [--hidden | --visible]

flags:
  --columns <string>  the columns, replaced in place (the create grammar); rows kept
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --footer <string>  the footer row's label ('' for none)
  --hidden  the whole table kept and read, not drawn by watch
  --hide <string>  columns to hide (kept, read, used by formulas; not drawn), comma-separated
  --rename <string>  the table's new name
  --show <string>  hidden columns to draw again, comma-separated
  --visible  the whole table drawn again by watch

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table drop -h`:

```
usage: nova-table drop <table> [--definition]

flags:
  --definition  also remove the saved column definition, the identity hash and the rows of every epoch; keep the definition snapshots of earlier epochs
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table list -h`:

```
usage: nova-table list

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table row add -h`:

```
usage: nova-table row add <table> <row>... [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --exclude <string>  one member the row's counts and members leave out
  --label <string>  the row's label, the row header cell (default the row key)
  --owner <string>  the verb that writes the row's bound sets, named by the refusal of a write here

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row set -h`:

```
usage: nova-table row set <table> <row> <col>=<value>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row hide -h`:

```
usage: nova-table row hide <table> <row>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row show -h`:

```
usage: nova-table row show <table> <row>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row del -h`:

```
usage: nova-table row del <table> <row>

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row move -h`:

```
usage: nova-table row move <table> <row> --first | --last | --before <row> | --after <row>

flags:
  --after <string>  put it just after this row
  --before <string>  put it just before this row
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --first  put it first
  --last  put it last

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row order -h`:

```
usage: nova-table row order <table> <row>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table row sort -h`:

```
usage: nova-table row sort <table> [--by name|label|<col>] [--desc] [--keep] | --manual

flags:
  --by <string>  name (the row key), label, a count column or a text column
  --desc  largest or last first
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --keep  a standing sort (by name or label): rows added later take their place
  --manual  end a standing sort; the rows stay where they are and are placed by hand again

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table col add -h`:

```
usage: nova-table col add <table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]

flags:
  --after <string>  put it just after this column
  --before <string>  put it just before this column
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --first  put it first
  --last  put it last

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table col del -h`:

```
usage: nova-table col del <table> <col>

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table col move -h`:

```
usage: nova-table col move <table> <col> --first | --last | --before <col> | --after <col>

flags:
  --after <string>  put it just after this column
  --before <string>  put it just before this column
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --first  put it first
  --last  put it last

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table cell add -h`:

```
usage: nova-table cell add <table> <row> <col> <member>... [--score <n>]

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --score <string>  the member's score, its place in the set's order (default the unix time in ms)

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table cell remove -h`:

```
usage: nova-table cell remove <table> <row> <col> <member>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table cell move -h`:

```
usage: nova-table cell move <table> <row> <from-col> <to-col> <member>...

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table cell members -h`:

```
usage: nova-table cell members <table> <row> <col>

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table member create -h`:

```
usage: nova-table member create <table> <id>

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table member find -h`:

```
usage: nova-table member find <table> <id>

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table member read -h`:

```
usage: nova-table member read <table> <id>... | <table> --cell <row:col>

flags:
  --at-epoch <string>  read a materialised epoch instead of the active one
  --cell <row:col>  read every member of the cell row:col (repeatable); the ids are then not given
  --json  print the reading (or the refusal) as one JSON object instead of the lines

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table batch -h`:

```
usage: nova-table batch (<manifest-file> | - | '<json>')

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --json  print the receipt (or the refusal, or the --dry-run plan) as one JSON object instead of the lines

exit codes: 0 done, 1 refused, 2 usage
effect: store write: applies the manifest in one atomic call and prints a receipt; --dry-run makes every check made before sending and prints the plan instead, dialling nothing; the epoch, the revision and each member's expectation are the store's to check, on the real run
```

`nova-table check -h`:

```
usage: nova-table check <table>

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table clear -h`:

```
usage: nova-table clear <table>

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the table in the store in one call and prints a receipt; --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table show -h`:

```
usage: nova-table show <table> [--at-epoch <n>]

flags:
  --at-epoch <string>  inspect a materialised epoch instead of the active one

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table render -h`:

```
usage: nova-table render <table> | --view <name> [--at-epoch <n>]
      [--width <col=n,...>] [--label-width <n>]

flags:
  --at-epoch <string>  read a saved epoch snapshot (table targets only)
  --label-width <int>  fixed width of the row-label column for this render (0: as wide as the labels)
  --view <string>  render a stored view once, including its title and summary
  --width <string>  fixed column widths for this render, col=n,...

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table watch -h`:

```
usage: nova-table watch <table>[,<table>...] | --view <name> [--every <duration>] [--out <file>]
      [--title <text>] [--width <col=n,...>] [--label-width <n>] [--check] [--once]

flags:
  --check  run table check every tick; show a stall row on invariant violation
  --every <duration>  the tick, a duration (1s)
  --label-width <int>  fixed width of the row-label column for this render (0: as wide as the labels)
  --once  render once and exit, with no clear
  --out <string>  publish to this file by atomic rename instead of drawing in place (the file and its directory must not be symlinks)
  --title <string>  a title line above the tables
  --view <string>  a stored view: its tables and title, read every frame (view set <name> --tables ...)
  --width <string>  fixed column widths for this render, col=n,...

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store every --every and writes nothing to it; --out writes that one local file, by rename
```

`nova-table view set -h`:

```
usage: nova-table view set <name> --tables <a,b,...> [--title <text>] [--summary <count-column>]

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --summary <string>  a count column in the first table to count as done (x/y z% -> ETA)
  --tables <string>  the tables, comma-separated, in order
  --title <string>  the view's title line

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the view in the store in one call (a view has no epoch and no receipt); --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table view state -h`:

```
usage: nova-table view state <name> (<text> | --clear)

flags:
  --clear  clear the state: the summary line shows the counts again
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the view in the store in one call (a view has no epoch and no receipt); --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table view show -h`:

```
usage: nova-table view show <name>

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table view list -h`:

```
usage: nova-table view list

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads the store, writes nothing
```

`nova-table view del -h`:

```
usage: nova-table view del <name>

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written

exit codes: 0 done, 1 refused, 2 usage
effect: store write: changes the view in the store in one call (a view has no epoch and no receipt); --dry-run runs the real run's own call up to its first command, so it refuses exactly where the real run refuses before sending, and prints that command instead of sending it, dialling nothing; what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run
```

`nova-table shell -h`:

```
usage: nova-table shell [--redis <addr> | --seat <name>] [--keep-going] [--epoch <n>] [--receipt=false]

flags:
  --dry-run  check the arguments, print the call the verb would send and stop: nothing is dialled or written
  --keep-going  continue after errors; final exit still reports failure (default true on a terminal)

exit codes: 0 done, 1 refused, 2 usage
effect: store write: runs each line's verb on one connection, so a line that writes changes the store; entered with --dry-run, every write line is planned instead and nothing is written to the store, and a line saying --dry-run=false is refused (a line that reads still reads the store, and a watch --out line still writes its one local file)
```

`nova-table version -h`:

```
usage: nova-table version

exit codes: 0 done, 1 refused, 2 usage
effect: inspection: reads nothing, writes nothing
```
<!-- clidoc:end nova-table -->

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

<!-- clidoc:begin nova-card -->
`nova-card help`:

```
nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
nova-card is pre-alpha: not ready for production use.

how it works: a source is read from a checkout of the target repository (a ratchet ledger of
internal/ci, a findings TSV, a tool's rendered help); the planner cuts one card per file with
its PATHS, TEST and tier computed from the row, plans every ledger in one wave with no
dependency, and holds every brief to the lint nova-sprint add runs before the directory is written.
State: none; the directory, its manifest.tsv and the one CARDS OK line are the whole result.

the flow, three lines:
  nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards
  nova-sprint add --stream debt --brief-dir ./cards --allow-shared-paths
  nova-sprint where

usage:
  nova-card generate --from ledger --ledger <name> --repo-dir <dir> --out <dir> [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--base <branch>] [--repo <owner/name>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card generate --from findings --file <tsv> --out <dir> (--repo-dir <dir> | --repo <owner/name> --base <branch> --sha <40hex>) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card generate --from help --tool <name> [--tool <name>...] --out <dir> [--bin-dir <dir>] (--repo-dir <dir> | --repo --base --sha) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card lint --card <file> [--card <file>...] [--name <n>...] [--dropped <id>...]
  nova-card template
  nova-card version
  nova-card help [<verb>]

generate reads the repository, the branch and the base sha from --repo-dir (its origin URL,
its branch, its HEAD); --repo, --base and --sha each override one, and all three together
need no checkout. A card's PATHS are computed from its START line, never typed: every directory
a START file lives in, as its Go files and its tests (<dir>/*.go, <dir>/*_test.go), and the docs
the card names. With a checkout every PATHS entry is checked to exist at it, so a card never
names a path the add would reject. The ledgers: `nova-card generate -h` lists them.
A ledger card is flash and a findings or help card is pro unless --tier says otherwise; a card
whose PATHS name TLA+ model work (a .tla module, an MC config under tla/) is frontier, as
nova-sprint add tiers it, and --tier flash or pro on such a card is a red line
(check=model-tier). The TLC run records tla/RUNS.tsv and tla/CASES.tsv alone are no model.
A ledger plan is one wave with no dependency chain: the lander resolves a ledger conflict as
the union of removals, so adjacent deletions of one file no longer conflict at land
(docs/SPEC-SPRINT.md section 7). Every card is wave 1 and shares the ledger's path with no
need between them, so the add wants --allow-shared-paths; the CARDS line says so.
lint holds a brief to the lint nova-sprint add runs (the model lines, the child rules under the
default rule set, a tree card's steps), and past the add to the typed header and the template's
unfilled <...> lines, which the add does not read, one LINT DRIFT line each; and to the card
checks: a tier on line 1, a TEST whose package PATHS names, no name --name gives outside
double-quoted words, no card --dropped gives. generate holds every brief the same before it
writes. A sprint initialised with --rules holds a brief to that file at
the add. template prints nova-swarm's card template, the shape every generated brief has.

what it prints:
  CARDS OK dir=<dir> cards=<n> waves=<k> tier=<t> [frontier=<n>] [shared-paths=yes]   then manifest.tsv in <dir> (--dry-run: the manifest on stdout, dry-run=yes)
  CARDS NOTE <what was skipped: a row the ledger did not read, a tool with no help>
  LINT DRIFT card=<id> check=<check> line=<n>: <excerpt>              and nothing is written
  LINT OK file=<file>

exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
2 could not run: a missing flag, a source that cannot be read, a checkout with no HEAD
```

`nova-card generate -h`:

```
usage: nova-card generate [flags]
from `nova-card help`:
  nova-card generate --from ledger --ledger serial-tests --repo-dir ./repo --out ./cards
  nova-card generate --from ledger --ledger <name> --repo-dir <dir> --out <dir> [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--base <branch>] [--repo <owner/name>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card generate --from findings --file <tsv> --out <dir> (--repo-dir <dir> | --repo <owner/name> --base <branch> --sha <40hex>) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card generate --from help --tool <name> [--tool <name>...] --out <dir> [--bin-dir <dir>] (--repo-dir <dir> | --repo --base --sha) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]
  nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards
effect: local write: creates --out and writes one .md per card and manifest.tsv into it; nothing when a brief is red; --dry-run plans, lints and prints the manifest, and writes nothing
flags:
  --base <branch>  the branch the BASE: line carries (default: --repo-dir's branch)
  --bin-dir <dir>  with --from help: the dir holding the tools' binaries (default: PATH)
  --dropped <id>  the id of a card dropped off the table, which no brief may name; repeat or comma separate for more
  --dry-run  plan and lint, print the manifest and the CARDS line, and write nothing
  --file <file>  with --from findings: the TSV file of file:line, finding, remedy, test (a header row is skipped)
  --from <kind>  the source kind: ledger, findings or help
  --ledger <name>  with --from ledger: the ledger's name, one of dead-code, fixed-waits, generality-fixtures, namedpaths, serial-tests, sleeps-skips, slowwaits, transcripts
  --max <int>  write at most this many cards, in source order; 0 is all
  --minutes <minutes>  the Deadline line's minutes (default: 45 flash, 60 pro)
  --name <name>  a person, friend or machine name no brief may carry outside double-quoted words; repeat or comma separate for more
  --out <dir>  the dir the briefs and manifest.tsv are written into; created, and refused when it already holds a brief
  --prefix <word>  the word every card id opens with (default: the ledger's name, finding, or help)
  --repo <owner/name>  the owner/name the REPO: line carries (default: --repo-dir's origin)
  --repo-dir <dir>  a checkout dir of the target repository at the base: the source is read from it, the repository, branch and sha are read off it, and every PATHS entry is checked to exist in it
  --sha <sha>  the base sha, 40 hex (default: --repo-dir's HEAD)
  --tier <string>  flash or pro; default by source: a ledger's own (mechanical ledgers flash, decisions pro), findings and help pro
  --tool <name>  with --from help: a tool name whose help the card is about; repeat for more
exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
```

`nova-card lint -h`:

```
usage: nova-card lint [flags]
from `nova-card help`:
  nova-card lint --card <file> [--card <file>...] [--name <n>...] [--dropped <id>...]
  nova-card lint --card ./cards/finding-internal-bus-send.md
  nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
effect: inspection: reads, writes nothing
flags:
  --card <file>  a brief file to hold to the add's lint; repeat for more
  --dropped <id>  the id of a card dropped off the table, which no brief may name; repeat or comma separate for more
  --name <name>  a person, friend or machine name no brief may carry outside double-quoted words; repeat or comma separate for more
exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
```

`nova-card template -h`:

```
usage: nova-card template [flags]
from `nova-card help`:
  nova-card template
effect: inspection: prints the card template, writes nothing
exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
```

`nova-card version -h`:

```
usage: nova-card version [flags]
from `nova-card help`:
  nova-card version
effect: inspection: prints the build identity
exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written;
```
<!-- clidoc:end nova-card -->

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

<!-- clidoc:begin nova-work -->
`nova-work help`:

```
nova-work: every issue of an organization's repositories in one tree file, verified field for field
nova-work is pre-alpha: not ready for production use.

how it works: import reads every issue through your gh login, read-only, into one tree file.
--dry-run reads GitHub exactly as the import does (every issue, the same calls) and writes nothing.
verify reads GitHub again: one MISSING, EXTRA or DRIFT line per difference; none is the proof.
verify --against compares two tree files and reads no network (a minimal tree: verify -h).
first run: gh logged in (gh auth status); export ORG and REPO, a repository you can read.

usage:
  nova-work import --org <org> (--out <tree.lisp> [--replace] | --dry-run) [--repo <owner/name>]... [--fixture <dir>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]
  nova-work verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]
  nova-work verify --tree <tree.lisp> --against <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-bytes <n>]
  nova-work version
  nova-work help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, or verify found no difference; 1 verify found differences, or an import's encoded tree did not read back equal; 2 could not run (a flag, the budget, gh, GitHub, a file)
```

`nova-work import -h`:

```
usage: nova-work import [flags]
from `nova-work help`:
  nova-work import --org <org> (--out <tree.lisp> [--replace] | --dry-run) [--repo <owner/name>]... [--fixture <dir>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]
  nova-work import --org  --repo / --page-size 15 --dry-run
  nova-work import --org  --repo / --page-size 15 --out ./tree.lisp
flags:
  --dry-run  print what the verb would write and write nothing
  --fixture <string>  a directory of recorded GraphQL pages (call-01.json, call-02.json, each with vars and reply) read instead of gh, so import can be tried with no login
  --gh <path>  the GitHub CLI to run, a path (default: gh on PATH); the path found is echoed as gh=
  --json  print the result as one JSON object instead of lines
  --max-calls <int>  the GitHub call budget of the run (default 1500); the estimate is checked against it before any issue is read; 0 is refused
  --org <string>  the organization whose repositories are read, as GitHub spells it (required)
  --out <file>  the tree file to write, created, or replaced with --replace; its directory must exist; required unless --dry-run
  --page-size <int>  issues per GraphQL page, 1 to 100 (default 50); a page GitHub fails to answer is asked again at half the size
  --replace  replace an existing --out file instead of refusing
  --repo <owner/name>  read only this repository, as owner/name; repeat for more (default: every repository of the organization)
  --timeout <duration>  the whole run's deadline (default 30m)
exit codes: 0 the tree is written (with --dry-run: read and checked, nothing written); 1 the encoded tree did not read back equal to what was fetched, nothing written; 2 could not run (a flag, the budget, gh, GitHub, the --out directory)
effect: local write: writes the tree file --out names; reads GitHub through gh, read-only; --dry-run reads GitHub exactly as the import does and writes nothing
```

`nova-work verify -h`:

```
usage: nova-work verify [flags]
from `nova-work help`:
  nova-work verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]
  nova-work verify --tree <tree.lisp> --against <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-bytes <n>]
  nova-work verify --tree ./tree.lisp --repo / --page-size 15
flags:
  --against <file>  a second tree file to compare with in place of GitHub: no gh and no network
  --gh <path>  the GitHub CLI to run, a path (default: gh on PATH); the path found is echoed as gh=
  --json  print the result as one JSON object instead of lines
  --max <int>  items listed before one MORE line stands for the rest; 0 lists all
  --max-bytes <int>  the largest tree file read, in bytes (default 134217728)
  --max-calls <int>  the GitHub call budget of the run (default 1500); the estimate is checked against it before any issue is read; 0 is refused
  --page-size <int>  issues per GraphQL page, 1 to 100 (default 50); a page GitHub fails to answer is asked again at half the size
  --repo <owner/name>  read only this repository, as owner/name; repeat for more (default: every repository of the organization)
  --timeout <duration>  the whole run's deadline (default 30m)
  --tree <string>  the tree file to compare, as import wrote it (required)
exit codes: 0 no difference; 1 one or more differences; 2 could not run (a flag, an unreadable or refused tree, the budget, gh, GitHub)
effect: inspection: reads the tree, and GitHub through gh, read-only (with --against, a second tree file and no network); writes nothing
```

`nova-work version -h`:

```
usage: nova-work version [flags]
from `nova-work help`:
  nova-work version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done, or verify found no difference; 1 verify found differences, or an import's encoded tree did not read back equal; 2 could not run (a flag, the budget, gh, GitHub, a file)
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-work -->

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


## nova-doctor

<!-- clidoc:begin nova-doctor -->
`nova-doctor help`:

```
nova-doctor: says what is missing for the nova tools to work, and the one line that fixes each

how it works: each check covers one dependency: ok, warn or fail, with evidence and a fix line.
Every check runs; a fail does not stop the others. --local skips the checks only a fleet
needs and says which. Exit 0 is all ok (or warn), 1 a warn under --strict, 2 a fail.
first run: nova-doctor run; nothing is changed, no fix is run for you.

usage:
  nova-doctor [run] [--check <name>]... [--local] [--strict] [--json]
  nova-doctor version
  nova-doctor help [<verb>]

Every verb but run takes --json: run prints one `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line per check; with --json it prints the same results as one object. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 every check ok (a warn too, unless --strict), 1 a warn under --strict, 2 a fail, or usage
```

`nova-doctor version -h`:

```
usage: nova-doctor version [flags]
from `nova-doctor help`:
  nova-doctor version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 every check ok (a warn too, unless --strict), 1 a warn under --strict, 2 a fail, or usage
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-doctor -->

See [nova-doctor](../cmd/nova-doctor/README.md) for the setup and worked examples.

## nova-up

<!-- clidoc:begin nova-up -->
`nova-up help`:

```
nova-up: sets up nova on one machine, from nothing to a first sprint

how it works: steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.
each step plans first, one line: UP <step> <ok|create|change|missing> <detail>; then
apply runs every step not ok. A missing step (a program, the system) stops the run before
anything is written, naming the install command. State lives under one root (~/nova).
first run: needs git, redis, sops, age and the nova tools on PATH; nothing else.

usage:
  nova-up --local [--root <dir>] [--dry-run] [--json]
  nova-up version
  nova-up help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done or nothing to change, 1 a step is missing or failed (the line names it), 2 could not run (a flag).
```

`nova-up version -h`:

```
usage: nova-up version [flags]
from `nova-up help`:
  nova-up version
flags:
  --json  print the result as one JSON object instead of lines
exit codes: 0 done or nothing to change, 1 a step is missing or failed (the line names it), 2 could not run (a flag).
effect: inspection: reads, writes nothing
```
<!-- clidoc:end nova-up -->

See [nova-up](../cmd/nova-up/README.md) for the setup and worked examples.
