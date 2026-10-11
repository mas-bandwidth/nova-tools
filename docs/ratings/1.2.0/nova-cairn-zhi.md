# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is this worker's, not the friend's
Build: c7257c468a7a
READ: 8/10
USE: 8.5/10

No v1.2.0 tag exists yet. The build rated is the head of sprint/mechanical-2026-10-02, built and run on a Linux bench in a scratch directory: no live store, no server.

## Reasons

READ. The banner says what the tool is in one line, then five lines of how it works that carry the whole contract: plain files synced before OK, duplicate at exit 0, conflict at exit 1, and `--publish` recorded and never sent. The four examples run as printed, in one sitting. Every verb answers `-h` with its usage, its example, a sentence on the verb's one subtle rule (a re-open, a carried policy), its flags with `(required)` or a default, and an `effect:` line that is true: open and append write, index and receipt only read. The spec is short (172 lines), names its refusals, and pins each promise to a named test.

What costs the score. The usage block still has a line beginning `nova-cairn NOTE:` (cmd/nova-cairn/main.go:104), which reads as a verb and is refused as one. Placeholders drift between the usage line and the flag list: `--store <dir>`, `--session <id>` and `--now <rfc3339-utc>` in usage are all `<string>` under `flags:`, and `index -h` gives `--max` no default where the banner says 20 (pkg/tool/tool.go:668). Every verb's `-h` repeats the exit table of all four verbs. The flat-record grammar a reader needs to write a record by hand (`## <rfc3339> — <entry>`) is in the spec and docs/CLI.md, not on `append -h`. The spec and the command reference both promise that nothing appears beside a flat record (docs/SPEC-CAIRN.md:53, docs/CLI.md:2531), and an append leaves `.<session>.md.lock` there (internal/cairn/cairn.go:288). The command reference's cairn section still has no `### First run`, and its receipt example carries `[--text]` inside a sh fence, so it does not run as pasted (docs/CLI.md:2491). The spec opens with a paragraph answering a draft it never names (docs/SPEC-CAIRN.md:13), calls the caller "the friend" (docs/SPEC-CAIRN.md:4), promises a "coverage ledger" that the output prints as two counts, and keeps a 544-character line (docs/SPEC-CAIRN.md:88).

A 10 is a usage block of verbs only, one placeholder vocabulary from banner to flag list, each verb's own exit row on its `-h`, the flat heading on `append -h`, and docs that say what the flat store really leaves beside the file.

USE. The first run is the banner's examples verbatim and every line came out as stated: `OPEN OK`, `APPEND OK ... persisted=true published=false publish=manual duplicate=false`, `INDEX OK sessions=1 entries=1` with a session row and an entry row, `RECEIPT OK ... text="the words to keep"`. A second job ran end to end: words from a file with a newline and non-ASCII (bytes=29, read back exactly), words from stdin, `--source` carried to the receipt, a same-words retry answered `duplicate=true` with the original stamp, other words under one id refused at exit 1 naming the `receipt --text` that reads the holder, a re-open with another policy refused at exit 1 naming the matching `open`. Thirty concurrent appends of one id with different words gave one write and twenty-nine conflicts, and the stored words were the one write: the 1.1.0 race is fixed. Bad id, bad policy and bad `--now` were named together in one run; `--dry-run` on open left the store absent; `--max 1` printed `INDEX MORE ... total=3` and the uncapped first line. `--json` gave one object on every verb, with a `why` array on a refusal. A hand-written flat record took a dated append with no sidecar data, and index and receipt read it with `publish=unknown`.

What costs the score. A re-open prints the same `OPEN OK` as a first open, with the current clock as `stamp=` rather than the recorded open stamp (cmd/nova-cairn/main.go:271), so a retry cannot tell a new record from one that stood. An append to a session that does not exist names `--publish manual` in its remedy when the caller gave no policy (internal/cairn/cairn.go:213): the one place the tool guesses, and the guess is a policy. Under `--json` that refusal's `remedy` is `nova-cairn help` while the runnable command is buried in `why`. A missing store refuses with Go's `stat ./missing: no such file or directory` and `run: nova-cairn help` (internal/cairn/read_existing.go:19), not the `open` that creates it. Missing-flag refusals end `run: nova-cairn help`, unknown-flag refusals `run: nova-cairn open -h`. `--json` after an unknown flag is never parsed, so the refusal prints as lines. `append --publish never` on a duplicate answers `publish=manual duplicate=true` without saying the asked policy was not recorded. In a flat record a retry with trailing blank lines is `duplicate=true` where the nested store would call the same bytes a conflict; it is documented, but it is the one place the two shapes disagree on the same input. A hand-written `## e1` section is silently not an entry: `receipt --entry e1` says no such entry. `version` prints `v1.0.1-0.20261006145440-c7257c468a7a`, naming neither 1.1 nor 1.2.

