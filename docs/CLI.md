# Command reference

[Back to Nova Tools](../README.md)

Command reference and worked examples. Run shell examples from the repository root unless a section says otherwise. `-h` or `--help` after any verb prints that verb's help (its usage lines and every flag it takes) on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question; `<tool> help` is the whole banner. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line, so what is shown here is what the tool does today.

## nova-check

```
nova-check quickstart --dir <dir> [--max <n>] # the first run: links, then nocode, both run even if the first says NO
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
nova-check dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--max <n>] [--dry-run]   # append one receipt, refusing a verb the list does not declare
nova-check dogfood gate (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--shipped <cmd dir>] [--require-all] [--allow-empty]   # exit 1 with the verbs no non-author has run and the edges nobody has cleared: the line a release calls
nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>] [--state <file>] [--by <name>] [--json] [--timeout <n>] [--dry-run]   # are we converging: one line per stream, now against --since, with the ratio and the trend
nova-check spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--dry-run] [--exclude <prefix>] [--max <n>]   # check markdown or prose for misspellings; fenced code blocks and inline code spans are blanked so code is not prose; --write fixes misspellings in place
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

**Reading it.** Every line is `<CHECK> OK` or `<CHECK> FAILED`; FAILED lines go to stderr with the subject named. `worst-exit=` is the run's exit code. A failing run is bounded: `attest`, `links`, `nocode`, `corpus` and `quickstart` print at most `--max` FAILED lines (default 20, `0` for all), then one `MORE` line naming the flag that shows the rest, then a count line that prints on success too. The four verbs `quickstart` names at the end each want something only you have: a size budget, a boot manifest, a seed to compare against, a ledger of what you have chosen never to lose.

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
nova-check hygiene REFUSED: --kind "fix-with-red-test" is not a kind this tool declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, read, probe, text, tone, report; run: nova-check help
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
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS: sign/**
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
HYGIENE FAILED base=main head=card paths=sign/** findings=4
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
nova-fuse version    print this build identity (--version also accepted)
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

```
nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]... [--json]
                                                                        the first run: stats, one search, one check, each with the line that ran it
nova-memory stats  --root <dir>... [--exclude <glob>]... [--json]
                                                                        measure m: files, chunks, bytes, vocab, build time, classes
nova-memory search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <words>...
                                                                        one query, k receipted hits (for work retrieval)
nova-memory check  --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <file|->
                                                                        do I already know this? k receipts per candidate paragraph
nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]... [--exempt <prefix>]... [--fail-max <n>] [--exclude <glob>]... [--json]
                                                                        coverage, backlinks, wikilinks, frontmatter — it finds, you decide
nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]... [--fail-max <n>] [--json] <gold.tsv>
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

**What the flags want.** `--channels` is a retrieval method, `bm25` or `trigram`, never a directory. `--k` is the number of hits, your reading budget; there is no default. `--root` is your corpus, written out every run, and repeatable: two roots are indexed together in one ranking, each hit naming its root. A run short two flags prints two sentences and stops once.

**`verify` and `eval` are bounded**, per kind: at most `--fail-max` findings per kind, one `MORE` line per kind that elided anything, then the count line. On a 5,000-entry corpus `verify` used to print 10,000 lines and no total. `eval` lists misses only; a passing row is a number, not a line.

