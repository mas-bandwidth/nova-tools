# nova-sprint USE rating, current baseline 0c5803c2de40

Rater: z-ai/glm-5.3-flash, a cold USE rater with no memory of this tool's code, its authors' reasoning or its earlier ratings
Build: 0c5803c2de40
Score: 8/10

## Reasons
Rated from a build of exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c (the staged tree had advanced past it, so the build ran in an isolated export of that commit; `nova-sprint version` prints `devel`, so the score leans on this provenance, not the binary's self-description). Every command ran in one scratch directory on the `--redis mem:<file>` twin the help advertises, one command at a time, with no store, remote, fleet or model touched. Three jobs went end to end: the help's own first-run script (a bare git stand-in for the forge, one card dealt, taken, finished, read by two readers, merged and landed, sprint DONE); a second job with a sentinel, two brief-file cards, a needs-ordered card, a failed finish, the inbox judgment, a rework, and the no-git merge stand-in; and a `quack --streams q1 --count 1` self-test that proved the whole chain in 9 seconds. The help's first-run script works exactly as printed, reads included: `read --as reader-a --begin` moves the asked card without a `queue` first (my own first run failed only because I took one tick early, and every refusal taught the missing step). Refusals are the tool's best habit: an unknown flag lists the verb's real flags, a bad value names what it wants, an unknown verb lists every verb, the brief lint lists all six missing rules with remedies, inbox judgments print runnable decisions that worked copied verbatim, and a stale generation names the live one. `--json` is real JSON on the store verbs (`refused`, `says`, and the goals `where --json` carries exactly as the help promises). It is not a 10 because the store still wastes an AI's trust in small ways: `log --max` is documented and then ignored (the whole log comes back whatever you ask), `version` silently accepts unknown options, usage refusals ignore `--json` after the help advertises it on every verb, one word ("working") means two states in adjacent lines and costs a failed `finish` to decode, and a `take` that takes nothing prints OK. A 10 would honor `--max` in `log`, refuse unknown flags on `version`, print usage refusals as the same JSON object state refusals use, say "dealt" where it now says "working", and make a no-op `take` unmistakable.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint log --max 1` | the help documents --max as "listed items of each kind; 0 is all", yet the output is 48 lines, byte-identical to `nova-sprint log` and to `--max 2`, in --json too; an AI asking for the last three events gets the whole log and must cut it itself | honor --max in log's line listing, cutting each kind to the last n | M |
| 2 | `nova-sprint version --bogus` | exits 0 and prints the version; every other verb refuses an unknown flag at exit 2, so a mistyped option here is a silent false OK | refuse unknown flags the way the store verbs do, naming the flags version takes | S |
| 3 | `nova-sprint init --bogus 1 --json` | usage refusals print prose on stderr and ignore --json, while state refusals honor it (`finish --json` prints a refused array); a program asking for JSON cannot read the refusal | print the usage refusal as the same JSON object the state refusals use | S |
| 4 | `nova-sprint finish --as m1 s2-1.w1@1 --epoch 0` | the tick's MOVED line says the deal moved the card "ready -> working", then this finish is refused "not working (it is m1:ready)": the dealt-but-untaken work card is ready in the fleet table while the primary shows working; one word, two meanings, decoded only after a failed finish | say "dealt" in the deal line, or have the refusal name `nova-sprint take --as m1 --epoch 0` as the next command | S |
| 5 | `nova-sprint take --as m1 --max 1 --epoch 0` | on an empty queue it prints TAKE OK moved=0 with the note "m1 took 0 of the 1 asked"; a script reads success where nothing moved, twice in a row following the first-run script too early | keep the OK but lead the line with "MOVED nothing" or refuse when the queue is empty and cards were asked | S |
| 6 | `nova-sprint land --stream s2 --repo-dir work --base main` | cards whose brief names REPO: are refused when the clone's origin differs; the remedy asks for a clone of https://github.com/local/scratch.git that cannot exist in a scratch run, and the brief lint warns nothing at add time | have the lint or the refusal say a brief's REPO: may name the clone's own origin for a local run | S |

## Good, keep
Refusals everywhere name the problem and hand over the next command: unknown flags list the verb's flags, the brief lint lists every missing rule with its remedy, inbox judgments print runnable decisions that worked copied verbatim, and the stale-generation refusal names the live generation.
The mem: twin runs the whole advertised flow offline: init, sentinel, release, deal, take, finish, failed judgment, rework, two reads, accept, land, DONE; the machine-only verbs (run, where --watch, inbox --wait) refuse with exact remedies instead of hanging.
--json is actable without guessing: where --json carries the goals the help promises, card --json holds the whole primary with its timeline, and take --json carries the notes as says.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| USE 2026-10-02 at 1aac13259: log --max does nothing | STILL THERE | `nova-sprint log` and `nova-sprint log --max 1` and `--max 2` each print the same 48 lines |
| USE 2026-10-02 at 1aac13259: play on a twin reports OK and moves nothing | FIXED | `nova-sprint play --simulation --ticks 2` ran merge --stream s2 --batch 100, MERGE OK moved=1; where then counts s2-2 landed |
| USE 2026-10-02 at 1aac13259: usage refusals ignore --json | STILL THERE | `nova-sprint init --bogus 1 --json` prints the prose refusal on stderr at exit 2 |
| USE 2026-10-02 at 1aac13259: version silently accepts unknown options | STILL THERE | `nova-sprint version --bogus` exits 0 printing the version line |
