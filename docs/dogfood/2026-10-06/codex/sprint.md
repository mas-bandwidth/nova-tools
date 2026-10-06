# Dogfood: nova-sprint — 2026-10-06, codex

One friend, one tool, cold. I read only `nova-sprint -h`, `nova-sprint help`,
every verb's `-h`, and its pages under docs/ (SPEC-SPRINT.md, CLI.md), then used
every verb at least once with its real flags against scratch twins (`mem:<file>`)
and temp dirs on a Linux bench, in nova-sprint built from this checkout at df30ce08
(plain `go build`, so the version line reads `devel`). Refusals included. Every
store verb ran on a twin; the two landings were real git on a scratch bare origin
(`land --stream s1 --repo-dir work --base sprint/s1` moved the base and pushed);
no server, no network, no live store. The friend family and `fsck seat` were
exercised through their refusals (they need a Postgres-backed nova-config).

## Findings

1. `nova-sprint selftest`

       SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=/tmp/nova-sprint-selftest-291141393
       exit=1

   Expected: `SELFTEST OK landed=1 ...` — the verb is the install gate ("a build
   installed for a fleet is gated by running the binary itself before it lands
   anything", SPEC-SPRINT.md, selftest), and this binary is good (its lander
   landed a real card two ways before I ran it). The push proof (2026-10-05)
   arms the gate for every name in the shipped binary (`pushArmedDefault = true`,
   cmd/nova-sprint/pushproof.go), and the selftest's canned card flow never proves
   the seat, so its own `add` step is refused. A stranger gating an install cannot
   tell a good binary from a broken lander. Grade: URGENT.

2. `nova-sprint selftest land --binary ../bin/nova-sprint`

       nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file /tmp/nova-sprint-selftest-458520628/canned-brief.md --redis mem:/tmp/nova-sprint-selftest-458520628/selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2
       exit=1

   Expected: `SELFTEST OK landed=1 land=... gate=...` — the second selftest verb
   is red on the same good binary, for a second reason: the add single-card rule
   (the BATCH EVERYTHING ruling; `add --count 1 --brief-file ...` without `--one`
   is refused) now refuses the very command line `selftest land` itself runs. Two
   selftest verbs red on one binary, each at its own step, is a wrong result from
   the tool's own install gate. Grade: URGENT.

