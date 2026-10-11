# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (claude-opus-5-5), harness Claude Code
Build: 7e117276e198
READ: 8.5/10
USE: 8/10

## Reasons

The build is the sprint base tip 7e117276e198, built from source on a Linux
bench; no v1.2.0 tag exists on the remote yet, so the binary's version line
reads `nova-cairn v1.0.1-0.20261006142739-7e117276e198`. Read cold from
`nova-cairn help`, every verb's `-h`, `help <verb>` and docs/SPEC-CAIRN.md,
then used in a throwaway directory on that bench, nothing real touched.

READ 8.5. The banner answers what, how and the first run in eleven lines, and
the four example lines run as printed in one sitting. Every verb states its
effect (local write or inspection) and quotes the exit table. The spec is
short, normative and says plainly what the tool refuses to be (no seal, no
consume, no lifecycle). What keeps it from 10: the usage block still carries
a `nova-cairn NOTE:` line that reads as a verb; every verb's `-h` repeats the
whole tool's exit table instead of its own four lines; the flat record's entry
grammar (`## <rfc3339> — <entry>`) is in the spec but in no help text, though
`append -h` points the reader at flat records; and docs/CLI.md's nova-cairn
section still has no `### First run`.

USE 8. The first run and a second, real job both went end to end: a day's
notes with multi-line non-ascii text from `--file`, a stdin append, a repeat
append answering `duplicate=true` at exit 0 with the original stamp, other
words under the same id refused at exit 1 naming the `receipt --text` to run,
a re-open with another policy or source refused at exit 1 naming the matching
open. All refusals named every problem at once (`open` with no flags named all
three; a bad id, a bad `--now` and a bad policy came back together), an
unknown verb and an unknown flag got the nearest name, `--dry-run` wrote
nothing (the store directory stayed absent), `--max` cut with a `MORE` line
keeping the total, and `--json` gave one object with `result`, `why` and a
runnable `remedy`. Sixty paired concurrent appends of one new id with
different words gave one OK and one conflict every time, so the 1.1.0 race is
gone. What keeps it from 10: on a flat record, a section added by hand in any
heading form but the machine one is silently absorbed into the entry above it,
so `receipt --text` returns words that were never appended and a retry of the
exact original append becomes a conflict; `open` on a flat record answers
`OPEN OK publish=<whatever was asked>` though nothing is recorded and the next
append says `publish=unknown`; a re-open still prints a fresh clock stamp, so
it reads as a first open; and the "no such session" remedy chooses
`--publish manual` for the caller.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:244 | on a flat record, a hand-added `## e1` section after a tool entry n1 is read as part of n1's body: `receipt --entry n1 --text` returns `kept in my shape\n\n## e1\n\nhand entry` (bytes 16 to 35), the retry of the exact original append exits 1 as a conflict, and `receipt --entry e1` refuses; nothing warns | state the flat entry grammar in `append -h`, and have index and receipt print a NOTE naming any `## ` line inside a section body that is not the machine form | M |
| 2 | internal/cairn/cairn.go:513 | `open --publish manual` and then `open --publish never` on the same flat record both answer `OPEN OK publish=<the asked policy>` at exit 0, though a flat record stores no policy and the next append says `publish=unknown` | on a flat record print `publish=unknown` and a NOTE that the flat shape records no policy | S |
| 3 | cmd/nova-cairn/main.go:271 | a re-open that changes nothing prints `OPEN OK` with the call's fresh clock stamp and the line of a first open; the log gains no line, so the stamp printed is held nowhere | print the recorded open stamp from the log and `reopened=true` (or `duplicate=true`, the append's word) | S |
| 4 | internal/cairn/cairn.go:212 | `append` to a session nothing holds names `open first: nova-cairn open ... --publish manual`; the tool chose a policy the caller never gave, and the paste records it | leave `--publish <never|manual|deferred|immediate>` as the one placeholder, or carry the append's own `--publish` when given | S |
| 5 | cmd/nova-cairn/main.go:104 | the usage block still has a line beginning `nova-cairn NOTE:`, which reads as a verb to a reader enumerating the usage | move the note below the usage lines without the tool prefix | S |
| 6 | cmd/nova-cairn/main.go:92 | every verb's `-h` quotes the whole tool's exit table (all four verbs, nine lines), so `version -h` lists append's conflict code | give each verb its own `Verb.ExitTable` line | S |
| 7 | pkg/nsprint/verbflag/verbflag.go:140 | `open ... --bogus 1 --json` and `open ... stray --json` print the refusal as a prose line, not JSON; `--json` after an unknown flag or a positional is lost | scan the whole argument list for `--json` before choosing the refusal's rendering | S |
| 8 | pkg/tool/out.go:102 | the `MORE` line ends `--max <n> raises the ceiling, --max 0 lists all`, a placeholder and not a command to paste | print the caller's own command with `--max 0` | S |
| 9 | internal/cairn/read_existing.go:19 | `index` or `receipt` on a missing store refuses with remedy `nova-cairn help`, which does not tell the reader what to do next | name `open` as the verb that makes a store, or check the path and say whether a parent exists | S |
| 10 | cmd/nova-cairn/main.go:214 | `--now 2026-01-01T00:00:00+02:00` is accepted and stored as `2025-12-31T22:00:00Z`, while the flag and the refusal both say RFC 3339 UTC | say "RFC 3339, converted to UTC" in the help, or refuse a non-Z offset | S |
| 11 | pkg/tool/tool.go:222 | the unknown-verb refusal lists `open, append, index, receipt, version` and omits `help`, which the usage block lists | include help in the list | S |
| 12 | docs/CLI.md:2451 | the nova-cairn section has no `### First run` subsection, which onboarding point 3 asks for | add the block from the executed transcript in docs/TESTS.md | S |

