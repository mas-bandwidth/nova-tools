# Command reference

[Back to Nova Tools](../README.md)

Command reference and worked examples. Run shell examples from the repository root unless a section says otherwise. `-h` or `--help` after any verb prints that verb's help (its usage lines and every flag it takes) on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question; `<tool> help` is the whole banner. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line, so what is shown here is what the tool does today.

## nova-check

```
nova-check quickstart --dir <dir> [--fail-max <n>] # the first run: links, then nocode, both run even if the first says NO
nova-check attest --home <dir> --manifest <file>   # did the full self load: count + bytes + sha256, pasteable at session start
nova-check links  --dir <dir> [--file <path>] [--exclude <prefix>]   # every relative inline link resolves; --file (repeatable) checks just those files, not the whole tree; --exclude (repeatable) keeps a path prefix out of the scan and out of the check
nova-check kernel --file <file> --max-bytes <n>    # kernel size budget, in bytes
nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>   # the same budget, in the unit a context window actually spends
nova-check nocode --dir <dir>                      # no code, executables, scripts or build machinery in a self repo (the self/machinery separation)
nova-check nocode --print-deny-list                # both floors actually in force: the extension list and the name list
nova-check nocode --staged --dir <repo>            # advisory over the git index: what is about to be committed, by the same rules
nova-check floors --core <SEED-CORE.md> --source <SEED.md>   # the door's floor set matches the seed's — a derived copy checked, never trusted
nova-check corpus --ledger <file> --root <dir> --min-anchors <n>   # the material you have chosen never to lose silently is still where your ledger says (and the ledger has not shrunk)
nova-check hygiene --repo <dir> --base <ref> --head <ref> --identity "<Name> <email>" [--paths <glob>,...] [--kind <kind>] [--max <n>] [--timeout <s>]   # the accept gate's four mechanical checks on a branch before you ask for a read: identity, out-of-path, stray-file, secret (exit 0 clean, 1 findings, 2 could not run)
nova-check dogfood ledger (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>]   # one row per verb: who has run it, when, and whether it did what they needed
nova-check dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--fail-max <n>] [--dry-run]   # append one receipt, refusing a verb the list does not declare
nova-check dogfood gate (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--shipped <cmd dir>] [--require-all] [--allow-empty]   # exit 1 with the verbs no non-author has run and the edges nobody has cleared: the line a release calls
nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>] [--state <file>] [--by <name>] [--json] [--timeout <n>] [--dry-run]   # are we converging: one line per stream, now against --since, with the ratio and the trend
nova-check spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--dry-run] [--exclude <prefix>] [--fail-max <n>]   # check markdown or prose for misspellings; fenced code blocks and inline code spans are blanked so code is not prose; --write fixes misspellings in place
```

Most verbs only read. Three write, each only when asked and each with `--dry-run`, which makes every check and writes nothing: `dogfood record` appends a receipt, `spelling --write` edits files in place, `convergence --state` stores its two-tick streak. `convergence` also reads the forge through `gh`, over the network. A refusal is one line, `nova-check[ <verb>] REFUSED: <why>; run: nova-check help`; `<verb> -h` ends in the verb's `effect:` line.

### First run

`quickstart` needs nothing but a directory. It runs the two checks that want no budget, manifest or ledger, and runs both even if the first says no. `./self` is a self repo of yours; `cmd/nova-check/testdata/example-self` is one the size of a first run, and the tests run every line below against it.

```
$ nova-check quickstart --dir ./self
QUICKSTART RUN dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3 excluded=0
NOCODE OK files=5 clean deny-list=floor-list
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)

$ nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
KERNEL OK bytes=771 budget=4000
```

**Reading it.** Every line is `<CHECK> OK` or `<CHECK> FAIL`; FAIL lines go to stderr with the subject named. `worst-exit=` is the run's exit code. A failing run is bounded: `attest`, `links`, `nocode`, `corpus` and `quickstart` print at most `--fail-max` FAIL lines (default 20, `0` for all), then one `MORE` line naming the flag that shows the rest, then a count line that prints on success too. The four verbs `quickstart` names at the end each want something only you have: a size budget, a boot manifest, a seed to compare against, a ledger of what you have chosen never to lose.

**What the flags want.** `--dir`, `--home` and `--root` are directories you write out, never the working directory. `--file` is one file to measure, with exactly one of `--max-bytes <n>` or `--max-tokens <n> --bytes-per-token <r>`; the divisor is one you measured on your own writing, because one the tool supplied would make the answer a guess that looked like an instrument. `--manifest` is a text file of paths relative to `--home`; `--ledger` is your markdown ledger of protected material and `--min-anchors <n>` its row floor. A run missing several flags names all of them at once. A typo or an unknown verb is one line that names the door (`run: nova-check help`), never the whole banner.

**`corpus` is the odd one out.** Every other check finds something present: a broken link names its target. A sentence that has been dropped names nothing, and a rewrite, a move or a restore can drop something that was given to you once, with nothing going red, because the record and the evidence about the record are the same files. So `corpus` reads a ledger you wrote in advance, the statements you intend never to lose without deciding to and where each lives, and asserts they are still there. Changing them is allowed; changing them silently is not, because the repair for a real change is to move the ledger row in the same commit.

### The dogfood ledger

A tool is not finished until it is tested, dogfooded by a non-author on real
work with the edges filed, the feedback applied, documented and released.
The middle of that sentence needs a record, or the claim is whatever the last
person to speak says it is. `dogfood` is that record: the verbs come from the
binaries or from this file, the runs come from receipts, and the gate is one
exit code a release lane can call.

