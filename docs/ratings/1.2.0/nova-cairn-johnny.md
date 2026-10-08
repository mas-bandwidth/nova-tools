# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 361d4451bcd8
READ: 8/10
USE: 8/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-cairn version` prints `nova-cairn v1.0.1-0.20261006144651-361d4451bcd8 linux/amd64 go1.26.6`. Built and run on a Linux bench machine, in a scratch directory made for the trial; no live store, no server, nothing sent anywhere. Both store shapes were used: the nested shape the tool makes, and a flat `<store>/<session>.md` written by hand first.

## Reasons

READ. The banner says what the tool is in one line, then the five facts a caller needs before the first command: the store is a directory you name, plain files synced before OK, same id and same words is a duplicate at exit 0, same id and other words is a conflict at exit 1, and `--publish` is recorded and never acted on. The four examples are one sitting and say so. Every verb answers `-h` and `help <verb>` with the same text (they diff equal): its usage line, its example, flags with a required marker and a default, the exit codes and an `effect:` line that is true of the binary (open and append write; index, receipt and version write nothing). The spec states what the tool deliberately does not do, and its numbered test list names each case.

What keeps READ at 8. The usage block still carries a line that begins `nova-cairn NOTE:` (cmd/nova-cairn/main.go:104), so a reader listing verbs from the usage meets a verb that `nova-cairn NOTE` refuses; this was found at 1.1.0. The one heading a flat record must carry to be read as an entry, `## <rfc3339> — <entry>`, is written down only in a code comment (internal/cairn/cairn.go:220); `append -h` names the flat record and not its grammar. Every verb's `-h` prints the exit codes of all four verbs, so `version -h` explains conflicts. `--now` says "RFC 3339 UTC" and accepts any offset. The spec says nothing appears beside a flat record (docs/SPEC-CAIRN.md:53) and a flat append leaves `.<session>.md.lock` there. The README still calls these the 1.0.0 commands (README.md:54) and docs/CLI.md's nova-cairn section still has no `### First run`, both carried from 1.1.0.

USE. The help's first run worked as printed: `OPEN OK`, `APPEND OK ... persisted=true published=false publish=manual duplicate=false`, `INDEX OK sessions=1 entries=1`, and `RECEIPT OK ... text="the words to keep"`, each exit 0. A second job ran end to end: open with `--source`, append from a file and from stdin with the source carried, a repeat append `duplicate=true` with the first stamp, other words under the same id exit 1 naming the `receipt --text` that reads the holder, a re-open with another policy or source exit 1 naming the open that matches. `receipt --text` gave back `"line one\nline two é\n\n"`, 22 bytes, exact. A bad session id, a Windows device name as entry id, a bad policy and a bad `--now` were named together in one run, exit 2. `--dry-run` said `persisted=false dry_run=true` and wrote nothing. `--max 2` printed one `INDEX MORE kind=entry shown=2 total=4` line with its remedy, and 25 sessions printed `INDEX MORE kind=session shown=20 total=25`. `--json` gave one object on ok, failed and refused alike. Twenty concurrent appends over eleven ids in one session gave eleven entries, nine exit 1 conflicts and no lost words: the 1.1.0 race is gone.

What keeps USE at 8. A hand-written flat record with an `## e1` heading opened fine and then indexed `entries=0`; `receipt --entry e1` said "no such entry" with no word about the heading it did not read. An append to a session nowhere puts the open command in the prose but sets the JSON `remedy` to `nova-cairn help` (cmd/nova-cairn/main.go:250), and that open command picks `--publish manual` for the caller (internal/cairn/cairn.go:213) in a tool that refuses to guess. A matching re-open prints `OPEN OK` with a fresh stamp (cmd/nova-cairn/main.go:271), so it reads as a first open; append says `duplicate=true` in the same case. Most refusals end `run: nova-cairn help` where the flag refusals end `run: nova-cairn <verb> -h`. A flag error stops parsing, so `--json` after an unknown flag prints prose, carried from 1.1.0. `--now 2020-01-01T00:00:00Z` files a real entry with that stamp and nothing on the entry says it was replayed.

