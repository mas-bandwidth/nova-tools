# Dogfood: nova-check — 2026-10-06, grok

Read cold as a stranger: only `nova-check -h`, `nova-check help`, every verb's
`-h`, and the page under `docs/` (`docs/CLI.md`'s `## nova-check` section and
`docs/SPEC-CHECK.md`). Built from the checkout at cb5fb8d4c as
`nova-check devel linux/amd64 go1.26.6`, and used over a scratch tree: every
verb at least once with its real flags, the refusals too. `convergence` is the
one verb that reaches a forge, so its forge reads were driven through a fake
`gh` that prints `[]` (no network), its git read through a scratch checkout,
and its other streams through a `bin/` of scripts, a versions/certs roll-up, a
retired-scripts README and a receipts directory; the two-tick streak was driven
with `--state` and `--now`.

## Findings

1. `nova-check version --version`

       nova-check version REFUSED: takes no flags and no arguments except --json: unknown flag --version; the flags of version are --json; run: nova-check help
       exit=2

   Expected: the build line, because `nova-check help` and `nova-check version
   -h` both print `nova-check version [--json] print this build identity
   (--version also accepted)`. The accepted spelling is a bare `nova-check
   --version`, which neither page shows; the verb help's `flags:` list names
   `--json` alone, so a reader who types the advertised spelling is turned
   away. Grade: URGENT.

2. `nova-check spelling --path 'no-such-pattern-*.md'`

       SPELLING OK files=0 misspellings=0 excluded=0
       exit=0

   Expected: the refusal a missing named path gets. `nova-check spelling --file
   missing.md` and `nova-check spelling --dir nope` each exit 2 naming the
   path; a `--path` glob that matches nothing instead reports a clean tree at
   exit 0, so a typo in a release gate's pattern passes green while checking no
   file. Grade: URGENT.

3. `nova-check convergence --repo owner/name --ledger ledger.md --receipts receipts --retired retired.md --since 24h --gh ./fake-gh --certs badcerts.tsv`
   (badcerts.tsv header `wrong<TAB>header`)

       CONVERGENCE LANDING now=- before=- ratio=- trend=absent measure=rounds-per-batch source=--repo\x20(no\x20integration\x20batch\x20in\x20the\x20window\x20carried\x20a\x20round\x20count) batches=0 per-hour=0 prev-batches=0 prev-per-hour=0 rounds-read=0
       ...
       CONVERGENCE WARN streams=3 contracting=0 widening=EDGES absent=LANDING,CLASSES,SCRIPTS,FLEET
       exit=0

   Expected: exit 2 naming the file and the header it wanted, as SPEC-CHECK.md's
   refusal 14 says and as the same run does when `--versions versions.tsv` is
   passed as well (`REFUSED: badcerts.tsv has a header this verb does not read
   (wrong<TAB>header); it reads name<TAB>status`). With no `--versions`, the bad
   `--certs` is silently dropped and the reading stays green. Grade: URGENT.

4. `nova-check convergence --repo owner/name --ledger ledger.md --receipts receipts --retired retired.md --since 24h --bin bin --repo-dir classes --versions versions.tsv --certs certs.tsv --state state.json --dry-run --timeout 10`

       nova-check convergence REFUSED: gh pr list --state open: exit status 4: To get started with GitHub CLI, please run:  gh auth login\x0aAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.; run: nova-check help
       exit=2

   Expected: the streams whose sources were named and readable (SCRIPTS, EDGES,
   FLEET, LEDGER) to print, with PRS and LANDING ABSENT or FAILED the way an
   unreadable source is named elsewhere, or a remedy that names the forge
   prerequisite; the printed door is `nova-check help`, which never names `gh
   auth login` or `GH_TOKEN`, and the forge's own words are folded into one line
   with literal `\x0a`. A reader with a working tree and no forge login gets no
   convergence reading at all. Grade: NEXT.

5. `nova-check convergence --repo owner/name --ledger ledger.md --receipts receipts --retired retired.md --since 24h --bin bin --repo-dir classes --versions versions.tsv --certs certs.tsv --gh ./fake-gh --timeout 10`

       nova-check convergence REFUSED: no commit in classes at or before 2026-10-05T20:54:40Z; the checkout does not reach back that far; run: nova-check help
       exit=2

   Expected: CLASSES ABSENT (as it is when `--repo-dir` is not given) or a
   FAILED stream, not a refusal of all seven streams. A fresh, shallow or
   one-commit checkout of the named repository cannot produce a reading, and the
   one line that says why is about a single stream. Grade: NEXT.

6. `nova-check convergence --repo owner/name --ledger ledger.md --receipts receipts --retired retired.md --since 24h --gh ./fake-gh --versions versions.tsv --certs certs_ok.tsv`
   (certs_ok.tsv rows `m1 ok`, `m2 valid`, `m3 certified`)

       CONVERGENCE FLEET now=1 before=- ratio=- trend=flat measure=units-off-the-one-build units=3 stamps=2 build=2026-10-06 certified=1/3
       exit=0

   Expected: the help and the page to say which `status` words certify. The
   header `name<TAB>status` is documented, the vocabulary is not; `valid` and
   `PASS` are counted as not certified (a certs file of `m1 valid` and `m2
   expired` prints `certified=0/2`), and an unrecognised status is never named.
   `ok` is the word that counts. Grade: NEXT.

