# Dogfood: nova-table — 2026-10-06, codex

One friend, one tool, cold. I read only `nova-table -h`, `nova-table help`,
every verb's `-h`, and its pages under docs/ (SPEC-NOVA-TABLE.md,
nova-table/README.md), then used every verb at least once with its real flags.
Run under opencode (model abliteration-ai/abliterated-model-large-v2). The
binary was built from this checkout at df30ce08 on the Linux bench, and every
store verb ran there against a throwaway `redis-server` (unix socket,
`--save ''`), started and stopped by me per the banner's own recipe; refusals
included. No go and no server ran on the working machine. About 30 minutes
of use.

## Findings

1. `nova-table row add demo fresh1 fresh1`

        ROW-ADD REFUSED: table "demo": ROW []; run: nova-table show 'demo'
        exit=1

    Expected: the README says "Duplicate members or row names within one list
    are refused", so I expected a refusal naming `fresh1` as the duplicated row
    name. The reason is the empty fragment `ROW []` — no row name, no rule —
    and the remedy (`show`) cannot reveal that the call repeated a name, so a
    cold reader cannot fix the call in one turn. The same fragment prints for
    `row add demo n1 n2 n1`. The equivalent member case is good
    (`duplicate manifest member (TWICE): member d1 appears more than once`),
    so the shape exists; this one message is broken. Grade: URGENT.

2. `nova-table show demo --json`

        SHOW REFUSED: unknown flag --json; the flags of show are --at-epoch, --redis; run: nova-table help show
        exit=2

    Expected: `--json`. The repository standard (AGENTS.md, "One shape across
    the set": "every verb accepts `--json`") says every verb does; in
    nova-table only `member read` and `batch` have it (`show`, `render`,
    `watch`, `list`, `check`, `member find`, `cell members` and `version` all
    refuse it as above). A programmatic reader of a table's whole state has no
    JSON rendering and must parse the text. Grade: NEXT.

3. `nova-table row add work build docs`

        TABLE ROWS ADD table=work rows=2 trips=1
        exit=0

    Expected: the README's verb table documents `row add` printing
    `TABLE ROW ADD table=<t> row=<r> cols=<n> bound=<n>`. The multi-row call
    prints an undocumented `TABLE ROWS ADD ... rows=<n>` line instead, with no
    `cols`, no `bound` and no per-row naming — a receipt parser loses exactly
    the `bound` count a binding mistake would show in. Grade: NEXT.

4. `nova-table row add t r1` (r1 already added with `--label "My label"`)

        TABLE ROW ADD table=t row=r1 cols=2 bound=0 trips=1
        exit=0

    Expected: the README says "a row already there keeps its place and its
    cells"; the banner says a second `row add` "rewrites the row and keeps its
    place". Neither sentence tells a cold reader that the rewrite silently
    resets the visible label to the row key (the render shows `r1` where it
    showed `My label`), which reads as data dropped by a verb documented as
    keeping the row. Grade: NEXT.

5. `nova-table cell add demo build done m1 b2` (both members already placed elsewhere)

        CELL-ADD REFUSED: table "demo" row "build" column "done" member "m1": member already has a place in this table: [build:ready m1]; run: nova-table show 'demo'
        exit=1

    Expected: both placed members named in one refusal. Only `m1` is; fixing
    it and re-running surfaces `b2` — two turns where the standard asks for
    every problem at once. `col del` does name all blocking members and builds
    one batch removal command, so the tool can. Grade: NEXT.

6. `nova-table -h` (the usage block)

          nova-table batch (<manifest-file> | - | '<json>')

    Expected: every other usage line in the banner names its flags inline
    (`watch ... [--every <duration>] [--out <file>] ...`); `batch`'s line
    names none of `--epoch`, `--actor`, `--receipt`, `--json`, `--dry-run`,
    which appear only in `help batch`. A stranger scanning the banner cannot
    tell `batch` has them. Grade: NEXT.

7. `nova-table show demo` (row `typed` binds column `ready` to `str:val`, a string key)

        nova-table show: warning: table "demo" row "typed" column "ready" cannot be read: key str:val is string, expected zset; run: nova-table set -h, and write the cell again
        nova-table show: 1 cell(s) printed as ? could not be read; run: nova-table check 'demo'
        exit=1

    Expected: the `?` rendering and the exit code are exactly as documented.
    But the cell is bound (`--owner` given at `row add`), and nova-table
    refuses writes to a bound cell naming its owner — so "write the cell
    again" is impossible here and `set -h` does not lead there. The remedy
    should point at the binding's owner verb or the key. Grade: NEXT.

## What worked (no finding, kept short)

The first-run `example:` block runs as printed, and the no-store refusal pastes
a whole throwaway store recipe that works. Every unknown verb, flag, table,
view, row, column and member is answered with the names there are and the
nearest guess; `-h`/`help` answer at 0 with no store. Epoch fencing behaves
exactly as documented (stale and ahead both refused naming both epochs, an
epoch-local row miss answered with `row add` at the live epoch, historical
`show`/`render --at-epoch` and `member read --at-epoch` read epoch 0 whole).
Bound cells render live counts, refuse `cell add` and `clear` naming the owner
verb; a binding to `table:*` storage is refused. `batch` commits atomically
(late invalid items left the store unchanged), replays its recorded receipt
marked `replay=yes`, guards revisions with precise refusals, flags manifest
type errors at their exact spot (`members[0].create.score`), takes stdin,
inline JSON and `--dry-run`, and props print as `TABLE PROP` lines. The `?`
rule (never a false 0) held, with `show` exit 1 as the spec says. Standing
sorts refuse `row move`/`row order` naming `row sort --manual`, and a row
added under a standing sort takes its place. `watch` published whole frames to
`--out` by rename, `--once` worked everywhere, and SIGINT/SIGTERM each ended a
clean watch run at exit 0 as documented. `row sort --desc`, `row order`,
`col move`, `set --hide/--show/--hidden/--visible/--rename/--columns`,
`drop --definition`, `view state` (including its 64-byte bound refusal) and
`shell` all did what their pages say.

The card's own mechanics: the named test `TestDocsTreeIsConsistent` does not
exist in ./internal/docs at this tip (`go test -run` reports `ok ... [no
tests to run]`); the gate lines below are the real check. The job's staged
worktree was retired by the daemon mid-run and re-staged from the local
mirror at the same tip (df30ce08); no clone was made.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.181s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	15.618s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.008s [no tests to run]

READ 8/10 — the banner answers what/how/use with a runnable example and a working throwaway-store recipe, and the spec page is precise and honest about limits; against that, one receipt shape is undocumented, batch's usage line hides its flags, and the primary read verbs have no JSON rendering.
USE 8/10 — every verb ran as documented with refusals that almost always name the exact cell, member, epoch or key and a remedy that runs, marred by the empty `ROW []` duplicate-row refusal and the silent label reset on a re-added row.

urgent=1 next=6