A 10 would take the NOTE line out of the usage block, print the flat heading grammar in `append -h` and say in index how many `##` headings were not entries, put the open command in the `remedy` field and leave the policy as `<policy>`, mark a re-open `existing=true` with the stored stamp, use one remedy grammar, keep `--json` whatever comes before it, and make the spec and help say what the lock file and `--now` really do.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-cairn/main.go:104 | The usage block has a line `nova-cairn NOTE: --publish is a recorded word ...`; `nova-cairn NOTE` is refused as an unknown verb. Carried from 1.1.0. | Print the note under the usage block without the `nova-cairn ` prefix. | S |
| 2 | `nova-cairn index --store ./flat` | A flat record `flat/s9.md` with a hand-written `## e1` heading indexes `sessions=1 entries=0`, and `receipt --entry e1` is `no such entry`. The heading grammar is only in a comment (internal/cairn/cairn.go:220). Carried from 1.1.0. | Print `## <rfc3339> — <entry>` in `append -h`; have index print `skipped_headings=<n>` for `##` lines it did not read as entries. | S |
| 3 | `nova-cairn append --store ./flat --session s9 --entry e2 --text two` | Leaves `.s9.md.lock` in the store beside `s9.md` (internal/cairn/cairn.go:290); docs/SPEC-CAIRN.md:53 says nothing appears beside the file. | Name the lock file in the spec and in `append -h`, or remove it after the append. | S |
| 4 | `nova-cairn append --store ./cairns --session zz --entry e1 --text x --json` | `"remedy":"nova-cairn help"`; the open to run is only inside `why`. The NotFoundError from noRecord carries no Remedy (internal/cairn/cairn.go:211, cmd/nova-cairn/main.go:250). | Set Remedy to the open command noRecord already builds. | S |
| 5 | internal/cairn/cairn.go:213 | The open remedy for a missing session names `--publish manual`, a policy the caller never chose, in a tool that says "refusing to guess". | Print `--publish <never\|manual\|deferred\|immediate>`, or say in the line that manual is a placeholder. | S |
| 6 | cmd/nova-cairn/main.go:271 | A matching re-open prints `OPEN OK ... stamp=<now>` (or the `--now` given), not the stamp the session was opened with, and no field says it was already open. | Print the stored stamp and `existing=true`, as append prints `duplicate=true`. | S |
| 7 | `nova-cairn append ... --text x --file w.txt`, `index --store ./nope`, `append ... --text ""` | Value and state refusals end `run: nova-cairn help`; flag refusals end `run: nova-cairn <verb> -h`. Two remedies for one kind of mistake, and the general one is the less useful. | End every verb's refusal with `run: nova-cairn <verb> -h`. | S |
| 8 | `nova-cairn open --store ./cairns --session z --publish manual --bogus 1 --json` | Prose, not JSON: parsing stops at the unknown flag before `--json` is read. Carried from 1.1.0. | Scan argv for `--json` before parsing, so every refusal honours it. | S |
| 9 | cmd/nova-cairn/main.go:212 | `--now` says "RFC 3339 UTC" and accepts `2026-10-06T10:00:00+02:00`, filing `08:00:00Z`. | Refuse a non-Z offset, or say "converted to UTC" in the flag text. | S |
| 10 | `nova-cairn append ... --now 2020-01-01T00:00:00Z` | A real append files a backdated stamp; the entry, the index row and the receipt carry nothing that says the stamp was given, not read from the clock. | Record `replayed=true` on an entry stamped by `--now`, and print it. | S |
| 11 | `nova-cairn version -h`, `index -h`, `receipt -h` | Every verb's `-h` prints all four verbs' exit codes; `version -h` explains append conflicts. | Print the verb's own exit-code line in its `-h`. | S |
| 12 | `nova-cairn append -h` | Says nothing of the empty-text refusal (`empty note stores nothing`) or that a flat record trims trailing newlines, so `--text $'two\n'` after `two\n\n` is `duplicate=true`. The spec says both. | Add both rules to `append -h`. | S |
| 13 | README.md:54 | "These are the Nova Tools 1.0.0 commands" and `@v1.0.0` installs, at a 1.2.0 candidate. Carried from 1.1.0. | Name the version the tree ships. | S |
| 14 | docs/CLI.md:2463 | The nova-cairn section has no `### First run` subsection. Carried from 1.1.0. | Add the help's four examples under `### First run`. | S |
| 15 | `nova-cairn help nope` | The verb list in refusals is `open, append, index, receipt, version`; `help` is a verb too. | List `help` with the verbs. | S |

## Good, keep

The banner's five facts are the ones a caller needs, and every one held under use. Same words is `duplicate=true` with the first stamp and other words is exit 1 naming the `receipt --text` that reads the holder, and that now holds under concurrency: the nested append writes with `atomicfile.NoReplace()` (internal/cairn/cairn.go:694). `receipt --text` gives back multi-line, non-ASCII words exact and quoted. Every problem in a call is named in one run. `--dry-run` writes nothing and says `persisted=false`. `persisted=true published=false` keeps local and remote apart on every line. The open remedy is quoted for a shell (`--store './my store'`). Flat records are read in their own shape and nothing is migrated.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| same-id concurrent appends can overwrite (READ 1.1.0 #1) | FIXED | twenty concurrent appends over eleven ids in one session: eleven entry files, nine exit 1 conflicts; internal/cairn/cairn.go:694 writes with `NoReplace()` |
| the hand-written flat entry heading is not shown (USE 1.1.0 #1) | STILL THERE | `## e1` in flat/s9.md: `INDEX OK sessions=1 entries=0`; `append -h` names no heading grammar |
| `--json` after a stray argument is lost (USE 1.1.0 #2) | CHANGED | `--json` before a stray positional now gives JSON; after an unknown flag it still prints prose |
| a NOTE line in the usage reads like a verb (USE 1.1.0 #3) | STILL THERE | cmd/nova-cairn/main.go:104 |
| no `### First run` in docs/CLI.md (READ 1.1.0 #2) | STILL THERE | docs/CLI.md:2463 |
| the README names 1.0.0 (READ 1.1.0 #8) | STILL THERE | README.md:54 |
| the flat writer trims trailing newlines unstated (READ 1.1.0 #4) | CHANGED | docs/SPEC-CAIRN.md now says the flat body is whitespace-trimmed; `append -h` still does not |
| both store shapes in one 870-line file (READ 1.1.0 #3) | WORSE | internal/cairn/cairn.go is 915 lines and still holds both writers |
| a re-open looks like a first open (USE 1.1.0, earlier) | CHANGED | a re-open naming another policy is exit 1; a matching re-open still prints `OPEN OK` with a fresh stamp |