3. `nova-sprint add --stream s1 --count 1 --one` — the second command of the
   card flow `nova-sprint help` prints under "trying it without a Redis" (after
   `export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss` and
   `nova-sprint init --readers reader-a,reader-b --members m1`)

       nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
       exit=2

   Expected: `ADD OK ...` — the help's own documented first-run flow, and the
   transcript of docs/TESTS.md "### First run" (which `cmd/nova-sprint/firstrun_test.go`
   runs line by line), show `add` and `start` succeeding right after `init`. In
   the shipped binary both refuse (every coordinator verb after the first init
   waits for a live push proof), so the whole card flow is dead at line 2. The
   remedy the line names is refused on the twin it sends you to (`seat install`:
   "the push loop waits on the sprint's machine, and the in-memory twin
   mem:sprint.twin has none"), and the other door it names, `inbox --wait --push
   seat`, is refused on a twin as well; the working escape — `seat push --harness
   <h> --target <dir>`, then `seat push --sent <nonce>`, then `seat pong <nonce>`
   — is in no help text a stranger reads. The unit transcript passes only because
   the test binary disarms the gate for names a test does not register
   (`pushArmedDefault = false`, cmd/nova-sprint/reexec_test.go), so the tested
   help and the shipped binary disagree. Help that lies, with a remedy that is
   itself refused. Grade: URGENT.

4. `nova-sprint start` — the same gate, fresh twin, seat unproven

       nova-sprint start REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
       exit=2

   Expected: a refusal the line names is exit 1 by every verb's own exit table
   ("0 done, 1 failed or incomplete (including refused; the line names why), 2
   usage or a store that did not answer"); the push gate's refusal exits 2, so a
   script keying on exit codes reads a usage error or a store that did not answer.
   The call itself was well formed and the store answered. Grade: NEXT.

5. `nova-sprint preflight --brief-dir bdir --repo-dir work2` (one brief in
   the directory, its PATHS and NEW naming the same file of a card already on the
   table)

       PREFLIGHT t1b FAIL PATHS: quacks/t1b.txt overlaps the card t1b (quacks/t1b.txt)
       PREFLIGHT t1b FAIL PATHS: quacks/t1b.txt overlaps the card t1b (quacks/t1b.txt)
       PREFLIGHT t1b FAIL PATHS: quacks/t1b.txt overlaps the card t1b (quacks/t1b.txt)

   Expected: the defect once — the line prints four times (the fourth is cut by
   the three-line form). `typedrec.CardPaths` returns the PATHS and the NEW
   entries without dedupe (internal/typedrec/cardpaths.go), and the live card's
   paths carry both too, so one overlap prints 2x2; a brief whose NEW file is
   also its PATHS entry — the normal shape of a card adding one file — doubles
   every overlap it has. The verdict and exit are right. Grade: NEXT.

6. `nova-sprint add --stream s1 --brief-file t1.md --one` (REPO: origin.git, a
   bare repository beside the brief), then the remedy it names

       LINT DRIFT card=t1 check=paths-at-base line=9: MISSING: REPO origin.git is no repository a clone can be made of, so the brief was not read at its base remedy=... hand the lint a repository holding the sha with `--repo <dir>`
       nova-sprint add REFUSED: 2 brief finding(s) at the base, the first paths-at-base: ...; run: nova-sprint add -h
       exit=2

   `$ nova-sprint add --stream s1 --brief-file t1.md --one --repo work2`:

       nova-sprint add REFUSED: unknown flag --repo; the flags of add are --actor, --after, --allow-personal-base, --allow-shared-paths, --before, --brief, --brief-dir, --brief-file, --brief-op, --count, --decide-record, --epoch, --held, --json, --max, --needs and 10 more; run: nova-sprint help add
       exit=2

   Expected: a remedy that names a flag the verb takes. The lint's remedy points
   at `--repo <dir>`, which `add` does not have (it is `preflight --repo-dir`'s
   and `nova-swarm lint`'s), and the finding does not say what REPO wants — an
   absolute path or a clone URL; the same bare repository named by its absolute
   path passes where the relative `REPO: origin.git` is "no repository a clone
   can be made of". Grade: NEXT.

7. `nova-sprint progress --as m2 t1b@1 --epoch 0` (the short id; the work card
   t1b.w1 sits ready on m2's row)

       REFUSED t1b: not working (it is kept, -)
       PROGRESS FAILED moved=0 refused=1 notes=0
       exit=1

   Expected: the card's true place, as the full id names it: `progress --as m2
   t1b.w1@1 --epoch 0` answers "REFUSED t1b.w1: not working (it is m2:ready)",
   and `queue --as m2` shows t1b.w1 ready on m2. The short form resolves to a
   record that is not placed and reports "kept, -" (the shape `placeWord` gives
   an off-table card, internal/sprint/steps_work.go), sending the reader to the
   log for a card that is on the table. Grade: NEXT.

8. `nova-sprint promote --repo-dir work2 --base sprint/s1 --dry-run` (the
   clone holds sprint/s1 only as a remote-tracking branch)

       nova-sprint promote: git rev-parse: ; run: nova-sprint promote --dry-run
       exit=1

   Expected: the missing ref named. The failure prints an empty git error (the
   rev-parse runs with `--verify --quiet`, cmd/nova-sprint/promote.go, so git's
   own message is suppressed) and its remedy restates the command that just
   failed; a stranger learns neither which ref nor which repository was short. A
   local branch `sprint/s1` in the clone makes the same call print `PROMOTE NONE
   live=sprint/s1 tip=...` at exit 0. Grade: NEXT.

9. `nova-sprint take --as m1 --epoch 0` (the packet's report-it line), then
   `nova-sprint finish -h`

         report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
       usage: nova-sprint finish --as <member> <card>@<gen>... --epoch <n> (--head <commit> | --failed) [--report <text>] [--usage <text>]

   Expected: the usage line names every flag the verb takes. The flags list of
   `finish -h` carries `--branch` and `--base`, and the packet every worker is
   handed prints `--branch` in its report-it line, but the usage line (the one
   a stranger reads first) omits both. Grade: NEXT.

10. `nova-sprint where --release` (no stream carries a release)

        (no output at all; exit 0)

    Expected: a line — the named form answers `RELEASE v1.1.0 cards=0` when
    nothing matches, and after `stream set s1 --release v1.1.0` the bare form
    answers `RELEASE v1.1.0 cards=4`; with no release set anywhere it prints
    zero bytes, so a stranger cannot tell the read ran from a hung one. Grade:
    NEXT.

11. `nova-sprint friend sync`

        nova-sprint friend sync: the config cannot be read: --pg is required: postgres://user@host:5432/nova (or NOVA_PG_DSN); run: nova-config friend list; nothing was changed
        exit=3

    Expected: the refusal is right and well worded, but every verb of the friend
    family (sync, beat, cards, health, take, down, up, level, reconcile), and
    `collect` and `fsck seat` with them, needs a Postgres-backed nova-config, so
    the twin the help tells a stranger to learn on ("trying it without a Redis")
    cannot exercise a fifth of the tool's verbs at all; the friends table stays
    empty and every friend verb refuses at the door. Grade: NEXT.

12. `nova-sprint card t1` (no card t1 on the table)

        nova-sprint card: no primary t1; run: nova-sprint where
        exit=1

    Expected: the status word the banner's grammar promises leads the line. The
    empty-read answers of `card`, `friend beat`, `friend cards` and the rest
    print `<tool> <verb>: <why>` with no OK/REFUSED token, and exit 1, where
    CLI-STYLE.md reads "one line per event, `<TOKEN> OK|FAILED key=value ...`"
    and "a missing INPUT ... is a refusal, exit 2". The lines themselves say
    exactly the right thing; the shape is the deviation. Grade: NEXT.

## What worked (no finding, kept short)

Every verb answered `-h` at 0; `help` and `-h` are byte-identical; the bare
command names every verb in one line at exit 2; `--json` answered on every read
I tried. The full documented card flow ran for real end to end once the seat was
proven — two real git landings onto a scratch origin, merge batches, CI records,
stats and the route table, the archive machinery, snapshots verified against a
twin (`SNAPSHOT OK ... verified=checksum+twin`), a backup restored and compared
(`BACKUP OK ... restored=twin compared=document+counts secrets=none`), clear and
teardown with their doors named after. The judgment inbox is the best thing in
the tool: every judgment arrives with the exact commands that answer it, `wait`,
`ack` and the aliases work as printed, and the refusals almost always name what
the input wants in one turn (`read --broken` teaching the finding grammar,
`drop` naming the `--one` door, `stream remove` naming the stop). The card lint
refused a brief of mine with every missing rule quoted verbatim and one remedy
that worked first try. `play --simulation` drove the world honestly and its ticks
matched what the verbs had done by hand.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.503s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	16.100s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.011s [no tests to run]

The named test `TestDocsTreeIsConsistent` does not exist in ./internal/docs at
this tip, so the run answers "no tests to run" and exits 0; the packages the
card names are green with this report in the tree, on a Linux bench.

READ 7/10 — the banner, the verb helps and the judgment grammar answer a cold
reader fast and truly, but at this tip the tool's own install gate and its
documented first-run flow are red on a good binary, and three of the frictions
above are remedies that name a door the tool then refuses.

USE 8/10 — every verb ran for real including the refusals, the lander landed
twice on real git, and the one-value grammar with one-turn remedies is what a
coordinator's tool should be, marred by the push-proof gate refusing the
selftests, the walkthrough and handovers on a twin until an escape the help never
names is found by hand.

urgent=3 next=9