Not tried: Windows device-name ids on Windows (refused on Linux by the rule), a filesystem without flock, and `--publish deferred` or `immediate` beyond recording, which the tool says it never acts on.

A 10 keeps this loop and these refusals, says `already=true` with the recorded stamp on a re-open, never suggests a policy the caller did not name, and puts the runnable remedy in the JSON `remedy` field on every refusal.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-cairn/main.go:271 | A re-open that changes nothing prints the same `OPEN OK` as a first open, with `stamp=` the current clock, not the recorded open stamp; a retry cannot tell whether a record was created or already stood. | Print `already=true` and the recorded stamp from `rec` on a matching re-open. | S |
| 2 | internal/cairn/cairn.go:213 | An append to a session nothing holds, with no `--publish`, names `open ... --publish manual` in its remedy: a policy the caller never chose, in a tool whose rule is refusing to guess. | Print `--publish <never\|manual\|deferred\|immediate>` when no valid policy was given, or name the four. | S |
| 3 | cmd/nova-cairn/main.go:250 | Under `--json`, the no-such-session refusal has `remedy: "nova-cairn help"` while the runnable `open` is inside the `why` string; a program cannot act on it without parsing prose. | Set `Remedy` to the open command on the NotFoundError, as index and receipt refusals already do. | S |
| 4 | internal/cairn/read_existing.go:19 | `index --store ./missing` refuses with Go's `stat ./missing: no such file or directory` and `run: nova-cairn help`. | Say `no store at ./missing` and name `nova-cairn open --store ./missing --session <id> --publish <policy>` as the remedy. | S |
| 5 | pkg/tool/tool.go:538 | Flag parsing stops at an unknown flag, so a `--json` after it is never seen and the refusal prints as lines (`open ... --bogus 1 --json`); before it, the same refusal is JSON. | Scan the arguments for `--json` before parsing, and render every refusal in the mode asked for. | S |
| 6 | cmd/nova-cairn/main.go:104 | The usage block carries a line `nova-cairn NOTE: --publish is a recorded word ...`; it reads as a verb, and `nova-cairn NOTE` is refused as an unknown verb. | Move the note under the usage block without the `nova-cairn ` prefix, or onto `open -h` and `append -h`. | S |
| 7 | internal/cairn/cairn.go:288 | A flat-record append leaves `.<session>.md.lock` beside the record, while docs/SPEC-CAIRN.md:53 and docs/CLI.md:2531 say nothing appears beside the file. | Say in both docs that a hidden `.<session>.md.lock` is left beside a flat record, or remove the lock file after unlock. | S |
| 8 | cmd/nova-cairn/main.go:183 | `-h` lists `--store <string>`, `--session <string>`, `--entry <string>`, `--now <string>` where the usage line says `<dir>`, `<id>` and `<rfc3339-utc>`. | Use the usage line's placeholders in the flag list. | S |
| 9 | pkg/tool/tool.go:668 | `index -h` shows `--max <int>` with no default; the banner and spec say default 20. | Print `(default 20)` on `--max`. | S |
| 10 | cmd/nova-cairn/main.go:92 | Every verb's `-h` prints the exit table of all four verbs; a reader of `receipt -h` reads append's conflict rule. | Quote only the verb's own row plus the shared `0 done, 2 usage` line. | S |
| 11 | `nova-cairn append -h` | The flat-record heading the tool reads (`## <rfc3339> — <entry>`) is not on `append -h`; a hand-written `## e1` section is silently not an entry, and `receipt --entry e1` says no such entry. | Name the heading form on `append -h`, and have index count headings it skipped as `unread=<n>`. | S |
| 12 | `nova-cairn append --publish never` | On a duplicate, `--publish never` answers `publish=manual duplicate=true` with no word that the asked policy was not recorded. | Add `asked=never` or refuse a duplicate whose `--publish` differs from the stored one. | S |
| 13 | internal/cairn/cairn.go:322 | In a flat record `kept\n\n` then `kept` under one id is `duplicate=true`; in the nested store the same two appends are a conflict. Documented, but the two shapes give different answers for one input. | Compare the exact bytes in both shapes and trim only for display, or print `trimmed=true` on the flat duplicate. | M |
| 14 | docs/CLI.md:2491 | The cairn section has no `### First run`, unlike its neighbours, and its receipt example carries `[--text]` inside a sh fence, so it does not run as pasted. | Add `### First run` with the banner's four lines, and drop the brackets from the fence. | S |
| 15 | docs/SPEC-CAIRN.md:13 | The spec opens with `This slice is a bounded contribution, not a ratification ... before any approval of that draft`, naming no draft; line 4 calls the caller `the friend`. | Cut the paragraph to the list of lifecycle verbs refused at exit 2, and say `the caller`. | S |
| 16 | docs/SPEC-CAIRN.md:101 | The spec says index builds a `coverage ledger`; the output is `INDEX OK sessions=<n> entries=<n>`, two counts. | Call it the coverage counts, as cmd/nova-cairn/main.go:334 does. | S |
| 17 | docs/SPEC-CAIRN.md:88 | One line is 544 characters, carrying the atomic-write implementation inside the append contract. | Wrap it, and move the atomicfile detail to the test list where case 20 already states it. | S |
| 18 | `nova-cairn version` | Built from the 1.2.0 candidate, `version` prints `v1.0.1-0.20261006145440-c7257c468a7a`, a pseudo-version naming neither 1.1 nor 1.2. | When unstamped, print `unreleased <sha12> (after <latest release>)`, or stamp from a VERSION file. | S |