## Good, keep

- Refusals name every problem in one run and end with a command that runs, including the whole `open` for an unopened session and the `receipt --text` for a conflict.
- Exact-text durability: duplicate at exit 0 with the original stamp, conflict at exit 1, multi-line non-ascii words back byte for byte; one id under concurrent appends now always gives one OK and one conflict.
- One result value, two renderings: `--json` carries `result`, `why` and `remedy` on ok, failed and refused alike; `--dry-run` writes nothing and says `persisted=false`.
- The id rule refuses `../x`, `CON` and whitespace before anything is written, with the whole rule in the line.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| two concurrent appends of one new id both win (READ 1.1.0, both raters) | FIXED | 60 paired appends of one new id with different words: one OK and one conflict every time, none both OK |
| no `--dry-run` on the write verbs (USE 1.1.0) | FIXED | `open --dry-run` leaves the store directory absent; `append --dry-run` says `persisted=false dry_run=true` |
| receipt text rendered with control escapes (USE 1.1.0) | FIXED | `text="the words to keep"` with spaces kept; newlines show as `\n` |
| conflict names no command (USE 1.1.0) | FIXED | the conflict ends `run: nova-cairn receipt --store ./cairns --session ... --entry morning --text` |
| flat entry grammar shown nowhere (USE 1.1.0) | STILL THERE | Finding 1; a hand section in another heading form is now shown to change an entry's words |
| `--json` lost after a stray positional (USE 1.1.0) | STILL THERE | Finding 7 |
| `nova-cairn NOTE:` line in the usage (USE 1.1.0) | STILL THERE | Finding 5 |
| re-open prints a fresh stamp and reads as a first open (READ and USE 1.1.0) | STILL THERE | Finding 3 |
| docs/CLI.md has no `### First run` for nova-cairn (READ 1.1.0) | STILL THERE | Finding 12 |
| README pinned to 1.0.0 (READ 1.1.0) | STILL THERE | README.md:54 still says "the Nova Tools 1.0.0 commands" |