**`boot` loads a pin, not a directory.** The pin file names the few memories a session loads — one slash path per line relative to `--root`, `#` comments and blank lines ignored, order = boot order — and boot reads exactly those files, reporting `BOOT OK files=<n> bytes=<n>`. It never walks the directory: search answers the rest from the index. A boot that cannot name a memory (missing file, empty file, non-canonical path) is a refusal, because a self that loaded less than it thinks is the failure this verb exists to remove.

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
nova-bus send --as ada --to bob --subject hello --body "are you there?"
nova-bus peek --as bob
nova-bus recv --as bob --exec true
nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
nova-bus log --max 5
nova-bus names
```

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
next run). The third is by hand, after a plain `recv`.

### Commands

| Command | What it does |
| --- | --- |
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

What a friend runs to be part of the team: the wake loop, the beat and the
proof of life, as one daemon. One launchd agent per friend parks on the
friend's nova-bus stream and, whenever the session is free, pushes every
waiting message into the running session as one turn through the harness's
deliver command, beats to the sprint server while the loop runs, answers the
coordinator's `PING` at once (`daemon-pong`) and never makes a turn of it; the
session's own `pong --nonce`, its line at the head of the next turn, alone
makes the friend up. No ping for a window and the session is told the
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

### The friend health check

The help of `nova-friend check -h` says, and this is the same text:

```
The health check: is each friend's row true. The friends are the arguments, else every friend with a
state directory under ~/.nova-friend (or --state-dir) or on the bus. Everything is judged over the --since
window (default 24h): deliveries, deferrals, real messages and the session pong. Per friend, five lines in
this order:
CHECK DAEMON friend=<f> agent=<loaded|not-loaded|none> pid=<n|-> status=<ok|stale|none> connection=<..> challenge=<..> pong_age=<age|-> presence=<up|asleep|down> seen_age=<age|->
CHECK HARNESS friend=<f> harness=<h> route=<push|defer|passive> last=<RFC3339|-> last_exit=<n|-> failed_of_last20=<n> deferred=<n> broken=<RFC3339|-> reason=<line|->
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
friend and daemon{friend, agent, pid, status, connection, challenge, pong_age, presence, seen_age},
harness{friend, harness, route, last, last_exit, failed_of_last20, deferred, delivered, failed, broken,
reason}, bus{friend, real_since, last_real}, work{friend, inbox, outbox, newest_outbox, newest_at},
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
| `status --as <me> --dir <d> [--state-dir <d>]` | The daemon's state, the last pong, the queue file's counts |
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
                        --pass names environment secrets to hand to children; a harness that needs one must receive it.
                        --identity names the pool's commit identity; otherwise the pool's identity.tsv supplies it.
                        Completed launches leave no checkout; each pool keeps its newest five failed launches.
                        No card starts below --disk-floor GiB free (default 10); --stage-wall bounds staging (default 120s).
                        A staging refusal reports why so the sprint can deal the card to another member.)
  nova-swarm disk-guard [--root <dir>]... [--scan <dir>]... [--cache <dir|glob>]... [--cache-max-gb <GiB>] [--modcache-max-gb <GiB>] [--logs <dir>] [--log-max-mb <MiB>] [--log-keep <n>] [--pool-idle <duration>] [--land <dir>] [--clone-age <duration>] [--mirrors <dir>] [--disk-floor <GiB>] [--dry-run]
                       (one pass over this machine, run every few minutes by the disk-guard loop row fleet/loops.yml adds to every machine: every Go build cache (the login's, each root's cache/go-build, each --cache) held under --cache-max-gb, default 20, by the member's trim, oldest entries first and never one used in the last two hours; a module cache over --modcache-max-gb, default 50, emptied while no go command runs; every loop log over --log-max-mb, default 50, copied to <log>.1 and emptied in place, --log-keep copies, default 3; the pool of a loop that stopped (no process names its root, nothing moved for --pool-idle, default 30m) swept as the member sweeps its own, a work launch whose checkout holds commits past its staged one kept; land clones unused for --clone-age, default 24h, removed; a mirror's temporary packs older than an hour removed while nothing fetches into it, never git prune; never anything with uncommitted work or a live process; one REMOVED, TRIMMED, CLEANED, ROTATED or KEPT line per action with freed=<bytes>, a DISK-GUARD WARN line under --disk-floor, default 10, and DISK-GUARD OK freed=<bytes> free=<bytes> at the end; --dry-run judges the same and removes nothing, each action said WOULD-REMOVE, WOULD-TRIM, WOULD-CLEAN or WOULD-ROTATE)
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
nova-sprint release <sentinel>... --reason <text> [--answers <note>]
nova-sprint resolve [<id>...] [--stream <s>] [--limit <n>]
nova-sprint start
nova-sprint stop
nova-sprint run [--answer-rules=false] [--idle-alarm=false]
nova-sprint tick [--answer-rules] [--idle-alarm]
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
nova-sprint accept (<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]
nova-sprint rework (<id>... | --group <id> [--expect <n>]) [--fix <text>] [--answers <note>]
nova-sprint return (<id>... | --group <id> [--expect <n>]) [--reason <text>] [--answers <note>]
nova-sprint drop (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>]) --reason <text> [--answers <note>]
nova-sprint rank <id>... (--score <n> | --first) [--answers <note>]
nova-sprint relink <old-id>[,<old-id>...] <new-id> [--reason <text>]
nova-sprint sentinel set <id> --needs <a,b>
nova-sprint brief <id> (--brief <text> | --brief-file <path>) [--rules <file>] | <id> --tier <flash|pro|heavy|frontier>
nova-sprint move <id>... --stream <s> [--before <id> | --after <id> | --score <n>]
nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> [--conflict-kind file|ledger] [--conflict-path <p>...] | --cross <id>=<other> | --red [--suspect <id>...] | --rejected | --base-red <error>] [--note <text>]
nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
nova-sprint stream set <stream>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--release <name>] [--prose <glob,...|default>] [--attempts <n|default>] [--reason <text>] [--answers <notes>]
nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
nova-sprint backup --file <path>
nova-sprint fleet beat <member> [--load <percent>]
nova-sprint fleet up <member> [--width <n>]
nova-sprint fleet down <member>
nova-sprint fleet sync [--check] [--pg <dsn>]
nova-sprint fleet level
nova-sprint friend sync [--pg <dsn>] [--root <dir>] [--every <duration>]
nova-sprint friend sync install --every <duration> [--redis <addr>] [--pg <dsn>] [--root <dir>] [--dir <dir>] [--log <file>] [--dry-run]
nova-sprint friend sync uninstall [--dir <dir>] [--dry-run]
nova-sprint collect [<friend>...] [--dead-lanes] [--pg <dsn>] [--root <dir>] [--dry-run]
nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>]
nova-sprint friend down <friend> [--reason <text>] [--until <RFC3339>]
nova-sprint friend up <friend> [--width <n>]
nova-sprint friend cards <friend> [--json]
nova-sprint friend take <friend> (<id>... | --all-unstarted) [--reason <text>]
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
nova-sprint card <id>
nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
nova-sprint check
nova-sprint repair
nova-sprint where [--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived]]
nova-sprint view coordinator [--all] [--since <cursor>] [--json]
nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]
nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]
nova-sprint dashboard [--listen <address:port>[,...] | none] [--pull <address:port>[,...] | none] [--logo <file>] [--every <duration>]
nova-sprint seat
nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr> [--sops <path>]
nova-sprint seat login --check
nova-sprint seat logout
nova-sprint seat push [--harness <name> --target <dir> [--session <id>]] [--json]
nova-sprint seat pong <nonce>
nova-sprint routes
nova-sprint rules
nova-sprint funded <provider> --reason <text>
nova-sprint cost reconcile [--dry-run] [--json]
nova-sprint stats
nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
nova-sprint clear --confirm sprint
nova-sprint teardown --confirm sprint
```

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
are reports; `tick`, `run` and `friend clean` are the machine's. Reads need no
actor except `inbox --read`, which moves the coordinator's cursor. A set is
ids, a stream, a column, `--max n` (`--limit` is an alias), or an inbox group:
`--group <id>`, the id `inbox` prints, with `--expect <n>` the size it printed,
which refuses a group that has changed. `nova-sprint help <verb>` (or
`<verb> -h`) prints one verb's usage, flags and exit codes; `nova-sprint help
<group>` (fleet, friend, reader, goal, stream) prints one group's.

### The seat's store login

`nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr>` records the store login in `~/.config/nova-sprint/login.json` (or under `$XDG_CONFIG_HOME`), mode 0600: the address, the user and where the password is in nova-secrets, never the password, and only once the secret resolves. After it, `nova-sprint <verb>` typed bare reaches that store as that user, the password read in the verb's own process through nova-secrets' checks, with no `nova-secrets exec` wrapper; `--redis`, `NOVA_SPRINT_REDIS`/`NOVA_REDIS_ADDR` and `NOVA_SPRINT_REDIS_USER` still win. `seat login --check` prints `SEAT LOGIN file=… redis=… user=… … resolves=yes|no` (exit 1 on no), the password never shown; `seat logout` removes the record. A recorded secret that does not resolve is refused naming the file and the remedy, never dialed without a password. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#the-seats-store-login).

The seat is held only by a session the push loop reaches ([SPEC-SPRINT.md](SPEC-SPRINT.md#the-push-proof)). `nova-sprint seat install --actor <seat> --harness <harness> --target <session dir>` records the seat's push target and installs the push loop; the loop delivers `NOVA SPRINT PUSH CHECK <nonce>` into the session through the harness's nova-friend adapter, and the session answers with `nova-sprint seat pong <nonce> --actor <seat>`. Until that pong is in, and again whenever it is older than 15 minutes (the loop asks every 10), every coordinator verb is refused with one line, `PUSH DOWN: <why>; ... run: nova-sprint seat install ...`, and `coordinator <name>` refuses a name with no live proof. `seat push` prints `PUSH OK` or `PUSH DOWN` with why and the remedy (exit 1). A harness whose adapter is still the Stub (Claude Code, until fg-claude-open-chatb-r lands) is refused at install.

### The sprint backup

`nova-sprint backup --file <path>` writes the store to a new file (owner-only; an existing file is refused, never overwritten), reads it back against its SHA-256, restores it into a twin and compares it with the store, and scans it for secret-shaped text. A file that fails any step is removed. On success it prints `BACKUP OK file=<path> sha256=<hex> bytes=<n> keys=<n> cards=<n> restored=twin compared=<document+counts|counts> secrets=none`; a refusal names the failed step and, for a secret, the lines (never the value). It runs on the store's host for a Redis, and on any twin (`--redis mem:<file>`) with no server. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md#sprint-backup-verb).

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
line. The contract is [SPEC-SPRINT.md](SPEC-SPRINT.md), the dashboard.

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
past its deadline, cannot ask, ready to accept, a card at its bound) by
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

### Exit codes

| exit | meaning |
|---|---|
| 0 | done |
| 1 | failed or incomplete (including refused; `start` while every provider is out of credit) |
| 2 | usage, or a store that did not answer (`fleet sync --check`: there is drift) |
| 3 | unreadable config (`fleet sync`, `friend sync`, `friend clean`), missing friend rows (`friend sync`, `friend clean`), or a replaced binary (`run`, `dashboard`) |

### What it does not prove

A landed card records a completed flow: the worker reports done, two different
readers pass that head, the tick (or the coordinator's `accept`) queues it for
merging, and the landing is reported. These are recorded judgments, not a
proof that the work is correct. A twin exercises that flow one command at a
time. It has no beats or ticks between commands, so it does not test fleet
timing, the `run` loop, `inbox --wait` or liveness. `finish` without `--head`,
then `merge`, records a landing without a push. The work table's cost column
is, per stream, the sum of its landed cards' total cost in US dollars — each
card's actual cost where one was priced, else its predicted one, `-` when
none was — so a total is a ledger of recorded spend, not a proof of it.

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
nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]   # create or upgrade schema config (or make the --file); --print lists the migrations and connects to nothing; --dry-run reads the ledger and prints each migration applied, pending or missing and each table of schema config the role does not own, applying none, and exits 1 when migrate would refuse (ready=no); the role that runs migrate must own every table in schema config, else migrate refuses before applying any and prints the ALTER TABLE ... OWNER TO lines
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
nova-config route add <name> --tier flash|pro|heavy --provider <p> --model <m> [--harness opencode|claude|codex|grok] --deadline <seconds> [--tokens <n>] [--usd <dollars>] [--enabled false] --as <friend>   # one way to run a model tier: the harness runs <provider>/<model>, (a headless --harness claude, codex or grok takes --provider subscription-<harness>) stopped at its token budget or its dollar budget (the harness's reported cost), whichever comes first; frontier cards escalate to the coordinator and are never dealt from routes
nova-config route set <name> --price_input <usd> --price_cache_read <usd> --price_cache_write <usd> --price_output <usd> [--reasoning_as_output false] [--long_context <tokens> --price_input_long <usd> --price_output_long <usd>] [--price_request <usd>] [--billing metered|plan] [--gateway_percent <pct>] [--price_source <text>] [--price_as_of YYYY-MM-DD] --as <friend>   # the route's price sheet, optional: USD per million tokens of each class, each a decimal kept exactly; a route with none prices no card
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
nova-redis serve  --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <u>[,<u>...]] # run redis-server in the foreground, loopback and tailnet only, AOF on, ACL users kept in <store-dir>/users.acl
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