## Good, keep

The banner's five how-it-works lines: a cold reader knows the duplicate and conflict rules and that `--publish` sends nothing before the first command.

Same-id appends are now serialised: thirty concurrent writers of one id with different words gave one write and twenty-nine exit-1 conflicts.

Every problem in one run, and refusals that end with the next command: a conflict names the `receipt --text` that reads the holder, a missing entry names the `index` that lists what is there.

`--json` is the same result as the lines on every verb, with `why` naming each problem on a refusal and `more` carrying the remedy when capped.

`--dry-run` on open and append makes every check and writes nothing, and says `persisted=false dry_run=true`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| same-id concurrent appends can overwrite (READ 1.1.0) | FIXED | 30 concurrent `append --entry x` with different words: exit codes `1 0` once and `29 1`; `receipt --text` reads the one write |
| the two store shapes store different bytes for one input (READ 1.1.0) | CHANGED | now stated in docs/SPEC-CAIRN.md:56; still `kept\n\n` then `kept` is `duplicate=true` flat and a conflict nested |
| the --publish words promise a timing never performed (READ 1.1.0) | CHANGED | the banner now says `--publish records your policy only: nothing is sent`; the NOTE line carrying it reads as a verb |
| a re-open looks like a first open (USE 1.1.0) | STILL THERE | a matching re-open prints `OPEN OK session=s1 ... stamp=2026-10-06T14:57:13.048291355Z`, the first said `stamp=2026-10-06T14:57:12.95875339Z`; no no-op word; another policy is now exit 1 |
| index cannot list sessions (USE 1.1.0) | FIXED | `index --store ./cairns` prints `INDEX SESSION session=s1 entries=1`, and an empty session lists as `INDEX SESSION session=s2 entries=0` |
| no --dry-run on the write verbs (USE 1.1.0) | FIXED | `open --store ./c2 ... --dry-run` prints `dry_run=true` and `./c2` does not exist after |
| receipt --text renders blanks as \x20 (USE 1.1.0) | FIXED | `receipt ... --text` prints `text="the words to keep"` and `text="line one\nline two  ünïcode\n"` |
| the flat entry grammar is not shown (USE 1.1.0) | STILL THERE | a hand-written `## e1` section: `receipt --entry e1` refuses `no such entry "e1"`; `append -h` does not name the heading |
| --json after a stray positional is lost (USE 1.1.0) | STILL THERE | `open ... --bogus 1 --json` prints `OPEN REFUSED: unknown flag --bogus` as a line |
| a NOTE line in the usage reads like a verb (USE 1.1.0) | STILL THERE | `nova-cairn help` line `nova-cairn NOTE: --publish is a recorded word ...` |
| CLI.md cairn section has no First run (READ 1.1.0) | STILL THERE | docs/CLI.md:2479 to 2542 has no `### First run` |