7. `nova-check floors --core floors/SEED-CORE.md --source floors/SEED.md`

       FLOORS FAILED floors/SEED-CORE.md: the door's numbered list: the floor "the never-delegate list" is missing (registry-pinned; if the floor set itself has legitimately changed, internal/check/floors.go and SPEC.md change in the same commit)
       FLOORS FAILED floors/SEED-CORE.md: the door's numbered list: "the never delegate list" is not a floor this check knows (registry-pinned; if the floor set itself has legitimately changed, internal/check/floors.go and SPEC.md change in the same commit)
       exit=1

   Expected: a remedy a stranger with their own self repo can act on. The banner
   says only that the door's floor set matches the seed's; no example pair and no
   list of the eight pinned titles is shipped or linked, and the refusal's
   remedy names repository-internal files (`internal/check/floors.go`,
   `SPEC.md`) the reader does not have. Grade: NEXT.

8. `nova-check links --dir self --file nope.md`

       LINKS FAILED nope.md: unreadable (no such file or directory)
       LINKS FAILED files=1 links=0 broken=1 shown=1 excluded=0
       exit=1

   Expected: a named path that does not exist is an invocation the tool cannot
   run, as `nova-check links --dir nope` (exit 2) and `nova-check spelling
   --file missing.md` (exit 2) both report; instead it is counted as `broken=1`
   and exit 1, so a caller reads a broken link where nothing was read. Grade:
   NEXT.

9. `nova-check quickstart --dir nope`

       QUICKSTART RUN dir=nope checks=2: links, then nocode
       nova-check links REFUSED: dir "nope": lstat nope: no such file or directory; run: nova-check help
       nova-check nocode REFUSED: dir "nope": lstat nope: no such file or directory; run: nova-check help
       QUICKSTART FAILED checks=2 failed=links,nocode worst-exit=2 next=nova-check links --dir nope (fix what it names, then run quickstart again)
       exit=2

   Expected: the `next=` line to name a step that can succeed — create the
   directory, or check the path — or at least to name both refused checks;
   pasted as printed it reproduces the refusal, and `failed=links,nocode`
   already knows there were two. Grade: NEXT.

10. `nova-check spelling --path 'self/**/*.md'`

        SPELLING FAILED self/docs/note.md:3:10: seperate -> separate
        SPELLING FAILED self/docs/note.md:3:31: recieve -> receive
        SPELLING FAILED files=3 misspellings=2 shown=2
        exit=1

    Expected: `self/README.md` is a markdown file under `self/` and `--dir self`
    counts `files=4`; the same tree read through `--path self/**/*.md` drops the
    top-level file with no note, because `**` here needs a directory. The count
    line gives the number but not the reach, so a gate can check fewer files
    than its author meant. Grade: NEXT.

## What worked

Every verb answers `-h` at exit 0 before reading anything, and `nova-check help
<verb>` prints the same text. A bare `nova-check` and an unknown verb or flag
each refuse in one line and name every verb or flag, with a nearest guess
(`frobnicate` lists the verbs, `links --nope` lists the flags of `links`,
`-version` answers `did you mean version?`). Missing required flags are named
all at once with what each wants and its role (`attest` with none names both
`--home` and `--manifest`; `kernel` with neither budget names `--file` and the
unit choice in one run; `convergence` with none names all five). `spelling
--write --dry-run` prints every fix and writes nothing, then `--write` applies
the same fixes and the count line says `written=1`. The `dogfood` loop ran end
to end: a `--not-ok` receipt made an open edge, `gate` printed its id and the
two ways to close it, `--require-all` added the non-author line, and a
`--dry-run` record wrote no file. `convergence` printed a real reading from the
local sources (PRS, EDGES, LEDGER, FLEET) with `absent=` naming LANDING,
CLASSES and SCRIPTS and the flag each wanted; the two-tick rule held, with two
runs at one `--now` staying at exit 0 and a later tick exiting 1.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.022s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.178s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.020s [no tests to run]

The card's named test `TestDocsTreeIsConsistent` does not exist in
./internal/docs at this tip, so the named-test run passes with nothing to run;
the docs package's own walk (terminology, links, maps, transcripts) is the
check this report's file answers, and both packages are green.

READ 7/10 — the banner answers what the tool does, how it works and where it
starts, every verb's effect and exit table are on its `-h`, and the refusal
grammar names every missing input at once; the `version` line advertises a
spelling the verb refuses, `floors` ships no example of the two files it wants,
and the cert status vocabulary is undocumented.

USE 7/10 — every verb ran for real against a scratch tree, the refusals are one
line with a next step, and `spelling --write`, `dogfood record` and
`convergence --state` all did what their help says; `convergence` is unusable
without a forge login, a bad `--certs` header is a silent pass, and an unmatched
`spelling --path` is a false green.

urgent=3 next=7
