# nova-sprint USE rating, nova-tools 1.1.0

Rater: a cold rater on the flash tier, judging the help and the twin alone
Build: bd7949b97aec
Score: 8.5/10

## Reasons
The help carries the whole tool: an overview with a words glossary, a per-verb
help with usage, every flag explained, examples and exit codes, a worked twin
example that ran as printed, and a judgment table whose lines are the commands
to copy. The mem twin makes every verb runnable with no service, so both jobs
ran end to end: a one-card flow through git to DONE (init, add, start, tick,
take, finish, read, land by merge, 1/1 landed), then a two-stream sprint with
briefs, a sentinel and a release to DONE 4/4. Refusals name the problem and the
exact next command every time (add without --stream says "run: nova-sprint add
-h"; an unknown flag lists the flags init has; land --dry-run on a headless
card names the return, rework and resume sequence). --json is on the store
verbs and is actionable: take --json carries packets with branch names, queue
--json carries the read packet, where --json carries the tables and held counts.
A 10 needs: log --max doing what its own flag help says, usage refusals that
report every problem at once and honor --json, version refusing unknown flags,
add's usage line saying a brief must pass the card lint, and the verbs that
need a service (run, the dashboard, the PG syncs, a real land push) tried and
not only read: their help and dry-runs are good, the runs stayed untried here.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint log --max 2` | the flag is ignored: LOG OK lines=50 of=50, all 50 lines printed, while log's own flag help says --max is "listed items of each kind; 0 is all"; a coordinator trimming a long log gets no trim and no refusal | cap each kind of listed line at --max, or refuse --max on log with the reason | M |
| 2 | `nova-sprint add --stream s1 --count 2 --brief-file brief.txt` | the usage line offers --brief and --brief-file with no hint that a brief must be a full card quoting the rule set, so a plain task text is refused by the card lint with 6 findings (the same refusal met on brief --brief); the remedy is only in the help's example paragraph and in the refusal's last line | say on the usage line of add and brief that the text must pass the card lint and name the template command there too | S |
| 3 | `nova-sprint take --json --epoch abc` | the refusal ignores --json: the line is text, exit 2, no JSON object, so a program driving the tool cannot parse the refusals it asked to have as JSON | print the refusal as a JSON object with the reason and the next command when --json is given | S |
| 4 | `nova-sprint init --bogus --alsobogus` | the refusal names only the first problem: --alsobogus is unnamed, so a second round is spent learning it | name every unknown flag and bad value in one refusal | S |
| 5 | `nova-sprint version --bogus` | the unknown flag is accepted in silence: the version prints, exit 0, where every other verb refuses an unknown flag by name | refuse unknown flags on version like every other verb does | S |
| 6 | `nova-sprint take --as m1 --epoch 0` | the packet's base line reads "base: the stream's base" while the brief names BASE: main, so a worker must open card to learn the branch it starts from | print the base branch the brief names, keeping the glossary phrase for when no BASE: is given | S |

## Good, keep
The mem twin with the tick left to the caller: the whole card flow is learnable
by doing, with no service, exactly as the help's example prints it.
Refusals that end in "run: <the exact command>", naming every flag or verb
available: each of the four provoked refusals said what to type next.
The inbox's judgments printing their decisions as filled-in commands with the
group id and --expect size ready to copy.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| log --max does nothing | STILL THERE | `nova-sprint log --max 2` exits 0 with LOG OK lines=50 of=50 and all 50 lines printed |
| play on a twin reports OK and moves nothing | FIXED | `nova-sprint play --ticks 2` on the running twin exits 0 and its reads move cards: READ OK moved=1 twice for s1-2.r1.reader-a, asked then reading then ok |
| usage refusals ignore --json | STILL THERE | `nova-sprint take --json --epoch abc` prints the refusal as text, exit 2, no JSON object |
| version silently accepts unknown options | STILL THERE | `nova-sprint version --bogus` exits 0 and prints the version line |
| the help's example block does not run as printed | CHANGED | the block now says brief.txt is a card from nova-swarm template --name card and that run ticks in a shell of its own, and the twin's refusals of run, where --watch and inbox --wait are stated in the help and named when refused |