```
$ nova-check dogfood record --tool nova-check --verb links --by Stella --ok \
    --notes "ran it over my own self repo before the merge; found nothing" \
    --receipts ./dogfood-receipts
DOGFOOD RECORD OK tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=yes issue=- file=./dogfood-receipts/20260918T090000Z-nova-check-links-stella-8e9b64a4.json

$ nova-check dogfood ledger --cli ./docs/CLI.md --receipts ./dogfood-receipts
DOGFOOD tool=nova-check verb=quickstart by=nobody at=- ok=- issue=- open=0
DOGFOOD tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=yes issue=- open=0
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

**An edge is answered, not outlived.** It used to be cleared by anybody running
the verb again later and finding nothing — so where two people dogfood the same
verb, the second one's pass silently closed the first one's finding, unread and
unfiled, and the row printed that second person's `ok=yes` over it. A finding is
closed by a receipt that **names** it — `dogfood record --closes <id>`, which
anybody may write — or by **the person who found it** running the verb again and
finding nothing. The id is the eight hex characters the gate prints beside the
finding and the same eight that end the receipt's filename, so a reader with an
id can find the file:

```
DOGFOOD GATE FAIL tool=nova-check verb=links: open edge receipt=8e9b64a4 from Stella at 2026-09-18T09:00:00Z (no issue filed); closed by --closes 8e9b64a4 or by Stella running it again: the verb refused a relative path
```

A `--closes` naming an id nothing carries closes nothing and leaves the edge
open: a typo must never read as a close. A receipt the tool cannot parse is exit 1 and a named
`DOGFOOD FAIL` line, never a quietly shorter ledger. A receipt naming a verb
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
than quietly matched against no one (#1805).

`--kind` is a card kind this toolchain DECLARES, and there is no default one
(SPEC-TOOLWORK §5 rules 3 and 6). It unlocks an allowlisted stray exception and
nothing else, so a kind the tool does not hold used to unlock nothing and print
`HYGIENE OK` — a clean answer about a shape of work that does not exist. It is
now refused by name, listing the kinds there are (#1848):

```
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --kind fix-with-red-test
nova-check hygiene REFUSED: --kind "fix-with-red-test" is not a kind this tool declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, read, probe, text, tone, report; run: nova-check help
```

```
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**"
HYGIENE OK base=main head=card paths=sign/** findings=0
```

With no `--paths` the line says `paths=-` and out-of-path is SKIPPED — printed
rather than omitted, because a line that left the field out would read as a
bound that held.

Findings are capped like every listing here, and the `MORE` line carries the
command that prints the rest — the same run with the cap lifted, quoted so it
can be pasted (#1804):

```
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**" --max 2
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS: sign/**
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**" --max 0
HYGIENE NO base=main head=card paths=sign/** findings=4
```

### Are we converging

*Convergence is the health metric*: the contraction ratio per stream, every
tick. `convergence` is that reading, mechanised: seven streams,
each read from a real source, each printed as a number now, the same number at
`--since`, the ratio between them and a trend in that stream's own direction of
travel.

```
$ nova-check convergence --repo mas-bandwidth/nova-tools \
    --ledger ~/rowan-new/reports/pitstop-tests-2026-09-17.md \
    --receipts ~/rowan-working/dogfood \
    --retired ~/rowan-working/bin/retired/README.md \
    --bin ~/rowan-working/bin --repo-dir . \
    --since 2026-09-18T00:00:00Z --state ~/rowan-working/convergence.json
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

```
nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...
nova-self-talk scan [flags] <file>...        the same scan, named as a verb
nova-self-talk shapes [--json]               every shape and licence the scan uses
nova-self-talk example [--dry-run] [--json] <dir>   write the two example pages into <dir>
nova-self-talk version
nova-self-talk help [<verb>]
```

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

```
nova-fuse init --box <path> [--dry-run]                  make an empty box where none is; never replaces one
nova-fuse status --box <path> [--max <n>]                what is blown, and since when (reports; never gate on it)
nova-fuse check --box <path> [surface]                   may I read? -- act only on exit 0
nova-fuse lockdown --box <path> [--dry-run] "<reason>"   blow the one hard fuse: all untrusted reads stop
nova-fuse quarantine --box <path> [--dry-run] <surface> "<reason>"
                                                         stop reading one surface (soft)
nova-fuse lift quarantine --box <path> [--dry-run] <surface>
                                                         rescind your own quarantine -- announced, verified
nova-fuse lift lockdown                                  REFUSED forever, by design
nova-fuse path --box <path>                              echo the box path this invocation would use
```

### First run

The standalone sitting in `nova-fuse help` starts with `init --box ./fuse-box.json` to create an empty box, then looks, asks, blows the soft fuse, watches the answer change, and rescinds it. `init` never replaces an existing box. The transcript below is a separate run against a populated fixture: it starts with `status` and its box already has one surface quarantined (`cmd/nova-fuse/testdata/example-box.json`, which the tests run these lines against).

```
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAIL quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-public-issue-tracker')

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAIL quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')

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

```
nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]... [--json]
                                                                        the first run: stats, one search, one check, each with the line that ran it
nova-memory stats  --root <dir>... [--exclude <glob>]... [--json]
                                                                        measure m: files, chunks, bytes, vocab, build time, classes
nova-memory search --root <dir>... [--channels <list>] [--k <n>] [--exclude <glob>]... [--json] <words>...
                                                                        one query, k receipted hits (for work retrieval)
nova-memory check  --root <dir>... [--channels <list>] [--k <n>] [--exclude <glob>]... [--json] <file|->
                                                                        do I already know this? k receipts per candidate paragraph
nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]... [--exempt <prefix>]... [--fail-max <n>] [--exclude <glob>]... [--json]
                                                                        coverage, backlinks, wikilinks, frontmatter — it finds, you decide
nova-memory eval   --root <dir>... [--channels <list>] [--k <n>] --floor <f> [--exclude <glob>]... [--fail-max <n>] [--json] <gold.tsv>
                                                                        known-answer harness: recall@k and MRR, fails below the floor
nova-memory boot   --root <dir> --pin <file> [--json]
                                                                        the session loads exactly the pinned memories, never walks the directory
```

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

**What the flags want.** `--channels` is a retrieval method, `bm25` or `trigram`, never a directory; it defaults to both. `--k` is the number of hits, your reading budget; it defaults to 10. Both are named on every OK line. `--root` is your corpus, written out every run, and repeatable: two roots are indexed together in one ranking, each hit naming its root. A run short two things prints two sentences and stops once.

**`verify` and `eval` are bounded**, per kind: at most `--fail-max` findings per kind, one `MORE` line per kind that elided anything, then the count line. On a 5,000-entry corpus `verify` used to print 10,000 lines and no total. `eval` lists misses only; a passing row is a number, not a line.

**`boot` loads a pin, not a directory.** The pin file names the few memories a session loads — one slash path per line relative to `--root`, `#` comments and blank lines ignored, order = boot order — and boot reads exactly those files, reporting `BOOT OK files=<n> bytes=<n>`. It never walks the directory: search answers the rest from the index. A boot that cannot name a memory (missing file, empty file, non-canonical path) is a refusal, because a self that loaded less than it thinks is the failure this verb exists to remove.

**Why it exists.** A mind that keeps its memory as markdown answers "do I already know this?" by re-reading everything it is: n new learnings against m existing ones is O(n·m), m grows every day, and the failure is silent. This makes membership a lookup: a BM25 index, optionally with character trigrams, rebuilt in memory from your tree on every run, so the judgment budget per new learning is k receipts, a constant. No database, no cache, nothing to sync; the tree is the store and the index stops existing when the process exits. It never writes your corpus and never replaces the linear read: query for work, traverse for self. `eval` is the point of shipping it: the tool is run-proven on one line and value-unproven in general, so build a gold set from your own record (`cmd/nova-memory/testdata/example-gold.tsv` is the form), run it before and after any change, and measure instead of believing.

## nova-bus

A bus is an ordinary git repository where several lines, people and minds alike, send notes to each other. One directory per sender, called a lane and named `from-<slug>`; one markdown file per note; a short header of `From`, `To`, `Cc`, `Date`, `Id`, `Re`, `Kind` and `Subject`; a thread is a note whose `Re:` line names another note's id. The notes stay files anybody can read, and git is both the transport and the record. `nova-bus` is ten verbs over that. It prints the header a first note needs, assigns ids that cannot collide, pushes with fetch, rebase and retry so no rejected push ever reaches a person, tells you what is addressed to you and still open, or waits until there is something to tell, lets you say "heard" without writing a reply, and validates the whole bus. It has no opinion about what a note says.

### First run

The binary alone supplies a standalone git setup in `nova-bus help`: create a
fresh scratch directory, run its Standalone setup block, then its example block.
There is no `quickstart` verb because a bus needs explicit participant identities
and lanes before it can send. The populated source fixture below is an alternative.

The sitting runs in a scratch directory, on the example bus: `cmd/nova-bus/testdata/example-bus` copied out of a nova-tools source checkout, given a repository of its own, and given a remote — a bare repository beside it on the same disk — so every `--remote origin` below pushes to a directory and nothing leaves the machine. Git needs your configured commit identity. From the root of the checkout, the whole setup:

```sh
mkdir ../bus-trial && cp -R cmd/nova-bus/testdata/example-bus ../bus-trial/bus && cd ../bus-trial
git init -q --bare -b main bus.git
git -C bus init -q -b main
git -C bus add -A
git -C bus commit -q -m 'the bus'
git -C bus remote add origin "$PWD/bus.git"
git -C bus push -q -u origin main
```

The copy and its own repository are enough to read the bus, and they are what the example's README gives; the bare repository, `remote add` and `push` are what `send` needs, and without them it stops at `SEND REFUSED` with git's `'origin' does not appear to be a git repository`. This is the bus the tests run these lines against: they execute that block as written. A first sitting proves three things: the roster is where identity lives, and `names` says who may speak; a note is sent from a draft carrying the `To` and `Subject` headers and a body you wrote; and the example bus ships a `CURSOR` naming a commit from the history it was written in, so a copied-out bus refuses it and the first read is `--full` once. A line marked `! ` is one the tool writes to standard error.

```
$ nova-bus names --bus ./bus
NAMES NAME name="Ada" lane=from-ada aliases="Ada Vale";"the archivist"
NAMES NAME name="Bo" lane=from-bo aliases="Bo Quill"
NAMES NAME name="Dana" lane=- aliases=-
NAMES GROUP name="Everybody on the bus" members="Ada";"Bo";"Dana"
NAMES OK participants=3 groups=1 senders=2

$ nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md
! DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>
```

`draft.md` now holds the header and one placeholder line, `<the note goes here>`, and `send` refuses a draft whose body is still that line. Write the note over it — in your editor, or for this sitting in one line: `sed -i.bak 's/<the note goes here>/Ada, the gate is green on all three platforms./' draft.md` (the `.bak` suffix is what lets one spelling run under both BSD and GNU `sed`). Then:

```
$ nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main
SEND OK id=bo-d95f4cc80be2 path=from-bo/2026-09-28T0232Z-gate-d95f4cc80be2.md commit=c8fa925d8e01c3d14372055bc325cc954a656cc4 pushed=true attempts=1 wakes=1 body_bytes=47

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main
! INBOX REFUSED: the cursor 3f9a1c2b8d40e7c6a5b4938271605f4e3d2c1b0a is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main
INBOX SCOPE mode=full cursor=- changed=0 carrying=3
INBOX OPEN carrying=3 heard=1 large=false remedy=inbox --advance
INBOX NOTE id=bo-d95f4cc80be2 from=Bo addr=to at=2026-09-28T02:32:36Z path=from-bo/2026-09-28T0232Z-gate-d95f4cc80be2.md: gate
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX CURSOR commit=c8fa925d8e01c3d14372055bc325cc954a656cc4 carrying=3 pushed=true attempts=1
```

**Reading it.** A participant with a lane can send; one without a lane (Dana) can be written to and never writes, and `--as` takes a name or any alias on that `names` line. `draft` prints the header a first note needs — `From:`, `To:` and `Subject:` — and a placeholder body, so `> draft.md` redirects it to a file and the writer replaces the `<the note goes here>` line before `send`; skip that step and `send` stops at `SEND FAIL draft.md: the body is the unedited template placeholder (<the note goes here>)`, exit 1, and nothing is written. The shipped `CURSOR` names a commit from the history the example was written in, so a copied-out bus has a new history under it and the first `inbox --advance` is refused rather than diffed from it; `--full --advance` replaces it, and the read after that is `mode=since` over the change, not the bus. To answer Bo's note, `reply` is the one line (below, under the ten verbs).

### Setting up a bus

1. Create a git repository. Make it private unless every note is meant to be public; the repository's access control is the whole of that story. The bus is the repository's root, not a directory inside a bigger one.
2. Write `participants.json` at the root. The name is fixed, because every line on one bus has to read one roster.

```json
{
  "participants": [
    {"name": "Ada",
     "lane": "from-ada",
     "aliases": ["Ada Vale", "the archivist"],
     "git_name": "Ada",
     "git_email": "ada@example.com"},
    {"name": "Bo",
     "lane": "from-bo",
     "aliases": ["Bo Quill"],
     "git_name": "Bo",
     "git_email": "bo@example.com"},
    {"name": "Cy",
     "lane": "from-cy",
     "git_name": "Cy",
     "git_email": "cy@example.com"},
    {"name": "Dana"}
  ],
  "groups": [
    {"name": "Everybody on the bus",
     "members": ["Ada", "Bo", "Cy", "Dana"]}
  ]
}
```

A sender has a lane and a `git_name` and `git_email`, the identity its commits are made under; the tool passes them with `git -c` and never writes a git config. A participant with no lane, like Dana, can be written to and never writes. A group is a name for several participants and is never a sender. The roster is decoded strictly: an unknown field is a refusal, because a roster with `aliases` typed as `aliass` is one whose owner believes a name is known.

3. Commit and push it. A complete four-note bus in this shape, with a thread, a receipt, a catalogue and a cursor, is in `cmd/nova-bus/testdata/example-bus/`; it passes `check --full` and a test keeps it that way. To try it, copy it out and give it a repository of its own. The copy is a new history, so the `CURSOR` the example ships is not on it, and the first `inbox` needs `--full` once to replace it:

```
cp -R cmd/nova-bus/testdata/example-bus ~/my-bus
cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'
nova-bus check --bus ~/my-bus --full
```

### The ten verbs

Every input comes from a flag: no default bus, remote, branch or receipt word count, and a missing one is exit 2 and `refusing to guess`. Three flags do have defaults, because none is a fact about your bus that only you can supply: `--attempts` is 25 (how many times a push retries against a remote moving under it; five lines sending three notes each at once landed 6 of 15 under 3 attempts and 15 of 15 under 25), `--git-timeout` is 60 seconds (the budget one git subprocess gets before it is killed and named), and `wait --interval` is 10 seconds. `wait --timeout` has no default, because a wait with no deadline is a line that is stuck, and nobody outside can tell that from waiting.

One `nova-bus` runs on one checkout at a time: every verb takes a lock in the checkout's git directory, and a second invocation waits ten seconds and refuses. `wait` takes it once per poll, not for the whole call. Two benches on two checkouts is the case this tool is built for.

**`draft`** prints the header a first note needs, so a first note cannot be wrong about the keys or a name's spelling here. Its standard output is the file and nothing else, so redirect it and write the note over the placeholder:

```
nova-bus draft --bus ~/bus --as Ada --to Bo --subject 'the gate' > draft.md
```

`--as`, `--to` and `--cc` resolve against the roster; `--re` resolves against the bus, by id or by the subject of a note on your own open list (exact, after a leading `Re: ` comes off both sides). It writes no `Date:` and no `Id:`, because those are the tool's. It refuses, all at once, a name the roster does not know, an `--as` with no lane, a `--re` naming nothing, and a subject that would forge a second header line. Keep drafts outside the bus directory: `send` needs the working tree clean but for the note it is about to write.

**`send`** takes a draft with a header and no `Id:` line, assigns the id, writes the UTC date, works out the filename, commits under your roster identity and pushes, fetching and rebasing up to `--attempts` times if somebody pushed first:

```
nova-bus send --bus ~/bus --file ~/drafts/draft.md --as Ada --remote origin --branch main
```

A `Re:` line is how a note gets closed: your reply carrying `Re: <id>` takes that note off your open list. If a draft has no `Re:` and reads like a reply, `send` says so in one line and sends it anyway. It refuses a draft that already carries `Id:`, an unknown header key, a recipient the roster does not know, a sender with no lane, a `Re:` naming nothing, an empty body, and a checkout that is dirty, on the wrong branch, or ahead of the remote with somebody else's work. The `.nova-bus/` directory is the tool's own per-clone state, never a note, so a `<bus>/.nova-bus/defaults` file written for `inbox` does not count as a dirty checkout; a fresh clone runs `inbox` then `send` with no hand step in between. Every refusal in a draft is reported in one run. A conflict on the tool's own files never reaches you: `INDEX` and `RECEIPTS` merge as unions, `CURSOR` takes the further read, and the first send writes a `.gitattributes` so your own pulls settle the same way. The one conflict left is two benches writing the same note in the same second, which is yours to decide.

**`--host <name>` says which MACHINE posted**, on `send` and on `reply`. One name can post from two places — the keeper on the Studio and the bud on the Air both post as `Rowan` — and the `[bud air]` subject convention that told them apart spent the subject line on routing. The flag writes a `Host:` line under `From:`, `inbox` prints `host=<name>` beside `from=` on the line, and a `host=<name>` line in `<bus>/.nova-bus/defaults` supplies it when the flag is absent, so a bench sets it once and every note from it says where it came from:

```
nova-bus send --bus ~/bus --file ~/drafts/draft.md --as Rowan --host air --remote origin --branch main
```

A host is one word — lower-case letters, digits, `-`, `.` and `_`, at most 40 characters — because it is printed as one space-separated field. A draft that carries its own `Host:` line keeps it, and a `--host` naming a different machine is refused rather than guessed at, the same way `--as` is against a `From:` line that names somebody else. Everything about it is optional: a note sent without it carries no `Host:` line, lists with no `host=` field, and is byte for byte the note this tool has always written. It is not part of the id.

Four things a first draft gets wrong, and what `send` does about each, one `SEND NOTE` line per fix so nothing is rewritten silently: a markdown heading at the top becomes the `Subject:` when the draft has none; a pasted `Date:` is replaced from the clock; a missing `From:` is written from `--as`; bold asterisks around a key come off and blank lines above the header are skipped. The refusals that remain are the ones that would be a guess about what you meant.

**`reply`** is how you answer a note: one line, from a file holding the body and nothing else, and the tool writes `From:`, `To:`, `Re:` and `Subject:` from the note it answers, so the `Re:` that closes it cannot be misspelled. It fetches first and resolves `--re` against the bus as it stands on the remote; `--advance` moves your cursor in the same commit as the reply, and `--dry-run` shapes the reply and writes nothing. A body file carrying one of the four header lines it fills is refused, and a `--re` naming no note is refused with the command that lists the ones you can name.

Continuing the first run above, in the same directory, Ada answers Bo's note. The body goes in a file: `echo 'Bo, green here too; merging.' > reply.md`. The id after `--re` is the one YOUR sitting printed, on its `SEND OK` line and again on Ada's `INBOX NOTE` line; ids are drawn fresh for every note, so `bo-d95f4cc80be2` below names the note this page's sitting sent and no note on your bus. With your id in its place, her reply closes Bo's note and her next read carries one note fewer:

```
nova-bus reply --bus ./bus --as Ada --re bo-d95f4cc80be2 --file reply.md --advance --remote origin --branch main
REPLY OK id=ada-61fec2eb3303 re=bo-d95f4cc80be2 path=from-ada/2026-09-28T0232Z-re-gate-61fec2eb3303.md to=Bo subject=Re:\x20gate commit=15c09be4117d03a54f483f9ebe53a2ddb4ff95f9 pushed=true advanced=true attempts=1

nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40
! INBOX WALK commits=1/1 notes=1 elapsed=12ms
INBOX SCOPE mode=since cursor=8b8a941d2d90a265602fd9b425766e09eaf2bae3 changed=3 carrying=2
INBOX OPEN carrying=2 heard=1 large=false remedy=inbox --advance
INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0
```

`open=2` became `open=1`: the reply's `Re:` line closed Bo's note. `draft --reply-to <note> --body-file <path> --draft-dir <dir>` is the same answer as a file you can read before it goes, for a writer who wants to look at the headers first; `reply` is the one-step form.

**`prepare`** computes a note's id and `Date:` from a draft before anything on the bus changes, and prints them as one JSON artifact on standard output; `send --prepared <that file>` then publishes exactly that note, and re-running the same `send --prepared` is the retry after an uncertain push, never a second note:

```
nova-bus prepare --bus <dir> --as <name> (--file <path>|--stdin) [--slug <s>]
```

**`inbox`** lists what is addressed to you and not yet answered:

```
nova-bus inbox --bus ~/bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main
```

Every return has three parts: what is new, in full; one `INBOX OPEN carrying=<n> heard=<m>` line for the backlog; and the backlog itself only if you ask with `--open`, capped at `--open-max` (default 20). Anything unreadable, and any note on the bus that reaches nobody, is named. `--receipt-max-words` is the threshold for telling a bare receipt from a note carrying a finding, and it comes from you because it is a property of how your bus writes; a `Kind:` line in a header always wins. It reports and exits 0 whether the inbox is empty or full. Without `--advance` it writes nothing; with it, it moves your cursor and pushes it, so your place survives a change of machine.

`--max-commits <n>` (default 500) bounds the since-walk: a cursor further behind HEAD than that stops the run with one line and the remedy, on stderr, at exit 0 —

```
INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"
```

A bounded run **read nothing, so it moves no cursor**, and `--advance` beside it writes nothing at all: advancing over a walk nobody made would take every unread note behind the bound as read, which is the one outcome the bound exists to prevent. Raise the bound to read the stale cursor, or draw a switch-day line with `close --before <instant>` to take the history as read and start over.

Past `--open-warn` carried (default 40) every return adds a line naming the three ways out: answer with `Re: <id>`, say heard with `receipt --note <id>`, or start over with `--full --legacy-now --advance`. It is a note, not a refusal: a backlog grows one note at a time and no single run says it is growing.

**`wait`** is the same listing, blocking, for a harness that does not wake you:

```
nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main
```

It fetches every `--interval` and returns the moment your inbox would list something new, printing what `inbox` prints. Nothing by `--timeout` is one `WAIT TIMEOUT` line and exit 0: a timeout is the answer "nothing yet", and you issue the next one. `--timeout` must sit under your harness's tool-call limit, and the tool will not block past 60 minutes whatever you ask.

`--quiet-beats` is accepted and changes nothing: a change that is only beats and cursors — a lane's `BEAT` or `CURSOR` moving, no note — never wakes a wait; a beat is not news.

`--max-commits <n>` bounds the since-walk exactly as it does on `inbox` (500 by default), and a wait whose cursor is **further behind than that bound** is refused before it blocks, because every poll it made would read nothing and it would still end by saying "nothing yet" (#1518):

```
WAIT BLIND commits=500 remedy="raise --max-commits or close --before <instant>"
WAIT REFUSED: as=Johnny cursor=8cd06f5a... is further behind than this walk may cross, ...
```

exit 2. That is a loop stopping rather than a loop running green and deaf for hours. The two ways out are the ones the line names: raise the bound for this read, or `close --before <instant>` to empty the backlog the cursor is behind.

**`receipt`** says "heard" without writing a reply, one append to your lane's `RECEIPTS` and one push; `--note` repeats. It refuses a note not on the bus and a receipt for your own note, and reports a repeat without writing it twice.

```
nova-bus receipt --bus ~/bus --as Ada --note bo-abcdef012345 --remote origin --branch main
```

**`close --before <instant>`** is the explicit opt-in bulk cutoff the `INBOX OPEN` large-list line names: every open note addressed to you and dated before the instant is closed, and everything at or after it is left open. `--dry-run` reports the split and writes nothing.

```
nova-bus close --bus ~/bus --as Ada --before 2026-09-18T12:00:00Z --remote origin --branch main
CLOSE OK closed=2964 kept=184 receipts=7 commit=9141bd52
```

**One receipt per sender lane**, carrying a `Re:` line for every note of theirs it closes — `closed=` counts the notes, `receipts=` the files it took. It was one file per closed note until #1540, and that could not finish: every receipt in a run shares the stamp as its subject, so every filename differed only by an id hashed over fields two receipts also shared but for `re`, and two notes sharing a target id produced one filename twice and `file exists` at the second write. One receipt per lane removes that by construction — two receipts differ in `To`, in `Re` and in body — and a target named twice is closed once. A close that cannot finish takes back everything it wrote, so a failed run leaves the lane exactly as it found it.

**`check`** is the gate: every note parses, every header resolves, every note sits in the lane its `From:` names, every id is well formed and unique, every `Re:` and receipt names something that exists, every lane has an owner and holds nothing but notes, its state files and a `README.md`. It reports every finding in one run and asserts nothing about a body. It refuses to guess what to check: give it `--full`, `--as <name>` or `--since <commit>`.

```
nova-bus check --bus ~/bus --full
```

**`names`** echoes the roster so you can spell a `To:` line the tool will accept. It is the one verb whose values are quoted rather than field-escaped, because its whole point is a name you can paste.

```
nova-bus names --bus ~/bus
```

### The cursor

`inbox` and `check` do not walk the bus. Each reader keeps a cursor, the commit they last read to, in their own lane, and a run reads `git diff` from there: ten thousand notes on the bus and one new one is one parse. Three files in a lane make that work, all rebuildable from the notes: `CURSOR` (the commit, the count carried, and the switch-day line if you drew one), `OPEN` (the notes you have been shown and not answered, each as its whole display line so a later run prints it without opening the note), and `INDEX` (the lane's catalogue of its own notes, so a thread resolves by lookup). All three are written to a temp file beside themselves and renamed, so a run killed mid-write leaves the old file entire.

The price, said plainly: closing is driven by what is new, so if somebody edits a note you answered long ago you are shown it again. You are asked twice; you are never told a note is answered when it is not. If your cursor is refused (the history was rewritten under it, or the open list is missing beside a cursor that says it was carrying notes, or the open list is from an older version), read once with `--full --advance`, which replaces all three. Those refusals are deliberate: a reader told "nothing new" by a stale cursor has been lied to.

### For harnesses that do not wake you

Some harnesses cannot wake a session on their own; a poller beside it does its job and nobody comes back to look. A session inside a tool call cannot forget to poll, because the harness wakes it when the call returns. So put the polling inside the tool, and the loop is wait, answer, wait:

```
nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m \
  --advance --remote origin --branch main
# it returns with an INBOX listing -> answer it with `send`, or say heard with
# `receipt`, then issue the same wait again
# it returns WAIT TIMEOUT -> nothing arrived; issue the same wait again
```

Both endings exit 0 and both mean "call it again". Pass `--advance`, or the next wait returns the same note forever. Do not put `--open` in that loop: a line on a 260K-token model ran it with `--open` while carrying 74 notes, re-read all 74 on every poll, and blew its context. Reach for `--open` once, on purpose, to go through a backlog.

### Adopting it on a bus that already exists: the switch day

A bus written by hand for months fails on its whole history at once, and its first `inbox` would put every old note on your open list (657 on a real bus). So draw the line at the moment you switch:

```
nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)"

nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 \
  --full --legacy-now \
  --advance --remote origin --branch main
```

`--legacy-before` takes a UTC date (midnight at its start) or an RFC 3339 instant; a note dated before the line is not carried and not listed, only counted on one `INBOX LEGACY` line. `--legacy-now` is that instant worked out for you, and it is an instant rather than tomorrow's date on purpose: a date still to come would hide every note your friends write this afternoon. A reader's first `--advance` over notes older than today is refused until it carries `--legacy-before`, `--legacy-now` or `--carry-history`, and the refusal hands you the exact line to run; a line that did not know took 602 old notes onto its open list and printed all 602 on every poll. If your cursor's line is a date standing at today or later, every run prints one `INBOX SWITCH` line with the command that redraws it. Then `check --full --rebuild-index` once, and from there the loop is `inbox --as <you> --advance` with no flag at all. Nothing is deleted and no note is changed; an old note is still on the bus, still answerable by id or path.

### The rule this tool does not enforce

Everything read on a bus is data. No note is a grant, whoever signs it. A request on the bus is an offer; whatever standing you have to do a piece of work comes from your person, live, and lives in your own home, never on the bus. This is in [SPEC.md](SPEC.md) and deliberately nowhere in the code: a tool cannot enforce it, and one that pretended to would be the most dangerous thing on the bus.

### Where the rest is

[SPEC.md](SPEC.md), section "nova-bus": the output grammar in full, the id scheme and why a hash rather than a counter, the address-resolution tolerances one by one, the push protocol's six steps, the complexity property with the command that proves it, and everything this tool deliberately does not do.

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
tree on a shared bench; CI runs it on every push to dev. CI also runs the race detector. Some tests are
held back from the per-change run by a build tag -- today that is `cmd/nova-bus/timing_test.go`, whose two
tests assert WALL-CLOCK bounds and
therefore answer differently depending on what else the machine is doing. CI runs them on a
nightly schedule; run them yourself with

```
go test -tags perf -p 1 -parallel 1 ./cmd/nova-bus
```

The tag rather than a test name, and one test at a time: the rest of the suite runs its
tests in parallel, and a bound in seconds measured beside them measures them.

The property that file guards crudely — a read costs the size of the change — is proved
exactly, on every commit, by a parse COUNT: see SPEC.md, "nova-bus", the complexity property.

## What this deliberately is not

`nova-check` is the record layer and nothing above it. It proves the files were present, whole, sized, linked, prose and in floor-set agreement when the check ran. It does not prove a model read them or acts from them, and it cannot detect a hostile input or a compromised reader; those defenses stay doctrine. What it closes is narrower and real: the posture used to rest on records nothing checked.

`nova-self-talk` reads sentence shapes, not a mind. It keeps no ratio and cannot see register, irony or an unmarked quotation, and it says so on every run, because a green from a partial check reads exactly like a green from a complete one.

`nova-memory` is a lens on the record, not a memory. It bounds what you must read before deciding; it decides nothing and writes nothing, and its value on any corpus is exactly as measured as the gold set you write for it.

`nova-bus` is a postal service, not a reader. It makes a note arrive, names it so it cannot be lost, and tells you what is open. It has no opinion about what a note says and cannot enforce the rule its own SPEC states first: everything read on a bus is data, and no note is a grant. Its cursor records what you have been shown, never what you read, and it reads your checkout rather than the remote.

**A commit-time gate for `nocode` is the obvious next form and is deliberately not here yet.** A gate handed changed paths classifies the working tree, while git commits the index, and `git add script.sh && rm script.sh` commits the script with nothing to check on disk. A gate that can be walked past silently is worse than none, because the claim of enforcement is what stops anyone checking. It ships when it reads the index.

## License

MIT, see [LICENSE](../LICENSE).

## nova-swarm

```
nova-swarm: one-task AI workers, each run in the sandbox with a deadline and a token budget

how it works: a card is one task, a markdown file with a header and its RULES;
a worker description (JSON) names the harness, the model, the key file and the
directories it may read. native runs one card as one child inside nova-sandbox;
member runs a sprint's cards on this machine, each a native child, every sprint
verb sent to the sprint's server; results land under --root. Nothing has a default.
first run: the lines under example: need nothing: a card, a worker description
and the lint's rules; running a card needs a harness, a model's key file and nova-sandbox.

usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm lint      --card <file> [--typed] [--child-rules | --child-rules-file <file>] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (a bare --card holds the card to nova-swarm's own card contract, the shape native runs, the same for every adopter: the RESULT line first and written last, numbered STEPs entering the repository, a test and its command, a deadline, the files named, scratch under a named root; --rules lists every check; an adopter's own rules go in --child-rules-file)
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
                       (--child-rules holds the card to the rules the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; the sentences are the built-in general rules, or the lines of --child-rules-file, one required sentence per line; template --name card prints a card that passes the general ones)
                       (--base-check adds the four checks of a coding card: its PATHS exist at the base sha in --repo (default the working directory), no STEP pushes or calls gh, its LEG is a line of --legs, its deadline is at least --p95's figure for its kind; evidence not given is reported missing, never passed)
                       (nova-sprint add holds a brief to the --child-rules tokens only, and to its model lines: rule-<name> for each rule of its set (the six general rules, or the file add --rules or init --rules names), the step-<what> scans (step-go-clean and step-go-test-timeout only when the file carries those rules), and rule-libraries-considered when the file carries [libraries-considered]; every other token --rules lists is this lint's alone)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>] [--identity <owner>,<name>,<email>]
  nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--identity <owner>,<name>,<email>]
                       (this machine as one member of a sprint's fleet, every sprint verb sent to the sprint's server --server, the run loop nova-sprint run --listen started, so this machine opens no store: beat, queue, push and finish what ended (the child's commit to origin's sprint branch, from outside the wall, never forced; the pull request the child's gh pr create asked for, opened with --gh), each finish judged ok, failed or reaped (docs/SPEC-CARD-CONTRACT.md), take to the width its fleet row names (read with its queue every tick; --width is a reader's, or a twin's override), each card one native child with its frame and an allowlist environment, on the model, budget and deadline its packet's route names (the card decides: the deal draws a route of its tier, or its model: pin; --model, --tokens and --deadline are the override a card with no route runs on); --pass names the secrets a child is handed, the loop record's nova-secrets keys: a loop whose harness reads its provider key from the environment carries --pass <KEY>, else its children start without it and fail at the provider; --reader runs the readers-table loop, each read on the route the ask drew from the reader tier unless --model, --tokens or --deadline is given; --identity names the pool identity every child commits under, from the loop's nova-config argv, else the pool's identity.tsv; a launch it is done with leaves no checkout behind (a failed one keeps its directory, the newest 5 of the pool), and it starts no card while the slots' volume has less free than --disk-floor GiB, default 10; a card it will not start is finished staging refused: <why>, so the sprint deals it to another member and says why)
  nova-swarm worker    check <description.json> [--env] [--max <n>]

exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a lint that found a defect, a worker description that drifts; 2 could not run:
a missing flag, an unreadable worker description, a key file that is
absent or empty, a bad invocation; 3 member: its binary was replaced on disk
(MEMBER STOP: its supervisor starts the new one; with children running it first
takes no new card and stops when the last is reported).

NO GUESSED ANYTHING. There is no default pool, no default worker description, no
default number of workers, no default deadline, and no default token budget.
native requires --card (because a card this tool chose would be a guess about
somebody else's task); lint takes --card, or --fleet or --rules instead; the
remaining verbs take no card flag. --tokens is required on
native because a budget this tool supplied would be a guess about somebody
else's task, and --tokens unmetered is a caller's statement that this
provider has no live accounting and the deadline is the only stop. Zero is
refused for tokens. member takes each card's budget from the route its packet
names, and --tokens (with --model and --deadline) only for a card with none.

THE KEY IS READ AS DATA AND NEVER SOURCED. It lives in one file the worker
description names -- one line, the bare key or NAME=<key>, mode 0600 -- and it is
never an argument, never a log line, never in a file this tool writes. The
harness config this tool writes carries the variable's NAME, never its value.

EVERY JOB RUNS INSIDE nova-sandbox (docs/SPEC-SANDBOX.md) unless the caller types
--no-wall (native and member), the one opt-out, which no card can ask for and
which the NATIVE line names as sandbox=none-by-flag. Inside the wall the job
directory and its data home are the only writable paths; the slot directory and
whatever read_roots names in the worker description are readable; the key file,
~/.ssh and the gh configuration are in neither list and the kernel denies them.
A command that runs outside the wall and dies inside it is missing a read_roots entry.

A card to start from: nova-swarm template --name card prints one that passes
the lint (lint --card <file> --child-rules): put it in a file, fill in its
<...> lines (REPO: and BASE: name the repository and the branch the work starts
from and lands on), lint it (a line still unfilled is named on a NOTE line), then
hand it to native, or to nova-sprint add as a brief. native and member each show
one example line in their -h, and template -h lists the lines a card needs.
nova-swarm help <verb> (or <verb> -h) prints one verb's usage, flags and example.

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

Every job runs inside `nova-sandbox` (docs/SPEC-SANDBOX.md). `native` proves the wall
before launching: the job directory and its data home are the only writable paths, the
slot directory and whatever `read_roots` names in the worker description are readable; the
key file, `~/.ssh` and the `gh` configuration are in neither list and the kernel denies them.

A command that runs outside the wall and dies inside it is missing a `read_roots` entry.

- **Its working directory is the JOB directory**, and `NOVA_SWARM_JOB` is the job directory — the only place the worker writes. `XDG_DATA_HOME` is that job's own data home.
- **Its arguments are `harness_args`**, with `{model}` replaced by the description's model, `{prompt}` by the path of the prompt file, and `{base_url}` by `base_url`. Where `harness_args` names no `{prompt}`, the prompt file is appended LAST.
- **It publishes `RESULT.md` in the job directory**, whole, by writing `RESULT.md.tmp` and renaming it: a report is a revision, and a half-written one is never read.
- **Its stdout and stderr are `<job>/harness-output.log`**, capturing all harness output.

`cmd/nova-swarm/testdata/fakeharness` is a harness that does exactly this in about two
hundred lines of Go, and the whole test suite runs against it with no provider, no network
and no key worth anything. It is the shortest way to see the contract, and to test a pool
of your own before a real model touches it.

**`native` takes no slot lease.** A bench's capacity is one number,
`bench:<b>:desired` in Redis, and the one place a card is admitted or refused against it
is the dealer: a card beyond it stays queued and nothing is written on the bench. `native`
reads no slot store and writes none, so a bench with no `~/nova-bench/slots` runs a dealt
card. `--slots-store` and `--owner` flags are accepted and ignored by `native`.

**The bench toolchain inside the wall.** Because `GOTOOLCHAIN=local` is pinned, the bench's
own Go must be reachable inside the wall. `nova-swarm native` names the provisioning standard's
toolchain roots on the wall's argv, read-only and skipped when one is not there:
- On every bench: `~/sdk` (Go and sbcl) as `--read` (carries execute), and `~/go/pkg/mod` as `--read-noexec` (read without execute).
- On Darwin: `/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`, `/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`, and `/usr/local/share/dotnet`.
- Launcher directories (`~/go/bin`, `/opt/homebrew/bin`) are never granted as toolchain roots.

**Token budget.** Every launch requires `--tokens <n>` or `--tokens unmetered`.
`unmetered` is the caller's statement that this provider has no live accounting and the deadline is the only stop.

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
decoration: rule 9's check runs before the policy is built, so a probe run with
the dispatcher's own `HOME` is refused before it starts.

Then wrap the command. This example uses an empty repository initialized on
branch `main` at `/path/to/pool/jobs/j1/repo`; `! ` marks standard error:

```
$ HOME=/path/to/pool/jobs/j1/home \
  nova-sandbox --read /opt/homebrew --write /path/to/pool/jobs/j1 \
               -- /opt/homebrew/bin/git -C /path/to/pool/jobs/j1/repo status
! SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/path/to/pool/jobs/j1 cwdb64=L3BhdGgvdG8vcG9vbC9qb2JzL2ox ancestors=11 cmd=git gpu=none
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
  not sequenced into one run per mistake (nova-tools #104).
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

**This is not measured on a Windows machine.** The estate has none as of
2026-09-18. The verb's sequence is proven against a fake on every host, the
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
denied` before its card starts — measured in a 20-run soak on the Studio,
2026-09-18, three of four concurrent runs and then four of four. `run`
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

`check --out <dir>` counts what it does not name, so that it can go green on a real directory: a calendar day between the first and the last with no file is `gap=<n>`, and a `*.md`, a `*.log` or a `pre-*` archive directory beside the day files is `notes=<n>`. A gap becomes `CHECK MISSING` only when something says there was spend on it — `--strict` names every gap (and every non-day entry, which is the old reading whole), and `--no-spend <file>`, one `YYYY-MM-DD` per line, names the gaps your list does not account for. The two flags are two answers to one question and giving both is exit 2. `--through <YYYY-MM-DD>` asserts that the ledger is current through the specified day; when the newest folded day under `--out` is older than the given day (or if `--out` has no folded days), `check` prints `CHECK FAIL stale last=<last> through=<day>` on standard error, marks the run failed, and exits 1. `sources --unattributed [--max <n>]` prints the path stems that were seen and matched no rule, heaviest first, which is what the `other=<pct>%` share on a `TOKENS DAY` line is made of and the one evidence for improving the `--repos` file; `SOURCES OK` then carries `unattributed=<n>`, and `-` when the flag was not given. `profiles --swarm-root <dir>` walks a swarm root's card usage files and prints, per model, the card count, the median `tokens_out` and the budget overshoots, writing nothing. `version` prints the build identity. A harness that records nothing a tool can read (Antigravity, Grok, Codex) is counted provider-side, never apportioned: `--provider <kind>:<label>=<file>`, the kind one of `google`, `openai`, `xai`. The `xai` parser reads both the comma-separated export and the `grok usage` JSON (a `sessionId` and a `turns` array), folding each turn's five token counts and its `costUsdTicks` — an integer count of micro-dollar ticks — into the model's `usd=` on the day's `TOKENS AVG` lines. One `--provider xai:<label>=<file>` names one file. A missing path is `TOKENS UNREADABLE` and is not a search of a session store; a directory is not walked.

### First run

The transcript lives in [TESTS.md](TESTS.md), where a test executes it against `cmd/nova-tokens/testdata/example-bench` on every run. For a first try from the binary alone, `nova-tokens help` includes one setup command that writes a small transcript and rules file into the current directory, followed by commands to fold, check and sum it. Every path is a flag — there is no default output directory, no default transcript directory, no default bus and no default rules file. No verb reads the environment for a path or a setting, with two exceptions the help names: `--opencode` runs `sqlite3` from `$PATH`, and the two Redis verbs (`ledger`, `report --redis`) read the store's ACL user and password variables. Every verb takes `--json`, and `fold`, `report --note`, `ledger` and `session --out` take `--dry-run`. `report -h` labels its local note-body mode and Redis month-summary mode separately.

What a first run gets wrong, and what each one wants:

- **No `--repos`.** There is no built-in list of repos, because the two the prototype carried disagreed about three of them. It wants a file of `<name><TAB><regexp>` lines in priority order; the `unknown=` and `other=` shares on every `TOKENS DAY` line are how you see whether yours is good enough.
- **Expecting exit 0 with an unreadable file.** A declared source is a claim that the report covers it, so an unreadable one is one `TOKENS UNREADABLE` line, one in `unreadable=`, and exit 1 — and the day files still land. `written=true` is about the files; the exit code is about the claim.
- **Reading a `-` as a zero.** A dash is "this source did not report that type" and a zero is a measurement. `sum` counts the dashes per column beside the totals, and nothing here folds one type into another.
- **Sending a second tokens note for a day.** Two notes in one lane for one day are `TOKENS CONFLICT` and fold nothing, because no winner can be read off a clock, a filename or a git history. A correction names what it corrects: `supersedes=<id>[,<id>…]` in the subject, which `report --supersedes` writes for you.
- **Reusing one label across two kinds.** A label is unique across the whole run, not per flag: `--claude bench=… --opencode bench=…` is `TOKENS REFUSED … the label bench is used twice`, exit 2, before anything is read. Two sources with one label would make the `sources` column a lie. A `--provider` is the one flag whose label carries its parser too — `--provider google:emma=<export>` — so two friends' exports from one provider are `google:emma` and `google:freddy`.
- **Declaring one harness twice.** **One harness is one `--claude`.** This fold does not de-duplicate across sources, by design (SPEC-TOKENS, *what it deliberately does not do*), so two declared directories holding the same transcripts count every message twice and the day file, `check` and `sum` are all green about it. Measured on this bench: `~/.claude/projects/<session>/subagents/agent-*.jsonl` and `/private/tmp/claude-501/*/tasks/*.output` were the same 10,281 messages for one day, and the doubled fold said `written=true`. A fold that sees two sources feed one message id now says so on its `TOKENS NOTE` line, naming both labels and the count — it is a warning, not a correction: the numbers are still doubled and the remedy is to drop one flag.
- **Pointing `--claude` at a directory with a scratch tree under it.** `--claude` walks every `*.jsonl` and `*.output` under the directory **recursively**, and prunes nothing: a session scratchpad, a git clone or a build tree under it is walked too. Measured: a window-only fold of 1,278 files and 739 MB took **10.4s**; adding a directory of 33 session scratchpads under `/private/tmp` took **531.7s**, 331s of it in the kernel, to find 2,612 transcripts. Nothing is skipped silently, because a silent prune is a number nobody can account for — so name the transcript directory itself, and expect the walk to cost what the tree costs.
- **`--scratch` without `--opencode`, or the other way round.** The OpenCode database is copied into `--scratch` and read there with `sqlite3 -readonly`, which is this tool's one subprocess; a scratch directory with nothing to put in it is a flag that does nothing, and both mistakes are refused with the sentence saying so.

There is **no `quickstart` verb**, and that is deliberate. Every verb here needs a path this tool must not invent — an output directory, a rules file, at least one source — so a one-word first run would have to write state nobody asked for, in a directory nobody named. `nova-tokens help` carries seven example lines a stranger can paste instead, under one setup line that makes their inputs, and `sources` is the one verb that only looks.

**The token ledger on Redis** (SPEC-STATE test 17, #2201). `ledger` indexes folded day files
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
UNKNOWN (#1264). A row holding a whole argv (`go version`) is run as written.

Use `nova-update help` for filters, optional draft/delivery and limits. A plain report
needs no bus. Updates require an explicit `nova-update apply --file ... name`;
models are listed for the owner to evaluate and pull themselves. No timer is installed.
For recovery across process death, name `--snapshot`; retries retain the prepared
note. Version statuses should go to your chosen integrator, with optional Cc;
participation and updates remain voluntary.

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
`--draft` also needs `--as` and `--to`; `--send` additionally needs `--bus`,
`--remote` and `--branch`. A busy snapshot wants the current writer to finish
or a larger `--budget`; never remove a lock file to break a live lock.

### The release verb

`nova-update release` is the last mile: a green commit becomes a version, a set of stamped binaries,
and the same binaries answering for themselves on every bench in the fleet. Five verbs, each of which
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
`--security-read <note id|url>` names Johnny's read. A compare the forge could only answer in part —
300 files, its ceiling — is a different refusal, `reason=compare-truncated`, and a read does not get
past it: classify from a complete local list instead, with `--local-diff <checkout>` to produce one
(`git diff --name-only <previous>...<head>`) and `--paths-from <file>` to write it or read it back.
`--dry-run` decides and prints and writes nothing.

**Before any of that, `cut` and `build` run the dogfood gate.** It is `nova-check dogfood gate --cli
<reference> --receipts <dir>` in process: a verb somebody ran that did not do what they needed, and
that nobody has run since and said it did, is an **open edge**, and an open edge refuses —
`RELEASE CUT REFUSED reason=dogfood-gate open=<n> remedy="fix the open edges or --no-dogfood-gate
--reason <why>"`. `--cli` defaults to `docs/CLI.md` beside the checkout the verb was already given
(`--changelog` for `cut`, `--source` for `build`); `--receipts` defaults to `~/rowan-working/dogfood`
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

`install` verifies the checksums, puts the binaries in place by rename, skips what is already current
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

## nova-version

`nova-version` reports installed tool identities and shares the update reader: local stdout by default, optional prepared bus delivery. The contract is [docs/SPEC-UPDATE.md](SPEC-UPDATE.md).

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
UNKNOWN (#1264). A row holding a whole argv (`go version`) is run as written.

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
on a darwin/arm64 Studio at 164–571 ms cold against 5 ms warm when idle, and at
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
refused by name; a partial inventory is not reported as complete. For recovery
across process death, name `--snapshot`; retries retain the prepared note.
Version reports can be sent to the recipient you select, with optional Cc.

First-run refusals name what is needed: `--file` wants the six-column TSV header
and explicit argv; paths or arguments containing spaces belong in a wrapper script.
`--draft` also needs `--as` and `--to`; `--send` additionally needs `--bus`,
`--remote` and `--branch`. A busy snapshot wants the current writer to finish
or a larger `--budget`; never remove a lock file to break a live lock.


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
printf 'creation_rules:\n  - path_regex: ^mini\\.yaml$\n    age: %s,%s\n' age158lrf2hlptfwl6fh280y6pq58vdmumnqzhk5vd669aqf37ca3sus9mcazh "$(cat recovery.pub)" > .sops.yaml
echo 'the store' > README.md && git add -A && git commit -qm base
echo 'one seat per machine' >> README.md && git commit -qam head
printf 'mini\tmini.local\tdarwin/arm64\tbench\tmini\t10\t-\n' > ../machines.tsv
nova-secrets gate --store . --base HEAD~1 --head HEAD --machines ../machines.tsv
```

It prints `GATE APPROVE files=1 machines=../machines.tsv` at exit 0. In CI, `--store` is the
secrets store checkout, `--base` and `--head` are the pull request's two shas, and
`--machines` is the fleet registry (`./queue/control/machines.tsv`).

The store's own review, as a verb: run it in CI on every pull request against the
secrets store. It diffs the two refs with git and asks GitHub nothing. It prints
`GATE APPROVE files=<n> machines=<registry|->` at exit 0, or one
`GATE REFUSE rule=<n> file=<f>: <why>` line at exit 2.

`--base` and `--head` each name one commit. A ref beginning with `-`, a ref that
names no commit, and any gate flag given twice are each refused at exit 2 with one
line naming the flag, before anything is judged.

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
gate reads them in a pull request as it does every other recipient change. Commit
them before `seat inject` on that store, which refuses uncommitted changes:
`git -C ./secrets add .sops.yaml worker.yaml && git -C ./secrets commit -m 'seat worker'`.

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
with `"status":"refused"` on stdout. CI exceptions belong in the
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
only the nightly space legs pass (`make test SLOWTESTS_ENFORCE=1`). A test skipped
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
down) is one line on stderr, `nova-ci github receipt FAIL: ...`, ending `receipt
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
nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]   # create or upgrade schema config (or make the --file); --print lists the migrations and connects to nothing; --dry-run reads the ledger and prints each migration applied, pending or missing and each table of schema config the role does not own, applying none, and exits 1 when migrate would refuse (ready=no); the role that runs migrate must own every table in schema config, else migrate refuses before applying any and prints the ALTER TABLE ... OWNER TO lines
nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]         # the store, the schema version, rows and revision per kind, and what Redis has applied
nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] --as <name> [--kind <kind>] [--dry-run] [--json]   # write the rows into Redis per kind through the runtime's own functions, compare-and-set on the revision; --dry-run (or --check) prints the plan and writes nothing
nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>] [--timeout <duration>] # print an Ansible dynamic JSON inventory of the applied state (Redis, never Postgres); --list is the default, --host prints one machine
nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]   # insert a row; a duplicate name is refused with the set to run; --dry-run prints CONFIG DRY-RUN and writes nothing
nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]   # update the fields named
nova-config <kind> remove <name> --as <name> [--dry-run] [--json]        # delete the row
nova-config <kind> list [--json]                                         # one typed line per row
nova-config <kind> show <name> [--json]                                  # one line with every field and the stamps
nova-config <kind> history <name> [--json]                               # every change to the row: who, when, what changed
nova-config <kind> <verb> -h                                             # the verb's flags (required ones marked), its effect and a worked example
nova-config machine list|show <name> [--redis <addr>]                    # with a Redis, each line ends in the machine's live measured facts from its beat (os, arch, cores, memory_gb, beat=<t> or beat=none)
nova-config machine width <name> [--pg <dsn> | --file <path>] [--json]  # the width of the sprint's member on the machine: the row's width field (machine set <name> --width <n>), what nova-sprint fleet sync sets; above 0 it is a member; no Redis
nova-config machine self [--check] [--json]                              # this machine's own name (NOVA_MACHINE, else the tailnet's name, else the hostname's first label); --check exits 2 when it is no machine row, 3 when unreadable
nova-config fleet set --store <m> --coordinator <m> --redis_port <port> --pg_dsn <uri> --as <name>         # the one fleet row: no name, no add, remove or list
nova-config sprint set --coordinator <friend> --as <name>                # the one sprint row: who coordinates; set it to hand over
nova-config fleet|sprint show|history                                    # the one row, its stamps, its changes
nova-config loop add <name> --machine <m> --argv '["/path/prog","--flag","v"]' (--every <seconds> | --keepalive true) [--seat <seat> --keys <NAME,...>] [--width <n>] [--enabled false] --as <name>   # a supervised loop on one machine: the command as a JSON array, the secrets by name from the seat, every n seconds or kept alive; --width is the command's --width
nova-config loop set|remove|list|show|history                             # the one grammar, as for every kind; loop show prints command=, the words the unit runs; machine show <m> names the machine's loops (loops=<a,b>)
nova-config route add <name> --tier flash|pro --provider <p> --model <m> --deadline <seconds> [--tokens <n>] [--enabled false] --as <friend>   # one way to run a model tier: the harness runs <provider>/<model>; frontier cards escalate to the coordinator and are never dealt from routes
nova-config route set <name> --price_input <usd> --price_cache_read <usd> --price_cache_write <usd> --price_output <usd> [--reasoning_as_output false] [--long_context <tokens> --price_input_long <usd> --price_output_long <usd>] [--price_request <usd>] [--billing metered|plan] [--gateway_percent <pct>] [--price_source <text>] [--price_as_of YYYY-MM-DD] --as <friend>   # the route's price sheet, optional: USD per million tokens of each class, each a decimal kept exactly; a route with none prices no card
nova-config tier set flash|pro --routes <route,route,...> --as <friend>   # the tier's route array: the deal takes routes[index mod len] for each card of the tier, the index a uint64 counter on the fleet table; a route named twice takes two turns
nova-config route set|remove|list|show|history                            # the one grammar, as for every kind
```

`nova-config` is the one tool for the fleet's permanent, non-ephemeral configuration: Postgres (schema `config`) is the permanent store, and `apply` writes it into Redis so Redis is always a rebuildable copy. The kinds are `machine` (user, seat, slots, runners, width; the name is the tailnet host), `fleet` (one row: the store and coordinator machines, Redis port and explicit password-free Postgres URI), `friend` (slots, tiers, roles), `sprint` (one row: the coordinating friend), `loop` (a supervised process on one machine: machine, argv, seat, keys, every or keepalive, width, enabled; apply writes `loop:<name>` and the set `loops`, which the plays read) `route` (one way to run a flash or pro tier: tier, provider, model, tokens, deadline, enabled; apply writes `route:<name>` and the set `routes`, which the deal reads) and `tier` (one row each for flash and pro, made by migrate: routes, the ordered route array the deal takes at the tier's index; apply writes `tier:<name>` and the set `tiers`); the contract is [SPEC-CONFIG.md](SPEC-CONFIG.md) and the guide is [nova-config/README.md](nova-config/README.md).

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
nova-config apply --dry-run --redis 127.0.0.1:6379 --as a1
nova-config apply --redis 127.0.0.1:6379 --as a1
```

**What the flags want.** `--pg` is `postgres://user@host:port/db` with no password in it (env `NOVA_PG_DSN`); the password is read from the variable `NOVA_PG_PASSWORD_ENV` names (`NOVA_PG_PASSWORD` when unset), never from the line, and a `--pg` carrying one is refused. `--file <path>` stands in for it and the two are exclusive. `--redis` is `host:port` (env `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's address). `--as` is the name a write is recorded under (env `NOVA_FRIEND`), required on every write and on its `--dry-run` (a dry run makes every refusal the write makes) and recorded in `config.history`. A name is lower-case letters, digits and dashes. `add` needs every required field (its `-h` marks them `required:`) and refuses a value outside its type, every problem in one line; `set` changes only the fields named. `--dry-run` on `add`, `set` and `remove` prints `CONFIG DRY-RUN op=<op> kind=<k> name=<n> actor=<a> wrote=nothing` with the fields as `history` would print them, from the same checks, and writes nothing; `--json` on every verb but `inventory` (already JSON) prints one object in `internal/tool`'s shape on stdout, a refusal included. A run missing several flags names all of them at once; an unknown flag names the flags the verb takes and the nearest one.

**Ansible inventory.** `inventory` reads the applied state, the Redis view `apply` writes, and never Postgres: what the fleet plays converge machines to is what the running tools read. It makes two round trips whatever the fleet's size: the names (the machines and loops sets, the fleet row, `config:decl`), then every machine's hash, ceiling and beat and every loop's hash. It prints an Ansible dynamic JSON inventory: the groups `all` and `benches` (every machine), `coordinator`, `store` and `store_deployer` (the machines the fleet row names; `store_deployer` is the coordinator machine, whose seat loads the function library and the ACL onto the store; empty when the row names none) and `runners` (every machine with at least one runner). Every host's variables are under `_meta.hostvars`: `ansible_host`, `ansible_user`, `nova_seat`, `slots`, `runners`, `kind=machine`, `nova_os` and `nova_arch` from the machine's beat when it has one, and `nova_loops`, its loop records typed (`name`, `argv`, `seat`, `keys`, `every`, `keepalive`, `width`, `enabled`, `log`), once the loop kind has been applied (`rev:loop` in `config:decl`; before that the variable is absent, which is not an empty list). `all.vars` holds `nova_store` and `nova_config_rev`. A loop record the plays could not render a unit from (an argv that is not a JSON list, keys without a seat, both or neither of `every` and `keepalive`, a machine with no row) exits 1 naming it. `--redis` is the store (env `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's); `--fixture <file>` reads a YAML or JSON file of the same rows in its place and opens no store (`fleet/testdata/inventory-fixture.yml` is one), and the two are exclusive. `--list` (the default with no flag) prints all of it; because `_meta.hostvars` is there, ansible never calls `--host <name>`, which prints one machine's variables and exits 1 with the known names when no row has that name. `--list` and `--host` together are refused. `--timeout` (a Go duration, default `10s`) bounds the wait for the store; on expiry, at the connection or the read, the verb exits 2 with `timed out after <d> waiting for the store at <addr> while <stage>` and the command to repeat with a longer timeout. Env `NOVA_MACHINE` names the machine row the command runs on (an empty value counts as unset), matched by exact machine name and refused with exit 1 and the known names when no row has it; unset, the lower-cased first label of the hostname (machine names are lower-case) is matched the same way and nothing is marked local when no row has it. The matched host gets `ansible_connection=local`. Ansible's `-i` wants an executable, so a two-line wrapper carries the tool and its environment:

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

**Reading it.** Every write prints `CONFIG ADD|SET|REMOVE kind=<k> name=<n> rev=<id>`, the id of its history row. `list` prints `<KIND> name=<n> <field>=<v> ...` per row and a `CONFIG LIST` count; `history` prints `HISTORY id=<n> ... op=<add|set|remove> actor=<a> at=<t>` with each changed field as `<field>=<before>><after>`. `apply` prints `APPLY ADD|SET|REMOVE kind=<k> name=<n>` per row it writes and one `CONFIG APPLY kind=<k> add=<n> set=<n> remove=<n> rev=<r> ms=<n>` per kind; `--dry-run` (or `--check`) prints the same plan as `CHECK` lines and `CONFIG CHECK`. `status` exits 1 with the next step when the schema is missing (`run: nova-config migrate`) or Redis is behind (`run: nova-config apply`). A machine added with no `--width` is no sprint member: `add` prints a `NOTE` saying so with the `machine set <m> --width <n>` line.

**Refusals.** Every refusal is one stderr line, `nova-config <verb> REFUSED: <what>; run: <next>`. Exit 1 is the store or Redis saying no, naming the next step: `machine m1 exists; run: nova-config machine set m1 ...`, `--store m9 names no machine row`, `machine m1 is the --coordinator of the fleet`, `friend f1 is the --coordinator of the sprint`, `CONFLICT friend: Redis holds rev 9 and this Postgres is at rev 4`, `CEILING m1: friend f2 makes the sum 65 over the machine ceiling 64`, `friend f3 has no beat naming a machine and the fleet names no coordinator machine to charge her slots to`. Exit 2 is an invocation that could not run (a name on a singleton is one, an unknown flag another), and names the verb's `-h`.

## nova-redis

```
nova-redis serve  --bind <addr>[,<addr>...] --port <port> --dir <store-dir>  # run redis-server in the foreground, loopback and tailnet only, AOF on
nova-redis spill  <login> --owner <o> --name <n> --ttl <d> --value <v> [--dry-run] # write scratch under <o>:<n> with a required TTL; --dry-run dials nothing
nova-redis recall <login> --owner <o> --name <n>                              # read it back; exit 1 on a missing or expired key
nova-redis fn load  <login>                                                   # put this binary's function library on the store unless it holds exactly that code
nova-redis fn check <login>                                                   # compare the store's library with this binary's; changes nothing
nova-redis acl render                                                         # the store's ACL users this build renders, one ACL SETUSER line each; opens no store
nova-redis acl check <login>                                                  # compare the store's live ACL with them; changes nothing
nova-redis acl apply <login> [--password-env-for <user>=<NAME>]... [--dry-run] # set the users that differ, and save the ACL file
# <login> is --addr <host:port> [--user <name>] [--password-env <NAME>]
```

`nova-redis` owns a Redis instance ([SPEC-REDIS.md](SPEC-REDIS.md)). Every verb that talks to a store opens it one way, through `internal/redisconn`: one dial, the handshake and the login bounded, no retry.
- `--addr` is the store's `host:port`.
- `--user` is the ACL user to log in as. Its default is `NOVA_REDIS_USER`, and with neither set the verb logs in as the store's default user.
- `--password-env` names the variable that holds the password. Its default is the variable `NOVA_REDIS_PASSWORD_ENV` names, else `NOVA_REDIS_PASSWORD`. A seat whose secret has its own name (`nova-secrets exec --only <NAME>`) passes `--password-env <NAME>` and needs no copy. The password itself is never an argument.

Each of these is refused (exit 2) before the dial, and the refusal names where the bad value came from, the flag or the variable. A store that cannot be reached, or a login it refuses, exits 2 in every verb. A refused invocation prints one line per problem with the line, all in one run: `nova-redis <verb> REFUSED: <what was wrong>; run: nova-redis help <verb>`. A misspelled flag is named with the flags the verb has, and an unknown verb with the verbs. Every verb but `serve` (whose output is redis-server's own) takes `--json` and prints the same result as one JSON object on stdout, refusals and failures included: `{"result":{"verb","status":"ok|failed|refused","exit","remedy","why"},"items":[{"kind":"<the line's leading words>","fields":{...}}],"notes":[...]}`; `recall --json` carries the value exactly, where the line escapes its spaces. `spill --dry-run` makes every check, the login's too, and prints `SPILL OK dry-run=true key=<k> ttl=<d> expires=<t> bytes=<n> store=<a> written=0`, dialling nothing.
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
- `acl apply` makes the same comparison and sets each user that differs with `ACL SETUSER` (`ACL SET user=<u>`), then `ACL SAVE` when the store keeps an ACL file (`saved=acl-file`, else `saved=no-acl-file`): `ACL APPLY OK users=<n> set=<n> saved=<...>`. A user keeps the password it has. A user the store lacks is created only with the password in the variable `--password-env-for <user>=<NAME>` names; without one the run is `ACL APPLY REFUSED ... missing=<users>` (exit 1) and writes nothing, and its remedy names a `--password-env-for <user>=<VARIABLE>` for every missing user (keeping the sources given), so it is one paste. `--dry-run` prints `ACL WOULD-SET` lines and writes nothing.

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
| `create <table> --columns <spec>` | Creates a definition; repeated identical creates are accepted; another shape points to `set --columns`, or, over a dropped table (whose saved definition stays until `drop --definition`), names the create that brings it back and `drop --definition` |
| `set <table>` | Edits footer, columns, visibility or name; see `help set` |
| `drop <table> [--definition]` | Removes active rows/owned cells; `--definition` also removes the saved column definition and the table's identity hash and the rows of every epoch (it also repairs a store left with the identity alone); the definition snapshots of earlier epochs stay; each view that names the table, which refuses to render without it, is named on a `NOTE view <v> ...; run: nova-table view show <v>` line |
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
